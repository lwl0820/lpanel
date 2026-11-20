package panel

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strings"
	"time"
)

func (a *App) InitializeAdministrator(username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 8 || len(password) > 72 {
		return fmt.Errorf("管理员不能为空，密码长度须为 8 至 72 字节")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec("INSERT INTO administrators(id,username,password_hash) VALUES(1,?,?)", username, string(hash))
	if err != nil {
		return fmt.Errorf("管理员初始化失败，已有账号不会被覆盖：%w", err)
	}
	return nil
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	var hash string
	err := a.DB.QueryRowContext(r.Context(), "SELECT password_hash FROM administrators WHERE username=?", input.Username).Scan(&hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		WriteError(w, 500, "database_error", "读取账号失败")
		return
	}
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		WriteError(w, 401, "invalid_credentials", "账号或密码错误")
		return
	}
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		WriteError(w, 500, "session_error", "创建会话失败")
		return
	}
	token := hex.EncodeToString(raw)
	expires := time.Now().Add(24 * time.Hour)
	if _, err = a.DB.ExecContext(r.Context(), "INSERT INTO sessions(token,expires_at) VALUES(?,?)", token, expires.Unix()); err != nil {
		WriteError(w, 500, "session_error", "保存会话失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "lpanel_session", Value: token, Path: "/", HttpOnly: true, Secure: a.currentConfig().HTTPS, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: 86400})
	WriteJSON(w, 200, map[string]string{"username": input.Username})
}
func (a *App) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if message := a.sessionError(r); message != "" {
			WriteError(w, 401, "unauthorized", message)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) sessionError(r *http.Request) string {
	cookie, err := r.Cookie("lpanel_session")
	if err != nil {
		return "请先登录"
	}
	var expires int64
	if err = a.DB.QueryRowContext(r.Context(), "SELECT expires_at FROM sessions WHERE token=?", cookie.Value).Scan(&expires); err != nil || expires <= time.Now().Unix() {
		return "登录已过期，请重新登录"
	}
	return ""
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie("lpanel_session")
	if _, err := a.DB.ExecContext(r.Context(), "DELETE FROM sessions WHERE token=?", cookie.Value); err != nil {
		WriteError(w, 500, "session_error", "退出登录失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "lpanel_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.currentConfig().HTTPS, SameSite: http.SameSiteLaxMode})
	WriteJSON(w, 200, map[string]bool{"ok": true})
}
func (a *App) me(w http.ResponseWriter, r *http.Request) {
	var username string
	if err := a.DB.QueryRowContext(r.Context(), "SELECT username FROM administrators WHERE id=1").Scan(&username); err != nil {
		WriteError(w, 500, "database_error", "读取账号失败")
		return
	}
	WriteJSON(w, 200, map[string]string{"username": username})
}
