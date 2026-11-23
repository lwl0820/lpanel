package panel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectImportProxyUpgrade(t *testing.T) {
	a := domainApp(t)
	path := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	a.Command = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "systemctl" {
			reloads++
		}
		return nil, nil
	}
	const old = "# 本文件由 LPanel 管理\nserver {}\n"
	for _, contents := range []string{"", "# 自定义配置\n", old} {
		if contents != "" {
			if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.refreshProjectImportProxy(t.Context()); err != nil {
			t.Fatal(err)
		}
		if contents != old && reloads != 0 {
			t.Fatal("无托管配置时不应重载 Nginx")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "location = /api/v1/projects/import {") {
		t.Fatalf("旧面板配置未补齐导入规则：%v", err)
	}
	if err := a.refreshProjectImportProxy(t.Context()); err != nil || reloads != 1 {
		t.Fatalf("再次启动时应保留已更新的配置：%v，已重载 %d 次", err, reloads)
	}
	if err := os.WriteFile(path, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	a.NginxProbe = func(context.Context, string, string, string) error {
		return errors.New("新配置未生效")
	}
	if err := a.refreshProjectImportProxy(t.Context()); err == nil {
		t.Fatal("应报告代理配置刷新失败")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != old {
		t.Fatalf("代理配置刷新失败后未恢复旧配置：%v", err)
	}
}
