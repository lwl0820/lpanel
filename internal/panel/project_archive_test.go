package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestProjectArchiveRoundTrip(t *testing.T) {
	source, sourceRoot := testApp(t)
	target, targetRoot := testApp(t)
	login := func(a *App) *http.Cookie {
		t.Helper()
		w := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
		if w.Code != 200 {
			t.Fatalf("登录失败：%s", w.Body)
		}
		return w.Result().Cookies()[0]
	}
	sourceCookie, targetCookie := login(source), login(target)
	const projectName = "迁移测试"
	sourceDir := filepath.Join(sourceRoot, "data/projects", "legacy-project-id")
	targetDir := filepath.Join(targetRoot, "data/projects", projectName)
	files := map[string]struct {
		content []byte
		mode    os.FileMode
	}{
		"compose.yaml":   {[]byte("services:\n  web:\n    image: alpine\n    volumes: [./nested:/data]\n"), 0600},
		".env":           {[]byte("TOKEN=测试\n"), 0600},
		"nested/.hidden": {[]byte{0, 1, 0xff, 2}, 0640},
		"nested/run.sh":  {[]byte("#!/bin/sh\nexit 0\n"), 0750},
	}
	dirs := map[string]os.FileMode{".": 0751, "nested": 0750, "nested/empty": 0710}
	if err := os.MkdirAll(filepath.Join(sourceDir, "nested/empty"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, mode := range dirs {
		if err := os.Chmod(filepath.Join(sourceDir, name), mode); err != nil {
			t.Fatal(err)
		}
	}
	for name, file := range files {
		if err := os.WriteFile(filepath.Join(sourceDir, name), file.content, file.mode); err != nil {
			t.Fatal(err)
		}
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid, gid = 999, 999
	} else if groups, err := os.Getgroups(); err == nil {
		for _, group := range groups {
			if group != gid && os.Chown(filepath.Join(sourceDir, "nested"), uid, group) == nil {
				gid = group
				break
			}
		}
	}
	for _, name := range []string{"nested", "nested/.hidden"} {
		if err := os.Chown(filepath.Join(sourceDir, name), uid, gid); err != nil {
			t.Fatal(err)
		}
	}
	ports := make([]int, maxProjectCustomPort)
	for i := range ports {
		ports[i] = i + 1
	}
	if err := source.storeProject(t.Context(), &Project{
		ID: "legacy-project-id", Name: projectName, Directory: sourceDir, LegacyDirectory: true, CustomPorts: ports,
	}); err != nil {
		t.Fatal(err)
	}
	exportPath := "/api/v1/projects/legacy-project-id/export"
	if w := call(source, "GET", exportPath, "", nil); w.Code != 401 {
		t.Fatalf("匿名导出返回 %d", w.Code)
	}
	exported := call(source, "GET", exportPath, "", sourceCookie)
	if exported.Code != 200 || exported.Header().Get("Content-Type") != "application/zip" || !strings.Contains(exported.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("导出返回 %d，响应头 %v", exported.Code, exported.Header())
	}
	archive := exported.Body.Bytes()
	upload := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/projects/import", bytes.NewReader(archive))
		r.Header.Set("Content-Type", "application/zip")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		target.Handler().ServeHTTP(w, r)
		return w
	}
	if w := upload(nil); w.Code != 401 {
		t.Fatalf("匿名导入返回 %d", w.Code)
	}
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(targetDir, "existing")
	if err := os.WriteFile(sentinel, []byte("保留"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := upload(targetCookie); w.Code != 409 {
		t.Fatalf("导入覆盖了现有目录：%d %s", w.Code, w.Body)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "保留" {
		t.Fatalf("现有目录内容改变：%q %v", data, err)
	}
	if err := os.RemoveAll(targetDir); err != nil {
		t.Fatal(err)
	}

	rejectConfig := true
	failDeploy := true
	target.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "docker" || len(args) < 10 || args[4] != targetDir {
			return nil, errors.New("Compose 未使用目标项目目录")
		}
		switch args[9] {
		case "config":
			if rejectConfig {
				return nil, errors.New("校验失败")
			}
			data, err := os.ReadFile(filepath.Join(args[4], "nested/.hidden"))
			if err != nil || !bytes.Equal(data, files["nested/.hidden"].content) {
				return nil, errors.New("校验前业务文件尚未就位")
			}
			return []byte(`{"services":{}}`), nil
		case "up":
			if failDeploy {
				return nil, errors.New("部署失败")
			}
			return nil, nil
		case "ps":
			return []byte("[]"), nil
		default:
			return nil, errors.New("意外的 Compose 子命令")
		}
	}

	if w := upload(targetCookie); w.Code != 400 || !strings.Contains(w.Body.String(), "校验失败") {
		t.Fatalf("校验失败返回 %d：%s", w.Code, w.Body)
	}
	if _, err := os.Stat(targetDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("校验失败后导入目录仍存在：%v", err)
	}
	if w := call(target, "GET", "/api/v1/projects", "", targetCookie); w.Body.String() != "[]\n" {
		t.Fatalf("校验失败后仍登记项目：%s", w.Body)
	}
	rejectConfig = false
	imported := upload(targetCookie)
	var result struct {
		Project
		Operation Operation
	}
	if err := json.Unmarshal(imported.Body.Bytes(), &result); err != nil || imported.Code != 200 {
		t.Fatalf("导入失败：%d %s %v", imported.Code, imported.Body, err)
	}
	if result.ID == "" || result.ID == "legacy-project-id" || result.UpdatedAt == "" || result.LegacyDirectory ||
		result.Name != projectName || result.Directory != targetDir || result.Operation.Kind != "project_deploy" {
		t.Fatalf("导入结果错误：%+v", result)
	}
	stored, err := target.project(t.Context(), result.ID)
	if err != nil || !slices.Equal(result.CustomPorts, ports) || !slices.Equal(stored.CustomPorts, ports) {
		t.Fatalf("导入没有还原完整的自定义端口列表：%v", err)
	}
	waitProjectOperationStatus(t, target, result.Operation.ID, "failed")
	for name, want := range files {
		path := filepath.Join(targetDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, want.content) || info.Mode().Perm() != want.mode {
			t.Fatalf("文件 %s 还原错误：内容 %q，权限 %o，%v", name, data, info.Mode().Perm(), err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(targetDir, "nested/empty")); err != nil || len(entries) != 0 {
		t.Fatalf("空目录还原错误：%v %v", entries, err)
	}
	for name, mode := range dirs {
		info, err := os.Stat(filepath.Join(targetDir, name))
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("目录 %s 权限还原错误：%v %v", name, info, err)
		}
	}
	for _, name := range []string{"nested", "nested/.hidden"} {
		info, err := os.Stat(filepath.Join(targetDir, name))
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if int(stat.Uid) != uid || int(stat.Gid) != gid {
			t.Fatalf("条目 %s 所有者还原错误：%d:%d", name, stat.Uid, stat.Gid)
		}
	}
	if w := upload(targetCookie); w.Code != 409 {
		t.Fatalf("同名项目未返回冲突：%d %s", w.Code, w.Body)
	}

	failDeploy = false
	edit, err := json.Marshal(Project{Name: result.Name, Compose: result.Compose + "# 重试\n", Environment: result.Environment})
	if err != nil {
		t.Fatal(err)
	}
	w := call(target, "PUT", "/api/v1/projects/"+result.ID, string(edit), targetCookie)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Operation.ID == "" {
		t.Fatalf("重新保存失败：%d %s", w.Code, w.Body)
	}
	waitProjectOperation(t, target, result.Operation.ID)
	for _, root := range []string{sourceRoot, targetRoot} {
		entries, err := os.ReadDir(filepath.Join(root, ".tmp"))
		if err != nil || slices.ContainsFunc(entries, func(entry os.DirEntry) bool { return strings.HasPrefix(entry.Name(), "project-") }) {
			t.Fatalf("归档临时文件未清理：%v %v", entries, err)
		}
	}
}

func TestProjectArchiveImportsLegacyDisplayName(t *testing.T) {
	a, root := testApp(t)
	source := filepath.Join(root, ".tmp", "legacy")
	if err := a.writeProjectFiles(Project{Compose: "services: {}"}, source); err != nil {
		t.Fatal(err)
	}
	const name = "旧/项目"
	var archive bytes.Buffer
	if err := writeProjectArchive(t.Context(), &archive, Project{Name: name, Directory: source}); err != nil {
		t.Fatal(err)
	}
	a.Command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[9] == "config" {
			return []byte(`{"services":{}}`), nil
		}
		return nil, nil
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	r := httptest.NewRequest("POST", "/api/v1/projects/import", &archive)
	r.Header.Set("Content-Type", "application/zip")
	r.AddCookie(login.Result().Cookies()[0])
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	var result struct {
		Project
		Operation Operation
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 ||
		result.Name != name || !result.LegacyDirectory || result.Directory != a.projectDirectory(result.ID) ||
		result.CustomPorts == nil || len(result.CustomPorts) != 0 {
		t.Fatalf("旧项目恢复错误：%d %s %v", w.Code, w.Body, err)
	}
	waitProjectOperation(t, a, result.Operation.ID)
}

func TestProjectArchiveRejectsUnsupportedEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("compose.yaml", filepath.Join(dir, "config-link")); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := writeProjectArchive(t.Context(), &archive, Project{Name: "测试", Directory: dir}); err == nil || !strings.Contains(err.Error(), "config-link") {
		t.Fatalf("应提示压缩包无法保留链接：%v", err)
	}
}
