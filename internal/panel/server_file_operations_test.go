package panel

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestServerFilesCopyAndMove(t *testing.T) {
	request := serverFileTestRequest(t)
	root := t.TempDir()
	source := filepath.Join(root, "源")
	parent := filepath.Join(root, "目标")
	nested := filepath.Join(source, "子目录")
	file := filepath.Join(nested, "文件")
	for _, dir := range []string{source, parent, nested} {
		if err := os.Mkdir(dir, 0750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, []byte("内容"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(nested, "硬链接")); err != nil {
		t.Fatal(err)
	}
	modifiedAt := time.Unix(1_700_000_001, 123_456_789)
	if err := os.Chtimes(file, modifiedAt, modifiedAt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../不存在", filepath.Join(nested, "链接")); err != nil {
		t.Fatal(err)
	}
	payload := func(directory string) string {
		return fmt.Sprintf(`{"path":%q,"directory":%q}`, source, directory)
	}
	request("POST", "/copy", payload(nested), 400)
	request("POST", "/move", payload(nested), 400)
	alias := filepath.Join(root, "别名")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	request("POST", "/copy", payload(filepath.Join(alias, "子目录")), 400)
	request("POST", "/copy", payload(parent), 200)
	target := filepath.Join(parent, "源")
	copiedFile := filepath.Join(target, "子目录", "文件")
	copiedLink := filepath.Join(target, "子目录", "链接")
	if data, err := os.ReadFile(copiedFile); err != nil || string(data) != "内容" {
		t.Fatalf("复制的文件内容不正确：%q，%v", data, err)
	}
	if link, err := os.Readlink(copiedLink); err != nil || link != "../不存在" {
		t.Fatalf("复制后应保留原来的链接：%q，%v", link, err)
	}
	original, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.Stat(copiedFile)
	if err != nil {
		t.Fatal(err)
	}
	wantStat := original.Sys().(*syscall.Stat_t)
	gotStat := copied.Sys().(*syscall.Stat_t)
	if copied.Mode() != original.Mode() || gotStat.Uid != wantStat.Uid || gotStat.Gid != wantStat.Gid {
		t.Fatalf("复制后权限或所有者发生变化：%v，%v", original, copied)
	}
	hardLink, err := os.Stat(filepath.Join(target, "子目录", "硬链接"))
	if err != nil || !os.SameFile(copied, hardLink) || !copied.ModTime().Equal(original.ModTime()) {
		t.Fatalf("复制后硬链接或修改时间发生变化：%v，%v", copied, err)
	}
	if err := os.WriteFile(copiedFile, []byte("已有目标"), 0640); err != nil {
		t.Fatal(err)
	}
	request("POST", "/copy", payload(parent), 409)
	request("POST", "/move", payload(parent), 409)
	if data, err := os.ReadFile(copiedFile); err != nil || string(data) != "已有目标" {
		t.Fatalf("已有目标被覆盖：%q，%v", data, err)
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(nested, "管道")
	if err := unix.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	request("POST", "/copy", payload(parent), 400)
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("复制失败后仍有暂存文件或目标：%v，%v", entries, err)
	}
	if err := os.Remove(pipe); err != nil {
		t.Fatal(err)
	}
	request("POST", "/move", payload(parent), 200)
	if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("移动后源条目仍存在：%v", err)
	}
	if data, err := os.ReadFile(copiedFile); err != nil || string(data) != "内容" {
		t.Fatalf("移动后的内容不正确：%q，%v", data, err)
	}
}

func TestServerFilesAttributes(t *testing.T) {
	request := serverFileTestRequest(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "文件")
	link := filepath.Join(dir, "链接")
	if err := os.WriteFile(path, nil, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	request("POST", "/attributes", fmt.Sprintf(`{"path":%q,"mode":"600"}`, link), 400)
	request("POST", "/attributes", fmt.Sprintf(`{"path":%q,"mode":"968"}`, path), 400)
	request("POST", "/attributes", fmt.Sprintf(`{"path":%q,"owner":""}`, path), 400)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("失败操作修改了链接所指文件：%v，%v", info, err)
	}
	request("POST", "/attributes", fmt.Sprintf(`{"path":%q,"mode":"4750"}`, path), 200)
	owner := strconv.Itoa(os.Geteuid())
	if account, err := user.LookupId(owner); err == nil {
		owner = account.Username
	}
	var result struct {
		Entries []serverFileEntry `json:"entries"`
	}
	list := request("GET", "/list?path="+url.QueryEscape(dir), "", 200)
	if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil || len(result.Entries) != 2 {
		t.Fatalf("无法读取条目属性：%s，%v", list.Body.String(), err)
	}
	for _, entry := range result.Entries {
		if entry.Path == path && (entry.Mode != "4750" || entry.Owner != owner) {
			t.Fatalf("文件权限或所有者不正确：%+v", entry)
		}
	}
	if os.Geteuid() != 0 {
		t.Log("实际变更所有者需要以 root 运行")
		return
	}
	request("POST", "/attributes", fmt.Sprintf(`{"path":%q,"owner":"65534"}`, link), 200)
	linkInfo, err := os.Lstat(link)
	if err != nil || linkInfo.Sys().(*syscall.Stat_t).Uid != 65534 {
		t.Fatalf("未修改链接本身的所有者：%v，%v", linkInfo, err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil || fileInfo.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Fatalf("链接所指文件的所有者被修改：%v，%v", fileInfo, err)
	}
	request("POST", "/attributes", fmt.Sprintf(`{"path":%q,"owner":"65534"}`, path), 200)
	fileInfo, err = os.Stat(path)
	if err != nil || fileInfo.Mode() != os.ModeSetuid|0750 || fileInfo.Sys().(*syscall.Stat_t).Uid != 65534 {
		t.Fatalf("只更改所有者时应保留全部权限：%v，%v", fileInfo, err)
	}
}
