package idmtool

// IDM 环境探测：路径、SID、HKCU 同步性、架构。对应 IAS.cmd 的准备段。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"idmtool/src/system"
)

// Env 一次探测、全程使用的环境快照。
type Env struct {
	Arch       string // x86 / x64
	SID        string // 当前（或 explorer 所在会话）用户的 SID
	HKCUsync   bool   // HKCU 与 HKU\<sid> 是否同步（同一键）
	IDManPath  string // IDMan.exe 完整路径
	IDMVersion string
	CLSIDRoot  syscall.Handle // HKCU
	CLSIDSub   string         // Software\Classes\[Wow6432Node\]CLSID
	CLSID2Sub  string         // HKU\<sid> 下的同款路径
	HKLMSub    string         // IDM 的 HKLM 键
}

// CLSID 键启发式排除的子键名（IAS 同款）。
var subKeysToExclude = map[string]bool{
	"LocalServer32":   true,
	"InProcServer32":  true,
	"InProcHandler32": true,
}

// Detect 探测环境。失败返回错误。
func Detect() (*Env, error) {
	e := &Env{}

	// 架构（IAS 从 HKLM Session Manager\Environment 读，避开 WOW64 重定向假象）
	if v, ok := system.RegReadString(syscall.HKEY_LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, "PROCESSOR_ARCHITECTURE"); ok {
		e.Arch = v
	}
	if !strings.EqualFold(e.Arch, "x86") {
		e.Arch = "x64"
	}

	// SID
	e.SID = currentUserSID()
	if e.SID == "" {
		return nil, fmt.Errorf("找不到当前用户 SID")
	}
	if !system.RegKeyExists(syscall.HKEY_USERS, e.SID+`\Software`) {
		return nil, fmt.Errorf("HKU\\%s\\Software 不可访问（SID 无效）", e.SID)
	}

	// HKCU 同步性：写 HKCU\IAS_TEST 再查 HKU\<sid>\IAS_TEST
	system.RegDeleteValue(syscall.HKEY_CURRENT_USER, "IAS_TEST", "")
	system.RegDeleteValue(syscall.HKEY_USERS, e.SID+`\IAS_TEST`, "")
	_ = system.RegWriteString(syscall.HKEY_CURRENT_USER, "IAS_TEST", "", "1")
	_, sync := system.RegReadString(syscall.HKEY_USERS, e.SID+`\IAS_TEST`, "")
	e.HKCUsync = sync
	system.RegDeleteValue(syscall.HKEY_CURRENT_USER, "IAS_TEST", "")
	system.RegDeleteValue(syscall.HKEY_USERS, e.SID+`\IAS_TEST`, "")

	// 按架构定注册表路径
	if e.Arch == "x86" {
		e.CLSIDSub = `Software\Classes\CLSID`
		e.CLSID2Sub = `Software\Classes\CLSID`
		e.HKLMSub = `Software\Internet Download Manager`
	} else {
		e.CLSIDSub = `Software\Classes\WOW6432Node\CLSID`
		e.CLSID2Sub = `Software\Classes\WOW6432Node\CLSID`
		e.HKLMSub = `SOFTWARE\Wow6432Node\Internet Download Manager`
	}

	// IDMan.exe 路径
	if v, ok := system.RegReadString(syscall.HKEY_USERS, e.SID+`\Software\DownloadManager`, "ExePath"); ok {
		e.IDManPath = v
	}
	if e.IDManPath == "" || !fileExists(e.IDManPath) {
		if e.Arch == "x64" {
			e.IDManPath = filepath.Join(os.Getenv("ProgramFiles(x86)"), "Internet Download Manager", "IDMan.exe")
		} else {
			e.IDManPath = filepath.Join(os.Getenv("ProgramFiles"), "Internet Download Manager", "IDMan.exe")
		}
	}
	if !fileExists(e.IDManPath) {
		return e, fmt.Errorf("未找到 IDMan.exe（IDM 未安装？）")
	}

	// 版本
	if v, ok := system.RegReadString(syscall.HKEY_USERS, e.SID+`\Software\DownloadManager`, "idmvers"); ok {
		e.IDMVersion = v
	}

	return e, nil
}

// CLSIDPaths 返回要扫描的 (root, subPath) 对。
// HKCUsync 时只扫 HKCU，否则加 HKU\<sid>。
type RegPath struct {
	Root syscall.Handle
	Sub  string
}

func (e *Env) CLSIDPaths() []RegPath {
	out := []RegPath{{syscall.HKEY_CURRENT_USER, e.CLSIDSub}}
	if !e.HKCUsync {
		out = append(out, RegPath{syscall.HKEY_USERS, e.SID + `\` + e.CLSID2Sub})
	}
	return out
}

// currentUserSID 取当前进程用户 SID（ConvertSidToStringSidW 直转，不依赖 stdlib 签名）。
func currentUserSID() string {
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return ""
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return ""
	}
	return sidToString(u.User.Sid)
}

var (
	advapi32Sid          = syscall.NewLazyDLL("advapi32.dll")
	procConvSidToString  = advapi32Sid.NewProc("ConvertSidToStringSidW")
	procLocalFree        = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

// sidToString 把 *SID 转成 "S-1-5-..." 字符串。
func sidToString(sid *syscall.SID) string {
	if sid == nil {
		return ""
	}
	var p *uint16
	r, _, _ := procConvSidToString.Call(uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&p)))
	if r == 0 || p == nil {
		return ""
	}
	s := syscall.UTF16ToString(unsafe.Slice(p, wcslen(p)))
	procLocalFree.Call(uintptr(unsafe.Pointer(p)))
	return s
}

func wcslen(p *uint16) int {
	n := 0
	for q := p; *q != 0; q = (*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(q)) + 2)) {
		n++
	}
	return n
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// KillIDMIfRunning 结束正在运行的 idman.exe，返回是否结束过。
// TestMode 下只探测并打印，不真杀进程。
//
// 【注意】只有「一键完成 / 回滚」这类需要独占注册表的重操作才该调用它，
// 而且是用户主动点按钮触发的（他知道会关 IDM）。守护进程绝不能调它。
func KillIDMIfRunning() bool {
	if !system.IDMRunning() {
		return false
	}
	if TestMode {
		Step("  [测试] 跳过结束 idman.exe（真实模式会结束）")
		return true
	}
	system.KillIDM()
	return true
}
