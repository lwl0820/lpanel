package main

import (
	"context"
	"flag"
	"fmt"
	"lpanel/internal/panel"
	"time"
)

func runUpgradeCommand(args []string) error {
	flags := flag.NewFlagSet("upgrade-worker", flag.ContinueOnError)
	root := flags.String("root", "/opt/lpanel", "安装根目录")
	operation := flags.String("operation", "", "升级操作标识")
	release := flags.String("release", "", "候选版本目录")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *operation == "" || *release == "" {
		return fmt.Errorf("升级操作和版本目录不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return panel.UpgradeWorker(ctx, *root, version, *operation, *release)
}
