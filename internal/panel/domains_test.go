package panel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func domainApp(t *testing.T) *App {
	t.Helper()
	a, _ := testApp(t)
	a.Config.NginxConfigDir = filepath.Join(a.Root, "nginx")
	a.Config.AgreeTOS = true
	if err := a.saveLegacyAccessConfig(t.Context(), a.Config); err != nil {
		t.Fatal(err)
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	a.NginxProbe = func(context.Context, string, string, string) error { return nil }
	return a
}
func TestBindingConfigAndRollback(t *testing.T) {
	a := domainApp(t)
	ctx := context.Background()
	b, err := a.SaveBinding(ctx, Binding{Kind: "project", Domain: "app.example.com", Upstream: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
	old, _ := os.ReadFile(path)
	for _, part := range []string{"proxy_pass http://127.0.0.1:8080", "proxy_set_header Upgrade", "/.well-known/acme-challenge/", "listen 8443", "current/web"} {
		if !strings.Contains(string(old), part) {
			t.Fatalf("缺少配置 %s", part)
		}
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("无效配置"), fmt.Errorf("检查失败")
	}
	b.Upstream = "http://127.0.0.1:9090"
	if _, err = a.SaveBinding(ctx, b); err == nil {
		t.Fatal("检查失败却保存成功")
	}
	current, _ := os.ReadFile(path)
	if string(current) != string(old) {
		t.Fatal("旧配置未恢复")
	}
	saved, _ := a.binding(ctx, b.ID)
	if saved.Upstream != "http://127.0.0.1:8080" {
		t.Fatal("绑定未恢复")
	}
}
func TestCertificateFailureKeepsHTTP(t *testing.T) {
	a := domainApp(t)
	ctx := context.Background()
	b, err := a.SaveBinding(ctx, Binding{Kind: "panel", Domain: "panel.example.com", AutoCertificate: true})
	if err != nil {
		t.Fatal(err)
	}
	a.Command = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "certbot" {
			return []byte("域名验证失败"), fmt.Errorf("验证失败")
		}
		return nil, nil
	}
	if err = a.RequestCertificate(ctx, b.ID); err == nil {
		t.Fatal("错误签发成功")
	}
	saved, _ := a.binding(ctx, b.ID)
	if saved.CertificateStatus != "failed" || !strings.Contains(saved.CertificateError, "域名验证失败") {
		t.Fatalf("失败状态错误：%+v", saved)
	}
	if a.Config.HTTPS {
		t.Fatal("失败却启用HTTPS")
	}
	data, _ := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
	if strings.Contains(string(data), "ssl_certificate") {
		t.Fatal("失败却写入HTTPS配置")
	}
}
func TestCertificateEnablesOnlyAfterReload(t *testing.T) {
	a := domainApp(t)
	ctx := context.Background()
	b, err := a.SaveBinding(ctx, Binding{Kind: "panel", Domain: "panel.example.com", AutoCertificate: true})
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: b.Domain}, DNSNames: []string{b.Domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "certbot" {
			joined := strings.Join(args, " ")
			for _, flag := range []string{"--webroot", "--cert-name panel.example.com", "--config-dir", "--work-dir", "--logs-dir"} {
				if !strings.Contains(joined, flag) {
					t.Errorf("缺少证书参数 %s", flag)
				}
			}
			dir := filepath.Join(a.Root, "data/acme/live", b.Domain)
			if err = os.MkdirAll(dir, 0700); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(dir, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
		}
		return nil, nil
	}
	if err = a.RequestCertificate(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	saved, _ := a.binding(ctx, b.ID)
	if saved.CertificateStatus != "enabled" || saved.ExpiresAt == "" || !a.Config.HTTPS {
		t.Fatal("证书未生效")
	}
	data, _ := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
	if !strings.Contains(string(data), "listen 8443 ssl") || !strings.Contains(string(data), "listen 80") {
		t.Fatal("自定义TLS端口或验证端口缺失")
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("续期失败"), fmt.Errorf("失败")
	}
	if err = a.RequestCertificate(ctx, b.ID); err == nil {
		t.Fatal("失败未上报")
	}
	saved, _ = a.binding(ctx, b.ID)
	if saved.CertificateStatus != "enabled" || !a.Config.HTTPS {
		t.Fatal("失败关闭了已有HTTPS")
	}
}
func TestBindingValidationAndDeletion(t *testing.T) {
	a := domainApp(t)
	ctx := context.Background()
	if _, err := a.SaveBinding(ctx, Binding{Kind: "project", Domain: "*.example.com", Upstream: "http://127.0.0.1:80"}); err == nil {
		t.Fatal("允许泛域名")
	}
	a.Config.AgreeTOS = false
	if err := a.saveLegacyAccessConfig(t.Context(), a.Config); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveBinding(ctx, Binding{Kind: "panel", Domain: "panel.example.com", AutoCertificate: true}); err == nil {
		t.Fatal("未接受条款允许申请")
	}
	dir := filepath.Join(a.Root, "website")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	b, err := a.SaveBinding(ctx, Binding{Kind: "site", Domain: "site.example.com", Directory: dir, SPA: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
	if !strings.Contains(string(data), "try_files $uri $uri/ /index.html") {
		t.Fatal("SPA回退未写入")
	}
	if err = a.DeleteBinding(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); err != nil {
		t.Fatal("删除绑定移除了目录")
	}
	if call(a, "GET", "/api/v1/bindings", "", nil).Code != 401 {
		t.Fatal("未登录可查询绑定")
	}
}

func TestCLIOpenDoesNotInterruptServiceOperations(t *testing.T) {
	a := domainApp(t)
	for _, value := range []struct{ id, kind string }{{"active-deploy", "deploy"}, {"active-upgrade", "upgrade"}} {
		if _, err := a.DB.Exec("INSERT INTO operations(id,kind,status,created_at,updated_at) VALUES(?,?,'running','','')", value.id, value.kind); err != nil {
			t.Fatal(err)
		}
	}
	// 续期等 CLI 复用 Open，不应改变仍在主服务执行的操作。
	second, err := Open(a.Root, "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var status string
	if err = second.DB.QueryRow("SELECT status FROM operations WHERE id='active-deploy'").Scan(&status); err != nil || status != "running" {
		t.Fatal("CLI误标记主服务操作中断")
	}
	second.Config.Listen = "无效监听地址"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = second.Serve(ctx); err == nil {
		t.Fatal("应拒绝无效监听地址")
	}
	if err = second.DB.QueryRow("SELECT status FROM operations WHERE id='active-deploy'").Scan(&status); err != nil || status != "failed" {
		t.Fatal("主服务启动未标记中断操作")
	}
	if err = second.DB.QueryRow("SELECT status FROM operations WHERE id='active-upgrade'").Scan(&status); err != nil || status != "running" {
		t.Fatal("独立升级工作被误标中断")
	}
}

func TestReloadSignalWithoutActivationDoesNotCommit(t *testing.T) {
	for _, failure := range []string{"标识不匹配", "入口不可达", "请求超时"} {
		t.Run(failure, func(t *testing.T) {
			a := domainApp(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, err := a.SaveBinding(ctx, Binding{Kind: "panel", Domain: "panel.example.com"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
			old, _ := os.ReadFile(path)
			previous := a.Config
			restored := false
			a.Command = func(commandCtx context.Context, name string, args ...string) ([]byte, error) {
				if name == "systemctl" {
					data, _ := os.ReadFile(path)
					if string(data) == string(old) {
						if commandCtx.Err() != nil {
							t.Error("恢复继承了已取消context")
						}
						restored = true
					}
				}
				return nil, nil
			}
			b.Domain = "new.example.com"
			// 确认探针使用候选域名，而不是尚未提交的 Config.PanelDomain。
			a.NginxProbe = func(_ context.Context, address, host, marker string) error {
				if host != b.Domain || address != "http://127.0.0.1:8443/.lpanel/config" || marker == "" {
					t.Error("未使用候选域名、协议、端口或配置标识")
				}
				if failure == "请求超时" {
					cancel()
				}
				return fmt.Errorf("%s", failure)
			}
			if _, err = a.SaveBinding(ctx, b); err == nil {
				t.Fatal("reload返回0被误判为生效")
			}
			data, _ := os.ReadFile(path)
			if string(data) != string(old) || !restored {
				t.Fatal("旧配置未恢复并重载")
			}
			saved, err := a.binding(context.Background(), b.ID)
			if err != nil || saved.Domain != previous.PanelDomain {
				t.Fatal("旧绑定未恢复")
			}
			persisted, err := LoadConfig(a.Root)
			if err != nil || persisted != previous || a.Config != previous {
				t.Fatal("候选配置被提交")
			}
		})
	}
}

func TestCertificateActivationProbeFailurePreservesPreviousAccess(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			a := domainApp(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, err := a.SaveBinding(ctx, Binding{Kind: "panel", Domain: "panel.example.com", AutoCertificate: true})
			if err != nil {
				t.Fatal(err)
			}
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			template := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
			der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(a.Root, "data/acme/live", b.Domain)
			if err = os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
			if enabled {
				if err = a.RequestCertificate(ctx, b.ID); err != nil {
					t.Fatal(err)
				}
			}
			old, _ := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
			previous := a.Config
			a.NginxProbe = func(_ context.Context, address, host, marker string) error {
				if !strings.HasPrefix(address, "https://127.0.0.1:8443/") {
					t.Errorf("未探测候选HTTPS：%s", address)
				}
				cancel()
				return fmt.Errorf("候选配置标识未生效")
			}
			if err = a.RequestCertificate(ctx, b.ID); err == nil {
				t.Fatal("未生效却报告证书成功")
			}
			data, _ := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
			if string(data) != string(old) {
				t.Fatal("旧入口配置被覆盖")
			}
			if a.Config != previous {
				t.Fatal("配置HTTPS状态被错误提交")
			}
			saved, err := a.binding(context.Background(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			expected := "failed"
			if enabled {
				expected = "enabled"
			}
			if saved.CertificateStatus != expected || saved.CertificateError == "" {
				t.Fatalf("旧证书状态未保留：%+v", saved)
			}
		})
	}
}

func TestLocalNginxProbeRequiresMatchingMarker(t *testing.T) {
	marker := "本次配置标识"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "panel.example.com" || r.URL.Path != "/.lpanel/config" {
			t.Error("探针主机或路径错误")
		}
		if r.TLS == nil || r.TLS.ServerName != "panel.example.com" {
			t.Error("探针未使用候选域名作为 TLS SNI")
		}
		_, _ = io.WriteString(w, marker)
	}))
	defer server.Close()
	if err := probeNginx(context.Background(), server.URL+"/.lpanel/config", "panel.example.com", marker); err != nil {
		t.Fatalf("本机测试证书应可确认标识：%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := probeNginx(ctx, server.URL+"/.lpanel/config", "panel.example.com", "另一版本"); err == nil {
		t.Fatal("旧标识被误判为新配置")
	}
}

func TestNginxCommitUsesCandidateMarker(t *testing.T) {
	a := domainApp(t)
	previousMarker := ""
	a.NginxProbe = func(_ context.Context, address, host, marker string) error {
		if address != "http://127.0.0.1:8443/.lpanel/config" || host != "panel.example.com" {
			t.Error("探针未使用候选入口")
		}
		if marker == "" || marker == previousMarker {
			t.Error("候选配置缺少独立标识")
		}
		data, err := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), fmt.Sprintf("location = /.lpanel/config { default_type text/plain; return 200 %q; }", marker)) {
			t.Error("确认标识未写入面板配置")
		}
		if previousMarker == "" && a.Config.PanelDomain != "" {
			t.Error("确认前提交了配置")
		}
		previousMarker = marker
		return nil
	}
	b, err := a.SaveBinding(context.Background(), Binding{Kind: "panel", Domain: "panel.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Config.PanelDomain != b.Domain {
		t.Fatal("确认成功后未提交配置")
	}
	if _, err = a.SaveBinding(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}
