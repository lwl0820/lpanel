package panel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"encoding/json/jsontext"
	"encoding/json/v2"
)

func mcpProtocolRequest(a *App, method, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:18080/api/v1/mcp", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	a.serveMCPProtocol(w, r)
	return w
}

func TestMCPProtocolNegotiation(t *testing.T) {
	a := &App{Config: DefaultConfig(), Version: "测试版本"}
	const init = `{"jsonrpc":"2.0","id":"连接","method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"测试","version":"1"}}}`
	for _, tc := range []struct {
		requested, expected string
	}{
		{mcpLatestProtocol, mcpLatestProtocol},
		{"2025-06-18", "2025-06-18"},
		{"1900-01-01", mcpLatestProtocol},
	} {
		w := mcpProtocolRequest(a, http.MethodPost, fmt.Sprintf(init, tc.requested), nil)
		var response struct {
			ID     string `json:"id"`
			Result struct {
				ProtocolVersion string                    `json:"protocolVersion"`
				Capabilities    map[string]map[string]any `json:"capabilities"`
				ServerInfo      map[string]string         `json:"serverInfo"`
			} `json:"result"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || response.ID != "连接" ||
			response.Result.ProtocolVersion != tc.expected || response.Result.Capabilities["tools"] == nil ||
			response.Result.ServerInfo["version"] != a.Version || w.Header().Get("MCP-Session-Id") != "" {
			t.Fatalf("协议协商返回错误：%s", w.Body)
		}
	}

	w := mcpProtocolRequest(a, http.MethodPost, `{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/list"}`, nil)
	var listed struct {
		ID     jsontext.Value `json:"id"`
		Result struct {
			Tools []mcpToolDefinition `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || w.Code != http.StatusOK || string(listed.ID) != "9007199254740993" {
		t.Fatalf("工具列表返回错误：%s", w.Body)
	}
	names := map[string]bool{}
	for _, tool := range listed.Result.Tools {
		if names[tool.Name] || tool.Description == "" || tool.InputSchema.Type != "object" || tool.InputSchema.AdditionalProperties {
			t.Fatalf("工具定义无效：%+v", tool)
		}
		names[tool.Name] = true
	}
	for _, name := range []string{"files_write_text", "containers_set_state", "sites_create", "bindings_update", "certificates_operation"} {
		if !names[name] {
			t.Fatalf("工具列表缺少 %s", name)
		}
	}

	const list = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for _, tc := range []struct {
		name, method, body string
		headers            map[string]string
		status             int
		text               string
	}{
		{"同源", http.MethodPost, list, map[string]string{"Origin": "http://127.0.0.1:18080"}, 200, "tools"},
		{"跨源", http.MethodPost, list, map[string]string{"Origin": "https://example.com"}, 403, "invalid_origin"},
		{"协议不同", http.MethodPost, list, map[string]string{"Origin": "https://127.0.0.1:18080"}, 403, "invalid_origin"},
		{"错误版本", http.MethodPost, list, map[string]string{mcpProtocolHeader: "1900-01-01"}, 400, "unsupported_protocol_version"},
		{"不支持事件流", http.MethodGet, "", nil, 405, ""},
		{"只接收事件流", http.MethodPost, list, map[string]string{"Accept": "text/event-stream"}, 406, "invalid_accept"},
		{"错误媒体类型", http.MethodPost, list, map[string]string{"Content-Type": "text/plain"}, 415, "invalid_content_type"},
		{"初始化通知", http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil, 202, ""},
		{"无编号工具调用", http.MethodPost, `{"jsonrpc":"2.0","method":"tools/call"}`, nil, 400, ""},
		{"错误 JSON", http.MethodPost, `{"invalid"`, nil, 400, "-32700"},
		{"空编号", http.MethodPost, `{"jsonrpc":"2.0","id":null,"method":"tools/list"}`, nil, 400, "-32600"},
		{"批量请求", http.MethodPost, "[" + list + "]", nil, 400, "-32600"},
		{"未知工具", http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"command"}}`, nil, 200, "-32602"},
		{"未知方法", http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"unknown"}`, nil, 200, "-32601"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := mcpProtocolRequest(a, tc.method, tc.body, tc.headers)
			if w.Code != tc.status || tc.text == "" && w.Body.Len() != 0 || !strings.Contains(w.Body.String(), tc.text) {
				t.Fatalf("返回 %d %s，预期 %d %q", w.Code, w.Body, tc.status, tc.text)
			}
		})
	}
}

func TestMCPProxyOrigin(t *testing.T) {
	a := &App{Config: Config{PanelDomain: "panel.example.com", PanelPort: 8443}}
	for _, tc := range []struct {
		host, origin, peer string
		allowed            bool
	}{
		{"panel.example.com:8443", "https://panel.example.com:8443", "127.0.0.1:50000", true},
		{"panel.example.com", "https://panel.example.com", "127.0.0.1:50000", false},
		{"panel.example.com:8443", "https://panel.example.com:8443", "203.0.113.1:50000", false},
		{"attacker.example.com:8443", "https://attacker.example.com:8443", "127.0.0.1:50000", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/api/v1/mcp", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-LPanel-Proxy", "1")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Origin", tc.origin)
		if allowed := a.mcpOriginAllowed(r); allowed != tc.allowed {
			t.Errorf("请求来源 %s，主机 %s，代理 %s，预期允许 %t", tc.origin, tc.host, tc.peer, tc.allowed)
		}
	}
}

func callMCPTestTool(t *testing.T, a *App, name string, arguments map[string]any, wantError bool) jsontext.Value {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := mcpProtocolRequest(a, http.MethodPost, string(body), map[string]string{mcpProtocolHeader: mcpLatestProtocol})
	var response struct {
		Result struct {
			Structured jsontext.Value   `json:"structuredContent"`
			Content    []mcpTextContent `json:"content"`
			IsError    bool             `json:"isError"`
		} `json:"result"`
		Error *mcpRPCError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || response.Error != nil ||
		len(response.Result.Content) != 1 || response.Result.IsError != wantError {
		t.Fatalf("%s 返回异常：%d %s", name, w.Code, w.Body)
	}
	if wantError {
		return jsontext.Value(response.Result.Content[0].Text)
	}
	if response.Result.Structured.Kind() != '{' {
		t.Fatalf("%s 缺少结构化结果：%s", name, w.Body)
	}
	return response.Result.Structured
}

func TestMCPFileToolsUsePanelRestrictions(t *testing.T) {
	a := &App{}
	base := t.TempDir()
	oldPath := filepath.Join(base, "原文 #1.txt")
	if err := os.WriteFile(oldPath, []byte("旧内容"), 0600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "新目录")
	callMCPTestTool(t, a, "files_mkdir", map[string]any{"path": parent}, false)
	if data := callMCPTestTool(t, a, "files_list", map[string]any{"path": base}, false); !strings.Contains(string(data), "原文 #1.txt") {
		t.Fatalf("目录缺少原文件：%s", data)
	}
	pathArgs := map[string]any{"path": oldPath}
	if data := callMCPTestTool(t, a, "files_read_text", pathArgs, false); !strings.Contains(string(data), "旧内容") {
		t.Fatalf("读取内容不符：%s", data)
	}
	callMCPTestTool(t, a, "files_write_text", map[string]any{"path": oldPath}, true)
	callMCPTestTool(t, a, "files_write_text", map[string]any{"path": oldPath, "text": "新内容"}, false)
	callMCPTestTool(t, a, "files_rename", map[string]any{"path": oldPath, "name": "新名.txt"}, false)
	callMCPTestTool(t, a, "files_move", map[string]any{"path": filepath.Join(base, "新名.txt"), "directory": parent}, false)
	moved := filepath.Join(parent, "新名.txt")
	if data, err := os.ReadFile(moved); err != nil || string(data) != "新内容" {
		t.Fatalf("宿主机文件内容 %q，错误 %v", data, err)
	}
	if failure := callMCPTestTool(t, a, "files_delete", map[string]any{"path": "/"}, true); !strings.Contains(string(failure), "invalid_path") {
		t.Fatalf("删除根目录的拒绝结果有误：%s", failure)
	}
	callMCPTestTool(t, a, "files_delete", map[string]any{"path": parent}, false)
	if _, err := os.Stat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("目录仍然存在：%v", err)
	}
}

func TestMCPSiteBindingsAndCertificateTools(t *testing.T) {
	a := domainApp(t)
	a.Command = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "certbot" {
			return []byte("域名验证失败"), errors.New("证书签发失败")
		}
		return nil, nil
	}
	firstDirectory, secondDirectory := t.TempDir(), t.TempDir()
	createResponse := callMCPTestTool(t, a, "sites_create", map[string]any{
		"hostnames": []string{"first.example.com"}, "directory": firstDirectory,
	}, false)
	var site siteSaveResult
	if err := json.Unmarshal(createResponse, &site); err != nil || len(site.Bindings) != 1 || site.ID == "" {
		t.Fatalf("创建网站失败：%s", createResponse)
	}
	callMCPTestTool(t, a, "sites_update", map[string]any{
		"site_id": site.ID, "identifier": "新标识", "directory": secondDirectory, "spa": true, "note": "",
	}, false)
	saved, err := a.site(t.Context(), site.ID)
	if err != nil || saved.Directory != secondDirectory || !saved.SPA {
		t.Fatalf("面板网站未更新：%+v %v", saved, err)
	}
	if data := callMCPTestTool(t, a, "sites_list", map[string]any{}, false); !strings.Contains(string(data), "新标识") {
		t.Fatalf("网站列表未反映变更：%s", data)
	}
	var additional struct {
		Binding Binding `json:"binding"`
	}
	if data := callMCPTestTool(t, a, "bindings_add_site", map[string]any{
		"site_id": site.ID, "domain": "second.example.com",
	}, false); json.Unmarshal(data, &additional) != nil || additional.Binding.ID == "" {
		t.Fatalf("添加域名失败：%s", data)
	}
	bindingID := site.Bindings[0].ID
	callMCPTestTool(t, a, "bindings_update", map[string]any{"binding_id": bindingID, "domain": "edited.example.com"}, false)
	callMCPTestTool(t, a, "bindings_remove", map[string]any{"binding_id": additional.Binding.ID}, false)
	binding, err := a.binding(t.Context(), bindingID)
	if err != nil || binding.Domain != "edited.example.com" || binding.Directory != secondDirectory || !binding.SPA {
		t.Fatalf("面板绑定未同步：%+v %v", binding, err)
	}

	var operation Operation
	if data := callMCPTestTool(t, a, "certificates_request", map[string]any{
		"binding_id": bindingID,
	}, false); json.Unmarshal(data, &operation) != nil || operation.Kind != "certificate" || operation.ID == "" {
		t.Fatalf("证书申请未返回操作编号：%s", data)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		data := callMCPTestTool(t, a, "certificates_operation", map[string]any{"operation_id": operation.ID}, false)
		var status Operation
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatal(err)
		}
		if status.Status == "failed" && strings.Contains(status.Error, "域名验证失败") {
			break
		}
		if status.Status != "running" || time.Now().After(deadline) {
			t.Fatalf("证书失败状态未更新：%s", data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if data := callMCPTestTool(t, a, "bindings_list", map[string]any{}, false); !strings.Contains(string(data), "域名验证失败") {
		t.Fatalf("绑定列表缺少证书失败原因：%s", data)
	}
	callMCPTestTool(t, a, "sites_delete", map[string]any{"site_id": site.ID}, false)
	if _, err := os.Stat(secondDirectory); err != nil {
		t.Fatalf("删除网站时移除了内容目录：%v", err)
	}
}

func TestMCPContainerToolsReuseProjectValidation(t *testing.T) {
	a, root := testApp(t)
	const projectID = "mcp-project"
	const containerID = "0123456789ab"
	if err := os.MkdirAll(filepath.Join(root, "data", "projects", projectID), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(t.Context(), "INSERT INTO projects(id,name,compose,environment,updated_at,directory) VALUES(?,?,?,?,?,?)",
		projectID, "容器测试", "services: {}", "TOKEN=secret-value", "", projectID); err != nil {
		t.Fatal(err)
	}
	var actions []string
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case name == "docker" && len(args) >= 9 && slices.Equal(args[9:], []string{"ps", "--all", "--format", "json"}):
			return fmt.Appendf(nil, `[{"ID":%q,"Service":"web","Name":"web-1","State":"running"}]`, containerID), nil
		case name == "docker" && slices.Equal(args, []string{"logs", "--tail", "200", "--", containerID}):
			return []byte("日志 secret-value"), nil
		case name == "docker" && len(args) == 3 && args[1] == "--" && args[2] == containerID:
			actions = append(actions, args[0])
			return nil, nil
		default:
			return nil, fmt.Errorf("意外命令 %s %v", name, args)
		}
	}
	if data := callMCPTestTool(t, a, "projects_list", map[string]any{}, false); !strings.Contains(string(data), projectID) {
		t.Fatalf("没有返回测试项目：%s", data)
	}
	if data := callMCPTestTool(t, a, "containers_list", map[string]any{"project_id": projectID}, false); !strings.Contains(string(data), containerID) {
		t.Fatalf("没有返回测试容器：%s", data)
	}
	if data := callMCPTestTool(t, a, "containers_logs", map[string]any{"project_id": projectID, "container_id": containerID}, false); strings.Contains(string(data), "secret-value") || !strings.Contains(string(data), "日志 [已隐藏]") {
		t.Fatalf("日志没有脱敏：%s", data)
	}
	callMCPTestTool(t, a, "containers_set_state", map[string]any{"project_id": projectID, "container_id": "other", "action": "stop"}, true)
	callMCPTestTool(t, a, "containers_set_state", map[string]any{"project_id": projectID, "container_id": containerID, "action": "restart"}, true)
	for _, action := range []string{"start", "stop"} {
		callMCPTestTool(t, a, "containers_set_state", map[string]any{"project_id": projectID, "container_id": containerID, "action": action}, false)
	}
	if !slices.Equal(actions, []string{"start", "stop"}) {
		t.Fatalf("容器启停命令错误：%v", actions)
	}
}
