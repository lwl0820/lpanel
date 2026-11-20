package panel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginPathLifecycle(t *testing.T) {
	a, root := testApp(t)
	a.Config.NginxConfigDir = filepath.Join(root, "nginx")
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	a.NginxProbe = func(context.Context, string, string, string) error { return nil }
	web := filepath.Join(root, "current", "web")
	if err := os.MkdirAll(filepath.Join(web, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"index.html":      `<html><head><script src="/assets/login.js"></script></head><body></body></html>`,
		"assets/login.js": `export const form = 'login';`,
	} {
		if err := os.WriteFile(filepath.Join(web, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	const credentials = `{"username":"管理员","password":"test-password"}`
	const settings = "/api/v1/system/access/login-path"
	login := func(path string) *http.Cookie {
		t.Helper()
		w := call(a, http.MethodPost, path, credentials, nil)
		cookies := w.Result().Cookies()
		if w.Code != 200 || len(cookies) != 1 {
			t.Fatalf("登录失败：%d %s", w.Code, w.Body.String())
		}
		return cookies[0]
	}
	cookie := login("/api/v1/auth/login")
	if w := call(a, http.MethodPut, settings, `{"login_path":"/guest"}`, nil); w.Code != 401 {
		t.Fatalf("未登录修改入口返回 %d", w.Code)
	}
	if w := call(a, http.MethodPut, settings, `{"login_path":"/api/private"}`, cookie); w.Code != 400 {
		t.Fatalf("占用系统路径返回 %d", w.Code)
	}
	if w := call(a, http.MethodPut, settings, `{"login_path":"/abc-DEF_01"}`, cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"login_path":"/abc-DEF_01"`) {
		t.Fatalf("设置入口失败：%d %s", w.Code, w.Body.String())
	}
	const entry = "/abc-DEF_01"
	for _, uri := range []string{"/", "/settings", "/index.html", "/assets/login.js", entry + "/", "/abc-def_01", "/%61bc-DEF_01"} {
		if w := call(a, http.MethodGet, uri, "", nil); w.Code != 404 || strings.Contains(w.Body.String(), entry) || w.Header().Get("Location") != "" {
			t.Errorf("匿名访问 %s 未直接返回 404：%d %s", uri, w.Code, w.Body.String())
		}
	}
	if w := call(a, http.MethodPost, "/api/v1/auth/login", credentials, nil); w.Code != 404 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("匿名访问旧登录接口返回 %d", w.Code)
	}
	if w := call(a, http.MethodGet, entry+"?source=bookmark", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `<meta name="lpanel-login-path" content="`+entry+`">`) || !strings.Contains(w.Body.String(), entry+"/assets/login.js") {
		t.Fatalf("入口登录页错误：%d %s", w.Code, w.Body.String())
	}
	if w := call(a, http.MethodGet, entry+"/assets/login.js", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "export const form") {
		t.Fatalf("入口资源读取失败：%d %s", w.Code, w.Body.String())
	}
	if w := call(a, http.MethodGet, "/api/v1/auth/me", "", cookie); w.Code != 200 {
		t.Fatalf("设置入口后的已有会话返回 %d", w.Code)
	}
	cookie = login(entry)
	for _, uri := range []string{"/", "/settings", entry, "/assets/login.js", "/api/v1/auth/me"} {
		if w := call(a, http.MethodGet, uri, "", cookie); w.Code != 200 || strings.Contains(w.Body.String(), `name="lpanel-login-path"`) {
			t.Errorf("登录后访问 %s 返回 %d %s", uri, w.Code, w.Body.String())
		}
	}

	if w := call(a, http.MethodPut, settings, `{"login_path":"/another/entry"}`, cookie); w.Code != 200 {
		t.Fatalf("更换入口失败：%d %s", w.Code, w.Body.String())
	}
	reopened, err := Open(root, a.Version)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if w := call(reopened, http.MethodGet, entry, "", nil); w.Code != 404 {
		t.Fatalf("重启后旧入口返回 %d", w.Code)
	}
	if w := call(reopened, http.MethodGet, "/another/entry", "", nil); w.Code != 200 {
		t.Fatalf("重启后新入口返回 %d", w.Code)
	}
	if _, err = a.DB.Exec("UPDATE sessions SET expires_at=0 WHERE token=?", cookie.Value); err != nil {
		t.Fatal(err)
	}
	if w := call(a, http.MethodGet, "/settings", "", cookie); w.Code != 404 {
		t.Fatalf("过期会话访问普通页面返回 %d", w.Code)
	}
	cookie = login("/another/entry")
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("配置检查失败") }
	if w := call(a, http.MethodPut, settings, `{"login_path":"/failed"}`, cookie); w.Code != 500 || a.currentConfig().LoginPath != "/another/entry" {
		t.Fatalf("Nginx 失败时入口未保留：%d %s", w.Code, w.Body.String())
	}
	a.Command = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	if w := call(a, http.MethodPut, settings, `{"login_path":"/"}`, cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"login_path":""`) {
		t.Fatalf("关闭校验失败：%d %s", w.Code, w.Body.String())
	}
	login("/api/v1/auth/login")
}

func TestLoginPathProxyHealthAndRedirect(t *testing.T) {
	root := t.TempDir()
	config := DefaultConfig()
	config.LoginPath = "/welcome"
	if err := SaveConfig(root, config); err != nil {
		t.Fatal(err)
	}
	a, err := Open(root, "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if w := call(a, http.MethodGet, "/api/v1/health", "", nil); w.Code != 200 {
		t.Fatalf("本机健康检查返回 %d", w.Code)
	}
	for _, test := range []struct {
		path     string
		redirect string
		status   int
		location string
	}{
		{path: "/api/v1/health", status: 404},
		{path: "/", redirect: "https://example.com:8443", status: 404},
		{path: "/welcome?source=bookmark", redirect: "https://example.com:8443", status: 301, location: "https://example.com:8443/welcome?source=bookmark"},
	} {
		r := httptest.NewRequest(http.MethodGet, test.path, nil)
		r.Header.Set("X-LPanel-Proxy", "1")
		r.Header.Set("X-LPanel-Redirect", test.redirect)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != test.status || w.Header().Get("Location") != test.location {
			t.Errorf("代理请求 %s 返回 %d，跳转地址为 %q", test.path, w.Code, w.Header().Get("Location"))
		}
	}
}
