package panel

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"slices"

	"encoding/json/jsontext"
	"encoding/json/v2"
)

type mcpToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema mcpInputSchema `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type mcpInputSchema struct {
	Type                 string              `json:"type"`
	Properties           map[string]mcpField `json:"properties"`
	Required             []string            `json:"required,omitempty"`
	AdditionalProperties bool                `json:"additionalProperties"`
}

type mcpField struct {
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Enum        []string  `json:"enum,omitempty"`
	Items       *mcpField `json:"items,omitzero"`
}

type mcpTool struct {
	Definition mcpToolDefinition
	Method     string
	Handler    http.HandlerFunc
	PathValues map[string]string
	QueryKeys  []string
}

func newMCPTool(name, description, method string, handler http.HandlerFunc, properties map[string]mcpField, required ...string) mcpTool {
	annotations := map[string]any{"readOnlyHint": method == http.MethodGet}
	if method != http.MethodGet {
		annotations["destructiveHint"] = true
	}
	return mcpTool{
		Definition: mcpToolDefinition{
			Name: name, Description: description, Annotations: annotations,
			InputSchema: mcpInputSchema{Type: "object", Properties: properties, Required: required},
		},
		Method: method, Handler: handler,
	}
}

func (a *App) mcpTools() []mcpTool {
	path := mcpField{Type: "string", Description: "服务器上的绝对路径"}
	directory := mcpField{Type: "string", Description: "服务器上的绝对目录"}
	id := mcpField{Type: "string", Description: "记录的 ID"}
	domain := mcpField{Type: "string", Description: "完整主机名，例如 www.example.com"}
	account := mcpField{Type: "string", Description: "证书账户的 ID。省略时沿用当前绑定的证书账户或使用默认账户"}
	autoCert := mcpField{Type: "boolean", Description: "自动申请证书"}
	projectPath := map[string]string{"id": "project_id"}
	containerPath := map[string]string{"id": "project_id", "containerID": "container_id"}
	sitePath := map[string]string{"id": "site_id"}
	bindingPath := map[string]string{"id": "binding_id"}

	tools := []mcpTool{
		newMCPTool("files_list", "浏览服务器目录，省略路径时从根目录开始。", http.MethodGet, listServerFiles,
			map[string]mcpField{"path": directory}),
		newMCPTool("files_read_text", "读取已有 UTF-8 文本文件，文件最大 1 MiB。", http.MethodGet, serverFileText,
			map[string]mcpField{"path": path}, "path"),
		newMCPTool("files_write_text", "替换已有 UTF-8 文本文件的全部内容，文件最大 1 MiB。", http.MethodPut, serverFileText,
			map[string]mcpField{"path": path, "text": {Type: "string", Description: "保存后的完整文本，可为空"}}, "path", "text"),
		newMCPTool("files_mkdir", "新建目录，父目录须已存在。", http.MethodPost, makeServerDirectory,
			map[string]mcpField{"path": directory}, "path"),
		newMCPTool("files_move", "将文件或目录移动到指定目录，保留原名称。", http.MethodPost, a.transferServerFile(true),
			map[string]mcpField{"path": path, "directory": directory}, "path", "directory"),
		newMCPTool("files_rename", "在原目录内重命名文件或目录。", http.MethodPost, renameServerFile,
			map[string]mcpField{"path": path, "name": {Type: "string", Description: "新的名称"}}, "path", "name"),
		newMCPTool("files_delete", "删除指定条目。删除目录会同时删除其内容。", http.MethodDelete, deleteServerFile,
			map[string]mcpField{"path": path}, "path"),
		newMCPTool("projects_list", "列出面板登记的 Compose 项目。", http.MethodGet, a.listProjects,
			map[string]mcpField{}),
		newMCPTool("containers_list", "列出指定项目的容器及其运行状态。", http.MethodGet, a.projectStatus,
			map[string]mcpField{"project_id": id}, "project_id"),
		newMCPTool("containers_logs", "读取指定容器的最近 200 行日志。", http.MethodGet, a.projectContainerLogs,
			map[string]mcpField{"project_id": id, "container_id": id}, "project_id", "container_id"),
		newMCPTool("containers_set_state", "启动或停止指定容器。", http.MethodPost, a.projectContainerAction,
			map[string]mcpField{"project_id": id, "container_id": id, "action": {
				Type: "string", Description: "要执行的容器操作", Enum: []string{"start", "stop"},
			}}, "project_id", "container_id", "action"),
		newMCPTool("sites_list", "列出静态网站。主机名可通过 bindings_list 查询。", http.MethodGet, a.listSites,
			map[string]mcpField{}),
		newMCPTool("sites_create", "创建静态网站并配置主机名。", http.MethodPost, a.handleCreateSite,
			map[string]mcpField{
				"identifier":       {Type: "string", Description: "面板内的网站标识，省略时使用第一个主机名"},
				"directory":        {Type: "string", Description: "网页绝对目录，省略时创建默认目录"},
				"hostnames":        {Type: "array", Description: "网站的主机名列表", Items: &domain},
				"spa":              {Type: "boolean", Description: "开启单页应用回退"},
				"note":             {Type: "string", Description: "网站备注"},
				"auto_certificate": autoCert,
				"acme_account_id":  account,
			}, "hostnames"),
		newMCPTool("sites_update", "保存静态网站的标识、目录、单页应用回退和备注。修改主机名请调用域名绑定工具。", http.MethodPut, a.handleUpdateSite,
			map[string]mcpField{
				"site_id":    id,
				"identifier": {Type: "string", Description: "网站标识"},
				"directory":  directory,
				"spa":        {Type: "boolean", Description: "开启单页应用回退"},
				"note":       {Type: "string", Description: "网站备注，可为空"},
			}, "site_id", "identifier", "directory", "spa", "note"),
		newMCPTool("sites_delete", "删除静态网站及其主机名绑定，保留网站目录。", http.MethodDelete, a.handleDeleteSite,
			map[string]mcpField{"site_id": id}, "site_id"),
		newMCPTool("bindings_list", "列出域名绑定及证书状态、签发失败原因和到期时间。", http.MethodGet, a.listBindings,
			map[string]mcpField{}),
		newMCPTool("bindings_add_site", "为静态网站添加主机名绑定。", http.MethodPost, a.handleCreateSiteBinding,
			map[string]mcpField{"site_id": id, "domain": domain, "auto_certificate": autoCert, "acme_account_id": account},
			"site_id", "domain"),
		newMCPTool("bindings_update", "修改现有域名绑定的主机名或证书选项，继续使用已保存的目标。", http.MethodPut, a.updateMCPBinding,
			map[string]mcpField{"binding_id": id, "domain": domain, "auto_certificate": autoCert, "acme_account_id": account},
			"binding_id", "domain"),
		newMCPTool("bindings_remove", "解除已绑定的主机名，保留网站目录或项目。", http.MethodDelete, a.deleteMCPBinding,
			map[string]mcpField{"binding_id": id}, "binding_id"),
		newMCPTool("certificates_request", "为绑定的域名申请证书，返回后台操作记录。", http.MethodPost, a.handleRequestCertificate,
			map[string]mcpField{"binding_id": id, "acme_account_id": account}, "binding_id"),
		newMCPTool("certificates_operation", "按操作 ID 查询证书申请的进度和失败原因。", http.MethodGet, a.mcpCertificateOperation,
			map[string]mcpField{"operation_id": id}, "operation_id"),
	}
	for i := range tools {
		tool := &tools[i]
		switch tool.Definition.Name {
		case "files_list", "files_read_text", "files_write_text", "files_delete":
			tool.QueryKeys = []string{"path"}
		case "containers_list":
			tool.PathValues = projectPath
		case "containers_logs":
			tool.PathValues = containerPath
		case "containers_set_state":
			tool.PathValues = map[string]string{"id": "project_id", "containerID": "container_id", "action": "action"}
		case "sites_update", "sites_delete":
			tool.PathValues = sitePath
		case "bindings_add_site":
			tool.PathValues = sitePath
			tool.Definition.Annotations["destructiveHint"] = false
		case "files_mkdir", "sites_create":
			tool.Definition.Annotations["destructiveHint"] = false
		case "bindings_update", "bindings_remove", "certificates_request":
			tool.PathValues = bindingPath
		case "certificates_operation":
			tool.PathValues = map[string]string{"id": "operation_id"}
		}
	}
	return tools
}

type mcpToolResult struct {
	Content           []mcpTextContent `json:"content"`
	StructuredContent any              `json:"structuredContent,omitzero"`
	IsError           bool             `json:"isError,omitzero"`
}

type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *mcpToolResponse) Header() http.Header { return w.header }

func (w *mcpToolResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *mcpToolResponse) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(data)
}

func (a *App) callMCPTool(ctx context.Context, raw jsontext.Value) (any, *mcpRPCError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments jsontext.Value `json:"arguments"`
	}
	if json.Unmarshal(raw, &params) != nil || params.Name == "" {
		return nil, &mcpRPCError{Code: -32602, Message: "请提供 MCP 工具名称"}
	}
	tools := a.mcpTools()
	i := slices.IndexFunc(tools, func(tool mcpTool) bool { return tool.Definition.Name == params.Name })
	if i < 0 {
		return nil, &mcpRPCError{Code: -32602, Message: "MCP 工具不存在"}
	}
	tool := tools[i]
	arguments := params.Arguments
	if arguments == nil {
		arguments = jsontext.Value("{}")
	}
	var fields map[string]jsontext.Value
	if arguments.Kind() != '{' || json.Unmarshal(arguments, &fields) != nil {
		return nil, &mcpRPCError{Code: -32602, Message: "工具参数须为对象"}
	}
	schema := tool.Definition.InputSchema
	for _, key := range schema.Required {
		if _, ok := fields[key]; !ok {
			return mcpToolFailure("缺少工具参数 " + key), nil
		}
	}
	for key, value := range fields {
		field, exists := schema.Properties[key]
		if !exists || !mcpValidField(field, value) {
			return mcpToolFailure("工具参数无效 " + key), nil
		}
	}

	// 在进程内调用明确注册的业务处理器。只传入工具参数及请求上下文。
	query := url.Values{}
	for _, key := range tool.QueryKeys {
		var value string
		if field, ok := fields[key]; ok {
			_ = json.Unmarshal(field, &value)
		}
		if key == "path" && tool.Definition.Name == "files_list" && value == "" {
			value = "/"
		}
		query.Set(key, value)
	}
	request, err := http.NewRequestWithContext(ctx, tool.Method, "/?"+query.Encode(), bytes.NewReader(arguments))
	if err != nil {
		return nil, &mcpRPCError{Code: -32603, Message: "无法构造业务请求"}
	}
	request.Header.Set("Content-Type", "application/json")
	for name, key := range tool.PathValues {
		var value string
		_ = json.Unmarshal(fields[key], &value)
		request.SetPathValue(name, value)
	}
	response := &mcpToolResponse{header: make(http.Header)}
	tool.Handler(response, request)
	body := jsontext.Value(bytes.TrimSpace(response.body.Bytes()))
	if response.status < 200 || response.status >= 300 {
		if !body.IsValid() {
			return nil, &mcpRPCError{Code: -32603, Message: "业务处理失败"}
		}
		return mcpToolFailure(string(body)), nil
	}
	var structured any = body
	switch body.Kind() {
	case '{':
	case '[':
		structured = map[string]jsontext.Value{"items": body}
		encoded, err := json.Marshal(structured)
		if err != nil {
			return nil, &mcpRPCError{Code: -32603, Message: "无法编码业务响应"}
		}
		body = encoded
	default:
		return nil, &mcpRPCError{Code: -32603, Message: "业务响应格式错误"}
	}
	return mcpToolResult{
		Content:           []mcpTextContent{{Type: "text", Text: string(body)}},
		StructuredContent: structured,
	}, nil
}

func mcpToolFailure(message string) mcpToolResult {
	return mcpToolResult{Content: []mcpTextContent{{Type: "text", Text: message}}, IsError: true}
}

func mcpValidField(field mcpField, value jsontext.Value) bool {
	switch field.Type {
	case "string":
		if value.Kind() != '"' {
			return false
		}
		if len(field.Enum) != 0 {
			var text string
			return json.Unmarshal(value, &text) == nil && slices.Contains(field.Enum, text)
		}
		return true
	case "boolean":
		return value.Kind() == 't' || value.Kind() == 'f'
	case "array":
		if value.Kind() != '[' {
			return false
		}
		var items []jsontext.Value
		if json.Unmarshal(value, &items) != nil {
			return false
		}
		for _, item := range items {
			if !mcpValidField(*field.Items, item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (a *App) updateMCPBinding(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Domain          string `json:"domain"`
		AutoCertificate *bool  `json:"auto_certificate"`
		ACMEAccountID   string `json:"acme_account_id"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	ctx := r.Context()
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	binding, err := a.binding(ctx, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "not_found", "域名绑定不存在")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "binding_error", err.Error())
		return
	}
	binding.Domain = input.Domain
	if input.AutoCertificate != nil {
		binding.AutoCertificate = *input.AutoCertificate
	}
	binding, err = a.saveBindingLocked(ctx, binding, input.ACMEAccountID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "binding_error", err.Error())
		return
	}
	a.writeSavedBinding(w, binding, input.ACMEAccountID)
}

func (a *App) deleteMCPBinding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a.domainsMu.Lock()
	defer a.domainsMu.Unlock()
	binding, err := a.binding(ctx, r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, http.StatusNotFound, "not_found", "域名绑定不存在")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "binding_error", err.Error())
		return
	}
	if binding.Kind == "panel" {
		WriteError(w, http.StatusBadRequest, "binding_error", "无法解除面板自身的主机名")
		return
	}
	if err := a.deleteBindingLocked(ctx, binding); err != nil {
		WriteError(w, http.StatusBadRequest, "binding_error", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) mcpCertificateOperation(w http.ResponseWriter, r *http.Request) {
	operation, err := a.loadOperation(r.Context(), r.PathValue("id"))
	if err != nil || operation.Kind != "certificate" {
		WriteError(w, http.StatusNotFound, "not_found", "证书申请记录不存在")
		return
	}
	WriteJSON(w, http.StatusOK, operation)
}
