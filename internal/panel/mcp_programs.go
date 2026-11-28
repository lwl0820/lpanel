package panel

import "net/http"

func (a *App) mcpProgramTools() []mcpTool {
	id := mcpField{Type: "string", Description: "面板登记的程序 ID"}
	name := mcpField{Type: "string", Description: "程序名称"}
	tools := []mcpTool{
		newMCPTool("programs_list", "列出已登记的程序、运行状态和 Supervisor 就绪情况。", http.MethodGet, a.listPrograms,
			map[string]mcpField{}),
		newMCPTool("programs_get", "查看指定程序的工作目录、启动设置和当前状态。", http.MethodGet, a.getProgram,
			map[string]mcpField{"program_id": id}, "program_id"),
		newMCPTool("programs_create", "登记程序并创建工作目录。文件就绪后可保存启动设置。", http.MethodPost, a.createProgram,
			map[string]mcpField{"name": name}, "name"),
		newMCPTool("programs_update", "保存完整启动设置。下次启动或重启时应用。", http.MethodPut, a.updateProgram,
			map[string]mcpField{
				"program_id": id,
				"name":       name,
				"entry":      {Type: "string", Description: "工作目录内已有普通文件的相对路径，空字符串清除入口"},
				"args":       {Type: "string", Description: "一行启动参数，可为空"},
				"run_user":   {Type: "string", Description: "运行程序的系统用户名"},
			}, "program_id", "name", "entry", "args", "run_user"),
		newMCPTool("programs_copy", "复制启动设置和工作目录中的文件，跳过符号链接。副本初始为停止状态。", http.MethodPost, a.copyProgram,
			map[string]mcpField{"program_id": id, "name": {Type: "string", Description: "副本名称"}}, "program_id", "name"),
		newMCPTool("programs_delete", "删除程序记录、域名绑定、托管配置和日志。默认保留工作目录。", http.MethodDelete, a.deleteMCPProgram,
			map[string]mcpField{"program_id": id, "remove_files": {Type: "boolean", Description: "同时删除工作目录及其中的文件"}}, "program_id"),
		newMCPTool("programs_set_state", "启动、停止或重启指定程序，返回操作后的程序状态。", http.MethodPost, a.programAction,
			map[string]mcpField{
				"program_id": id,
				"action":     {Type: "string", Description: "要执行的程序操作", Enum: []string{"start", "stop", "restart"}},
			}, "program_id", "action"),
		newMCPTool("programs_runtime", "读取程序主进程的 PID、资源用量、TCP 连接数和监听端口。", http.MethodGet, a.getProgramRuntime,
			map[string]mcpField{"program_id": id}, "program_id"),
		newMCPTool("programs_logs", "读取程序最近的日志，最多 200 行、从文件末尾读取最多 256 KiB。", http.MethodGet, a.mcpProgramLogs,
			map[string]mcpField{"program_id": id}, "program_id"),
	}
	for i := range tools {
		tool := &tools[i]
		switch tool.Definition.Name {
		case "programs_create", "programs_copy":
			tool.Definition.Annotations["destructiveHint"] = false
		}
		if _, ok := tool.Definition.InputSchema.Properties["program_id"]; ok {
			tool.PathValues = map[string]string{"id": "program_id"}
		}
		if tool.Definition.Name == "programs_set_state" {
			tool.PathValues["action"] = "action"
		}
	}
	return tools
}

func (a *App) deleteMCPProgram(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RemoveFiles bool `json:"remove_files"`
	}
	if ReadJSON(w, r, &input) {
		a.deleteProgramWithFiles(w, r, input.RemoveFiles)
	}
}

func (a *App) mcpProgramLogs(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireProgram(w, r)
	if !ok {
		return
	}
	var cursor programLogCursor
	_, output, err := cursor.read(a.programLogPath(p.ID))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "program_error", "无法读取程序日志："+err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"log": output})
}
