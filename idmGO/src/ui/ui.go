package ui

// 纯 Win32 GUI：零第三方依赖。窗口 + 按钮 + 多行日志框 + 状态栏。
// 编译目标 -H=windowsgui，无控制台黑窗。

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"idmtool/src/idmtool"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	gdi32            = syscall.NewLazyDLL("gdi32.dll")
	kernel32UI       = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleH   = kernel32UI.NewProc("GetModuleHandleW")
	procRegisterCls  = user32.NewProc("RegisterClassExW")
	procCreateWnd    = user32.NewProc("CreateWindowExW")
	procShowWnd      = user32.NewProc("ShowWindow")
	procUpdateWnd    = user32.NewProc("UpdateWindow")
	procGetMessage   = user32.NewProc("GetMessageW")
	procTranslateMsg = user32.NewProc("TranslateMessage")
	procDispatchMsg  = user32.NewProc("DispatchMessageW")
	procPostQuit     = user32.NewProc("PostQuitMessage")
	procDefWndProc   = user32.NewProc("DefWindowProcW")
	procSendMsg      = user32.NewProc("SendMessageW")
	procPostMsg      = user32.NewProc("PostMessageW")
	procSetWindowText= user32.NewProc("SetWindowTextW")
	procGetWndText   = user32.NewProc("GetWindowTextW")
	procDestroyWnd   = user32.NewProc("DestroyWindow")
	procEnableWnd    = user32.NewProc("EnableWindow")
	procGetSysColor  = user32.NewProc("GetSysColor")
	procMsgBox       = user32.NewProc("MessageBoxW")
	procSetFocus     = user32.NewProc("SetFocus")
	procSetFont      = gdi32.NewProc("SelectObject")
	procCreateFont   = gdi32.NewProc("CreateFontW")
	procDeleteObject = gdi32.NewProc("DeleteObject")
	procSetDlgCtrlID = user32.NewProc("SetWindowLongPtrW")
	procGetDlgItem   = user32.NewProc("GetDlgItem")
)

const (
	csHRedraw    = 0x0002
	csVRedraw    = 0x0001
	wsOverlapped = 0x00000000
	wsCaption    = 0x00C00000
	wsSysMenu    = 0x00080000
	wsMinimizeBox= 0x00020000
	wsThickFrame = 0x00040000
	wsChild      = 0x40000000
	wsVisible    = 0x10000000
	wsTabstop    = 0x00010000
	wsGroup      = 0x00020000
	wsBorder     = 0x00800000
	wsVScroll    = 0x00200000

	wmCreate     = 0x0001
	wmDestroy    = 0x0002
	wmClose      = 0x0010
	wmCommand    = 0x0111
	wmPaint      = 0x000F
	wmSetFont   = 0x0030

	colorWindow   = 5
	colorBtnFace  = 15
	colorBtnText  = 18

	swShow = 5
)

// 控件 ID（3 个按钮：一键完成 / 回滚 / 退出）
const (
	idBtnOneClick = 1001 // 一键完成
	idBtnRollback = 1002 // 回滚
	idBtnQuit     = 1003 // 退出
	idEdtLog      = 1005
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   syscall.Handle
	Icon       syscall.Handle
	Cursor     syscall.Handle
	Background syscall.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     syscall.Handle
}

type msg struct {
	Hwnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	Private uint32
}

// UI 回调接口：由 main 注入，避免循环依赖。只保留 3 个动作。
type Callbacks struct {
	OnOneClick func(log func(string)) // 一键完成
	OnRollback func(log func(string)) // 回滚
}

var (
	callbacks Callbacks
	hwndMain  syscall.Handle
	hwndLog   syscall.Handle
	hFont     syscall.Handle
	busy      bool
)

// utf16 helper
func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// Run 启动 GUI 并进入消息循环（阻塞）。返回 false 表示启动失败。
func Run(cb Callbacks, title string) bool {
	// 【关键】Win32 消息队列是 OS 线程绑定的：窗口在此线程创建，
	// GetMessage/窗口过程也必须在此线程。Go 调度器默认会把 goroutine
	// 迁移到其它线程，导致消息循环跑在错误线程 → 窗口"未响应"、
	// 点关闭无反应、整个进程假死。必须锁定本 goroutine 到当前 OS 线程。
	runtime.LockOSThread()
	// 注意：Run 返回前不解锁；进程退出由 os.Exit 完成，无需解锁。

	callbacks = cb
	hInst, _, _ := procGetModuleH.Call(0)

	className := u16(windowClass)
	wc := wndClassEx{
		Size:       uint32(unsafe.Sizeof(wndClassEx{})),
		Style:      csHRedraw | csVRedraw,
		WndProc:    syscall.NewCallback(wndProc),
		Instance:   syscall.Handle(hInst),
		Background: syscall.Handle(6), // COLOR_BTNFACE+1
		ClassName:  className,
	}
	if r, _, _ := procRegisterCls.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return false
	}

	style := uint32(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox | wsThickFrame)
	// 客户区 520x420
	hwnd, _, _ := procCreateWnd.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(u16(title))),
		uintptr(style),
		300, 200, 520, 430,
		0, 0,
		hInst,
		0,
	)
	if hwnd == 0 {
		return false
	}
	hwndMain = syscall.Handle(hwnd)

	// 字体：微软雅黑 9pt
	const fontHeight = ^uintptr(12) // -13（两补数）
	hf, _, _ := procCreateFont.Call(
		fontHeight, 0, 0, 0, 400, 0, 0, 0,
		1 /*DEFAULT_CHARSET*/, 0, 0, 5 /*CLEARTYPE*/, 0, 0,
		uintptr(unsafe.Pointer(u16("Microsoft YaHei UI"))),
	)
	hFont = syscall.Handle(hf)

	// 日志框（多行 Edit，只读）
	hwndLog = createControl(hInst, "EDIT",
		wsChild|wsVisible|wsVScroll|wsBorder|0x0004 /*ES_MULTILINE*/ |0x0800 /*ES_READONLY*/ |0x0080 /*ES_AUTOVSCROLL*/,
		idEdtLog, 12, 12, 484, 290)
	applyFont(hwndLog)

	// 三个大按钮：一键完成（主）/ 回滚 / 退出
	b1 := createControl(hInst, "BUTTON", wsChild|wsVisible|wsTabstop, idBtnOneClick, 12, 312, 234, 42)
	setText(b1, "一键完成（冻结试用·长期有效）")
	applyFont(b1)
	b2 := createControl(hInst, "BUTTON", wsChild|wsVisible|wsTabstop, idBtnRollback, 252, 312, 120, 42)
	setText(b2, "回滚")
	applyFont(b2)
	b3 := createControl(hInst, "BUTTON", wsChild|wsVisible|wsTabstop, idBtnQuit, 378, 312, 118, 42)
	setText(b3, "退出")
	applyFont(b3)

	hint := createControl(hInst, "STATIC", wsChild|wsVisible, 0, 12, 362, 484, 20)
	setText(hint, "点「一键完成」后长期有效，无需再次打开 · 后台守护 idmguard.exe 自动常驻")
	applyFont(hint)

	procShowWnd.Call(hwnd, swShow)
	procUpdateWnd.Call(hwnd)

	logLine("点「一键完成」即可：自动冻结 IDM 试用期并安装自动续期，之后长期正常使用。")

	// 消息循环
	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMsg.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMsg.Call(uintptr(unsafe.Pointer(&m)))
	}
	return true
}

func createControl(hInst uintptr, class string, style uint32, id uintptr, x, y, w, h int32) syscall.Handle {
	hr, _, _ := procCreateWnd.Call(
		0,
		uintptr(unsafe.Pointer(u16(class))),
		0,
		uintptr(style)|wsChild,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(hwndMain),
		uintptr(id),
		hInst,
		0,
	)
	return syscall.Handle(hr)
}

func applyFont(h syscall.Handle) {
	procSendMsg.Call(uintptr(h), wmSetFont, uintptr(hFont), 0)
}

func setText(h syscall.Handle, s string) {
	procSetWindowText.Call(uintptr(h), uintptr(unsafe.Pointer(u16(s))))
}

// logLine 主线程内直接追加。
func logLine(s string) { appendLogText(s) }

// Log 供外部线程写入（跨线程安全：投递到主线程）。
func Log(s string) { logFromThread(s) }

// Logf 带格式。
func Logf(format string, a ...interface{}) { logFromThread(fmt.Sprintf(format, a...)) }

// SetBusy 切换按钮可用状态（主线程内调用）。
func SetBusy(b bool) { setBusyUI(b) }

func boolPtr(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

// MessageBox 弹窗。
func MessageBox(title, text string, icon uintptr) {
	procMsgBox.Call(uintptr(hwndMain), uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), icon)
}

// 消息框图标
const (
	MBIconInfo    = 0x40
	MBIconWarning = 0x30
	MBIconError   = 0x10
)

// 自定义消息：跨线程日志（lp = *uint16 字符串指针，主线程负责释放）
const (
	wmAppLog     = 0x8000 + 0x100 // WM_APP+0x100
	wmAppBusy    = 0x8000 + 0x101 // WM_APP+0x101（wp=1 忙 / 0 闲）
	wmAppDone    = 0x8000 + 0x102 // WM_APP+0x102（一段操作结束）
)

// pendingLogs 收集跨线程日志（由互斥锁保护）。
var (
	logMu     sync.Mutex
	logQueue  []string
)

func wndProc(hwnd syscall.Handle, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case wmAppLog:
		// 从队列取（而非直接用 lp，避免多个日志乱序）
		logMu.Lock()
		var s string
		if len(logQueue) > 0 {
			s = logQueue[0]
			logQueue = logQueue[1:]
		}
		logMu.Unlock()
		if s != "" {
			appendLogText(s)
		}
		return 0
	case wmAppBusy:
		setBusyUI(wp != 0)
		return 0
	case wmCommand:
		id := wp & 0xFFFF
		// 退出按钮任何时候都可用（即使正在执行）
		if id == idBtnQuit {
			idmtool.RequestCancel()
			procDestroyWnd.Call(uintptr(hwnd))
			return 0
		}
		if busy {
			return 0
		}
		var fn func(func(string))
		name := ""
		switch id {
		case idBtnOneClick:
			fn, name = callbacks.OnOneClick, "一键完成"
		case idBtnRollback:
			fn, name = callbacks.OnRollback, "回滚"
		}
		if fn != nil {
			idmtool.ResetCancel()
			go runOpAsync(fn, name) // 工作线程，消息循环不阻塞
		}
		return 0
	case wmAppQuit:
		// 第二个实例点击时发来的"置前"通知已由 notifyExisting 处理；
		// 这里仅作为兜底：关闭窗口。
		procDestroyWnd.Call(uintptr(hwnd))
		return 0
	case wmClose:
		// 用户点 X：先发取消信号，让工作线程的长循环尽快退出；
		// 再销毁窗口。兜底看门狗：3 秒后若进程还在（业务线程卡在 syscall），
		// 直接 os.Exit(0) —— 保证"永远能退出"。
		idmtool.RequestCancel()
		go func() {
			time.Sleep(3 * time.Second)
			os.Exit(0)
		}()
		procDestroyWnd.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		procPostQuit.Call(0)
		return 0
	}
	r, _, _ := procDefWndProc.Call(uintptr(hwnd), uintptr(msg), wp, lp)
	return r
}

// logFromThread 工作线程调用的日志函数：入队 + 投递到主线程。
func logFromThread(s string) {
	logMu.Lock()
	logQueue = append(logQueue, s)
	logMu.Unlock()
	procPostMsg.Call(uintptr(hwndMain), wmAppLog, 0, 0)
}

func runOpAsync(fn func(func(string)), name string) {
	// 工作 goroutine 锁定独立 OS 线程：绝不占用 GUI 线程，
	// 也避免 Go 调度器把它塞回消息循环所在线程。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	setBusyFromThread(true)
	// defer 复位 busy：即使 fn panic 或提前 return，按钮也一定能恢复可用，
	// 避免"按钮全灰 = 界面像卡死"的观感。
	defer setBusyFromThread(false)

	logFromThread("—— " + name + " 开始 ——")
	defer logFromThread("—— " + name + " 结束 ——")
	fn(logFromThread)
}

func setBusyFromThread(b bool) {
	procPostMsg.Call(uintptr(hwndMain), wmAppBusy, boolPtr(b), 0)
}

func setBusyUI(b bool) {
	busy = b
	for _, id := range []uintptr{idBtnOneClick, idBtnRollback} {
		h, _, _ := procGetDlgItem.Call(uintptr(hwndMain), id)
		procEnableWnd.Call(h, boolPtr(!b))
	}
}

// appendLogText 主线程内追加日志文本。
func appendLogText(s string) {
	const emSetSel = 0x00B1
	const emReplaceSel = 0x00C2
	procSendMsg.Call(uintptr(hwndLog), emSetSel, ^uintptr(0), ^uintptr(0))
	s16, _ := syscall.UTF16FromString(s + "\r\n")
	procSendMsg.Call(uintptr(hwndLog), emReplaceSel, 0, uintptr(unsafe.Pointer(&s16[0])))
}

