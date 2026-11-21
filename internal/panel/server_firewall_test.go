package panel

import (
	"context"
	"crypto/md5"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func firewallFixtureRules(prefix string, rules ...string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("*filter\n:" + prefix + "-user-input - [0:0]\n### RULES ###\n" + strings.Join(rules, "\n") + "\n### END RULES ###\nCOMMIT\n")}
}

func firewallFixtureFiles() fstest.MapFS {
	files := fstest.MapFS{
		"etc/default/ufw": {Data: []byte(`IPV6=yes
DEFAULT_INPUT_POLICY="DROP"
DEFAULT_OUTPUT_POLICY="ACCEPT"
DEFAULT_FORWARD_POLICY="DROP"
DEFAULT_APPLICATION_POLICY="SKIP"
MANAGE_BUILTINS=no
IPT_SYSCTL=/etc/ufw/sysctl.conf
IPT_MODULES=""
`)},
		"etc/ufw/ufw.conf":    {Data: []byte("ENABLED=no\nLOGLEVEL=low\n")},
		"etc/ufw/user.rules":  firewallFixtureRules("ufw"),
		"etc/ufw/user6.rules": firewallFixtureRules("ufw6"),
		"etc/ufw/before.init": {Mode: 0640},
		"proc/sys/net/ipv6":   {Mode: fs.ModeDir},
	}
	var manifest strings.Builder
	for _, name := range []string{"before.rules", "after.rules", "before6.rules", "after6.rules"} {
		data := []byte("*filter\nCOMMIT\n")
		files["etc/ufw/"+name] = &fstest.MapFile{Data: data}
		fmt.Fprintf(&manifest, "%x  usr/share/ufw/iptables/%s\n", md5.Sum(data), name)
	}
	files["var/lib/dpkg/info/ufw.md5sums"] = &fstest.MapFile{Data: []byte(manifest.String())}
	files["etc/ufw/sysctl.conf"] = &fstest.MapFile{Data: []byte("net/ipv4/icmp_echo_ignore_all=0\n")}
	files["var/lib/dpkg/status"] = &fstest.MapFile{Data: fmt.Appendf(nil, "Package: ufw\nConffiles:\n /etc/ufw/sysctl.conf %x\n", md5.Sum(files["etc/ufw/sysctl.conf"].Data))}
	return files
}

func TestServerFirewallEnable(t *testing.T) {
	const allow = "### tuple ### allow tcp 12443 0.0.0.0/0 any 0.0.0.0/0 in\n-A ufw-user-input -p tcp --dport 12443 -j ACCEPT"
	const allow6 = "### tuple ### allow tcp 12443 ::/0 any ::/0 in\n-A ufw6-user-input -p tcp --dport 12443 -j ACCEPT"
	const scoped = "### tuple ### allow tcp 12400:12500 192.0.2.20 any 198.51.100.0/24 in\n-A ufw-user-input -p tcp -m multiport --dports 12400:12500 -d 192.0.2.20 -s 198.51.100.0/24 -j ACCEPT"
	const deny = "### tuple ### deny any any 0.0.0.0/0 any 198.51.100.7 in\n-A ufw-user-input -s 198.51.100.7 -j DROP"
	const deny6 = "### tuple ### deny tcp 12443 ::/0 any 2001:db8:1::/64 in\n-A ufw6-user-input -p tcp --dport 12443 -s 2001:db8:1::/64 -j DROP"
	const docker = "-P INPUT ACCEPT\n-P FORWARD DROP\n-P OUTPUT ACCEPT\n-N DOCKER-USER\n-A FORWARD -j DOCKER-USER\n"
	const guard = "FORWARD -m comment --comment lpanel-ufw-stop -j DROP"
	for _, tc := range []struct {
		name         string
		v4, v6       []string
		source, dest string
		proxy, host  string
		native       string
		nft          string
		change       func(fstest.MapFS)
		addFail      bool
		enableFail   bool
		wantCode     int
		wantWrites   []string
	}{
		{name: "补齐双地址族", wantCode: 200, wantWrites: []string{"allow 12443/tcp", "--force enable"}},
		{name: "旧代理即使传入地址也不可信", proxy: "1", v4: []string{scoped}, wantCode: 409},
		{name: "更早的代理未传入口端口", host: "panel.example.com", v4: []string{scoped}, wantCode: 409},
		{name: "实际端口与配置不符", host: "panel.example.com:8443", wantCode: 409},
		{name: "本机转发不能证明外部连接可达", source: "127.0.0.1", wantCode: 409},
		{name: "已有其他入站链", native: "-P INPUT ACCEPT\n-A INPUT -j local-input\n", wantCode: 409},
		{name: "不覆盖现有入站策略", native: "-P INPUT DROP\n", wantCode: 409},
		{name: "保留与配置一致的 Docker 转发策略和链", native: docker, wantCode: 200, wantWrites: []string{"allow 12443/tcp", "--force enable"}},
		{name: "Docker 转发在失败回退中仍默认拒绝", native: docker, v4: []string{allow}, enableFail: true, wantCode: 500, wantWrites: []string{"--force enable", "iptables -A " + guard, "--force disable", "iptables -P FORWARD DROP", "iptables -D " + guard}},
		{name: "停用留下的默认空链允许再次启用", native: "-P INPUT ACCEPT\n-N ufw-before-logging-input\n-A INPUT -j ufw-before-logging-input\n-A INPUT -j ufw-before-input\n-A INPUT -j ufw-after-input\n-A INPUT -j ufw-after-logging-input\n-A INPUT -j ufw-reject-input\n-A INPUT -j ufw-track-input\n", wantCode: 200, wantWrites: []string{"allow 12443/tcp", "--force enable"}},
		{name: "独立 nftables 入站链", nft: `{"nftables":[{"chain":{"family":"inet","table":"local","name":"input","hook":"input"}}]}`, wantCode: 409},
		{name: "自定义内核参数可能切断 IPv6", change: func(files fstest.MapFS) {
			files["etc/ufw/sysctl.conf"].Data = []byte("net/ipv6/conf/all/disable_ipv6=1\n")
		}, wantCode: 409},
		{name: "已有相符来源和目标的范围规则", v4: []string{scoped, deny}, wantCode: 200, wantWrites: []string{"--force enable"}},
		{name: "优先拒绝", v4: []string{deny, allow}, wantCode: 409},
		{name: "不放宽现有来源", v4: []string{scoped}, source: "203.0.113.7", wantCode: 409},
		{name: "不忽略目标地址限制", v4: []string{scoped}, dest: "192.0.2.30", wantCode: 409},
		{name: "IPv6 拒绝不使用 IPv4 放行", v4: []string{allow}, v6: []string{deny6, allow6}, source: "2001:db8:1::7", dest: "2001:db8:2::20", wantCode: 409},
		{name: "启用前拒绝已修改的前置配置", v4: []string{allow}, change: func(files fstest.MapFS) { files["etc/ufw/before.rules"].Data = []byte("-A ufw-before-input -j DROP\n") }, wantCode: 409},
		{name: "启用前拒绝可执行的钩子", v4: []string{allow}, change: func(files fstest.MapFS) { files["etc/ufw/before.init"].Mode = 0750 }, wantCode: 409},
		{name: "未登记的输入规则", v4: []string{"-I ufw-user-input -j DROP", allow}, wantCode: 409},
		{name: "规则写入失败", addFail: true, wantCode: 500, wantWrites: []string{"allow 12443/tcp"}},
		{name: "部分启用失败后停用", v4: []string{allow}, enableFail: true, wantCode: 500, wantWrites: []string{"--force enable", "--force disable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := testApp(t)
			a.Config.PanelPort = 12443
			files := firewallFixtureFiles()
			a.firewallFS = files
			files["etc/ufw/user.rules"] = firewallFixtureRules("ufw", tc.v4...)
			files["etc/ufw/user6.rules"] = firewallFixtureRules("ufw6", tc.v6...)
			if tc.change != nil {
				tc.change(files)
			}
			active := false
			var writes []string
			a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "nft" && slices.Equal(args, []string{"-j", "list", "chains"}) {
					if tc.nft != "" {
						return []byte(tc.nft), nil
					}
					return nil, exec.ErrNotFound
				}
				if name == "iptables" || name == "ip6tables" {
					if slices.Equal(args, []string{"-S"}) || slices.Equal(args, []string{"-S", "FORWARD"}) {
						if name == "iptables" && tc.native != "" {
							return []byte(tc.native), nil
						}
						return []byte("-P INPUT ACCEPT\n-P FORWARD ACCEPT\n-P OUTPUT ACCEPT\n"), nil
					}
					writes = append(writes, name+" "+strings.Join(args, " "))
					return nil, nil
				}
				if name != "ufw" {
					t.Fatalf("非预期命令：%s %v", name, args)
				}
				command := strings.Join(args, " ")
				if command == "status" {
					if active {
						return []byte("Status: active\n"), nil
					}
					return []byte("Status: inactive\n"), nil
				}
				writes = append(writes, command)
				switch command {
				case "allow 12443/tcp":
					if tc.addFail {
						return nil, errors.New("写入失败")
					}
					files["etc/ufw/user.rules"] = firewallFixtureRules("ufw", allow)
					files["etc/ufw/user6.rules"] = firewallFixtureRules("ufw6", allow6)
				case "--force enable":
					active = true
					files["etc/ufw/ufw.conf"].Data = []byte("ENABLED=yes\nLOGLEVEL=low\n")
					if tc.enableFail {
						return nil, errors.New("规则仅部分加载")
					}
				case "--force disable":
					active = false
					files["etc/ufw/ufw.conf"].Data = []byte("ENABLED=no\nLOGLEVEL=low\n")
				default:
					t.Fatalf("非预期防火墙命令：%s", command)
				}
				return nil, nil
			}
			login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
			request := httptest.NewRequestWithContext(t.Context(), "POST", firewallAPI+"/enable", nil)
			request.RemoteAddr = "127.0.0.1:44444"
			request.AddCookie(login.Result().Cookies()[0])
			request.Host = "panel.example.com:12443"
			if tc.host != "" {
				request.Host = tc.host
			}
			request.Header.Set("X-LPanel-Proxy", "2")
			if tc.proxy != "" {
				request.Header.Set("X-LPanel-Proxy", tc.proxy)
			}
			src, dst := "198.51.100.7", "192.0.2.20"
			if tc.source != "" {
				src = tc.source
			}
			if tc.dest != "" {
				dst = tc.dest
			}
			request.Header.Set("X-LPanel-Client-IP", src)
			request.Header.Set("X-LPanel-Server-IP", dst)
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, request)
			if w.Code != tc.wantCode || !slices.Equal(writes, tc.wantWrites) || active != (tc.wantCode == 200) {
				t.Fatalf("状态 %d，运行 %v，写命令 %v：%s", w.Code, active, writes, w.Body.String())
			}
		})
	}
}

func TestServerFirewallDelete(t *testing.T) {
	a, _ := testApp(t)
	a.Config.PanelPort = 12443
	files := firewallFixtureFiles()
	a.firewallFS = files
	files["etc/ufw/ufw.conf"].Data = []byte("ENABLED=yes\nLOGLEVEL=low\n")
	const panel = "### tuple ### allow tcp 12443 0.0.0.0/0 any 0.0.0.0/0 in\n-A ufw-user-input -p tcp --dport 12443 -j ACCEPT"
	const other = "### tuple ### deny udp 1234 ::/0 any ::/0 in\n-A ufw6-user-input -p udp --dport 1234 -j DROP"
	files["etc/ufw/user.rules"] = firewallFixtureRules("ufw", panel)
	files["etc/ufw/user6.rules"] = firewallFixtureRules("ufw6", other)
	var writes []string
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ufw" {
			t.Fatalf("非预期命令：%s", name)
		}
		if slices.Equal(args, []string{"status"}) {
			return []byte("Status: active\n"), nil
		}
		writes = append(writes, strings.Join(args, " "))
		files["etc/ufw/user6.rules"] = firewallFixtureRules("ufw6")
		return nil, nil
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	if response := call(a, "DELETE", firewallAPI+"/rules?id=anything", "", nil); response.Code != 401 {
		t.Fatalf("未登录的删除状态：%d", response.Code)
	}
	if response := call(a, "POST", firewallAPI+"/rules", `{"port":12443,"protocol":"tcp","action":"deny"}`, cookie); response.Code != 409 {
		t.Fatalf("允许拒绝面板：%s", response.Body.String())
	}
	var state firewallState
	response := call(a, "GET", firewallAPI, "", cookie)
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || len(state.Rules) != 2 || state.Rules[0].Removable || !state.Rules[1].Removable {
		t.Fatalf("读取规则失败：%s，%v", response.Body.String(), err)
	}
	if w := call(a, "DELETE", firewallAPI+"/rules?id="+url.QueryEscape(state.Rules[0].ID), "", cookie); w.Code != 409 {
		t.Fatalf("允许删除面板规则：%s", w.Body.String())
	}
	if w := call(a, "DELETE", firewallAPI+"/rules?id="+url.QueryEscape(state.Rules[1].ID), "", cookie); w.Code != 200 {
		t.Fatalf("删除 IPv6 规则失败：%s", w.Body.String())
	}
	if !slices.Equal(writes, []string{"--force delete deny proto udp from ::/0 to ::/0 port 1234"}) {
		t.Fatalf("删除命令：%v", writes)
	}
}

func TestServerFirewallStatusFailure(t *testing.T) {
	a, _ := testApp(t)
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	a.Command = func(context.Context, string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("ufw：%w", exec.ErrNotFound)
	}
	var state firewallState
	response := call(a, "GET", firewallAPI, "", cookie)
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || response.Code != 200 || state.Available || state.Rules == nil {
		t.Fatalf("缺少 UFW 应返回不可用：%s", response.Body.String())
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("读取失败") }
	if response = call(a, "GET", firewallAPI, "", cookie); response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "firewall_status_failed") {
		t.Fatalf("执行失败不能显示为已停用：%s", response.Body.String())
	}
}

func TestServerFirewallPackageDefaults(t *testing.T) {
	root := os.Getenv("LPANEL_UFW_QA_ROOT")
	if root == "" {
		t.Skip("仅在提供解包后的 UFW 目标镜像时检查实际默认配置")
	}
	files := os.DirFS(root)
	if _, err := readFirewallUFWConfig(files); err != nil {
		t.Fatal(err)
	}
	if err := checkFirewallUFWSetup(files, true, true); err != nil {
		t.Fatal(err)
	}
	for _, ipv6 := range []bool{false, true} {
		rules, safe, err := readFirewallUserRules(files, ipv6)
		if err != nil || !safe || len(rules) != 0 {
			t.Fatalf("包默认的用户规则无法读取，IPv6=%v，规则数=%d，安全=%v：%v", ipv6, len(rules), safe, err)
		}
	}
}
