package panel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestReleaseVersionOrdering(t *testing.T) {
	for _, test := range []struct {
		latest, current string
		want            bool
	}{{"v1.10.0", "v1.9.9", true}, {"v2.0.0", "v1.99.0", true}, {"v1.0.0", "v1.0.0", false}, {"v0.9.0", "v1.0.0", false}, {"v1.0.0+build", "v1.0.0", false}} {
		got, err := newerRelease(test.latest, test.current)
		if err != nil || got != test.want {
			t.Fatalf("%s/%s：%v %v", test.latest, test.current, got, err)
		}
	}
	for _, version := range []string{"v1.0.0-rc.1", "v01.2.3", "latest"} {
		if _, err := newerRelease(version, "v1.0.0"); err == nil {
			t.Fatal("接受无效正式版本", version)
		}
	}
}
func upgradeTestApp(t *testing.T) *App {
	a, _ := testApp(t)
	a.Version = "v1.0.0"
	if _, err := a.DB.Exec("UPDATE upgrade_settings SET repository='owner/panel'"); err != nil {
		t.Fatal(err)
	}
	return a
}
func TestUpgradeChecksReleaseAndArchitecture(t *testing.T) {
	a := upgradeTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/panel/releases/latest" {
			t.Errorf("请求地址：%s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"tag_name":"v1.10.0","assets":[{"name":"lpanel_v1.10.0_linux_%s.tar.gz","browser_download_url":"https://example.test/package"},{"name":"lpanel_v1.10.0_linux_%s.tar.gz.sha256","browser_download_url":"https://example.test/checksum"}]}`, runtime.GOARCH, runtime.GOARCH)
	}))
	defer server.Close()
	state, err := a.checkUpgrade(context.Background(), server.URL, server.Client())
	if err != nil || !state.Available || state.AssetURL == "" {
		t.Fatalf("更新检查：%+v %v", state, err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"tag_name":"v1.11.0","assets":[]}`) }))
	defer bad.Close()
	state, err = a.checkUpgrade(context.Background(), bad.URL, bad.Client())
	if err == nil || state.Available {
		t.Fatal("缺少附件仍允许升级")
	}
	persisted, _ := a.UpgradeStatus()
	if persisted.Error == "" {
		t.Fatal("失败未保存")
	}
	if persisted.Available {
		t.Fatal("缺少架构附件的状态被重新标记为可升级")
	}
}
func TestUpgradeDownloadAndChecksum(t *testing.T) {
	a := upgradeTestApp(t)
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{"lpanel": "程序", "web/index.html": "页面"} {
		data := []byte(content)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = gz.Close()
	sum := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksum" {
			fmt.Fprint(w, sum)
			return
		}
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()
	state := UpgradeState{Latest: "v1.1.0", ChecksumURL: server.URL + "/checksum", AssetURL: server.URL + "/package"}
	dir, err := a.prepareUpgrade(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "web", "index.html")); err != nil {
		t.Fatal(err)
	}
	sum = strings.Repeat("0", 64)
	state.Latest = "v1.2.0"
	if _, err = a.prepareUpgrade(context.Background(), state); err == nil {
		t.Fatal("损坏发行包未拒绝")
	}
}
func TestUpgradeWorkerSuccessAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			a := upgradeTestApp(t)
			a.Config.NginxConfigDir = filepath.Join(a.Root, "nginx")
			if err := os.MkdirAll(a.Config.NginxConfigDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(a.Config.NginxConfigDir, "lpanel.conf"), []byte(managedNginxHeader+"server {}\n"), 0644); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(a.Root, "releases", "v1.0.0")
			next := filepath.Join(a.Root, "releases", "v1.1.0")
			for _, dir := range []string{old, next} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "lpanel"), []byte("程序"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(old, filepath.Join(a.Root, "current")); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok","version":"v1.1.0"}`) }))
			defer server.Close()
			a.Config.Listen = strings.TrimPrefix(server.URL, "http://")
			calls := 0
			a.Command = func(context.Context, string, ...string) ([]byte, error) {
				calls++
				if fail && calls == 1 {
					return nil, fmt.Errorf("新版无法启动")
				}
				return nil, nil
			}
			_, _ = a.DB.Exec("INSERT INTO operations(id,kind,status,created_at,updated_at) VALUES('test','upgrade','running','','')")
			err := a.applyUpgrade(t.Context(), "test", next)
			actual, _ := os.Readlink(filepath.Join(a.Root, "current"))
			var status string
			_ = a.DB.QueryRow("SELECT status FROM operations WHERE id='test'").Scan(&status)
			if fail {
				// 两次重启之间只停止候选服务，相同的配置不需要重载。
				if err == nil || actual != old || calls != 3 || status != "failed" {
					t.Fatalf("回退失败 %v %s %d %s", err, actual, calls, status)
				}
			} else if err != nil || actual != next || status != "success" {
				t.Fatalf("升级失败 %v %s %s", err, actual, status)
			}
		})
	}
}

func TestUpgradeWorkerRestoresProxyWhenCandidateIsUnhealthy(t *testing.T) {
	a := domainApp(t)
	old := filepath.Join(a.Root, "releases", "v1.0.0")
	next := filepath.Join(a.Root, "releases", "v1.1.0")
	if err := os.MkdirAll(next, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "lpanel"), []byte("程序"), 0755); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(a.Root, "current")
	if err := os.Symlink(old, current); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.Config.NginxConfigDir, "lpanel.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	const previous = managedNginxHeader + "server {}\n"
	if err := os.WriteFile(path, []byte(previous), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec("INSERT INTO operations(id,kind,status,created_at,updated_at) VALUES('test','upgrade','running','','')"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		cancel()
	}))
	defer server.Close()
	a.Config.Listen = strings.TrimPrefix(server.URL, "http://")
	var calls []string
	a.Command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if len(calls) == 1 {
			return nil, os.WriteFile(path, []byte(managedNginxHeader+nginxTemplateRevision+"server {}\n"), 0644)
		}
		if ctx.Err() != nil {
			t.Fatalf("恢复命令应使用独立的上下文：%v", ctx.Err())
		}
		if target, err := os.Readlink(current); err != nil || target != old {
			t.Fatalf("运行恢复命令前应切换到旧版本：%q，%v", target, err)
		}
		if name == "nginx" || args[0] != "stop" {
			if data, err := os.ReadFile(path); err != nil || string(data) != previous {
				t.Fatalf("检查、重载和启动旧版本前应恢复代理配置：%q，%v", data, err)
			}
		}
		return nil, nil
	}
	if err := a.applyUpgrade(ctx, "test", next); !errors.Is(err, context.Canceled) {
		t.Fatalf("应报告候选版本未通过健康检查的原因：%v", err)
	}
	want := []string{"systemctl restart lpanel.service", "systemctl stop lpanel.service", "nginx -t", "systemctl reload nginx", "systemctl restart lpanel.service"}
	if !slices.Equal(calls, want) {
		t.Fatalf("恢复步骤错误：%q", calls)
	}
	var status, message string
	if err := a.DB.QueryRow("SELECT status,error FROM operations WHERE id='test'").Scan(&status, &message); err != nil || status != "failed" || !strings.Contains(message, "已恢复上一版程序") {
		t.Fatalf("应记录升级失败和恢复结果：%q，%q，%v", status, message, err)
	}
}

func TestUpgradeRollbackPreservesUnmanagedProxy(t *testing.T) {
	a := domainApp(t)
	path := filepath.Join(t.TempDir(), "lpanel.conf")
	previous := []byte(managedNginxHeader + "server {}\n")
	a.Command = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		t.Fatalf("未托管配置不应触发命令：%s", name)
		return nil, nil
	}
	if err := a.restoreUpgradeNginx(t.Context(), path, previous); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("回退不应创建缺失的配置：%v", err)
	}
	const custom = "# 管理员自定义配置\n"
	if err := os.WriteFile(path, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	if err := a.restoreUpgradeNginx(t.Context(), path, previous); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != custom {
		t.Fatalf("回退应保留自定义配置：%q，%v", data, err)
	}
}

func TestUpgradeRollbackChecksLoadedProxy(t *testing.T) {
	a := domainApp(t)
	config := a.currentConfig()
	const marker = "0123456789abcdef0123456789abcdef"
	previous := a.renderNginx([]Binding{{Kind: "panel", Domain: "_"}}, marker, config, "", "$remote_addr", nginxHTTP2Shared)
	path := filepath.Join(t.TempDir(), "lpanel.conf")
	if err := os.WriteFile(path, []byte(managedNginxHeader+"server {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("重载后仍在运行候选配置")
	wantAddress, wantHost := localPanelEndpoint(config, "/.lpanel/config")
	a.NginxProbe = func(_ context.Context, address, host, gotMarker string) error {
		if address != wantAddress || host != wantHost || gotMarker != marker {
			t.Fatalf("恢复后应回读旧版入口和配置标识：%q %q %q", address, host, gotMarker)
		}
		return failure
	}
	if err := a.restoreUpgradeNginx(t.Context(), path, []byte(previous)); !errors.Is(err, failure) {
		t.Fatalf("应报告旧配置未生效：%v", err)
	}
}

func TestUpgradeEndpointsRequireLogin(t *testing.T) {
	a := upgradeTestApp(t)
	for _, endpoint := range []struct{ method, path string }{{"GET", "/api/v1/system/updates"}, {"POST", "/api/v1/system/updates/check"}, {"PUT", "/api/v1/system/updates/repository"}, {"PUT", "/api/v1/system/updates/auto-update"}, {"POST", "/api/v1/system/updates/upgrade"}} {
		if result := call(a, endpoint.method, endpoint.path, `{}`, nil); result.Code != 401 {
			t.Fatalf("未登录访问 %s 返回 %d", endpoint.path, result.Code)
		}
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	if result := call(a, "PUT", "/api/v1/system/updates/repository", `{"repository":"invalid"}`, cookie); result.Code != 400 {
		t.Fatalf("无效仓库返回 %d", result.Code)
	}
	if result := call(a, "PUT", "/api/v1/system/updates/repository", `{"repository":"new-owner/new-panel"}`, cookie); result.Code != 200 {
		t.Fatalf("保存仓库返回 %s", result.Body.String())
	}
	state, err := a.UpgradeStatus()
	if err != nil || state.Repository != "new-owner/new-panel" || state.Available {
		t.Fatalf("仓库状态未重置 %+v %v", state, err)
	}
}

func TestAutoUpdateSettingsPersistAfterMigration(t *testing.T) {
	a := upgradeTestApp(t)
	if _, err := a.DB.Exec("ALTER TABLE upgrade_settings DROP COLUMN auto_update"); err != nil {
		t.Fatal(err)
	}
	reopen := func() {
		t.Helper()
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		a, err = Open(a.Root, "v1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		reopened := a
		t.Cleanup(func() { _ = reopened.Close() })
	}
	reopen()
	state, err := a.UpgradeStatus()
	if err != nil || state.AutoUpdate || state.Repository != "owner/panel" {
		t.Fatalf("迁移后的设置：%+v，%v", state, err)
	}
	if err := a.saveUpgradeState(UpgradeState{Latest: "v1.1.0", Available: true}); err != nil {
		t.Fatal(err)
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	cookie := login.Result().Cookies()[0]
	path := "/api/v1/system/updates/auto-update"
	if result := call(a, "PUT", path, `{}`, cookie); result.Code != 400 {
		t.Fatalf("缺少设置值时返回 %d", result.Code)
	}
	assertState := func(result *httptest.ResponseRecorder, enabled bool) {
		t.Helper()
		var state UpgradeState
		err := json.Unmarshal(result.Body.Bytes(), &state)
		if result.Code != 200 || err != nil || state.AutoUpdate != enabled || state.Repository != "owner/panel" || !state.Available {
			t.Fatalf("更新状态：%d %s，%v", result.Code, result.Body.String(), err)
		}
	}
	assertState(call(a, "PUT", path, `{"auto_update":true}`, cookie), true)
	reopen()
	assertState(call(a, "GET", "/api/v1/system/updates", "", cookie), true)
	assertState(call(a, "PUT", path, `{"auto_update":false}`, cookie), false)
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM operations WHERE kind='upgrade'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("保存设置后升级任务数为 %d，%v", count, err)
	}
}

func TestUpgradeRejectsManualInstallWhileRunning(t *testing.T) {
	a := upgradeTestApp(t)
	if err := a.saveUpgradeState(UpgradeState{Latest: "v1.1.0", Available: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec("INSERT INTO operations(id,kind,status,created_at,updated_at) VALUES('running','upgrade','running','','')"); err != nil {
		t.Fatal(err)
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	result := call(a, "POST", "/api/v1/system/updates/upgrade", "", login.Result().Cookies()[0])
	if result.Code != 409 || !strings.Contains(result.Body.String(), "upgrade_running") {
		t.Fatalf("升级进行中时手动安装返回 %d %s", result.Code, result.Body.String())
	}
}

func TestAutoUpdateUsesOnlyEligibleBackgroundChecks(t *testing.T) {
	a := upgradeTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/valid/repos/owner/panel/releases/latest":
			name := "lpanel_v1.1.0_linux_" + runtime.GOARCH + ".tar.gz"
			fmt.Fprintf(w, `{"tag_name":"v1.1.0","assets":[{"name":%q,"browser_download_url":"http://%s/package"},{"name":%q,"browser_download_url":"http://%s/checksum"}]}`, name, r.Host, name+".sha256", r.Host)
		case "/missing/repos/owner/panel/releases/latest":
			fmt.Fprint(w, `{"tag_name":"v1.1.0","assets":[]}`)
		case "/current/repos/owner/panel/releases/latest":
			fmt.Fprint(w, `{"tag_name":"v1.0.0","assets":[]}`)
		default:
			w.WriteHeader(502)
		}
	}))
	defer server.Close()

	a.checkAndAutoUpgrade(t.Context(), server.URL+"/valid", server.Client())
	state, err := a.UpgradeStatus()
	if err != nil || !state.Available || state.AutoUpdate {
		t.Fatalf("关闭自动更新时应继续检查：%+v，%v", state, err)
	}
	if _, err := a.DB.Exec("UPDATE upgrade_settings SET auto_update=1"); err != nil {
		t.Fatal(err)
	}
	if state, err := a.checkUpgrade(t.Context(), server.URL+"/valid", server.Client()); err != nil || !state.Available {
		t.Fatalf("手动检查：%+v，%v", state, err)
	}
	for _, path := range []string{"/failed", "/missing", "/current"} {
		a.checkAndAutoUpgrade(t.Context(), server.URL+path, server.Client())
	}
	var count int
	if err := a.DB.QueryRow("SELECT count(*) FROM operations WHERE kind='upgrade'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("不符合条件时升级任务数为 %d，%v", count, err)
	}

	a.checkAndAutoUpgrade(t.Context(), server.URL+"/valid", server.Client())
	deadline := time.Now().Add(3 * time.Second)
	for {
		var status, message string
		if err := a.DB.QueryRow("SELECT status,error FROM operations WHERE kind='upgrade'").Scan(&status, &message); err != nil {
			t.Fatalf("缺少自动创建的升级任务：%v", err)
		}
		if status == "failed" && message != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("升级下载应记录 HTTP 失败：%s %s", status, message)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
