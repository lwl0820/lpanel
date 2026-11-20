package panel

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	AgreeTOS          bool   `json:"agree_tos"`
	Listen            string `json:"listen"`
	PanelPort         int    `json:"panel_port"`
	PanelDomain       string `json:"panel_domain"`
	LoginPath         string `json:"login_path"`
	HTTPS             bool   `json:"https"`
	ReleaseRepository string `json:"release_repository"`
	ACMEEmail         string `json:"acme_email"`
	ACMEServer        string `json:"acme_server"`
	NginxConfigDir    string `json:"nginx_config_dir"`
}

var loginPathPattern = regexp.MustCompile(`^(/[a-zA-Z0-9_-]+)+$`)

func validateLoginPath(path string) error {
	if path == "" || path == "/" {
		return nil
	}
	if !loginPathPattern.MatchString(path) {
		return fmt.Errorf("登录路径须以 / 开头，每段仅可使用英文字母、数字、短横线和下划线")
	}
	if path == "/api" || strings.HasPrefix(path, "/api/") || path == "/assets" || strings.HasPrefix(path, "/assets/") {
		return fmt.Errorf("此登录路径已被面板使用，请换一个路径")
	}
	return nil
}

func DefaultConfig() Config {
	return Config{Listen: "127.0.0.1:18080", PanelPort: 8443, NginxConfigDir: "/etc/nginx/conf.d"}
}

func LoadConfig(root string) (Config, error) {
	config := DefaultConfig()
	data, err := os.ReadFile(filepath.Join(root, "data", "config.json"))
	if err != nil {
		return config, err
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("配置格式错误：%w", err)
	}
	if config.LoginPath == "/" {
		config.LoginPath = ""
	}
	return config, config.Validate()
}

func (config Config) Validate() error {
	host, port, err := net.SplitHostPort(config.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("后端必须监听回环地址")
	}
	backendPort, err := strconv.Atoi(port)
	if err != nil || backendPort < 1 || backendPort > 65535 {
		return fmt.Errorf("后端端口无效")
	}
	if config.PanelPort < 1 || config.PanelPort > 65535 {
		return fmt.Errorf("面板端口必须在 1 至 65535 之间")
	}
	if backendPort == config.PanelPort {
		return fmt.Errorf("面板端口不能与后端端口相同")
	}
	return validateLoginPath(config.LoginPath)
}

func SaveConfig(root string, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if config.LoginPath == "/" {
		config.LoginPath = ""
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	if err := os.Chmod(root, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "data", "config.json"), append(data, '\n'), 0600)
}
