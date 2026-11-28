package panel

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

type Site struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Directory  string `json:"directory"`
	SPA        bool   `json:"spa"`
	Note       string `json:"note"`
	UpdatedAt  string `json:"updated_at"`
}

const siteColumns = "id,name,directory,spa,note,updated_at"

var errSiteIdentifierInUse = errors.New("该网站标识已被使用，请更换网站标识")

type siteBindingInput struct {
	Domain          string `json:"domain"`
	AutoCertificate bool   `json:"auto_certificate"`
	ACMEAccountID   string `json:"acme_account_id"`
}

func (s Site) domainBinding(input siteBindingInput) Binding {
	return Binding{Kind: "site", ResourceID: s.ID, Domain: input.Domain, Directory: s.Directory, SPA: s.SPA, AutoCertificate: input.AutoCertificate}
}

type siteCreateInput struct {
	Site
	Hostnames       []string `json:"hostnames"`
	AutoCertificate bool     `json:"auto_certificate"`
	ACMEAccountID   string   `json:"acme_account_id"`
}

type siteSaveResult struct {
	Site
	Bindings         []Binding  `json:"bindings"`
	Operation        *Operation `json:"operation,omitzero"`
	CertificateError string     `json:"certificate_error,omitempty"`
}

func (a *App) migrateSites() error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS sites(id TEXT PRIMARY KEY,name TEXT NOT NULL,directory TEXT NOT NULL,spa INTEGER NOT NULL,note TEXT NOT NULL DEFAULT '',updated_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var hasNote bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('sites') WHERE name='note')").Scan(&hasNote); err != nil {
		return err
	}
	if !hasNote {
		if _, err = tx.Exec("ALTER TABLE sites ADD COLUMN note TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT id,name FROM sites ORDER BY name,id")
		if err != nil {
			return err
		}
		defer rows.Close()
		names := map[string]int{}
		var duplicates []Site
		for rows.Next() {
			var s Site
			if err = rows.Scan(&s.ID, &s.Identifier); err != nil {
				return err
			}
			if names[s.Identifier] == 0 {
				names[s.Identifier] = 1
			} else {
				duplicates = append(duplicates, s)
			}
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for _, s := range duplicates {
			for {
				names[s.Identifier]++
				identifier := fmt.Sprintf("%s-%d", s.Identifier, names[s.Identifier])
				if names[identifier] != 0 {
					continue
				}
				if _, err = tx.Exec("UPDATE sites SET name=? WHERE id=?", identifier, s.ID); err != nil {
					return err
				}
				names[identifier] = 1
				break
			}
		}
	}
	if _, err = tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS sites_identifier ON sites(name)"); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) RegisterSiteRoutes() error {
	if err := a.migrateSites(); err != nil {
		return err
	}
	a.Mux.Handle("GET /api/v1/sites", a.RequireAuth(http.HandlerFunc(a.listSites)))
	a.Mux.Handle("POST /api/v1/sites", a.RequireAuth(http.HandlerFunc(a.handleCreateSite)))
	a.Mux.Handle("PUT /api/v1/sites/{id}", a.RequireAuth(http.HandlerFunc(a.handleUpdateSite)))
	a.Mux.Handle("DELETE /api/v1/sites/{id}", a.RequireAuth(http.HandlerFunc(a.handleDeleteSite)))
	a.Mux.Handle("POST /api/v1/sites/{id}/bindings", a.RequireAuth(http.HandlerFunc(a.handleCreateSiteBinding)))
	return nil
}

func (a *App) startSiteCertificate(bindings []Binding, accountID string) (*Operation, error) {
	if !slices.ContainsFunc(bindings, func(b Binding) bool { return b.AutoCertificate }) {
		return nil, nil
	}
	op, err := a.StartOperation("certificate", func(ctx context.Context) (string, error) {
		var failures []error
		for _, b := range bindings {
			if b.AutoCertificate {
				if err := a.RequestCertificateWithAccount(ctx, b.ID, accountID); err != nil {
					failures = append(failures, fmt.Errorf("%s：%w", b.Domain, err))
				}
			}
		}
		return "", errors.Join(failures...)
	})
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func (a *App) site(ctx context.Context, id string) (Site, error) {
	var s Site
	err := a.DB.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE id=?", id).Scan(&s.ID, &s.Identifier, &s.Directory, &s.SPA, &s.Note, &s.UpdatedAt)
	return s, err
}

func (a *App) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	var input siteCreateInput
	if !ReadJSON(w, r, &input) {
		return
	}
	saved, err := a.createSite(r.Context(), input)
	if err != nil {
		WriteError(w, 400, "site_error", err.Error())
		return
	}
	saved.Operation, err = a.startSiteCertificate(saved.Bindings, input.ACMEAccountID)
	if err != nil {
		saved.CertificateError = "无法启动证书申请，可在域名与证书中重新申请"
	}
	WriteJSON(w, 201, saved)
}

func (a *App) handleUpdateSite(w http.ResponseWriter, r *http.Request) {
	var input Site
	if !ReadJSON(w, r, &input) {
		return
	}
	input.ID = r.PathValue("id")
	saved, err := a.SaveSite(r.Context(), input)
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, 404, "not_found", "网站不存在")
		return
	}
	if err != nil {
		WriteError(w, 400, "site_error", err.Error())
		return
	}
	WriteJSON(w, 200, saved)
}

func validateSiteIdentifier(identifier string) error {
	if identifier == "" {
		return errors.New("请填写网站标识")
	}
	if len(identifier) > 253 {
		return errors.New("网站标识过长，请缩短后重试")
	}
	return nil
}

func validateSiteDirectory(directory string) error {
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, "\n\r\"$;") {
		return errors.New("请输入有效的服务器绝对目录")
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return errors.New("网站目录不存在或无法访问")
	}
	return nil
}

func (a *App) createSite(ctx context.Context, input siteCreateInput) (siteSaveResult, error) {
	var result siteSaveResult
	s := input.Site
	bindings := make([]Binding, 0, len(input.Hostnames))
	seen := make(map[string]bool, len(input.Hostnames))
	requestsCertificate := false
	for _, line := range input.Hostnames {
		if strings.TrimSpace(line) == "" {
			continue
		}
		domain, err := normalizeBindingDomain(line, "site")
		if err != nil {
			return result, fmt.Errorf("主机名 %q：%w", strings.TrimSpace(line), err)
		}
		if seen[domain] {
			return result, fmt.Errorf("主机名 %q 重复", domain)
		}
		seen[domain] = true
		autoCertificate := input.AutoCertificate && !strings.HasPrefix(domain, "*.")
		requestsCertificate = requestsCertificate || autoCertificate
		bindings = append(bindings, Binding{Kind: "site", Domain: domain, AutoCertificate: autoCertificate, CertificateStatus: "disabled"})
	}
	if len(bindings) == 0 {
		return result, errors.New("请至少填写一个主机名")
	}
	s.Identifier = cmp.Or(strings.TrimSpace(s.Identifier), bindings[0].Domain)
	if err := validateSiteIdentifier(s.Identifier); err != nil {
		return result, err
	}
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return result, err
	}
	s.ID = hex.EncodeToString(raw[:])
	allocateDirectory := strings.TrimSpace(s.Directory) == ""
	if allocateDirectory {
		name := strings.Map(func(r rune) rune {
			if !unicode.IsPrint(r) || strings.ContainsRune(`<>:"/\|?*$;`, r) {
				return '_'
			}
			return r
		}, s.Identifier)
		if name == "." || name == ".." {
			name = strings.ReplaceAll(name, ".", "_")
		}
		s.Directory = filepath.Join(a.Root, "sites", name)
	} else {
		s.Directory = filepath.Clean(s.Directory)
		if err := validateSiteDirectory(s.Directory); err != nil {
			return result, err
		}
	}
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	if requestsCertificate {
		if _, err := a.certificateAccount(ctx, input.ACMEAccountID, ""); err != nil {
			return result, err
		}
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err = tx.ExecContext(ctx, "INSERT INTO sites("+siteColumns+") VALUES(?,?,?,?,?,?)", s.ID, s.Identifier, s.Directory, s.SPA, s.Note, s.UpdatedAt); err != nil {
		if isUniqueConstraint(err) {
			return result, errSiteIdentifierInUse
		}
		return result, fmt.Errorf("保存网站失败：%w", err)
	}
	for i := range bindings {
		b := &bindings[i]
		b.ID, err = newBindingID()
		if err != nil {
			return result, err
		}
		b.ResourceID, b.Directory, b.SPA = s.ID, s.Directory, s.SPA
		if err = writeBinding(ctx, tx, *b); err != nil {
			if isUniqueConstraint(err) {
				return result, fmt.Errorf("主机名 %q 已被使用", b.Domain)
			}
			return result, fmt.Errorf("保存主机名 %q 失败：%w", b.Domain, err)
		}
	}
	keepDirectory := false
	if allocateDirectory {
		if err := os.MkdirAll(filepath.Dir(s.Directory), 0755); err != nil {
			return result, fmt.Errorf("无法创建公开网站目录：%w", err)
		}
		if err := os.Mkdir(s.Directory, 0755); err != nil {
			if errors.Is(err, os.ErrExist) {
				return result, errors.New("默认网页目录已存在，请修改网站标识或填写已有的网页目录")
			}
			return result, fmt.Errorf("无法创建默认网站目录：%w", err)
		}
		defer func() {
			if !keepDirectory {
				// 内容可能由管理员写入，只回收本次创建且仍为空的目录。
				_ = os.Remove(s.Directory)
			}
		}()
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	keepDirectory = true
	if err = a.applyNginx(ctx); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if rollbackErr := a.removeCreatedSite(rollbackCtx, s.ID, bindings); rollbackErr != nil {
			return result, errors.Join(err, fmt.Errorf("回收网站和主机名记录失败：%w", rollbackErr))
		}
		keepDirectory = false
		return result, err
	}
	return siteSaveResult{Site: s, Bindings: bindings}, nil
}

func (a *App) removeCreatedSite(ctx context.Context, id string, bindings []Binding) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, b := range bindings {
		if _, err = tx.ExecContext(ctx, "DELETE FROM bindings WHERE id=?", b.ID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sites WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) SaveSite(ctx context.Context, s Site) (Site, error) {
	s.Identifier = strings.TrimSpace(s.Identifier)
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	old, err := a.site(ctx, s.ID)
	if err != nil {
		return s, err
	}
	if err = validateSiteIdentifier(s.Identifier); err != nil {
		return s, err
	}
	s.Directory = filepath.Clean(s.Directory)
	if s.Directory != old.Directory {
		if err = validateSiteDirectory(s.Directory); err != nil {
			return s, err
		}
	}
	var previous, changed []Binding
	if s.Directory != old.Directory || s.SPA != old.SPA {
		bindings, err := a.ListBindings(ctx)
		if err != nil {
			return s, err
		}
		for _, b := range bindings {
			if b.Kind == "site" && b.ResourceID == s.ID {
				previous = append(previous, b)
				b.Directory, b.SPA = s.Directory, s.SPA
				changed = append(changed, b)
			}
		}
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err = a.updateSiteWithBindings(ctx, s, changed); err != nil {
		return s, err
	}
	if len(changed) > 0 {
		if err = a.applyNginx(ctx); err != nil {
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if rollbackErr := a.updateSiteWithBindings(rollbackCtx, old, previous); rollbackErr != nil {
				return s, errors.Join(err, fmt.Errorf("恢复网站和主机名记录失败：%w", rollbackErr))
			}
			return s, err
		}
	}
	return s, nil
}

func (a *App) updateSiteWithBindings(ctx context.Context, s Site, bindings []Binding) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE sites SET name=?,directory=?,spa=?,note=?,updated_at=? WHERE id=?", s.Identifier, s.Directory, s.SPA, s.Note, s.UpdatedAt, s.ID); err != nil {
		if isUniqueConstraint(err) {
			return errSiteIdentifierInUse
		}
		return fmt.Errorf("保存网站失败：%w", err)
	}
	for _, b := range bindings {
		if err = writeBinding(ctx, tx, b); err != nil {
			return err
		}
	}
	return tx.Commit()
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

func (a *App) listSites(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), "SELECT "+siteColumns+" FROM sites ORDER BY name")
	if err != nil {
		WriteError(w, 500, "site_error", "读取网站失败")
		return
	}
	defer rows.Close()
	items := []Site{}
	for rows.Next() {
		var s Site
		if err = rows.Scan(&s.ID, &s.Identifier, &s.Directory, &s.SPA, &s.Note, &s.UpdatedAt); err != nil {
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
}

func (a *App) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	if _, err := a.site(r.Context(), r.PathValue("id")); err != nil {
		WriteError(w, 404, "not_found", "网站不存在")
		return
	}
	if err := a.DeleteSite(r.Context(), r.PathValue("id")); err != nil {
		WriteError(w, 500, "site_error", err.Error())
		return
	}
	WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) handleCreateSiteBinding(w http.ResponseWriter, r *http.Request) {
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
	operation, err := a.startSiteCertificate([]Binding{b}, input.ACMEAccountID)
	if err != nil {
		WriteError(w, 500, "operation_error", err.Error())
		return
	}
	WriteJSON(w, 201, map[string]any{"binding": b, "operation": operation, "access_url": a.BindingURL(b)})
}
