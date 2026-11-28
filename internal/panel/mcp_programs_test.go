package panel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"encoding/json/v2"
)

func TestMCPProgramTools(t *testing.T) {
	a, root := testApp(t)
	a.programSupervisorDir = filepath.Join(root, "supervisor")
	if err := os.Mkdir(a.programSupervisorDir, 0700); err != nil {
		t.Fatal(err)
	}
	ready, running, failStart := false, false, false
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "supervisorctl" || len(args) < 3 {
			t.Fatalf("意外的命令：%s %q", name, args)
		}
		switch args[2] {
		case "pid":
			if !ready {
				return nil, errors.New("Supervisor 服务未就绪")
			}
			return []byte("100"), nil
		case "status":
			if len(args) == 3 {
				return nil, nil
			}
			if running {
				return fmt.Appendf(nil, "%s RUNNING pid %d, uptime 0:00:01", args[3], os.Getpid()), nil
			}
			return []byte(args[3] + " STOPPED Not started"), nil
		case "update":
			return nil, nil
		case "start", "restart":
			if failStart {
				return []byte("启动失败测试错误"), errors.New("启动失败")
			}
			running = true
			return nil, nil
		case "stop":
			running = false
			return nil, nil
		default:
			t.Fatalf("意外的 Supervisor 操作：%v", args)
			return nil, nil
		}
	}
	call := func(tool string, arguments map[string]any, output any) {
		t.Helper()
		result := callMCPTestTool(t, a, "programs_"+tool, arguments, false)
		if output != nil {
			if err := json.Unmarshal(result, output); err != nil {
				t.Fatalf("%s 结果无效：%s，%v", tool, result, err)
			}
		}
	}
	assertError := func(tool string, arguments map[string]any, expected string) {
		t.Helper()
		result := callMCPTestTool(t, a, "programs_"+tool, arguments, true)
		if !strings.Contains(string(result), expected) {
			t.Fatalf("%s 未返回预期错误 %s：%s", tool, expected, result)
		}
	}
	var list struct {
		Programs        []Program `json:"programs"`
		DependencyError string    `json:"dependency_error"`
	}
	call("list", map[string]any{}, &list)
	if !strings.Contains(list.DependencyError, "Supervisor 服务未就绪") || len(list.Programs) != 0 {
		t.Fatalf("就绪状态不正确：%+v", list)
	}
	var p Program
	call("create", map[string]any{"name": "程序"}, &p)
	args := map[string]any{"program_id": p.ID, "action": "start"}
	assertError("set_state", args, "supervisor_unavailable")
	id := map[string]any{"program_id": p.ID}
	var log map[string]string
	call("logs", id, &log)
	if value, ok := log["log"]; !ok || value != "" {
		t.Fatalf("空日志响应：%+v", log)
	}
	entry := filepath.Join(p.Directory, "entry")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("entry", filepath.Join(p.Directory, "link")); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"program_id": p.ID, "name": p.Name, "entry": "../entry", "args": "", "run_user": p.RunUser}
	assertError("update", settings, "invalid_program")
	ready = true
	settings["entry"] = "entry"
	call("update", settings, &p)
	failStart = true
	assertError("set_state", args, "启动失败测试错误")
	failStart = false
	call("set_state", args, &p)
	if p.Status != "running" || p.PID != os.Getpid() {
		t.Fatalf("启动状态不正确：%+v", p)
	}
	var runtime ProgramRuntime
	call("runtime", id, &runtime)
	if runtime.PID != os.Getpid() || runtime.Ports == nil {
		t.Fatalf("运行状态缺少主进程 PID 或监听列表：%+v", runtime)
	}
	if err := os.WriteFile(a.programLogPath(p.ID), []byte("最早一行\n"+strings.Repeat("近期日志\n", 200)), 0600); err != nil {
		t.Fatal(err)
	}
	call("logs", id, &log)
	if log["log"] != strings.Repeat("近期日志\n", 200) {
		t.Fatalf("日志未按最近 200 行截取：%+v", log)
	}
	var copy struct {
		Program      Program `json:"program"`
		SkippedLinks int     `json:"skipped_links"`
	}
	call("copy", map[string]any{"program_id": p.ID, "name": "副本"}, &copy)
	if copy.Program.Directory == p.Directory || copy.Program.Status != "stopped" || copy.SkippedLinks != 1 {
		t.Fatalf("复制结果不正确：%+v", copy)
	}
	args["action"] = "restart"
	call("set_state", args, &p)
	args["action"] = "stop"
	call("set_state", args, nil)
	call("get", id, &p)
	if p.Status != "stopped" {
		t.Fatalf("停止状态不正确：%+v", p)
	}
	call("delete", id, nil)
	assertError("get", id, "not_found")
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("默认删除后业务文件未保留：%v", err)
	}
	call("delete", map[string]any{"program_id": copy.Program.ID, "remove_files": true}, nil)
	if _, err := os.Stat(copy.Program.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("副本目录未清理：%v", err)
	}
}
