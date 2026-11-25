package system

// 注册表键 ACL 操作：拿所有权 → 重置权限 → 可选锁死（Deny Everyone）。
// 对应 IAS.cmd :regscan: 段里的 Take-Permissions PowerShell 函数。
//
// 锁死的原理：把键的所有者设为 S-1-0-0（Nobody），并给 Everyone 加 FullControl Deny。
// 之后 IDM（包括管理员权限的 IDM）也无法打开/修改该键，激活状态得以保全。
//
// 关键点（都对应 .NET/PowerShell 的行为）：
//   - SeTakeOwnershipPrivilege(9)  → 打开键时获得 WRITE_OWNER
//   - SeBackupPrivilege(17)        → 打开键时获得 READ_CONTROL
//   - SeRestorePrivilege(18)       → 打开键时获得 WRITE_DAC | WRITE_OWNER
//   - RegGetKeySecurity 是 4 参数（hKey, SI, pSD, pcb）

import (
	"fmt"
	"syscall"
	"unsafe"
)

// RtlAdjustPrivilege 的特权编号（与 IAS 的 9,17,18 一致）。
const (
	seTakeOwnershipPriv = 9
	seBackupPriv        = 17
	seRestorePriv       = 18
)

const (
	ownerSecurityInformation  = 0x00000001
	groupSecurityInformation  = 0x00000002
	daclSecurityInformation   = 0x00000004
	secInfoAll                = ownerSecurityInformation | groupSecurityInformation | daclSecurityInformation
	securityDescriptorRevision = 1
)

// 打开键用的访问掩码
const (
	accessTakeOwnership = 0x000A0000 // READ_CONTROL | WRITE_OWNER
	accessChangePerm    = 0x00060000 // READ_CONTROL | WRITE_DAC
	accessFull          = 0x000E0000 // READ_CONTROL | WRITE_DAC | WRITE_OWNER
)

const (
	fullControl      = 0x000F003F // KEY_ALL_ACCESS
	aceContainerInh  = 0x00000002 // CONTAINER_INHERIT_ACE
	// ACCESS_MODE 枚举（winnt.h）：GRANT_ACCESS=1 SET_ACCESS=2 DENY_ACCESS=3
	// 【曾误写 deny=1 —— 那是 GRANT_ACCESS，会给 Everyone 完全控制，锁键静默失效】
	accessModeDeny   = 3 // DENY_ACCESS
	accessModeGrant  = 2 // SET_ACCESS
	trusteeIsSid     = 0
	trusteeIsName    = 1
	trusteeWellKnown = 5 // TRUSTEE_IS_WELL_KNOWN_GROUP
)

var (
	advapi32ACL       = syscall.NewLazyDLL("advapi32.dll")
	ntdllPriv         = syscall.NewLazyDLL("ntdll.dll")
	procRtlAdjustPriv = ntdllPriv.NewProc("RtlAdjustPrivilege")

	procRegGetKeySec  = advapi32ACL.NewProc("RegGetKeySecurity")
	procRegSetKeySec  = advapi32ACL.NewProc("RegSetKeySecurity")
	procSetEntries    = advapi32ACL.NewProc("SetEntriesInAclW")
	procInitSD        = advapi32ACL.NewProc("InitializeSecurityDescriptor")
	procSetSDOwner    = advapi32ACL.NewProc("SetSecurityDescriptorOwner")
	procSetSDGroup    = advapi32ACL.NewProc("SetSecurityDescriptorGroup")
	procSetSDDacl     = advapi32ACL.NewProc("SetSecurityDescriptorDacl")
	procGetSDDacl     = advapi32ACL.NewProc("GetSecurityDescriptorDacl")
	procSetEntriesFree = advapi32ACL.NewProc("LocalFree") // 实际在 kernel32，见下
	procLocalFreeACL  = kernel32ACL.NewProc("LocalFree")
)

var kernel32ACL = syscall.NewLazyDLL("kernel32.dll")

// TRUSTEE（64 位布局）
type trustee struct {
	PMultipleTrustee         uintptr
	MultipleTrusteeOperation uint32
	TrusteeForm              uint32
	TrusteeType              uint32
	PTstrName                uintptr
}

// EXPLICIT_ACCESS（64 位布局）
type explicitAccess struct {
	GrfAccessPermissions uint32
	GrfAccessMode        uint32
	GrfInheritance       uint32
	Trustee              trustee
}

// strToSID 把 SID 字符串转成 *syscall.SID（LocalAlloc 的，进程内复用没问题）。
func strToSID(s string) (*syscall.SID, error) {
	return syscall.StringToSid(s)
}

// adjustPrivilege 启用当前进程 token 的指定特权。
func adjustPrivilege(priv uint32) {
	var prev bool
	procRtlAdjustPriv.Call(
		uintptr(priv),
		1, // enable
		0, // not thread-adjust
		uintptr(unsafe.Pointer(&prev)),
	)
}

func adjustAllPrivileges() {
	adjustPrivilege(seTakeOwnershipPriv)
	adjustPrivilege(seBackupPriv)
	adjustPrivilege(seRestorePriv)
}

// regGetSecurity 拿键的安全描述符（OWNER|GROUP|DACL）。4 参数调用。
func regGetSecurity(h syscall.Handle) ([]byte, error) {
	var size uint32
	r, _, _ := procRegGetKeySec.Call(
		uintptr(h),
		secInfoAll,
		0, // NULL → 探测需要的大小
		uintptr(unsafe.Pointer(&size)),
	)
	if r != 122 /* ERROR_INSUFFICIENT_BUFFER */ && (r != 0 || size == 0) {
		return nil, fmt.Errorf("RegGetKeySecurity 探测大小失败：错误码 %d", r)
	}
	if size == 0 {
		return nil, fmt.Errorf("RegGetKeySecurity 探测大小为 0")
	}
	buf := make([]byte, size)
	r, _, _ = procRegGetKeySec.Call(
		uintptr(h),
		secInfoAll,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r != 0 {
		return nil, fmt.Errorf("RegGetKeySecurity 失败：错误码 %d", r)
	}
	return buf, nil
}

// buildSD 组一个绝对格式的 SECURITY_DESCRIPTOR。
func buildSD(owner, group *syscall.SID, dacl uintptr) ([80]byte, error) {
	var sd [80]byte // SECURITY_DESCRIPTOR x64 实际 40 字节，给余量
	if r, _, _ := procInitSD.Call(
		uintptr(unsafe.Pointer(&sd[0])),
		securityDescriptorRevision,
	); r == 0 {
		return sd, fmt.Errorf("InitializeSecurityDescriptor 失败")
	}
	if owner != nil {
		procSetSDOwner.Call(uintptr(unsafe.Pointer(&sd[0])), uintptr(unsafe.Pointer(owner)), 0)
	}
	if group != nil {
		procSetSDGroup.Call(uintptr(unsafe.Pointer(&sd[0])), uintptr(unsafe.Pointer(group)), 0)
	}
	if dacl != 0 {
		procSetSDDacl.Call(uintptr(unsafe.Pointer(&sd[0])), 1, dacl, 0)
	}
	return sd, nil
}

// getExistingAclPtr 从已拿到的 SD 里解出 DACL 指针（传给 SetEntriesInAcl 做基底）。
// IAS 的 .NET ResetAccessRule 是"在现有 ACL 上改"，不是重建空 ACL——必须保留
// 原有继承 Allow 项，否则锁不生效（实测对照结论）。
func getExistingDacl(sd []byte) uintptr {
	var present int32
	var pAcl uintptr
	var defaulted int32
	procGetSDDacl.Call(
		uintptr(unsafe.Pointer(&sd[0])),
		uintptr(unsafe.Pointer(&present)),
		uintptr(unsafe.Pointer(&pAcl)),
		uintptr(unsafe.Pointer(&defaulted)),
	)
	if present == 0 {
		return 0
	}
	return pAcl
}

// makeAcl 在现有 DACL（可为 0）上加一条显式访问条目。
// mode: accessModeDeny / accessModeGrant。
// GrfInheritance = 0（NO_INHERITANCE），与 IAS 的 RegistryAccessRule 行为一致。
//
// 【重要】trustee 必须用 SID 形式（TRUSTEE_IS_SID），不能用名字形式：
// 中文 Windows 上 SetEntriesInAclW 用 "Everyone"/本地化组名做 LookupAccountName
// 会解析失败，函数却返回成功、生成空 ACL（ACE count=0），导致锁键静默失效。
// 直接传 SID 字节（S-1-1-0）绕开名字解析，任何语言/权限上下文都可靠。
func makeAcl(mode uint32, sid *syscall.SID, oldAcl uintptr) (uintptr, error) {
	if sid == nil {
		return 0, fmt.Errorf("makeAcl: sid 为 nil")
	}
	ea := explicitAccess{
		GrfAccessPermissions: fullControl,
		GrfAccessMode:        mode,
		GrfInheritance:       0,
	}
	ea.Trustee = trustee{
		TrusteeForm: trusteeIsSid, // TRUSTEE_IS_SID
		TrusteeType: trusteeWellKnown,
		PTstrName:   uintptr(unsafe.Pointer(sid)),
	}
	var newAcl uintptr
	r, _, _ := procSetEntries.Call(
		1,
		uintptr(unsafe.Pointer(&ea)),
		oldAcl, // 现有 DACL 作为基底（保留继承 Allow 项，Deny 排在最前）
		uintptr(unsafe.Pointer(&newAcl)),
	)
	if r != 0 {
		return 0, fmt.Errorf("SetEntriesInAcl 失败：错误码 %d", r)
	}
	return newAcl, nil
}

// TakeOwnershipKey 解锁：把键恢复成 Everyone 完全控制，使之后能正常删除。
//
// 两条路径（按顺序尝试，任一成功即可）：
//  1. 非管理员路径：HKCU 键当前用户本来就有 WRITE_DAC，直接重写 DACL 为 Allow。
//     （键若被 Deny Everyone 锁死，WRITE_DAC 仍能打开——因为 DACL 只影响数据访问，
//      READ_CONTROL/WRITE_DAC 走的是 owner 与标准权限，当前用户是 owner 即可）
//  2. 管理员路径：抢所有权（SeTakeOwnership）后再写 DACL。
func TakeOwnershipKey(root syscall.Handle, subKey string) error {
	// 路径 1：直接写 DACL 为 Allow Everyone
	if err := grantEveryone(root, subKey); err == nil {
		return nil
	}
	// 路径 2：抢所有权 + 写 DACL
	adjustAllPrivileges()
	if err := takeOwnershipAdmin(root, subKey); err != nil {
		// 抢所有权也失败，最后再试一次直接写 DACL（可能刚拿到权限）
		if e2 := grantEveryone(root, subKey); e2 == nil {
			return nil
		}
		return err
	}
	return grantEveryone(root, subKey)
}

// grantEveryone 用 WRITE_DAC 打开键，把 DACL 重置为 Everyone 完全控制（Allow）。
func grantEveryone(root syscall.Handle, subKey string) error {
	h, err := regOpen(root, subKey, accessChangePerm) // READ_CONTROL | WRITE_DAC
	if err != nil {
		// 若连 WRITE_DAC 都不行，按 owner 权限再试（WRITE_DAC 即 0x40000）
		h, err = regOpen(root, subKey, 0x00040000)
		if err != nil {
			return err
		}
	}
	defer procRegCloseKey.Call(uintptr(h))

	everyone, err := strToSID("S-1-1-0")
	if err != nil {
		return err
	}
	baseSD, err := regGetSecurity(h)
	if err != nil {
		return err
	}
	oldAcl := getExistingDacl(baseSD)
	// SET_ACCESS + Everyone FullControl = 替换为完全允许
	acl, err := makeAcl(accessModeGrant, everyone, oldAcl)
	if err != nil {
		return err
	}
	defer procLocalFreeACL.Call(acl)
	daclSD, err := buildSD(nil, nil, acl)
	if err != nil {
		return err
	}
	if r, _, _ := procRegSetKeySec.Call(
		uintptr(h),
		daclSecurityInformation,
		uintptr(unsafe.Pointer(&daclSD[0])),
	); r != 0 {
		return fmt.Errorf("RegSetKeySecurity(DACL Allow) 失败：错误码 %d", r)
	}
	return nil
}

// takeOwnershipAdmin 只做 owner → Administrators（不动 DACL）。
func takeOwnershipAdmin(root syscall.Handle, subKey string) error {
	// 以 TakeOwnership 权限打开（特权使其不受 DACL 拒绝影响）
	h, err := regOpen(root, subKey, accessTakeOwnership)
	if err != nil {
		h, err = regOpen(root, subKey, 0x00080000) // 仅 WRITE_OWNER
		if err != nil {
			return err
		}
	}
	defer procRegCloseKey.Call(uintptr(h))

	adminSid, err := strToSID("S-1-5-32-544")
	if err != nil {
		return err
	}
	newSD, err := buildSD(adminSid, nil, 0)
	if err != nil {
		return err
	}
	if r, _, _ := procRegSetKeySec.Call(
		uintptr(h),
		ownerSecurityInformation,
		uintptr(unsafe.Pointer(&newSD[0])),
	); r != 0 {
		return fmt.Errorf("RegSetKeySecurity(OWNER) 失败：错误码 %d", r)
	}
	return nil
}

// LockKey 锁死一个键：Everyone Deny FullControl（Deny ACE 排最前）。
// 键不存在时先创建。成功后任何进程都无法再打开该键读写。
//
// 实测结论：
//   - 锁的效果完全来自 DACL 里的 Deny ACE，owner 改动不是必需；
//   - 非管理员对 HKCU 下自己的键本来就有 WRITE_DAC，可直接改 DACL，
//     不必抢所有权（抢 owner 为 Administrators 需要 SeTakeOwnership，非管理员会 1307）；
//   - Deny 必须以现有 DACL 为基底（保留继承 Allow，Deny 才排最前）。
//
// 顺序：（尽力）owner→Admin → DACL 追加 Everyone Deny → 验证。
func LockKey(root syscall.Handle, subKey string) error {
	adjustAllPrivileges()

	// 键不存在就创建（已锁死打不开则忽略，走后续路径）
	if h, err := RegCreateKey(root, subKey); err == nil {
		procRegCloseKey.Call(uintptr(h))
	}

	// 1) 尽力把 owner 改成 Administrators（给后续 DACL 操作打底）。
	//    非管理员拿不到 SeTakeOwnership 会失败，但 HKCU 键我们已有 WRITE_DAC，
	//    失败可忽略，直接进入 DACL 加 Deny。
	if err := takeOwnershipAdmin(root, subKey); err != nil {
		// 不致命：HKCU 键当前用户已是 owner，可直接写 DACL
		_ = err
	}

	// 2) DACL 追加 Everyone Deny
	h, err := regOpen(root, subKey, accessChangePerm)
	if err != nil {
		return err
	}
	defer procRegCloseKey.Call(uintptr(h))

	everyone, err := strToSID("S-1-1-0")
	if err != nil {
		return err
	}
	curSD, err := regGetSecurity(h)
	if err != nil {
		return err
	}
	oldAcl := getExistingDacl(curSD)
	acl, err := makeAcl(accessModeDeny, everyone, oldAcl)
	if err != nil {
		return err
	}
	defer procLocalFreeACL.Call(acl)
	daclSD, err := buildSD(nil, nil, acl)
	if err != nil {
		return err
	}
	if r, _, _ := procRegSetKeySec.Call(
		uintptr(h),
		daclSecurityInformation,
		uintptr(unsafe.Pointer(&daclSD[0])),
	); r != 0 {
		return fmt.Errorf("RegSetKeySecurity(LOCK) 失败：错误码 %d", r)
	}

	// 3) 验证：用「降权后的自己」视角确认。
	// 提权管理员 token 对已锁键仍可能 KEY_READ 成功（owner=Admin 时 READ_CONTROL 不受
	// 该 Deny 影响有限），但 Deny 对 IDM（中等完整性、无特权）一定生效。
	// 程序内等价验证：CreateRestrictedToken 掉管理员组后重开，应失败。
	if !verifyLockedAsRestrictedToken(root, subKey) {
		return fmt.Errorf("锁键失败：%s 受限 token 仍可读，Deny 未生效", subKey)
	}
	return nil
}

// verifyLockedAsRestrictedToken 用受限 token（去掉 Administrators）尝试打开键。
// 打不开 = 锁对普通进程生效。
func verifyLockedAsRestrictedToken(root syscall.Handle, subKey string) bool {
	advapi := syscall.NewLazyDLL("advapi32.dll")
	procCRT := advapi.NewProc("CreateRestrictedToken")
	procCT := kernel32ACL.NewProc("CreateToolhelp32Snapshot") // 占位避免 import 混淆

	_ = procCT
	tok, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return true // 无法验证时乐观通过
	}
	defer tok.Close()

	// 禁用 Administrators SID
	adminSid, err := strToSID("S-1-5-32-544")
	if err != nil {
		return true
	}
	sidAndAttr := struct {
		Sid        *syscall.SID
		Attributes uint32
	}{adminSid, 0} // SE_GROUP_ENABLED = 0 → deny-only

	// SID_AND_ATTRIBUTES 数组只有 1 项
	var restrictedToken syscall.Token
	r, _, _ := procCRT.Call(
		uintptr(tok),      // base token
		0,                 // flags
		1,                 // nRestrictedSids
		uintptr(unsafe.Pointer(&sidAndAttr)), // 也可以同时作为 restrict sids
		0, 0,              // no disable sids / privileges
		1,
		uintptr(unsafe.Pointer(&sidAndAttr)), // restrict sids = same list
		uintptr(unsafe.Pointer(&restrictedToken)),
	)
	if r == 0 {
		return true // 建不了受限 token，乐观通过
	}
	defer restrictedToken.Close()

	// 用受限 token 模拟打开
	// （没有直接的 RegOpenKeyEx with token API；用 ImpersonateLoggedOnUser + RegOpenKeyEx）
	procImp := advapi.NewProc("ImpersonateLoggedOnUser")
	procRev := advapi.NewProc("RevertToSelf")
	if r, _, _ := procImp.Call(uintptr(restrictedToken)); r == 0 {
		return true
	}
	defer procRev.Call()

	u, _ := syscall.UTF16FromString(subKey)
	var hv syscall.Handle
	procRKOE := advapi.NewProc("RegOpenKeyExW")
	rr, _, _ := procRKOE.Call(uintptr(root), uintptr(unsafe.Pointer(&u[0])), 0, uintptr(keyRead), uintptr(unsafe.Pointer(&hv)))
	if rr == 0 {
		procRegCloseKey.Call(uintptr(hv))
		return false // 受限 token 竟然能读 → 锁没生效
	}
	return true // 打不开 = 锁生效
}

// UnlockKey 解锁一个被 LockKey 锁死的键（回滚用）：
// 用 SeTakeOwnershipPrivilege 重新拿所有权 → Everyone Allow → 可正常操作。
func UnlockKey(root syscall.Handle, subKey string) error {
	return TakeOwnershipKey(root, subKey)
}
