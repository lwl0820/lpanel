package panel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const testSwapHeader = "Filename\tType\tSize\tUsed\tPriority\n"

func writeSwapFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
}

func TestServerSwapAPI(t *testing.T) {
	a, root := testApp(t)
	a.swapProcPath = filepath.Join(root, "swaps")
	a.swapFstabPath = filepath.Join(root, "fstab")
	const route = "/api/v1/server/swap"
	external := filepath.Join(root, "外部 swap")
	partition := filepath.Join(root, "partition")
	alias := filepath.Join(root, "partition-alias")
	writeSwapFixture(t, external, "")
	writeSwapFixture(t, partition, "")
	if err := os.Symlink(partition, alias); err != nil {
		t.Fatal(err)
	}
	partitionLine := fmt.Sprintf("%s\tnone swap sw 0 0\n", alias)
	fstab := "# 保留注释\n" + partitionLine + "/dev/root / ext4 defaults 0 1\n"
	externalLine := fmt.Sprintf("%s\tnone swap sw 0 0\n", encodeSwapPath.Replace(external))
	writeSwapFixture(t, a.swapFstabPath, fstab+externalLine)
	partitionProc := fmt.Sprintf("%s partition 4096 8 -2\n", partition)
	externalProc := fmt.Sprintf("%s file 2048 32 -3\n", encodeSwapPath.Replace(external))
	writeSwapFixture(t, a.swapProcPath, testSwapHeader+partitionProc+externalProc)

	if got := call(a, "GET", route, "", nil).Code; got != 401 {
		t.Fatalf("未登录返回 %d", got)
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	get := func() swapState {
		t.Helper()
		response := call(a, "GET", route, "", cookie)
		var state swapState
		if err := json.Unmarshal(response.Body.Bytes(), &state); response.Code != 200 || err != nil {
			t.Fatalf("读取失败：%d，%v，%s", response.Code, err, response.Body)
		}
		return state
	}
	run := func(method, endpoint, body, status string) {
		t.Helper()
		response := call(a, method, route+endpoint, body, cookie)
		var op Operation
		if err := json.Unmarshal(response.Body.Bytes(), &op); response.Code != 202 || err != nil || op.ID == "" {
			t.Fatalf("启动失败：%d，%v，%s", response.Code, err, response.Body)
		}
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			response := call(a, "GET", "/api/v1/operations/"+op.ID, "", cookie)
			if err := json.Unmarshal(response.Body.Bytes(), &op); err != nil {
				t.Fatal(err)
			}
			if op.Status != "running" {
				if op.Status != status {
					t.Fatalf("操作结果：%+v，期望 %s", op, status)
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("等待操作超时")
	}
	deletePath := func(path string) string { return "/files?path=" + url.QueryEscape(path) }

	state := get()
	if len(state.Entries) != 2 || state.Entries[0].Type != "partition" || state.Entries[1].Type != "file" ||
		state.TotalBytes != 6144*1024 || state.UsedBytes != 40*1024 {
		t.Fatalf("交换条目或汇总错误：%+v", state)
	}
	if got := call(a, "POST", route+"/files", `{"size_mib":1048577}`, cookie).Code; got != 400 {
		t.Fatalf("超限容量返回 %d", got)
	}

	commandCalled := false
	failStop := true
	a.Command = func(_ context.Context, command string, args ...string) ([]byte, error) {
		commandCalled = true
		if command != "swapoff" || len(args) != 2 || args[1] != external {
			return nil, fmt.Errorf("意外命令：%s %v", command, args)
		}
		if failStop {
			return nil, errors.New("模拟停用失败")
		}
		return nil, os.WriteFile(a.swapProcPath, []byte(testSwapHeader+partitionProc), 0640)
	}
	run("DELETE", deletePath(partition), "", "failed")
	run("DELETE", deletePath(alias), "", "failed")
	if commandCalled {
		t.Fatal("分区或分区别名进入了写操作")
	}

	fileAlias := filepath.Join(root, "swap-alias")
	if err := os.Symlink(external, fileAlias); err != nil {
		t.Fatal(err)
	}
	aliasLine := fileAlias + " none swap sw 0 0\n"
	fstab += fileAlias + " none bind defaults 0 0\n"
	writeSwapFixture(t, a.swapFstabPath, fstab+aliasLine+externalLine)
	if state := get(); len(state.Entries) != 2 || state.Entries[1].Type != "file" || state.Entries[1].Path != external {
		t.Fatalf("交换文件别名未合并到真实路径：%+v", state)
	}
	run("DELETE", deletePath(fileAlias), "", "failed")
	if commandCalled {
		t.Fatal("接口接受了别名操作路径")
	}
	run("DELETE", deletePath(external), "", "failed")
	if _, err := os.Stat(external); err != nil {
		t.Fatal("停用失败后文件丢失", err)
	}
	if raw, err := os.ReadFile(a.swapFstabPath); err != nil || string(raw) != fstab+aliasLine+externalLine {
		t.Fatalf("停用失败后配置变更：%q，%v", raw, err)
	}
	failStop = false
	run("DELETE", deletePath(external), "", "success")
	if target, err := os.Readlink(fileAlias); err != nil || target != external {
		t.Fatalf("未保留别名链接：%q，%v", target, err)
	}
	if _, err := os.Stat(external); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("外部交换文件未删除", err)
	}
	if raw, err := os.ReadFile(a.swapFstabPath); err != nil || string(raw) != fstab {
		t.Fatalf("无关开机配置被修改：%q，%v", raw, err)
	}
	if state := get(); len(state.Entries) != 1 || state.TotalBytes != 4096*1024 || !state.Entries[0].Active {
		t.Fatalf("删除外部交换文件后分区状态错误：%+v", state)
	}
}

func TestServerSwapManagedLifecycle(t *testing.T) {
	a, root := testApp(t)
	a.swapProcPath = filepath.Join(root, "swaps")
	a.swapFstabPath = filepath.Join(root, "fstab")
	writeSwapFixture(t, a.swapProcPath, testSwapHeader)
	const originalFstab = "# 保留原有配置\n"
	writeSwapFixture(t, a.swapFstabPath, originalFstab)

	failSize := int64(0)
	a.Command = func(_ context.Context, command string, args ...string) ([]byte, error) {
		path := args[len(args)-1]
		switch command {
		case "dd":
			_, path, _ := strings.Cut(args[1], "=")
			_, count, _ := strings.Cut(args[3], "=")
			size, err := strconv.ParseInt(count, 10, 64)
			if err != nil {
				return nil, err
			}
			return nil, os.Truncate(path, size*swapMiB)
		case "mkswap":
			file, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			_, err = file.WriteAt([]byte("SWAPSPACE2"), int64(os.Getpagesize()-10))
			return nil, err
		case "swapon":
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			if info.Size() == failSize {
				return nil, errors.New("模拟启用新文件失败")
			}
			return nil, os.WriteFile(a.swapProcPath, fmt.Appendf([]byte(testSwapHeader), "%s file %d 0 -2\n", encodeSwapPath.Replace(path), info.Size()/1024), 0640)
		case "swapoff":
			return nil, os.WriteFile(a.swapProcPath, []byte(testSwapHeader), 0640)
		default:
			return nil, fmt.Errorf("意外命令：%s", command)
		}
	}

	change := func(action, path string, size int64) error {
		t.Helper()
		return a.changeServerSwap(t.Context(), action, swapRequest{Path: path, SizeMiB: size})
	}
	check := func(path string, size int64, active bool) swapEntry {
		t.Helper()
		state, err := a.readServerSwap(t.Context())
		if err != nil || len(state.Entries) != 1 {
			t.Fatalf("读取交换项失败：%+v，%v", state, err)
		}
		entry := state.Entries[0]
		if !entry.Managed || entry.Type != "file" || entry.Active != active || entry.SizeBytes == nil || *entry.SizeBytes != size ||
			(path != "" && entry.Path != path) {
			t.Fatalf("交换文件状态错误：%+v", entry)
		}
		return entry
	}
	if err := change("files", "", 1); err != nil {
		t.Fatal(err)
	}
	path := check("", swapMiB, true).Path
	old, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	failSize = 2 * swapMiB
	if err := change("resize", path, 2); err == nil {
		t.Fatal("调整后启用失败未报告")
	}
	check(path, swapMiB, true)
	if restored, err := os.Stat(path); err != nil || !os.SameFile(old, restored) {
		t.Fatalf("原交换文件未恢复：%v", err)
	}
	if err := change("disable", path, 0); err != nil {
		t.Fatal(err)
	}
	check(path, swapMiB, false)
	if raw, err := os.ReadFile(a.swapFstabPath); err != nil || string(raw) != originalFstab {
		t.Fatalf("停用后开机配置错误：%q，%v", raw, err)
	}
	if err := change("resize", path, 2); err != nil {
		t.Fatal(err)
	}
	check(path, 2*swapMiB, false)
	failSize = 0
	if err := change("enable", path, 0); err != nil {
		t.Fatal(err)
	}
	check(path, 2*swapMiB, true)
	if err := change("delete", path, 0); err != nil {
		t.Fatal(err)
	}
	if state, err := a.readServerSwap(t.Context()); err != nil || len(state.Entries) != 0 || state.TotalBytes != 0 {
		t.Fatalf("删除后仍有交换记录：%+v，%v", state, err)
	}
}

func TestServerSwapFstabAtomicReplacement(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "实际配置")
	link := filepath.Join(root, "fstab")
	const previous = "# 保留\n/swap none swap noauto,pri=10 0 0 # 备注\n"
	const updated = "# 保留\n/swap none swap pri=10 0 0 # 备注\n"
	writeSwapFixture(t, target, previous)
	if err := os.Symlink(filepath.Base(target), link); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	a := &App{swapFstabPath: link}
	if got := swapFstabWith(previous, "/swap", true); got != updated {
		t.Fatalf("启用配置错误：%q", got)
	}
	if err := a.writeSwapFstab(previous, updated); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("链接被替换：%v，%v", info, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("目标权限未保留：%v，%v", info, err)
	}
	buf := make([]byte, len(previous))
	if _, err := old.ReadAt(buf, 0); err != nil || string(buf) != previous {
		t.Fatalf("旧文件被就地改写：%q，%v", buf, err)
	}
	if raw, err := os.ReadFile(link); err != nil || string(raw) != updated {
		t.Fatalf("新配置内容错误：%q，%v", raw, err)
	}
}

func TestServerSwapInsufficientDisk(t *testing.T) {
	dir := t.TempDir()
	var space unix.Statfs_t
	if err := unix.Statfs(dir, &space); err != nil {
		t.Fatal(err)
	}
	size := int64(space.Bavail*uint64(space.Frsize)/swapMiB) + 1
	called := false
	a := &App{Command: func(context.Context, string, ...string) ([]byte, error) {
		called = true
		return nil, errors.New("磁盘不足时不应执行命令")
	}}
	_, err := a.prepareSwapFile(t.Context(), dir, size)
	if err == nil || !strings.Contains(err.Error(), "磁盘空间不足") || called {
		t.Fatalf("未提前拒绝分配：%v，命令已执行 %t", err, called)
	}
}
