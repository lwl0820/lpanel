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
	p.Directory = dir
	if err := a.writeProjectFiles(p, dir); err != nil {
		t.Fatal(err)
	}
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
	_, err = a.DB.Exec(`
		DROP TABLE projects;
		CREATE TABLE projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,compose TEXT NOT NULL,environment TEXT NOT NULL,updated_at TEXT NOT NULL)
	`)
	if err != nil {
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
	if _, err = a.projectCommand(ctx, p, "up", "-d", "--remove-orphans"); err != nil {
		t.Fatal(err)
	}
	items, err := a.ProjectStatus(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	before := items
	if len(before) != 1 || before[0].ID == "" {
		t.Fatalf("升级前容器信息有误：%+v", before)
	}
	mountsBefore, err := a.Command(ctx, "docker", "inspect", "--format", "{{json .Mounts}}", before[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.migrateProjects(); err != nil {
		t.Fatal(err)
	}
	p, err = a.project(ctx, p.ID)
	if err != nil || p.Directory != dir || !p.LegacyDirectory {
		t.Fatalf("旧项目路径迁移失败：%+v %v", p, err)
	}
	items, err = a.ProjectStatus(ctx, p)
	if err != nil || len(items) != 1 || items[0].ID != before[0].ID || items[0].State != before[0].State {
		t.Fatalf("升级改变了容器状态：%+v %v", items, err)
	}
	mountsAfter, err := a.Command(ctx, "docker", "inspect", "--format", "{{json .Mounts}}", items[0].ID)
	if err != nil || string(mountsBefore) != string(mountsAfter) {
		t.Fatalf("升级改变了容器挂载：%s -> %s (%v)", mountsBefore, mountsAfter, err)
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
	if _, err = a.runProjectAction(ctx, p.ID, "stop", false); err != nil {
		t.Fatal(err)
	}
	items, err = a.ProjectStatus(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = projectUpstream(items, "web", 80, "http"); err == nil {
		t.Fatal("停止后依然被视为运行")
	}
	if _, err = a.runProjectAction(ctx, p.ID, "start", false); err != nil {
		t.Fatal(err)
	}
	if _, err = a.runProjectAction(ctx, p.ID, "delete", true); err != nil {
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
