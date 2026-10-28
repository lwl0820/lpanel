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
	"strings"
	"syscall"
)

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
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	root := flags.String("root", "/opt/lpanel", "安装根目录")
	username := flags.String("username", "admin", "管理员账号")
	port := flags.Int("port", 8443, "面板端口")
	listen := flags.String("listen", "127.0.0.1:18080", "后端回环地址")
	domain := flags.String("domain", "", "面板域名")
	autoCert := flags.Bool("auto-cert", false, "自动申请证书")
	agreeTOS := flags.Bool("agree-tos", false, "同意证书服务条款")
	email := flags.String("email", "", "证书联系邮箱")
	acmeServer := flags.String("acme-server", "", "ACME 服务地址")
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
		app.Config.AgreeTOS = *agreeTOS
		app.Config.ACMEEmail = *email
		app.Config.ACMEServer = *acmeServer
		if err = panel.SaveConfig(app.Root, app.Config); err != nil {
			return err
		}
		items, err := app.ListBindings(ctx)
		if err != nil {
			return err
		}
		binding := panel.Binding{Kind: "panel", Domain: *domain, AutoCertificate: *autoCert}
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
			if err = app.RequestCertificate(ctx, binding.ID); err != nil {
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
		fmt.Printf("面板访问地址：%s\n", app.BindingURL(binding))
		return nil
	case "serve":
		app, err := panel.Open(*root, version)
		if err != nil {
			return err
		}
		defer app.Close()
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		return app.Serve(ctx)
	default:
		return fmt.Errorf("未知命令：%s", command)
	}
}
