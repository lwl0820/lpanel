package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	dir, err := filepath.Abs("../../.tmp/tests")
	if err != nil {
		panic(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		panic(err)
	}
	_ = os.Setenv("TMPDIR", dir)
	os.Exit(m.Run())
}
func testApp(t *testing.T) (*App, string) {
	t.Helper()
	root := t.TempDir()
	if err := SaveConfig(root, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	a, err := Open(root, "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if err = a.InitializeAdministrator("管理员", "test-password"); err != nil {
		t.Fatal(err)
	}
	return a, root
}
func call(a *App, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	return w
}
func TestAuthenticationLifecycle(t *testing.T) {
	a, _ := testApp(t)
	if w := call(a, "GET", "/api/v1/auth/me", "", nil); w.Code != 401 {
		t.Fatalf("未登录返回 %d", w.Code)
	}
	if w := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"wrong"}`, nil); w.Code != 401 {
		t.Fatalf("错误密码返回 %d", w.Code)
	}
	w := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	if w.Code != 200 {
		t.Fatalf("登录失败：%s", w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("会话 Cookie 属性错误")
	}
	cookie := cookies[0]
	if w = call(a, "GET", "/api/v1/auth/me", "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), "管理员") {
		t.Fatal("当前用户错误")
	}
	if w = call(a, "POST", "/api/v1/auth/logout", "", cookie); w.Code != 200 {
		t.Fatal("退出失败")
	}
	if w = call(a, "GET", "/api/v1/auth/me", "", cookie); w.Code != 401 {
		t.Fatal("退出后会话仍有效")
	}
}
func TestAdministratorPersistence(t *testing.T) {
	a, root := testApp(t)
	if err := a.InitializeAdministrator("其他管理员", "new-password"); err == nil {
		t.Fatal("已有管理员被覆盖")
	}
	var hash string
	if err := a.DB.QueryRow("SELECT password_hash FROM administrators").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == "test-password" || !strings.HasPrefix(hash, "$2") {
		t.Fatal("密码未散列")
	}
	second, err := Open(root, "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	w := call(second, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	if w.Code != 200 {
		t.Fatal("重开后登录失败")
	}
	var state map[string]string
	if err = json.NewDecoder(call(second, "GET", "/api/v1/health", "", nil).Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state["version"] != "v0.2.0" {
		t.Fatal("版本未更新")
	}
}
func TestSecureAndExpiredSession(t *testing.T) {
	a, _ := testApp(t)
	a.Config.HTTPS = true
	w := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure {
		t.Fatal("HTTPS Cookie 缺少 Secure")
	}
	if _, err := a.DB.Exec("UPDATE sessions SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if call(a, "GET", "/api/v1/auth/me", "", cookie).Code != 401 {
		t.Fatal("过期会话被接受")
	}
}
func TestOperationsPersistOutcome(t *testing.T) {
	a, _ := testApp(t)
	op, err := a.StartOperation("deployment", func(context.Context) (string, error) { return "部署完成", nil })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var status, log string
		if err = a.DB.QueryRow("SELECT status,log FROM operations WHERE id=?", op.ID).Scan(&status, &log); err != nil {
			t.Fatal(err)
		}
		if status == "success" {
			if log != "部署完成" {
				t.Fatal(log)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("操作未完成")
}
func TestConfigRejectsExternalBackend(t *testing.T) {
	root := t.TempDir()
	config := DefaultConfig()
	config.Listen = "0.0.0.0:18080"
	if err := SaveConfig(root, config); err == nil {
		t.Fatal("保存配置接受非回环地址")
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(root, "data", "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root, "dev"); err == nil {
		t.Fatal("后端接受非回环地址")
	}
}
func TestPasswordNotInResponses(t *testing.T) {
	a, _ := testApp(t)
	w := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	if bytes.Contains(w.Body.Bytes(), []byte("test-password")) {
		t.Fatal("响应泄露密码")
	}
}
