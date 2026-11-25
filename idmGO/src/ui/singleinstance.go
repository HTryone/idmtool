package ui

// 单实例：命名 Mutex（Global\ 跨会话；Local\ 当前会话）。
// 第二个实例启动时找到已有窗口 → 置前 + 闪烁 → 自己退出。
// 同时提供「请求已有实例退出」的 IPC（WM_APP+0x110）。

import (
	"syscall"
	"unsafe"
)

var (
	kernel32Single     = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW   = kernel32Single.NewProc("CreateMutexW")
	procOpenMutexW     = kernel32Single.NewProc("OpenMutexW")
	procGetLastError   = kernel32Single.NewProc("GetLastError")
	user32Single       = syscall.NewLazyDLL("user32.dll")
	procSetForeground  = user32Single.NewProc("SetForegroundWindow")
	procShowWindow     = user32Single.NewProc("ShowWindow")
	procFlashWindowEx  = user32Single.NewProc("FlashWindowEx")
)

const (
	mutexName  = "Global\\idmtool_single_instance_v1"
	errAlreadyExists = 183 // ERROR_ALREADY_EXISTS

	wmAppQuit = 0x8000 + 0x110 // 跨实例：请求退出（第二个实例点击时把主窗口置前）
	swRestore = 9

	// windowClass 主窗口类名，ui.go 注册窗口时共用。
	windowClass = "idmtoolUI"
)

// AcquireSingleInstance 成功返回 true（本实例是主实例）。
// 失败（已有实例）返回 false，调用方应退出。
func AcquireSingleInstance() bool {
	p, err := syscall.UTF16PtrFromString(mutexName)
	if err != nil {
		return true // 名字异常时不阻断
	}

	// 先尝试 OpenMutex：能打开说明已有实例持有它。
	if existing, _, _ := procOpenMutexW.Call(0x00100000 /* SYNCHRONIZE */, 0, uintptr(unsafe.Pointer(p))); existing != 0 {
		procCloseHandle.Call(existing)
		notifyExisting()
		return false
	}

	// 创建新 mutex；若返回 ERROR_ALREADY_EXISTS 也视为已有实例（竞态窗口）。
	h, _, callErr := procCreateMutexW.Call(0, 1 /* bInitialOwner */, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return true
	}
	if errno, ok := callErr.(syscall.Errno); ok && uint32(errno) == errAlreadyExists {
		notifyExisting()
		procCloseHandle.Call(h)
		return false
	}
	// 主实例：保留 mutex 句柄直到进程退出
	singleMutex = syscall.Handle(h)
	return true
}

var singleMutex syscall.Handle

var (
	procReleaseMutex = kernel32Single.NewProc("ReleaseMutex")
	procCloseHandle  = kernel32Single.NewProc("CloseHandle")
)

// notifyExisting 找到已有窗口并置前。
func notifyExisting() {
	// 用 mut exe 名 + 类名找窗口（EnumWindows 回调里匹配类名）
	procEnumWindows.Call(syscall.NewCallback(func(hwnd syscall.Handle, lp uintptr) uintptr {
		var cn [256]uint16
		procGetClassName.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cn[0])), 256)
		if syscall.UTF16ToString(cn[:]) == windowClass {
			procShowWindow.Call(uintptr(hwnd), swRestore)
			procSetForeground.Call(uintptr(hwnd))
			flash := flashWindowInfo{Size: uint32(unsafe.Sizeof(flashWindowInfo{})), Hwnd: hwnd}
			procFlashWindowEx.Call(uintptr(unsafe.Pointer(&flash)))
			// 只置前+闪烁，不要求已有实例退出（用户要的是继续用那个窗口）
			return 0
		}
		return 1
	}), 0)
}

type flashWindowInfo struct {
	Size        uint32
	Hwnd        syscall.Handle
	Count       uint32
	Timeout     uint32
	Flags       uint32
}

var (
	procEnumWindows    = user32Single.NewProc("EnumWindows")
	procGetClassName   = user32Single.NewProc("GetClassNameW")
)
