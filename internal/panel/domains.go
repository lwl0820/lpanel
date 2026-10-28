package panel

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Binding struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	ResourceID        string `json:"resource_id"`
	Domain            string `json:"domain"`
	Upstream          string `json:"upstream"`
	Directory         string `json:"directory"`
	SPA               bool   `json:"spa"`
	Service           string `json:"service"`
	ContainerPort     int    `json:"container_port"`
	AutoCertificate   bool   `json:"auto_certificate"`
	CertificateStatus string `json:"certificate_status"`
	CertificateError  string `json:"certificate_error"`
	ExpiresAt         string `json:"expires_at"`
}

func (a *App) registerDomains() error {
	if _, err := a.DB.Exec(`CREATE TABLE IF NOT EXISTS bindings(id TEXT PRIMARY KEY,domain TEXT NOT NULL UNIQUE,payload TEXT NOT NULL)`); err != nil {
		return err
	}
	a.Mux.Handle("GET /api/v1/bindings", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := a.ListBindings(r.Context())
		if err != nil {
			WriteError(w, 500, "bindings_error", err.Error())
			return
		}
		WriteJSON(w, 200, items)
	})))
	a.Mux.Handle("POST /api/v1/bindings", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b Binding
		if !ReadJSON(w, r, &b) {
			return
		}
		saved, err := a.SaveBinding(r.Context(), b)
		if err != nil {
			WriteError(w, 400, "binding_error", err.Error())
			return
		}
		var op *Operation
		if saved.AutoCertificate && saved.CertificateStatus != "enabled" {
			job, e := a.StartOperation("certificate", func(ctx context.Context) (string, error) {
				err := a.RequestCertificate(ctx, saved.ID)
				return "证书申请流程结束", err
			})
			if e != nil {
				WriteError(w, 500, "operation_error", e.Error())
				return
			}
			op = &job
		}
		WriteJSON(w, 200, map[string]any{"binding": saved, "operation": op, "access_url": a.BindingURL(saved)})
	})))
	a.Mux.Handle("DELETE /api/v1/bindings/{id}", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.DeleteBinding(r.Context(), r.PathValue("id")); err != nil {
			WriteError(w, 400, "binding_error", err.Error())
			return
		}
		WriteJSON(w, 200, map[string]bool{"ok": true})
	})))
	a.Mux.Handle("POST /api/v1/bindings/{id}/certificate", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := a.binding(r.Context(), id); err != nil {
			WriteError(w, 404, "not_found", "域名绑定不存在")
			return
		}
		op, err := a.StartOperation("certificate", func(ctx context.Context) (string, error) {
			err := a.RequestCertificate(ctx, id)
			return "证书申请流程结束", err
		})
		if err != nil {
			WriteError(w, 500, "operation_error", err.Error())
			return
		}
		WriteJSON(w, 202, op)
	})))
	a.Mux.Handle("GET /api/v1/system/access", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { WriteJSON(w, 200, a.Config) })))
	a.Mux.Handle("PUT /api/v1/system/access", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ACMEEmail  string `json:"acme_email"`
			ACMEServer string `json:"acme_server"`
			AgreeTOS   bool   `json:"agree_tos"`
		}
		if !ReadJSON(w, r, &in) {
			return
		}
		config := a.Config
		config.ACMEEmail = in.ACMEEmail
		config.ACMEServer = in.ACMEServer
		config.AgreeTOS = in.AgreeTOS
		if err := SaveConfig(a.Root, config); err != nil {
			WriteError(w, 400, "config_error", err.Error())
			return
		}
		a.Config = config
		WriteJSON(w, 200, config)
	})))
	return nil
}
func (a *App) ListBindings(ctx context.Context) ([]Binding, error) {
	rows, err := a.DB.QueryContext(ctx, "SELECT payload FROM bindings ORDER BY domain")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Binding{}
	for rows.Next() {
		var data string
		var b Binding
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &b); err != nil {
			return nil, err
		}
		items = append(items, b)
	}
	return items, rows.Err()
}
func (a *App) binding(ctx context.Context, id string) (Binding, error) {
	var data string
	var b Binding
	err := a.DB.QueryRowContext(ctx, "SELECT payload FROM bindings WHERE id=?", id).Scan(&data)
	if err != nil {
		return b, err
	}
	err = json.Unmarshal([]byte(data), &b)
	return b, err
}
func (a *App) storeBinding(ctx context.Context, b Binding) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = a.DB.ExecContext(ctx, "INSERT INTO bindings(id,domain,payload) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET domain=excluded.domain,payload=excluded.payload", b.ID, b.Domain, string(data))
	return err
}
func (a *App) SaveBinding(ctx context.Context, b Binding) (Binding, error) {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	b.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(b.Domain), "."))
	if !regexp.MustCompile(`^([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}$`).MatchString(b.Domain) || len(b.Domain) > 253 {
		return b, fmt.Errorf("请输入有效的普通域名")
	}
	switch b.Kind {
	case "panel":
		b.ResourceID = "panel"
	case "site":
		if !filepath.IsAbs(b.Directory) || strings.ContainsAny(b.Directory, "\n\r\"$;") {
			return b, fmt.Errorf("请输入有效的静态网站绝对目录")
		}
		info, err := os.Stat(b.Directory)
		if err != nil || !info.IsDir() {
			return b, fmt.Errorf("静态网站目录不存在")
		}
	case "project":
		u, err := url.Parse(b.Upstream)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.User != nil || strings.ContainsAny(b.Upstream, "\n\r ;{}\"$") {
			return b, fmt.Errorf("应用上游须为有效的 HTTP/HTTPS 地址")
		}
	default:
		return b, fmt.Errorf("网站类型无效")
	}
	if b.ID == "" {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return b, err
		}
		b.ID = hex.EncodeToString(raw)
	}
	old, oldErr := a.binding(ctx, b.ID)
	b.CertificateStatus = "disabled"
	b.CertificateError = ""
	b.ExpiresAt = ""
	if oldErr == nil && old.Domain == b.Domain {
		b.CertificateStatus = old.CertificateStatus
		b.ExpiresAt = old.ExpiresAt
	}
	if b.AutoCertificate && !a.Config.AgreeTOS {
		return b, fmt.Errorf("请先在面板设置中同意证书服务条款")
	}
	if b.Kind == "panel" {
		items, err := a.ListBindings(ctx)
		if err != nil {
			return b, err
		}
		for _, item := range items {
			if item.Kind == "panel" && item.ID != b.ID {
				return b, fmt.Errorf("面板已有绑定域名，请编辑原绑定")
			}
		}
	}
	if err := a.storeBinding(ctx, b); err != nil {
		return b, fmt.Errorf("保存域名失败，域名可能已被使用：%w", err)
	}
	if err := a.applyNginx(ctx); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if oldErr == nil {
			_ = a.storeBinding(rollbackCtx, old)
		} else {
			_, _ = a.DB.ExecContext(rollbackCtx, "DELETE FROM bindings WHERE id=?", b.ID)
		}
		return b, err
	}
	return b, nil
}
func (a *App) DeleteBinding(ctx context.Context, id string) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	old, err := a.binding(ctx, id)
	if err != nil {
		return fmt.Errorf("域名绑定不存在")
	}
	if _, err = a.DB.ExecContext(ctx, "DELETE FROM bindings WHERE id=?", id); err != nil {
		return err
	}
	if err = a.applyNginx(ctx); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.storeBinding(rollbackCtx, old)
	}
	return err
}
func (a *App) BindingURL(b Binding) string {
	scheme := "http"
	if b.CertificateStatus == "enabled" {
		scheme = "https"
	}
	if b.Kind == "panel" {
		return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(b.Domain, fmt.Sprint(a.Config.PanelPort)))
	}
	return scheme + "://" + b.Domain
}
func (a *App) ApplyNginx(ctx context.Context) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	return a.applyNginx(ctx)
}
func (a *App) renderNginx(items []Binding, marker string) string {
	var out strings.Builder
	acme := fmt.Sprintf("location ^~ /.well-known/acme-challenge/ { root %q; default_type text/plain; }\n", filepath.Join(a.Root, "acme-webroot"))
	panel := Binding{Kind: "panel", Domain: "_"}
	others := []Binding{}
	for _, b := range items {
		if b.Kind == "panel" {
			panel = b
		} else {
			others = append(others, b)
		}
	}
	all := append([]Binding{panel}, others...)
	for _, b := range all {
		port := 80
		if b.Kind == "panel" {
			port = a.Config.PanelPort
		}
		tls := b.CertificateStatus == "enabled"
		if tls || b.Kind == "panel" && port != 80 {
			fmt.Fprintf(&out, "server { listen 80; server_name %s; %s location / { return 301 %s; } }\n", b.Domain, acme, strings.ReplaceAll(a.BindingURL(b), "://_:", "://$host:")+"$request_uri")
		}
		if tls && b.Kind != "panel" {
			port = 443
		}
		fmt.Fprintf(&out, "server { listen %d", port)
		if tls {
			out.WriteString(" ssl")
		}
		fmt.Fprintf(&out, "; server_name %s;\n", b.Domain)
		if tls {
			fmt.Fprintf(&out, "ssl_certificate %q; ssl_certificate_key %q;\n", filepath.Join(a.Root, "data/acme/live", b.Domain, "fullchain.pem"), filepath.Join(a.Root, "data/acme/live", b.Domain, "privkey.pem"))
		}
		out.WriteString(acme)
		switch b.Kind {
		case "panel":
			fmt.Fprintf(&out, "location = /.lpanel/config { default_type text/plain; return 200 %q; }\n", marker)
			fmt.Fprintf(&out, "root %q; index index.html; location / { try_files $uri $uri/ /index.html; } location /api/ { proxy_pass http://%s; proxy_set_header Host $host; proxy_set_header X-Forwarded-Proto $scheme; }\n", filepath.Join(a.Root, "current/web"), a.Config.Listen)
		case "site":
			fallback := "=404"
			if b.SPA {
				fallback = "/index.html"
			}
			fmt.Fprintf(&out, "root %q; index index.html; location / { try_files $uri $uri/ %s; }\n", b.Directory, fallback)
		case "project":
			fmt.Fprintf(&out, "location / { proxy_pass %s; proxy_http_version 1.1; proxy_set_header Host $host; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection \"upgrade\"; proxy_set_header X-Forwarded-Proto $scheme; }\n", b.Upstream)
		}
		out.WriteString("}\n")
	}
	return "# 本文件由 LPanel 管理\n" + out.String()
}
func (a *App) applyNginx(ctx context.Context) error {
	items, err := a.ListBindings(ctx)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(a.Root, "acme-webroot/.well-known/acme-challenge"), 0755); err != nil {
		return err
	}
	if err = os.MkdirAll(a.Config.NginxConfigDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
	old, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return err
	}
	marker := hex.EncodeToString(raw)
	config := a.Config
	config.PanelDomain = ""
	config.HTTPS = false
	for _, b := range items {
		if b.Kind == "panel" {
			config.PanelDomain = b.Domain
			config.HTTPS = b.CertificateStatus == "enabled"
		}
	}
	if err = os.WriteFile(path, []byte(a.renderNginx(items, marker)), 0644); err != nil {
		return err
	}
	restore := func() error {
		if readErr == nil {
			return os.WriteFile(path, old, 0644)
		}
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rollback := func() error {
		if err := restore(); err != nil {
			return err
		}
		// 恢复不能继承已经超时的原请求，否则旧配置无法重新加载。
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		output, err := a.Command(rollbackCtx, "systemctl", "reload", "nginx")
		if err != nil {
			return fmt.Errorf("恢复 Nginx 配置失败：%s：%w", output, err)
		}
		return nil
	}
	if output, e := a.Command(ctx, "nginx", "-t"); e != nil {
		_ = restore()
		return fmt.Errorf("Nginx 配置检查失败：%s：%w", output, e)
	}
	if output, e := a.Command(ctx, "systemctl", "reload", "nginx"); e != nil {
		restoreErr := rollback()
		return fmt.Errorf("Nginx 重载失败：%s：%w（恢复结果：%v）", output, e, restoreErr)
	}
	scheme := "http"
	if config.HTTPS {
		scheme = "https"
	}
	host := config.PanelDomain
	if host == "" {
		host = "localhost"
	}
	address := scheme + "://" + net.JoinHostPort("127.0.0.1", fmt.Sprint(config.PanelPort)) + "/.lpanel/config"
	if err = a.NginxProbe(ctx, address, host, marker); err != nil {
		restoreErr := rollback()
		return fmt.Errorf("Nginx 配置未确认生效：%w（恢复结果：%v）", err, restoreErr)
	}
	if err = SaveConfig(a.Root, config); err != nil {
		return err
	}
	a.Config = config
	return nil
}
func (a *App) RequestCertificate(ctx context.Context, id string) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	b, err := a.binding(ctx, id)
	if err != nil {
		return err
	}
	if !a.Config.AgreeTOS {
		return fmt.Errorf("尚未同意证书服务条款")
	}
	if b.Kind == "panel" && a.Config.PanelPort == 80 {
		return fmt.Errorf("面板使用 HTTPS 时不能占用证书验证端口 80")
	}
	previous := b
	b.AutoCertificate = true
	b.CertificateStatus = "requesting"
	if err = a.storeBinding(ctx, b); err != nil {
		return err
	}
	args := []string{"certonly", "--non-interactive", "--agree-tos", "--webroot", "-w", filepath.Join(a.Root, "acme-webroot"), "--cert-name", b.Domain, "-d", b.Domain, "--config-dir", filepath.Join(a.Root, "data/acme"), "--work-dir", filepath.Join(a.Root, ".tmp/acme"), "--logs-dir", filepath.Join(a.Root, "data/logs/acme")}
	if a.Config.ACMEEmail != "" {
		args = append(args, "--email", a.Config.ACMEEmail)
	} else {
		args = append(args, "--register-unsafely-without-email")
	}
	if a.Config.ACMEServer != "" {
		args = append(args, "--server", a.Config.ACMEServer)
	}
	output, err := a.Command(ctx, "certbot", args...)
	if err == nil {
		data, e := os.ReadFile(filepath.Join(a.Root, "data/acme/live", b.Domain, "fullchain.pem"))
		if e != nil {
			err = e
		} else {
			block, _ := pem.Decode(data)
			if block == nil {
				err = fmt.Errorf("证书格式无效")
			} else {
				cert, e := x509.ParseCertificate(block.Bytes)
				err = e
				if e == nil {
					b.ExpiresAt = cert.NotAfter.UTC().Format(time.RFC3339)
				}
			}
		}
	}
	if err == nil {
		b.CertificateStatus = "enabled"
		b.CertificateError = ""
		err = a.storeBinding(ctx, b)
		if err == nil {
			err = a.applyNginx(ctx)
		}
	}
	if err != nil {
		b.CertificateStatus = "failed"
		b.ExpiresAt = ""
		if previous.CertificateStatus == "enabled" {
			b.CertificateStatus = "enabled"
			b.ExpiresAt = previous.ExpiresAt
		}
		b.CertificateError = fmt.Sprintf("申请或启用证书失败：%s：%v", strings.TrimSpace(string(output)), err)
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.storeBinding(rollbackCtx, b)
		return fmt.Errorf("%s", b.CertificateError)
	}
	return nil
}
func (a *App) RenewCertificates(ctx context.Context) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	args := []string{"renew", "--non-interactive", "--config-dir", filepath.Join(a.Root, "data/acme"), "--work-dir", filepath.Join(a.Root, ".tmp/acme"), "--logs-dir", filepath.Join(a.Root, "data/logs/acme"), "--deploy-hook", "nginx -t && systemctl reload nginx"}
	output, err := a.Command(ctx, "certbot", args...)
	if err != nil {
		return fmt.Errorf("证书续期失败：%s：%w", output, err)
	}
	items, err := a.ListBindings(ctx)
	if err != nil {
		return err
	}
	for _, b := range items {
		if b.CertificateStatus != "enabled" {
			continue
		}
		data, e := os.ReadFile(filepath.Join(a.Root, "data/acme/live", b.Domain, "fullchain.pem"))
		if e != nil {
			return e
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return fmt.Errorf("证书格式无效")
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			return e
		}
		b.ExpiresAt = cert.NotAfter.UTC().Format(time.RFC3339)
		if e = a.storeBinding(ctx, b); e != nil {
			return e
		}
	}
	return a.applyNginx(ctx)
}

// probeNginx 只读取本机面板的配置标识，不验证公网证书信任或探测业务上游。
func probeNginx(ctx context.Context, address, host, marker string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// 此客户端仅用于 127.0.0.1 的生效确认：允许 ACME staging 或测试证书。
	// 面向用户或外部服务的 HTTP 客户端不能使用此 TLS 设置。
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: host}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		req.Host = host
		response, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 128))
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && string(body) == marker {
				return nil
			}
			err = fmt.Errorf("返回的配置标识不匹配")
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("%v：%w", lastErr, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
