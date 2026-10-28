package panel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
			err := a.applyUpgrade(context.Background(), "test", next)
			actual, _ := os.Readlink(filepath.Join(a.Root, "current"))
			var status string
			_ = a.DB.QueryRow("SELECT status FROM operations WHERE id='test'").Scan(&status)
			if fail {
				if err == nil || actual != old || calls != 2 || status != "failed" {
					t.Fatalf("回退失败 %v %s %d %s", err, actual, calls, status)
				}
			} else if err != nil || actual != next || status != "success" {
				t.Fatalf("升级失败 %v %s %s", err, actual, status)
			}
		})
	}
}

func TestUpgradeEndpointsRequireLogin(t *testing.T) {
	a := upgradeTestApp(t)
	for _, endpoint := range []struct{ method, path string }{{"GET", "/api/v1/system/updates"}, {"POST", "/api/v1/system/updates/check"}, {"PUT", "/api/v1/system/updates/repository"}, {"POST", "/api/v1/system/updates/upgrade"}} {
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
