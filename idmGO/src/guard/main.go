package main

// idmguard.exe —— 纯守护进程（无窗口、无界面）。
//
// 与 idmfrost.exe（界面 + 激活）彻底分开：
//   - 本程序只做一件事：常驻后台，按周期检查 IDM 试用键，被解锁/被删就补锁。
//   - 不弹任何窗口、不提权、不写界面，日志只落 data\log\YYYY-MM-DD.log。
//   - 单实例：已在跑时再启动会直接退出，不会出现两个守护互相打架。
//
// 启动方式：由 idmfrost.exe「一键完成」立即拉起一次 + 写入开机启动文件夹（开机自启）。
//
// 命令行：
//
//	idmguard.exe            常驻守护（默认）
//	idmguard.exe --quiet    静默（等价默认行为）
//	IDMTOOL_TEST=1          自测模式：不写注册表、不锁键、不装任务

import (
	"flag"
	"os"
	"syscall"
	"unsafe"

	"idmtool/src/idmtool"
)

const (
	guardMutexName   = "Global\\idmguard_single_instance_v1"
	errAlreadyExists = 183 // ERROR_ALREADY_EXISTS
)

var (
	kernel32g       = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = kernel32g.NewProc("CreateMutexW")
	procCloseHandle = kernel32g.NewProc("CloseHandle")
)

func main() {
	var (
		_        = flag.Bool("quiet", false, "静默模式（默认即静默，保留兼容）")
		flagTest = flag.Bool("test", false, "自测模式（等同 IDMTOOL_TEST=1）")
	)
	flag.Parse()

	if os.Getenv("IDMTOOL_TEST") == "1" || *flagTest {
		idmtool.TestMode = true
	}

	// 单实例：已有守护在跑 → 直接退出（0），不报错、不弹窗。
	if !acquireSingleInstance() {
		return
	}

	env, err := idmtool.Detect()
	if err != nil {
		// 静默失败：写一行诊断日志后退出（不弹窗、不阻塞开机）。
		idmtool.FileLogger()("✗ 守护启动失败：%v", err)
		os.Exit(1)
	}

	// 刷新配置心跳（LastSeen/ExePath），不改 enabled。
	// 独立守护进程以前漏调这一步，config.last_seen 会停在旧日期，排查时误导。
	idmtool.TouchConfig()

	// 守护主循环（阻塞，直到进程被杀）。日志全程写文件，不碰 stdout/stderr。
	idmtool.Daemon(env, true)
}

// acquireSingleInstance 用命名 Mutex 保证只有一个守护进程。
// 返回 true = 本进程是唯一实例。
func acquireSingleInstance() bool {
	p, err := syscall.UTF16PtrFromString(guardMutexName)
	if err != nil {
		return true
	}
	h, _, callErr := procCreateMutex.Call(0, 1 /* bInitialOwner */, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return true
	}
	if errno, ok := callErr.(syscall.Errno); ok && uint32(errno) == errAlreadyExists {
		procCloseHandle.Call(h)
		return false
	}
	// 主实例：句柄保留到进程退出（进程结束由 OS 自动释放）。
	return true
}
