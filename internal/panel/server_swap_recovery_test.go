package panel

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestServerSwapRestoreAfterStatusReadFailure(t *testing.T) {
	a, root := testApp(t)
	path := filepath.Join(root, "swap")
	a.swapProcPath = filepath.Join(root, "swaps")
	a.swapFstabPath = filepath.Join(root, "fstab")
	fstab := path + " none swap defaults 0 0\n"
	writeSwapFixture(t, path, "")
	writeSwapFixture(t, a.swapProcPath, testSwapHeader+path+" file 1024 0 -2\n")
	writeSwapFixture(t, a.swapFstabPath, fstab)

	var commands []string
	a.Command = func(_ context.Context, command string, args ...string) ([]byte, error) {
		if !slices.Equal(args, []string{"--", path}) {
			t.Fatalf("操作目标错误：%v", args)
		}
		commands = append(commands, command)
		if command == "swapoff" {
			return nil, os.Remove(a.swapProcPath)
		}
		return nil, nil
	}
	if err := a.changeServerSwap(t.Context(), "delete", swapRequest{Path: path}); err == nil {
		t.Fatal("复查失败应报告操作失败")
	}
	if !slices.Equal(commands, []string{"swapoff", "swapon"}) {
		t.Fatalf("未恢复原有启用状态：%v", commands)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(a.swapFstabPath); err != nil || string(raw) != fstab {
		t.Fatalf("开机配置未保留：%q，%v", raw, err)
	}
}

func TestServerSwapInactiveFileAlias(t *testing.T) {
	a, root := testApp(t)
	path := filepath.Join(root, "swap")
	alias := filepath.Join(root, "alias")
	a.swapProcPath = filepath.Join(root, "swaps")
	a.swapFstabPath = filepath.Join(root, "fstab")
	writeSwapFixture(t, a.swapProcPath, testSwapHeader)
	writeSwapFixture(t, a.swapFstabPath, alias+" none swap defaults 0 0\n")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	writeSwapFixture(t, path, "普通文件")
	check := func(want string) {
		t.Helper()
		state, err := a.readServerSwap(t.Context())
		if err != nil || len(state.Entries) != 1 || state.Entries[0].Type != want {
			t.Fatalf("期望类型 %s，实际 %+v，%v", want, state, err)
		}
		if want == "file" && state.Entries[0].Path != path {
			t.Fatalf("真实路径错误：%s", state.Entries[0].Path)
		}
	}
	check("unknown")
	data := make([]byte, os.Getpagesize())
	copy(data[len(data)-10:], "SWAPSPACE2")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	check("file")
	if err := os.Link(path, path+"-hard"); err != nil {
		t.Fatal(err)
	}
	check("unknown")
}
