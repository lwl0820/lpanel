package panel

import (
	"bytes"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"encoding/json/v2"
)

func TestMCPAccessLifecycle(t *testing.T) {
	a, root := testApp(t)
	config := a.currentConfig()
	config.LoginPath = "/mcp"
	if err := SaveConfig(root, config); err != nil {
		t.Fatal(err)
	}
	a.setConfig(config)

	const settingsURL = mcpPath + "/settings"
	const tokensURL = mcpPath + "/tokens"
	if w := call(a, http.MethodGet, settingsURL, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("匿名访问管理接口返回 %d", w.Code)
	}
	login := call(a, http.MethodPost, config.LoginPath, `{"username":"管理员","password":"test-password"}`, nil)
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("自定义入口登录返回 %d", login.Code)
	}
	cookie := login.Result().Cookies()[0]

	settings := func() mcpSettings {
		t.Helper()
		w := call(a, http.MethodGet, settingsURL, "", cookie)
		var result mcpSettings
		if err := json.Unmarshal(w.Body.Bytes(), &result); w.Code != http.StatusOK || err != nil {
			t.Fatalf("读取 MCP 设置返回 %d：%v", w.Code, err)
		}
		if result.Path != mcpPath || result.Tokens == nil {
			t.Fatalf("MCP 连接信息有误：%+v", result)
		}
		return result
	}
	sign := func(name string) string {
		t.Helper()
		w := call(a, http.MethodPost, tokensURL, `{"name":"`+name+`"}`, cookie)
		var result map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &result); w.Code != http.StatusCreated || err != nil || result["token"] == "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("签发令牌返回 %d：%v", w.Code, err)
		}
		return result["token"]
	}
	revoke := func(id string) {
		t.Helper()
		if w := call(a, http.MethodDelete, tokensURL+"/"+id, "", cookie); w.Code != http.StatusOK {
			t.Fatalf("撤销令牌返回 %d", w.Code)
		}
	}
	request := func(token, body string, status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, mcpPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != status || (status == http.StatusOK && !strings.Contains(w.Body.String(), `"result":`)) {
			t.Fatalf("MCP 请求返回 %d，期望 %d：%s", w.Code, status, w.Body)
		}
	}
	connect := func(token string, status int) {
		t.Helper()
		request(token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, status)
	}
	const toolsList = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	if state := settings(); state.Enabled || len(state.Tokens) != 0 {
		t.Fatalf("MCP 初始状态有误：%+v", state)
	}
	redirectRequest := httptest.NewRequest(http.MethodGet, mcpPath, nil)
	redirectRequest.Header.Set("X-LPanel-Proxy", "1")
	redirectRequest.Header.Set("X-LPanel-Redirect", "https://panel.example")
	redirectResponse := httptest.NewRecorder()
	a.Handler().ServeHTTP(redirectResponse, redirectRequest)
	if redirectResponse.Code != http.StatusNotFound || redirectResponse.Header().Get("Location") != "" {
		t.Fatalf("关闭时代理入口返回 %d", redirectResponse.Code)
	}
	if w := call(a, http.MethodPut, settingsURL, `{"enabled":true}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("没有令牌时启用返回 %d", w.Code)
	}

	first, second := sign("Agent A"), sign("Agent B")
	state := settings()
	if len(state.Tokens) != 2 || state.Tokens[0].Name != "Agent A" || state.Tokens[1].Name != "Agent B" || first == second {
		t.Fatal("令牌签发结果有误")
	}
	var digest []byte
	if err := a.DB.QueryRowContext(t.Context(), "SELECT digest FROM mcp_tokens WHERE id = ?", state.Tokens[0].ID).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte(first))
	if !bytes.Equal(digest, expected[:]) {
		t.Fatal("数据库中应保存 MCP 令牌摘要")
	}
	connect(first, http.StatusNotFound)
	if w := call(a, http.MethodPut, settingsURL, `{"enabled":true}`, cookie); w.Code != http.StatusOK {
		t.Fatalf("启用 MCP 返回 %d", w.Code)
	}
	connect("", http.StatusUnauthorized)
	connect("invalid", http.StatusUnauthorized)
	connect(first, http.StatusOK)
	connect(second, http.StatusOK)
	revoke(state.Tokens[0].ID)
	request(first, toolsList, http.StatusUnauthorized)
	request(second, toolsList, http.StatusOK)

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root, a.Version)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	a = reopened
	if state = settings(); !state.Enabled || len(state.Tokens) != 1 || state.Tokens[0].Name != "Agent B" {
		t.Fatalf("服务重启后的 MCP 设置有误：%+v", state)
	}
	connect(first, http.StatusUnauthorized)
	connect(second, http.StatusOK)
	if w := call(a, http.MethodPut, settingsURL, `{"enabled":false}`, cookie); w.Code != http.StatusOK {
		t.Fatalf("关闭 MCP 返回 %d", w.Code)
	}
	connect(second, http.StatusNotFound)
	if w := call(a, http.MethodPut, settingsURL, `{"enabled":true}`, cookie); w.Code != http.StatusOK {
		t.Fatalf("重新开启 MCP 返回 %d", w.Code)
	}
	revoke(state.Tokens[0].ID)
	if state := settings(); state.Enabled || len(state.Tokens) != 0 {
		t.Fatalf("撤销最后一枚令牌后的 MCP 设置有误：%+v", state)
	}
	connect(second, http.StatusNotFound)
}
