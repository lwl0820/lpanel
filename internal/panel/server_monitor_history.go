package panel

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

const monitorHistoryRetention = 30 * 24 * time.Hour

func (a *App) registerMonitorHistory() error {
	ctx := context.Background()
	conn, err := a.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("连接服务器监控历史库失败：%w", err)
	}
	defer conn.Close()
	// 在检查列之前取得写锁，避免并发启动时两个事务同时尝试升级旧表。
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("启动服务器监控历史表迁移失败：%w", err)
	}
	defer conn.ExecContext(ctx, "ROLLBACK")

	_, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS server_monitor_samples (
		at INTEGER PRIMARY KEY,
		cpu_percent REAL,
		memory_used_bytes INTEGER NOT NULL,
		memory_total_bytes INTEGER NOT NULL,
		disks TEXT NOT NULL,
		load_average TEXT
	)`)
	if err != nil {
		return fmt.Errorf("初始化服务器监控历史表失败：%w", err)
	}
	var hasLoadAverage bool
	if err := conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_table_info('server_monitor_samples') WHERE name='load_average')").Scan(&hasLoadAverage); err != nil {
		return fmt.Errorf("检查服务器监控历史表失败：%w", err)
	}
	if !hasLoadAverage {
		if _, err := conn.ExecContext(ctx, "ALTER TABLE server_monitor_samples ADD COLUMN load_average TEXT"); err != nil {
			return fmt.Errorf("升级服务器监控历史表失败：%w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("提交服务器监控历史表迁移失败：%w", err)
	}
	a.Mux.Handle("GET /api/v1/server/monitor/history", a.RequireAuth(http.HandlerFunc(a.monitorHistory)))
	return nil
}

func (a *App) runMonitorHistory(ctx context.Context) {
	var baseline monitorCPUCounter
	if _, err := readMonitorSample(&baseline); err != nil && ctx.Err() == nil {
		log.Printf("读取服务器监控基线失败：%v", err)
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	a.pruneMonitorHistory(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample, err := readMonitorSample(&baseline)
			if err == nil {
				err = a.storeMonitorSample(ctx, sample)
			}
			if err != nil && ctx.Err() == nil {
				log.Printf("记录服务器监控样本失败：%v", err)
			}
			a.pruneMonitorHistory(ctx, time.Now())
		}
	}
}

func (a *App) storeMonitorSample(ctx context.Context, sample monitorSample) error {
	// 同一秒的整机读数和磁盘列表一起替换。
	disks, err := json.Marshal(sample.Disks)
	if err != nil {
		return err
	}
	var loadAverage *string
	if sample.LoadAverage != nil {
		data, err := json.Marshal(sample.LoadAverage)
		if err != nil {
			return err
		}
		loadAverage = new(string(data))
	}
	_, err = a.DB.ExecContext(ctx, `
		INSERT INTO server_monitor_samples (at, cpu_percent, memory_used_bytes, memory_total_bytes, disks, load_average)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(at) DO UPDATE SET cpu_percent=excluded.cpu_percent,
			memory_used_bytes=excluded.memory_used_bytes, memory_total_bytes=excluded.memory_total_bytes,
			disks=excluded.disks, load_average=excluded.load_average`,
		sample.At, sample.CPUPercent, sample.MemoryUsedBytes, sample.MemoryTotalBytes, string(disks), loadAverage)
	return err
}

func (a *App) pruneMonitorHistory(ctx context.Context, now time.Time) {
	_, err := a.DB.ExecContext(ctx, "DELETE FROM server_monitor_samples WHERE at < ?", now.Add(-monitorHistoryRetention).Unix())
	if err != nil && ctx.Err() == nil {
		log.Printf("清理服务器监控历史失败：%v", err)
	}
}

func (a *App) monitorHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, fromErr := strconv.ParseInt(query.Get("from"), 10, 64)
	to, toErr := strconv.ParseInt(query.Get("to"), 10, 64)
	if fromErr != nil || toErr != nil || from > to {
		WriteError(w, http.StatusBadRequest, "invalid_monitor_range", "请选择有效的查询时间范围")
		return
	}
	now := time.Now()
	samples, err := a.loadMonitorHistory(r.Context(), max(from, now.Add(-monitorHistoryRetention).Unix()), min(to, now.Unix()))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "monitor_history_error", "读取服务器监控历史失败")
		return
	}
	WriteJSON(w, http.StatusOK, struct {
		Samples []monitorSample `json:"samples"`
	}{Samples: samples})
}

func (a *App) loadMonitorHistory(ctx context.Context, from, to int64) ([]monitorSample, error) {
	rows, err := a.DB.QueryContext(ctx, `
		SELECT at, cpu_percent, memory_used_bytes, memory_total_bytes, disks, load_average
		FROM server_monitor_samples WHERE at BETWEEN ? AND ? ORDER BY at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := []monitorSample{}
	for rows.Next() {
		var sample monitorSample
		var disks string
		var loadAverage *string
		if err := rows.Scan(&sample.At, &sample.CPUPercent, &sample.MemoryUsedBytes, &sample.MemoryTotalBytes, &disks, &loadAverage); err != nil {
			return nil, err
		}
		sample.Disks = []monitorDisk{}
		if err := json.Unmarshal([]byte(disks), &sample.Disks); err != nil {
			return nil, err
		}
		if loadAverage != nil {
			if err := json.Unmarshal([]byte(*loadAverage), &sample.LoadAverage); err != nil {
				return nil, err
			}
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}
