package panel

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) projectHostListeners(ctx context.Context, item ProjectContainer) ([]ProjectRuntimePort, error) {
	mode, err := a.Command(ctx, "docker", "inspect", "--format", "{{.HostConfig.NetworkMode}}", "--", item.ID)
	if err != nil {
		return nil, fmt.Errorf("读取容器网络模式失败：%w", err)
	}
	if strings.TrimSpace(string(mode)) != "host" {
		return nil, nil
	}
	processes, err := a.Command(ctx, "docker", "top", item.ID, "-eo", "pid")
	if err != nil {
		return nil, fmt.Errorf("读取容器进程失败：%w", err)
	}
	return containerTCPListeners("/proc", item.Service, string(processes))
}

func containerTCPListeners(proc, service, processes string) ([]ProjectRuntimePort, error) {
	sockets := make(map[string]struct{})
	lines := strings.FieldsSeq(processes)
	for pid := range lines {
		if pid == "PID" {
			continue
		}
		number, err := strconv.Atoi(pid)
		if err != nil || number <= 0 {
			return nil, fmt.Errorf("无法识别容器进程编号 %q", pid)
		}
		fdDir := filepath.Join(proc, strconv.Itoa(number), "fd")
		entries, err := os.ReadDir(fdDir)
		if errors.Is(err, os.ErrNotExist) {
			continue // 进程已退出。
		}
		if err != nil {
			return nil, fmt.Errorf("读取容器进程的文件描述符失败：%w", err)
		}
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
			if errors.Is(err, os.ErrNotExist) {
				continue // 进程已关闭此描述符。
			}
			if err != nil {
				return nil, fmt.Errorf("读取容器进程的套接字失败：%w", err)
			}
			if inode, ok := strings.CutPrefix(target, "socket:["); ok {
				sockets[strings.TrimSuffix(inode, "]")] = struct{}{}
			}
		}
	}
	if len(sockets) == 0 {
		return nil, nil
	}

	var ports []ProjectRuntimePort
	// host 容器共享宿主机网络，按套接字 inode 筛出容器进程持有的监听。
	for _, table := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(proc, "net", table))
		if table == "tcp6" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("读取宿主机 TCP 监听表失败：%w", err)
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			if _, ok := sockets[fields[9]]; !ok {
				continue
			}
			addressHex, portHex, _ := strings.Cut(fields[1], ":")
			raw, err := hex.DecodeString(addressHex)
			if err != nil || (len(raw) != 4 && len(raw) != 16) {
				return nil, fmt.Errorf("无法识别 TCP 监听地址 %q", fields[1])
			}
			// procfs 按本机字节序逐个输出 32 位地址字。
			for offset := 0; offset < len(raw); offset += 4 {
				binary.BigEndian.PutUint32(raw[offset:], binary.NativeEndian.Uint32(raw[offset:]))
			}
			address, _ := netip.AddrFromSlice(raw)
			address = address.Unmap()
			if address.IsUnspecified() {
				if address.Is4() {
					address = netip.AddrFrom4([4]byte{127, 0, 0, 1})
				} else {
					address = netip.IPv6Loopback()
				}
			}
			port, err := strconv.ParseUint(portHex, 16, 16)
			if err != nil || port == 0 {
				return nil, fmt.Errorf("无法识别 TCP 监听端口 %q", fields[1])
			}
			ports = append(ports, ProjectRuntimePort{
				Source: "host", Service: service, HostIP: address.String(), HostPort: int(port),
			})
		}
	}
	return ports, nil
}
