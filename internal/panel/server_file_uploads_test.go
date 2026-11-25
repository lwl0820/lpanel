package panel

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestServerFilesChunkedUpload(t *testing.T) {
	app, _ := testApp(t)
	const route = testServerFilesAPI + "/uploads"
	if response := call(app, http.MethodPost, route, "", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("未登录创建上传的状态为 %d", response.Code)
	}
	login := call(app, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	dir := t.TempDir()

	request := func(method, endpoint string, body io.Reader, status int) []byte {
		t.Helper()
		req := httptest.NewRequest(method, route+endpoint, body)
		req.AddCookie(cookie)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("%s %s 返回 %d，预期 %d：%s", method, endpoint, response.Code, status, response.Body)
		}
		return response.Body.Bytes()
	}
	start := func(path string, size int) string {
		t.Helper()
		body, err := json.Marshal(struct {
			Path string `json:"path"`
			Size int    `json:"size"`
		}{path, size})
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			ID        string `json:"id"`
			ChunkSize int    `json:"chunk_size"`
		}
		response := request(http.MethodPost, "", bytes.NewReader(body), http.StatusCreated)
		if err := json.Unmarshal(response, &result); err != nil || result.ID == "" || result.ChunkSize != serverFileChunkSize {
			t.Fatalf("上传无法开始：%s，%v", response, err)
		}
		return "/" + result.ID
	}

	content := bytes.Repeat([]byte("上传内容 "), 100_000)
	path := filepath.Join(dir, "含空格 #.bin")
	upload := start(path, len(content))
	request(http.MethodPost, upload+"/complete", nil, http.StatusConflict)
	request(http.MethodPut, upload+"?offset=1", strings.NewReader("错误偏移"), http.StatusConflict)
	for offset := 0; offset < len(content); {
		end := min(offset+serverFileChunkSize, len(content))
		response := request(http.MethodPut, fmt.Sprintf("%s?offset=%d", upload, offset), bytes.NewReader(content[offset:end]), http.StatusOK)
		var progress struct {
			Received int `json:"received"`
		}
		if err := json.Unmarshal(response, &progress); err != nil || progress.Received != end {
			t.Fatalf("接收进度错误：%s，%v", response, err)
		}
		offset = end
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("上传完成前目标应不存在：%v", err)
	}
	request(http.MethodPost, upload+"/complete", nil, http.StatusOK)
	if result, err := os.ReadFile(path); err != nil || !bytes.Equal(result, content) {
		t.Fatalf("最终文件大小为 %d，读取错误为 %v", len(result), err)
	}

	legacyPath := filepath.Join(t.TempDir(), "整文件上传")
	if err := os.WriteFile(legacyPath, nil, 0666); err != nil {
		t.Fatal(err)
	}
	legacyInfo, err := os.Stat(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != legacyInfo.Mode().Perm() {
		t.Fatalf("分片上传的文件权限与整文件上传不一致：%v，%v", info, err)
	}

	conflictPath := filepath.Join(dir, "同名空文件")
	upload = start(conflictPath, 0)
	if err := os.WriteFile(conflictPath, []byte("保留原文件"), 0600); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, upload+"/complete", nil, http.StatusConflict)
	if result, err := os.ReadFile(conflictPath); err != nil || string(result) != "保留原文件" {
		t.Fatalf("冲突时未保留原文件：%q，%v", result, err)
	}

	emptyPath := filepath.Join(dir, "空文件")
	upload = start(emptyPath, 0)
	request(http.MethodPost, upload+"/complete", nil, http.StatusOK)
	if result, err := os.ReadFile(emptyPath); err != nil || len(result) != 0 {
		t.Fatalf("零字节文件无效：%q，%v", result, err)
	}

	upload = start(filepath.Join(dir, "中断文件"), 20)
	body := io.MultiReader(strings.NewReader("已传输"), iotest.ErrReader(io.ErrUnexpectedEOF))
	request(http.MethodPut, upload+"?offset=0", body, http.StatusBadRequest)
	request(http.MethodPost, upload+"/complete", nil, http.StatusNotFound)

	upload = start(filepath.Join(dir, "分片过大"), len(content))
	request(http.MethodPut, upload+"?offset=0", bytes.NewReader(content[:serverFileChunkSize+1]), http.StatusRequestEntityTooLarge)

	upload = start(filepath.Join(dir, "放弃文件"), 20)
	request(http.MethodPut, upload+"?offset=0", strings.NewReader("部分"), http.StatusOK)
	request(http.MethodDelete, upload, nil, http.StatusOK)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("上传结束后目录中有残留：%v，%v", entries, err)
	}
}
