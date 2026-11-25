package panel

import (
	"archive/zip"
	json "encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestServerFilesCompress(t *testing.T) {
	request := serverFileTestRequest(t)
	dir := t.TempDir()
	tree := filepath.Join(dir, "目录")
	if err := os.MkdirAll(filepath.Join(tree, "空目录"), 0750); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"顶层.txt": "顶层内容", "目录/文件.txt": "目录内容"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	payload := func(output string, names ...string) string {
		t.Helper()
		body, err := json.Marshal(map[string]any{"directory": dir, "names": names, "name": output})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	request("POST", "/compress", payload("归档.ZIP", "顶层.txt", "目录"), 200)
	archive, err := zip.OpenReader(filepath.Join(dir, "归档.ZIP"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	expected := map[string]string{"顶层.txt": "顶层内容", "目录/": "", "目录/空目录/": "", "目录/文件.txt": "目录内容"}
	for _, entry := range archive.File {
		contents, found := expected[entry.Name]
		if !found {
			t.Fatalf("压缩包包含未选中的条目：%s", entry.Name)
		}
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(file)
		if err := errors.Join(readErr, file.Close()); err != nil || string(data) != contents {
			t.Fatalf("条目 %s 内容错误：%q，%v", entry.Name, data, err)
		}
		delete(expected, entry.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("压缩包缺少条目：%v", expected)
	}

	conflict := filepath.Join(dir, "已有.zip")
	if err := os.WriteFile(conflict, []byte("旧文件"), 0600); err != nil {
		t.Fatal(err)
	}
	request("POST", "/compress", payload("已有.zip", "目录"), 409)
	if data, err := os.ReadFile(conflict); err != nil || string(data) != "旧文件" {
		t.Fatalf("同名目标发生变化：%q，%v", data, err)
	}
	request("POST", "/compress", payload("无后缀", "目录"), 400)
	request("POST", "/compress", payload("失败.zip", "../目录"), 400)
	if err := os.Symlink("../顶层.txt", filepath.Join(tree, "链接")); err != nil {
		t.Fatal(err)
	}
	request("POST", "/compress", payload("失败.zip", "顶层.txt", "目录"), 400)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 4 {
		t.Fatalf("压缩失败后出现结果或暂存文件：%v，%v", entries, err)
	}
}
