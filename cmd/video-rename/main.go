package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"video-rename/internal/app"
	"video-rename/internal/config"
)

var version = "0.2.0"

func main() {
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if o.Help {
		fmt.Print(usageText)
		return
	}
	if o.Version {
		fmt.Println("video-rename", version)
		return
	}
	cfg, err := config.Load(o.ConfigPath)
	if err == nil {
		err = cfg.ValidateService()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	slog.Info("starting video-rename")
	if err = app.Serve(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type cliOptions struct {
	ConfigPath    string
	Help, Version bool
}

const defaultConfigPath = "configs/config.local.yaml"
const usageText = `video-rename — 视频命名工作台
用法：video-rename [--config configs/config.local.yaml]
  -f, --config  YAML 配置文件
  -h, --help    显示帮助
      --version 显示版本
媒体目录使用 YAML path；登录、手动运行与自动计划在 Web 页面操作。
`

func parseArgs(args []string) (*cliOptions, error) {
	o := &cliOptions{ConfigPath: defaultConfigPath}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, has := strings.Cut(arg, "=")
		switch {
		case name == "--help" || name == "-h":
			if has {
				return nil, fmt.Errorf("帮助参数不接受值")
			}
			o.Help = true
			name = "help"
		case name == "--version":
			if has {
				return nil, fmt.Errorf("版本参数不接受值")
			}
			o.Version = true
			name = "version"
		case name == "--config" || name == "-f" || strings.HasPrefix(name, "-f"):
			if rest, ok := strings.CutPrefix(name, "-f"); ok && rest != "" {
				value = rest
				has = true
			}
			if !has {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("--config 缺少路径")
				}
				value = args[i]
			}
			if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") {
				return nil, fmt.Errorf("配置路径无效")
			}
			o.ConfigPath = value
			name = "config"
		default:
			return nil, fmt.Errorf("不支持参数 %s；媒体目录请写入 YAML path，处理选项请使用 Web 页面", arg)
		}
		if seen[name] {
			return nil, fmt.Errorf("参数 %s 重复", name)
		}
		seen[name] = true
	}
	return o, nil
}
