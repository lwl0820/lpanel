package panel

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagementAccessIPv6AndRootLogin(t *testing.T) {
	a := domainApp(t)
	backend := httptest.NewServer(a.Handler())
	defer backend.Close()
	a.Config.Listen = strings.TrimPrefix(backend.URL, "http://")
	var loginAddress, loginHost string
	a.NginxProbe = func(_ context.Context, address, host, marker string) error {
		if marker == "" {
			loginAddress, loginHost = address, host
		}
		return nil
	}
	ctx := t.Context()
	if _, err := a.SaveBinding(ctx, Binding{Kind: "project", Domain: "app.example.com", Upstream: "http://127.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
	result, err := a.ManageAccess(ctx, AccessChange{
		Host: new("[2001:0db8::1]"), Port: new(19443), LoginPath: new("/new-entry"),
	})
	if err != nil || result != "http://[2001:db8::1]:19443/new-entry" {
		t.Fatalf("返回的登录地址或结果错误：%q %v", result, err)
	}
	if loginAddress != "http://[::1]:19443/new-entry" || loginHost != "2001:db8::1" {
		t.Fatalf("本机登录验证未使用新入口：%q %q", loginAddress, loginHost)
	}
	current, err := LoadConfig(a.Root)
	if err != nil || current.PanelDomain != "2001:db8::1" || current.PanelPort != 19443 || current.LoginPath != "/new-entry" {
		t.Fatalf("配置保存失败：%+v %v", current, err)
	}
	data, err := os.ReadFile(filepath.Join(current.NginxConfigDir, "lpanel.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"listen [::]:19443", "server_name [2001:db8::1]", "X-LPanel-Redirect http://[2001:db8::1]:19443",
		"$remote_addr != ::1", "server_name app.example.com",
	} {
		if !strings.Contains(string(data), part) {
			t.Fatalf("未生成预期的配置 %q", part)
		}
	}
	result, err = a.ManageAccess(ctx, AccessChange{Host: new("auto"), LoginPath: new("/")})
	if err != nil || !strings.HasSuffix(result, ":19443/") || loginAddress != "http://127.0.0.1:19443/" {
		t.Fatalf("恢复默认入口失败：%q %q %v", result, loginAddress, err)
	}
	items, err := a.ListBindings(ctx)
	if err != nil || len(items) != 1 || items[0].Kind != "project" {
		t.Fatalf("恢复默认地址后的绑定不正确：%+v %v", items, err)
	}
}

func TestManagementAccessRestoresAfterFailedRestart(t *testing.T) {
	a := domainApp(t)
	backend := httptest.NewServer(a.Handler())
	defer backend.Close()
	a.Config.Listen = strings.TrimPrefix(backend.URL, "http://")
	ctx := t.Context()
	old, err := a.SaveBinding(ctx, Binding{Kind: "panel", Domain: "old.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	previous := a.currentConfig()
	restarts := 0
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "systemctl" && args[0] == "restart" {
			restarts++
			if restarts == 1 {
				return nil, fmt.Errorf("服务启动出错")
			}
		}
		return nil, nil
	}
	if _, err = a.ManageAccess(ctx, AccessChange{Host: new("new.example.com"), Port: new(19443)}); err == nil ||
		!strings.Contains(err.Error(), "服务启动出错") || strings.Contains(err.Error(), "恢复原面板设置失败") {
		t.Fatalf("未报告候选设置错误或旧配置恢复失败：%v", err)
	}
	current, err := LoadConfig(a.Root)
	if err != nil || current != previous {
		t.Fatalf("旧配置未恢复：%+v %v", current, err)
	}
	saved, err := a.binding(ctx, old.ID)
	if err != nil || saved != old || restarts != 2 {
		t.Fatalf("旧绑定未恢复或启动次数异常：%+v %d %v", saved, restarts, err)
	}
	data, err := os.ReadFile(filepath.Join(current.NginxConfigDir, "lpanel.conf"))
	if err != nil || !strings.Contains(string(data), "server_name old.example.com") || strings.Contains(string(data), "listen 19443") {
		t.Fatalf("旧的 Nginx 入口未恢复：%v\n%s", err, data)
	}
}

func TestManagementAccessCertificateFailureKeepsPreviousHTTPS(t *testing.T) {
	a := domainApp(t)
	ctx := t.Context()
	old, err := a.SaveBinding(ctx, Binding{Kind: "panel", Domain: "old.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	old.CertificateStatus = "enabled"
	if err = a.storeBinding(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err = a.ApplyNginx(ctx); err != nil {
		t.Fatal(err)
	}
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "certbot" {
			t.Fatalf("签发失败前不应切换入口：%s %v", name, args)
		}
		return nil, errors.New("地址验证失败")
	}
	if _, err = a.ManageAccess(ctx, AccessChange{Host: new("new.example.com")}); err == nil || !strings.Contains(err.Error(), "地址验证失败") {
		t.Fatalf("错误签发结果：%v", err)
	}
	current, err := LoadConfig(a.Root)
	if err != nil || current.PanelDomain != old.Domain || !current.HTTPS {
		t.Fatalf("原 HTTPS 入口未保留：%+v %v", current, err)
	}
	saved, err := a.binding(ctx, old.ID)
	if err != nil || saved != old {
		t.Fatalf("原 HTTPS 绑定未保留：%+v %v", saved, err)
	}
}
