package panel

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	json "encoding/json/v2"
)

func TestProgramLifecycleAndCopy(t *testing.T) {
	a, root := testApp(t)
	a.programSupervisorConfig = filepath.Join(root, "supervisord.conf")
	a.programSupervisorDir = filepath.Join(root, "supervisor")
	if err := os.Mkdir(a.programSupervisorDir, 0700); err != nil {
		t.Fatal(err)
	}
	groups := map[string]string{}
	running := map[string]bool{}
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "supervisorctl" || len(args) < 3 || args[1] != a.programSupervisorConfig {
			t.Fatalf("意外的命令：%s %q", name, args)
		}
		status := func(group string) string {
			if running[group] {
				return group + "    RUNNING   pid 123, uptime 0:00:01"
			}
			return group + "    STOPPED   Not started"
		}
		switch args[2] {
		case "pid":
			return []byte("100"), nil
		case "status":
			if len(args) == 4 {
				if _, exists := groups[args[3]]; !exists {
					return []byte(args[3] + ": ERROR (no such group)"), errors.New("未知程序组")
				}
				if !running[args[3]] {
					return []byte(status(args[3])), errors.New("程序已停止")
				}
				return []byte(status(args[3])), nil
			}
			var lines []string
			for group := range groups {
				lines = append(lines, status(group))
			}
			return []byte(strings.Join(lines, "\n")), nil
		case "update":
			group := args[3]
			data, err := os.ReadFile(filepath.Join(a.programSupervisorDir, group+".conf"))
			if errors.Is(err, os.ErrNotExist) {
				delete(groups, group)
				delete(running, group)
				return nil, nil
			}
			if err != nil {
				t.Fatal(err)
			}
			if groups[group] != string(data) {
				running[group] = false
			}
			groups[group] = string(data)
			return nil, nil
		case "start", "restart", "stop":
			running[args[3]] = args[2] != "stop"
			return nil, nil
		}
		t.Fatalf("意外的 Supervisor 动作：%v", args)
		return nil, nil
	}
	cookie := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil).Result().Cookies()[0]
	const collection = "/api/v1/programs"
	if w := call(a, "GET", collection, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("程序列表无需登录：%d", w.Code)
	}
	send := func(method, path, body string, want int, output any) {
		t.Helper()
		w := call(a, method, path, body, cookie)
		if w.Code != want {
			t.Fatalf("%s %s = %d，预期 %d：%s", method, path, w.Code, want, w.Body.String())
		}
		if output != nil {
			if err := json.Unmarshal(w.Body.Bytes(), output); err != nil {
				t.Fatal(err)
			}
		}
	}

	var p Program
	send("POST", collection, `{"name":"测试程序"}`, http.StatusCreated, &p)
	path := collection + "/" + p.ID
	if p.Status != "stopped" || p.RunUser == "" || !filepath.IsAbs(p.Directory) {
		t.Fatalf("新建后的程序错误：%+v", p)
	}
	send("POST", path+"/actions/start", "", http.StatusBadRequest, nil)
	entry := filepath.Join(p.Directory, "entry")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("entry", filepath.Join(p.Directory, "link")); err != nil {
		t.Fatal(err)
	}
	input := p
	settings := func(path string, input Program, want int, output *Program) {
		t.Helper()
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if output == nil {
			send("PUT", path, string(data), want, nil)
		} else {
			send("PUT", path, string(data), want, output)
		}
	}
	input.Entry = "../other/entry"
	settings(path, input, http.StatusBadRequest, nil)
	input.Entry, input.Args = "entry", `--label "多个 字" "a ;#b" "100%"`
	settings(path, input, http.StatusOK, &p)
	if info, err := os.Stat(entry); err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("入口没有获得执行权限：%+v，%v", info, err)
	}
	send("POST", path+"/actions/start", "", http.StatusOK, &p)
	group := programSupervisorName(p.ID)
	config := groups[group]
	if p.Status != "running" || p.PID != 123 || !strings.Contains(config, "autostart=false\nautorestart=true") {
		t.Fatalf("配置或运行状态错误：%+v\n%s", p, config)
	}
	input.Args = "--new value"
	settings(path, input, http.StatusOK, &p)
	if p.Status != "running" || groups[group] != config {
		t.Fatal("保存配置影响了正在运行的实例")
	}
	send("POST", path+"/actions/restart", "", http.StatusOK, &p)
	if p.Status != "running" || !strings.Contains(groups[group], `"--new" "value"`) {
		t.Fatalf("重启后未应用新参数：%+v\n%s", p, groups[group])
	}

	var copied struct {
		Program      Program `json:"program"`
		SkippedLinks int     `json:"skipped_links"`
	}
	send("POST", path+"/copy", `{"name":"副本"}`, http.StatusCreated, &copied)
	copy := copied.Program
	if copy.Directory == p.Directory || copy.Status != "stopped" || copy.Entry != p.Entry ||
		copy.Args != p.Args || copy.RunUser != p.RunUser || copied.SkippedLinks != 1 || len(groups) != 1 {
		t.Fatalf("副本未独立保存：%+v", copied)
	}
	if data, err := os.ReadFile(filepath.Join(copy.Directory, "entry")); err != nil || string(data) != "#!/bin/sh\n" {
		t.Fatalf("副本内容不完整：%q，%v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(copy.Directory, "link")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("副本包含链接：%v", err)
	}
	send("POST", path+"/actions/stop", "", http.StatusOK, &p)
	if p.Status != "stopped" || p.PID != 0 {
		t.Fatalf("停止后状态未更新：%+v", p)
	}
	send("DELETE", path, "", http.StatusOK, nil)
	if _, err := os.Stat(entry); err != nil || len(groups) != 0 {
		t.Fatalf("删除程序未保留业务文件或未移除托管组：%v", err)
	}
	send("DELETE", collection+"/"+copy.ID+"?remove_files=true", "", http.StatusOK, nil)
	if _, err := os.Stat(copy.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("副本工作目录未清理：%v", err)
	}
}

func TestProgramDependencyUnavailable(t *testing.T) {
	a, _ := testApp(t)
	a.Command = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("supervisorctl 命令不存在")
	}
	cookie := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil).Result().Cookies()[0]
	list := call(a, "GET", "/api/v1/programs", "", cookie)
	var result struct {
		Programs        []Program `json:"programs"`
		DependencyError string    `json:"dependency_error"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &result); list.Code != 200 || err != nil ||
		!strings.Contains(result.DependencyError, "请安装并启动 Supervisor") || result.Programs == nil {
		t.Fatalf("缺少 Supervisor 时没有依赖提示：%d %s", list.Code, list.Body.String())
	}
	if w := call(a, "GET", "/api/v1/health", "", nil); w.Code != 200 {
		t.Fatalf("缺少 Supervisor 时影响面板健康检查：%d", w.Code)
	}
}

func TestProgramArguments(t *testing.T) {
	input := `--label "多个 字" 'say "hello"' "a ;#b" "100%" "" a\ b "dir\name" '%(here)s'`
	want := []string{"--label", "多个 字", `say "hello"`, "a ;#b", "100%", "", "a b", `dir\name`, "%(here)s"}
	got, err := splitProgramArgs(input)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("解析启动参数 = %q，%v，预期 %q", got, err, want)
	}
	for _, input := range []string{"--label \"未结束", "反斜杠\\", "换\n行"} {
		if _, err := splitProgramArgs(input); err == nil {
			t.Fatalf("接受了无效参数 %q", input)
		}
	}
}
