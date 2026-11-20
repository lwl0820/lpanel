package panel

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
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

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type Binding struct {
	ID                   string `json:"id"`
	Kind                 string `json:"kind"`
	ResourceID           string `json:"resource_id"`
	Domain               string `json:"domain"`
	Upstream             string `json:"upstream"`
	Directory            string `json:"directory"`
	SPA                  bool   `json:"spa"`
	Service              string `json:"service"`
	ContainerPort        int    `json:"container_port"`
	AutoCertificate      bool   `json:"auto_certificate"`
	CertificateStatus    string `json:"certificate_status"`
	CertificateAccountID string `json:"certificate_account_id,omitempty"`
	CertificateError     string `json:"certificate_error"`
	ExpiresAt            string `json:"expires_at"`
}

var (
	ordinaryDomain         = regexp.MustCompile(`^([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}$`)
	errWildcardCertificate = errors.New("通配符域名仅支持 HTTP 访问")
)

func normalizeBindingDomain(domain, kind string) (string, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	name := domain
	if kind == "site" {
		name = strings.TrimPrefix(domain, "*.")
	}
	if len(domain) > 253 || !ordinaryDomain.MatchString(name) {
		if kind == "site" {
			return "", errors.New("请输入有效的域名，通配符只能用于开头，例如 *.example.com")
		}
		return "", errors.New("请输入有效的普通域名")
	}
	return domain, nil
}

func (a *App) registerDomains() error {
	if _, err := a.DB.Exec(`CREATE TABLE IF NOT EXISTS bindings(id TEXT PRIMARY KEY,domain TEXT NOT NULL UNIQUE,payload TEXT NOT NULL)`); err != nil {
		return err
	}
	if err := a.registerACMEAccounts(); err != nil {
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
		var input struct {
			Binding
			ACMEAccountID string `json:"acme_account_id"`
		}
		if !ReadJSON(w, r, &input) {
			return
		}
		saved, err := a.SaveBindingWithAccount(r.Context(), input.Binding, input.ACMEAccountID)
		if err != nil {
			WriteError(w, 400, "binding_error", err.Error())
			return
		}
		var op *Operation
		if saved.AutoCertificate && saved.CertificateStatus != "enabled" {
			job, e := a.StartOperation("certificate", func(ctx context.Context) (string, error) {
				err := a.RequestCertificateWithAccount(ctx, saved.ID, input.ACMEAccountID)
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
		b, err := a.binding(r.Context(), id)
		if err != nil {
			WriteError(w, 404, "not_found", "域名绑定不存在")
			return
		}
		if strings.HasPrefix(b.Domain, "*.") {
			WriteError(w, 400, "binding_error", errWildcardCertificate.Error())
			return
		}
		var input struct {
			ACMEAccountID string `json:"acme_account_id"`
		}
		if r.ContentLength != 0 && !ReadJSON(w, r, &input) {
			return
		}
		op, err := a.StartOperation("certificate", func(ctx context.Context) (string, error) {
			err := a.RequestCertificateWithAccount(ctx, id, input.ACMEAccountID)
			return "证书申请流程结束", err
		})
		if err != nil {
			WriteError(w, 500, "operation_error", err.Error())
			return
		}
		WriteJSON(w, 202, op)
	})))
	a.Mux.Handle("GET /api/v1/system/access", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { WriteJSON(w, 200, a.currentConfig()) })))
	a.Mux.Handle("PUT /api/v1/system/access/login-path", a.RequireAuth(http.HandlerFunc(a.saveLoginPath)))
	a.Mux.Handle("PUT /api/v1/system/access", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ACMEEmail  string `json:"acme_email"`
			ACMEServer string `json:"acme_server"`
			AgreeTOS   bool   `json:"agree_tos"`
		}
		if !ReadJSON(w, r, &in) {
			return
		}
		config := a.currentConfig()
		config.ACMEEmail = in.ACMEEmail
		config.ACMEServer = in.ACMEServer
		config.AgreeTOS = in.AgreeTOS
		if err := a.saveLegacyAccessConfig(r.Context(), config); err != nil {
			WriteError(w, 400, "config_error", err.Error())
			return
		}
		WriteJSON(w, 200, a.currentConfig())
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
	return writeBinding(ctx, a.DB, b)
}
func writeBinding(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, b Binding) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "INSERT INTO bindings(id,domain,payload) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET domain=excluded.domain,payload=excluded.payload", b.ID, b.Domain, string(data))
	return err
}
func isUniqueConstraint(err error) bool {
	sqliteErr, ok := errors.AsType[*sqlite.Error](err)
	return ok && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}
func newBindingID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
func (a *App) SaveBinding(ctx context.Context, b Binding) (Binding, error) {
	return a.SaveBindingWithAccount(ctx, b, "")
}

func (a *App) SaveBindingWithAccount(ctx context.Context, b Binding, accountID string) (Binding, error) {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	return a.saveBindingLocked(ctx, b, accountID)
}
func (a *App) saveBindingLocked(ctx context.Context, b Binding, accountID string) (Binding, error) {
	domain, err := normalizeBindingDomain(b.Domain, b.Kind)
	if err != nil {
		return b, err
	}
	b.Domain = domain
	if b.AutoCertificate && strings.HasPrefix(b.Domain, "*.") {
		return b, errWildcardCertificate
	}
	switch b.Kind {
	case "panel":
		b.ResourceID = "panel"
	case "site":
		if err := validateSiteDirectory(b.Directory); err != nil {
			return b, err
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
		b.ID, err = newBindingID()
		if err != nil {
			return b, err
		}
	}
	old, oldErr := a.binding(ctx, b.ID)
	b.CertificateStatus = "disabled"
	b.CertificateAccountID = ""
	b.CertificateError = ""
	b.ExpiresAt = ""
	if oldErr == nil && old.Domain == b.Domain {
		b.CertificateStatus = old.CertificateStatus
		b.CertificateAccountID = old.CertificateAccountID
		b.ExpiresAt = old.ExpiresAt
	}
	if b.AutoCertificate && b.CertificateStatus != "enabled" {
		if _, err := a.certificateAccount(ctx, accountID, b.CertificateAccountID); err != nil {
			return b, err
		}
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
		if isUniqueConstraint(err) {
			return b, errors.New("该域名已被使用")
		}
		return b, fmt.Errorf("保存域名失败：%w", err)
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
	if strings.HasPrefix(b.Domain, "*.") {
		return ""
	}
	scheme := "http"
	if b.CertificateStatus == "enabled" {
		scheme = "https"
	}
	if b.Kind == "panel" {
		return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(b.Domain, fmt.Sprint(a.currentConfig().PanelPort)))
	}
	return scheme + "://" + b.Domain
}
func (a *App) ApplyNginx(ctx context.Context) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	return a.applyNginx(ctx)
}
func (a *App) renderNginx(items []Binding, marker string, config Config) string {
	var out strings.Builder
	acme := fmt.Sprintf("location ^~ /.well-known/acme-challenge/ { root %q; default_type text/plain; }\n", filepath.Join(a.Root, "acme-webroot"))
	proxy := fmt.Sprintf("proxy_pass http://%s; proxy_set_header Host $host; proxy_set_header X-Forwarded-Proto $scheme; proxy_set_header X-LPanel-Proxy 1;", config.Listen)
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
			port = config.PanelPort
		}
		tls := b.CertificateStatus == "enabled"
		if tls || b.Kind == "panel" && port != 80 {
			destination := strings.ReplaceAll(a.BindingURL(b), "://_:", "://$host:")
			fmt.Fprintf(&out, "server { listen 80; server_name %s; %s", b.Domain, acme)
			if b.Kind == "panel" && config.LoginPath != "" {
				fmt.Fprintf(&out, "location / { %s proxy_set_header X-LPanel-Redirect %s; } }\n", proxy, destination)
			} else {
				fmt.Fprintf(&out, "location / { return 301 %s$request_uri; } }\n", destination)
			}
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
			dir := a.certificateDir(b.CertificateAccountID, b.Domain)
			fmt.Fprintf(&out, "ssl_certificate %q; ssl_certificate_key %q;\n", filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem"))
		}
		out.WriteString(acme)
		switch b.Kind {
		case "panel":
			out.WriteString("location = /.lpanel/config { default_type text/plain; ")
			if config.LoginPath != "" {
				out.WriteString("if ($remote_addr != 127.0.0.1) { return 404; } ")
			}
			fmt.Fprintf(&out, "return 200 %q; }\n", marker)
			if config.LoginPath != "" {
				fmt.Fprintf(&out, "location / { %s proxy_set_header X-LPanel-Redirect \"\"; }\n", proxy)
			} else {
				fmt.Fprintf(&out, "root %q; index index.html; location / { try_files $uri $uri/ /index.html; }\n", filepath.Join(a.Root, "current/web"))
				out.WriteString("location = /index.html { add_header Cache-Control no-cache; }\n")
				fmt.Fprintf(&out, "location /api/ { %s proxy_set_header X-LPanel-Redirect \"\"; }\n", proxy)
			}
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
	return a.applyNginxConfig(ctx, a.currentConfig())
}

func (a *App) applyNginxConfig(ctx context.Context, config Config) error {
	items, err := a.ListBindings(ctx)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(a.Root, "acme-webroot/.well-known/acme-challenge"), 0755); err != nil {
		return err
	}
	if err = os.MkdirAll(config.NginxConfigDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(config.NginxConfigDir, "lpanel.conf")
	old, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return err
	}
	marker := hex.EncodeToString(raw)
	config.PanelDomain = ""
	config.HTTPS = false
	for _, b := range items {
		if b.Kind == "panel" {
			config.PanelDomain = b.Domain
			config.HTTPS = b.CertificateStatus == "enabled"
		}
	}
	if err = os.WriteFile(path, []byte(a.renderNginx(items, marker, config)), 0644); err != nil {
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
		return errors.Join(fmt.Errorf("保存面板配置失败：%w", err), rollback())
	}
	a.setConfig(config)
	return nil
}
func (a *App) RequestCertificate(ctx context.Context, id string) error {
	return a.RequestCertificateWithAccount(ctx, id, "")
}

func (a *App) RequestCertificateWithAccount(ctx context.Context, id, accountID string) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	b, err := a.binding(ctx, id)
	if err != nil {
		return err
	}
	if strings.HasPrefix(b.Domain, "*.") {
		return errWildcardCertificate
	}
	account, err := a.certificateAccount(ctx, accountID, b.CertificateAccountID)
	if err != nil {
		return err
	}
	if b.Kind == "panel" && a.Config.PanelPort == 80 {
		return fmt.Errorf("面板使用 HTTPS 时不能占用证书验证端口 80")
	}
	previous := b
	b.AutoCertificate = true
	if b.CertificateStatus != "enabled" {
		b.CertificateStatus = "requesting"
	}
	if err = a.storeBinding(ctx, b); err != nil {
		return err
	}
	args := []string{"certonly", "--non-interactive", "--agree-tos", "--webroot", "-w", filepath.Join(a.Root, "acme-webroot"), "--cert-name", b.Domain, "-d", b.Domain}
	args = append(args, a.certbotDirs(account.ID)...)
	args = append(args, "--logs-dir", filepath.Join(a.Root, "data/logs/acme", account.ID), "--server", account.server)
	args = append(args, certbotContact(account.Email)...)
	output, err := a.Command(ctx, "certbot", args...)
	if err == nil {
		b.ExpiresAt, err = a.certificateExpiry(account.ID, b.Domain)
	}
	if err == nil {
		b.CertificateStatus = "enabled"
		b.CertificateAccountID = account.ID
		b.CertificateError = ""
		err = a.storeBinding(ctx, b)
		if err == nil {
			err = a.applyNginx(ctx)
		}
	}
	if err != nil {
		b.CertificateStatus = "failed"
		b.CertificateAccountID = ""
		b.ExpiresAt = ""
		if previous.CertificateStatus == "enabled" {
			b.CertificateStatus = "enabled"
			b.CertificateAccountID = previous.CertificateAccountID
			b.ExpiresAt = previous.ExpiresAt
		}
		b.CertificateError = fmt.Sprintf("申请或启用证书失败：%s：%v", strings.TrimSpace(string(output)), err)
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = a.storeBinding(rollbackCtx, b)
		return fmt.Errorf("%s", b.CertificateError)
	}
	return nil
}

func (a *App) certificateExpiry(accountID, domain string) (string, error) {
	data, err := os.ReadFile(filepath.Join(a.certificateDir(accountID, domain), "fullchain.pem"))
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("证书格式无效")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return cert.NotAfter.UTC().Format(time.RFC3339), nil
}

func (a *App) RenewCertificates(ctx context.Context) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	items, err := a.ListBindings(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, b := range items {
		if b.CertificateStatus != "enabled" {
			continue
		}
		args := []string{"renew", "--non-interactive", "--cert-name", b.Domain}
		args = append(args, a.certbotDirs(b.CertificateAccountID)...)
		args = append(args, "--logs-dir", filepath.Join(a.Root, "data/logs/acme", b.CertificateAccountID),
			"--deploy-hook", "nginx -t && systemctl reload nginx")
		output, err := a.Command(ctx, "certbot", args...)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s 证书续期失败：%s：%w", b.Domain, strings.TrimSpace(string(output)), err))
			continue
		}
		expiresAt, err := a.certificateExpiry(b.CertificateAccountID, b.Domain)
		if err == nil {
			// 定时器独立运行，只更新仍使用该证书的绑定。
			_, err = a.DB.ExecContext(ctx, `UPDATE bindings SET payload=json_set(payload,'$.expires_at',?)
				WHERE id=? AND domain=? AND json_extract(payload,'$.certificate_status')='enabled'
				AND COALESCE(json_extract(payload,'$.certificate_account_id'),'')=?`, expiresAt, b.ID, b.Domain, b.CertificateAccountID)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s 证书到期时间更新失败：%w", b.Domain, err))
		}
	}
	return errors.Join(failures...)
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
