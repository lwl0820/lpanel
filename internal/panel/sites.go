package panel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
		var input struct {
			Domain          string `json:"domain"`
			AutoCertificate bool   `json:"auto_certificate"`
		}
		if !ReadJSON(w, r, &input) {
			return
		}
		b, err := a.SaveBinding(r.Context(), Binding{Kind: "site", ResourceID: s.ID, Domain: input.Domain, Directory: s.Directory, SPA: s.SPA, AutoCertificate: input.AutoCertificate})
		if err != nil {
			WriteError(w, 400, "binding_error", err.Error())
			return
		}
		var operation *Operation
		if input.AutoCertificate {
			op, e := a.StartOperation("certificate", func(ctx context.Context) (string, error) { return "", a.RequestCertificate(ctx, b.ID) })
			if e != nil {
				WriteError(w, 500, "operation_error", e.Error())
				return
			}
			operation = &op
		}
		WriteJSON(w, 201, map[string]any{"binding": b, "operation": operation, "access_url": a.BindingURL(b)})
	})))
	return nil
}
func (a *App) site(ctx context.Context, id string) (Site, error) {
	var s Site
	err := a.DB.QueryRowContext(ctx, "SELECT id,name,directory,spa,updated_at FROM sites WHERE id=?", id).Scan(&s.ID, &s.Name, &s.Directory, &s.SPA, &s.UpdatedAt)
	return s, err
}
func (a *App) saveSite(w http.ResponseWriter, r *http.Request) {
	var s Site
	if !ReadJSON(w, r, &s) {
		return
	}
	s.ID = r.PathValue("id")
	if s.ID != "" {
		if _, err := a.site(r.Context(), s.ID); err != nil {
			WriteError(w, 404, "not_found", "网站不存在")
			return
		}
	}
	saved, err := a.SaveSite(r.Context(), s)
	if err != nil {
		WriteError(w, 400, "site_error", err.Error())
		return
	}
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	WriteJSON(w, status, saved)
}
func (a *App) SaveSite(ctx context.Context, s Site) (Site, error) {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len(s.Name) > 100 {
		return s, fmt.Errorf("网站名称不能为空且不能超过100字节")
	}
	if !filepath.IsAbs(s.Directory) || strings.ContainsAny(s.Directory, "\n\r\"$;") {
		return s, fmt.Errorf("请输入有效的服务器绝对目录")
	}
	s.Directory = filepath.Clean(s.Directory)
	info, err := os.Stat(s.Directory)
	if err != nil || !info.IsDir() {
		return s, fmt.Errorf("网站目录不存在或无法访问")
	}
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	old, oldErr := a.site(ctx, s.ID)
	if s.ID == "" {
		raw := make([]byte, 12)
		if _, err = rand.Read(raw); err != nil {
			return s, err
		}
		s.ID = hex.EncodeToString(raw)
	} else if oldErr != nil {
		return s, fmt.Errorf("网站不存在")
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	bindings, err := a.ListBindings(ctx)
	if err != nil {
		return s, err
	}
	changed := []Binding{}
	for _, b := range bindings {
		if b.Kind == "site" && b.ResourceID == s.ID {
			changed = append(changed, b)
		}
	}
	if _, err = a.DB.ExecContext(ctx, "INSERT INTO sites(id,name,directory,spa,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,directory=excluded.directory,spa=excluded.spa,updated_at=excluded.updated_at", s.ID, s.Name, s.Directory, s.SPA, s.UpdatedAt); err != nil {
		return s, err
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
		if oldErr == nil {
			_, _ = a.DB.ExecContext(ctx, "UPDATE sites SET name=?,directory=?,spa=?,updated_at=? WHERE id=?", old.Name, old.Directory, old.SPA, old.UpdatedAt, old.ID)
		} else {
			_, _ = a.DB.ExecContext(ctx, "DELETE FROM sites WHERE id=?", s.ID)
		}
		return s, err
	}
	return s, nil
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
