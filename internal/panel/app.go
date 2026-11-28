package panel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	_ "modernc.org/sqlite"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type App struct {
	domainsMu    sync.Mutex
	projectsMu   sync.Mutex
	projectLocks map[string]*projectLock
	programsMu   sync.Mutex
	configMu     sync.RWMutex
	firewallMu   sync.Mutex
	swapMu       sync.Mutex
	serverTimeMu sync.Mutex
	serverDNSMu  sync.Mutex
	Root         string
	Config       Config
	Version      string
	DB           *sql.DB
	Mux          *http.ServeMux
	Command      func(context.Context, string, ...string) ([]byte, error)
	NginxProbe   func(context.Context, string, string, string) error

	zeroSSLClient           *http.Client
	panelIPACMEHTTPClient   *http.Client
	panelIPCertificateProbe func(context.Context, string, [sha256.Size]byte) error
	firewallFS              fs.FS
	swapProcPath            string
	swapFstabPath           string
	serverTimeSyncMarker    string
	serverDNSHostRoot       string
	serverTimeSyncRunning   atomic.Bool
	monitorLive             monitorLiveState
	programSupervisorConfig string
	programSupervisorDir    string
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, map[string]any{"error": APIError{code, message}})
}
func ReadJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(value); err != nil {
		WriteError(w, 400, "invalid_json", "请求内容格式错误")
		return false
	}
	return true
}

func Open(root, version string) (*App, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	config, err := LoadConfig(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{"data", "data/logs", ".tmp"} {
		if err = os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "data", "panel.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=3000;
 PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS administrators (id INTEGER PRIMARY KEY CHECK(id=1),username TEXT NOT NULL UNIQUE,password_hash TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions (token TEXT PRIMARY KEY,expires_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS operations (id TEXT PRIMARY KEY,kind TEXT NOT NULL,status TEXT NOT NULL,log TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL);`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	a := &App{Root: root, Config: config, Version: version, DB: db, Mux: http.NewServeMux()}
	a.zeroSSLClient = &http.Client{Timeout: 8 * time.Second}
	a.NginxProbe = probeNginx
	a.panelIPCertificateProbe = probePanelIPCertificate
	a.firewallFS = os.DirFS("/")
	a.swapProcPath = "/proc/swaps"
	a.swapFstabPath = "/etc/fstab"
	a.serverDNSHostRoot = "/"
	a.serverTimeSyncMarker = "/run/systemd/timesync/synchronized"
	a.programSupervisorConfig = "/etc/supervisor/supervisord.conf"
	a.programSupervisorDir = "/etc/supervisor/conf.d"
	a.Command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = a.Root
		cmd.Env = append(os.Environ(), "TMPDIR="+filepath.Join(a.Root, ".tmp"))
		if name == "apt-get" {
			cmd.Env = append(cmd.Env, "DEBIAN_FRONTEND=noninteractive")
		}
		if name == "ufw" || name == "resolvectl" {
			cmd.Env = append(cmd.Env, "LC_ALL=C", "LANGUAGE=C")
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return out, fmt.Errorf("%s 执行失败：%w", name, err)
		}
		return out, nil
	}
	if err := a.RegisterUpgradeRoutes(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := a.RegisterProjectRoutes(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := a.RegisterSiteRoutes(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := a.registerDomains(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := a.registerPrograms(); err != nil {
		_ = db.Close()
		return nil, err
	}
	a.registerServerFiles()
	a.registerServerFirewall()
	a.registerServerTime()
	a.registerServerDNS()
	a.registerImages()
	if err := a.registerServerSwap(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := a.registerMonitorHistory(); err != nil {
		_ = db.Close()
		return nil, err
	}
	a.registerMonitorLive()
	if err := a.registerMCPAccess(); err != nil {
		_ = db.Close()
		return nil, err
	}
	a.Mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if err := a.DB.PingContext(r.Context()); err != nil {
			WriteError(w, 503, "database_unavailable", "数据库不可用")
			return
		}
		WriteJSON(w, 200, map[string]string{"status": "ok", "version": a.Version})
	})
	a.Mux.HandleFunc("POST /api/v1/auth/login", a.login)
	a.Mux.Handle("POST /api/v1/auth/logout", a.RequireAuth(http.HandlerFunc(a.logout)))
	a.Mux.Handle("GET /api/v1/auth/me", a.RequireAuth(http.HandlerFunc(a.me)))
	a.Mux.Handle("GET /api/v1/operations/{id}", a.RequireAuth(http.HandlerFunc(a.operation)))
	a.Mux.Handle("GET /api/v1/system/version", a.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, 200, map[string]string{"version": a.Version})
	})))
	a.Mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, 404, "not_found", "请求的接口不存在")
	})
	return a, nil
}
func (a *App) Close() error          { return a.DB.Close() }
func (a *App) Handler() http.Handler { return http.HandlerFunc(a.servePanel) }

func (a *App) currentConfig() Config {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.Config
}

func (a *App) setConfig(config Config) {
	a.configMu.Lock()
	a.Config = config
	a.configMu.Unlock()
}

// 由目标版本刷新已有配置，兼容只会重启服务并检查健康的旧版升级程序。
func (a *App) refreshPanelProxy(ctx context.Context) error {
	path := filepath.Join(a.currentConfig().NginxConfigDir, "lpanel.conf")
	config, err := readManagedNginxConfig(path)
	if err != nil {
		return err
	}
	if config == nil || bytes.HasPrefix(config, []byte(managedNginxHeader+nginxTemplateRevision)) {
		return nil
	}
	return a.ApplyNginx(ctx)
}

// 在监听健康接口之前执行目标版本要求的配套操作。
func (a *App) runUpgradeActions(ctx context.Context) error {
	if err := a.refreshPanelProxy(ctx); err != nil {
		actionErr := fmt.Errorf("更新面板的 Nginx 代理配置失败：%w", err)
		var id string
		queryErr := a.DB.QueryRowContext(ctx, "SELECT id FROM operations WHERE kind='upgrade' AND status='running'").Scan(&id)
		if queryErr == nil {
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if _, err := a.Command(checkCtx, "systemctl", "is-active", "--quiet", "lpanel-upgrade-"+id+".service"); err == nil {
				return actionErr
			}
		} else if !errors.Is(queryErr, sql.ErrNoRows) {
			log.Printf("读取升级任务失败：%v", queryErr)
		}
		log.Print(actionErr)
	}
	return nil
}

func (a *App) Serve(ctx context.Context) error {
	if err := a.runUpgradeActions(ctx); err != nil {
		return err
	}
	if _, err := a.DB.Exec("UPDATE operations SET status='failed',error='服务重启，操作已中断' WHERE status IN ('queued','running') AND kind!='upgrade'"); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	var monitor sync.WaitGroup
	monitor.Go(func() { a.runMonitorHistory(ctx) })
	defer func() {
		cancel()
		monitor.Wait()
	}()
	a.StartUpgradeChecker(ctx)
	server := &http.Server{Addr: a.Config.Listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
