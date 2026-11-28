package panel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPanelProxyTemplateUpgrade(t *testing.T) {
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
	const old = managedNginxHeader + "server {}\n"
	for _, contents := range []string{"", "# 自定义配置\n", old} {
		if contents != "" {
			if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.refreshPanelProxy(t.Context()); err != nil {
			t.Fatal(err)
		}
		if contents != old && reloads != 0 {
			t.Fatal("无托管配置时不应重载 Nginx")
		}
		if contents == "" {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("启动不应创建缺失的配置：%v", err)
			}
		} else if contents != old {
			if data, err := os.ReadFile(path); err != nil || string(data) != contents {
				t.Fatalf("启动不应改写自定义配置：%v", err)
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "location = /api/v1/projects/import {") {
		t.Fatalf("旧面板配置未补齐导入规则：%v", err)
	}
	if !strings.Contains(string(data), programLogStreamLocation+" proxy_buffering off;") {
		t.Fatal("日志跟踪的代理规则应关闭缓冲")
	}
	if !strings.Contains(string(data), "location = "+mcpPath+" { client_max_body_size 7m; proxy_pass http://"+a.Config.Listen+"; proxy_set_header Host $http_host;") {
		t.Fatal("MCP 文本编辑须经过面板代理，并保留客户端访问端口")
	}
	previousRevision := strings.Replace(string(data), nginxTemplateRevision, "# LPanel Nginx 配置模板修订号：1\n", 1)
	if err := os.WriteFile(path, []byte(previousRevision), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshPanelProxy(t.Context()); err != nil || reloads != 2 {
		t.Fatalf("已有导入和日志规则的面板也应更新配置：%v，已重载 %d 次", err, reloads)
	}
	if err := a.refreshPanelProxy(t.Context()); err != nil || reloads != 2 {
		t.Fatalf("再次启动时应保留已更新的配置：%v，已重载 %d 次", err, reloads)
	}
}

func TestPanelProxyUpgradeFailure(t *testing.T) {
	for _, step := range []string{"检查", "重载", "回读"} {
		t.Run(step, func(t *testing.T) {
			a := domainApp(t)
			path := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
			const old = managedNginxHeader + "server {}\n"
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(old), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := a.DB.Exec("INSERT INTO operations(id,kind,status,created_at,updated_at) VALUES('test','upgrade','running','','')"); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("新配置未生效")
			active := true
			activeChecks := 0
			reloads := 0
			a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
				switch name + " " + strings.Join(args, " ") {
				case "nginx -t":
					if step == "检查" {
						return nil, failure
					}
				case "systemctl reload nginx":
					reloads++
					if step == "重载" && reloads%2 == 1 {
						return nil, failure
					}
				case "systemctl is-active --quiet lpanel-upgrade-test.service":
					activeChecks++
					if !active {
						return nil, errors.New("升级工作单元已退出")
					}
				}
				return nil, nil
			}
			a.NginxProbe = func(context.Context, string, string, string) error {
				if step == "回读" {
					return failure
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := a.Serve(ctx); !errors.Is(err, failure) {
				t.Fatalf("升级期间应在启动前返回配置错误：%v", err)
			}
			active = false
			if err := a.runUpgradeActions(t.Context()); err != nil {
				t.Fatalf("升级工作单元不活动时应继续正常启动：%v", err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != old {
				t.Fatalf("代理配置刷新失败后未恢复旧配置：%q，%v", data, err)
			}
			if activeChecks != 2 {
				t.Fatalf("应在每次刷新失败后检查一次工作单元，共检查 %d 次", activeChecks)
			}
			wantReloads := 4
			if step == "检查" {
				wantReloads = 0
			}
			if reloads != wantReloads {
				t.Fatalf("预期重载 %d 次，实际 %d 次", wantReloads, reloads)
			}
		})
	}
}
