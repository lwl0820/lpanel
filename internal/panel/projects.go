package panel

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
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
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Project struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Directory       string `json:"directory,omitempty"`
	Compose         string `json:"compose,omitempty"`
	Environment     string `json:"environment,omitempty"`
	CustomPorts     []int  `json:"custom_ports"`
	UpdatedAt       string `json:"updated_at"`
	LegacyDirectory bool   `json:"legacy_directory"`
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

type projectLock struct {
	mutex sync.Mutex
	users int
}

// 同一项目的保存与后台操作串行，其他项目可在部署期间继续操作。
func (a *App) lockProject(id string) func() {
	a.projectsMu.Lock()
	if a.projectLocks == nil {
		a.projectLocks = make(map[string]*projectLock)
	}
	lock := a.projectLocks[id]
	if lock == nil {
		lock = &projectLock{}
		a.projectLocks[id] = lock
	}
	lock.users++
	a.projectsMu.Unlock()

	lock.mutex.Lock()
	return func() {
		lock.mutex.Unlock()
		a.projectsMu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(a.projectLocks, id)
		}
		a.projectsMu.Unlock()
	}
}

func (a *App) migrateProjects() error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS projects(
		id TEXT PRIMARY KEY, name TEXT NOT NULL, compose TEXT NOT NULL, environment TEXT NOT NULL, updated_at TEXT NOT NULL,
		directory TEXT NOT NULL, legacy_directory INTEGER NOT NULL DEFAULT 0, custom_ports TEXT NOT NULL DEFAULT '[]'
	)`); err != nil {
		return err
	}
	var hasDirectory, hasCustomPorts bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('projects') WHERE name='directory'), EXISTS(SELECT 1 FROM pragma_table_info('projects') WHERE name='custom_ports')").Scan(&hasDirectory, &hasCustomPorts); err != nil {
		return err
	}
	if !hasDirectory {
		if _, err = tx.Exec(`
			ALTER TABLE projects ADD COLUMN directory TEXT NOT NULL DEFAULT '';
			ALTER TABLE projects ADD COLUMN legacy_directory INTEGER NOT NULL DEFAULT 0;
			UPDATE projects SET directory=id, legacy_directory=1
		`); err != nil {
			return err
		}
	}
	if !hasCustomPorts {
		if _, err = tx.Exec("ALTER TABLE projects ADD COLUMN custom_ports TEXT NOT NULL DEFAULT '[]'"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS projects_directory ON projects(directory)"); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) RegisterProjectRoutes() error {
	if err := a.migrateProjects(); err != nil {
		return err
	}
	a.Mux.Handle("GET /api/v1/projects", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows, err := a.DB.QueryContext(r.Context(), "SELECT id,name,directory,legacy_directory,updated_at,custom_ports FROM projects ORDER BY name")
		if err != nil {
			WriteError(w, 500, "project_error", "读取项目失败")
			return
		}
		defer rows.Close()
		items := []Project{}
		for rows.Next() {
			var p Project
			var directory string
			var customPorts []byte
			if err = rows.Scan(&p.ID, &p.Name, &directory, &p.LegacyDirectory, &p.UpdatedAt, &customPorts); err != nil {
				WriteError(w, 500, "project_error", "读取项目失败")
				return
			}
			if err = jsonv2.Unmarshal(customPorts, &p.CustomPorts); err != nil {
				WriteError(w, 500, "project_error", "读取项目失败")
				return
			}
			p.Directory = a.projectDirectory(directory)
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
	a.Mux.Handle("GET /api/v1/projects/{id}/export", a.RequireAuth(http.HandlerFunc(a.exportProject)))
	a.Mux.Handle("POST /api/v1/projects/import", a.RequireAuth(http.HandlerFunc(a.importProject)))
	a.Mux.Handle("POST /api/v1/projects/scan", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p Project
		if !ReadJSON(w, r, &p) {
			return
		}
		p.Directory = ""
		if p.ID != "" {
			unlock := a.lockProject(p.ID)
			defer unlock()
		}
		if p.ID != "" || p.Name != "" {
			a.projectsMu.Lock()
			previous, status, code, err := a.prepareProject(r.Context(), &p, true)
			a.projectsMu.Unlock()
			if err != nil {
				WriteError(w, status, code, err.Error())
				return
			}
			if previous.Directory != "" {
				// 改名前，配置引用的相对路径仍在原目录中。
				p.Directory = previous.Directory
				if err := a.checkProjectDirectory(p.Directory, true); err != nil {
					WriteError(w, 400, "project_directory", err.Error())
					return
				}
			}
		}
		ports, err := a.previewProject(r.Context(), p)
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
			return a.runProjectAction(ctx, p.ID, action, removeFiles)
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
func (a *App) projectDirectory(name string) string {
	return filepath.Join(a.Root, "data/projects", name)
}

func (a *App) project(ctx context.Context, id string) (Project, error) {
	var p Project
	var directory string
	var customPorts []byte
	err := a.DB.QueryRowContext(ctx, "SELECT id,name,directory,legacy_directory,compose,environment,updated_at,custom_ports FROM projects WHERE id=?", id).
		Scan(&p.ID, &p.Name, &directory, &p.LegacyDirectory, &p.Compose, &p.Environment, &p.UpdatedAt, &customPorts)
	if err != nil {
		return p, err
	}
	if !validProjectDirectoryName(directory) {
		return p, fmt.Errorf("无法确认项目目录，请检查项目记录")
	}
	p.Directory = a.projectDirectory(directory)
	return p, jsonv2.Unmarshal(customPorts, &p.CustomPorts)
}

const maxProjectCustomPort = 65535

func normalizeProjectCustomPorts(ports []int) ([]int, error) {
	unique := []int{}
	seen := make(map[int]struct{})
	for _, port := range ports {
		if port < 1 || port > maxProjectCustomPort {
			return nil, fmt.Errorf("自定义端口必须是 1 至 %d 的整数", maxProjectCustomPort)
		}
		if _, exists := seen[port]; !exists {
			seen[port] = struct{}{}
			unique = append(unique, port)
		}
	}
	return unique, nil
}

func validProjectDirectoryName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}

func (a *App) prepareProject(ctx context.Context, p *Project, scanning bool) (previous Project, status int, code string, err error) {
	if p.ID != "" {
		previous, err = a.project(ctx, p.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return previous, 404, "not_found", fmt.Errorf("项目不存在")
		}
		if err != nil {
			return previous, 500, "project_error", err
		}
	}
	if scanning && p.ID != "" && p.Name == "" {
		p.Name = previous.Name
	}
	if p.Name != previous.Name || p.ID == "" {
		p.Name = strings.TrimSpace(p.Name)
	}
	p.LegacyDirectory = previous.LegacyDirectory
	p.Directory = previous.Directory
	if p.Name != previous.Name || p.ID == "" {
		if len(p.Name) < 1 || len(p.Name) > 100 {
			return previous, 400, "invalid_name", fmt.Errorf("请输入 1 至 100 字节的项目名称")
		}
		if !p.LegacyDirectory {
			if !validProjectDirectoryName(p.Name) {
				return previous, 400, "invalid_name", fmt.Errorf("项目名称不能包含斜杠或空字符，也不能是「.」或「..」")
			}
			p.Directory = a.projectDirectory(p.Name)
		}
		occupied, err := a.projectNameOrDirectoryInUse(ctx, *p)
		if err != nil {
			return previous, 500, "project_error", fmt.Errorf("检查项目名称失败：%w", err)
		}
		if occupied {
			return previous, 409, "project_conflict", fmt.Errorf("该项目名称或目录已被使用，请更换项目名称")
		}
	}
	if err = a.checkProjectDirectory(p.Directory, true); err != nil {
		return previous, 400, "project_directory", err
	}
	if previous.ID != "" && p.Directory != previous.Directory {
		entries, readErr := os.ReadDir(p.Directory)
		if readErr == nil {
			if len(entries) > 0 {
				return previous, 409, "project_conflict", fmt.Errorf("新名称对应的目录中已有文件，无法合并，请更换项目名称")
			}
			if err = a.checkProjectDirectoryForRemoval(p.Directory); err != nil {
				return previous, 400, "project_directory", err
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return previous, 500, "project_error", readErr
		}
		if err = a.checkProjectDirectoryForRemoval(previous.Directory); err != nil {
			return previous, 400, "project_directory", err
		}
	}
	if p.ID == "" && strings.TrimSpace(p.Compose) == "" {
		if err = loadProjectConfig(p); err != nil {
			return previous, 400, "compose_error", err
		}
	}
	return previous, 0, "", nil
}

func (a *App) projectNameOrDirectoryInUse(ctx context.Context, p Project) (bool, error) {
	var occupied bool
	err := a.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id<>? AND (name=? OR directory=?))",
		p.ID, p.Name, filepath.Base(p.Directory)).Scan(&occupied)
	return occupied, err
}

func loadProjectConfig(p *Project) error {
	contents, err := readProjectFiles(p.Directory)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(contents[0])) == "" {
		return fmt.Errorf("项目目录中没有可用的 compose.yaml，请填写 Compose 配置")
	}
	p.Compose, p.Environment = string(contents[0]), string(contents[1])
	return nil
}

func (a *App) saveProject(w http.ResponseWriter, r *http.Request) {
	var p Project
	if !ReadJSON(w, r, &p) {
		return
	}
	ctx := r.Context()
	p.ID = r.PathValue("id")
	if p.ID != "" {
		unlock := a.lockProject(p.ID)
		defer unlock()
	}
	a.projectsMu.Lock()
	defer a.projectsMu.Unlock()

	previous, status, code, err := a.prepareProject(ctx, &p, false)
	if err != nil {
		WriteError(w, status, code, err.Error())
		return
	}
	if p.CustomPorts == nil {
		p.CustomPorts = previous.CustomPorts
	}
	p.CustomPorts, err = normalizeProjectCustomPorts(p.CustomPorts)
	if err != nil {
		WriteError(w, 400, "invalid_custom_ports", err.Error())
		return
	}
	if p.ID == "" {
		p.ID = newProjectID()
	}
	renaming := previous.ID != "" && previous.Directory != p.Directory
	originalDir := p.Directory
	if renaming {
		originalDir = previous.Directory
	}
	original, err := readProjectFiles(originalDir)
	if err != nil {
		WriteError(w, 400, "project_directory", err.Error())
		return
	}
	target, statErr := os.Lstat(p.Directory)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		WriteError(w, 500, "project_error", statErr.Error())
		return
	}
	restoreDirectory := func() error {
		if target == nil {
			if err := os.Remove(p.Directory); !errors.Is(err, os.ErrNotExist) && !errors.Is(err, unix.ENOTEMPTY) {
				return err
			}
		}
		return nil
	}
	if renaming {
		// Linux 可原子替换空目录，目标非空时则保留两边。
		if err = unix.Rename(originalDir, p.Directory); err != nil {
			WriteError(w, 500, "project_error", fmt.Sprintf("移动项目目录失败：%v", err))
			return
		}
		restoreDirectory = func() error {
			if err := os.Rename(p.Directory, originalDir); err != nil {
				return err
			}
			if target != nil {
				return os.Mkdir(p.Directory, target.Mode().Perm())
			}
			return nil
		}
	}
	rollback := func(err error) error {
		return errors.Join(err, setProjectFiles(p.Directory, original), restoreDirectory())
	}
	// 在部署目录校验，使 env_file、include 等相对引用与部署一致。
	if err = a.writeProjectFiles(p, p.Directory); err != nil {
		err = rollback(err)
		WriteError(w, 400, "project_error", fmt.Sprintf("写入项目配置失败：%v", err))
		return
	}
	if _, err = a.scanProject(ctx, p, p.Directory); err != nil {
		err = rollback(err)
		WriteError(w, 400, "compose_error", err.Error())
		return
	}
	if err = a.storeProject(ctx, &p); err != nil {
		err = rollback(err)
		WriteError(w, 500, "project_error", fmt.Sprintf("保存项目失败：%v", err))
		return
	}
	a.writeProjectDeployment(w, p)
}

func newProjectID() string {
	var raw [12]byte
	rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

func (a *App) storeProject(ctx context.Context, p *Project) error {
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	customPorts, err := jsonv2.Marshal(p.CustomPorts)
	if err != nil {
		return err
	}
	_, err = a.DB.ExecContext(ctx, `INSERT INTO projects(id,name,compose,environment,updated_at,directory,legacy_directory,custom_ports) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,compose=excluded.compose,environment=excluded.environment,
		updated_at=excluded.updated_at,directory=excluded.directory,custom_ports=excluded.custom_ports`,
		p.ID, p.Name, p.Compose, p.Environment, p.UpdatedAt, filepath.Base(p.Directory), p.LegacyDirectory, string(customPorts))
	return err
}

func (a *App) writeProjectDeployment(w http.ResponseWriter, p Project) {
	result := struct {
		Project
		Operation       *Operation `json:"operation,omitzero"`
		DeploymentError string     `json:"deployment_error,omitempty"`
	}{Project: p}
	op, err := a.StartOperation("project_deploy", func(ctx context.Context) (string, error) {
		return a.runProjectAction(ctx, p.ID, "deploy", false)
	})
	if err != nil {
		result.DeploymentError = "配置已保存，暂时无法开始部署。请点击「编辑」并重新保存以重试。"
	} else {
		result.Operation = new(op)
	}
	WriteJSON(w, 200, result)
}

var projectConfigNames = [...]string{"compose.yaml", ".env"}

func readProjectFiles(dir string) ([len(projectConfigNames)][]byte, error) {
	var contents [len(projectConfigNames)][]byte
	for i, name := range projectConfigNames {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return contents, err
		}
		if !info.Mode().IsRegular() {
			return contents, fmt.Errorf("项目配置必须是普通文件：%s", path)
		}
		contents[i], err = os.ReadFile(path)
		if err != nil {
			return contents, err
		}
	}
	return contents, nil
}

func setProjectFiles(dir string, contents [len(projectConfigNames)][]byte) error {
	for i, name := range projectConfigNames {
		path := filepath.Join(dir, name)
		if contents[i] == nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		} else if err := os.WriteFile(path, contents[i], 0600); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) writeProjectFiles(p Project, dir string) error {
	if strings.TrimSpace(p.Compose) == "" {
		return fmt.Errorf("Compose 配置不能为空")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return setProjectFiles(dir, [len(projectConfigNames)][]byte{[]byte(p.Compose), []byte(p.Environment)})
}

func (a *App) previewProject(ctx context.Context, p Project) ([]ProjectPort, error) {
	dir, err := os.MkdirTemp(filepath.Join(a.Root, ".tmp"), "compose-scan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := a.writeProjectFiles(p, dir); err != nil {
		return nil, err
	}
	return a.scanProject(ctx, p, dir)
}

func (a *App) scanProject(ctx context.Context, p Project, dir string) ([]ProjectPort, error) {
	projectDir := cmp.Or(p.Directory, dir)
	args := []string{"compose", "--project-name", "lpanel-" + cmp.Or(p.ID, "scan"), "--project-directory", projectDir, "--env-file", filepath.Join(dir, ".env"), "-f", filepath.Join(dir, "compose.yaml"), "config", "--format", "json"}
	if dir != p.Directory {
		// 端口插值使用候选 .env，服务的 env_file 在保存时用正式路径校验。
		args = append(args, "--no-env-resolution")
	}
	out, err := a.Command(ctx, "docker", args...)
	if err != nil && dir != p.Directory && bytes.Contains(out, []byte("unknown flag: --no-env-resolution")) {
		out, err = a.Command(ctx, "docker", args[:len(args)-1]...)
	}
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
	dir := p.Directory
	if err := a.checkProjectDirectory(dir, false); err != nil {
		return nil, err
	}
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
func (a *App) runProjectAction(ctx context.Context, id, action string, removeFiles bool) (string, error) {
	unlock := a.lockProject(id)
	defer unlock()
	p, err := a.project(ctx, id)
	if err != nil {
		return "", err
	}

	if action == "delete" && removeFiles {
		if err := a.checkProjectDirectoryForRemoval(p.Directory); err != nil {
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
		a.projectsMu.Lock()
		defer a.projectsMu.Unlock()
		log := a.redactProjectOutput(p, string(out))
		tx, err := a.DB.BeginTx(ctx, nil)
		if err != nil {
			return log, err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, "DELETE FROM projects WHERE id=?", p.ID); err != nil {
			return log, err
		}
		if removeFiles {
			if err = os.RemoveAll(p.Directory); err != nil {
				return log, fmt.Errorf("删除项目文件失败：%w", err)
			}
			return log, tx.Commit()
		}
		original, err := readProjectFiles(p.Directory)
		if err != nil {
			return log, err
		}
		if err = setProjectFiles(p.Directory, [len(projectConfigNames)][]byte{}); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			err = errors.Join(err, setProjectFiles(p.Directory, original))
		}
		return log, err
	}
	if action == "deploy" || action == "start" || action == "restart" {
		bindings, e := a.ListBindings(ctx)
		if e != nil {
			return a.redactProjectOutput(p, string(out)), e
		}
		bindings = slices.DeleteFunc(bindings, func(b Binding) bool {
			return b.Kind != "project" || b.ResourceID != p.ID || b.HostPort != 0
		})
		if len(bindings) == 0 {
			return a.redactProjectOutput(p, string(out)), nil
		}
		items, e := a.ProjectStatus(ctx, p)
		if e != nil {
			return a.redactProjectOutput(p, string(out)), e
		}
		for _, b := range bindings {
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
func (a *App) checkProjectDirectory(dir string, allowMissing bool) error {
	if filepath.Dir(dir) != a.projectDirectory("") || !validProjectDirectoryName(filepath.Base(dir)) {
		return fmt.Errorf("无法确认项目目录，请检查项目记录")
	}
	for _, path := range []string{filepath.Join(a.Root, "data"), filepath.Dir(dir), dir} {
		info, err := os.Lstat(path)
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("无法确认项目目录：%w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("项目所用位置必须是普通目录：%s", path)
		}
	}
	return nil
}

func (a *App) checkProjectDirectoryForRemoval(dir string) error {
	if err := a.checkProjectDirectory(dir, false); err != nil {
		return err
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
		HostPort        *int   `json:"host_port"`
		Scheme          string `json:"scheme"`
		AutoCertificate bool   `json:"auto_certificate"`
		ACMEAccountID   string `json:"acme_account_id"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	if input.Scheme != "http" && input.Scheme != "https" {
		WriteError(w, 400, "invalid_target", "请选择 HTTP 或 HTTPS 协议")
		return
	}
	b := Binding{Kind: "project", ResourceID: p.ID, Domain: input.Domain, AutoCertificate: input.AutoCertificate}
	if input.HostPort != nil {
		if input.Service != "" || input.ContainerPort != 0 {
			WriteError(w, 400, "invalid_target", "请选择一个端口作为绑定目标")
			return
		}
		if !slices.Contains(p.CustomPorts, *input.HostPort) {
			WriteError(w, 400, "invalid_target", "请选择此项目已保存的自定义端口")
			return
		}
		b.HostPort = *input.HostPort
		b.Upstream = fmt.Sprintf("%s://127.0.0.1:%d", input.Scheme, b.HostPort)
	} else {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(input.Service) || input.ContainerPort < 1 || input.ContainerPort > 65535 {
			WriteError(w, 400, "invalid_target", "请选择有效服务和端口")
			return
		}
		items, err := a.ProjectStatus(r.Context(), p)
		if err != nil {
			WriteError(w, 502, "docker_error", err.Error())
			return
		}
		b.Upstream, err = projectUpstream(items, input.Service, input.ContainerPort, input.Scheme)
		if err != nil {
			WriteError(w, 400, "unpublished_port", err.Error())
			return
		}
		b.Service = input.Service
		b.ContainerPort = input.ContainerPort
	}
	b, err = a.SaveBindingWithAccount(r.Context(), b, input.ACMEAccountID)
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
