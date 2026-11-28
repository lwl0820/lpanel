package panel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMCPProjectCreationAndDeployment(t *testing.T) {
	a, _ := testApp(t)
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "docker" && len(args) > 9 {
			switch args[9] {
			case "config":
				return []byte(`{"services":{"web":{}}}`), nil
			case "up":
				if strings.HasSuffix(args[4], "/部署失败") {
					return []byte("镜像不存在 TOKEN-VALUE"), errors.New("退出状态 1")
				}
				return []byte("已启动"), nil
			}
		}
		return nil, fmt.Errorf("意外命令 %s %v", name, args)
	}

	login := call(a, http.MethodPost, "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	const settingsURL = mcpPath + "/settings"
	const tokensURL = mcpPath + "/tokens"
	var token map[string]string
	w := call(a, http.MethodPost, tokensURL, `{"name":"Compose 测试令牌"}`, cookie)
	if err := json.Unmarshal(w.Body.Bytes(), &token); w.Code != http.StatusCreated || err != nil || token["token"] == "" {
		t.Fatalf("签发测试令牌失败：HTTP %d，%v", w.Code, err)
	}
	var settings mcpSettings
	w = call(a, http.MethodGet, settingsURL, "", cookie)
	if err := json.Unmarshal(w.Body.Bytes(), &settings); w.Code != http.StatusOK || err != nil || len(settings.Tokens) != 1 {
		t.Fatalf("读取 MCP 设置失败：HTTP %d，%v", w.Code, err)
	}
	enable := func(enabled bool) {
		t.Helper()
		w := call(a, http.MethodPut, settingsURL, fmt.Sprintf(`{"enabled":%t}`, enabled), cookie)
		if w.Code != http.StatusOK {
			t.Fatalf("更改 MCP 设置失败：%d %s", w.Code, w.Body)
		}
	}
	request := func(t *testing.T, tool string, arguments map[string]any, wantStatus int, wantError bool) jsontext.Value {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": arguments},
		})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, mcpPath, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token["token"])
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != wantStatus {
			t.Fatalf("%s 返回 %d %s，预期 %d", tool, w.Code, w.Body, wantStatus)
		}
		if wantStatus != http.StatusOK {
			return nil
		}
		var response struct {
			Result struct {
				Structured jsontext.Value   `json:"structuredContent"`
				Content    []mcpTextContent `json:"content"`
				IsError    bool             `json:"isError"`
			} `json:"result"`
			Error *mcpRPCError `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Error != nil ||
			len(response.Result.Content) != 1 || response.Result.IsError != wantError {
			t.Fatalf("%s 返回异常：%s", tool, w.Body)
		}
		if wantError {
			return jsontext.Value(response.Result.Content[0].Text)
		}
		return response.Result.Structured
	}

	enable(true)
	for _, tc := range []struct{ name, status, reason string }{
		{"部署成功", "success", ""},
		{"部署失败", "failed", "镜像不存在 [已隐藏]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arguments := map[string]any{
				"name": tc.name, "compose": "services:\n  web:\n    image: nginx:alpine\n",
				"environment": "TOKEN=TOKEN-VALUE", "custom_ports": []int{8080},
			}
			data := request(t, "projects_create", arguments, http.StatusOK, false)
			var result struct {
				Project
				Operation Operation `json:"operation"`
			}
			if err := json.Unmarshal(data, &result); err != nil || result.ID == "" || result.Name != tc.name ||
				!slices.Equal(result.CustomPorts, []int{8080}) || result.Operation.ID == "" || result.Operation.Kind != "project_deploy" {
				t.Fatalf("创建项目未返回项目及部署记录：%s", data)
			}
			operationArguments := map[string]any{"operation_id": result.Operation.ID}
			for deadline := time.Now().Add(5 * time.Second); ; {
				data = request(t, "projects_deployment_operation", operationArguments, http.StatusOK, false)
				var status Operation
				if err := json.Unmarshal(data, &status); err != nil || status.ID != result.Operation.ID {
					t.Fatalf("部署记录有误：%s", data)
				}
				if status.Status == tc.status && strings.Contains(status.Error, tc.reason) && !strings.Contains(status.Error, "TOKEN-VALUE") {
					break
				}
				if status.Status != "running" || time.Now().After(deadline) {
					t.Fatalf("部署终态有误：%s", data)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if data := request(t, "projects_create", arguments, http.StatusOK, true); !strings.Contains(string(data), "project_conflict") {
				t.Fatalf("未拒绝同名项目：%s", data)
			}
			arguments["id"] = result.ID
			if data := request(t, "projects_create", arguments, http.StatusOK, true); !strings.Contains(string(data), "工具参数无效 id") {
				t.Fatalf("未拒绝项目 ID：%s", data)
			}
		})
	}
	if _, err := a.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_mcp_deployment BEFORE INSERT ON operations
		WHEN NEW.kind = 'project_deploy' BEGIN SELECT RAISE(ABORT, '不启动测试部署'); END`); err != nil {
		t.Fatal(err)
	}
	data := request(t, "projects_create", map[string]any{"name": "已保存待部署", "compose": "services: {}"}, http.StatusOK, false)
	var saved struct {
		Project
		Operation       *Operation `json:"operation"`
		DeploymentError string     `json:"deployment_error"`
	}
	if err := json.Unmarshal(data, &saved); err != nil || saved.ID == "" || saved.Operation != nil || !strings.Contains(saved.DeploymentError, "配置已保存") {
		t.Fatalf("未返回已保存及部署启动错误：%s", data)
	}
	if _, err := a.project(t.Context(), saved.ID); err != nil {
		t.Fatalf("部署无法启动后读取项目失败：%v", err)
	}

	enable(false)
	request(t, "projects_create", map[string]any{}, http.StatusNotFound, false)
	enable(true)
	var second map[string]string
	w = call(a, http.MethodPost, tokensURL, `{"name":"备用令牌"}`, cookie)
	if err := json.Unmarshal(w.Body.Bytes(), &second); w.Code != http.StatusCreated || err != nil || second["token"] == "" {
		t.Fatalf("签发备用令牌失败：HTTP %d，%v", w.Code, err)
	}
	if w := call(a, http.MethodDelete, tokensURL+"/"+settings.Tokens[0].ID, "", cookie); w.Code != http.StatusOK {
		t.Fatalf("撤销令牌失败：%d %s", w.Code, w.Body)
	}
	request(t, "projects_deployment_operation", map[string]any{}, http.StatusUnauthorized, false)
}
