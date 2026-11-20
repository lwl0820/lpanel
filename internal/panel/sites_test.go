package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSiteUpdateSynchronizesBindingsAndRollback(t *testing.T) {
	a := domainApp(t)
	ctx := t.Context()
	directory := t.TempDir()
	created, err := a.createSite(ctx, siteCreateInput{Identifier: "官网", Directory: directory, Hostnames: []string{"site.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	site, binding := created.Site, created.Bindings[0]
	next := t.TempDir()
	site.Directory = next
	site.SPA = true
	site.Note = "已更新备注"
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
	site.Note = "只更新备注"
	if _, err = a.SaveSite(ctx, site); err != nil {
		t.Fatalf("仅修改备注时不应依赖 Nginx：%v", err)
	}
	site.Directory = directory
	site.SPA = false
	site.Identifier = "修改失败的标识"
	site.Note = "修改失败的备注"
	if _, err = a.SaveSite(ctx, site); err == nil {
		t.Fatal("无效配置被标记成功")
	}
	savedSite, _ := a.site(ctx, site.ID)
	savedBinding, _ := a.binding(ctx, binding.ID)
	if savedSite.Directory != next || !savedSite.SPA || savedBinding.Directory != next || !savedBinding.SPA {
		t.Fatal("失败未恢复旧网站和绑定")
	}
	if savedSite.Identifier != "官网" || savedSite.Note != "只更新备注" {
		t.Fatalf("失败未恢复网站标识和备注：%+v", savedSite)
	}
	current, _ := os.ReadFile(configPath)
	if string(current) != string(config) {
		t.Fatal("失败未恢复旧Nginx配置")
	}
}

func TestSiteDeleteKeepsContentAndRestoresOnFailure(t *testing.T) {
	a := domainApp(t)
	ctx := t.Context()
	directory := t.TempDir()
	content := filepath.Join(directory, "index.html")
	if err := os.WriteFile(content, []byte("网站内容"), 0644); err != nil {
		t.Fatal(err)
	}
	created, err := a.createSite(ctx, siteCreateInput{Identifier: "官网", Directory: directory, Hostnames: []string{"site.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	site, b := created.Site, created.Bindings[0]
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
	for _, test := range []struct {
		body, hint string
	}{
		{`{"hostnames":["example.com"],"directory":"relative"}`, "绝对"},
		{`{"hostnames":["example.com"],"directory":"/does-not-exist-lpanel"}`, "目录"},
		{`{"identifier":"官网","hostnames":["","  "]}`, "主机名"},
		{`{"hostnames":["example.com","invalid/path"]}`, "invalid/path"},
		{`{"hostnames":["example.com"," EXAMPLE.COM. "]}`, "example.com"},
	} {
		if w := call(a, "POST", "/api/v1/sites", test.body, cookie); w.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(w.Body.String()), test.hint) {
			t.Fatalf("未提示新建网站输入错误：%s 返回 %d %s", test.body, w.Code, w.Body.String())
		}
	}
	data, err := json.Marshal(siteCreateInput{Identifier: "官网", Directory: t.TempDir(), SPA: true, Hostnames: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
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
	for _, test := range []struct {
		body, identifier, directory, note string
		hostnames                         []string
	}{
		{
			body:       `{"hostnames":[" "," Blog.Example.com. ","www.example.com"],"spa":true}`,
			identifier: "blog.example.com", directory: "blog.example.com",
			hostnames: []string{"blog.example.com", "www.example.com"},
		},
		{
			body:       `{"hostnames":[" *.Docs.Example.com. "],"auto_certificate":true,"spa":true}`,
			identifier: "*.docs.example.com", directory: "_.docs.example.com",
			hostnames: []string{"*.docs.example.com"},
		},
		{
			body:       `{"hostnames":["docs.example.com"],"identifier":"团队/官网","directory":" ","spa":true,"note":"团队文档"}`,
			identifier: "团队/官网", directory: "团队_官网", note: "团队文档",
			hostnames: []string{"docs.example.com"},
		},
	} {
		w := call(a, http.MethodPost, "/api/v1/sites", test.body, cookie)
		if w.Code != http.StatusCreated {
			t.Fatal(w.Body.String())
		}
		var result siteSaveResult
		if err := jsonv2.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.ID == "" || result.Identifier != test.identifier || result.Note != test.note ||
			result.Directory != filepath.Join(a.Root, "sites", test.directory) {
			t.Fatalf("未按网站标识分配默认目录：%+v", result.Site)
		}
		for _, path := range []string{result.Directory, filepath.Dir(result.Directory)} {
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() || info.Mode().Perm()&0005 != 0005 {
				t.Fatalf("Nginx 无法遍历网站目录 %s：%v，%v", path, info, err)
			}
		}
		if len(result.Bindings) != len(test.hostnames) || result.Operation != nil {
			t.Fatalf("创建时的 HTTP 绑定数量或证书操作有误：%+v", result)
		}
		for i, b := range result.Bindings {
			if b.Domain != test.hostnames[i] || b.ResourceID != result.ID || b.Directory != result.Directory ||
				!b.SPA || b.AutoCertificate || b.CertificateStatus != "disabled" {
				t.Fatalf("创建时的 HTTP 绑定有误：%+v", b)
			}
		}
	}
	content := filepath.Join(a.Root, "sites", "团队_官网", "index.html")
	if err := os.WriteFile(content, []byte("原网页"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ body, hint string }{
		{`{"hostnames":["duplicate-id.example.com"],"identifier":"团队/官网","directory":"/"}`, "网站标识"},
		{`{"hostnames":["duplicate-path.example.com"],"identifier":"团队\\官网"}`, "目录"},
	} {
		w := call(a, http.MethodPost, "/api/v1/sites", test.body, cookie)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), test.hint) {
			t.Fatalf("未提示标识或目录冲突：%d %s", w.Code, w.Body.String())
		}
	}
	if current, err := os.ReadFile(content); err != nil || string(current) != "原网页" {
		t.Fatalf("目录冲突影响了已有网页：%s，%v", current, err)
	}
}

func TestSiteCreateBindingFailureRollsBack(t *testing.T) {
	a := domainApp(t)
	ctx := t.Context()
	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	existing, err := a.createSite(ctx, siteCreateInput{Identifier: "原网站", Hostnames: []string{"taken.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
	oldConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	a.Config.AgreeTOS = false
	if err := a.saveLegacyAccessConfig(ctx, a.Config); err != nil {
		t.Fatal(err)
	}
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "systemctl" && slices.Contains(args, "reload") {
			return nil, errors.New("Nginx 重载失败")
		}
		return nil, nil
	}
	custom := t.TempDir()
	content := filepath.Join(custom, "index.html")
	if err := os.WriteFile(content, []byte("原网页"), 0640); err != nil {
		t.Fatal(err)
	}
	customRequest, err := jsonv2.Marshal(siteCreateInput{
		Identifier: "自定义目录", Directory: custom, Hostnames: []string{"custom.example.com", "custom2.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ body, hint string }{
		{`{"identifier":"重复域名","hostnames":["free.example.com","taken.example.com"]}`, "taken.example.com"},
		{`{"identifier":"证书条款","hostnames":["*.terms.example.com","terms.example.com"],"auto_certificate":true}`, "条款"},
		{`{"identifier":"重载失败","hostnames":["new.example.com","new2.example.com"]}`, "Nginx 重载失败"},
		{string(customRequest), "Nginx 重载失败"},
	} {
		w := call(a, http.MethodPost, "/api/v1/sites", test.body, cookie)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), test.hint) {
			t.Fatalf("创建未按预期失败：%d %s", w.Code, w.Body.String())
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
	w := call(a, http.MethodPost, "/api/v1/sites", `{"identifier":"面板配置写入失败","hostnames":["persist.example.com","persist2.example.com"]}`, cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("面板配置写入失败时创建应失败：%s", w.Body.String())
	}
	directories, err := os.ReadDir(filepath.Join(a.Root, "sites"))
	if err != nil || len(directories) != 1 || directories[0].Name() != filepath.Base(existing.Directory) {
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
	if err := os.Remove(panelConfig); err != nil {
		t.Fatal(err)
	}
	retried := call(a, http.MethodPost, "/api/v1/sites", `{"identifier":"重复域名","hostnames":["free.example.com","another.example.com"]}`, cookie)
	if retried.Code != http.StatusCreated {
		t.Fatalf("改正已被占用的主机名后无法重试：%d %s", retried.Code, retried.Body.String())
	}
}

func TestSiteCreateCertificateFailureKeepsHTTPAndContinues(t *testing.T) {
	a := domainApp(t)
	ctx := t.Context()
	certificate := acmeTestCertificate(t, "ok.example.com")
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "certbot" {
			return nil, nil
		}
		domainArg := slices.Index(args, "--cert-name")
		if domainArg < 0 || domainArg+1 >= len(args) {
			return nil, errors.New("Certbot 参数缺少域名")
		}
		switch args[domainArg+1] {
		case "fail.example.com":
			return []byte("域名验证失败"), errors.New("证书签发失败")
		case "ok.example.com":
			dir := a.certificateDir(legacyACMEID, "ok.example.com")
			if err := os.MkdirAll(dir, 0700); err != nil {
				return nil, err
			}
			return nil, os.WriteFile(filepath.Join(dir, "fullchain.pem"), certificate, 0600)
		default:
			t.Errorf("不应向 Certbot 请求此主机名：%v", args)
			return nil, errors.New("主机名不应申请证书")
		}
	}
	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	w := call(a, http.MethodPost, "/api/v1/sites", `{"hostnames":["fail.example.com","*.example.com","ok.example.com"],"auto_certificate":true}`, cookie)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	var result siteSaveResult
	if err := jsonv2.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Bindings) != 3 || result.Operation == nil || result.Operation.Kind != "certificate" {
		t.Fatalf("返回的绑定或证书操作有误：%+v", result)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		var status, message string
		err := a.DB.QueryRowContext(ctx, "SELECT status,error FROM operations WHERE id=?", result.Operation.ID).Scan(&status, &message)
		if err != nil {
			t.Fatal(err)
		}
		if status == "failed" {
			if !strings.Contains(message, "fail.example.com") || !strings.Contains(message, "域名验证失败") {
				t.Fatalf("证书操作未报告失败域名和原因：%s", message)
			}
			break
		}
		if status != "running" || time.Now().After(deadline) {
			t.Fatalf("证书操作未正确标记失败：%s", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, test := range []struct{ domain, status, accessURL string }{
		{"fail.example.com", "failed", "http://fail.example.com"},
		{"*.example.com", "disabled", ""},
		{"ok.example.com", "enabled", "https://ok.example.com"},
	} {
		i := slices.IndexFunc(result.Bindings, func(binding Binding) bool { return binding.Domain == test.domain })
		if i < 0 || result.Bindings[i].CertificateStatus != "disabled" {
			t.Fatalf("建站响应未返回 HTTP 绑定：%s", test.domain)
		}
		saved, err := a.binding(ctx, result.Bindings[i].ID)
		if err != nil || saved.CertificateStatus != test.status || a.BindingURL(saved) != test.accessURL {
			t.Fatalf("证书处理结果有误：%+v，%v", saved, err)
		}
		if test.status == "failed" && !strings.Contains(saved.CertificateError, "域名验证失败") {
			t.Fatalf("签发失败的原因未保存：%+v", saved)
		}
		if test.status == "disabled" {
			w := call(a, http.MethodPost, "/api/v1/bindings/"+saved.ID+"/certificate", "", cookie)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "通配符") {
				t.Fatalf("手动申请通配符证书未被阻止：%d %s", w.Code, w.Body.String())
			}
		}
	}
	config, err := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
	if err != nil || !strings.Contains(string(config), "listen 80; server_name fail.example.com;") ||
		!strings.Contains(string(config), "listen 80; server_name *.example.com;") ||
		!strings.Contains(string(config), "listen 443 ssl; server_name ok.example.com;") {
		t.Fatalf("证书部分失败后的 Nginx 配置有误：%s，%v", config, err)
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
	w := call(a, http.MethodPost, "/api/v1/sites", `{"identifier":"证书待重试","hostnames":["blog.example.com"],"auto_certificate":true}`, cookie)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body.String())
	}
	var result siteSaveResult
	if err := jsonv2.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID == "" || len(result.Bindings) != 1 || a.BindingURL(result.Bindings[0]) != "http://blog.example.com" || result.Operation != nil || !strings.Contains(result.CertificateError, "重新申请") {
		t.Fatalf("证书操作写入失败时未返回可重试的 HTTP 网站：%+v", result)
	}
	binding, err := a.binding(t.Context(), result.Bindings[0].ID)
	if err != nil || binding.ResourceID != result.ID || binding.CertificateStatus != "disabled" {
		t.Fatalf("证书操作写入失败时未保留 HTTP 绑定：%+v，%v", binding, err)
	}
}

func TestSiteMigrationMakesIdentifiersUniqueAndKeepsExistingSites(t *testing.T) {
	root := t.TempDir()
	config := DefaultConfig()
	config.AgreeTOS = true
	if err := SaveConfig(root, config); err != nil {
		t.Fatal(err)
	}
	firstDir, duplicateDir := t.TempDir(), t.TempDir()
	oldDB, err := sql.Open("sqlite", filepath.Join(root, "data", "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer oldDB.Close()
	payload, err := jsonv2.Marshal(Binding{
		ID: "existing-binding", Kind: "site", ResourceID: "first", Domain: "legacy.example.com", Directory: firstDir,
		SPA: true, CertificateStatus: "enabled", CertificateAccountID: legacyACMEID, ExpiresAt: "2030-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldDB.ExecContext(t.Context(), `
		CREATE TABLE sites(id TEXT PRIMARY KEY,name TEXT NOT NULL,directory TEXT NOT NULL,spa INTEGER NOT NULL,updated_at TEXT NOT NULL);
		CREATE TABLE bindings(id TEXT PRIMARY KEY,domain TEXT NOT NULL UNIQUE,payload TEXT NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := oldDB.ExecContext(t.Context(), `
		INSERT INTO sites VALUES('first','旧站',?,1,'2026-11-19T08:00:00Z'),
			('second','旧站',?,0,'2026-11-19T08:00:00Z'),
			('third','旧站-2',?,0,'2026-11-19T08:00:00Z');
	`, firstDir, duplicateDir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := oldDB.ExecContext(t.Context(), "INSERT INTO bindings VALUES('existing-binding','legacy.example.com',?)", string(payload)); err != nil {
		t.Fatal(err)
	}
	if err := oldDB.Close(); err != nil {
		t.Fatal(err)
	}

	a, err := Open(root, "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := t.Context()
	first, err := a.site(ctx, "first")
	if err != nil || first.Identifier != "旧站" || first.Directory != firstDir || !first.SPA || first.Note != "" {
		t.Fatalf("原网站迁移结果有误：%+v，%v", first, err)
	}
	duplicate, err := a.site(ctx, "second")
	if err != nil || duplicate.Identifier == "" || duplicate.Identifier == first.Identifier || duplicate.Identifier == "旧站-2" ||
		duplicate.Directory != duplicateDir || duplicate.Note != "" {
		t.Fatalf("旧重名网站的标识或数据有误：%+v，%v", duplicate, err)
	}
	third, err := a.site(ctx, "third")
	if err != nil || third.Identifier != "旧站-2" {
		t.Fatalf("生成的唯一标识覆盖了原有名称：%+v，%v", third, err)
	}
	binding, err := a.binding(ctx, "existing-binding")
	if err != nil || binding.ResourceID != first.ID || binding.Directory != firstDir || binding.CertificateStatus != "enabled" ||
		binding.CertificateAccountID != legacyACMEID || binding.ExpiresAt != "2030-01-01T00:00:00Z" {
		t.Fatalf("网站迁移未保留旧域名和证书：%+v，%v", binding, err)
	}

	if err := a.InitializeAdministrator("管理员", "test-password"); err != nil {
		t.Fatal(err)
	}
	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	data, err := jsonv2.Marshal(Site{Identifier: "旧文档站", Directory: duplicateDir, Note: "旧站用途\n待整理"})
	if err != nil {
		t.Fatal(err)
	}
	w := call(a, http.MethodPut, "/api/v1/sites/second", string(data), cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("无域名旧网站无法改名或添加备注：%d %s", w.Code, w.Body.String())
	}
	reopened, err := Open(root, a.Version)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.site(ctx, "second")
	if err != nil || saved.Identifier != "旧文档站" || saved.Note != "旧站用途\n待整理" || saved.Directory != duplicateDir {
		t.Fatalf("重启后网站标识、目录或备注有误：%+v，%v", saved, err)
	}
	saved.Note = ""
	data, err = jsonv2.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	w = call(reopened, http.MethodPut, "/api/v1/sites/second", string(data), cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("无法清空网站备注：%d %s", w.Code, w.Body.String())
	}
	if saved, err = reopened.site(ctx, "second"); err != nil || saved.Note != "" {
		t.Fatalf("清空后的备注未保存：%+v，%v", saved, err)
	}
}
