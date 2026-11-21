package panel

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerMonitorHistoryMigratesLoadAverage(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "monitor.db") + "?_pragma=busy_timeout(3000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE server_monitor_samples (
		at INTEGER PRIMARY KEY,
		cpu_percent REAL,
		memory_used_bytes INTEGER NOT NULL,
		memory_total_bytes INTEGER NOT NULL,
		disks TEXT NOT NULL
	);
	INSERT INTO server_monitor_samples VALUES (1, 12.5, 100, 200, '[]')`)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	start := make(chan struct{})
	migrations := make(chan error, 2)
	for _, conn := range []*sql.DB{db, other} {
		go func() {
			<-start
			app := &App{DB: conn, Mux: http.NewServeMux()}
			migrations <- app.registerMonitorHistory()
		}()
	}
	close(start)
	for range 2 {
		if err := <-migrations; err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		return
	}
	a := &App{DB: db}
	load := [3]float64{0, 1.25, 2.5}
	if err := a.storeMonitorSample(t.Context(), monitorSample{At: 2, LoadAverage: &load}); err != nil {
		t.Fatal(err)
	}
	samples, err := a.loadMonitorHistory(t.Context(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || samples[0].LoadAverage != nil || samples[0].MemoryUsedBytes != 100 ||
		samples[1].LoadAverage == nil || *samples[1].LoadAverage != load {
		t.Fatalf("升级后历史读数错误：%+v", samples)
	}
}

func TestServerMonitorHistoryRetentionAndReplacement(t *testing.T) {
	a, _ := testApp(t)
	ctx := t.Context()
	now := time.Now()
	cutoff := now.Add(-monitorHistoryRetention).Unix()
	root := monitorDisk{Mount: "/", TotalBytes: 500, UsedBytes: 250}
	data := monitorDisk{Mount: "/data", TotalBytes: 200, UsedBytes: 150}
	for _, sample := range []monitorSample{
		{At: now.Unix(), CPUPercent: new(10.0), LoadAverage: new([3]float64{2, 3, 4}), MemoryTotalBytes: 1000, Disks: []monitorDisk{root, data}},
		{At: cutoff, MemoryTotalBytes: 1000},
		{At: cutoff - 1, MemoryTotalBytes: 1000, Disks: []monitorDisk{data}},
		{At: now.Unix(), CPUPercent: new(20.0), LoadAverage: new([3]float64{0.25, 1, 2}), MemoryUsedBytes: 300, MemoryTotalBytes: 1000, Disks: []monitorDisk{root}},
	} {
		if err := a.storeMonitorSample(ctx, sample); err != nil {
			t.Fatal(err)
		}
	}
	a.pruneMonitorHistory(ctx, now)
	samples, err := a.loadMonitorHistory(ctx, cutoff-1, now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 || samples[0].At != cutoff || samples[1].At != now.Unix() {
		t.Fatalf("历史保留边界或时间顺序错误：%+v", samples)
	}
	if samples[0].CPUPercent != nil || samples[0].Disks == nil || len(samples[0].Disks) != 0 {
		t.Fatalf("缺失的 CPU 和磁盘读数返回错误：%+v", samples[0])
	}
	last := samples[1]
	if last.CPUPercent == nil || *last.CPUPercent != 20 || last.LoadAverage == nil || *last.LoadAverage != [3]float64{0.25, 1, 2} ||
		last.MemoryUsedBytes != 300 ||
		len(last.Disks) != 1 || last.Disks[0] != root {
		t.Fatalf("同秒替换后整机和磁盘读数不一致：%+v", last)
	}
}

func TestServerMonitorHistoryRequest(t *testing.T) {
	a, _ := testApp(t)
	const path = "/api/v1/server/monitor/history"
	if w := call(a, "GET", path, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("匿名请求返回 %d", w.Code)
	}
	login := call(a, "POST", "/api/v1/auth/login", `{"username":"管理员","password":"test-password"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("登录失败：%s", login.Body)
	}
	cookie := login.Result().Cookies()[0]
	if w := call(a, "GET", path+"?from=2&to=1", "", cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("倒序的时间范围返回 %d", w.Code)
	}
	now := time.Now()
	if w := call(a, "GET", fmt.Sprintf("%s?from=%d&to=%d", path, now.Unix()+60, now.Unix()+120), "", cookie); w.Code != http.StatusOK ||
		strings.TrimSpace(w.Body.String()) != `{"samples":[]}` {
		t.Fatalf("空时间范围的响应错误（%d）：%s", w.Code, w.Body)
	}
	for _, at := range []int64{now.Add(-monitorHistoryRetention).Unix() - 60, now.Unix()} {
		sample := monitorSample{At: at, MemoryUsedBytes: 1, MemoryTotalBytes: 2, Disks: []monitorDisk{{Mount: "/", TotalBytes: 10, UsedBytes: 3}}}
		if err := a.storeMonitorSample(t.Context(), sample); err != nil {
			t.Fatal(err)
		}
	}
	w := call(a, "GET", fmt.Sprintf("%s?from=%d&to=%d", path, now.Add(-monitorHistoryRetention-time.Minute).Unix(), now.Unix()+60), "", cookie)
	var result struct {
		Samples []map[string]any `json:"samples"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(result.Samples) != 1 {
		t.Fatalf("跨保留边界的查询响应错误（%d）：%s", w.Code, w.Body)
	}
	sample := result.Samples[0]
	if cpu, ok := sample["cpu_percent"]; !ok || cpu != nil ||
		sample["at"] != float64(now.Unix()) || sample["memory_total_bytes"] != float64(2) ||
		sample["memory_used_bytes"] != float64(1) {
		t.Fatalf("整机历史字段错误：%+v", sample)
	}
	if load, ok := sample["load_average"]; !ok || load != nil {
		t.Fatalf("未知负载的历史字段错误：%+v", sample)
	}
	disks, ok := sample["disks"].([]any)
	if !ok || len(disks) != 1 {
		t.Fatalf("磁盘历史字段错误：%+v", sample)
	}
	disk, ok := disks[0].(map[string]any)
	if !ok || disk["mount"] != "/" || disk["total_bytes"] != float64(10) || disk["used_bytes"] != float64(3) {
		t.Fatalf("磁盘历史读数错误：%+v", disks)
	}
}
