package panel

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

const (
	serverDNSDropIn = "etc/systemd/resolved.conf.d/99-lpanel-dns.conf"
	staticDNSLimit  = 3
)

type serverDNSStatus struct {
	Servers    []string `json:"servers"`
	CanEdit    bool     `json:"can_edit"`
	CanRestore bool     `json:"can_restore"`
	Message    string   `json:"message,omitempty"`
	MaxServers int      `json:"max_servers,omitzero"`
}

type serverDNSState struct {
	serverDNSStatus
	kind          string
	staticContent []byte
	staticMode    fs.FileMode
	globalDNS     []string
	globalDomains []string
}

type serverDNSBackup struct {
	Kind          string   `json:"kind"`
	StaticContent []byte   `json:"static_content,omitempty"`
	GlobalDNS     []string `json:"global_dns,omitempty"`
	GlobalDomains []string `json:"global_domains,omitempty"`
}

func (a *App) registerServerDNS() {
	a.Mux.Handle("GET /api/v1/server/dns", a.RequireAuth(http.HandlerFunc(a.serverDNS)))
	a.Mux.Handle("PUT /api/v1/server/dns", a.RequireAuth(http.HandlerFunc(a.serverDNS)))
	a.Mux.Handle("POST /api/v1/server/dns/restore", a.RequireAuth(http.HandlerFunc(a.serverDNS)))
}

func (a *App) serverDNS(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Servers []string `json:"servers"`
	}
	if r.Method == http.MethodPut {
		if !ReadJSON(w, r, &request) {
			return
		}
		if len(request.Servers) == 0 {
			WriteError(w, 400, "invalid_dns_servers", "请至少填写一个 DNS 地址")
			return
		}
		seen := make(map[netip.Addr]bool, len(request.Servers))
		for i, value := range request.Servers {
			ip, err := netip.ParseAddr(strings.TrimSpace(value))
			if err == nil && (ip.Zone() != "" || ip.IsLinkLocalUnicast()) {
				WriteError(w, 400, "invalid_dns_servers", "该 DNS 地址需要指定网卡，请填写无需指定网卡的地址")
				return
			}
			if err != nil || ip.Is4In6() || (!ip.IsGlobalUnicast() && !ip.IsLoopback()) || isDNSStub(ip) {
				WriteError(w, 400, "invalid_dns_servers", "请输入有效的 IPv4 或 IPv6 地址")
				return
			}
			request.Servers[i] = ip.String()
			if seen[ip] {
				WriteError(w, 400, "invalid_dns_servers", "DNS 地址不能重复")
				return
			}
			seen[ip] = true
		}
	}

	a.serverDNSMu.Lock()
	defer a.serverDNSMu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()

	backupPath := filepath.Join(a.Root, "data", "server-dns.json")
	var backup serverDNSBackup
	data, err := os.ReadFile(backupPath)
	if err == nil {
		err = json.Unmarshal(data, &backup)
		if err == nil && backup.Kind != "static" && backup.Kind != "resolved" {
			err = errors.New("备份类型无效")
		}
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		WriteError(w, 500, "dns_backup_error", "无法读取原 DNS 设置的备份")
		return
	}

	state := a.readServerDNS(ctx, backup.Kind)
	restoring := r.Method == http.MethodPost
	switch {
	case r.Method == http.MethodGet:
		WriteJSON(w, 200, state.serverDNSStatus)
		return
	case restoring && !state.CanRestore:
		WriteError(w, 409, "dns_restore_unavailable", "当前没有可恢复的 DNS 设置")
		return
	case !restoring && !state.CanEdit:
		WriteError(w, 409, "dns_read_only", state.Message)
		return
	case !restoring && state.MaxServers != 0 && len(request.Servers) > state.MaxServers:
		WriteError(w, 400, "invalid_dns_servers", "当前系统最多支持 3 个 DNS 地址")
		return
	case !restoring && backup.Kind == "":
		backup = serverDNSBackup{state.kind, state.staticContent, state.globalDNS, state.globalDomains}
		data, err = json.Marshal(backup)
		if err == nil {
			err = writeServerDNSFile(backupPath, data, 0600)
		}
		if err != nil {
			WriteError(w, 500, "dns_backup_error", "无法备份原 DNS 设置")
			return
		}
	}

	var expectedStatic []byte
	if state.kind == "static" {
		if restoring {
			currentOther := bytes.TrimSuffix(replaceStaticDNS(state.staticContent, nil), []byte("\n"))
			originalOther := bytes.TrimSuffix(replaceStaticDNS(backup.StaticContent, nil), []byte("\n"))
			expectedStatic = backup.StaticContent
			if !bytes.Equal(currentOther, originalOther) {
				var originalServers []string
				for _, match := range staticDNSLine.FindAllSubmatch(backup.StaticContent, -1) {
					originalServers = append(originalServers, string(match[2]))
				}
				expectedStatic = replaceStaticDNS(state.staticContent, originalServers)
			}
		} else {
			expectedStatic = replaceStaticDNS(state.staticContent, request.Servers)
		}
		err = writeServerDNSFile(filepath.Join(a.serverDNSHostRoot, "etc/resolv.conf"), expectedStatic, state.staticMode)
	} else {
		path := filepath.Join(a.serverDNSHostRoot, serverDNSDropIn)
		if restoring {
			err = os.Remove(path)
			if errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
		} else {
			err = os.MkdirAll(filepath.Dir(path), 0755)
			if err == nil {
				content := "[Resolve]\nDNS=\nDNS=" + strings.Join(request.Servers, " ") + "\nDomains=~.\n"
				err = writeServerDNSFile(path, []byte(content), 0644)
			}
		}
		if err == nil {
			_, err = a.Command(ctx, "systemctl", "reload-or-restart", "systemd-resolved.service")
		}
	}
	if err != nil {
		WriteError(w, 500, "dns_apply_error", "无法应用 DNS 设置，可以恢复原设置后重试")
		return
	}

	state = a.readServerDNS(ctx, backup.Kind)
	switch {
	case state.kind == "static" && backup.Kind == "static":
		if !bytes.Equal(state.staticContent, expectedStatic) || (!restoring && !slices.Equal(state.Servers, request.Servers)) {
			err = errors.New("回读的静态设置与写入内容不一致")
		}
	case state.kind == "resolved" && restoring:
		if !slices.Equal(state.globalDNS, backup.GlobalDNS) || !slices.Equal(state.globalDomains, backup.GlobalDomains) || state.globalDNS == nil {
			err = errors.New("回读的全局设置与原设置不一致")
		}
	case state.kind == "resolved":
		if !state.CanEdit || !slices.Equal(state.globalDNS, request.Servers) || !slices.Contains(state.globalDomains, "~.") {
			err = errors.New("回读的默认 DNS 与写入内容不一致")
		}
	default:
		err = errors.New("无法读取生效的 DNS 设置")
	}
	if err != nil {
		WriteError(w, 500, "dns_readback_error", "无法确认 DNS 设置已生效，可以恢复原设置后重试")
		return
	}
	if restoring {
		if err := os.Remove(backupPath); err != nil {
			WriteError(w, 500, "dns_backup_error", "原 DNS 设置已恢复，但无法清理备份")
			return
		}
		state.CanRestore = false
	}
	WriteJSON(w, 200, state.serverDNSStatus)
}

func writeServerDNSFile(path string, content []byte, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// 在目标文件所在的文件系统暂存，避免跨文件系统阻止原子替换。
	stage := filepath.Join(filepath.Dir(path), ".tmp")
	if err := os.MkdirAll(stage, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(stage, "lpanel-dns-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(content)
	if err == nil && info != nil {
		owner := info.Sys().(*syscall.Stat_t)
		err = file.Chown(int(owner.Uid), int(owner.Gid))
	}
	if err == nil {
		err = file.Chmod(mode)
	}
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
