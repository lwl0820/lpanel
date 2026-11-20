package panel

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) saveLoginPath(w http.ResponseWriter, r *http.Request) {
	var input struct {
		LoginPath *string `json:"login_path"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	if input.LoginPath == nil {
		WriteError(w, 400, "invalid_login_path", "请填写登录路径，关闭校验时可留空")
		return
	}
	path := *input.LoginPath
	if err := validateLoginPath(path); err != nil {
		WriteError(w, 400, "invalid_login_path", err.Error())
		return
	}
	if path == "/" {
		path = ""
	}
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	config := a.currentConfig()
	if config.LoginPath != path {
		config.LoginPath = path
		if err := a.applyNginxConfig(r.Context(), config); err != nil {
			WriteError(w, 500, "login_path_error", err.Error())
			return
		}
		config = a.currentConfig()
	}
	WriteJSON(w, 200, config)
}

func (a *App) servePanel(w http.ResponseWriter, r *http.Request) {
	loginPath := a.currentConfig().LoginPath
	path := r.URL.EscapedPath()
	if loginPath == "" || path == "/api/v1/health" && r.Header.Get("X-LPanel-Proxy") == "" {
		a.Mux.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	entry := path == loginPath
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	webRoot := filepath.Join(a.Root, "current", "web")
	asset, scopedAsset := strings.CutPrefix(path, loginPath+"/assets/")
	if redirect := r.Header.Get("X-LPanel-Redirect"); redirect != "" {
		if !entry && a.sessionError(r) != "" {
			WriteError(w, 404, "not_found", "页面不存在")
			return
		}
		http.Redirect(w, r, redirect+r.URL.RequestURI(), http.StatusMovedPermanently)
		return
	}
	if entry {
		switch {
		case read:
			if a.sessionError(r) == "" {
				loginPath = ""
			}
			servePanelIndex(w, r, webRoot, loginPath)
		case r.Method == http.MethodPost:
			a.login(w, r)
		default:
			WriteError(w, 404, "not_found", "页面不存在")
		}
		return
	}
	if scopedAsset && read {
		servePanelAsset(w, r, webRoot, asset)
		return
	}
	if a.sessionError(r) != "" {
		WriteError(w, 404, "not_found", "页面不存在")
		return
	}
	if asset, ok := strings.CutPrefix(path, "/assets/"); ok && read {
		servePanelAsset(w, r, webRoot, asset)
		return
	}
	if path == "/api" || strings.HasPrefix(path, "/api/") || !read {
		a.Mux.ServeHTTP(w, r)
		return
	}
	servePanelIndex(w, r, webRoot, "")
}

func servePanelIndex(w http.ResponseWriter, r *http.Request, webRoot, loginPath string) {
	content, err := os.ReadFile(filepath.Join(webRoot, "index.html"))
	if err != nil {
		WriteError(w, 404, "not_found", "页面不存在")
		return
	}
	if loginPath != "" {
		content = bytes.Replace(content, []byte("<head>"), []byte(`<head><meta name="lpanel-login-path" content="`+loginPath+`">`), 1)
		content = bytes.ReplaceAll(content, []byte(`"/assets/`), []byte(`"`+loginPath+`/assets/`))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = w.Write(content)
	}
}

func servePanelAsset(w http.ResponseWriter, r *http.Request, webRoot, name string) {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		WriteError(w, 404, "not_found", "页面不存在")
		return
	}
	http.ServeFile(w, r, filepath.Join(webRoot, "assets", name))
}
