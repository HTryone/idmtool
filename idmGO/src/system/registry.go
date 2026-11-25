package system

// 注册表工具箱：直接用 advapi32 的系统调用，不依赖 golang.org/x/sys，
// 不 fork reg.exe（避免 GBK 解码与控制台窗口闪烁）。
// 支持任意根键（HKCU / HKU\<sid> / HKLM），这是 IDM 锁键法的基础。

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	keyQueryValue = 0x0001
	keySetValue   = 0x0002
	keyCreateSub  = 0x0004
	keyEnumerate  = 0x0008
	keyNotify     = 0x0010
	keyRead       = 0x00020019
	keyAllAccess  = 0x000F003F
)

// KeyRead 对外导出只读权限常量。
const KeyRead = keyRead

const (
	regSZ      = 1
	regDword   = 4
	regBinary  = 3
)

var advapi32 = syscall.NewLazyDLL("advapi32.dll")

var (
	procRegOpenKeyEx    = advapi32.NewProc("RegOpenKeyExW")
	procRegCreateKeyEx  = advapi32.NewProc("RegCreateKeyExW")
	procRegCloseKey     = advapi32.NewProc("RegCloseKey")
	procRegQueryValEx   = advapi32.NewProc("RegQueryValueExW")
	procRegSetValueEx   = advapi32.NewProc("RegSetValueExW")
	procRegDeleteVal    = advapi32.NewProc("RegDeleteValueW")
	procRegDeleteTree   = advapi32.NewProc("RegDeleteTreeW")
	procRegEnumKeyEx    = advapi32.NewProc("RegEnumKeyExW")
	procRegQueryInfoKey = advapi32.NewProc("RegQueryInfoKeyW")
)

// RootByName 把 "HKCU"/"HKU"/"HKLM" 转成根键句柄。
func RootByName(name string) (syscall.Handle, bool) {
	switch name {
	case "HKCU", "HKEY_CURRENT_USER":
		return syscall.HKEY_CURRENT_USER, true
	case "HKU", "HKEY_USERS":
		return syscall.HKEY_USERS, true
	case "HKLM", "HKEY_LOCAL_MACHINE":
		return syscall.HKEY_LOCAL_MACHINE, true
	}
	return 0, false
}

func regOpen(root syscall.Handle, sub string, access uint32) (syscall.Handle, error) {
	var h syscall.Handle
	r, _, _ := procRegOpenKeyEx.Call(
		uintptr(root),
		uintptr(unsafe.Pointer(utf16Ptr(sub))),
		0,
		uintptr(access),
		uintptr(unsafe.Pointer(&h)),
	)
	if r != 0 {
		return 0, fmt.Errorf("打开注册表键失败（%s）：错误码 %d", sub, r)
	}
	return h, nil
}

// RegOpen 打开任意根键下的键。
func RegOpen(root syscall.Handle, sub string, access uint32) (syscall.Handle, error) {
	return regOpen(root, sub, access)
}

// RegClose 关闭键句柄。
func RegClose(h syscall.Handle) { procRegCloseKey.Call(uintptr(h)) }

// RegReadString 读取任意根键下的字符串值；值不存在时返回 ("", false)。
func RegReadString(root syscall.Handle, sub, name string) (string, bool) {
	h, err := regOpen(root, sub, keyRead)
	if err != nil {
		return "", false
	}
	defer procRegCloseKey.Call(uintptr(h))
	n := utf16Ptr(name)
	var typ, size uint32
	r, _, _ := procRegQueryValEx.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(n)),
		0,
		uintptr(unsafe.Pointer(&typ)),
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	if r != 0 || size == 0 {
		return "", false
	}
	buf := make([]uint16, size/2+1)
	r, _, _ = procRegQueryValEx.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(n)),
		0,
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r != 0 || typ != regSZ {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

// RegReadDefault 读键的默认值（名字为空串）。
func RegReadDefault(root syscall.Handle, sub string) (string, bool) {
	return RegReadString(root, sub, "")
}

// RegWriteString 在任意根键下写 REG_SZ（键不存在时自动创建，等价 reg add）。
func RegWriteString(root syscall.Handle, sub, name, value string) error {
	h, err := RegCreateKey(root, sub)
	if err != nil {
		return err
	}
	defer procRegCloseKey.Call(uintptr(h))
	val, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	var np *uint16
	if name != "" {
		np = utf16Ptr(name)
	}
	r, _, _ := procRegSetValueEx.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(np)),
		0,
		regSZ,
		uintptr(unsafe.Pointer(&val[0])),
		uintptr(len(val)*2),
	)
	if r != 0 {
		return fmt.Errorf("写入 %s\\%s 失败：错误码 %d", sub, name, r)
	}
	return nil
}

// RegWriteDword 在任意根键下写 REG_DWORD（键不存在时自动创建，等价 reg add）。
func RegWriteDword(root syscall.Handle, sub, name string, value uint32) error {
	h, err := RegCreateKey(root, sub)
	if err != nil {
		return err
	}
	defer procRegCloseKey.Call(uintptr(h))
	var np *uint16
	if name != "" {
		np = utf16Ptr(name)
	}
	r, _, _ := procRegSetValueEx.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(np)),
		0,
		regDword,
		uintptr(unsafe.Pointer(&value)),
		4,
	)
	if r != 0 {
		return fmt.Errorf("写入 %s\\%s 失败：错误码 %d", sub, name, r)
	}
	return nil
}

// RegDeleteValue 删除任意根键下的值；键或值不存在视为成功。
func RegDeleteValue(root syscall.Handle, sub, name string) error {
	h, err := regOpen(root, sub, keyAllAccess)
	if err != nil {
		return nil
	}
	defer procRegCloseKey.Call(uintptr(h))
	procRegDeleteVal.Call(uintptr(h), uintptr(unsafe.Pointer(utf16Ptr(name))))
	return nil
}

// RegDeleteTree 递归删除任意根键下的键；不存在视为成功。
func RegDeleteTree(root syscall.Handle, sub string) error {
	var rootKey = root
	subKey := sub
	// RegDeleteTreeW 不能删根键本身，需要打开父键删最后一段。
	parent, leaf := splitKeyPath(sub)
	if leaf != "" {
		subKey = parent
	} else {
		return fmt.Errorf(" refusing to delete root key %s", sub)
	}
	h, err := regOpen(rootKey, subKey, keyAllAccess)
	if err != nil {
		return nil // 父键不存在 = 已删除
	}
	defer procRegCloseKey.Call(uintptr(h))
	r, _, _ := procRegDeleteTree.Call(uintptr(h), uintptr(unsafe.Pointer(utf16Ptr(leaf))))
	if r != 0 {
		return fmt.Errorf("删除 %s 失败：错误码 %d", sub, r)
	}
	return nil
}

// splitKeyPath 把 "Software\Classes\CLSID\{...}" 拆成父路径和最后一段。
func splitKeyPath(p string) (string, string) {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '\\' {
			return p[:i], p[i+1:]
		}
	}
	return "", p
}

// RegKeyExists 判断键是否存在。
func RegKeyExists(root syscall.Handle, sub string) bool {
	h, err := regOpen(root, sub, keyRead)
	if err != nil {
		return false
	}
	procRegCloseKey.Call(uintptr(h))
	return true
}

// RegValueExists 判断某值是否存在（任意根键）。
func RegValueExists(root syscall.Handle, sub, name string) bool {
	h, err := regOpen(root, sub, keyQueryValue)
	if err != nil {
		return false
	}
	defer procRegCloseKey.Call(uintptr(h))
	var typ, size uint32
	r, _, _ := procRegQueryValEx.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(utf16Ptr(name))),
		0,
		uintptr(unsafe.Pointer(&typ)),
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	return r == 0
}

// RegSubKeys 枚举直接子键名。
func RegSubKeys(root syscall.Handle, sub string) ([]string, error) {
	h, err := regOpen(root, sub, keyRead)
	if err != nil {
		return nil, err
	}
	defer procRegCloseKey.Call(uintptr(h))
	var (
		subKeyCount, maxValueLen uint32
		maxSubKeyLen             uint32
	)
	r, _, _ := procRegQueryInfoKey.Call(
		uintptr(h),
		0, 0, 0,
		uintptr(unsafe.Pointer(&subKeyCount)),
		uintptr(unsafe.Pointer(&maxSubKeyLen)),
		0, 0, 0, 0, 0, 0,
	)
	_ = maxValueLen
	if r != 0 {
		return nil, fmt.Errorf("查询键信息失败（%s）：错误码 %d", sub, r)
	}
	nameBuf := make([]uint16, maxSubKeyLen+2)
	var out []string
	for i := uint32(0); i < subKeyCount; i++ {
		var nameLen = uint32(len(nameBuf))
		r, _, _ = procRegEnumKeyEx.Call(
			uintptr(h),
			uintptr(i),
			uintptr(unsafe.Pointer(&nameBuf[0])),
			uintptr(unsafe.Pointer(&nameLen)),
			0, 0, 0, 0, 0, 0, 0,
		)
		if r != 0 {
			continue
		}
		out = append(out, syscall.UTF16ToString(nameBuf[:nameLen]))
	}
	return out, nil
}

// RegValueNames 枚举键下所有值名（含默认值 ""）。
func RegValueNames(h syscall.Handle) []string {
	var valueCount, maxValueNameLen, maxValueLen uint32
	r, _, _ := procRegQueryInfoKey.Call(
		uintptr(h),
		0, 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&valueCount)),
		uintptr(unsafe.Pointer(&maxValueNameLen)),
		uintptr(unsafe.Pointer(&maxValueLen)),
		0, 0,
	)
	if r != 0 {
		return nil
	}
	nameBuf := make([]uint16, maxValueNameLen+2)
	var out []string
	for i := uint32(0); i < valueCount; i++ {
		nameLen := uint32(len(nameBuf))
		r, _, _ = procRegEnumValue.Call(
			uintptr(h),
			uintptr(i),
			uintptr(unsafe.Pointer(&nameBuf[0])),
			uintptr(unsafe.Pointer(&nameLen)),
			0, 0, 0, 0,
		)
		if r != 0 {
			continue
		}
		out = append(out, syscall.UTF16ToString(nameBuf[:nameLen]))
	}
	return out
}

// RegValueCount 返回键下的值的个数。
func RegValueCount(h syscall.Handle) int {
	var valueCount uint32
	r, _, _ := procRegQueryInfoKey.Call(
		uintptr(h),
		0, 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&valueCount)),
		0, 0, 0, 0,
	)
	if r != 0 {
		return -1
	}
	return int(valueCount)
}

var procRegEnumValue = advapi32.NewProc("RegEnumValueW")

// RegCreateKey 确保键存在（不存在则创建），返回句柄（调用方负责 RegClose）。
func RegCreateKey(root syscall.Handle, sub string) (syscall.Handle, error) {
	var h syscall.Handle
	var disp uint32
	r, _, _ := procRegCreateKeyEx.Call(
		uintptr(root),
		uintptr(unsafe.Pointer(utf16Ptr(sub))),
		0,
		0,
		0, // REG_OPTION_NON_VOLATILE
		keyAllAccess,
		0,
		uintptr(unsafe.Pointer(&h)),
		uintptr(unsafe.Pointer(&disp)),
	)
	if r != 0 {
		return 0, fmt.Errorf("创建键 %s 失败：错误码 %d", sub, r)
	}
	return h, nil
}
