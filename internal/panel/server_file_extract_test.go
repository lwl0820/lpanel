package panel

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestServerFilesExtract(t *testing.T) {
	request := serverFileTestRequest(t)
	root := t.TempDir()
	archive := filepath.Join(root, "备份.zip")
	target := filepath.Join(root, "解压")
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)

	add := func(name, content string, mode os.FileMode) {
		t.Helper()
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetMode(mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	save := func() {
		t.Helper()
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archive, buffer.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	extract := func(dir string, status int) {
		t.Helper()
		request("POST", "/extract", fmt.Sprintf(`{"path":%q,"directory":%q}`, archive, dir), status)
	}
	check := func(path, content string) {
		t.Helper()
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != content {
			t.Fatalf("%s 内容为 %q，错误为 %v", path, actual, err)
		}
	}

	add("./", "", os.ModeDir|0755)
	add("./文件夹/子 目录/脚本.sh", "echo ok", os.ModeSetuid|0710)
	add("./文件夹/子 目录/私有文件", "私人内容", 0600)
	add("./文件夹/", "", os.ModeDir|os.ModeSetgid|os.ModeSticky|0700)
	add("./文件夹/无访问/", "", os.ModeDir)
	add("./文件夹/无访问/内容", "供清理验证", 0600)
	add("空目录/", "", os.ModeDir|0700)
	ordinary, err := writer.Create("同名文件")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ordinary.Write([]byte("新内容")); err != nil {
		t.Fatal(err)
	}
	save()

	extract(filepath.Join(target, "新目录"), 200)
	nested := filepath.Join(target, "新目录")
	script := filepath.Join(nested, "文件夹", "子 目录", "脚本.sh")
	check(script, "echo ok")
	check(filepath.Join(nested, "同名文件"), "新内容")
	for path, want := range map[string]os.FileMode{
		script:                       0710,
		filepath.Join(nested, "文件夹"): os.ModeDir | 0700,
		filepath.Join(nested, "文件夹", "子 目录", "私有文件"): 0600,
		filepath.Join(nested, "文件夹", "无访问"):          os.ModeDir,
	} {
		info, err := os.Stat(path)
		if err != nil || info.Mode() != want {
			t.Fatalf("%s 权限为 %v，期望 %v，错误为 %v", path, info, want, err)
		}
	}
	t.Cleanup(func() {
		if err := os.Chmod(filepath.Join(nested, "文件夹", "无访问"), 0700); err != nil {
			t.Error(err)
		}
	})
	if info, err := os.Stat(filepath.Join(nested, "空目录")); err != nil || !info.IsDir() {
		t.Fatalf("空目录未保留：%v，%v", info, err)
	}
	existing := filepath.Join(target, "同名文件")
	if err := os.WriteFile(existing, []byte("原文件"), 0600); err != nil {
		t.Fatal(err)
	}
	extract(target, 409)
	check(existing, "原文件")
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 2 {
		t.Fatalf("同名后留下了半成品：%v，%v", entries, err)
	}

	outside := filepath.Join(root, "外部文件")
	if err := os.WriteFile(outside, []byte("保持原样"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../外部文件", "/外部文件", "目录/../../外部文件"} {
		buffer.Reset()
		writer = zip.NewWriter(&buffer)
		add("先解出的文件", "完整", 0644)
		add(name, "更改", 0644)
		save()
		extract(target, 400)
		check(outside, "保持原样")
	}
	buffer.Reset()
	writer = zip.NewWriter(&buffer)
	add("链接", root, os.ModeSymlink|0777)
	add("链接/外部文件", "更改", 0644)
	save()
	extract(target, 400)
	check(outside, "保持原样")

	buffer.Reset()
	writer = zip.NewWriter(&buffer)
	add("校验失败", "测试损坏的文件内容", 0644)
	save()
	begin := bytes.Index(buffer.Bytes(), []byte("测试损坏的文件内容"))
	if begin < 0 {
		t.Fatal("无法找到用于损坏测试的 ZIP 文件内容")
	}
	buffer.Bytes()[begin] ^= 0xff
	if err := os.WriteFile(archive, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	extract(filepath.Join(target, "不应创建"), 400)
	entries, err = os.ReadDir(target)
	if err != nil || len(entries) != 2 {
		t.Fatalf("无效压缩包在目标中留下了条目：%v，%v", entries, err)
	}
}

func TestServerFilesExtractProjectZIP(t *testing.T) {
	request := serverFileTestRequest(t)
	root := t.TempDir()
	projectDir := filepath.Join(root, "项目")
	if err := os.Mkdir(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "compose.yaml"), []byte("services: {}"), 0644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "项目.zip")
	var buffer bytes.Buffer
	if err := writeProjectArchive(context.Background(), &buffer, Project{Name: "示例", Directory: projectDir}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "解压")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	request("POST", "/extract", fmt.Sprintf(`{"path":%q,"directory":%q}`, archive, target), 200)
	if data, err := os.ReadFile(filepath.Join(target, "files", "compose.yaml")); err != nil || string(data) != "services: {}" {
		t.Fatalf("项目文件未按原 ZIP 路径解压：%q，%v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "project.json")); err != nil || !bytes.Contains(data, []byte("示例")) {
		t.Fatalf("项目清单未作为普通文件解压：%q，%v", data, err)
	}
}
