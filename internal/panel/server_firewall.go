package panel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

const firewallAPI = "/api/v1/server/firewall"

type firewallRule struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Port        int    `json:"port,omitzero"`
	Protocol    string `json:"protocol,omitempty"`
	Action      string `json:"action,omitempty"`
	Source      string `json:"source,omitempty"`
	Removable   bool   `json:"removable"`
}

type firewallState struct {
	Available bool           `json:"available"`
	Enabled   bool           `json:"enabled"`
	PanelPort int            `json:"panel_port"`
	Message   string         `json:"message,omitempty"`
	Rules     []firewallRule `json:"rules"`

	config firewallUFWConfig
	saved  []firewallUFWRule
	issue  error
}

func (a *App) registerServerFirewall() {
	for _, pattern := range []string{"GET " + firewallAPI, "POST " + firewallAPI + "/enable", "POST " + firewallAPI + "/disable", "POST " + firewallAPI + "/rules", "DELETE " + firewallAPI + "/rules"} {
		a.Mux.Handle(pattern, a.RequireAuth(http.HandlerFunc(a.serverFirewall)))
	}
}

func (a *App) firewallStatus(ctx context.Context) (available, enabled bool, err error) {
	out, err := a.Command(ctx, "ufw", "status")
	if errors.Is(err, exec.ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return true, false, fmt.Errorf("无法读取防火墙状态：%w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		switch strings.TrimSpace(line) {
		case "Status: active":
			return true, true, nil
		case "Status: inactive":
			return true, false, nil
		}
	}
	return true, false, errors.New("无法识别防火墙的当前状态")
}

func (a *App) readFirewall(ctx context.Context, panelPort int) (firewallState, error) {
	state := firewallState{PanelPort: panelPort, Rules: []firewallRule{}}
	var err error
	state.Available, state.Enabled, err = a.firewallStatus(ctx)
	if err != nil {
		return state, err
	}
	if !state.Available {
		state.Message = "服务器未安装 UFW 防火墙"
		return state, nil
	}
	state.config, state.issue = readFirewallUFWConfig(a.firewallFS)
	if state.issue == nil {
		for _, ipv6 := range []bool{false, true} {
			if ipv6 && !state.config.ipv6 {
				break
			}
			rules, safe, err := readFirewallUserRules(a.firewallFS, ipv6)
			state.saved = append(state.saved, rules...)
			if err != nil || !safe {
				state.issue = errors.New("部分防火墙规则无法在面板中管理，请在服务器终端管理")
			}
		}
	}
	if state.issue != nil {
		state.Message = state.issue.Error()
	} else if state.Enabled != state.config.enabled {
		state.Message = "防火墙运行状态和启动设置不一致，请在服务器终端停用 UFW 后刷新"
	}
	for _, rule := range state.saved {
		view := rule.firewallRule
		view.Removable = view.Removable && state.Message == "" &&
			(!state.Enabled || view.Action != "allow" || view.Protocol != "tcp" || view.Port != panelPort)
		state.Rules = append(state.Rules, view)
	}
	return state, nil
}

func (a *App) serverFirewall(w http.ResponseWriter, r *http.Request) {
	var input firewallRule
	add := r.Method == http.MethodPost && r.URL.Path == firewallAPI+"/rules"
	if add {
		if !ReadJSON(w, r, &input) {
			return
		}
		if input.Port < 1 || input.Port > 65535 || (input.Protocol != "tcp" && input.Protocol != "udp") ||
			(input.Action != "allow" && input.Action != "deny") {
			WriteError(w, 400, "invalid_firewall_rule", "请输入有效的端口，并选择 TCP 或 UDP 及允许或拒绝")
			return
		}
	}
	id := r.URL.Query().Get("id")
	if r.Method == http.MethodDelete && id == "" {
		WriteError(w, 400, "invalid_firewall_rule", "请选择要删除的规则")
		return
	}
	a.firewallMu.Lock()
	defer a.firewallMu.Unlock()
	if r.Context().Err() != nil {
		return
	}
	ctx := r.Context()
	if r.Method != http.MethodGet {
		// 浏览器断开后，仍要完成防火墙命令和状态核对。
		ctx = context.WithoutCancel(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	panelPort := a.currentConfig().PanelPort
	state, err := a.readFirewall(ctx, panelPort)
	if err != nil {
		WriteError(w, 503, "firewall_status_failed", err.Error())
		return
	}
	if r.Method == http.MethodGet {
		WriteJSON(w, 200, state)
		return
	}
	if !state.Available {
		WriteError(w, 503, "firewall_unavailable", state.Message)
		return
	}
	writeOK := func() { WriteJSON(w, 200, map[string]bool{"ok": true}) }
	if r.URL.Path == firewallAPI+"/disable" {
		// 停用不要求能解释用户规则，但仍须确认 UFW 不清空其他过滤链。
		config, err := readFirewallUFWConfig(a.firewallFS)
		if err == nil {
			err = checkFirewallUFWSetup(a.firewallFS, config.ipv6, false)
		}
		if err != nil {
			WriteError(w, 409, "firewall_config_unsupported", err.Error())
			return
		}
		if state.Enabled || config.enabled {
			if err := a.setFirewallEnabled(ctx, false, config.ipv6Kernel); err != nil {
				WriteError(w, 500, "firewall_disable_failed", err.Error())
				return
			}
		}
		writeOK()
		return
	}
	if state.Message != "" {
		WriteError(w, 409, "firewall_config_unsupported", state.Message)
		return
	}
	if err := checkFirewallUFWSetup(a.firewallFS, state.config.ipv6, false); err != nil {
		WriteError(w, 409, "firewall_config_unsupported", err.Error())
		return
	}
	if r.URL.Path == firewallAPI+"/enable" {
		if state.Enabled {
			writeOK()
			return
		}
		code, err := a.enableFirewall(ctx, r, state)
		if err != nil {
			status := 409
			if code == "firewall_enable_failed" {
				status = 500
			}
			WriteError(w, status, code, err.Error())
			return
		}
		writeOK()
		return
	}
	if panelPort < 1 || panelPort > 65535 {
		WriteError(w, 409, "firewall_panel_port_unknown", "无法确认当前面板端口")
		return
	}
	if add {
		if state.Enabled && input.Port == panelPort && input.Protocol == "tcp" && input.Action == "deny" {
			WriteError(w, 409, "firewall_panel_rule", "防火墙已启用，不能拒绝访问当前面板端口")
			return
		}
		for _, rule := range state.saved {
			if rule.Port != input.Port || rule.Protocol != input.Protocol || rule.src.Bits() != 0 || rule.dst.Bits() != 0 || rule.conditional {
				continue
			}
			if rule.Action != input.Action || !rule.Removable {
				WriteError(w, 409, "firewall_rule_exists", "已有使用同一端口和协议的规则，无法覆盖")
				return
			}
		}
		if !firewallHasRule(state.saved, false, input) || state.config.ipv6 && !firewallHasRule(state.saved, true, input) {
			if _, err := a.Command(ctx, "ufw", input.Action, fmt.Sprintf("%d/%s", input.Port, input.Protocol)); err != nil {
				WriteError(w, 500, "firewall_rule_failed", "无法添加防火墙规则，请刷新后查看当前规则")
				return
			}
			after, err := a.readFirewall(ctx, panelPort)
			if err != nil || !after.Available || after.Message != "" || after.Enabled != state.Enabled ||
				!firewallHasRule(after.saved, false, input) || state.config.ipv6 && !firewallHasRule(after.saved, true, input) {
				WriteError(w, 500, "firewall_rule_failed", "无法确认新规则已保存，请刷新后查看当前规则")
				return
			}
		}
	} else {
		index := slices.IndexFunc(state.saved, func(rule firewallUFWRule) bool { return rule.ID == id })
		if index == -1 {
			WriteError(w, 404, "firewall_rule_not_found", "该规则已不存在，请刷新规则列表")
			return
		}
		rule := state.saved[index]
		if !state.Rules[index].Removable {
			WriteError(w, 409, "firewall_rule_protected", "该规则不能删除，可能影响当前面板访问")
			return
		}
		for i, other := range state.saved {
			if i != index && other.ipv6 == rule.ipv6 && other.Protocol == rule.Protocol && other.Port == rule.Port &&
				other.Action == rule.Action && other.src == rule.src && other.dst == rule.dst && !other.conditional {
				WriteError(w, 409, "firewall_rule_protected", "此规则与其他规则重复，无法单独删除")
				return
			}
		}
		// 使用保存的规则内容而非位置，避免列表顺序改变时删错规则。
		args := []string{"--force", "delete", rule.Action, "proto", rule.Protocol, "from", rule.Source, "to", rule.destination, "port", strconv.Itoa(rule.Port)}
		if _, err := a.Command(ctx, "ufw", args...); err != nil {
			WriteError(w, 500, "firewall_rule_failed", "无法删除防火墙规则，请刷新后查看当前规则")
			return
		}
		after, err := a.readFirewall(ctx, panelPort)
		if err != nil || !after.Available || after.Message != "" || after.Enabled != state.Enabled ||
			slices.ContainsFunc(after.saved, func(candidate firewallUFWRule) bool { return candidate.ID == id }) {
			WriteError(w, 500, "firewall_rule_failed", "无法确认规则已删除，请刷新后查看当前规则")
			return
		}
	}
	writeOK()
}

func firewallHasRule(rules []firewallUFWRule, ipv6 bool, wanted firewallRule) bool {
	return slices.ContainsFunc(rules, func(rule firewallUFWRule) bool {
		return rule.ipv6 == ipv6 && rule.Removable && rule.Port == wanted.Port && rule.Protocol == wanted.Protocol &&
			rule.Action == wanted.Action && rule.src.Bits() == 0 && rule.dst.Bits() == 0
	})
}

func (a *App) setFirewallEnabled(ctx context.Context, enabled, ipv6Kernel bool) (result error) {
	command, label := "disable", "停用"
	if enabled {
		command, label = "enable", "启用"
	} else {
		for _, table := range []string{"iptables", "ip6tables"} {
			if table == "ip6tables" && !ipv6Kernel {
				continue
			}
			out, err := a.Command(ctx, table, "-S", "FORWARD")
			if err != nil {
				return errors.New("无法检查当前转发策略，未执行停用")
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			external := slices.ContainsFunc(lines, func(line string) bool {
				return strings.HasPrefix(line, "-A FORWARD ") && !strings.HasPrefix(line, "-A FORWARD -j ufw-") && !strings.HasPrefix(line, "-A FORWARD -j ufw6-")
			})
			if !external || !slices.Contains(lines, "-P FORWARD DROP") {
				continue
			}
			// ufw_stop 会暂时将默认策略改为 ACCEPT。先在外部转发规则之后守住 DROP。
			guard := []string{"FORWARD", "-m", "comment", "--comment", "lpanel-ufw-stop", "-j", "DROP"}
			if _, err := a.Command(ctx, table, append([]string{"-A"}, guard...)...); err != nil {
				return errors.New("无法保护现有转发策略，未执行停用")
			}
			defer func() {
				restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if _, err := a.Command(restore, table, "-P", "FORWARD", "DROP"); err != nil {
					result = errors.Join(result, errors.New("无法恢复转发策略，已保留临时拒绝规则，请在服务器终端检查"))
				} else if _, err := a.Command(restore, table, append([]string{"-D"}, guard...)...); err != nil {
					result = errors.Join(result, errors.New("转发策略已恢复，临时拒绝规则未能删除，请在服务器终端检查"))
				}
			}()
		}
	}
	if _, err := a.Command(ctx, "ufw", "--force", command); err != nil {
		return fmt.Errorf("无法%s防火墙，请刷新后查看当前状态", label)
	}
	available, active, err := a.firewallStatus(ctx)
	config, configErr := readFirewallUFWConfig(a.firewallFS)
	if err != nil || configErr != nil || !available || active != enabled || config.enabled != enabled {
		return fmt.Errorf("无法确认防火墙已%s，请刷新后查看当前状态", label)
	}
	return nil
}

func (a *App) enableFirewall(ctx context.Context, r *http.Request, state firewallState) (string, error) {
	port := state.PanelPort
	if port < 1 || port > 65535 {
		return "firewall_panel_port_unknown", errors.New("无法确认当前面板端口，防火墙未启用")
	}
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	proxy, _ := netip.ParseAddr(peer)
	_, entryPort, _ := net.SplitHostPort(r.Host)
	src, _ := netip.ParseAddr(r.Header.Get("X-LPanel-Client-IP"))
	dst, _ := netip.ParseAddr(r.Header.Get("X-LPanel-Server-IP"))
	src, dst = src.Unmap(), dst.Unmap()
	if !proxy.Unmap().IsLoopback() || r.Header.Get("X-LPanel-Proxy") != "2" || entryPort != strconv.Itoa(port) || !src.IsValid() || !dst.IsValid() ||
		src.IsUnspecified() || dst.IsUnspecified() || src.IsLoopback() || dst.IsLoopback() || src.Is6() != dst.Is6() || src.Zone() != "" || dst.Zone() != "" {
		return "firewall_panel_connection_unknown", errors.New("无法确认当前面板入口，防火墙未启用。请从其他机器通过面板端口访问；升级后请先更新面板的 Nginx 配置")
	}
	if state.config.ipv6Kernel && !state.config.ipv6 {
		return "firewall_config_unsupported", errors.New("当前防火墙设置会阻断 IPv6，请在服务器终端管理")
	}
	if err := checkFirewallUFWSetup(a.firewallFS, state.config.ipv6, true); err != nil {
		return "firewall_config_unsupported", err
	}
	// UFW 将自己的链追加到 INPUT，并改写内置链的默认策略。
	// 已有的入站规则或非默认策略不能当成空白防火墙再启用。
	for _, table := range []string{"iptables", "ip6tables"} {
		if table == "ip6tables" && !state.config.ipv6 {
			continue
		}
		out, err := a.Command(ctx, table, "-S")
		if err != nil {
			return "firewall_config_unsupported", errors.New("无法检查服务器现有的过滤规则，防火墙未启用")
		}
		prefix := "ufw-"
		if table == "ip6tables" {
			prefix = "ufw6-"
		}
		var jumps []string
		var reused bool
		input := false
		for line := range strings.SplitSeq(string(out), "\n") {
			switch line {
			case "-P INPUT ACCEPT":
				input = true
			case "-P FORWARD ACCEPT", "-P FORWARD " + state.config.forwardPolicy, "-P OUTPUT ACCEPT":
			case "-N " + prefix + "before-logging-input":
				reused = true
			default:
				if chain, ok := strings.CutPrefix(line, "-A INPUT -j "+prefix); ok {
					jumps = append(jumps, chain)
				} else if strings.HasPrefix(line, "-A INPUT ") || strings.HasPrefix(line, "-A "+prefix) || strings.HasPrefix(line, "-P ") || strings.HasPrefix(line, "# Warning:") {
					return "firewall_config_unsupported", errors.New("服务器已有其他过滤规则或默认策略，请在服务器终端管理")
				}
			}
		}
		// UFW 停用后会留下按固定顺序连接的空链，下一次启用会复用它们。
		if !input || (reused || len(jumps) != 0) && (!reused || !slices.Equal(jumps, []string{"before-logging-input", "before-input", "after-input", "after-logging-input", "reject-input", "track-input"})) {
			return "firewall_config_unsupported", errors.New("无法确认现有入站策略，防火墙未启用")
		}
	}
	// 原生 nftables 可在 iptables 看不到的独立入站链中再次过滤。
	if out, err := a.Command(ctx, "nft", "-j", "list", "chains"); !errors.Is(err, exec.ErrNotFound) {
		var tables struct {
			NFTables []struct {
				Chain struct{ Family, Table, Name, Hook string }
			}
		}
		if err != nil || json.Unmarshal(out, &tables, json.MatchCaseInsensitiveNames(true)) != nil || tables.NFTables == nil {
			return "firewall_config_unsupported", errors.New("无法检查本机的 nftables 入站规则，防火墙未启用")
		}
		for _, item := range tables.NFTables {
			c := item.Chain
			if c.Hook == "input" && !(c.Table == "filter" && c.Name == "INPUT" && (c.Family == "ip" || c.Family == "ip6")) {
				return "firewall_config_unsupported", errors.New("服务器已有独立的 nftables 入站规则，请在服务器终端管理")
			}
		}
	}
	allowed, canAdd := state.panelAccess(src, dst)
	if !allowed && !canAdd {
		return "firewall_panel_conflict", fmt.Errorf("现有规则可能阻止访问面板端口 %d，无法确认放行，防火墙未启用", port)
	}
	if !allowed {
		wanted := firewallRule{Port: port, Protocol: "tcp", Action: "allow"}
		_, err := a.Command(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", port))
		after, readErr := a.readFirewall(ctx, port)
		confirmed, _ := after.panelAccess(src, dst)
		if err != nil || readErr != nil || !after.Available || after.Enabled || after.Message != "" ||
			!confirmed || !firewallHasRule(after.saved, false, wanted) || state.config.ipv6 && !firewallHasRule(after.saved, true, wanted) {
			return "firewall_enable_failed", fmt.Errorf("无法确认面板端口 %d 已添加放行规则，防火墙未启用，请刷新后查看当前规则", port)
		}
	}
	if err := a.setFirewallEnabled(ctx, true, state.config.ipv6Kernel); err != nil {
		rollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
		defer cancel()
		if a.setFirewallEnabled(rollback, false, state.config.ipv6Kernel) != nil {
			return "firewall_enable_failed", errors.New("启用失败，无法确认防火墙已恢复停用，请立即在服务器终端检查")
		}
		return "firewall_enable_failed", errors.New("启用失败，防火墙已恢复停用。请在服务器终端检查防火墙设置")
	}
	return "", nil
}

func (state firewallState) panelAccess(src, dst netip.Addr) (allowed, canAdd bool) {
	overlaps := false
	for _, rule := range state.saved {
		matchesPort, _ := firewallPorts(rule.dstPorts, state.PanelPort)
		if !matchesPort || (rule.Protocol != "tcp" && rule.Protocol != "any") {
			continue
		}
		overlaps = true
		if rule.ipv6 != src.Is6() || !rule.src.Contains(src) || !rule.dst.Contains(dst) {
			continue
		}
		return rule.Action == "allow" && !rule.conditional, false
	}
	return state.config.inputPolicy == "ACCEPT", !overlaps
}
