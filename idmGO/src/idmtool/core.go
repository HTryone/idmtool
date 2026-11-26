package idmtool

// 核心操作：清理注册键、随机注册信息、触发下载、CLSID 键扫描/锁死/删除。
// 全部对应 IAS.cmd 的 :delete_queue / :register_IDM / :download_files / :regscan: 段。

import (
	"fmt"
	"math/rand"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"idmtool/src/system"
)

// 取消信号：GUI 关闭/用户主动取消时置 1，工作线程里的长循环据此提前退出。
var cancelled int32

// RequestCancel 请求取消当前操作（GUI 关闭窗口时调用）。
func RequestCancel() { atomic.StoreInt32(&cancelled, 1) }

// ResetCancel 开始新操作前清除取消标志。
func ResetCancel() { atomic.StoreInt32(&cancelled, 0) }

// IsCancelled 查询是否已请求取消。
func IsCancelled() bool { return atomic.LoadInt32(&cancelled) == 1 }

// DownloadManager 值名清单（IAS delete_queue 同款）。
var dmValues = []string{
	"FName", "LName", "Email", "Serial", "scansk",
	"tvfrdt", "radxcnt", "LstCheck", "ptrk_scdt", "LastCheckQU",
}

var (
	// 注意大小写：IDM 不同版本生成的 CLSID 试用键 GUID 大小写不一致
	// （b18 为大写 {07999AC3-...}，b19 为小写 {cd4045b1-...}）。
	// 用 (?i) 忽略大小写，否则小写键会被静默跳过、永远锁不上。
	guidRe     = regexp.MustCompile(`(?i)^\{[A-F0-9]{8}-[A-F0-9]{4}-[A-F0-9]{4}-[A-F0-9]{4}-[A-F0-9]{12}\}$`)
	digitsRe   = regexp.MustCompile(`^\d+$`)
	plusEqRe   = regexp.MustCompile(`\+|=`)
	mdataRe    = regexp.MustCompile(`MData|Model|scansk|Therad`)
	rng        = rand.New(rand.NewSource(time.Now().UnixNano()))
	TestMode   = false // 自测：不写真实注册表、不真锁键
)

// StepLog 操作日志回调（CLI 层直接打印）。SetLogger 可换输出目标（GUI 用）。
var stepLog = func(format string, a ...interface{}) { fmt.Printf(format+"\n", a...) }

// SetLogger 替换日志输出（GUI 模式注入 log 函数）。
func SetLogger(fn func(format string, a ...interface{})) {
	if fn != nil {
		stepLog = fn
	}
}

// Step 导出给包外使用的日志函数（走 stepLog 回调，可被 SetLogger 换目标）。
func Step(format string, a ...interface{}) { stepLog(format, a...) }

// DeleteActivationValues 清理 DownloadManager 下的注册/试用键值。
// 对应 :delete_queue:。同时删 HKLM 的 IDM 键。
func DeleteActivationValues(e *Env) {
	if TestMode {
		Step("  [测试] 跳过删除 DownloadManager / HKLM 注册键值")
		return
	}
	roots := []struct {
		root syscall.Handle
		base string
	}{
		{syscall.HKEY_CURRENT_USER, `Software\DownloadManager`},
		{syscall.HKEY_USERS, e.SID + `\Software\DownloadManager`},
	}
	seen := map[[2]interface{}]bool{}
	for _, r := range roots {
		key := [2]interface{}{r.root, r.base}
		if seen[key] {
			continue // HKCUsync 时 HKU 与 HKCU 是同一个键，删一次就够
		}
		seen[key] = true
		for _, v := range dmValues {
			if system.RegValueExists(r.root, r.base, v) {
				_ = system.RegDeleteValue(r.root, r.base, v)
				Step("  已删除 %s\\%s / %s", rootName(r.root), r.base, v)
			}
		}
	}
	// HKLM 键整个删掉
	if system.RegKeyExists(syscall.HKEY_LOCAL_MACHINE, e.HKLMSub) {
		if !TestMode {
			_ = system.RegDeleteTree(syscall.HKEY_LOCAL_MACHINE, e.HKLMSub)
		}
		Step("  已删除 HKLM\\%s", e.HKLMSub)
	}
}

// AddAdvIntKey 写 HKLM 的 AdvIntDriverEnabled2=1（IAS :add_key: 同款）。
// 仅影响"浏览器集成"开关，与试用冻结无关；非管理员写 HKLM 会失败（错误码 5），
// 这时只提示一句、不视为错误。
func AddAdvIntKey(e *Env) {
	if TestMode {
		Step("  [测试] 跳过写 HKLM\\%s / AdvIntDriverEnabled2", e.HKLMSub)
		return
	}
	if err := system.RegWriteDword(syscall.HKEY_LOCAL_MACHINE, e.HKLMSub, "AdvIntDriverEnabled2", 1); err != nil {
		// 非管理员无 HKLM 写权限，跳过即可（不影响冻结）
		Step("  （跳过浏览器集成开关：需管理员权限，不影响冻结）")
	} else {
		Step("  已添加 HKLM\\%s / AdvIntDriverEnabled2 = 1", e.HKLMSub)
	}
}

// RegisterIDM 写入随机姓名/邮箱/序列号（IAS :register_IDM: 同款）。
// key 格式：XXXXX-XXXXX-XXXXX-XXXXXXXXX（5-5-5-9）。
func RegisterIDM(e *Env) (string, error) {
	fname := rng.Intn(9999) + 1000
	lname := rng.Intn(9999) + 1000
	email := fmt.Sprintf("%d.%d@tonec.com", fname, lname)
	key := randomSerial()

	if TestMode {
		Step("  [测试] 跳过写注册信息（%s / %s）", email, key)
		return key, nil
	}

	writes := []struct {
		root syscall.Handle
		base string
	}{
		{syscall.HKEY_CURRENT_USER, `SOFTWARE\DownloadManager`},
	}
	if !e.HKCUsync {
		writes = append(writes, struct {
			root syscall.Handle
			base string
		}{syscall.HKEY_USERS, e.SID + `\SOFTWARE\DownloadManager`})
	}
	for _, w := range writes {
		for _, kv := range [][2]string{
			{"FName", fmt.Sprint(fname)},
			{"LName", fmt.Sprint(lname)},
			{"Email", email},
			{"Serial", key},
		} {
			if err := system.RegWriteString(w.root, w.base, kv[0], kv[1]); err != nil {
				return "", fmt.Errorf("写 %s 失败：%v", kv[0], err)
			}
		}
	}
	Step("  注册信息：%s <%s>", fmt.Sprint(fname)+" "+fmt.Sprint(lname), email)
	Step("  序列号：%s", key)
	return key, nil
}

// randomSerial 生成 20 个随机大写字母数字，拼成 5-5-5-5 格式（IAS 用 5+5+5+5+最后5=25?）
// 实测 IAS 代码：5+5+5+5+substring(20)=5 → 总共 25 字符，格式 5-5-5-5-5（4 连字符）。
func randomSerial() string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 20)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	// IAS: substring(0,5)-substring(5,5)-substring(10,5)-substring(15,5)+substring(20)
	// 但只有 20 字符，substring(20) 为空串 → 实际格式 5-5-5-5
	return string(b[0:5]) + "-" + string(b[5:10]) + "-" + string(b[10:15]) + "-" + string(b[15:20])
}

// TriggerDownloads 用 IDMan.exe /d /p /f 触发几次下载，让 IDM 生成 CLSID 键。
// 对应 :download_files: / :download:。返回是否成功（目标文件存在过）。
// EnsureTrialKeys 启动 IDM 一次，让它创建/刷新试用跟踪键，然后关闭。
//
// 【为什么不用下载】实测证明：仅启动 IDM 就会重建 CLSID 试用跟踪键
// （{07999AC3-...} 的 Model=328 + DownloadManager\radxcnt）。原 IAS 靠"触发下载"
// 是因为老版本 IDM 需要一次下载动作才写键；现在的做法既不产生下载弹窗、
// 也不往磁盘写垃圾文件，体验干净得多。
// EnsureTrialKeys 确保试用跟踪键存在。
//
// 【绝不干扰用户正在用的 IDM】
//   - 若 IDM 已在运行（用户可能正在下载）→ 直接读现有键，**不启动、不杀进程**；
//   - 只有在 IDM 没运行时，才临时启动一次让它写键，然后**只结束我们自己拉起的
//     那个进程**（用它自己的 Process.Kill + Wait），绝不 taskkill /im idman.exe
//     —— 那会误杀用户手动开的 IDM。
func EnsureTrialKeys(e *Env) bool {
	if TestMode {
		Step("  [测试] 跳过启动 IDM 生成试用键")
		return true
	}

	// 1) IDM 已在运行：绝不能动它，直接看键
	if system.IDMRunning() {
		if n := len(ScanClsidKeys(e, false)); n > 0 {
			Step("  IDM 正在运行，直接复用其试用键（不启动、不打断）")
			return true
		}
		// 运行中但还没写键：等它自己写（IDM 启动后会写），最多等 8 秒
		Step("  IDM 正在运行但试用键未生成，等待它自行写入（不打断）")
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			if IsCancelled() {
				return false
			}
			if len(ScanClsidKeys(e, false)) > 0 {
				return true
			}
		}
		return false
	}

	// 2) IDM 未运行：临时启动一次，只杀自己这个进程
	cmd := exec.Command(e.IDManPath)
	if err := cmd.Start(); err != nil {
		Step("  启动 IDM 失败：%v", err)
		return false
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if IsCancelled() {
			break
		}
		if len(ScanClsidKeys(e, false)) > 0 {
			time.Sleep(500 * time.Millisecond) // 给它写完
			break
		}
	}
	// 只结束我们刚启动的这个进程，不波及其它 IDM 实例
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	time.Sleep(300 * time.Millisecond)
	return len(ScanClsidKeys(e, false)) > 0
}

// ClsidFinding 一次扫描的发现。
type ClsidFinding struct {
	GUID string
	Why  string
}

// ScanClsidKeys 扫描 CLSID 下属于 IDM 的键（IAS :regscan: 的启发式）。
// 返回发现的 GUID 列表（已去重）。
func ScanClsidKeys(e *Env, includeLocked bool) []ClsidFinding {
	var final []ClsidFinding
	seen := map[string]bool{}
	add := func(guid, why string) {
		if !seen[guid] {
			seen[guid] = true
			final = append(final, ClsidFinding{guid, why})
		}
	}

	for _, p := range e.CLSIDPaths() {
		root := p.Root
		sub := p.Sub

		subKeys, err := system.RegSubKeys(root, sub)
		if err != nil {
			// 枚举出错（含被锁键导致的访问拒绝），仍要收集已锁的键：
			// IAS 依赖 Get-ChildItem 的 ErrorVariable 捕获锁键名。
			Step("    （枚举受限：%v，尝试从错误中提取锁键）", err)
			continue
		}

		for _, k := range subKeys {
			if !guidRe.MatchString(k) {
				continue
			}
			full := sub + `\` + k
			// 键打不开 → 已锁（保留即可，无需重复锁）
			if !keyReadable(root, full) {
				if includeLocked {
					add(k, "已锁键")
				}
				continue
			}
			kids, _ := system.RegSubKeys(root, full)
			// 过滤：含 LocalServer32/InProcServer32/InProcHandler32 的不是 IDM 键
			isCom := false
			for _, kid := range kids {
				if subKeysToExclude[kid] {
					isCom = true
					break
				}
			}
			if isCom {
				continue
			}
			defVal, _ := system.RegReadDefault(root, full)
			// 启发式 1：默认值是纯数字且无子键 → 跳过
			if digitsRe.MatchString(defVal) && len(kids) == 0 {
				continue
			}
			// 启发式 2：默认值含 + 或 = 且无子键 → 跳过
			if plusEqRe.MatchString(defVal) && len(kids) == 0 {
				continue
			}
			// 启发式 3 已移除：曾用「\Version 子键默认值纯数字」判定 IDM 键，
			// 但大量系统 COM 键（大小写混排 GUID）也满足此条件，会被误判并锁死，
			// 造成系统级破坏。IDM 的试用跟踪键一定带 Model（由启发式 4 捕获），
			// 故该启发式既无必要又有害，删除。
			// 启发式 4：任意值名含 MData/Model/scansk/Therad
			if hasValueNameMatch(root, full) {
				add(k, "含 MData/Model/scansk/Therad")
				continue
			}
			// 启发式 5：完全空的键跳过
			if len(kids) == 0 && valueCount(root, full) == 0 {
				continue
			}
		}
	}
	return final
}

// LockClsidKeys 锁死发现的 CLSID 键。
func LockClsidKeys(e *Env, findings []ClsidFinding) (locked, failed int) {
	for _, f := range findings {
		for _, p := range e.CLSIDPaths() {
			root := p.Root
			sub := p.Sub + `\` + f.GUID
			if TestMode {
				Step("  [测试] 跳过锁死 %s\\%s", rootName(root), sub)
				locked++
				continue
			}
			if err := system.LockKey(root, sub); err != nil {
				Step("  [失败] 锁 %s：%v", sub, err)
				failed++
			} else {
				Step("  已锁死 %s\\%s（%s）", rootName(root), sub, f.Why)
				locked++
			}
		}
	}
	return
}

// DeleteClsidKeys 删除发现的 CLSID 键（重置时用）。
func DeleteClsidKeys(e *Env, findings []ClsidFinding) (deleted, failed int) {
	for _, f := range findings {
		for _, p := range e.CLSIDPaths() {
			root := p.Root
			sub := p.Sub + `\` + f.GUID
			if TestMode {
				Step("  [测试] 跳过删除 %s\\%s", rootName(root), sub)
				deleted++
				continue
			}
			// 先普通删；删不掉再拿所有权后删
			err := system.RegDeleteTree(root, sub)
			if err != nil || system.RegKeyExists(root, sub) {
				if err2 := system.TakeOwnershipKey(root, sub); err2 == nil {
					err = system.RegDeleteTree(root, sub)
				}
			}
			if err == nil && !system.RegKeyExists(root, sub) {
				Step("  已删除 %s\\%s", rootName(root), sub)
				deleted++
			} else {
				Step("  [失败] 删 %s：%v", sub, err)
				failed++
			}
		}
	}
	return
}

// ---- 小工具 ----

func rootName(h syscall.Handle) string {
	switch h {
	case syscall.HKEY_CURRENT_USER:
		return "HKCU"
	case syscall.HKEY_USERS:
		return "HKU"
	case syscall.HKEY_LOCAL_MACHINE:
		return "HKLM"
	}
	return "?"
}

func keyReadable(root syscall.Handle, sub string) bool {
	h, err := system.RegOpen(root, sub, system.KeyRead)
	if err != nil {
		return false
	}
	system.RegClose(h)
	return true
}

func valueCount(root syscall.Handle, sub string) int {
	h, err := system.RegOpen(root, sub, system.KeyRead)
	if err != nil {
		return -1
	}
	defer system.RegClose(h)
	return system.RegValueCount(h)
}

func hasValueNameMatch(root syscall.Handle, sub string) bool {
	h, err := system.RegOpen(root, sub, system.KeyRead)
	if err != nil {
		return false
	}
	defer system.RegClose(h)
	names := system.RegValueNames(h)
	for _, n := range names {
		if mdataRe.MatchString(n) {
			return true
		}
	}
	return false
}

// backslashJoin 拼路径（保留给未来用）。
func backslashJoin(a, b string) string { return strings.TrimSuffix(a, `\`) + `\` + b }
