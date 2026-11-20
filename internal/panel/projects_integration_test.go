package panel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectDockerIntegration(t *testing.T) {
	if os.Getenv("LPANEL_DOCKER_INTEGRATION") != "1" {
		t.Skip("设置 LPANEL_DOCKER_INTEGRATION=1 以验证隔离 Docker 项目")
	}
	a, root := testApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p := Project{ID: fmt.Sprintf("integration%d", time.Now().UnixNano()), Name: "隔离验证", Compose: `version: "3.8"
services:
  web:
    image: nginx:alpine
    ports:
      - target: 80
        host_ip: 127.0.0.1
    volumes:
      - retained:/var/cache/nginx
      - ./data:/srv/lpanel-test
volumes:
  retained:
`}
	dir := filepath.Join(root, "data/projects", p.ID)
	ports, err := a.scanProject(ctx, p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 1 || ports[0].Published != "" {
		t.Fatalf("动态端口解析错误：%+v", ports)
	}
	localData := filepath.Join(dir, "data")
	if err := os.Mkdir(localData, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localData, "data.txt"), []byte("相对路径挂载数据"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = a.DB.Exec("INSERT INTO projects VALUES(?,?,?,?,?)", p.ID, p.Name, p.Compose, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = a.projectCommand(context.Background(), p, "down", "--volumes")
		_, _ = a.Command(context.Background(), "docker", "volume", "rm", "lpanel-"+p.ID+"_retained")
	}()
	if _, err = a.runProjectAction(ctx, p, "deploy", false); err != nil {
		t.Fatal(err)
	}
	items, err := a.ProjectStatus(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := projectUpstream(items, "web", 80, "http")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(upstream)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("应用状态 %d", response.StatusCode)
	}
	if _, err = a.runProjectAction(ctx, p, "stop", false); err != nil {
		t.Fatal(err)
	}
	items, err = a.ProjectStatus(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = projectUpstream(items, "web", 80, "http"); err == nil {
		t.Fatal("停止后依然被视为运行")
	}
	if _, err = a.runProjectAction(ctx, p, "start", false); err != nil {
		t.Fatal(err)
	}
	if _, err = a.runProjectAction(ctx, p, "delete", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("删除后项目目录仍存在：%s，错误：%v", dir, err)
	}
	if _, err = a.Command(ctx, "docker", "volume", "inspect", "lpanel-"+p.ID+"_retained"); err != nil {
		t.Fatal("删除项目后数据卷不存在：", err)
	}
	t.Log("真实 Docker 部署、HTTP动态端口访问、停止/启动、删除保留卷通过")
}
