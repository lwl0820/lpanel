package panel

import (
	"crypto/md5"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var firewallAssignment = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)=(?:"([a-zA-Z0-9_./-]*)"|([a-zA-Z0-9_./-]+))$`)

type firewallUFWConfig struct {
	ipv6, ipv6Kernel, enabled  bool
	inputPolicy, forwardPolicy string
}

func readFirewallUFWConfig(files fs.FS) (firewallUFWConfig, error) {
	var config firewallUFWConfig
	values := map[string]string{}
	for _, item := range []struct {
		path string
		keys []string
	}{
		{"etc/default/ufw", []string{"IPV6", "DEFAULT_INPUT_POLICY", "DEFAULT_OUTPUT_POLICY", "DEFAULT_FORWARD_POLICY", "DEFAULT_APPLICATION_POLICY", "MANAGE_BUILTINS", "IPT_SYSCTL", "IPT_MODULES"}},
		{"etc/ufw/ufw.conf", []string{"ENABLED", "LOGLEVEL"}},
	} {
		data, err := fs.ReadFile(files, item.path)
		if err != nil {
			return config, err
		}
		for raw := range strings.SplitSeq(string(data), "\n") {
			line := strings.TrimSpace(raw)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			m := firewallAssignment.FindStringSubmatch(raw)
			if m == nil || !slices.Contains(item.keys, m[1]) {
				return config, fmt.Errorf("无法识别 %s 中的配置", item.path)
			}
			if _, duplicate := values[m[1]]; duplicate {
				return config, fmt.Errorf("%s 中有重复配置", item.path)
			}
			values[m[1]] = m[2] + m[3]
		}
	}
	validPolicy := func(value string) bool { return value == "ACCEPT" || value == "DROP" || value == "REJECT" }
	if values["IPV6"] != "yes" && values["IPV6"] != "no" || values["ENABLED"] != "yes" && values["ENABLED"] != "no" ||
		values["MANAGE_BUILTINS"] != "no" || !validPolicy(values["DEFAULT_INPUT_POLICY"]) || !validPolicy(values["DEFAULT_OUTPUT_POLICY"]) ||
		!validPolicy(values["DEFAULT_FORWARD_POLICY"]) || values["DEFAULT_APPLICATION_POLICY"] != "SKIP" && !validPolicy(values["DEFAULT_APPLICATION_POLICY"]) ||
		!slices.Contains([]string{"off", "low", "medium", "high", "full"}, values["LOGLEVEL"]) {
		return config, errors.New("防火墙的系统配置不支持在面板中修改")
	}
	if values["IPT_SYSCTL"] != "/etc/ufw/sysctl.conf" || values["IPT_MODULES"] != "" {
		return config, errors.New("防火墙使用了自定义网络设置")
	}
	_, err := fs.Stat(files, "proc/sys/net/ipv6")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return config, err
	}
	return firewallUFWConfig{
		ipv6: values["IPV6"] == "yes" && err == nil, ipv6Kernel: err == nil,
		enabled: values["ENABLED"] == "yes", inputPolicy: values["DEFAULT_INPUT_POLICY"], forwardPolicy: values["DEFAULT_FORWARD_POLICY"],
	}, nil
}

func checkFirewallUFWSetup(files fs.FS, ipv6 bool, switching bool) error {
	for _, name := range []string{"before.init", "after.init"} {
		info, err := fs.Stat(files, "etc/ufw/"+name)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0111 != 0) {
			return errors.New("防火墙使用了自定义启停脚本，请在服务器终端管理")
		}
	}
	if !switching {
		return nil
	}
	// UFW 启用时会把此文件直接交给 sysctl，不能仅验证配置中的文件路径。
	sysctl, err := fs.ReadFile(files, "etc/ufw/sysctl.conf")
	packages, pkgErr := fs.ReadFile(files, "var/lib/dpkg/status")
	if err != nil || pkgErr != nil || !strings.Contains(string(packages), fmt.Sprintf("\n /etc/ufw/sysctl.conf %x\n", md5.Sum(sysctl))) {
		return errors.New("防火墙的网络参数已修改或无法验证，请在服务器终端管理")
	}
	manifest, err := fs.ReadFile(files, "var/lib/dpkg/info/ufw.md5sums")
	if err != nil {
		return errors.New("无法验证防火墙的软件包配置，请在服务器终端管理")
	}
	hashes := map[string]string{}
	for line := range strings.SplitSeq(string(manifest), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 {
			hashes[fields[1]] = fields[0]
		}
	}
	names := []string{"before.rules", "after.rules"}
	if ipv6 {
		names = append(names, "before6.rules", "after6.rules")
	}
	for _, name := range names {
		data, err := fs.ReadFile(files, "etc/ufw/"+name)
		// 此处比对 dpkg 安装记录，不将 MD5 用作密码或签名算法。
		if err != nil || hashes["usr/share/ufw/iptables/"+name] != fmt.Sprintf("%x", md5.Sum(data)) {
			return fmt.Errorf("防火墙的 %s 已修改或无法验证，请在服务器终端管理", name)
		}
	}
	return nil
}

type firewallUFWRule struct {
	firewallRule
	ipv6                  bool
	src, dst              netip.Prefix
	dstPorts, destination string
	inbound, conditional  bool
}

var firewallIface = regexp.MustCompile(`^[a-zA-Z0-9_.:+-]+$`)
var firewallApp = regexp.MustCompile(`^[a-zA-Z0-9_.+%:/()-]+$`)
var firewallComment = regexp.MustCompile(`^[[:xdigit:]]+$`)

// 数字端口列表按 UFW 保存时的语法读取，包含连续范围及逗号分隔的端口。
func firewallPorts(ports string, target int) (matches, valid bool) {
	if ports == "any" {
		return true, true
	}
	for item := range strings.SplitSeq(ports, ",") {
		start, end, isRange := strings.Cut(item, ":")
		first, err := strconv.Atoi(start)
		if err != nil || first < 1 || first > 65535 || start != strconv.Itoa(first) {
			return false, false
		}
		last := first
		if isRange {
			last, err = strconv.Atoi(end)
			if err != nil || last < first || last > 65535 || end != strconv.Itoa(last) {
				return false, false
			}
		}
		matches = matches || target >= first && target <= last
	}
	return matches, true
}

// 对照 UFW 0.36 生成的规则，确保命令重写文件时不会丢掉未登记的规则。
func decodeFirewallUFWTuple(tuple, prefix string, ipv6 bool) (firewallUFWRule, []string, bool) {
	var rule firewallUFWRule
	text, comment, hasComment := strings.Cut(tuple, " comment=")
	f := strings.Fields(text)
	if len(f) < 6 || len(f) > 9 || hasComment && (!firewallComment.MatchString(comment) || len(comment)%2 != 0) {
		return rule, nil, false
	}
	action, protocol, dport, sport := f[0], f[1], f[2], f[4]
	forward := strings.HasPrefix(action, "route:")
	action = strings.TrimPrefix(action, "route:")
	if !slices.Contains([]string{"allow", "deny", "reject"}, action) ||
		!slices.Contains([]string{"any", "tcp", "udp", "ah", "esp", "gre", "ipv6", "igmp", "vrrp"}, protocol) {
		return rule, nil, false
	}
	parseAddress := func(raw string) netip.Prefix {
		if addr, err := netip.ParseAddr(raw); err == nil {
			return netip.PrefixFrom(addr, addr.BitLen())
		}
		prefix, _ := netip.ParsePrefix(raw)
		return prefix.Masked()
	}
	dst, src := parseAddress(f[3]), parseAddress(f[5])
	if !dst.IsValid() || !src.IsValid() || dst.Addr().Is6() != ipv6 || src.Addr().Is6() != ipv6 ||
		(dst.Bits() == 0 && f[3] != dst.String()) || (src.Bits() == 0 && f[5] != src.String()) {
		return rule, nil, false
	}
	_, dportValid := firewallPorts(dport, 0)
	_, sportValid := firewallPorts(sport, 0)
	if !dportValid || !sportValid {
		return rule, nil, false
	}
	chain := "input"
	var selectors []string
	if len(f)%2 != 0 {
		direction, ifaces, hasIface := strings.Cut(f[len(f)-1], "_")
		if direction == "out" {
			chain = "output"
		} else if direction != "in" {
			return rule, nil, false
		}
		if hasIface {
			in, out, both := strings.Cut(ifaces, "!out_")
			if !firewallIface.MatchString(in) || both && (!forward || direction != "in" || !firewallIface.MatchString(out)) {
				return rule, nil, false
			}
			selectors = append(selectors, "-"+direction[:1], in)
			if both {
				selectors = append(selectors, "-o", out)
			}
		}
	}
	if forward {
		chain = "forward"
	}
	iface := len(selectors) != 0
	var apps []string
	if len(f) >= 8 {
		for i, label := range []string{"dapp_", "sapp_"} {
			if value := f[6+i]; value != "-" {
				if !firewallApp.MatchString(value) {
					return rule, nil, false
				}
				apps = append(apps, label+value)
			}
		}
	}
	protocols := []string{protocol}
	if protocol == "any" && (dport != "any" || sport != "any") {
		protocols = []string{"tcp", "udp"}
	}
	multi := strings.ContainsAny(dport+sport, ",:")
	if multi && protocol != "tcp" && protocol != "udp" {
		return rule, nil, false
	}
	var body []string
	for _, proto := range protocols {
		args := slices.Clone(selectors)
		if proto != "any" {
			args = append(args, "-p", proto)
		}
		if multi {
			if dport != "any" {
				args = append(args, "-m", "multiport", "--dports", dport)
			}
			if sport != "any" {
				args = append(args, "-m", "multiport", "--sports", sport)
			}
		}
		if dst.Bits() != 0 {
			args = append(args, "-d", f[3])
		}
		if !multi && dport != "any" {
			args = append(args, "--dport", dport)
		}
		if src.Bits() != 0 {
			args = append(args, "-s", f[5])
		}
		if !multi && sport != "any" {
			args = append(args, "--sport", sport)
		}
		match := ""
		if len(args) != 0 {
			match = " " + strings.Join(args, " ")
		}
		app := ""
		if len(apps) != 0 {
			app = " -m comment --comment '" + strings.Join(apps, ",") + "'"
		}
		base := "-A " + prefix + "-user-" + chain + match
		target, reject := "", ""
		switch action {
		case "allow":
			target = "ACCEPT"
		case "deny":
			target = "DROP"
		case "reject":
			target = "REJECT"
			if proto == "tcp" {
				reject = " --reject-with tcp-reset"
			}
		}
		body = append(body, base+" -j "+target+reject+app)
	}
	port, _ := strconv.Atoi(dport)
	family := "IPv4"
	if ipv6 {
		family = "IPv6"
	}
	description := family + " · " + dport
	if dst.Bits() != 0 {
		description += " · 目标 " + f[3]
	}
	if iface {
		description += " · 接口 " + selectors[1]
	}
	if sport != "any" {
		description += " · 来源端口 " + sport
	}
	rule = firewallUFWRule{
		firewallRule: firewallRule{
			ID: fmt.Sprintf("%x", sha256.Sum256([]byte(family+" "+tuple))), Port: port, Protocol: protocol,
			Action: action, Source: f[5], Description: description,
			Removable: (action == "allow" || action == "deny") && (protocol == "tcp" || protocol == "udp") &&
				port > 0 && sport == "any" && !iface && len(apps) == 0 && !hasComment,
		},
		ipv6: ipv6, src: src, dst: dst, dstPorts: dport, destination: f[3],
		inbound: chain == "input", conditional: iface || sport != "any",
	}
	return rule, body, true
}

func readFirewallUserRules(files fs.FS, ipv6 bool) ([]firewallUFWRule, bool, error) {
	suffix := ""
	if ipv6 {
		suffix = "6"
	}
	data, err := fs.ReadFile(files, "etc/ufw/user"+suffix+".rules")
	if err != nil {
		return nil, false, err
	}
	prefix := "ufw" + suffix
	var rules []firewallUFWRule
	var tuple, section string
	var body []string
	seen := map[string]bool{}
	safe := true
	commit := false
	flush := func() {
		if tuple == "" {
			return
		}
		rule, expected, ok := decodeFirewallUFWTuple(tuple, prefix, ipv6)
		if !ok || !slices.Equal(body, expected) {
			safe = false
			inbound := rule.inbound
			if !ok {
				fields := strings.Fields(tuple)
				inbound = len(fields) != 0 && !strings.HasPrefix(fields[0], "route:")
			}
			rule = firewallUFWRule{ID: fmt.Sprintf("%x", sha256.Sum256([]byte(prefix+" "+tuple))), Description: "无法确认内容的规则", ipv6: ipv6, inbound: inbound}
		}
		if rule.inbound {
			if seen[rule.ID] {
				safe = false
			} else {
				seen[rule.ID] = true
				rules = append(rules, rule)
			}
		}
		tuple, body = "", nil
	}
	managedChains := []string{"user-input", "user-output", "user-forward", "before-logging-input", "before-logging-output", "before-logging-forward", "user-logging-input", "user-logging-output", "user-logging-forward", "after-logging-input", "after-logging-output", "after-logging-forward", "logging-deny", "logging-allow", "user-limit", "user-limit-accept"}
	for raw := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if value, ok := strings.CutPrefix(line, "### tuple ### "); ok && section == "rules" {
			flush()
			safe = safe && strings.HasPrefix(raw, "### tuple ### ")
			tuple = value
			continue
		}
		switch line {
		case "*filter":
			safe = safe && section == ""
			section = "header"
		case "### RULES ###":
			safe = safe && section == "header"
			section = "rules"
		case "### END RULES ###", "### LOGGING ###", "### END LOGGING ###", "### RATE LIMITING ###", "### END RATE LIMITING ###", "COMMIT":
			flush()
			safe = safe && section != "" && section != "commit"
			section = "footer"
			if line == "COMMIT" {
				section, commit = "commit", true
			}
		default:
			switch {
			case strings.HasPrefix(line, "#"):
			case section == "header" && strings.HasPrefix(line, ":"+prefix+"-"):
				name, counters, _ := strings.Cut(strings.TrimPrefix(line, ":"+prefix+"-"), " ")
				safe = safe && slices.Contains(managedChains, name) && counters == "- [0:0]"
			case section == "rules" && tuple != "":
				body = append(body, line)
			case section == "rules" || section == "footer":
				// 旧版的初始文件把限速链直接放在规则标记之后。
				safe = safe && firewallUFWFooter(line, prefix)
			default:
				safe = false
			}
		}
	}
	flush()
	return rules, safe && commit, nil
}

var firewallLogFooter = regexp.MustCompile(`^-(?:A|I) (?:ufw6?)-(?:(?:before|after)-logging-(?:input|output|forward)|logging-(?:allow|deny)|user-limit) (?:(?:-m conntrack --ctstate INVALID )?-j LOG --log-prefix "\[UFW (?:BLOCK|ALLOW|AUDIT|AUDIT INVALID|LIMIT BLOCK)\] "(?: -m conntrack --ctstate NEW)?(?: -m limit --limit [0-9]+/min --limit-burst [0-9]+)?|-m limit --limit [0-9]+/minute -j LOG --log-prefix "\[UFW LIMIT BLOCK\] "|-m conntrack --ctstate INVALID -j RETURN -m limit --limit [0-9]+/min --limit-burst [0-9]+)$`)

func firewallUFWFooter(line, prefix string) bool {
	return line == "-A "+prefix+"-user-limit -j REJECT" || line == "-A "+prefix+"-user-limit-accept -j ACCEPT" ||
		(len(line) > 2 && strings.HasPrefix(line[2:], " "+prefix+"-") && firewallLogFooter.MatchString(line))
}
