package panel

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	AgreeTOS          bool   `json:"agree_tos"`
	Listen            string `json:"listen"`
	PanelPort         int    `json:"panel_port"`
	PanelDomain       string `json:"panel_domain"`
	HTTPS             bool   `json:"https"`
	ReleaseRepository string `json:"release_repository"`
	ACMEEmail         string `json:"acme_email"`
	ACMEServer        string `json:"acme_server"`
	NginxConfigDir    string `json:"nginx_config_dir"`
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
	return nil
}

func SaveConfig(root string, config Config) error {
	if err := config.Validate(); err != nil {
		return err
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
