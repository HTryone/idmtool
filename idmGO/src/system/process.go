package system

// 进程探测与结束：CreateToolhelp32Snapshot 枚举 + OpenProcess/TerminateProcess，
// 不 spawn taskkill（不闪黑窗、不依赖外部命令）。

import (
	"syscall"
	"unsafe"
)

const (
	th32csSnapProcess = 0x00000002
	processTerminate  = 0x0001
	invalidHandleVal  = ^uintptr(0)
	maxPath           = 260
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procCreateToolhelp32 = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW  = kernel32.NewProc("Process32FirstW")
	procProcess32NextW   = kernel32.NewProc("Process32NextW")
	procOpenProcess      = kernel32.NewProc("OpenProcess")
	procTerminateProcess = kernel32.NewProc("TerminateProcess")
	procCloseHandle      = kernel32.NewProc("CloseHandle")
)

type processEntry32 struct {
	Size            uint32
	CntUsage        uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	CntThreads      uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [maxPath]uint16
}

// FindProcesses 返回所有进程名等于 exeName（不区分大小写）的 PID。
func FindProcesses(exeName string) []uint32 {
	snap, _, _ := procCreateToolhelp32.Call(th32csSnapProcess, 0)
	if snap == invalidHandleVal || snap == 0 {
		return nil
	}
	defer procCloseHandle.Call(snap)
	var pe processEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []uint32
	r, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	for r != 0 {
		if EqualFoldASCII(syscall.UTF16ToString(pe.ExeFile[:]), exeName) {
			out = append(out, pe.ProcessID)
		}
		pe.Size = uint32(unsafe.Sizeof(pe))
		r, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	}
	return out
}

// EqualFoldASCII 不区分大小写的 ASCII 比较。
func EqualFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// KillProcesses 结束所有同名进程，返回结束的个数。
func KillProcesses(exeName string) int {
	n := 0
	for _, pid := range FindProcesses(exeName) {
		h, _, _ := procOpenProcess.Call(processTerminate, 0, uintptr(pid))
		if h == 0 {
			continue
		}
		if r, _, _ := procTerminateProcess.Call(h, 1); r != 0 {
			n++
		}
		procCloseHandle.Call(h)
	}
	return n
}

// KillIDM 结束 idman.exe（若在运行）。
func KillIDM() int { return KillProcesses("idman.exe") }

// IDMRunning 检测 idman.exe 是否在运行。
func IDMRunning() bool { return len(FindProcesses("idman.exe")) > 0 }
