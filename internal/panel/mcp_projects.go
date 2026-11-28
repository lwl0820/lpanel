package panel

import "net/http"

func (a *App) mcpProjectTools() []mcpTool {
	create := newMCPTool("projects_create", "新建 Compose 项目并自动部署。提供的配置可覆盖未登记目录中的现有配置。用返回的 operation.id 调用 projects_deployment_operation 查询结果。若已保存但无法发起部署，返回 deployment_error。", http.MethodPost, a.saveProject,
		map[string]mcpField{
			"name":         {Type: "string", Description: "项目名称，同时用于项目目录名"},
			"compose":      {Type: "string", Description: "完整的 Compose 配置。省略或留空时，从对应项目目录读取现有配置"},
			"environment":  {Type: "string", Description: "填写 Compose 配置时可同时提供完整的 .env 内容"},
			"custom_ports": {Type: "array", Description: "面板中登记的自定义端口", Items: new(mcpField{Type: "integer", Description: "端口号"})},
		}, "name")
	deployment := newMCPTool("projects_deployment_operation", "按操作 ID 查询项目保存触发的自动部署进度和失败原因。", http.MethodGet, a.mcpProjectDeploymentOperation,
		map[string]mcpField{"operation_id": {Type: "string", Description: "项目保存返回的 operation.id"}}, "operation_id")
	deployment.PathValues = map[string]string{"id": "operation_id"}
	return []mcpTool{create, deployment}
}

func (a *App) mcpProjectDeploymentOperation(w http.ResponseWriter, r *http.Request) {
	operation, err := a.loadOperation(r.Context(), r.PathValue("id"))
	if err != nil || operation.Kind != "project_deploy" {
		WriteError(w, http.StatusNotFound, "not_found", "项目自动部署记录不存在")
		return
	}
	WriteJSON(w, http.StatusOK, operation)
}
