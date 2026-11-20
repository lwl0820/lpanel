package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"lpanel/internal/panel"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const installRoot = "/opt/lpanel"

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	if command == "upgrade-worker" {
		return runUpgradeCommand(args)
	}
	if command == "manage" {
		return runManageCommand(args)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	root := flags.String("root", installRoot, "安装根目录")
	username := flags.String("username", "admin", "管理员账号")
	port := flags.Int("port", 8443, "面板端口")
	listen := flags.String("listen", "127.0.0.1:18080", "后端回环地址")
	loginPath := flags.String("login-path", "", "面板登录路径，留空通过根地址登录")
	domain := flags.String("domain", "", "面板域名")
	autoCert := flags.Bool("auto-cert", false, "自动申请证书")
	agreeTOS := flags.Bool("agree-tos", false, "同意证书服务条款")
	email := flags.String("email", "", "证书联系邮箱")
	acmeServer := flags.String("acme-server", "", "ACME 服务地址")
	acmeProvider := flags.String("acme-provider", "", "证书提供商，letsencrypt（默认）或 zerossl")
	acmeEABFile := flags.String("acme-eab-file", "", "ZeroSSL 凭据文件，两行依次为 Key ID 和 HMAC 密钥")
	repository := flags.String("repository", "", "发行仓库 owner/repo")
	if err := flags.Parse(args); err != nil {
		return err
	}
	switch command {
	case "version":
		fmt.Println(version)
		return nil
	case "init":
		if _, err := os.Stat(*root + "/data/config.json"); err == nil {
			return fmt.Errorf("已有安装配置，不能重复初始化")
		} else if !os.IsNotExist(err) {
			return err
		}
		config := panel.DefaultConfig()
		config.PanelPort = *port
		config.Listen = *listen
		config.LoginPath = *loginPath
		config.ReleaseRepository = *repository
		if *port < 1 || *port > 65535 {
			return fmt.Errorf("端口无效")
		}
		password, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && len(password) == 0 {
			return fmt.Errorf("请通过标准输入提供密码")
		}
		password = strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r")
		if err = panel.SaveConfig(*root, config); err != nil {
			return err
		}
		app, err := panel.Open(*root, version)
		if err != nil {
			return err
		}
		defer app.Close()
		if err = app.InitializeAdministrator(*username, password); err != nil {
			_ = os.Remove(*root + "/data/config.json")
			return err
		}
		fmt.Println("管理员初始化完成")
		return nil
	case "setup-nginx", "bind-panel", "renew":
		app, err := panel.Open(*root, version)
		if err != nil {
			return err
		}
		defer app.Close()
		ctx := context.Background()
		if command == "renew" {
			return app.RenewCertificates(ctx)
		}
		if command == "setup-nginx" {
			return app.ApplyNginx(ctx)
		}
		var acmeAccount panel.ACMEAccountInput
		if *autoCert {
			acmeAccount = panel.ACMEAccountInput{Email: *email, AgreeTOS: *agreeTOS}
			switch {
			case *acmeServer != "":
				if *acmeProvider != "" {
					return fmt.Errorf("--acme-server 和 --acme-provider 只能选择一个")
				}
				acmeAccount.Name, acmeAccount.Provider, acmeAccount.Server = "自定义 ACME", "legacy", *acmeServer
			case *acmeProvider == "" || *acmeProvider == "letsencrypt":
				acmeAccount.Name, acmeAccount.Provider = "Let’s Encrypt", "letsencrypt"
			case *acmeProvider == "zerossl":
				acmeAccount.Name, acmeAccount.Provider = "ZeroSSL", "zerossl"
			default:
				return fmt.Errorf("证书提供商须为 letsencrypt 或 zerossl")
			}
			if acmeAccount.Provider == "zerossl" {
				if *acmeEABFile == "" {
					return fmt.Errorf("请通过 --acme-eab-file 提供 ZeroSSL 凭据文件")
				}
				content, readErr := os.ReadFile(*acmeEABFile)
				if readErr != nil {
					return fmt.Errorf("读取 ZeroSSL 凭据文件失败：%w", readErr)
				}
				kid, key, found := strings.Cut(strings.TrimSuffix(string(content), "\n"), "\n")
				acmeAccount.EABKID, acmeAccount.EABHMACKey = strings.TrimSpace(kid), strings.TrimSpace(key)
				if !found || strings.Contains(key, "\n") || acmeAccount.EABKID == "" || acmeAccount.EABHMACKey == "" {
					return fmt.Errorf("ZeroSSL 凭据文件须为两行，依次填写 Key ID 和 HMAC 密钥")
				}
			} else if *acmeEABFile != "" {
				return fmt.Errorf("--acme-eab-file 仅用于 ZeroSSL")
			}
		}
		items, err := app.ListBindings(ctx)
		if err != nil {
			return err
		}
		binding := panel.Binding{Kind: "panel", Domain: *domain}
		for _, item := range items {
			if item.Kind == "panel" {
				binding.ID = item.ID
			}
		}
		binding, err = app.SaveBinding(ctx, binding)
		if err != nil {
			return err
		}
		if *autoCert {
			account, createErr := app.CreateACMEAccount(ctx, acmeAccount)
			if createErr != nil {
				return createErr
			}
			if err = app.RequestCertificateWithAccount(ctx, binding.ID, account.ID); err != nil {
				return err
			}
		}
		items, err = app.ListBindings(ctx)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.ID == binding.ID {
				binding = item
			}
		}
		fmt.Printf("面板访问地址：%s%s\n", app.BindingURL(binding), app.Config.LoginPath)
		return nil
	case "serve":
		app, err := panel.Open(*root, version)
		if err != nil {
			return err
		}
		defer app.Close()
		if app.Root == installRoot && os.Geteuid() == 0 {
			// 旧版升级程序会启动新版服务，由新版补建终端命令链接。
			const link = "/usr/local/bin/lpanel"
			target := filepath.Join(installRoot, "current", "lpanel")
			if existing, _ := os.Readlink(link); existing != target {
				if _, err := os.Stat(target); err == nil {
					if err = os.MkdirAll(filepath.Dir(link), 0755); err == nil {
						err = os.Symlink(target, link)
					}
					if err != nil {
						log.Printf("无法创建命令链接 %s：%v", link, err)
					}
				}
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return app.Serve(ctx)
	default:
		return fmt.Errorf("未知命令：%s", command)
	}
}
