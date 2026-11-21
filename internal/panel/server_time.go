package panel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const timeSyncUnit = "systemd-timesyncd.service"

type serverTimeStatus struct {
	ServerTime   time.Time `json:"server_time"`
	Timezone     string    `json:"timezone"`
	AutoSync     *bool     `json:"auto_sync"`
	Synchronized *bool     `json:"synchronized"`
	CanSetAuto   bool      `json:"can_set_auto"`
	CanSyncNow   bool      `json:"can_sync_now"`
	Message      string    `json:"message,omitempty"`
}

func (a *App) registerServerTime() {
	a.Mux.Handle("GET /api/v1/server/time", a.RequireAuth(http.HandlerFunc(a.getServerTime)))
	a.Mux.Handle("GET /api/v1/server/time/zones", a.RequireAuth(http.HandlerFunc(a.getServerTimeZones)))
	a.Mux.Handle("PUT /api/v1/server/time/timezone", a.RequireAuth(http.HandlerFunc(a.setServerTimezone)))
	a.Mux.Handle("PUT /api/v1/server/time/auto", a.RequireAuth(http.HandlerFunc(a.setServerTimeAuto)))
	a.Mux.Handle("POST /api/v1/server/time/sync", a.RequireAuth(http.HandlerFunc(a.syncServerTime)))
}

func timeProperties(out []byte) map[string]string {
	properties := make(map[string]string)
	for line := range strings.SplitSeq(string(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			properties[key] = value
		}
	}
	return properties
}

func (a *App) readServerTime(ctx context.Context) (serverTimeStatus, error) {
	syncRunning := a.serverTimeSyncRunning.Load()
	out, err := a.Command(ctx, "timedatectl", "show")
	properties := timeProperties(out)
	status := serverTimeStatus{Timezone: properties["Timezone"]}
	if err == nil {
		status.CanSetAuto = properties["CanNTP"] == "yes"
		for property, field := range map[string]**bool{"NTP": &status.AutoSync, "NTPSynchronized": &status.Synchronized} {
			switch properties[property] {
			case "yes":
				*field = new(true)
			case "no":
				*field = new(false)
			}
		}
	} else {
		status.Timezone = ""
	}

	var location *time.Location
	if status.Timezone != "" && status.Timezone != "Local" {
		location, _ = time.LoadLocation(status.Timezone)
	}
	if location == nil {
		// 重新读取系统文件，避免进程留存更换时区前的 time.Local。
		data, readErr := os.ReadFile("/etc/localtime")
		if errors.Is(readErr, os.ErrNotExist) {
			status.Timezone, location = "UTC", time.UTC
		} else if readErr != nil {
			return status, readErr
		} else {
			if status.Timezone == "" {
				link, _ := os.Readlink("/etc/localtime")
				_, status.Timezone, _ = strings.Cut(link, "zoneinfo/")
			}
			if status.Timezone == "" {
				status.Timezone = "系统本地时区"
			}
			location, err = time.LoadLocationFromTZData(status.Timezone, data)
			if err != nil {
				return status, err
			}
		}
	}
	status.ServerTime = time.Now().In(location)
	if status.AutoSync != nil {
		_, enabled, reason := a.timeSyncService(ctx, *status.AutoSync)
		if enabled != nil {
			status.AutoSync = enabled
		}
		status.CanSyncNow = reason == ""
		status.Message = reason
	} else {
		status.Message = "无法读取自动校时与同步状态"
	}
	if syncRunning || a.serverTimeSyncRunning.Load() {
		status.CanSyncNow = false
		status.Message = "正在进行本次校时，请稍后再试"
	}
	return status, nil
}

func (a *App) getServerTime(w http.ResponseWriter, r *http.Request) {
	status, err := a.readServerTime(r.Context())
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "server_time_unavailable", "无法读取服务器时区")
		return
	}
	WriteJSON(w, http.StatusOK, status)
}

func (a *App) serverTimeZones(ctx context.Context) ([]string, error) {
	out, err := a.Command(ctx, "timedatectl", "list-timezones")
	return strings.Fields(string(out)), err
}

func (a *App) getServerTimeZones(w http.ResponseWriter, r *http.Request) {
	zones, err := a.serverTimeZones(r.Context())
	if err != nil || len(zones) == 0 {
		WriteError(w, http.StatusServiceUnavailable, "timezones_unavailable", "无法读取服务器支持的时区")
		return
	}
	WriteJSON(w, http.StatusOK, map[string][]string{"zones": zones})
}

func (a *App) setServerTimezone(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Timezone string `json:"timezone"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	ctx := r.Context()
	zones, err := a.serverTimeZones(ctx)
	if err != nil || len(zones) == 0 {
		WriteError(w, http.StatusServiceUnavailable, "timezones_unavailable", "无法读取服务器支持的时区")
		return
	}
	if !slices.Contains(zones, input.Timezone) {
		WriteError(w, http.StatusBadRequest, "invalid_timezone", "请选择服务器支持的时区")
		return
	}
	if _, err := a.Command(ctx, "timedatectl", "set-timezone", input.Timezone); err != nil {
		WriteError(w, http.StatusInternalServerError, "set_timezone_failed", "无法更改服务器时区")
		return
	}
	a.getServerTime(w, r)
}

func (a *App) setServerTimeAuto(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !ReadJSON(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		WriteError(w, http.StatusBadRequest, "invalid_auto_sync", "请选择开启或关闭自动校时")
		return
	}
	if !a.serverTimeMu.TryLock() {
		WriteError(w, http.StatusConflict, "server_time_busy", "服务器正在处理校时请求，请稍后再试")
		return
	}
	defer a.serverTimeMu.Unlock()
	if _, err := a.Command(r.Context(), "timedatectl", "set-ntp", strconv.FormatBool(*input.Enabled)); err != nil {
		WriteError(w, http.StatusInternalServerError, "set_auto_sync_failed", "无法更改自动校时设置，请检查服务器的校时服务")
		return
	}
	a.getServerTime(w, r)
}

func (a *App) timeSyncService(ctx context.Context, auto bool) (active bool, enabled *bool, reason string) {
	out, err := a.Command(ctx, "systemctl", "show",
		"-p", "LoadState", "-p", "ActiveState", "-p", "UnitFileState", "-p", "Conflicts",
		"-p", "RuntimeDirectory", "-p", "RuntimeDirectoryPreserve", timeSyncUnit)
	unit := timeProperties(out)
	if err != nil || unit["LoadState"] != "loaded" {
		return false, nil, "服务器缺少可用的 systemd-timesyncd 校时服务"
	}
	state := unit["ActiveState"]
	active = state == "active"
	if auto && (state == "inactive" || state == "failed") {
		return false, nil, "服务器正在使用其他校时服务，此处无法立即同步"
	}

	conflicts := append(strings.Fields(unit["Conflicts"]), "chrony.service", "chronyd.service", "ntp.service", "ntpd.service", "ntpsec.service", "openntpd.service")
	args := append([]string{"list-units", "--state=active,activating,reloading,deactivating", "--no-legend", "--plain", "--"}, conflicts...)
	out, err = a.Command(ctx, "systemctl", args...)
	if err != nil {
		return false, nil, "无法确认服务器是否有其他正在运行的校时服务"
	}
	if strings.TrimSpace(string(out)) != "" {
		return false, nil, "服务器有其他正在运行的服务与立即校时冲突"
	}
	// timedatectl 的 NTP 属性反映服务是否运行，持久设置由 systemd 提供。
	switch unit["UnitFileState"] {
	case "enabled":
		enabled = new(true)
	case "disabled", "enabled-runtime", "masked", "masked-runtime":
		enabled = new(false)
	}
	if !active && state != "inactive" && state != "failed" {
		return false, enabled, "校时服务正在切换状态，请稍后再试"
	}

	// 重启时由 systemd 清除运行目录，使同步标记只能来自新的服务进程。
	if unit["RuntimeDirectoryPreserve"] != "no" || !slices.Contains(strings.Fields(unit["RuntimeDirectory"]), "systemd/timesync") {
		return false, enabled, "当前校时服务配置无法确认新的同步结果"
	}
	return active, enabled, ""
}

func (a *App) syncServerTime(w http.ResponseWriter, r *http.Request) {
	if !a.serverTimeMu.TryLock() {
		WriteError(w, http.StatusConflict, "server_time_busy", "服务器正在处理校时请求，请稍后再试")
		return
	}
	ctx := r.Context()
	out, err := a.Command(ctx, "timedatectl", "show", "--property=NTP", "--value")
	auto := strings.TrimSpace(string(out))
	if err != nil || (auto != "yes" && auto != "no") {
		a.serverTimeMu.Unlock()
		WriteError(w, http.StatusServiceUnavailable, "time_sync_unavailable", "无法确认自动校时状态")
		return
	}
	active, _, reason := a.timeSyncService(ctx, auto == "yes")
	if reason != "" {
		a.serverTimeMu.Unlock()
		WriteError(w, http.StatusConflict, "time_sync_unavailable", reason)
		return
	}
	op, err := a.StartOperation("server_time_sync", func(ctx context.Context) (string, error) {
		defer a.serverTimeMu.Unlock()
		a.serverTimeSyncRunning.Store(true)
		defer a.serverTimeSyncRunning.Store(false)
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		return a.runServerTimeSync(ctx, active)
	})
	if err != nil {
		a.serverTimeMu.Unlock()
		WriteError(w, http.StatusInternalServerError, "time_sync_start_failed", "无法发起校时，请稍后再试")
		return
	}
	WriteJSON(w, http.StatusAccepted, op)
}

func (a *App) runServerTimeSync(ctx context.Context, active bool) (_ string, result error) {
	previous, err := os.Stat(a.serverTimeSyncMarker)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("无法读取同步标记：%w", err)
	}
	action := "restart"
	if !active {
		if previous != nil {
			return "", errors.New("发现上次校时留下的标记，无法确认本次同步")
		}
		action = "start"
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if _, err := a.Command(cleanup, "systemctl", "stop", timeSyncUnit); err != nil {
				result = errors.Join(result, fmt.Errorf("无法停止临时启动的校时服务：%w", err))
			}
		}()
	}

	if _, err := a.Command(ctx, "systemctl", action, timeSyncUnit); err != nil {
		return "", fmt.Errorf("无法启动本次校时：%w", err)
	}
	out, err := a.Command(ctx, "systemctl", "show", "--property=ActiveState", "--value", timeSyncUnit)
	if err != nil || strings.TrimSpace(string(out)) != "active" {
		return "", errors.New("校时服务未运行，无法开始同步")
	}

	ticks := time.Tick(250 * time.Millisecond)
	for {
		marker, err := os.Stat(a.serverTimeSyncMarker)
		if err == nil && (previous == nil || !os.SameFile(previous, marker) || !marker.ModTime().Equal(previous.ModTime())) {
			// 标记证明本次收到有效的校时响应，内核状态确认系统接受校时。
			out, err := a.Command(ctx, "timedatectl", "show", "--property=NTPSynchronized", "--value")
			if err == nil && strings.TrimSpace(string(out)) == "yes" {
				return "服务器时间已同步", nil
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("无法读取本次同步标记：%w", err)
		}
		select {
		case <-ctx.Done():
			return "", errors.New("等待校时超时，服务器未确认本次同步，请检查校时源是否可达")
		case <-ticks:
		}
	}
}
