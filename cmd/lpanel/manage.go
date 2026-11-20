package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"

	"lpanel/internal/panel"
)

func runManageCommand(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(`用法：lpanel manage <命令>

  username <用户名>   修改管理员用户名
  password            重置管理员密码
  access              查看或修改面板访问设置

运行 lpanel manage <命令> --help 查看命令参数。
`)
		return nil
	}

	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", installRoot, "面板安装目录")
	usage := "lpanel manage " + command
	var host, path, https string
	var port int
	var change panel.AccessChange
	switch command {
	case "username":
		usage += " [--root <目录>] <用户名>"
	case "password":
		usage += " [--root <目录>]"
	case "access":
		usage += " [选项]"
		flags.StringVar(&host, "host", "", "访问域名或 IP，填写 auto 恢复使用服务器地址")
		flags.IntVar(&port, "port", 0, "对外访问端口")
		flags.StringVar(&path, "login-path", "", "专用登录路径，填写 / 恢复根路径")
		flags.StringVar(&https, "https", "", "开启或关闭 HTTPS，填写 on 或 off")
		flags.BoolVar(&change.AgreeTOS, "agree-tos", false, "同意 Let’s Encrypt 服务条款（https://letsencrypt.org/repository/）")
	default:
		return errors.New("未知的管理命令，请运行 lpanel manage --help 查看用法")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Printf("用法：%s\n", usage)
			flags.SetOutput(os.Stdout)
			flags.PrintDefaults()
			return nil
		}
		return fmt.Errorf("参数无效。用法：%s", usage)
	}
	if command == "username" && flags.NArg() != 1 || command != "username" && flags.NArg() != 0 {
		return fmt.Errorf("参数数量不正确。用法：%s", usage)
	}
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "host":
			change.Host = &host
		case "port":
			change.Port = &port
		case "login-path":
			change.LoginPath = &path
		case "https":
			change.HTTPS = new(https == "on")
		}
	})
	if change.HTTPS != nil && https != "on" && https != "off" {
		return errors.New("HTTPS 选项须填写 on 或 off")
	}
	if os.Geteuid() != 0 {
		return errors.New("请使用 sudo 运行面板管理命令")
	}

	app, err := panel.Open(*root, version)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("找不到面板安装配置，请检查安装目录 %s", *root)
	}
	if err != nil {
		return fmt.Errorf("无法打开面板安装数据：%w", err)
	}
	defer app.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	switch command {
	case "username":
		if err := app.ChangeAdministratorUsername(ctx, flags.Arg(0)); err != nil {
			return err
		}
		fmt.Println("管理员用户名已修改")
	case "password":
		password, err := readManagementPassword(ctx)
		if err != nil {
			return err
		}
		defer clear(password)
		if err = app.ResetAdministratorPassword(ctx, password); err != nil {
			return err
		}
		fmt.Println("管理员密码已重置，请重新登录")
	case "access":
		address, err := app.ManageAccess(ctx, change)
		if err != nil {
			return err
		}
		fmt.Printf("面板登录地址：%s\n", address)
	}
	return nil
}

func readManagementPassword(ctx context.Context) (password []byte, err error) {
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("请在服务器终端交互重置密码")
	}
	defer terminal.Close()
	fd := int(terminal.Fd())
	original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, fmt.Errorf("无法读取终端设置：%w", err)
	}
	hidden := *original
	hidden.Lflag = (hidden.Lflag | unix.ICANON) &^ (unix.ECHO | unix.ECHONL)
	if err = unix.IoctlSetTermios(fd, unix.TCSETS, &hidden); err != nil {
		return nil, fmt.Errorf("无法隐藏密码输入：%w", err)
	}
	defer func() {
		if restoreErr := unix.IoctlSetTermios(fd, unix.TCSETS, original); restoreErr != nil {
			clear(password)
			password = nil
			err = errors.Join(err, fmt.Errorf("恢复终端设置失败：%w", restoreErr))
		}
	}()

	var buffer [4096]byte
	defer clear(buffer[:])
	var first []byte
	defer func() { clear(first) }()
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for i, prompt := range []string{"请输入新密码：", "请再次输入新密码："} {
		if _, err = fmt.Fprint(terminal, prompt); err != nil {
			return nil, fmt.Errorf("无法写入终端：%w", err)
		}
		var n int
		for {
			if ctx.Err() != nil {
				fmt.Fprintln(terminal)
				return nil, errors.New("已取消密码重置")
			}
			ready, pollErr := unix.Poll(poll, 100)
			if errors.Is(pollErr, unix.EINTR) || pollErr == nil && ready == 0 {
				continue
			}
			if pollErr != nil {
				return nil, fmt.Errorf("无法读取终端：%w", pollErr)
			}
			n, err = unix.Read(fd, buffer[:])
			if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
				continue
			}
			break
		}
		fmt.Fprintln(terminal)
		if err != nil {
			return nil, fmt.Errorf("读取密码失败：%w", err)
		}
		if n == 0 || buffer[n-1] != '\n' {
			return nil, errors.New("已取消密码重置")
		}
		input := buffer[:n-1]
		if i == 0 {
			first = bytes.Clone(input)
		} else if !bytes.Equal(first, input) {
			return nil, errors.New("两次输入的密码不一致，请重试")
		}
	}
	return bytes.Clone(first), nil
}
