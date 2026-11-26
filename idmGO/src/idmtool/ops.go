package idmtool

// 用户面向的三个操作：
//   OneClick  一键完成（冻结试用 + 装开机自启 + 拉起守护）—— 一次使用，长期有效
//   Rollback  回滚（停守护 + 删自启 + 解锁并删除 CLSID 键 + 清理注册信息）—— 恢复原状
//   AutoFix   单次补锁（诊断/手动，重新补锁被 IDM 重写的 CLSID 键，不常驻）

import (
	"fmt"
	"net"
	"syscall"
	"time"

	"idmtool/src/system"
)

// Result 流程结果。
type Result struct {
	OK      bool
	Summary string
}

// checkInternet 探测 internetdownloadmanager.com 可达（TCP 80）。
func checkInternet() bool {
	c, err := net.DialTimeout("tcp", "internetdownloadmanager.com:80", 5*time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// OneClick 一键完成：重置试用计数 → 让 IDM 重建试用键 → 锁死 → 装自动续期。
//
// 为什么是「重置+冻结」而不是「激活」：
//   - 激活要写随机序列号，IDM 6.42+ 联网校验后判为假冒序列号，弹窗甚至让 IDM 退出；
//   - 冻结不写序列号、不触发序列号校验，是开源社区一致推荐的长期稳定方案；
//   - 若试用已过期（radxcnt 用尽），必须先清掉过期计数让 IDM 重建，再锁死，
//     才能既"救回"又"锁住"。这一步等于把 IDM 的试用状态重置。
func OneClick(e *Env) (*Result, error) {
	if system.IDMRunning() {
		Step("[1/6] IDM 正在运行，先关闭（重置注册表需要独占）...")
		if n := KillIDMIfRunning(); n {
			Step("  已关闭 IDM（如有未完成的下载，IDM 下次启动会续传）")
		}
	} else {
		Step("[1/6] IDM 未运行，跳过")
	}

	Step("[2/6] 备份 CLSID 注册表键（可回滚）...")
	if err := backupCLSID(e); err != nil {
		Step("  （备份失败，继续：%v）", err)
	}

	Step("[3/6] 清理注册信息与试用计数（重置）...")
	DeleteActivationValues(e)
	// 试用已过期时，radxcnt/scansk/tvfrdt 等计数与 CLSID 跟踪键都要清掉，
	// IDM 下次启动才会按新试用重建。
	deleteTrialTracking(e)

	Step("[4/6] 恢复 IDM 浏览器集成开关 ...")
	AddAdvIntKey(e)

	Step("[5/6] 启动 IDM 让它重建试用跟踪键 ...")
	// 建键有延迟，EnsureTrialKeys 的轮询判定偶尔偏严会误报。
	// 这里不直接失败：即使它报 false，只要最终能扫到键，就继续走冻结流程。
	if ok := EnsureTrialKeys(e); !ok {
		Step("  （建键检测偏严，按最终状态复核 ...）")
	}

	Step("[6/6] 锁死试用跟踪键（之后 IDM 无法递减试用天数）...")
	// 复核给 IDM 一点时间写完键（跨天/慢机/新版 IDM 用异步建键都会稍晚出现）。
	// 等到 90 秒：新版 IDM（如 6.43b19）会先写一个「临时 GUID」跟踪键，
	// 随后才生成/切换最终的 Model 键；轮询偏短会漏锁，导致冻结失效。
	findings := ScanClsidKeys(e, false)
	for attempt := 0; len(findings) == 0 && attempt < 180; attempt++ {
		if IsCancelled() {
			break
		}
		if attempt%2 == 0 {
			Step("  等待 IDM 生成试用跟踪键…（%ds）", attempt/2)
		}
		time.Sleep(500 * time.Millisecond)
		findings = ScanClsidKeys(e, false)
	}
	if len(findings) == 0 {
		return nil, fmt.Errorf("未发现 IDM 试用跟踪键，无法冻结")
	}
	locked, failed := LockClsidKeys(e, findings)
	if locked == 0 {
		return nil, fmt.Errorf("锁键全部失败（%d 个）", failed)
	}

	// 启用守护：写配置文件（enabled=true）+ 装开机启动快捷方式 + 立即拉起守护进程。
	taskMsg := ""
	if err := EnableConfig(); err != nil {
		taskMsg = fmt.Sprintf("\n  ⚠ 配置写入失败：%v", err)
	}
	if err := InstallAutoRenewBoot(); err != nil {
		taskMsg += fmt.Sprintf("\n  ⚠ 开机自启安装失败：%v（可再运行一次本工具）", err)
	} else if taskMsg == "" {
		taskMsg = "\n  已启用开机守护：随开机启动、定期检查补锁，IDM 更新后自动自愈。"
	}
	// 立即拉起独立的守护进程（后台常驻、单实例；关掉本窗口也继续跑）。
	if err := LaunchGuard(); err != nil {
		taskMsg += fmt.Sprintf("\n  ⚠ 后台守护启动失败：%v（下次开机会自动再启）", err)
	} else if taskMsg == "" {
		taskMsg = "\n  后台守护已在运行。"
	}

	return &Result{
		OK: true,
		Summary: fmt.Sprintf("完成！IDM 试用已重置并冻结（锁死 %d 个键，失败 %d 个）。\n"+
			"  现在可以直接正常使用 IDM，长期有效，无需再打开本工具。%s\n"+
			"  若要恢复原状，点「回滚」。", locked, failed, taskMsg),
	}, nil
}

// deleteTrialTracking 清理试用计数与 CLSID 试用跟踪键（"重置"的核心）。
// 只清会阻止 IDM 重置试探期的计数；不动其它 CLSID（避免误伤系统 COM 项）。
func deleteTrialTracking(e *Env) {
	// 1) DownloadManager 下的试用计数值
	trialValues := []string{"scansk", "tvfrdt", "radxcnt", "LstCheck", "ptrk_scdt", "LastCheckQU"}
	for _, v := range trialValues {
		_ = systemDeleteValue(e, `Software\DownloadManager`, v)
	}

	// 2) CLSID 下带 IDM 特征的试用跟踪键（Model/Therad），解锁后删除
	findings := ScanClsidKeys(e, true)
	if len(findings) == 0 {
		return
	}
	for _, f := range findings {
		for _, p := range e.CLSIDPaths() {
			sub := p.Sub + `\` + f.GUID
			if TestMode {
				Step("  [测试] 跳过删除试用键 %s", sub)
				continue
			}
			// 先解锁（可能被旧版错误 ACL 锁死），再删
			_ = systemUnlock(p.Root, sub)
			_ = systemDeleteTree(p.Root, sub)
		}
	}
}

// AutoFix 自动续期（计划任务静默调用）：保证试用键存在且被锁死。
//
// 绝不能弹 UAC（计划任务无交互桌面），也绝不能弹 IDM 下载框。
func AutoFix(e *Env) (*Result, error) {
	KillIDMIfRunning()

	lockedCount := countLockedTrialKeys(e)
	unlocked := ScanClsidKeys(e, false)

	if lockedCount > 0 && len(unlocked) == 0 {
		return &Result{OK: true, Summary: fmt.Sprintf("自动续期：%d 个试用键已锁死，无需动作。", lockedCount)}, nil
	}

	// 键不存在了 → 启动 IDM 重建（仅启动，不下载）
	if lockedCount == 0 && len(unlocked) == 0 {
		if !EnsureTrialKeys(e) {
			return &Result{OK: false, Summary: "自动续期：IDM 试用键不存在且无法重建。"}, nil
		}
		unlocked = ScanClsidKeys(e, false)
	}

	locked, failed := LockClsidKeys(e, unlocked)
	return &Result{
		OK:      locked > 0 || failed == 0,
		Summary: fmt.Sprintf("自动续期：补锁 %d 个，失败 %d 个（原有已锁 %d 个）。", locked, failed, lockedCount),
	}, nil
}

// countLockedTrialKeys 数出"打不开（已锁）"的 GUID 键个数。
func countLockedTrialKeys(e *Env) int {
	n := 0
	for _, p := range e.CLSIDPaths() {
		subKeys, err := system.RegSubKeys(p.Root, p.Sub)
		if err != nil {
			continue
		}
		for _, k := range subKeys {
			if !guidRe.MatchString(k) {
				continue
			}
			if !keyReadable(p.Root, p.Sub+`\`+k) {
				n++
			}
		}
	}
	return n
}

// Rollback 回滚：禁用守护（配置文件）+ 停守护进程 + 删自启 + 解锁并删除 CLSID 键 + 清理注册信息。
func Rollback(e *Env) (*Result, error) {
	Step("[1/5] 禁用守护 ...")
	// 先关配置开关：守护下次 tick 就会静默退出，不再补锁
	if err := DisableConfig(); err != nil {
		Step("  （禁用配置失败，继续：%v）", err)
	} else {
		Step("  已禁用守护（config.enabled=false）")
	}
	// 结束正在运行的守护进程（idmguard.exe），避免它在清理过程中又补锁
	if n := StopGuard(); n > 0 {
		Step("  已结束 %d 个后台守护进程", n)
	} else {
		Step("  没有正在运行的守护进程")
	}
	if err := RemoveAutoRenewBoot(); err != nil {
		Step("  （删除自启失败，继续：%v）", err)
	} else {
		Step("  已删除开机自启快捷方式")
	}

	Step("[2/5] 结束 idman.exe ...")
	KillIDMIfRunning()

	Step("[3/5] 清理注册信息与试用计数值 ...")
	DeleteActivationValues(e)

	Step("[4/5] 解锁并删除 CLSID 试用键 ...")
	findings := ScanClsidKeys(e, true)
	if len(findings) == 0 {
		return &Result{OK: true, Summary: "已回滚：没有需要清理的 CLSID 键，IDM 回到原始状态。"}, nil
	}
	deleted, failed := DeleteClsidKeys(e, findings)
	return &Result{
		OK:      deleted > 0,
		Summary: fmt.Sprintf("已回滚：删除 %d 个键，失败 %d 个。IDM 回到原始试用状态。", deleted, failed),
	}, nil
}

// Status 只读查看当前状态。
func Status(e *Env) *Result {
	var b string
	b = fmt.Sprintf("IDM %s | %s | 架构 %s", orDash(e.IDMVersion), e.IDManPath, e.Arch)

	serial, _ := systemReadString(e, "Serial")
	if serial != "" {
		b += fmt.Sprintf("\n  注册信息：%s", serial)
	} else {
		b += "\n  未注册（试用模式，正常）"
	}

	findings := ScanClsidKeys(e, true)
	lockedN := 0
	for _, f := range findings {
		if f.Why == "已锁键" {
			lockedN++
		}
	}
	b += fmt.Sprintf("\n  IDM 试用键：%d 个（已锁死 %d 个）", len(findings), lockedN)
	if lockedN > 0 {
		b += "\n  状态：已冻结（长期有效）"
	} else {
		b += "\n  状态：未冻结（点「一键完成」）"
	}
	if cd := CooldownLeft(); cd > 0 {
		b += fmt.Sprintf("\n  熔断中：连续失败，%v 后再试", cd.Round(time.Minute))
	}
	if DaemonEnabled() {
		b += "\n  开机守护：" + ConfigSummary()
	} else {
		b += "\n  开机守护：未启用"
	}
	return &Result{OK: true, Summary: b}
}

func systemReadString(e *Env, name string) (string, bool) {
	return readStr(e, `Software\DownloadManager`, name)
}

// 小包装，避免 ops.go 直接 import syscall。
func systemDeleteValue(e *Env, base, name string) error {
	if err := system.RegDeleteValue(syscall.HKEY_CURRENT_USER, base, name); err != nil {
		return err
	}
	if !e.HKCUsync {
		_ = system.RegDeleteValue(syscall.HKEY_USERS, e.SID+`\`+base, name)
	}
	return nil
}

func systemUnlock(root syscall.Handle, sub string) error { return system.UnlockKey(root, sub) }

func systemDeleteTree(root syscall.Handle, sub string) error { return system.RegDeleteTree(root, sub) }

func orDash(s string) string {
	if s == "" {
		return "版本未知"
	}
	return s
}
