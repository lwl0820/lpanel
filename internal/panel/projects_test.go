package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestProjectPortNormalization(t *testing.T) {
	ports, err := parseProjectPorts([]byte("time=now level=warning msg=\"version obsolete\"\n" + `{"services":{"web":{"ports":[{"target":80,"published":"18080"},{"target":81},{"target":53,"published":"18053","protocol":"udp"}],"expose":["90","91/udp","92-93"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 7 {
		t.Fatalf("扫描结果：%+v", ports)
	}
	if ports[0].Protocol != "udp" || ports[1].Published != "18080" || ports[2].Source != "ports" || ports[2].Published != "" {
		t.Fatalf("端口语义错误：%+v", ports)
	}
	for _, p := range ports {
		if p.Source == "expose" && p.Published != "" {
			t.Fatal("内部端口被发布")
		}
	}
}
func TestProjectStatusJSONFormats(t *testing.T) {
	for _, data := range []string{"", `[]`, `{"Service":"web","State":"running","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":32345,"Protocol":"tcp"}]}` + "\n" + `{"Service":"db","State":"exited"}`} {
		items, err := decodeProjectContainers([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(data, "web") {
			if len(items) != 2 {
				t.Fatal(items)
			}
			upstream, err := projectUpstream(items, "web", 80, "http")
			if err != nil || upstream != "http://127.0.0.1:32345" {
				t.Fatalf("动态端口错误 %s %v", upstream, err)
			}
			if _, err = projectUpstream(items, "db", 5432, "http"); err == nil {
				t.Fatal("已停止服务不应可绑定")
			}
		}
	}
	ipv6, err := projectUpstream([]ProjectContainer{{Service: "web", State: "running", Publishers: []ProjectPublisher{{URL: "::1", TargetPort: 80, PublishedPort: 18080, Protocol: "tcp"}}}}, "web", 80, "https")
	if err != nil || ipv6 != "https://[::1]:18080" {
		t.Fatalf("IPv6错误：%s %v", ipv6, err)
	}
}
func TestProjectSaveAndSecrets(t *testing.T) {
	a, root := testApp(t)
	// 路由由入口注册，测试也允许单独加载模块。
	var count int
	_ = a.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='projects'").Scan(&count)
	if count == 0 {
		if err := a.RegisterProjectRoutes(); err != nil {
			t.Fatal(err)
		}
	}
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "docker" || len(args) < 9 {
			return nil, fmt.Errorf("意外的命令：%s %v", name, args)
		}
		switch {
		case slices.Equal(args[9:], []string{"config", "--format", "json"}):
			config, err := os.ReadFile(args[8])
			if err != nil {
				return nil, err
			}
			if string(config) == "bad" {
				return []byte("secret-value"), fmt.Errorf("配置无效")
			}
			return []byte(`{"services":{"web":{"ports":[{"target":80,"published":"18080"}]}}}`), nil
		case slices.Equal(args[9:], []string{"up", "-d", "--remove-orphans"}):
			return os.ReadFile(args[8])
		case slices.Equal(args[9:], []string{"ps", "--all", "--format", "json"}):
			return []byte(`[]`), nil
		}
		return nil, fmt.Errorf("意外的命令：%s %v", name, args)
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	if call(a, "GET", "/api/v1/projects", "", nil).Code != 401 {
		t.Fatal("项目接口未认证")
	}
	const projectsPath = "/api/v1/projects"
	var projectID, lastOperationID string
	for _, save := range []struct {
		method, name, image string
	}{
		{http.MethodPost, "测试网站", "nginx:alpine"},
		{http.MethodPut, "新名称", "nginx:stable"},
	} {
		path := projectsPath
		if save.method == http.MethodPut {
			path += "/" + projectID
		}
		compose := "services:\n  web:\n    image: " + save.image + "\n"
		body := fmt.Sprintf(`{"name":%q,"compose":%q,"environment":"TOKEN=secret-value"}`, save.name, compose)
		w := call(a, save.method, path, body, cookie)
		if w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
		var p struct {
			Project
			Operation Operation `json:"operation"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.ID == "" || p.Name != save.name || (projectID != "" && p.ID != projectID) {
			t.Fatalf("保存项目返回了错误的项目数据：%s %q %q", save.method, p.ID, p.Name)
		}
		if p.Operation.ID == "" || p.Operation.ID == lastOperationID || p.Operation.Kind != "project_deploy" || p.Operation.Status != "running" {
			t.Fatalf("%s 保存项目未返回新的部署操作：%+v", save.method, p.Operation)
		}
		projectID, lastOperationID = p.ID, p.Operation.ID
		var operation Operation
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
			status := call(a, "GET", "/api/v1/operations/"+p.Operation.ID, "", cookie)
			if status.Code != http.StatusOK {
				t.Fatal(status.Body.String())
			}
			if err := json.Unmarshal(status.Body.Bytes(), &operation); err != nil {
				t.Fatal(err)
			}
			if operation.Status != "running" {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if operation.Status != "success" || operation.Log != compose {
			t.Fatalf("%s 未部署刚保存的配置：%+v", save.method, operation)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "data/projects", projectID, ".env")); err != nil {
		t.Fatal(err)
	}
	list := call(a, "GET", "/api/v1/projects", "", cookie)
	if strings.Contains(list.Body.String(), "secret-value") || strings.Contains(list.Body.String(), "TOKEN") {
		t.Fatal("列表泄露环境变量")
	}
	var listed []Project
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].Directory != filepath.Join(root, "data/projects", projectID) {
		t.Fatalf("列表未返回实际项目目录：%s", list.Body.String())
	}
	badConfig := `{"name":"错误配置","compose":"bad","environment":"TOKEN=secret-value"}`
	bad := call(a, "POST", "/api/v1/projects", badConfig, cookie)
	if bad.Code != 400 || strings.Contains(bad.Body.String(), "secret-value") {
		t.Fatal(bad.Body.String())
	}
	bad = call(a, "PUT", "/api/v1/projects/"+projectID, badConfig, cookie)
	if bad.Code != 400 || strings.Contains(bad.Body.String(), "secret-value") {
		t.Fatal(bad.Body.String())
	}
	if err := a.DB.QueryRowContext(t.Context(), "SELECT count(*) FROM operations").Scan(&count); err != nil || count != 2 {
		t.Fatalf("配置校验失败时启动了部署：操作数 %d，错误 %v", count, err)
	}
	stored, err := a.project(t.Context(), projectID)
	if err != nil || stored.Name != "新名称" {
		t.Fatal("错误配置覆盖了项目")
	}
}

func TestProjectCreateKeepsProjectWhenDeploymentCannotStart(t *testing.T) {
	a, _ := testApp(t)
	a.Command = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"services":{"web":{}}}`), nil
	}
	if _, err := a.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_deployment BEFORE INSERT ON operations
		WHEN NEW.kind = 'project_deploy' BEGIN SELECT RAISE(ABORT, '测试拒绝提交部署'); END`); err != nil {
		t.Fatal(err)
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	w := call(a, "POST", "/api/v1/projects", `{"name":"待部署项目","compose":"services: {}"}`, login.Result().Cookies()[0])
	var result struct {
		Project
		Operation       *Operation `json:"operation"`
		DeploymentError string     `json:"deployment_error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Operation != nil || !strings.Contains(result.DeploymentError, "重试") {
		t.Fatal(w.Body.String())
	}
	stored, err := a.project(t.Context(), result.ID)
	if err != nil || stored.Name != "待部署项目" {
		t.Fatalf("部署无法启动时未保留项目：%+v %v", stored, err)
	}
}

func TestProjectContainerLogsAndActions(t *testing.T) {
	a, _ := testApp(t)
	const firstID = "0123456789aa"
	const secondID = "0123456789bb"
	const otherID = "0123456789cc"
	for _, projectID := range []string{"replicas", "other"} {
		if _, err := a.DB.ExecContext(t.Context(), "INSERT INTO projects VALUES(?,?,?,?,?)", projectID, "容器日志测试", "services: {}", "TOKEN=secret-value", ""); err != nil {
			t.Fatal(err)
		}
	}
	var failLogs, failAction bool
	var actions [][]string
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "docker" && len(args) >= 9 && slices.Equal(args[9:], []string{"ps", "--all", "--format", "json"}) {
			if args[2] == "lpanel-other" {
				return fmt.Appendf(nil, `[{"ID":%q,"Service":"web","Name":"other-web-1"}]`, otherID), nil
			}
			return fmt.Appendf(nil, `[{"ID":%q,"Service":"web","Name":"web-1"},{"ID":%q,"Service":"web","Name":"web-2"}]`, firstID, secondID), nil
		}
		if name == "docker" && len(args) == 5 && slices.Equal(args[:4], []string{"logs", "--tail", "200", "--"}) {
			if failLogs {
				return []byte("日志驱动错误 secret-value"), errors.New("读取失败")
			}
			return []byte(args[4] + " secret-value"), nil
		}
		if name == "docker" && len(args) == 3 && (args[0] == "start" || args[0] == "stop") {
			actions = append(actions, args)
			if failAction {
				return []byte("容器网络错误 secret-value"), errors.New("启动失败")
			}
			return nil, nil
		}
		return nil, fmt.Errorf("意外的命令：%s %v", name, args)
	}

	const path = "/api/v1/projects/replicas/containers/"
	if w := call(a, "GET", path+firstID+"/logs", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("读取容器日志未验证会话：%s", w.Body.String())
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	for _, id := range []string{firstID, secondID} {
		w := call(a, "GET", path+id+"/logs", "", cookie)
		var result struct {
			Log string `json:"log"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || result.Log != id+" [已隐藏]" {
			t.Fatalf("日志没有对应所选容器或脱敏失败：%s", w.Body.String())
		}
	}
	if w := call(a, "GET", path+otherID+"/logs", "", cookie); w.Code != http.StatusNotFound {
		t.Fatalf("跨项目读取日志未拒绝：%s", w.Body.String())
	}
	failLogs = true
	w := call(a, "GET", path+firstID+"/logs", "", cookie)
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "secret-value") || !strings.Contains(w.Body.String(), "日志驱动错误") {
		t.Fatalf("日志错误提示或脱敏失败：%s", w.Body.String())
	}

	for _, tt := range []struct {
		path   string
		cookie *http.Cookie
		status int
	}{
		{firstID + "/actions/start", nil, http.StatusUnauthorized},
		{firstID + "/actions/restart", cookie, http.StatusBadRequest},
		{otherID + "/actions/stop", cookie, http.StatusNotFound},
		{firstID + "/actions/start", cookie, http.StatusOK},
		{secondID + "/actions/stop", cookie, http.StatusOK},
	} {
		w := call(a, "POST", path+tt.path, "", tt.cookie)
		if w.Code != tt.status {
			t.Fatalf("容器操作 %s 返回 %d，期望 %d：%s", tt.path, w.Code, tt.status, w.Body.String())
		}
	}
	if len(actions) != 2 || !slices.Equal(actions[0], []string{"start", "--", firstID}) || !slices.Equal(actions[1], []string{"stop", "--", secondID}) {
		t.Fatalf("操作命令未限定到所选容器：%v", actions)
	}
	failAction = true
	w = call(a, "POST", path+firstID+"/actions/start", "", cookie)
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "secret-value") || !strings.Contains(w.Body.String(), "容器网络错误") {
		t.Fatalf("容器操作错误提示或脱敏失败：%s", w.Body.String())
	}
}

func TestProjectDeletePreservesVolumesAndStopBindings(t *testing.T) {
	a, root := testApp(t)
	var count int
	_ = a.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='projects'").Scan(&count)
	if count == 0 {
		if err := a.RegisterProjectRoutes(); err != nil {
			t.Fatal(err)
		}
	}
	p := Project{ID: "projecttest", Name: "测试", Compose: "services: {}", Environment: "PORT=18080"}
	_, err := a.DB.Exec("INSERT INTO projects VALUES(?,?,?,?,?)", p.ID, p.Name, p.Compose, p.Environment, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "data/projects", p.ID, "keep.txt")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte("保留"), 0600)
	binding := Binding{ID: "project-binding", Kind: "project", ResourceID: p.ID, Domain: "app.example.com", Upstream: "http://127.0.0.1:18080", Service: "web", ContainerPort: 80}
	if err = a.storeBinding(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	a.Config.NginxConfigDir = filepath.Join(root, "nginx")
	a.NginxProbe = func(context.Context, string, string, string) error { return nil }
	var commands []string
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	if _, err = a.runProjectAction(context.Background(), p, "stop", false); err != nil {
		t.Fatal(err)
	}
	bindings, err := a.ListBindings(context.Background())
	if err != nil || len(bindings) != 1 {
		t.Fatal("停止项目删除了绑定")
	}
	if _, err = a.runProjectAction(context.Background(), p, "delete", false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("删除移除了挂载目录")
	}
	for _, command := range commands {
		if strings.Contains(command, "--volumes") {
			t.Fatal("删除传入了--volumes")
		}
	}
	bindings, err = a.ListBindings(context.Background())
	if err != nil || len(bindings) != 0 {
		t.Fatal("删除未清理绑定")
	}
}

func TestProjectDeleteFiles(t *testing.T) {
	for _, scenario := range []struct {
		name                    string
		removeFiles, failDocker bool
		symlink, keepDir        bool
	}{
		{name: "默认保留", keepDir: true},
		{name: "删除项目目录", removeFiles: true},
		{name: "容器清理失败", removeFiles: true, failDocker: true, keepDir: true},
		{name: "目录链接到外部位置", removeFiles: true, symlink: true, keepDir: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			a, root := testApp(t)
			const id = "cleanup"
			if _, err := a.DB.ExecContext(t.Context(), "INSERT INTO projects VALUES(?,?,?,?,?)", id, "清理测试", "services: {}", "", ""); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "data/projects", id)
			outside := filepath.Join(root, "保留数据")
			relativeData := filepath.Join(dir, "data")
			if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(outside, 0700); err != nil {
				t.Fatal(err)
			}
			if scenario.symlink {
				if err := os.Symlink(outside, dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(relativeData, 0700); err != nil {
				t.Fatal(err)
			}
			outsideFile := filepath.Join(outside, "目录外数据")
			if err := os.WriteFile(outsideFile, []byte("保留"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(relativeData, "目录内数据"), []byte("相对路径挂载数据"), 0600); err != nil {
				t.Fatal(err)
			}
			a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "docker" || len(args) < 9 || !slices.Equal(args[9:], []string{"down", "--remove-orphans"}) {
					return nil, fmt.Errorf("意外的命令：%s %v", name, args)
				}
				if scenario.failDocker {
					return nil, errors.New("容器移除失败")
				}
				return nil, nil
			}

			login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
			cookie := login.Result().Cookies()[0]
			path := "/api/v1/projects/" + id + "/actions/delete"
			if scenario.removeFiles {
				path += "?remove_files=true"
			}
			w := call(a, "POST", path, "", cookie)
			var operation Operation
			if err := json.Unmarshal(w.Body.Bytes(), &operation); err != nil || w.Code != http.StatusAccepted || operation.ID == "" {
				t.Fatalf("删除操作未启动：%s", w.Body.String())
			}
			for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
				w = call(a, "GET", "/api/v1/operations/"+operation.ID, "", cookie)
				if err := json.Unmarshal(w.Body.Bytes(), &operation); err != nil || w.Code != http.StatusOK {
					t.Fatalf("未能读取删除状态：%s", w.Body.String())
				}
				if operation.Status != "running" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			switch {
			case scenario.failDocker:
				if operation.Status != "failed" || !strings.Contains(operation.Error, "容器移除失败") {
					t.Fatalf("容器清理失败未报告：%+v", operation)
				}
			case scenario.symlink:
				if operation.Status != "failed" || !strings.Contains(operation.Error, "普通目录") {
					t.Fatalf("目录链接未被拒绝：%+v", operation)
				}
			default:
				if operation.Status != "success" {
					t.Fatalf("删除失败：%+v", operation)
				}
			}
			if scenario.keepDir {
				if _, err := os.Stat(filepath.Join(relativeData, "目录内数据")); err != nil {
					t.Fatalf("应保留的项目文件丢失：%v", err)
				}
			} else if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("项目目录未删除：%s，错误：%v", dir, err)
			}
			if file, err := os.ReadFile(outsideFile); err != nil || string(file) != "保留" {
				t.Fatalf("项目目录外的数据有误：%q，错误：%v", file, err)
			}
			w = call(a, "GET", "/api/v1/projects/"+id, "", cookie)
			want := http.StatusNotFound
			if operation.Status == "failed" {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("项目记录状态有误：%s", w.Body.String())
			}
		})
	}
}

func TestProjectRuntimeRefreshesPublishedPort(t *testing.T) {
	a, root := testApp(t)
	p := Project{ID: "refresh", Name: "测试"}
	a.Config.NginxConfigDir = filepath.Join(root, "nginx")
	a.NginxProbe = func(context.Context, string, string, string) error { return nil }
	b := Binding{ID: "bound", Kind: "project", ResourceID: p.ID, Domain: "app.example.com", Upstream: "http://127.0.0.1:18080", Service: "web", ContainerPort: 80}
	if err := a.storeBinding(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "docker" && args[len(args)-1] == "json" {
			return []byte(`{"Service":"web","State":"running","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":32345,"Protocol":"tcp"}]}`), nil
		}
		return nil, nil
	}
	if _, err := a.runProjectAction(context.Background(), p, "deploy", false); err != nil {
		t.Fatal(err)
	}
	got, err := a.binding(context.Background(), b.ID)
	if err != nil || got.Upstream != "http://127.0.0.1:32345" {
		t.Fatalf("没有刷新上游：%+v %v", got, err)
	}
}

func TestProjectComposeCLINormalization(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("本机没有 Docker CLI")
	}
	a, root := testApp(t)
	if _, err := a.Command(context.Background(), "docker", "compose", "version"); err != nil {
		t.Skip("本机没有 Compose CLI")
	}
	p := Project{Name: "真实解析", Environment: "LPANEL_COMPOSE_TEST_PUBLISHED=18080", Compose: `version: "3.8"
services:
  web:
    image: nginx:alpine
    ports:
      - "${LPANEL_COMPOSE_TEST_PUBLISHED}:80"
      - target: 81
        published: "18081"
        host_ip: 127.0.0.1
        protocol: tcp
      - "18053:53/udp"
    expose:
      - "90-91"
      - "92/udp"
`}
	ports, err := a.scanProject(context.Background(), p, filepath.Join(root, ".tmp/actual-compose"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 6 {
		t.Fatalf("实际扫描结果错误：%+v", ports)
	}
	found := false
	for _, port := range ports {
		if port.Service == "web" && port.ContainerPort == 80 && port.Published == "18080" && port.Source == "ports" {
			found = true
		}
	}
	if !found {
		t.Fatalf("环境变量没有正确展开：%+v", ports)
	}
}
