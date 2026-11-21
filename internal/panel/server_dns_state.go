package panel

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var staticDNSLine = regexp.MustCompile(`(?m)^([ \t]*nameserver[ \t]+)([^ \t\r\n;#]+)([^\r\n]*)(\r?\n|$)`)

func isDNSStub(ip netip.Addr) bool {
	return ip == netip.AddrFrom4([4]byte{127, 0, 0, 53}) || ip == netip.AddrFrom4([4]byte{127, 0, 0, 54})
}

func (a *App) readServerDNS(ctx context.Context, backupKind string) serverDNSState {
	state := serverDNSState{serverDNSStatus: serverDNSStatus{
		Servers: []string{},
		Message: "当前 DNS 由其他服务管理，面板无法修改",
	}}
	path := filepath.Join(a.serverDNSHostRoot, "etc/resolv.conf")
	info, err := os.Lstat(path)
	if err != nil {
		state.Message = "无法读取服务器的 DNS 设置"
		return state
	}
	nss, nssErr := os.ReadFile(filepath.Join(a.serverDNSHostRoot, "etc/nsswitch.conf"))
	resolved := false
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return state
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join("/etc", target)
		}
		switch filepath.Clean(target) {
		case "/run/systemd/resolve/stub-resolv.conf", "/usr/lib/systemd/resolv.conf", "/lib/systemd/resolv.conf":
			resolved = true
		}
	} else if !info.Mode().IsRegular() {
		return state
	}

	if nssErr != nil || !usesServerDNS(string(nss), resolved) {
		state.Message = "无法确认系统名称查询会使用这份 DNS 设置，面板无法修改"
		return state
	}
	if resolved {
		return a.readResolvedDNS(ctx, state, backupKind)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		state.Message = "无法读取服务器的 DNS 设置"
		return state
	}
	generated := false
	for line := range strings.SplitSeq(string(content), "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			for _, marker := range []string{"generated", "managed by", "do not edit", "resolvconf", "networkmanager", "dhclient", "systemd-resolved", "cloud-init"} {
				generated = generated || strings.Contains(line, marker)
			}
		}
	}
	matches := staticDNSLine.FindAllSubmatch(content, staticDNSLimit)
	for _, match := range matches {
		if ip, err := netip.ParseAddr(string(match[2])); err == nil {
			if !isDNSStub(ip.Unmap()) {
				state.Servers = append(state.Servers, ip.String())
			} else {
				generated = true
			}
		}
	}
	if !info.Mode().IsRegular() || generated {
		return state
	}
	state.kind, state.staticContent, state.staticMode = "static", content, info.Mode()
	state.CanRestore = backupKind == state.kind
	if backupKind != "" && !state.CanRestore {
		state.Message = "DNS 管理方式已变更，无法应用面板保存的设置"
		return state
	}
	state.CanEdit, state.Message, state.MaxServers = true, "", staticDNSLimit
	if len(matches) == 0 {
		state.Servers = []string{"127.0.0.1"}
		state.Message = "服务器未配置 DNS 地址，系统默认尝试使用本机 DNS"
	}
	return state
}

func usesServerDNS(nss string, resolved bool) bool {
	for line := range strings.SplitSeq(nss, "\n") {
		line, _, _ = strings.Cut(line, "#")
		service, entries, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(service) != "hosts" {
			continue
		}
		fields := strings.Fields(entries)
		for i, entry := range fields {
			switch entry {
			case "dns":
				return true
			case "resolve":
				return resolved
			case "files", "myhostname", "mdns4_minimal", "mdns_minimal":
			case "[NOTFOUND=return]":
				if i == 0 || (fields[i-1] != "mdns4_minimal" && fields[i-1] != "mdns_minimal") {
					return false
				}
			default:
				return false
			}
		}
		return false
	}
	return false
}

func replaceStaticDNS(content []byte, servers []string) []byte {
	matches := staticDNSLine.FindAllSubmatchIndex(content, -1)
	var updated []byte
	pos := 0
	for i, match := range matches {
		updated = append(updated, content[pos:match[0]]...)
		pos = match[1]
		if i < len(servers) {
			updated = append(updated, content[match[2]:match[3]]...)
			updated = append(updated, servers[i]...)
			updated = append(updated, content[match[5]:pos]...)
		} else if len(bytes.TrimSpace(content[match[5]:match[7]])) != 0 {
			updated = append(updated, content[match[5]:pos]...)
		}
	}
	if len(matches) == 0 {
		updated, pos = append(updated, content...), len(content)
	}
	for _, server := range servers[min(len(matches), len(servers)):] {
		if len(updated) != 0 && updated[len(updated)-1] != '\n' {
			updated = append(updated, '\n')
		}
		updated = append(updated, "nameserver "+server+"\n"...)
	}
	return append(updated, content[pos:]...)
}

func (a *App) readResolvedDNS(ctx context.Context, state serverDNSState, backupKind string) serverDNSState {
	state.kind, state.CanRestore = "resolved", backupKind == "resolved"
	state.Message = "无法读取系统 DNS 服务的运行状态"
	output, err := a.Command(ctx, "resolvectl", "dns")
	if err != nil {
		return state
	}
	dns, links := parseResolvedDNSLists(string(output))
	output, err = a.Command(ctx, "resolvectl", "domain")
	if err != nil {
		return state
	}
	domains, linkDomains := parseResolvedDNSLists(string(output))
	if dns == nil || domains == nil {
		return state
	}
	rootLinks := make(map[string]bool)
	for link, list := range linkDomains {
		if slices.Contains(list, "~.") || slices.Contains(list, ".") {
			rootLinks[link] = true
		}
	}
	roots := slices.Contains(domains, "~.") || slices.Contains(domains, ".")
	defaultRoutes := map[string][]string{}
	var hasLinkDNS bool
	for servers := range maps.Values(links) {
		hasLinkDNS = hasLinkDNS || len(servers) != 0
	}
	if !roots && len(rootLinks) == 0 && hasLinkDNS {
		output, err = a.Command(ctx, "resolvectl", "default-route")
		if err != nil {
			return state
		}
		_, defaultRoutes = parseResolvedDNSLists(string(output))
		for link, servers := range links {
			route := defaultRoutes[link]
			if len(servers) != 0 && (len(route) != 1 || (route[0] != "yes" && route[0] != "no")) {
				return state
			}
		}
	}

	state.globalDNS, state.globalDomains = dns, domains
	if roots || len(rootLinks) == 0 {
		state.Servers = append(state.Servers, dns...)
	}
	for _, link := range slices.Sorted(maps.Keys(links)) {
		if rootLinks[link] || (!roots && len(rootLinks) == 0 && slices.Equal(defaultRoutes[link], []string{"yes"})) {
			state.Servers = append(state.Servers, links[link]...)
		}
	}
	if len(state.Servers) == 0 {
		if output, err := a.Command(ctx, "resolvectl", "status"); err == nil {
			fallback := false
			for line := range strings.SplitSeq(string(output), "\n") {
				line = strings.TrimSpace(line)
				if value, found := strings.CutPrefix(line, "Fallback DNS Servers:"); found {
					state.Servers = append(state.Servers, strings.Fields(value)...)
					fallback = true
				} else if fallback && !strings.Contains(line, ": ") && line != "" {
					state.Servers = append(state.Servers, strings.Fields(line)...)
				} else if fallback {
					break
				}
			}
		}
	}
	displayed := state.Servers[:0]
	for _, server := range state.Servers {
		if ip, err := netip.ParseAddr(server); (err != nil || !isDNSStub(ip.Unmap())) && !slices.Contains(displayed, server) {
			displayed = append(displayed, server)
		}
	}
	state.Servers = displayed

	switch {
	case backupKind != "" && !state.CanRestore:
		state.Message = "DNS 管理方式已变更，无法应用面板保存的设置"
	case len(rootLinks) != 0:
		state.Message = "已有网卡设置了默认 DNS，面板无法在保留该设置的同时修改服务器默认 DNS"
	default:
		path := filepath.Join(a.serverDNSHostRoot, serverDNSDropIn)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && info.Mode().IsRegular() && state.CanRestore) {
			state.CanEdit, state.Message = true, ""
		} else {
			state.CanRestore = false
			state.Message = "默认 DNS 设置文件已被其他配置占用，面板无法修改"
		}
	}
	return state
}

func parseResolvedDNSLists(output string) (global []string, links map[string][]string) {
	links = make(map[string][]string)
	var current string
	for line := range strings.SplitSeq(output, "\n") {
		if value, found := strings.CutPrefix(line, "Global:"); found {
			global = append([]string{}, strings.Fields(value)...)
			current = "Global"
		} else if link, value, found := strings.CutLast(line, "):"); found && strings.HasPrefix(link, "Link ") {
			current = link
			links[link] = strings.Fields(value)
		} else if value, found := strings.CutPrefix(line, "        "); found && current != "" {
			if current == "Global" {
				global = append(global, strings.Fields(value)...)
			} else {
				links[current] = append(links[current], strings.Fields(value)...)
			}
		} else {
			current = ""
		}
	}
	return global, links
}
