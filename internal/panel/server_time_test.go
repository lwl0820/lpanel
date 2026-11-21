package panel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestServerTimeSettings(t *testing.T) {
	a, _ := testApp(t)
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	zone, ntp := "UTC", "yes"
	a.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name + " " + strings.Join(args, " ") {
		case "timedatectl show":
			return []byte("Timezone=" + zone + "\nCanNTP=yes\nNTP=" + ntp + "\nNTPSynchronized=no\n"), nil
		case "timedatectl list-timezones":
			return []byte("Asia/Kathmandu\nUTC\n"), nil
		case "timedatectl set-timezone Asia/Kathmandu":
			zone = "Asia/Kathmandu"
			return nil, nil
		case "timedatectl set-ntp false":
			ntp = "no"
			return nil, nil
		case "systemctl show -p LoadState -p ActiveState -p UnitFileState -p Conflicts -p RuntimeDirectory -p RuntimeDirectoryPreserve " + timeSyncUnit:
			return []byte("LoadState=not-found\n"), nil
		default:
			t.Fatalf("意外命令：%s %v", name, args)
			return nil, nil
		}
	}
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/v1/server/time/timezone", `{"timezone":"Asia/Kathmandu"}`},
		{"PUT", "/api/v1/server/time/auto", `{"enabled":false}`},
		{"POST", "/api/v1/server/time/sync", ""},
	} {
		if w := call(a, tc.method, tc.path, tc.body, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("未登录的 %s 返回 %d", tc.path, w.Code)
		}
	}
	if w := call(a, "PUT", "/api/v1/server/time/timezone", `{"timezone":"--help"}`, cookie); w.Code != http.StatusBadRequest {
		t.Fatal(w.Body.String())
	}
	for _, tc := range []struct {
		path, body string
		auto       bool
	}{
		{"timezone", `{"timezone":"Asia/Kathmandu"}`, true},
		{"auto", `{"enabled":false}`, false},
	} {
		w := call(a, "PUT", "/api/v1/server/time/"+tc.path, tc.body, cookie)
		var status serverTimeStatus
		if err := json.Unmarshal(w.Body.Bytes(), &status); w.Code != http.StatusOK || err != nil {
			t.Fatalf("返回 %d：%s", w.Code, w.Body.String())
		}
		_, offset := status.ServerTime.Zone()
		if status.Timezone != "Asia/Kathmandu" || offset != 5*3600+45*60 ||
			status.AutoSync == nil || *status.AutoSync != tc.auto || status.Synchronized == nil ||
			*status.Synchronized || !status.CanSetAuto || status.CanSyncNow {
			t.Fatalf("返回的状态不正确：%s", w.Body.String())
		}
	}
}

func TestServerTimeSyncEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, firstCommand                                    string
		active, marker, kernelSync, skipStart, stale, success bool
	}{
		{name: "临时启动成功", firstCommand: "start", marker: true, kernelSync: true, success: true},
		{name: "已有同步但本次无标记", firstCommand: "start", kernelSync: true},
		{name: "有标记但系统未确认", firstCommand: "start", marker: true},
		{name: "服务条件未满足", firstCommand: "start", skipStart: true},
		{name: "服务未启动但有旧标记", stale: true, kernelSync: true},
		{name: "重启服务并保持运行", firstCommand: "restart", active: true, marker: true, stale: true, kernelSync: true, success: true},
		{name: "重启后旧标记未更新", firstCommand: "restart", active: true, stale: true, kernelSync: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "synchronized")
			if tc.stale {
				if err := os.WriteFile(marker, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			a := App{serverTimeSyncMarker: marker}
			var lifecycle []string
			a.Command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				switch name + " " + strings.Join(args, " ") {
				case "systemctl start " + timeSyncUnit, "systemctl restart " + timeSyncUnit:
					lifecycle = append(lifecycle, args[0])
					if tc.marker {
						if err := os.WriteFile(marker, nil, 0600); err != nil {
							t.Fatal(err)
						}
						// 模拟本轮校时后系统时钟向过去跳变。
						past := time.Unix(100, 0)
						return nil, os.Chtimes(marker, past, past)
					}
				case "systemctl show --property=ActiveState --value " + timeSyncUnit:
					if tc.skipStart {
						return []byte("inactive\n"), nil
					}
					return []byte("active\n"), nil
				case "timedatectl show --property=NTPSynchronized --value":
					if tc.kernelSync {
						return []byte("yes\n"), nil
					}
					return []byte("no\n"), nil
				case "systemctl stop " + timeSyncUnit:
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					lifecycle = append(lifecycle, "stop")
				default:
					return nil, errors.New("意外命令")
				}
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			_, err := a.runServerTimeSync(ctx, tc.active)
			if (err == nil) != tc.success {
				t.Fatalf("同步结果不正确：%v", err)
			}
			var want []string
			if tc.firstCommand != "" {
				want = []string{tc.firstCommand}
				if !tc.active {
					want = append(want, "stop")
				}
			}
			if !slices.Equal(lifecycle, want) {
				t.Fatalf("服务操作是 %v，预期 %v", lifecycle, want)
			}
		})
	}
}

func TestServerTimeSyncRequest(t *testing.T) {
	a, _ := testApp(t)
	a.serverTimeSyncMarker = filepath.Join(t.TempDir(), "synchronized")
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	conflict := true
	a.Command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name + " " + strings.Join(args, " ") {
		case "timedatectl show --property=NTP --value":
			return []byte("no\n"), nil
		case "timedatectl show":
			return []byte("Timezone=UTC\nCanNTP=yes\nNTP=yes\nNTPSynchronized=no\n"), nil
		case "systemctl show -p LoadState -p ActiveState -p UnitFileState -p Conflicts -p RuntimeDirectory -p RuntimeDirectoryPreserve " + timeSyncUnit:
			state := "inactive"
			select {
			case <-started:
				state = "active"
			default:
			}
			return []byte("LoadState=loaded\nActiveState=" + state + "\nUnitFileState=disabled\nConflicts=shutdown.target\nRuntimeDirectory=systemd/timesync\nRuntimeDirectoryPreserve=no\n"), nil
		case "systemctl list-units --state=active,activating,reloading,deactivating --no-legend --plain -- shutdown.target chrony.service chronyd.service ntp.service ntpd.service ntpsec.service openntpd.service":
			if conflict {
				return []byte("chrony.service loaded active running\n"), nil
			}
			return nil, nil
		case "systemctl start " + timeSyncUnit:
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil, os.WriteFile(a.serverTimeSyncMarker, nil, 0600)
		case "systemctl show --property=ActiveState --value " + timeSyncUnit:
			return []byte("active\n"), nil
		case "timedatectl show --property=NTPSynchronized --value":
			return []byte("yes\n"), nil
		case "systemctl stop " + timeSyncUnit:
			return nil, errors.New("服务仍在运行")
		default:
			return nil, errors.New("意外命令")
		}
	}
	const path = "/api/v1/server/time/sync"
	if w := call(a, "POST", path, "", cookie); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "time_sync_unavailable") {
		t.Fatalf("服务冲突返回 %d：%s", w.Code, w.Body.String())
	}
	conflict = false
	w := call(a, "POST", path, "", cookie)
	var op Operation
	if err := json.Unmarshal(w.Body.Bytes(), &op); w.Code != http.StatusAccepted || err != nil || op.Status != "running" || op.ID == "" {
		t.Fatalf("提交操作返回 %d：%s", w.Code, w.Body.String())
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("后台操作未尝试启动校时服务")
	}
	w = call(a, "GET", "/api/v1/server/time", "", cookie)
	var status serverTimeStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); w.Code != http.StatusOK || err != nil ||
		status.ServerTime.IsZero() || status.AutoSync == nil || *status.AutoSync ||
		status.Synchronized == nil || *status.Synchronized || status.CanSyncNow {
		t.Fatalf("临时运行期间的状态返回 %d：%s", w.Code, w.Body.String())
	}
	if w := call(a, "POST", path, "", cookie); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "server_time_busy") {
		t.Fatalf("重复提交返回 %d：%s", w.Code, w.Body.String())
	}
	// 等待失败记录落库，避免测试关闭数据库时后台操作尚未完成。
	release <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for range time.Tick(10 * time.Millisecond) {
		w := call(a, "GET", "/api/v1/operations/"+op.ID, "", cookie)
		if err := json.Unmarshal(w.Body.Bytes(), &op); w.Code != http.StatusOK || err != nil {
			t.Fatalf("读取操作返回 %d：%s", w.Code, w.Body.String())
		}
		if op.Status == "failed" && strings.Contains(op.Error, "无法停止临时启动的校时服务") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("未记录停止临时校时服务失败：%s", w.Body.String())
		}
	}
	w = call(a, "GET", "/api/v1/server/time", "", cookie)
	status = serverTimeStatus{}
	if err := json.Unmarshal(w.Body.Bytes(), &status); w.Code != http.StatusOK || err != nil ||
		status.AutoSync == nil || *status.AutoSync || !status.CanSyncNow {
		t.Fatalf("停止失败且服务仍在运行时的状态返回 %d：%s", w.Code, w.Body.String())
	}
}
