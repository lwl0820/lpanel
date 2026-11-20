package panel

import (
	"net/http"
	"strings"
	"testing"
)

func TestManagementCredentials(t *testing.T) {
	a, root := testApp(t)
	loginPath := "/api/v1/auth/login"
	mePath := "/api/v1/auth/me"
	initialLogin := call(a, "POST", loginPath, `{"username":"管理员","password":"test-password"}`, nil)
	if initialLogin.Code != http.StatusOK {
		t.Fatalf("初始管理员登录失败：%s", initialLogin.Body)
	}
	cookie := initialLogin.Result().Cookies()[0]

	manager, err := Open(root, "dev")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.ChangeAdministratorUsername(t.Context(), "  新管理员  "); err != nil {
		t.Fatal(err)
	}
	if w := call(a, "POST", loginPath, `{"username":"管理员","password":"test-password"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("旧用户名仍可登录，状态码 %d", w.Code)
	}
	if w := call(a, "GET", mePath, "", cookie); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "新管理员") {
		t.Fatalf("用户名修改后已有会话未读取新用户名：%d %s", w.Code, w.Body)
	}
	if w := call(a, "POST", loginPath, `{"username":"新管理员","password":"test-password"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("新用户名与原密码不能登录：%d %s", w.Code, w.Body)
	}

	if err := manager.ResetAdministratorPassword(t.Context(), []byte("new-password")); err != nil {
		t.Fatal(err)
	}
	if w := call(a, "GET", mePath, "", cookie); w.Code != http.StatusUnauthorized {
		t.Fatalf("密码重置前的会话仍有效，状态码 %d", w.Code)
	}
	if w := call(a, "POST", loginPath, `{"username":"新管理员","password":"test-password"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("旧密码仍可登录，状态码 %d", w.Code)
	}
	if w := call(a, "POST", loginPath, `{"username":"新管理员","password":"new-password"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("新密码不能登录：%d %s", w.Code, w.Body)
	}
}

func TestManagementPasswordResetRollsBack(t *testing.T) {
	a, _ := testApp(t)
	loginPath := "/api/v1/auth/login"
	credentials := `{"username":"管理员","password":"test-password"}`
	w := call(a, "POST", loginPath, credentials, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("初始管理员登录失败：%s", w.Body)
	}
	cookie := w.Result().Cookies()[0]
	if _, err := a.DB.Exec(`CREATE TRIGGER fail_session_delete BEFORE DELETE ON sessions
		BEGIN SELECT RAISE(ABORT, 'the session cannot be deleted'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.ResetAdministratorPassword(t.Context(), []byte("new-password")); err == nil || !strings.Contains(err.Error(), "清除已有会话失败") {
		t.Fatalf("没有报告清除会话失败：%v", err)
	}
	if w = call(a, "GET", "/api/v1/auth/me", "", cookie); w.Code != http.StatusOK {
		t.Fatalf("事务失败后原会话不可用，状态码 %d", w.Code)
	}
	if w = call(a, "POST", loginPath, credentials, nil); w.Code != http.StatusOK {
		t.Fatalf("事务失败后原密码不可用，状态码 %d", w.Code)
	}
}
