package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
