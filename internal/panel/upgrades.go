package panel

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type UpgradeState struct {
	Repository  string `json:"repository"`
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Available   bool   `json:"available"`
	AutoUpdate  bool   `json:"auto_update"`
	CheckedAt   string `json:"checked_at"`
	Error       string `json:"error"`
	AssetURL    string `json:"asset_url,omitempty"`
	ChecksumURL string `json:"checksum_url,omitempty"`
}

var releaseVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:\+[0-9A-Za-z.-]+)?$`)
var releaseRepository = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func newerRelease(latest, current string) (bool, error) {
	l, c := releaseVersion.FindStringSubmatch(latest), releaseVersion.FindStringSubmatch(current)
	if l == nil {
		return false, fmt.Errorf("发布版本须为 vX.Y.Z 正式版本")
	}
	if c == nil {
		return false, fmt.Errorf("当前为开发版本，无法自动比较升级；请使用正式发行包")
	}
	for i := 1; i <= 3; i++ {
		lv, e := strconv.ParseUint(l[i], 10, 64)
		if e != nil {
			return false, fmt.Errorf("发布版本无效")
		}
		cv, e := strconv.ParseUint(c[i], 10, 64)
		if e != nil {
			return false, fmt.Errorf("当前版本无效")
		}
		if lv != cv {
			return lv > cv, nil
		}
	}
	return false, nil
}

func (a *App) migrateUpgradeSettings() error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`
		CREATE TABLE IF NOT EXISTS upgrade_settings(id INTEGER PRIMARY KEY CHECK(id=1),repository TEXT NOT NULL,auto_update INTEGER NOT NULL DEFAULT 0);
		CREATE TABLE IF NOT EXISTS upgrade_state(id INTEGER PRIMARY KEY CHECK(id=1),payload TEXT NOT NULL);
	`); err != nil {
		return err
	}
	var hasAutoUpdate bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('upgrade_settings') WHERE name='auto_update')").Scan(&hasAutoUpdate); err != nil {
		return err
	}
	if !hasAutoUpdate {
		if _, err = tx.Exec("ALTER TABLE upgrade_settings ADD COLUMN auto_update INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT OR IGNORE INTO upgrade_settings(id,repository) VALUES(1,?)", a.Config.ReleaseRepository); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) RegisterUpgradeRoutes() error {
	if err := a.migrateUpgradeSettings(); err != nil {
		return err
	}
	writeStatus := func(w http.ResponseWriter) {
		state, err := a.UpgradeStatus()
		if err != nil {
			WriteError(w, 500, "update_state_error", "读取版本状态失败")
			return
		}
		WriteJSON(w, 200, state)
	}
	a.Mux.Handle("GET /api/v1/system/updates", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w)
	})))
	a.Mux.Handle("PUT /api/v1/system/updates/repository", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Repository string `json:"repository"`
		}
		if !ReadJSON(w, r, &input) {
			return
		}
		input.Repository = strings.TrimSpace(input.Repository)
		if input.Repository != "" && !releaseRepository.MatchString(input.Repository) {
			WriteError(w, 400, "invalid_repository", "仓库格式应为 owner/repo")
			return
		}
		if _, err := a.DB.Exec("UPDATE upgrade_settings SET repository=? WHERE id=1", input.Repository); err != nil {
			WriteError(w, 500, "update_settings_error", "保存发布源失败")
			return
		}
		state := UpgradeState{Repository: input.Repository, Current: a.Version}
		if err := a.saveUpgradeState(state); err != nil {
			WriteError(w, 500, "update_state_error", "保存版本状态失败")
			return
		}
		writeStatus(w)
	})))
	a.Mux.Handle("PUT /api/v1/system/updates/auto-update", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			AutoUpdate *bool `json:"auto_update"`
		}
		if !ReadJSON(w, r, &input) {
			return
		}
		if input.AutoUpdate == nil {
			WriteError(w, 400, "invalid_auto_update", "请选择是否开启自动更新")
			return
		}
		if _, err := a.DB.ExecContext(r.Context(), "UPDATE upgrade_settings SET auto_update=? WHERE id=1", *input.AutoUpdate); err != nil {
			WriteError(w, 500, "update_settings_error", "保存自动更新设置失败")
			return
		}
		writeStatus(w)
	})))
	a.Mux.Handle("POST /api/v1/system/updates/check", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, err := a.checkUpgrade(r.Context(), "https://api.github.com", &http.Client{Timeout: 45 * time.Second})
		if err != nil {
			WriteError(w, 502, "update_check_failed", err.Error())
			return
		}
		WriteJSON(w, 200, state)
	})))
	a.Mux.Handle("POST /api/v1/system/updates/upgrade", a.RequireAuth(http.HandlerFunc(a.startUpgrade)))
	return nil
}
func (a *App) UpgradeStatus() (UpgradeState, error) {
	var state UpgradeState
	var repository, payload string
	var autoUpdate bool
	if err := a.DB.QueryRow(`
		SELECT settings.repository, settings.auto_update, COALESCE(state.payload, '{}')
		FROM upgrade_settings AS settings LEFT JOIN upgrade_state AS state ON state.id=settings.id
		WHERE settings.id=1
	`).Scan(&repository, &autoUpdate, &payload); err != nil {
		return state, err
	}
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return state, err
	}
	state.Repository = repository
	state.AutoUpdate = autoUpdate
	state.Current = a.Version
	newer, _ := newerRelease(state.Latest, state.Current)
	state.Available = state.Available && newer
	return state, nil
}
func (a *App) saveUpgradeState(state UpgradeState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec("INSERT INTO upgrade_state(id,payload) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", string(data))
	return err
}
func (a *App) checkUpgrade(ctx context.Context, base string, client *http.Client) (state UpgradeState, err error) {
	state, err = a.UpgradeStatus()
	if err != nil {
		return state, err
	}
	state = UpgradeState{Repository: state.Repository, AutoUpdate: state.AutoUpdate, Current: a.Version, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	defer func() {
		if err != nil {
			state.Error = err.Error()
		}
		if saveErr := a.saveUpgradeState(state); err == nil && saveErr != nil {
			err = saveErr
		}
	}()
	if state.Repository == "" {
		return state, fmt.Errorf("尚未配置 GitHub 发布仓库")
	}
	if !releaseRepository.MatchString(state.Repository) {
		return state, fmt.Errorf("发布仓库格式错误")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/repos/"+state.Repository+"/releases/latest", nil)
	if err != nil {
		return state, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "LPanel/"+a.Version)
	response, err := client.Do(req)
	if err != nil {
		return state, fmt.Errorf("连接发布源失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return state, fmt.Errorf("读取发布源失败（HTTP %d）", response.StatusCode)
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err = json.NewDecoder(response.Body).Decode(&release); err != nil {
		return state, fmt.Errorf("发布信息格式错误：%w", err)
	}
	if release.Draft || release.Prerelease {
		return state, fmt.Errorf("发布源未返回正式版本")
	}
	state.Latest = release.Tag
	state.Available, err = newerRelease(state.Latest, state.Current)
	if err != nil {
		return state, err
	}
	if !state.Available {
		return state, nil
	}
	name := "lpanel_" + state.Latest + "_linux_" + runtime.GOARCH + ".tar.gz"
	for _, asset := range release.Assets {
		if asset.Name == name {
			state.AssetURL = asset.URL
		}
		if asset.Name == name+".sha256" {
			state.ChecksumURL = asset.URL
		}
	}
	if state.AssetURL == "" || state.ChecksumURL == "" {
		state.Available = false
		return state, fmt.Errorf("最新版本缺少 %s 架构发行包或校验文件", runtime.GOARCH)
	}
	return state, nil
}
func (a *App) StartUpgradeChecker(ctx context.Context) {
	go func() {
		client := &http.Client{Timeout: 45 * time.Second}
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				checkCtx, cancel := context.WithTimeout(ctx, time.Minute)
				a.checkAndAutoUpgrade(checkCtx, "https://api.github.com", client)
				cancel()
				timer.Reset(24 * time.Hour)
			}
		}
	}()
}

func (a *App) checkAndAutoUpgrade(ctx context.Context, base string, client *http.Client) {
	state, err := a.checkUpgrade(ctx, base, client)
	if err != nil || !state.Available || !state.AutoUpdate {
		return
	}
	if _, err := a.enqueueUpgrade(ctx, state, true); err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("启动自动更新失败：%v", err)
	}
}

func (a *App) startUpgrade(w http.ResponseWriter, r *http.Request) {
	state, err := a.UpgradeStatus()
	if err != nil || !state.Available || state.Error != "" {
		WriteError(w, 400, "update_unavailable", "请先检查并确认存在可用的新版本")
		return
	}
	op, err := a.enqueueUpgrade(r.Context(), state, false)
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, 409, "upgrade_running", "已有升级正在执行")
		return
	}
	if err != nil {
		WriteError(w, 500, "operation_error", "保存升级任务失败")
		return
	}
	WriteJSON(w, 202, op)
}

func (a *App) enqueueUpgrade(ctx context.Context, state UpgradeState, automatic bool) (Operation, error) {
	raw := make([]byte, 16)
	rand.Read(raw)
	now := time.Now().UTC().Format(time.RFC3339)
	op := Operation{ID: hex.EncodeToString(raw), Kind: "upgrade", Status: "running", CreatedAt: now, UpdatedAt: now}
	// 手动和自动安装共用一次数据库写入判定，避免同时创建升级任务。
	err := a.DB.QueryRowContext(ctx, `
		INSERT INTO operations(id,kind,status,created_at,updated_at)
		SELECT ?,?,?,?,?
		WHERE NOT EXISTS (SELECT 1 FROM operations WHERE kind='upgrade' AND status='running')
		  AND (NOT ? OR EXISTS (SELECT 1 FROM upgrade_settings WHERE id=1 AND auto_update=1 AND repository=?))
		RETURNING id
	`, op.ID, op.Kind, op.Status, now, now, automatic, state.Repository).Scan(&op.ID)
	if err != nil {
		return Operation{}, err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		dir, err := a.prepareUpgrade(ctx, state)
		if err == nil {
			_, err = a.Command(ctx, "systemd-run", "--unit=lpanel-upgrade-"+op.ID, "--collect", "--property=Type=exec", "--working-directory="+a.Root, "--setenv=TMPDIR="+a.Root+"/.tmp", a.Root+"/current/lpanel", "upgrade-worker", "--root", a.Root, "--operation", op.ID, "--release", dir)
		}
		if err != nil {
			a.finishUpgrade(op.ID, "failed", "", err.Error())
		}
	}()
	return op, nil
}
func (a *App) finishUpgrade(id, status, log, message string) {
	_, _ = a.DB.Exec("UPDATE operations SET status=?,log=?,error=?,updated_at=? WHERE id=?", status, log, message, time.Now().UTC().Format(time.RFC3339), id)
}
