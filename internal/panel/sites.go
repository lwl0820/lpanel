package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Site struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Directory string `json:"directory"`
	SPA       bool   `json:"spa"`
	UpdatedAt string `json:"updated_at"`
}

type siteBindingInput struct {
	Domain          string `json:"domain"`
	AutoCertificate bool   `json:"auto_certificate"`
	ACMEAccountID   string `json:"acme_account_id"`
}

func (s Site) domainBinding(input siteBindingInput) Binding {
	return Binding{Kind: "site", ResourceID: s.ID, Domain: input.Domain, Directory: s.Directory, SPA: s.SPA, AutoCertificate: input.AutoCertificate}
}

type siteSaveResult struct {
	Site
	Binding          *Binding   `json:"binding,omitzero"`
	Operation        *Operation `json:"operation,omitzero"`
	CertificateError string     `json:"certificate_error,omitempty"`
}

func (a *App) RegisterSiteRoutes() error {
	if _, err := a.DB.Exec(`CREATE TABLE IF NOT EXISTS sites(id TEXT PRIMARY KEY,name TEXT NOT NULL,directory TEXT NOT NULL,spa INTEGER NOT NULL,updated_at TEXT NOT NULL)`); err != nil {
		return err
	}
	a.Mux.Handle("GET /api/v1/sites", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := a.DB.QueryContext(r.Context(), "SELECT id,name,directory,spa,updated_at FROM sites ORDER BY name")
		if err != nil {
			WriteError(w, 500, "site_error", "读取网站失败")
			return
		}
		defer rows.Close()
		items := []Site{}
		for rows.Next() {
			var s Site
			if err = rows.Scan(&s.ID, &s.Name, &s.Directory, &s.SPA, &s.UpdatedAt); err != nil {
				WriteError(w, 500, "site_error", "读取网站失败")
				return
			}
			items = append(items, s)
		}
		if err = rows.Err(); err != nil {
			WriteError(w, 500, "site_error", "读取网站失败")
			return
		}
		WriteJSON(w, 200, items)
	})))
	a.Mux.Handle("POST /api/v1/sites", a.RequireAuth(http.HandlerFunc(a.saveSite)))
	a.Mux.Handle("PUT /api/v1/sites/{id}", a.RequireAuth(http.HandlerFunc(a.saveSite)))
	a.Mux.Handle("DELETE /api/v1/sites/{id}", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.site(r.Context(), r.PathValue("id")); err != nil {
			WriteError(w, 404, "not_found", "网站不存在")
			return
		}
		if err := a.DeleteSite(r.Context(), r.PathValue("id")); err != nil {
			WriteError(w, 500, "site_error", err.Error())
			return
		}
		WriteJSON(w, 200, map[string]bool{"ok": true})
	})))
	a.Mux.Handle("POST /api/v1/sites/{id}/bindings", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := a.site(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, 404, "not_found", "网站不存在")
			return
		}
		var input siteBindingInput
		if !ReadJSON(w, r, &input) {
			return
		}
		b, err := a.SaveBindingWithAccount(r.Context(), s.domainBinding(input), input.ACMEAccountID)
		if err != nil {
			WriteError(w, 400, "binding_error", err.Error())
			return
		}
		operation, err := a.startSiteCertificate(b, input.ACMEAccountID)
		if err != nil {
			WriteError(w, 500, "operation_error", err.Error())
			return
		}
		WriteJSON(w, 201, map[string]any{"binding": b, "operation": operation, "access_url": a.BindingURL(b)})
	})))
	return nil
}
func (a *App) startSiteCertificate(binding Binding, accountID string) (*Operation, error) {
	if !binding.AutoCertificate {
		return nil, nil
	}
	op, err := a.StartOperation("certificate", func(ctx context.Context) (string, error) {
		return "", a.RequestCertificateWithAccount(ctx, binding.ID, accountID)
	})
	if err != nil {
		return nil, err
	}
	return &op, nil
}
func (a *App) site(ctx context.Context, id string) (Site, error) {
	var s Site
	err := a.DB.QueryRowContext(ctx, "SELECT id,name,directory,spa,updated_at FROM sites WHERE id=?", id).Scan(&s.ID, &s.Name, &s.Directory, &s.SPA, &s.UpdatedAt)
	return s, err
}
func (a *App) saveSite(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Site
		Binding *siteBindingInput `json:"binding"`
	}
	if r.Method == http.MethodPost {
		if !ReadJSON(w, r, &input) {
			return
		}
	} else if !ReadJSON(w, r, &input.Site) {
		return
	}
	input.ID = r.PathValue("id")
	if input.ID != "" {
		if _, err := a.site(r.Context(), input.ID); err != nil {
			WriteError(w, 404, "not_found", "网站不存在")
			return
		}
	}
	saved, err := a.saveSiteWithBinding(r.Context(), input.Site, input.Binding)
	if err != nil {
		WriteError(w, 400, "site_error", err.Error())
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	if saved.Binding != nil {
		saved.Operation, err = a.startSiteCertificate(*saved.Binding, input.Binding.ACMEAccountID)
		if err != nil {
			saved.CertificateError = "无法启动证书申请，可在域名与证书中重新申请"
		}
	}
	WriteJSON(w, status, saved)
}
func (a *App) SaveSite(ctx context.Context, s Site) (Site, error) {
	saved, err := a.saveSiteWithBinding(ctx, s, nil)
	return saved.Site, err
}
func (a *App) saveSiteWithBinding(ctx context.Context, s Site, input *siteBindingInput) (siteSaveResult, error) {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len(s.Name) > 100 {
		return siteSaveResult{Site: s}, fmt.Errorf("网站名称不能为空且不能超过100字节")
	}
	creating := s.ID == ""
	allocateDirectory := creating && strings.TrimSpace(s.Directory) == ""
	if creating {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			return siteSaveResult{Site: s}, err
		}
		s.ID = hex.EncodeToString(raw)
	}
	if allocateDirectory {
		s.Directory = filepath.Join(a.Root, "sites", s.ID)
	} else {
		if !filepath.IsAbs(s.Directory) || strings.ContainsAny(s.Directory, "\n\r\"$;") {
			return siteSaveResult{Site: s}, fmt.Errorf("请输入有效的服务器绝对目录")
		}
		s.Directory = filepath.Clean(s.Directory)
		info, err := os.Stat(s.Directory)
		if err != nil || !info.IsDir() {
			return siteSaveResult{Site: s}, fmt.Errorf("网站目录不存在或无法访问")
		}
	}
	result := siteSaveResult{Site: s}
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	var old Site
	var err error
	var changed []Binding
	if !creating {
		old, err = a.site(ctx, s.ID)
		if err != nil {
			return result, fmt.Errorf("网站不存在")
		}
		bindings, err := a.ListBindings(ctx)
		if err != nil {
			return result, err
		}
		for _, b := range bindings {
			if b.Kind == "site" && b.ResourceID == s.ID {
				changed = append(changed, b)
			}
		}
	}
	siteSaved := false
	if allocateDirectory {
		if err := os.MkdirAll(filepath.Dir(s.Directory), 0755); err != nil {
			return result, fmt.Errorf("无法创建公开网站目录：%w", err)
		}
		if err := os.Mkdir(s.Directory, 0755); err != nil {
			return result, fmt.Errorf("无法创建默认网站目录：%w", err)
		}
		defer func() {
			if !siteSaved {
				// 内容可能由管理员写入，只回收本次创建且仍为空的目录。
				_ = os.Remove(s.Directory)
			}
		}()
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	result.Site = s
	if _, err = a.DB.ExecContext(ctx, "INSERT INTO sites(id,name,directory,spa,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,directory=excluded.directory,spa=excluded.spa,updated_at=excluded.updated_at", s.ID, s.Name, s.Directory, s.SPA, s.UpdatedAt); err != nil {
		return result, err
	}
	siteSaved = true
	if input != nil {
		binding, err := a.saveBindingLocked(ctx, s.domainBinding(*input), input.ACMEAccountID)
		if err != nil {
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if _, rollbackErr := a.DB.ExecContext(rollbackCtx, "DELETE FROM sites WHERE id=?", s.ID); rollbackErr != nil {
				return result, errors.Join(err, fmt.Errorf("回收网站记录失败：%w", rollbackErr))
			}
			siteSaved = false
			return result, err
		}
		result.Binding = &binding
	}
	for _, b := range changed {
		b.Directory = s.Directory
		b.SPA = s.SPA
		if err = a.storeBinding(ctx, b); err != nil {
			break
		}
	}
	if err == nil && len(changed) > 0 {
		err = a.applyNginx(ctx)
	}
	if err != nil {
		for _, b := range changed {
			_ = a.storeBinding(ctx, b)
		}
		_, _ = a.DB.ExecContext(ctx, "UPDATE sites SET name=?,directory=?,spa=?,updated_at=? WHERE id=?", old.Name, old.Directory, old.SPA, old.UpdatedAt, old.ID)
		return result, err
	}
	return result, nil
}
func (a *App) DeleteSite(ctx context.Context, id string) error {
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	if _, err := a.site(ctx, id); err != nil {
		return fmt.Errorf("网站不存在")
	}
	bindings, err := a.ListBindings(ctx)
	if err != nil {
		return err
	}
	removed := []Binding{}
	for _, b := range bindings {
		if b.Kind == "site" && b.ResourceID == id {
			removed = append(removed, b)
		}
	}
	for _, b := range removed {
		if _, err = a.DB.ExecContext(ctx, "DELETE FROM bindings WHERE id=?", b.ID); err != nil {
			break
		}
	}
	if err == nil && len(removed) > 0 {
		err = a.applyNginx(ctx)
	}
	if err == nil {
		_, err = a.DB.ExecContext(ctx, "DELETE FROM sites WHERE id=?", id)
	}
	if err != nil {
		for _, b := range removed {
			_ = a.storeBinding(ctx, b)
		}
		if len(removed) > 0 {
			_ = a.applyNginx(ctx)
		}
	}
	return err
}
