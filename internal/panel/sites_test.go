package panel

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSiteUpdateSynchronizesBindingsAndRollback(t *testing.T) {
	a := domainApp(t)
	ctx := context.Background()
	directory := t.TempDir()
	site, err := a.SaveSite(ctx, Site{Name: "官网", Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := a.SaveBinding(ctx, Binding{Kind: "site", ResourceID: site.ID, Domain: "site.example.com", Directory: site.Directory})
	if err != nil {
		t.Fatal(err)
	}
	next := t.TempDir()
	site.Directory = next
	site.SPA = true
	if _, err = a.SaveSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	saved, _ := a.binding(ctx, binding.ID)
	if saved.Directory != next || !saved.SPA {
		t.Fatal("网站编辑未同步域名")
	}
	configPath := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
	config, _ := os.ReadFile(configPath)
	if !strings.Contains(string(config), "try_files $uri $uri/ /index.html") || !strings.Contains(string(config), next) {
		t.Fatal("网站配置未更新")
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, fmt.Errorf("配置错误") }
	site.Directory = directory
	site.SPA = false
	if _, err = a.SaveSite(ctx, site); err == nil {
		t.Fatal("无效配置被标记成功")
	}
	savedSite, _ := a.site(ctx, site.ID)
	savedBinding, _ := a.binding(ctx, binding.ID)
	if savedSite.Directory != next || !savedSite.SPA || savedBinding.Directory != next || !savedBinding.SPA {
		t.Fatal("失败未恢复旧网站和绑定")
	}
	current, _ := os.ReadFile(configPath)
	if string(current) != string(config) {
		t.Fatal("失败未恢复旧Nginx配置")
	}
}

func TestSiteDeleteKeepsContentAndRestoresOnFailure(t *testing.T) {
	a := domainApp(t)
	ctx := context.Background()
	directory := t.TempDir()
	content := filepath.Join(directory, "index.html")
	if err := os.WriteFile(content, []byte("网站内容"), 0644); err != nil {
		t.Fatal(err)
	}
	site, err := a.SaveSite(ctx, Site{Name: "官网", Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.SaveBinding(ctx, Binding{Kind: "site", ResourceID: site.ID, Domain: "site.example.com", Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, fmt.Errorf("重载失败") }
	if err = a.DeleteSite(ctx, site.ID); err == nil {
		t.Fatal("删除失败未返回错误")
	}
	if _, err = a.site(ctx, site.ID); err != nil {
		t.Fatal("失败删除网站记录")
	}
	if _, err = a.binding(ctx, b.ID); err != nil {
		t.Fatal("失败未恢复绑定")
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	if err = a.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.site(ctx, site.ID); err == nil {
		t.Fatal("网站未删除")
	}
	if _, err = a.binding(ctx, b.ID); err == nil {
		t.Fatal("域名绑定未删除")
	}
	if data, err := os.ReadFile(content); err != nil || string(data) != "网站内容" {
		t.Fatal("内容被删除")
	}
}

func TestSiteAPIValidationAndBinding(t *testing.T) {
	a := domainApp(t)
	if w := call(a, "GET", "/api/v1/sites", "", nil); w.Code != 401 {
		t.Fatal("未登录可访问网站")
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	for _, body := range []string{`{"name":"官网","directory":"relative"}`, `{"name":"官网","directory":"/does-not-exist-lpanel"}`, `{"name":"","directory":"/"}`} {
		if w := call(a, "POST", "/api/v1/sites", body, cookie); w.Code != 400 {
			t.Fatalf("错误输入被接受：%s", w.Body.String())
		}
	}
	data, _ := json.Marshal(Site{Name: "官网", Directory: t.TempDir(), SPA: true})
	w := call(a, "POST", "/api/v1/sites", string(data), cookie)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var s Site
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	w = call(a, "POST", "/api/v1/sites/"+s.ID+"/bindings", `{"domain":"www.example.com","auto_certificate":false}`, cookie)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		Binding   Binding `json:"binding"`
		Operation any     `json:"operation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Binding.Directory != s.Directory || !result.Binding.SPA || result.Binding.Kind != "site" || result.Operation != nil {
		t.Fatal("绑定契约错误")
	}
	w = call(a, "GET", "/api/v1/sites", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), s.ID) {
		t.Fatal("网站列表错误")
	}
}

func TestSiteCreateAllocatesDirectoryAndBindsHTTP(t *testing.T) {
	a := domainApp(t)
	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	id := ""
	for index, body := range []string{
		`{"name":"中文官网"}`,
		`{"name":"中文官网","directory":" ","spa":true,"binding":{"domain":" Blog.Example.com. "}}`,
	} {
		w := call(a, http.MethodPost, "/api/v1/sites", body, cookie)
		if w.Code != http.StatusCreated {
			t.Fatal(w.Body.String())
		}
		var result siteSaveResult
		if err := jsonv2.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.ID == "" || result.ID == id || result.Directory != filepath.Join(a.Root, "sites", result.ID) {
			t.Fatalf("未分配独立的默认目录：%+v", result.Site)
		}
		id = result.ID
		for _, path := range []string{result.Directory, filepath.Dir(result.Directory)} {
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() || info.Mode().Perm()&0005 != 0005 {
				t.Fatalf("Nginx 无法遍历网站目录 %s：%v，%v", path, info, err)
			}
		}
		if index == 0 {
			if result.Binding != nil || result.Operation != nil {
				t.Fatal("未填写域名也创建了绑定或证书操作")
			}
			continue
		}
		b := result.Binding
		if b == nil || b.Domain != "blog.example.com" || b.ResourceID != id || b.Directory != result.Directory || !b.SPA || a.BindingURL(*b) != "http://blog.example.com" || result.Operation != nil {
			t.Fatalf("创建时的 HTTP 绑定有误：%+v", result)
		}
	}
}

func TestSiteCreateBindingFailureRollsBack(t *testing.T) {
	a := domainApp(t)
	ctx := t.Context()
	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	existing, err := a.SaveSite(ctx, Site{Name: "原网站"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.SaveBinding(ctx, existing.domainBinding(siteBindingInput{Domain: "taken.example.com"})); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
	oldConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	a.Config.AgreeTOS = false
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("Nginx 重载失败") }
	custom := t.TempDir()
	content := filepath.Join(custom, "index.html")
	if err := os.WriteFile(content, []byte("原网页"), 0640); err != nil {
		t.Fatal(err)
	}
	customRequest, err := jsonv2.Marshal(struct {
		Site
		Binding siteBindingInput `json:"binding"`
	}{Site: Site{Name: "自定义目录", Directory: custom}, Binding: siteBindingInput{Domain: "custom.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	for index, body := range []string{
		`{"name":"重复域名","binding":{"domain":"taken.example.com"}}`,
		`{"name":"证书条款","binding":{"domain":"terms.example.com","auto_certificate":true}}`,
		`{"name":"重载失败","binding":{"domain":"new.example.com"}}`,
		string(customRequest),
	} {
		w := call(a, http.MethodPost, "/api/v1/sites", body, cookie)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("创建应失败：%s", w.Body.String())
		}
		if index == 0 && !strings.Contains(w.Body.String(), "该域名已被使用") {
			t.Fatalf("重复域名未返回明确提示：%s", w.Body.String())
		}
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	panelConfig := filepath.Join(a.Root, "data", "config.json")
	if err := os.Remove(panelConfig); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(panelConfig, 0700); err != nil {
		t.Fatal(err)
	}
	w := call(a, http.MethodPost, "/api/v1/sites", `{"name":"面板配置写入失败","binding":{"domain":"persist.example.com"}}`, cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("面板配置写入失败时创建应失败：%s", w.Body.String())
	}
	directories, err := os.ReadDir(filepath.Join(a.Root, "sites"))
	if err != nil || len(directories) != 1 || directories[0].Name() != existing.ID {
		t.Fatalf("绑定失败留下了默认目录：%v，%v", directories, err)
	}
	var siteCount int
	if err := a.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sites").Scan(&siteCount); err != nil || siteCount != 1 {
		t.Fatalf("绑定失败留下了网站记录：%d，%v", siteCount, err)
	}
	bindings, err := a.ListBindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].Domain != "taken.example.com" {
		t.Fatalf("绑定失败未保留原域名配置：%v，%v", bindings, err)
	}
	if current, err := os.ReadFile(configPath); err != nil || string(current) != string(oldConfig) {
		t.Fatal("绑定失败未恢复原 Nginx 配置")
	}
	if current, err := os.ReadFile(content); err != nil || string(current) != "原网页" {
		t.Fatal("绑定失败移除了自定义目录的内容")
	}
}

func TestSiteCreateCertificateFailureKeepsHTTP(t *testing.T) {
	a := domainApp(t)
	ctx := t.Context()
	a.Command = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "certbot" {
			return []byte("域名验证失败"), errors.New("证书签发失败")
		}
		return nil, nil
	}
	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	w := call(a, http.MethodPost, "/api/v1/sites", `{"name":"申请 HTTPS","binding":{"domain":"blog.example.com","auto_certificate":true}}`, cookie)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	var result siteSaveResult
	if err := jsonv2.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Binding == nil || a.BindingURL(*result.Binding) != "http://blog.example.com" || result.Operation == nil || result.Operation.Kind != "certificate" {
		t.Fatalf("返回的绑定或证书操作有误：%+v", result)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		var status string
		err := a.DB.QueryRowContext(ctx, "SELECT status FROM operations WHERE id=?", result.Operation.ID).Scan(&status)
		if err != nil {
			t.Fatal(err)
		}
		if status == "failed" {
			break
		}
		if status != "running" || time.Now().After(deadline) {
			t.Fatalf("证书操作未正确标记失败：%s", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	saved, err := a.binding(ctx, result.Binding.ID)
	if err != nil || saved.CertificateStatus != "failed" || !strings.Contains(saved.CertificateError, "域名验证失败") {
		t.Fatalf("签发失败的原因未保存：%+v，%v", saved, err)
	}
	config, err := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
	if err != nil || !strings.Contains(string(config), "server_name blog.example.com") || strings.Contains(string(config), "ssl_certificate") {
		t.Fatal("签发失败没有保留 HTTP 配置")
	}
	if _, err := a.site(ctx, result.ID); err != nil {
		t.Fatal("签发失败没有保留网站：", err)
	}
}

func TestSiteCreateCertificateQueueFailureKeepsHTTP(t *testing.T) {
	a := domainApp(t)
	if _, err := a.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_certificate BEFORE INSERT ON operations
		WHEN NEW.kind = 'certificate' BEGIN SELECT RAISE(ABORT, '测试证书操作写入失败'); END`); err != nil {
		t.Fatal(err)
	}
	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	w := call(a, http.MethodPost, "/api/v1/sites", `{"name":"证书待重试","binding":{"domain":"blog.example.com","auto_certificate":true}}`, cookie)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	var result siteSaveResult
	if err := jsonv2.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID == "" || result.Binding == nil || a.BindingURL(*result.Binding) != "http://blog.example.com" || result.Operation != nil || !strings.Contains(result.CertificateError, "重新申请") {
		t.Fatalf("证书操作写入失败时未返回可重试的 HTTP 网站：%+v", result)
	}
	binding, err := a.binding(t.Context(), result.Binding.ID)
	if err != nil || binding.ResourceID != result.ID || binding.CertificateStatus != "disabled" {
		t.Fatalf("证书操作写入失败时未保留 HTTP 绑定：%+v，%v", binding, err)
	}
}
