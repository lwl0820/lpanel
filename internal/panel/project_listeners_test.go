package panel

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestProjectHostListenersMatchContainerProcesses(t *testing.T) {
	proc := t.TempDir()
	for _, socket := range []struct{ pid, fd, inode string }{
		{"10", "4", "100"}, // 容器主进程持有的非监听套接字。
		{"11", "5", "101"}, // 同一容器的子进程。
		{"11", "6", "102"},
		{"11", "7", "103"},
		{"11", "8", "106"},
		{"20", "4", "104"}, // 同一网络中的其他项目。
		{"30", "4", "105"}, // 宿主机进程。
	} {
		dir := filepath.Join(proc, socket.pid, "fd")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("socket:["+socket.inode+"]", filepath.Join(dir, socket.fd)); err != nil {
			t.Fatal(err)
		}
	}

	row := func(ip string, port int, state, inode string) string {
		raw := netip.MustParseAddr(ip).AsSlice()
		for offset := 0; offset < len(raw); offset += 4 {
			binary.NativeEndian.PutUint32(raw[offset:], binary.BigEndian.Uint32(raw[offset:]))
		}
		return fmt.Sprintf("0: %s:%04X 00000000:0000 %s 00000000:00000000 00:00000000 00000000 0 0 %s\n",
			hex.EncodeToString(raw), port, state, inode)
	}
	netDir := filepath.Join(proc, "net")
	if err := os.Mkdir(netDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netDir, "tcp"), []byte(
		row("127.0.0.1", 8081, "01", "100")+
			row("0.0.0.0", 8080, "0A", "101")+
			row("127.0.0.2", 8080, "0A", "102")+
			row("127.0.0.1", 8082, "0A", "104")+
			row("127.0.0.1", 8083, "0A", "105")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netDir, "tcp6"), []byte(
		row("::", 8084, "0A", "103")+
			row("2001:db8::2", 8085, "0A", "106")), 0600); err != nil {
		t.Fatal(err)
	}

	_, connections, err := processTCPPorts(proc, "10 11")
	if err != nil || connections != 1 {
		t.Fatalf("主进程及其子进程应只有一条 TCP 连接，得到 %d, %v", connections, err)
	}
	ports, err := containerTCPListeners(proc, "web", "PID\n10\n11\n12\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []ProjectRuntimePort{
		{Source: "host", Service: "web", HostIP: "127.0.0.1", HostPort: 8080},
		{Source: "host", Service: "web", HostIP: "127.0.0.2", HostPort: 8080},
		{Source: "host", Service: "web", HostIP: "::1", HostPort: 8084},
		{Source: "host", Service: "web", HostIP: "2001:db8::2", HostPort: 8085},
	}
	if !slices.Equal(ports, want) {
		t.Fatalf("得到监听端口 %+v，期望 %+v", ports, want)
	}
}
