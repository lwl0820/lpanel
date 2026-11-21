package panel

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Directory   string `json:"directory,omitempty"`
	Compose     string `json:"compose,omitempty"`
	Environment string `json:"environment,omitempty"`
	UpdatedAt   string `json:"updated_at"`
}
type ProjectPort struct {
	Service       string `json:"service"`
	ContainerPort int    `json:"container_port"`
	Published     string `json:"published"`
	HostIP        string `json:"host_ip"`
	Protocol      string `json:"protocol"`
	Source        string `json:"source"`
}
type ProjectPublisher struct {
	URL           string `json:"URL"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}
type ProjectContainer struct {
	ID         string             `json:"ID"`
	Name       string             `json:"Name"`
	Service    string             `json:"Service"`
	State      string             `json:"State"`
	Health     string             `json:"Health"`
	ExitCode   int                `json:"ExitCode"`
	Publishers []ProjectPublisher `json:"Publishers"`
}

func (a *App) RegisterProjectRoutes() error {
	if _, err := a.DB.Exec(`CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,compose TEXT NOT NULL,environment TEXT NOT NULL,updated_at TEXT NOT NULL)`); err != nil {
		return err
	}
	a.Mux.Handle("GET /api/v1/projects", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := a.DB.QueryContext(r.Context(), "SELECT id,name,updated_at FROM projects ORDER BY name")
		if err != nil {
			WriteError(w, 500, "project_error", "读取项目失败")
			return
		}
		defer rows.Close()
		items := []Project{}
		for rows.Next() {
			var p Project
			if err = rows.Scan(&p.ID, &p.Name, &p.UpdatedAt); err != nil {
				WriteError(w, 500, "project_error", "读取项目失败")
				return
			}
			p.Directory = a.projectDirectory(p.ID)
			items = append(items, p)
		}
		if err = rows.Err(); err != nil {
			WriteError(w, 500, "project_error", "读取项目失败")
			return
		}
		WriteJSON(w, 200, items)
	})))
	a.Mux.Handle("GET /api/v1/projects/{id}", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.project(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, 404, "not_found", "项目不存在")
			return
		}
		WriteJSON(w, 200, p)
	})))
	a.Mux.Handle("POST /api/v1/projects", a.RequireAuth(http.HandlerFunc(a.saveProject)))
	a.Mux.Handle("PUT /api/v1/projects/{id}", a.RequireAuth(http.HandlerFunc(a.saveProject)))
	a.Mux.Handle("POST /api/v1/projects/scan", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p Project
		if !ReadJSON(w, r, &p) {
			return
		}
		dir, err := os.MkdirTemp(filepath.Join(a.Root, ".tmp"), "compose-scan-")
		if err != nil {
			WriteError(w, 500, "scan_error", err.Error())
			return
		}
		defer os.RemoveAll(dir)
		if p.ID != "" {
			if _, err := a.project(r.Context(), p.ID); err != nil {
				WriteError(w, 404, "not_found", "项目不存在")
				return
			}
		}
		ports, err := a.scanProject(r.Context(), p, dir)
		if err != nil {
			WriteError(w, 400, "compose_error", err.Error())
			return
		}
		WriteJSON(w, 200, ports)
	})))
	a.Mux.Handle("POST /api/v1/projects/{id}/actions/{action}", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.project(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, 404, "not_found", "项目不存在")
			return
		}
		action := r.PathValue("action")
		switch action {
		case "deploy", "start", "stop", "restart", "delete":
		default:
			WriteError(w, 400, "invalid_action", "项目操作无效")
			return
		}
		removeFiles := action == "delete" && r.URL.Query().Get("remove_files") == "true"
		op, err := a.StartOperation("project_"+action, func(ctx context.Context) (string, error) {
			return a.runProjectAction(ctx, p, action, removeFiles)
		})
		if err != nil {
			WriteError(w, 500, "operation_error", err.Error())
			return
		}
		WriteJSON(w, 202, op)
	})))
	a.Mux.Handle("GET /api/v1/projects/{id}/status", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.project(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, 404, "not_found", "项目不存在")
			return
		}
		items, err := a.ProjectStatus(r.Context(), p)
		if err != nil {
			WriteError(w, 502, "docker_error", err.Error())
			return
		}
		WriteJSON(w, 200, items)
	})))
	a.Mux.Handle("GET /api/v1/projects/{id}/logs", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.project(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, 404, "not_found", "项目不存在")
			return
		}
		out, err := a.projectCommand(r.Context(), p, "logs", "--no-color", "--tail", "200")
		if err != nil {
			WriteError(w, 502, "docker_error", err.Error())
			return
		}
		WriteJSON(w, 200, map[string]string{"log": a.redactProjectOutput(p, string(out))})
	})))
	a.Mux.Handle("GET /api/v1/projects/{id}/containers/{containerID}/logs", a.RequireAuth(http.HandlerFunc(a.projectContainerLogs)))
	a.Mux.Handle("POST /api/v1/projects/{id}/containers/{containerID}/actions/{action}", a.RequireAuth(http.HandlerFunc(a.projectContainerAction)))
	a.Mux.Handle("POST /api/v1/projects/{id}/bindings", a.RequireAuth(http.HandlerFunc(a.bindProject)))
	return nil
}
func (a *App) projectDirectory(id string) string {
	return filepath.Join(a.Root, "data/projects", id)
}
func (a *App) project(ctx context.Context, id string) (Project, error) {
	var p Project
	err := a.DB.QueryRowContext(ctx, "SELECT id,name,compose,environment,updated_at FROM projects WHERE id=?", id).Scan(&p.ID, &p.Name, &p.Compose, &p.Environment, &p.UpdatedAt)
	if err == nil {
		p.Directory = a.projectDirectory(p.ID)
	}
	return p, err
}
func (a *App) saveProject(w http.ResponseWriter, r *http.Request) {
	var p Project
	if !ReadJSON(w, r, &p) {
		return
	}
	p.ID = r.PathValue("id")
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 100 {
		WriteError(w, 400, "invalid_name", "请输入不超过 100 字节的项目名称")
		return
	}
	if p.ID != "" {
		if _, err := a.project(r.Context(), p.ID); err != nil {
			WriteError(w, 404, "not_found", "项目不存在")
			return
		}
	} else {
		raw := make([]byte, 12)
		if _, err := rand.Read(raw); err != nil {
			WriteError(w, 500, "project_error", err.Error())
			return
		}
		p.ID = hex.EncodeToString(raw)
	}
	// 在候选目录验证，避免错误配置覆盖正在运行项目的文件。
	candidate, err := os.MkdirTemp(filepath.Join(a.Root, ".tmp"), "compose-save-")
	if err != nil {
		WriteError(w, 500, "project_error", err.Error())
		return
	}
	defer os.RemoveAll(candidate)
	if _, err = a.scanProject(r.Context(), p, candidate); err != nil {
		WriteError(w, 400, "compose_error", err.Error())
		return
	}
	p.Directory = a.projectDirectory(p.ID)
	if err = a.writeProjectFiles(p, p.Directory); err != nil {
		WriteError(w, 500, "project_error", err.Error())
		return
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err = a.DB.ExecContext(r.Context(), "INSERT INTO projects(id,name,compose,environment,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,compose=excluded.compose,environment=excluded.environment,updated_at=excluded.updated_at", p.ID, p.Name, p.Compose, p.Environment, p.UpdatedAt); err != nil {
		WriteError(w, 500, "project_error", "保存项目失败")
		return
	}
	result := struct {
		Project
		Operation       *Operation `json:"operation,omitzero"`
		DeploymentError string     `json:"deployment_error,omitempty"`
	}{Project: p}
	op, err := a.StartOperation("project_deploy", func(ctx context.Context) (string, error) {
		return a.runProjectAction(ctx, p, "deploy", false)
	})
	if err != nil {
		result.DeploymentError = "配置已保存，暂时无法开始部署。请点击「编辑」并重新保存以重试。"
	} else {
		result.Operation = new(op)
	}
	WriteJSON(w, 200, result)
}
func (a *App) writeProjectFiles(p Project, dir string) error {
	if strings.TrimSpace(p.Compose) == "" {
		return fmt.Errorf("Compose 配置不能为空")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(p.Compose), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".env"), []byte(p.Environment), 0600)
}
func (a *App) scanProject(ctx context.Context, p Project, dir string) ([]ProjectPort, error) {
	if err := a.writeProjectFiles(p, dir); err != nil {
		return nil, err
	}
	projectDir := dir
	if p.ID != "" {
		projectDir = a.projectDirectory(p.ID)
		if err := os.MkdirAll(projectDir, 0700); err != nil {
			return nil, err
		}
	}
	out, err := a.Command(ctx, "docker", "compose", "--project-name", "lpanel-scan", "--project-directory", projectDir, "--env-file", filepath.Join(dir, ".env"), "-f", filepath.Join(dir, "compose.yaml"), "config", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("Compose 配置校验失败：%s：%w", a.redactProjectOutput(p, string(out)), err)
	}
	return parseProjectPorts(out)
}

// Compose 会把过时 version 等提示写到 stderr，命令合并输出时须保留真正的 JSON 正文。
func projectJSON(data []byte) []byte {
	data = bytes.TrimSpace(data)
	for len(data) > 0 && data[0] != '{' && data[0] != '[' {
		_, rest, ok := bytes.Cut(data, []byte("\n"))
		if !ok {
			return data
		}
		data = bytes.TrimSpace(rest)
	}
	return data
}
func parseProjectPorts(data []byte) ([]ProjectPort, error) {
	var config struct {
		Services map[string]struct {
			Ports []struct {
				Target    int    `json:"target"`
				Published string `json:"published"`
				HostIP    string `json:"host_ip"`
				Protocol  string `json:"protocol"`
			} `json:"ports"`
			Expose []string `json:"expose"`
		} `json:"services"`
	}
	if err := json.Unmarshal(projectJSON(data), &config); err != nil {
		return nil, fmt.Errorf("无法读取 Compose 端口：%w", err)
	}
	items := []ProjectPort{}
	for service, value := range config.Services {
		for _, port := range value.Ports {
			protocol := port.Protocol
			if protocol == "" {
				protocol = "tcp"
			}
			items = append(items, ProjectPort{Service: service, ContainerPort: port.Target, Published: port.Published, HostIP: port.HostIP, Protocol: protocol, Source: "ports"})
		}
		for _, raw := range value.Expose {
			portText, protocol, hasProtocol := strings.Cut(raw, "/")
			if !hasProtocol {
				protocol = "tcp"
			}
			left, right, hasRange := strings.Cut(portText, "-")
			first, err := strconv.Atoi(left)
			if err != nil {
				return nil, fmt.Errorf("无法读取 expose 端口：%s", raw)
			}
			last := first
			if hasRange {
				last, err = strconv.Atoi(right)
				if err != nil || last < first {
					return nil, fmt.Errorf("无法读取 expose 端口范围：%s", raw)
				}
			}
			if first < 1 || last > 65535 {
				return nil, fmt.Errorf("expose 端口超出范围")
			}
			for port := first; port <= last; port++ {
				items = append(items, ProjectPort{Service: service, ContainerPort: port, Protocol: protocol, Source: "expose"})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.ContainerPort != b.ContainerPort {
			return a.ContainerPort < b.ContainerPort
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Protocol < b.Protocol
	})
	return items, nil
}
func (a *App) projectCommand(ctx context.Context, p Project, args ...string) ([]byte, error) {
	dir := a.projectDirectory(p.ID)
	command := []string{"compose", "--project-name", "lpanel-" + p.ID, "--project-directory", dir, "--env-file", filepath.Join(dir, ".env"), "-f", filepath.Join(dir, "compose.yaml")}
	command = append(command, args...)
	out, err := a.Command(ctx, "docker", command...)
	if err != nil {
		return nil, fmt.Errorf("Docker Compose 执行失败：%s：%w", a.redactProjectOutput(p, string(out)), err)
	}
	return out, nil
}
func (a *App) redactProjectOutput(p Project, output string) string {
	for _, line := range strings.Split(p.Environment, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if strings.HasPrefix(line, "#") {
			continue
		}
		_, value, ok := strings.Cut(line, "=")
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		if ok && value != "" {
			output = strings.ReplaceAll(output, value, "[已隐藏]")
		}
	}
	return output
}
func decodeProjectContainers(data []byte) ([]ProjectContainer, error) {
	items := []ProjectContainer{}
	data = projectJSON(data)
	if len(data) == 0 {
		return items, nil
	}
	if data[0] == '[' {
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var item ProjectContainer
		err := dec.Decode(&item)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
func (a *App) ProjectStatus(ctx context.Context, p Project) ([]ProjectContainer, error) {
	out, err := a.projectCommand(ctx, p, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	return decodeProjectContainers(out)
}
func (a *App) requireProjectContainer(w http.ResponseWriter, r *http.Request) (Project, bool) {
	ctx := r.Context()
	p, err := a.project(ctx, r.PathValue("id"))
	if err != nil {
		WriteError(w, 404, "not_found", "项目不存在")
		return Project{}, false
	}
	items, err := a.ProjectStatus(ctx, p)
	if err != nil {
		WriteError(w, 502, "docker_error", err.Error())
		return Project{}, false
	}
	id := r.PathValue("containerID")
	if !slices.ContainsFunc(items, func(item ProjectContainer) bool { return item.ID == id }) {
		WriteError(w, 404, "not_found", "此项目中没有该容器，请刷新项目列表")
		return Project{}, false
	}
	return p, true
}
func (a *App) projectContainerLogs(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireProjectContainer(w, r)
	if !ok {
		return
	}
	out, err := a.Command(r.Context(), "docker", "logs", "--tail", "200", "--", r.PathValue("containerID"))
	log := a.redactProjectOutput(p, string(out))
	if err != nil {
		WriteError(w, 502, "docker_error", fmt.Sprintf("读取容器日志失败：%s：%v", log, err))
		return
	}
	WriteJSON(w, 200, map[string]string{"log": log})
}
func (a *App) projectContainerAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var label string
	switch action {
	case "start":
		label = "启动"
	case "stop":
		label = "停止"
	default:
		WriteError(w, 400, "invalid_action", "容器操作无效")
		return
	}
	p, ok := a.requireProjectContainer(w, r)
	if !ok {
		return
	}
	out, err := a.Command(r.Context(), "docker", action, "--", r.PathValue("containerID"))
	if err != nil {
		detail := cmp.Or(strings.TrimSpace(string(out)), err.Error())
		WriteError(w, 502, "docker_error", label+"容器失败："+a.redactProjectOutput(p, detail))
		return
	}
	WriteJSON(w, 200, map[string]string{"message": "容器已" + label})
}
func projectUpstream(items []ProjectContainer, service string, port int, scheme string) (string, error) {
	for _, item := range items {
		if item.Service != service || item.State != "running" {
			continue
		}
		for _, pub := range item.Publishers {
			if pub.TargetPort != port || pub.Protocol != "tcp" || pub.PublishedPort < 1 {
				continue
			}
			host := pub.URL
			if host == "" || host == "0.0.0.0" {
				host = "127.0.0.1"
			}
			if host == "::" {
				host = "::1"
			}
			return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(pub.PublishedPort)), nil
		}
	}
	return "", fmt.Errorf("目标服务没有可用的已发布 TCP 端口，请先部署并启动项目")
}
func (a *App) runProjectAction(ctx context.Context, p Project, action string, removeFiles bool) (string, error) {
	if action == "delete" && removeFiles {
		if err := a.checkProjectDirectoryForRemoval(p.ID); err != nil {
			return "", err
		}
	}
	args := map[string][]string{"deploy": {"up", "-d", "--remove-orphans"}, "start": {"start"}, "stop": {"stop"}, "restart": {"restart"}, "delete": {"down", "--remove-orphans"}}[action]
	out, err := a.projectCommand(ctx, p, args...)
	if err != nil {
		return "", err
	}
	if action == "delete" {
		bindings, e := a.ListBindings(ctx)
		if e != nil {
			return a.redactProjectOutput(p, string(out)), e
		}
		for _, b := range bindings {
			if b.Kind == "project" && b.ResourceID == p.ID {
				if e = a.DeleteBinding(ctx, b.ID); e != nil {
					return a.redactProjectOutput(p, string(out)), e
				}
			}
		}
		if removeFiles {
			if err = os.RemoveAll(a.projectDirectory(p.ID)); err != nil {
				return a.redactProjectOutput(p, string(out)), fmt.Errorf("删除项目文件失败：%w", err)
			}
		}
		_, err = a.DB.ExecContext(ctx, "DELETE FROM projects WHERE id=?", p.ID)
		return a.redactProjectOutput(p, string(out)), err
	}
	if action == "deploy" || action == "start" || action == "restart" {
		items, e := a.ProjectStatus(ctx, p)
		if e != nil {
			return a.redactProjectOutput(p, string(out)), e
		}
		bindings, e := a.ListBindings(ctx)
		if e != nil {
			return a.redactProjectOutput(p, string(out)), e
		}
		for _, b := range bindings {
			if b.Kind != "project" || b.ResourceID != p.ID {
				continue
			}
			scheme := "http"
			if u, e := url.Parse(b.Upstream); e == nil && u.Scheme == "https" {
				scheme = "https"
			}
			upstream, e := projectUpstream(items, b.Service, b.ContainerPort, scheme)
			if e != nil {
				return a.redactProjectOutput(p, string(out)), e
			}
			b.Upstream = upstream
			if _, e = a.SaveBinding(ctx, b); e != nil {
				return a.redactProjectOutput(p, string(out)), e
			}
		}
	}
	return a.redactProjectOutput(p, string(out)), nil
}
func (a *App) checkProjectDirectoryForRemoval(id string) error {
	if !filepath.IsLocal(id) || id == "." || id != filepath.Base(id) {
		return fmt.Errorf("无法确认项目目录，请检查项目记录")
	}
	dir := a.projectDirectory(id)
	for _, path := range []string{filepath.Join(a.Root, "data"), filepath.Dir(dir), dir} {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("无法确认项目目录：%w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("无法清理项目文件，所选位置必须是普通目录：%s", path)
		}
	}
	// 面板根路径可能经过符号链接，挂载点按系统实际路径识别。
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("无法确认项目目录：%w", err)
	}
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("无法检查项目目录的挂载点：%w", err)
	}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for line := range strings.SplitSeq(string(mountInfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountPoint := unescape.Replace(fields[4])
		if mountPoint == realDir || strings.HasPrefix(mountPoint, realDir+string(os.PathSeparator)) {
			return fmt.Errorf("项目目录有宿主机挂载点，请先卸载：%s", mountPoint)
		}
	}
	return nil
}
func (a *App) bindProject(w http.ResponseWriter, r *http.Request) {
	p, err := a.project(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteError(w, 404, "not_found", "项目不存在")
		return
	}
	var input struct {
		Domain          string `json:"domain"`
		Service         string `json:"service"`
		ContainerPort   int    `json:"container_port"`
		Scheme          string `json:"scheme"`
		AutoCertificate bool   `json:"auto_certificate"`
		ACMEAccountID   string `json:"acme_account_id"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	if (input.Scheme != "http" && input.Scheme != "https") || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(input.Service) || input.ContainerPort < 1 || input.ContainerPort > 65535 {
		WriteError(w, 400, "invalid_target", "请选择有效服务、端口和 HTTP/HTTPS 协议")
		return
	}
	items, err := a.ProjectStatus(r.Context(), p)
	if err != nil {
		WriteError(w, 502, "docker_error", err.Error())
		return
	}
	upstream, err := projectUpstream(items, input.Service, input.ContainerPort, input.Scheme)
	if err != nil {
		WriteError(w, 400, "unpublished_port", err.Error())
		return
	}
	b, err := a.SaveBindingWithAccount(r.Context(), Binding{Kind: "project", ResourceID: p.ID, Domain: input.Domain, Upstream: upstream, Service: input.Service, ContainerPort: input.ContainerPort, AutoCertificate: input.AutoCertificate}, input.ACMEAccountID)
	if err != nil {
		WriteError(w, 400, "binding_error", err.Error())
		return
	}
	var operation *Operation
	if b.AutoCertificate && b.CertificateStatus != "enabled" {
		op, e := a.StartOperation("certificate", func(ctx context.Context) (string, error) {
			err := a.RequestCertificateWithAccount(ctx, b.ID, input.ACMEAccountID)
			return "证书申请流程结束", err
		})
		if e != nil {
			WriteError(w, 500, "operation_error", e.Error())
			return
		}
		operation = &op
	}
	WriteJSON(w, 200, map[string]any{"binding": b, "operation": operation})
}
