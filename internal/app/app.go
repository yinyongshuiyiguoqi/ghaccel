package app

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"github.com/yinyongshuiyiguoqi/ghaccel/internal/config"
	"github.com/yinyongshuiyiguoqi/ghaccel/internal/version"
)

const (
	exitOK        = 0
	exitError     = 1
	exitUsage     = 2
	exitNeedAdmin = 3
	exitDegraded  = 4
	exitConflict  = 5
)

type globals struct {
	configPath string
	json       bool
	verbose    bool
	quiet      bool
}

func (g *globals) bind(fs *flag.FlagSet) {
	fs.StringVar(&g.configPath, "config", "", "config file path")
	fs.BoolVar(&g.json, "json", false, "emit machine-readable JSON")
	fs.BoolVar(&g.verbose, "verbose", false, "verbose logs")
	fs.BoolVar(&g.quiet, "quiet", false, "only errors")
}

func (g *globals) logger() *slog.Logger {
	level := slog.LevelInfo
	if g.verbose {
		level = slog.LevelDebug
	}
	if g.quiet {
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func Run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	switch args[0] {
	case "init":
		return cmdInit(args[1:])
	case "once":
		return cmdOnce(args[1:])
	case "start":
		return cmdDaemon(args[1:])
	case "daemon":
		return cmdDaemon(args[1:])
	case "stop":
		return cmdStop(args[1:])
	case "restore":
		return cmdRestore(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "version", "--version", "-version":
		fmt.Println("ghaccel", versionString())
		return exitOK
	case "help", "-h", "--help":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		return exitUsage
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ghaccel - GitHub 专用智能 DNS + hosts 加速器

用法:
  ghaccel init [--force]                 生成默认配置
  ghaccel once [--dry-run]               跑一轮解析/探测/写入
  ghaccel start [--interval 15m]         启动后台刷新（需管理员）
  ghaccel stop [--restore]               停止后台刷新
  ghaccel status [--json]                查看最近一轮结果与 hosts 状态
  ghaccel restore [--dry-run]            移除 hosts 中的托管块
  ghaccel doctor [--json] [--strict]     环境与网络诊断
  ghaccel version                        版本信息

全局参数:
  --config <path>   指定配置文件
  --json            JSON 输出
  --verbose         详细日志
  --quiet           只输出错误

说明:
  本项目只改 hosts，不安装证书、不接管系统代理、不做流量拦截。
  写入 hosts 需要管理员权限。
`)
}

func versionString() string {
	return version.String()
}

func loadConfig(g *globals) (config.Config, string, error) {
	path := g.configPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return config.Config{}, "", err
		}
		path = p
	}
	cfg, err := config.Load(path)
	return cfg, path, err
}

func fail(err error, code int) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	return code
}

func adminHint(command string) {
	if runtime.GOOS == "windows" {
		fmt.Fprintf(os.Stderr, `
需要管理员权限才能写 hosts。
请右键开始菜单 → “终端(管理员)” / “Windows PowerShell(管理员)”，然后执行：
  %s

提示：只想先看结果，可以加 --dry-run，不需要管理员。
`, command)
		return
	}
	fmt.Fprintf(os.Stderr, "\n需要 root 权限才能写 hosts。请用 sudo 重新执行：\n  sudo %s\n", command)
}
