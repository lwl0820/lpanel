package panel

import (
	json "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"golang.org/x/sys/unix"
)

const testServerFilesAPI = "/api/v1/server/files"

func serverFileTestRequest(t *testing.T) func(string, string, string, int, ...string) *httptest.ResponseRecorder {
	t.Helper()
	app, _ := testApp(t)
	anonymous := call(app, "GET", testServerFilesAPI+"/list?path=/", "", nil)
	if anonymous.Code != 401 {
		t.Fatalf("未登录的目录请求返回 %d", anonymous.Code)
	}
	login := call(app, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookies := login.Result().Cookies()
	if login.Code != 200 || len(cookies) != 1 {
		t.Fatalf("登录失败：%s", login.Body.String())
	}
	return func(method, path, body string, status int, contentTypes ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, testServerFilesAPI+path, strings.NewReader(body))
		req.AddCookie(cookies[0])
		if len(contentTypes) > 0 {
			req.Header.Set("Content-Type", contentTypes[0])
		} else if method == "POST" {
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("%s %s 返回 %d，预期 %d：%.200s", method, path, response.Code, status, response.Body.String())
		}
		if status == 200 && method != "GET" && !strings.Contains(response.Body.String(), `"ok":true`) {
			t.Fatalf("写操作缺少成功结果：%s", response.Body.String())
		}
		return response
	}
}

func TestServerFilesLifecycle(t *testing.T) {
	request := serverFileTestRequest(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "样本")
	path := filepath.Join(dir, "原始 文件.txt")
	newPath := filepath.Join(dir, "修改后.txt")
	dirBody, err := json.Marshal(map[string]string{"path": dir})
	if err != nil {
		t.Fatal(err)
	}
	request("POST", "/mkdir", string(dirBody), 415, "")
	request("POST", "/mkdir", string(dirBody), 415, "text/plain")
	request("POST", "/mkdir", string(dirBody), 200, "application/json; charset=UTF-8")
	query := "?path=" + url.QueryEscape(path)
	request("PUT", "/upload"+query, "原始内容\n", 200)
	request("PUT", "/upload"+query, "不能覆盖", 409)

	list := request("GET", "/list?path="+url.QueryEscape(dir+"//./"), "", 200)
	var result struct {
		Path    string            `json:"path"`
		Entries []serverFileEntry `json:"entries"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Path != dir || len(result.Entries) != 1 {
		t.Fatalf("目录路径或条目错误：%+v", result)
	}
	entry := result.Entries[0]
	if entry.Name != filepath.Base(path) || entry.Kind != "file" || entry.Path != path || entry.Size != int64(len("原始内容\n")) {
		t.Fatalf("文件条目错误：%+v", entry)
	}
	if _, err := time.Parse(time.RFC3339, entry.ModifiedAt); err != nil {
		t.Fatal(err)
	}
	down := request("GET", "/download"+query, "", 200)
	disposition, params, err := mime.ParseMediaType(down.Header().Get("Content-Disposition"))
	if err != nil || disposition != "attachment" || params["filename"] != filepath.Base(path) || down.Body.String() != "原始内容\n" {
		t.Fatalf("下载的文件名或内容错误：%s，%q", down.Header().Get("Content-Disposition"), down.Body.String())
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	request("PUT", "/text"+query, `{"text":"新内容"}`, 200)
	if err := os.WriteFile(newPath, []byte("不能覆盖"), 0600); err != nil {
		t.Fatal(err)
	}
	renameBody, err := json.Marshal(map[string]string{"path": path, "name": filepath.Base(newPath)})
	if err != nil {
		t.Fatal(err)
	}
	request("POST", "/rename", string(renameBody), 409)
	if contents, err := os.ReadFile(newPath); err != nil || string(contents) != "不能覆盖" {
		t.Fatalf("目标文件被覆盖：%q，%v", contents, err)
	}
	request("DELETE", "?path="+url.QueryEscape(newPath), "", 200)
	request("POST", "/rename", string(renameBody), 415, "text/plain; charset=UTF-8")
	request("POST", "/rename", string(renameBody), 200)
	if contents, err := os.ReadFile(newPath); err != nil || string(contents) != "新内容" {
		t.Fatalf("文件编辑或重命名未生效：%q，%v", contents, err)
	}
	if info, err := os.Stat(newPath); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("未保留权限：%v，%v", info, err)
	}
	request("GET", "/download"+query, "", 404)
	request("DELETE", "?path="+url.QueryEscape(newPath), "", 200)
	if got := request("GET", "/list?path="+url.QueryEscape(dir), "", 200).Body.String(); !strings.Contains(got, `"entries":[]`) {
		t.Fatalf("删除后目录仍有文件：%s", got)
	}
	request("DELETE", "?path="+url.QueryEscape(dir), "", 200)
}

func TestServerFilesDeleteNonEmptyDirectory(t *testing.T) {
	request := serverFileTestRequest(t)
	request("DELETE", "?path=%2F", "", 400)
	parent := t.TempDir()
	directory := filepath.Join(parent, "待删除")
	nested := filepath.Join(directory, "子目录")
	if err := os.MkdirAll(nested, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "文件"), []byte("目录内"), 0600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(parent, "保留")
	if err := os.Mkdir(external, 0750); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(external, "文件")
	if err := os.WriteFile(externalFile, []byte("目录外"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(nested, "目录外的链接")); err != nil {
		t.Fatal(err)
	}
	request("DELETE", "?path="+url.QueryEscape(directory), "", 200)
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("非空目录未删除：%v", err)
	}
	if contents, err := os.ReadFile(externalFile); err != nil || string(contents) != "目录外" {
		t.Fatalf("目录外的文件被删除或修改：%q，%v", contents, err)
	}
}

func TestServerFilesDeleteDirectoryPartialFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("此用例需要普通用户权限以复现删除失败")
	}
	request := serverFileTestRequest(t)
	dir := t.TempDir()
	locked := filepath.Join(dir, "只读目录")
	if err := os.Mkdir(locked, 0700); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(locked, "保留")
	removed := filepath.Join(dir, "可删除")
	for _, path := range []string{retained, removed} {
		if err := os.WriteFile(path, []byte("内容"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(locked, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
	response := request("DELETE", "?path="+url.QueryEscape(dir), "", 500)
	if !strings.Contains(response.Body.String(), "directory_delete_incomplete") {
		t.Fatalf("未提示目录可能已部分删除：%s", response.Body.String())
	}
	if _, err := os.Stat(retained); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("预期可删除文件已被删除：%v", err)
	}
}

func TestServerFilesNonUTF8Names(t *testing.T) {
	request := serverFileTestRequest(t)
	dir := t.TempDir()
	raw := filepath.Join(dir, "样本\xff")
	visible := filepath.Join(dir, "样本\uFFFD")
	if err := os.WriteFile(raw, []byte("不可表示的名称"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visible, []byte("可见名称"), 0600); err != nil {
		t.Fatal(err)
	}
	list := request("GET", "/list?path="+url.QueryEscape(dir), "", 200)
	var result struct {
		Entries      []serverFileEntry `json:"entries"`
		SkippedNames int               `json:"skipped_names"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SkippedNames != 1 || len(result.Entries) != 1 || result.Entries[0].Path != visible || result.Entries[0].Name != filepath.Base(visible) {
		t.Fatalf("列表未区分名称碰撞：%+v", result)
	}
	request("DELETE", "?path="+url.QueryEscape(raw), "", 400)
	// JSON 的非法原始字节不能被修复成另一个已存在的文件名。
	renameBody, err := json.Marshal(map[string]string{"path": visible, "name": "另一个文件"})
	if err != nil {
		t.Fatal(err)
	}
	request("POST", "/rename", strings.Replace(string(renameBody), "\uFFFD", "\xff", 1), 400)
	query := "?path=" + url.QueryEscape(result.Entries[0].Path)
	if got := request("GET", "/download"+query, "", 200).Body.String(); got != "可见名称" {
		t.Fatalf("列表指向了错误的文件：%s", got)
	}
	request("DELETE", query, "", 200)
	if got, err := os.ReadFile(raw); err != nil || string(got) != "不可表示的名称" {
		t.Fatalf("被跳过的文件发生改动：%q，%v", got, err)
	}
}

func TestServerFilesInterruptedDownload(t *testing.T) {
	prefix := "已经发送的内容"
	reader := io.MultiReader(strings.NewReader(prefix), iotest.ErrReader(io.ErrUnexpectedEOF))
	w := httptest.NewRecorder()
	defer func() {
		if failure := recover(); failure != http.ErrAbortHandler || w.Body.String() != prefix {
			t.Fatalf("下载失败时应中断传输且不能追加错误响应，实际：%v，%q", failure, w.Body.String())
		}
	}()
	sendServerFileDownload(w, httptest.NewRequest("GET", "/", nil), "样本", reader)
}

type failingServerTextFile struct {
	*os.File
	step string
}

func (f failingServerTextFile) WriteAt(data []byte, offset int64) (int, error) {
	if f.step == "写入" {
		n, err := f.File.WriteAt(data[:1], offset)
		if err != nil {
			return n, err
		}
		return n, unix.ENOSPC
	}
	return f.File.WriteAt(data, offset)
}

func (f failingServerTextFile) Truncate(size int64) error {
	if f.step == "截断" {
		return unix.EIO
	}
	return f.File.Truncate(size)
}

func (f failingServerTextFile) Close() error {
	err := f.File.Close()
	if err == nil && f.step == "关闭" {
		return unix.EIO
	}
	return err
}

func TestServerFilesIncompleteSave(t *testing.T) {
	for _, step := range []string{"写入", "截断", "关闭"} {
		t.Run(step, func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "样本"))
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			saveServerFileText(w, failingServerTextFile{file, step}, "新内容")
			var result struct {
				Error APIError `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 500 || result.Error.Code != "text_save_incomplete" || !strings.Contains(result.Error.Message, "核对原文件") {
				t.Fatalf("%s失败后缺少核对原文件的提示：%d，%s", step, w.Code, w.Body.String())
			}
		})
	}
}

func TestServerFilesLinksAndSpecialFiles(t *testing.T) {
	request := serverFileTestRequest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "文件")
	link := filepath.Join(dir, "链接")
	pipe := filepath.Join(dir, "管道")
	if err := os.WriteFile(path, []byte("原始内容"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	request("DELETE", "", "", 400)
	request("PUT", "/upload?path=%2F.%2F", "", 400)
	request("GET", "/list?path=relative", "", 400)
	query := "?path=" + url.QueryEscape(link)
	request("GET", "/download"+query, "", 400)
	request("GET", "/text"+query, "", 400)
	request("PUT", "/text"+query, `{"text":"不能修改目标"}`, 400)
	request("PUT", "/upload"+query, "不能覆盖链接", 409)
	request("GET", "/download?path="+url.QueryEscape(pipe), "", 400)
	pipeBody, err := json.Marshal(map[string]string{"path": pipe, "name": "不能重命名的管道"})
	if err != nil {
		t.Fatal(err)
	}
	request("POST", "/rename", string(pipeBody), 400)
	request("DELETE", "?path="+url.QueryEscape(pipe), "", 400)
	var result struct {
		Entries []serverFileEntry `json:"entries"`
	}
	list := request("GET", "/list?path="+url.QueryEscape(dir), "", 200)
	if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	kinds := make(map[string]string)
	for _, entry := range result.Entries {
		kinds[entry.Path] = entry.Kind
	}
	if kinds[link] != "link" || kinds[pipe] != "other" {
		t.Fatalf("链接或特殊文件被识别为普通文件：%v", kinds)
	}
	request("DELETE", query, "", 200)
	request("GET", "/download"+query, "", 404)
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "原始内容" {
		t.Fatalf("链接目标被删除或修改：%q，%v", contents, err)
	}
}

func TestServerFilesTextLimits(t *testing.T) {
	request := serverFileTestRequest(t)
	path := filepath.Join(t.TempDir(), "text.txt")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	endpoint := "/text?path=" + url.QueryEscape(path)
	request("PUT", endpoint, `{"text":"`+strings.Repeat(`\u0061`, 1<<20)+`"}`, 200)
	for _, test := range []struct{ code, body string }{
		{"text_too_large", `{"text":"` + strings.Repeat("é", 1<<19) + `x"}`},
		{"request_too_large", `{"text":"` + strings.Repeat(`\u0061`, 1280<<10) + `"}`},
	} {
		response := request("PUT", endpoint, test.body, 413)
		var result struct {
			Error APIError `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Error.Code != test.code {
			t.Fatalf("预期错误码 %q，实际响应：%s", test.code, response.Body.String())
		}
	}
	request("PUT", endpoint, `{"text":"a\u0000b"}`, 400)
	response := request("GET", endpoint, "", 200)
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result["text"] != strings.Repeat("a", 1<<20) {
		t.Fatalf("读回的文本与保存的 1 MiB 样本不一致：%v", err)
	}
	for _, binary := range []string{"a\x00b", "\xff"} {
		if err := os.WriteFile(path, []byte(binary), 0600); err != nil {
			t.Fatal(err)
		}
		request("GET", endpoint, "", 400)
		request("PUT", endpoint, `{"text":"不能覆盖二进制文件"}`, 400)
		if contents, err := os.ReadFile(path); err != nil || string(contents) != binary {
			t.Fatalf("二进制文件被覆盖：%q，%v", contents, err)
		}
	}
}
