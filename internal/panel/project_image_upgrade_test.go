package panel

import (
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProjectUpgradeImages(t *testing.T) {
	const (
		webImage   = "example.com/web:stable"
		dbImage    = "example.com/db@sha256:1111111111111111111111111111111111111111111111111111111111111111"
		config     = `{"services":{"web":{"image":"` + webImage + `"},"db":{"image":"` + dbImage + `","platform":"linux/arm64","build":{}},"local":{"build":{}}}}`
		webRunning = `{"ID":"web-old","Service":"web","State":"running","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":30001,"Protocol":"tcp"}]}`
		dbStopped  = `{"ID":"db-stopped","Service":"db","State":"exited"}`
	)
	for _, tc := range []struct {
		name, status, config, containerImage, failPull string
		wantUpdate, retry                              bool
	}{
		{name: "旧镜像仍在运行", status: "[" + webRunning + "," + dbStopped + "]", containerImage: "sha256:old", wantUpdate: true},
		{name: "端口刷新失败后重试", status: "[" + webRunning + "," + dbStopped + "]", containerImage: "sha256:old", wantUpdate: true, retry: true},
		{name: "运行镜像已是当前版本", status: "[" + webRunning + "," + dbStopped + "]", containerImage: "sha256:new"},
		{name: "项目全部停止", status: "[" + dbStopped + "]"},
		{name: "项目尚未启动", status: "[]"},
		{name: "较早的镜像拉取失败", status: "[" + webRunning + "]", failPull: dbImage},
		{name: "部分拉取成功后失败", status: "[" + webRunning + "]", failPull: webImage},
		{name: "项目只有构建配置", status: "[]", config: `{"services":{"local":{"build":{}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, root := testApp(t)
			p := Project{ID: "upgrade", Name: "镜像升级", Directory: filepath.Join(root, "data/projects", "镜像升级"), Compose: "services: {}", Environment: "TOKEN=secret-value\n"}
			if err := a.writeProjectFiles(p, p.Directory); err != nil {
				t.Fatal(err)
			}
			if _, err := a.DB.ExecContext(t.Context(), "INSERT INTO projects(id,name,compose,environment,updated_at,directory) VALUES(?,?,?,?,?,?)", p.ID, p.Name, p.Compose, p.Environment, "", p.Name); err != nil {
				t.Fatal(err)
			}
			bindings := []Binding{
				{ID: "web-live", Kind: "project", ResourceID: p.ID, Domain: "web.example.com", Upstream: "https://127.0.0.1:30001", Service: "web", ContainerPort: 80},
				{ID: "db-stopped", Kind: "project", ResourceID: p.ID, Domain: "db.example.com", Upstream: "http://127.0.0.1:30002", Service: "db", ContainerPort: 5432},
				{ID: "web-fixed", Kind: "project", ResourceID: p.ID, Domain: "fixed.example.com", Upstream: "http://127.0.0.1:30003", Service: "web", HostPort: 30003},
				{ID: "other-project", Kind: "project", ResourceID: "other", Domain: "other.example.com", Upstream: "http://127.0.0.1:30004", Service: "web", ContainerPort: 80},
			}
			for _, b := range bindings {
				if err := a.storeBinding(t.Context(), b); err != nil {
					t.Fatal(err)
				}
			}
			a.Config.NginxConfigDir = filepath.Join(root, "nginx")
			a.NginxProbe = func(context.Context, string, string, string) error { return nil }
			composePrefix := []string{"compose", "--project-name", "lpanel-" + p.ID, "--project-directory", p.Directory, "--env-file", filepath.Join(p.Directory, ".env"), "-f", filepath.Join(p.Directory, "compose.yaml")}
			var calls []string
			a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "nginx" || name == "systemctl" {
					return nil, nil
				}
				if name == "docker" {
					switch {
					case slices.Equal(args, append(slices.Clone(composePrefix), "ps", "--all", "--format", "json")):
						if slices.Contains(calls, "重建") {
							calls = append(calls, "刷新端口")
							if tc.retry {
								return []byte("读取发布端口失败"), errors.New("exit status 1")
							}
							return []byte(strings.Replace(tc.status, "30001", "40001", 1)), nil
						}
						calls = append(calls, "记录状态")
						return []byte(tc.status), nil
					case slices.Equal(args, append(slices.Clone(composePrefix), "config", "--format", "json")):
						calls = append(calls, "解析配置")
						if tc.config != "" {
							return []byte(tc.config), nil
						}
						return []byte(config), nil
					case slices.Equal(args, append(slices.Clone(composePrefix), "config", "--environment")):
						calls = append(calls, "读取平台")
						return []byte("DOCKER_DEFAULT_PLATFORM=linux/amd64\n"), nil
					case slices.Equal(args, []string{"image", "pull", "--platform", "linux/amd64", "--", webImage}),
						slices.Equal(args, []string{"image", "pull", "--platform", "linux/arm64", "--", dbImage}):
						ref := args[len(args)-1]
						calls = append(calls, ref)
						if ref == tc.failPull {
							return []byte("仓库拒绝访问 secret-value"), errors.New("exit status 1")
						}
						return []byte("镜像已拉取 secret-value\n"), nil
					case len(args) == 6 && slices.Equal(args[:3], []string{"container", "inspect", "--format"}) && args[4] == "--" && args[len(args)-1] == "web-old":
						calls = append(calls, "读取容器镜像")
						return []byte(tc.containerImage + "\n"), nil
					case slices.Equal(args, []string{"image", "inspect", "--format", "{{.Id}}", "--", webImage}):
						calls = append(calls, "读取仓库镜像")
						return []byte("sha256:new\n"), nil
					case slices.Equal(args, append(slices.Clone(composePrefix), "up", "-d", "--no-deps", "--no-build", "--pull", "never", "--", "web")):
						calls = append(calls, "重建")
						return []byte("容器已启动\n"), nil
					}
				}
				return nil, fmt.Errorf("意外的命令：%s %v", name, args)
			}

			var log string
			var err error
			if tc.retry {
				_, err := a.runProjectAction(t.Context(), p.ID, "upgrade-images", false)
				if err == nil || !strings.Contains(err.Error(), "读取发布端口失败") {
					t.Fatalf("升级未报告刷新端口失败：%v", err)
				}
				calls = nil
				tc.containerImage = "sha256:new"
				tc.status = strings.Replace(tc.status, "30001", "40001", 1)
			}
			if tc.wantUpdate {
				login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
				cookie := login.Result().Cookies()[0]
				response := call(a, "POST", "/api/v1/projects/"+p.ID+"/actions/upgrade-images", "", cookie)
				var operation Operation
				if err := jsonv2.Unmarshal(response.Body.Bytes(), &operation); err != nil || response.Code != http.StatusAccepted || operation.Kind != "project_upgrade-images" {
					t.Fatalf("升级动作未提交：%d %s %v", response.Code, response.Body.String(), err)
				}
				waitProjectOperation(t, a, operation.ID)
				err = a.DB.QueryRowContext(t.Context(), "SELECT log FROM operations WHERE id=?", operation.ID).Scan(&log)
			} else {
				log, err = a.runProjectAction(t.Context(), p.ID, "upgrade-images", false)
			}

			wantCalls := []string{"记录状态", "解析配置", "读取平台"}
			switch {
			case tc.config != "":
				if log != "项目没有可拉取的镜像" {
					t.Fatalf("仅含构建服务的结果有误：%q", log)
				}
			case tc.failPull == dbImage:
				wantCalls = append(wantCalls, dbImage)
			default:
				wantCalls = append(wantCalls, dbImage, webImage)
			}
			if tc.failPull != "" {
				if err == nil || !strings.Contains(err.Error(), "仓库拒绝访问 [已隐藏]") {
					t.Fatalf("拉取失败原因或脱敏有误：%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if tc.containerImage != "" {
				wantCalls = append(wantCalls, "读取容器镜像", "读取仓库镜像")
			}
			if tc.wantUpdate {
				if !tc.retry {
					wantCalls = append(wantCalls, "重建", "刷新端口")
				}
				bindings[0].Upstream = "https://127.0.0.1:40001"
			}
			if !slices.Equal(calls, wantCalls) || strings.Contains(log, "secret-value") {
				t.Fatalf("升级命令或结果有误：%v，期望 %v，日志 %q", calls, wantCalls, log)
			}
			for _, want := range bindings {
				got, err := a.binding(t.Context(), want.ID)
				if err != nil || got.Upstream != want.Upstream {
					t.Fatalf("绑定 %s 上游有误：%s，期望 %s：%v", want.ID, got.Upstream, want.Upstream, err)
				}
			}
		})
	}
}
