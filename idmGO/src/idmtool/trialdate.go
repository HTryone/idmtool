package idmtool

// 跨天日期守护：IDM 除了锁死的 CLSID Model 键，还会看
// DownloadManager\LstCheck（上次校验日期，格式 MM/DD/YY）。跨天后它停留在
// 昨天，IDM 在启动约 3 分钟后做延迟校验时会判"该重新验证试用"→ 弹到期窗。
//
// 原守护只看"键锁没锁"，键锁着就当正常，对此完全无感。
// 这里补一个轻量定时器：启动时 + 运行中每分钟检查，跨天就把 LstCheck 刷成当天。
// 成本极低（一次注册表读 + 偶尔一次写），且抢在 IDM 的 3 分钟延迟校验之前。

import (
	"syscall"
	"time"

	"idmtool/src/system"
)

const trialDateSubKey = `Software\DownloadManager`

// RefreshTrialDate 把 LstCheck 刷新为当天；已是当天则不做任何写操作。
// 返回是否实际写入。
func RefreshTrialDate() bool {
	if TestMode {
		return false
	}
	today := time.Now().Format("01/02/06") // MM/DD/YY，与 IDM 自己的格式一致
	cur, _ := system.RegReadString(syscall.HKEY_CURRENT_USER, trialDateSubKey, "LstCheck")
	if cur == today {
		return false
	}
	if err := system.RegWriteString(syscall.HKEY_CURRENT_USER, trialDateSubKey, "LstCheck", today); err != nil {
		FileLogger()("✗ LstCheck 跨天刷新失败：%v", err)
		return false
	}
	FileLogger()("LstCheck 跨天刷新：%s → %s", cur, today)
	return true
}

// StartTrialDateKeeper 启动轻量定时器：每分钟检查一次跨天日期。
// 独立于 6 小时的补锁 tick —— 补锁管"键权限"，这个管"日期状态"，两者正交。
func StartTrialDateKeeper() {
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for range t.C {
			RefreshTrialDate()
		}
	}()
}
