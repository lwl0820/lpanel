package panel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectCustomPortBinding(t *testing.T) {
	a, root := testApp(t)
	a.Config.NginxConfigDir = filepath.Join(root, "nginx")
	a.NginxProbe = func(context.Context, string, string, string) error { return nil }
	const projectID = "fixed-ports"
	p := Project{ID: projectID, Name: projectID, Directory: a.projectDirectory(projectID), Compose: "services: {}"}
	if err := a.writeProjectFiles(p, p.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(t.Context(), "INSERT INTO projects(id,name,compose,environment,updated_at,directory,custom_ports) VALUES(?,?,?,?,?,?,?)", p.ID, p.Name, p.Compose, "", "", p.Name, "[8080]"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.ExecContext(t.Context(), "INSERT INTO projects(id,name,compose,environment,updated_at,directory,custom_ports) VALUES(?,?,?,?,?,?,?)", "other", "other", p.Compose, "", "", "other", "[8081]"); err != nil {
		t.Fatal(err)
	}
	published := 0
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "docker" && args[len(args)-1] == "json" {
			if published == 0 {
				return nil, errors.New("无法读取 Docker 端口")
			}
			return fmt.Appendf(nil, `{"Service":"web","State":"running","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":%d,"Protocol":"tcp"}]}`, published), nil
		}
		return nil, nil
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	const bindPath = "/api/v1/projects/" + projectID + "/bindings"
	for _, body := range []string{
		`{"domain":"invalid.example.com","host_port":8081,"scheme":"http"}`,
		`{"domain":"invalid.example.com","host_port":8080,"service":"web","container_port":80,"scheme":"http"}`,
	} {
		if w := call(a, http.MethodPost, bindPath, body, cookie); w.Code != http.StatusBadRequest {
			t.Fatalf("应拒绝其他项目端口或混用端口目标：%d %s", w.Code, w.Body.String())
		}
	}
	bind := func(path, body string) Binding {
		t.Helper()
		w := call(a, http.MethodPost, path, body, cookie)
		if w.Code != http.StatusOK {
			t.Fatalf("绑定失败：%d %s", w.Code, w.Body.String())
		}
		var result struct {
			Binding Binding `json:"binding"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Binding
	}
	fixed := bind(bindPath, `{"domain":"fixed.example.com","host_port":8080,"scheme":"https","http2":false}`)
	another := bind(bindPath, `{"domain":"another.example.com","host_port":8080,"scheme":"http"}`)
	if fixed.HTTP2 == nil || *fixed.HTTP2 || another.HTTP2 != nil {
		t.Fatal("新建域名应区分显式关闭 HTTP/2 和使用默认设置")
	}
	edit := call(a, http.MethodPut, bindPath+"/"+another.ID, `{"domain":"another.example.com","scheme":"http","http2":false}`, cookie)
	if edit.Code != http.StatusOK {
		t.Fatalf("项目域名编辑失败：%d %s", edit.Code, edit.Body.String())
	}
	if saved, err := a.binding(t.Context(), another.ID); err != nil || saved.HTTP2 == nil || *saved.HTTP2 {
		t.Fatalf("项目编辑应保存 HTTP/2 关闭设置：%+v，%v", saved, err)
	}
	if fixed.HostPort != 8080 || fixed.Upstream != "https://127.0.0.1:8080" || another.HostPort != 8080 || another.Upstream != "http://127.0.0.1:8080" {
		t.Fatalf("两个域名应可共享自定义端口：%+v，%+v", fixed, another)
	}
	if _, err := a.runProjectAction(t.Context(), p.ID, "start", false); err != nil {
		t.Fatalf("仅有固定端口时不需要读取 Docker 端口：%v", err)
	}
	if _, err := a.DB.ExecContext(t.Context(), "UPDATE projects SET custom_ports='[]' WHERE id=?", p.ID); err != nil {
		t.Fatal(err)
	}
	if w := call(a, http.MethodPost, bindPath, `{"domain":"removed.example.com","host_port":8080,"scheme":"http"}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("不应允许使用已移除的候选端口：%d %s", w.Code, w.Body.String())
	}
	fixed.Domain = "renamed.example.com"
	fixed.HTTP2 = nil
	body, err := json.Marshal(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if edited := bind("/api/v1/bindings", string(body)); edited.HostPort != fixed.HostPort || edited.Upstream != fixed.Upstream || edited.Domain != fixed.Domain {
		t.Fatalf("编辑域名应保留固定端口：%+v", edited)
	} else if edited.HTTP2 == nil || *edited.HTTP2 {
		t.Fatal("编辑时省略 HTTP/2 字段应保留原设置")
	}
	published = 18080
	dynamic := bind(bindPath, `{"domain":"dynamic.example.com","service":"web","container_port":80,"scheme":"http"}`)
	for i, action := range []string{"start", "restart", "deploy"} {
		published = 32345 + i
		if _, err := a.runProjectAction(t.Context(), p.ID, action, false); err != nil {
			t.Fatalf("%s：%v", action, err)
		}
		for _, want := range []Binding{fixed, another, {ID: dynamic.ID, Upstream: fmt.Sprintf("http://127.0.0.1:%d", published)}} {
			got, err := a.binding(t.Context(), want.ID)
			if err != nil || got.Upstream != want.Upstream || got.HostPort != want.HostPort {
				t.Fatalf("%s 后绑定发生意外变化：%+v，%v", action, got, err)
			}
		}
	}
	config, err := os.ReadFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, upstream := range []string{fixed.Upstream, another.Upstream, fmt.Sprintf("http://127.0.0.1:%d", published)} {
		if !strings.Contains(string(config), "proxy_pass "+upstream+";") {
			t.Errorf("Nginx 配置缺少上游 %s", upstream)
		}
	}
}
