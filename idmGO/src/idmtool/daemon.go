package idmtool

// 常驻守护（方案 B）：随开机自启，周期性检查并补锁试用键。
//
// 为什么不用计划任务：
//   - 计划任务依赖 schtasks 服务 + /TR 里写死的 exe 路径；
//     exe 一改名/移动（如构建出 cli-real.exe）任务就指向空气，静默失效
//     （实测：Last Result = 0x80070002 文件不存在，持续好几天没人知道）。
//   - 常驻进程只需"开机启动文件夹里的一个快捷方式"，路径由快捷方式指向，
//     进程活着就一直看得住；IDM 更新重写了锁，最迟一个检查周期就能补回。
//
// 守护逻辑（每 interval 检查一次）：
//  1. 试用键都锁着 → 什么都不做
//  2. 有未锁的键 → 立即补锁
//  3. 键被删了 → 启动 IDM 重建再锁
//
// 为什么这样能扛住 IDM 更新：
//   IDM 更新是以管理员权限跑的，能解开 Deny ACL 并重写键；但它不能阻止
//   下一轮守护重新锁死。更新只发生一次，守护一直在，所以最多露出一个窗口。

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"idmtool/src/system"
)

func execCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
func hideWindow() *syscall.SysProcAttr                  { return &syscall.SysProcAttr{HideWindow: true} }

const (
	daemonIntervalOK   = 6 * time.Hour   // 正常检查周期
	daemonIntervalFast = 5 * time.Minute // 刚补过锁时加密检查（防更新后立刻又被改）
	shortcutName       = "idmguard 守护.lnk"
	oldShortcutName    = "idmtool 守护.lnk" // 旧版单 exe 时代留下的，回滚时一并清掉
	guardExeName       = "idmguard.exe"   // 独立守护程序文件名（与 idmfrost.exe 同级）
)

// Daemon 常驻主循环（阻塞，直到进程被杀）。由 idmguard.exe 调用。
// 所有日志一律写文件（不碰 stdout/stderr）——避免控制台构建时弹黑窗。
func Daemon(e *Env, quiet bool) {
	SelfHealShortcut()
	// 跨天日期守护：启动即刷一次 + 每分钟复核，抢在 IDM 约 3 分钟后的延迟校验前。
	RefreshTrialDate()
	StartTrialDateKeeper()
	DaemonLoop(e, daemonLogger())
}

// DaemonLoop 守护主循环（阻塞）。由 idmguard.exe 调用，logf 决定日志去向。
//
// 【顺序约束】守护只在"用户已经点过一键完成"之后才工作：
//   - 没装开机守护（快捷方式不存在）→ 直接后台待着，不碰注册表、不启 IDM、不报错；
//   - 装过之后每次 tick 先看标记，标记丢了也不主动重建，只等用户再点一键完成。
//
// 这样避免"先开机自启、后一键完成"这种顺序下守护抢跑、把状态搅乱。
func DaemonLoop(e *Env, logf func(string, ...interface{})) {
	logf("守护启动，检查周期 %v（试用键异常时加密到 %v）", daemonIntervalOK, daemonIntervalFast)

	next := daemonIntervalOK
	if acted := daemonTick(e, logf); acted {
		next = daemonIntervalFast
	}
	timer := time.NewTimer(next)
	defer timer.Stop()

	for {
		<-timer.C
		next = daemonIntervalOK
		if acted := daemonTick(e, logf); acted {
			next = daemonIntervalFast
		}
		timer.Reset(next)
	}
}

// FileLogger 对外暴露的文件日志函数（供 guard / 失败熔断记录）。
func FileLogger() func(string, ...interface{}) { return daemonLogger() }

// 日志保留天数（超出自动删除最旧的）。
const logKeepDays = 5

// daemonLogger 返回写文件的日志函数（不碰 stdout/stderr）。
//
// 日志结构：data\logs\2026-11-25.log （按天一个文件）
// 轮转：每次写之前清理超过 logKeepDays 天的旧日志，只留最近 5 天。
func daemonLogger() func(string, ...interface{}) {
	logDir := filepath.Join(DataDir(), "log")
	_ = os.MkdirAll(logDir, 0o755)

	lastCleanup := time.Time{} // 本轮已清理过的日期，避免每行都扫目录

	return func(f string, a ...interface{}) {
		now := time.Now()
		path := filepath.Join(logDir, now.Format("2006-01-02")+".log")

		fh, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		if p, e := fh.Stat(); e == nil && p.Size() > 4*1024*1024 {
			// 单日文件超 4MB 就不再写（防爆盘，不截断以免丢历史）
			fh.Close()
			return
		}
		fmt.Fprintf(fh, "%s %s\n", now.Format("2006-01-02 15:04:05"), fmt.Sprintf(f, a...))
		fh.Close()

		// 每天第一次写时做一次轮转清理
		if lastCleanup.Format("2006-01-02") != now.Format("2006-01-02") {
			cleanupOldLogs(logDir, now)
			lastCleanup = now
		}
	}
}

// cleanupOldLogs 删除超过 logKeepDays 天的日志文件（按文件名日期判断）。
func cleanupOldLogs(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.AddDate(0, 0, -logKeepDays)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(name, ".log"), time.Local)
		if err != nil {
			continue // 不是日期命名的，别动
		}
		if day.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// daemonTick 执行一次检查，返回是否需要加密下一轮（true=刚动过）。
//
// 【三条硬约束】
//  1. 用户无感：IDM 在跑时绝不杀它、绝不启第二个实例（见 EnsureTrialKeys）；
//     只在"键已丢且 IDM 没开"时才临时启动一次，用完只关自己那个进程。
//  2. 触发条件：只有「键不在了（过期/被更新清掉）」或「键被解锁」才动作；
//     键都好好锁着 → 什么都不做，连 IDM 都不碰。
//  3. 失败不反复：同一时段连续失败达上限 → 记错误日志并停手（熔断），
//     直到跨过冷却期才允许再试，避免空转占资源。
func daemonTick(e *Env, logf func(string, ...interface{})) bool {
	// 前提条件：配置里启用过守护（用户点过「一键完成」）
	if !DaemonEnabled() {
		return false // 静默待着，等用户点一键完成
	}

	// 熔断中：本时段失败太多，直接跳过（不再启 IDM、不再锁键）
	if inFailureCooldown() {
		return false
	}

	// 键都锁着 → 状态正常，什么都不做
	locked := countLockedTrialKeys(e)
	unlocked := ScanClsidKeys(e, false)
	if locked > 0 && len(unlocked) == 0 {
		ResetFailureState() // 状态健康，清零失败计数
		return false
	}

	// 需要行动：要么键丢了，要么有未锁的键
	acted := false

	// 键完全不存在 → 尝试重建
	if locked == 0 && len(unlocked) == 0 {
		if system.IDMRunning() {
			// 用户正在用 IDM：等它自己写键，绝不杀它、不再启一个
			logf("试用键缺失，但 IDM 正在运行；等待其自行写入（不打断用户）")
			return true // 稍后加密检查
		}
		logf("试用键缺失且 IDM 未运行，临时启动 IDM 重建 ...")
		if !EnsureTrialKeys(e) {
			RecordFailure("试用键重建失败")
			logf("✗ 重建失败，已计入失败次数（连续失败将暂停重试）")
			return true
		}
		unlocked = ScanClsidKeys(e, false)
		acted = true
	}

	if len(unlocked) == 0 {
		return acted
	}

	// 有未锁的键 → 补锁（锁键不需要 IDM，用户无感）
	logf("发现 %d 个未锁的试用键，正在补锁 ...", len(unlocked))
	n, failed := LockClsidKeys(e, unlocked)
	if n == 0 && failed > 0 {
		RecordFailure("补锁全部失败")
		logf("✗ 补锁失败 %d 个，已计入失败次数", failed)
	} else {
		ResetFailureState()
		logf("补锁完成：成功 %d，失败 %d", n, failed)
	}
	return true
}

// GuardExePath 返回守护程序（idmguard.exe）完整路径：与当前程序（idmfrost.exe）同级。
// 用户可能把整个文件夹搬走，所以每次都用"当前 exe 所在目录"现场算，不写死。
func GuardExePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), guardExeName)
}

// InstallAutoRenewBoot 在「开机启动文件夹」放一个指向 idmguard.exe 的快捷方式。
//
// 【进程程序与激活程序分离】
//   - 快捷方式指向的是独立的 idmguard.exe，而不是本程序 idmfrost.exe。
//   - 开机自启的永远是那个纯守护进程（无窗口、单实例）。
//
// 为防范"用户把文件夹搬走导致自启失效"，守护每次启动都会自检：
// 若发现快捷方式指向的路径已不存在，就把当前路径写回去（见 SelfHealShortcut）。
func InstallAutoRenewBoot() error {
	if TestMode {
		Step("  [测试] 跳过安装开机守护")
		return nil
	}
	guard := GuardExePath()
	if guard == "" || !fileExists(guard) {
		return fmt.Errorf("未找到守护程序 %s（应放在本程序同目录）", guardExeName)
	}
	// 清理旧版单 exe 时代留下的「--daemon 指向 idmfrost.exe」快捷方式，
	// 否则升级后旧快捷方式会在开机时拉起界面程序。写新快捷方式前先删旧的。
	if startup, e := startupFolder(); e == nil {
		_ = os.Remove(filepath.Join(startup, oldShortcutName))
	}
	return writeStartupShortcut(guard)
}

// guardArgs 返回守护进程的启动参数。
func guardArgs() string { return "--quiet" }

// writeStartupShortcut 把快捷方式指向指定的守护 exe。
func writeStartupShortcut(guardExe string) error {
	startup, err := startupFolder()
	if err != nil {
		return err
	}
	lnk := filepath.Join(startup, shortcutName)

	ps := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');`+
			`$s.TargetPath='%s';$s.Arguments='%s';`+
			`$s.WorkingDirectory='%s';$s.WindowStyle=7;$s.Description='IDMFrost IDM 试用守护';$s.Save()`,
		psQuote(lnk), psQuote(guardExe), psQuote(guardArgs()), psQuote(filepath.Dir(guardExe)),
	)
	if out, err := runPowerShellHidden(ps); err != nil {
		return fmt.Errorf("创建快捷方式失败：%s（%v）", out, err)
	}
	if _, err := os.Stat(lnk); err != nil {
		return fmt.Errorf("快捷方式未生成：%v", err)
	}
	return nil
}

// LaunchGuard 立即后台拉起守护进程（脱离本进程，本程序关掉它也不受影响）。
// 已在运行则什么也不做（守护自身单实例互斥会兜底）。
func LaunchGuard() error {
	if TestMode {
		Step("  [测试] 跳过后台拉起 idmguard.exe")
		return nil
	}
	guard := GuardExePath()
	if guard == "" || !fileExists(guard) {
		return fmt.Errorf("未找到守护程序 %s（应放在本程序同目录）", guardExeName)
	}
	cmd := execCommand(guard, "--quiet")
	cmd.SysProcAttr = hideWindow()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动守护失败：%v", err)
	}
	// 释放句柄：不 Wait，让它独立常驻（单实例互斥保证不会重复）。
	_ = cmd.Process.Release()
	return nil
}

// StopGuard 结束正在运行的守护进程（回滚时用），返回结束的个数。
func StopGuard() int {
	return system.KillProcesses(guardExeName)
}

// SelfHealShortcut 自检开机快捷方式：路径失效就用当前目录的 idmguard.exe 修回。
// 守护每次启动都调用，这样即使用户把整个文件夹改名/移动，只要还能运行一次，
// 下次开机就会用新路径。
func SelfHealShortcut() {
	if TestMode {
		return
	}
	startup, err := startupFolder()
	if err != nil {
		return
	}
	lnk := filepath.Join(startup, shortcutName)
	if _, err := os.Stat(lnk); err != nil {
		return // 没装过，不主动创建（用户没点"一键完成"）
	}
	guard := GuardExePath()
	if guard == "" || !fileExists(guard) {
		return
	}
	// 读快捷方式目标，失效则改为当前 idmguard.exe 路径
	ps := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');`+
			`if(-not (Test-Path $s.TargetPath)){`+
			`$s.TargetPath='%s';$s.Arguments='%s';$s.WorkingDirectory='%s';$s.Save();Write-Output 'healed'}`,
		psQuote(lnk), psQuote(guard), psQuote(guardArgs()), psQuote(filepath.Dir(guard)),
	)
	runPowerShellHidden(ps)
}

// psQuote 转义 PowerShell 单引号字符串。
func psQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

// copyFile 复制文件（覆盖）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	// 先删旧再改名（旧的可能正被运行）
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}

// RemoveAutoRenewBoot 删除开机启动快捷方式（新旧两个名字都清，兼容旧版）。
func RemoveAutoRenewBoot() error {
	if TestMode {
		return nil
	}
	startup, err := startupFolder()
	if err != nil {
		return err
	}
	var firstErr error
	for _, name := range []string{shortcutName, oldShortcutName} {
		lnk := filepath.Join(startup, name)
		if _, err := os.Stat(lnk); err == nil {
			if err := os.Remove(lnk); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// AutoRenewInstalledBoot 检查开机启动快捷方式是否存在。
func AutoRenewInstalledBoot() bool {
	if TestMode {
		return false
	}
	startup, err := startupFolder()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(startup, shortcutName))
	return err == nil
}

// startupFolder 返回当前用户的开机启动文件夹。
func startupFolder() (string, error) {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return "", fmt.Errorf("APPDATA 未设置")
	}
	p := filepath.Join(appdata, `Microsoft\Windows\Start Menu\Programs\Startup`)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("开机启动文件夹不存在：%s", p)
	}
	return p, nil
}

// runPowerShellHidden 以隐藏窗口跑一段 PowerShell。
func runPowerShellHidden(script string) (string, error) {
	cmd := execCommand("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	cmd.SysProcAttr = hideWindow()
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if len(s) > 300 {
		s = s[:300]
	}
	return s, err
}
