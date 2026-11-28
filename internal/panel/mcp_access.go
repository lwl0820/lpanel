package panel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

const mcpPath = "/api/v1/mcp"

type mcpTokenSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type mcpSettings struct {
	Enabled bool              `json:"enabled"`
	Tokens  []mcpTokenSummary `json:"tokens"`
	Path    string            `json:"path"`
}

func (a *App) registerMCPAccess() error {
	_, err := a.DB.Exec(`
CREATE TABLE IF NOT EXISTS mcp_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1))
);
INSERT OR IGNORE INTO mcp_settings (id, enabled) VALUES (1, 0);
CREATE TABLE IF NOT EXISTS mcp_tokens (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  digest BLOB NOT NULL UNIQUE
);`)
	if err != nil {
		return err
	}
	a.Mux.Handle("GET "+mcpPath+"/settings", a.RequireAuth(http.HandlerFunc(a.getMCPSettings)))
	a.Mux.Handle("PUT "+mcpPath+"/settings", a.RequireAuth(http.HandlerFunc(a.saveMCPSettings)))
	a.Mux.Handle("POST "+mcpPath+"/tokens", a.RequireAuth(http.HandlerFunc(a.createMCPToken)))
	a.Mux.Handle("DELETE "+mcpPath+"/tokens/{id}", a.RequireAuth(http.HandlerFunc(a.deleteMCPToken)))
	return nil
}

func (a *App) getMCPSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings := mcpSettings{Tokens: []mcpTokenSummary{}, Path: mcpPath}
	rows, err := a.DB.QueryContext(r.Context(), `
SELECT enabled, COALESCE(mcp_tokens.id, ''), COALESCE(mcp_tokens.name, '')
FROM mcp_settings LEFT JOIN mcp_tokens ON TRUE
WHERE mcp_settings.id = 1 ORDER BY mcp_tokens.name, mcp_tokens.id`)
	if err != nil {
		WriteError(w, 500, "mcp_settings_error", "无法读取 MCP 设置")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var token mcpTokenSummary
		if err := rows.Scan(&settings.Enabled, &token.ID, &token.Name); err != nil {
			WriteError(w, 500, "mcp_settings_error", "无法读取 MCP 设置")
			return
		}
		if token.ID != "" {
			settings.Tokens = append(settings.Tokens, token)
		}
	}
	if err := rows.Err(); err != nil {
		WriteError(w, 500, "mcp_settings_error", "无法读取 MCP 设置")
		return
	}
	WriteJSON(w, 200, settings)
}

func (a *App) saveMCPSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		WriteError(w, 400, "invalid_mcp_settings", "请指定是否开启 MCP")
		return
	}
	result, err := a.DB.ExecContext(r.Context(), `
UPDATE mcp_settings SET enabled = ?
WHERE id = 1 AND (NOT ? OR EXISTS (SELECT 1 FROM mcp_tokens))`, *input.Enabled, *input.Enabled)
	if err != nil {
		WriteError(w, 500, "mcp_settings_error", "无法保存 MCP 设置")
		return
	}
	changed, err := result.RowsAffected()
	if err != nil {
		WriteError(w, 500, "mcp_settings_error", "无法保存 MCP 设置")
		return
	}
	if changed == 0 {
		WriteError(w, 400, "mcp_token_required", "请先签发一枚 MCP 令牌")
		return
	}
	a.getMCPSettings(w, r)
}

func (a *App) createMCPToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Name string `json:"name"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		WriteError(w, 400, "invalid_mcp_token_name", "请填写令牌名称")
		return
	}
	var random [32]byte
	rand.Read(random[:])
	token := "lpm_" + base64.RawURLEncoding.EncodeToString(random[:])
	digest := sha256.Sum256([]byte(token))
	if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO mcp_tokens (id, name, digest) VALUES (?, ?, ?)`, rand.Text(), name, digest[:]); err != nil {
		WriteError(w, 500, "mcp_token_error", "无法签发 MCP 令牌")
		return
	}
	WriteJSON(w, 201, map[string]string{"token": token})
}

func (a *App) deleteMCPToken(w http.ResponseWriter, r *http.Request) {
	if err := a.revokeMCPToken(r.Context(), r.PathValue("id")); err != nil {
		WriteError(w, 500, "mcp_token_error", "无法撤销 MCP 令牌")
		return
	}
	WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) revokeMCPToken(ctx context.Context, id string) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM mcp_tokens WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mcp_settings SET enabled = 0
WHERE id = 1 AND NOT EXISTS (SELECT 1 FROM mcp_tokens)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) serveMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	var digest []byte
	if ok && strings.EqualFold(scheme, "Bearer") && token != "" {
		sum := sha256.Sum256([]byte(token))
		digest = sum[:]
	}
	var enabled, allowed bool
	err := a.DB.QueryRowContext(r.Context(), `
SELECT enabled, EXISTS (SELECT 1 FROM mcp_tokens WHERE digest = ?)
FROM mcp_settings WHERE id = 1`, digest).Scan(&enabled, &allowed)
	if err != nil {
		WriteError(w, 503, "mcp_unavailable", "MCP 暂不可用")
		return
	}
	if !enabled {
		WriteError(w, 404, "not_found", "页面不存在")
		return
	}
	if redirect := r.Header.Get("X-LPanel-Redirect"); redirect != "" {
		http.Redirect(w, r, redirect+r.URL.RequestURI(), http.StatusMovedPermanently)
		return
	}
	if !allowed {
		w.Header().Set("WWW-Authenticate", "Bearer")
		WriteError(w, 401, "unauthorized", "需要有效的 MCP 令牌")
		return
	}
	a.serveMCPProtocol(w, r)
}
