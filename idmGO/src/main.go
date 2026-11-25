package main

// 入口：GUI（默认，无 CMD 窗口）+ 命令行开关。
//
// 本程序 idmfrost.exe 只负责【界面 + 激活】，不再承担守护职责。
// 守护是独立程序 idmguard.exe（同目录），由「一键完成」拉起并写入开机自启。
//
//	idmfrost.exe                     GUI：一键完成 / 回滚 / 退出（推荐）
//	idmfrost.exe --oneclick          CLI 一键完成（冻结试用 + 拉起守护 + 装开机自启）
//	idmfrost.exe --rollback          CLI 回滚（停守护 + 删自启 + 解锁删键）
//	idmfrost.exe --autofix           单次补锁（诊断/手动，不常驻）
//	idmfrost.exe --status            CLI 只读查看状态
//	idmfrost.exe --no-elevate        不自动提权（调试）
//	IDMTOOL_TEST=1                   自测模式：不写注册表、不锁键、不装任务

import (
	"flag"
	"fmt"
	"os"

	"idmtool/src/console"
	"idmtool/src/idmtool"
	"idmtool/src/system"
	"idmtool/src/ui"
)

func main() {
	var (
		flagOneClick = flag.Bool("oneclick", false, "一键完成（冻结试用 + 拉起守护 + 装开机自启）")
		flagRollback = flag.Bool("rollback", false, "回滚到原始状态")
		flagAutoFix  = flag.Bool("autofix", false, "单次补锁（调试/手动）")
		flagQuiet    = flag.Bool("quiet", false, "静默模式（不输出菜单/不暂停）")
		flagStatus   = flag.Bool("status", false, "只读查看状态")
		flagNoElev   = flag.Bool("no-elevate", false, "不自动提权（调试）")
		flagNoColor  = flag.Bool("no-color", false, "关闭彩色输出")
	)
	flag.Usage = usage
	flag.Parse()

	console.SetColor(!*flagNoColor)
	if os.Getenv("IDMTOOL_TEST") == "1" {
		idmtool.TestMode = true
	}

	isCLI := *flagOneClick || *flagRollback || *flagAutoFix || *flagStatus

	// 需要管理员：一键完成（写 HKLM）、回滚（解旧锁）。
	// 绝不能提权：--autofix（可能由无交互环境调用）。
	if !*flagNoElev && !*flagAutoFix && !system.IsElevated() {
		if err := system.RelaunchElevated(); err != nil {
			if isCLI {
				console.Fail(err.Error())
				if !*flagQuiet {
					console.Pause()
				}
			} else {
				system.ShowErrorBox("IDMFrost", err.Error()+"\n\n请右键本程序选择「以管理员身份运行」。")
			}
			os.Exit(1)
		}
		if isCLI && !*flagQuiet {
			console.Info("已请求管理员权限，请在新窗口继续操作…")
		}
		return
	}

	env, err := idmtool.Detect()
	if err != nil {
		if isCLI {
			console.Fail(err.Error())
			if !*flagQuiet {
				console.Pause()
			}
		} else {
			system.ShowErrorBox("IDMFrost", err.Error())
		}
		os.Exit(1)
	}

	// 【启动后就写】刷新配置文件心跳（不改 enabled，只更新 LastSeen/ExePath）。
	// 这样配置文件永远存在，守护读它时不会因"文件缺失"误判。
	idmtool.TouchConfig()

	switch {
	case *flagAutoFix:
		idmtool.SetLogger(func(f string, a ...interface{}) {
			if !*flagQuiet {
				console.Info(fmt.Sprintf(f, a...))
			}
		})
		r, err := idmtool.AutoFix(env)
		if err != nil {
			os.Exit(1)
		}
		if !r.OK {
			os.Exit(1)
		}
		return
	case *flagStatus:
		idmtool.SetLogger(func(f string, a ...interface{}) { console.Info(fmt.Sprintf(f, a...)) })
		r := idmtool.Status(env)
		console.Banner()
		for _, line := range splitLines(r.Summary) {
			console.Info(line)
		}
	case *flagOneClick:
		idmtool.SetLogger(func(f string, a ...interface{}) { console.Info(fmt.Sprintf(f, a...)) })
		runCLIOp(env, "一键完成", func() (*idmtool.Result, error) { return idmtool.OneClick(env) }, *flagQuiet)
	case *flagRollback:
		idmtool.SetLogger(func(f string, a ...interface{}) { console.Info(fmt.Sprintf(f, a...)) })
		runCLIOp(env, "回滚", func() (*idmtool.Result, error) { return idmtool.Rollback(env) }, *flagQuiet)
	default:
		runGUI(env)
	}
}

func runGUI(env *idmtool.Env) {
	// 单实例：已有实例时把它的窗口置前后自己退出。
	if !ui.AcquireSingleInstance() {
		return
	}

	// 【进程程序与激活程序分离】GUI 不再内置守护。
	// 「一键完成」会拉起独立的 idmguard.exe（见 ops.OneClick），关掉本窗口不影响守护。
	cb := ui.Callbacks{
		OnOneClick: func(log func(string)) {
			doOp(env, log, "一键完成", func() (*idmtool.Result, error) { return idmtool.OneClick(env) })
		},
		OnRollback: func(log func(string)) {
			doOp(env, log, "回滚", func() (*idmtool.Result, error) { return idmtool.Rollback(env) })
		},
	}
	ui.Run(cb, "IDMFrost · IDM 试用守护")
}

func doOp(env *idmtool.Env, log func(string), name string, fn func() (*idmtool.Result, error)) {
	idmtool.SetLogger(wrapLog(log))
	r, err := fn()
	if err != nil {
		log("✗ " + name + "失败：" + err.Error())
		return
	}
	for _, line := range splitLines(r.Summary) {
		log(line)
	}
}

func runCLIOp(env *idmtool.Env, name string, fn func() (*idmtool.Result, error), quiet bool) {
	if !quiet {
		console.Banner()
	}
	r, err := fn()
	if err != nil {
		console.Fail(name + "失败：" + err.Error())
	} else if r.OK {
		console.Ok(r.Summary)
	} else {
		console.Warn(r.Summary)
	}
	if !quiet {
		console.Pause()
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// wrapLog 把 GUI 的单字符串 log 适配成格式化 logger。
func wrapLog(log func(string)) func(string, ...interface{}) {
	return func(f string, a ...interface{}) {
		if len(a) == 0 {
			log(f)
		} else {
			log(fmt.Sprintf(f, a...))
		}
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "idmfrost — IDM 试用冻结（界面 + 激活）")
	fmt.Fprintln(os.Stderr, "用法：idmfrost.exe [--oneclick|--rollback|--status|--autofix] [--no-elevate]")
	fmt.Fprintln(os.Stderr, "说明：常驻守护由同目录的 idmguard.exe 负责，本程序不再承担守护。")
	flag.PrintDefaults()
}
