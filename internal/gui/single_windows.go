//go:build windows

package gui

import (
	"syscall"
	"unsafe"
)

const errorAlreadyExists = 183

var (
	procCreateMutexW = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	procMessageBoxW  = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
)

func acquireSingleInstance() (syscall.Handle, bool) {
	name, err := syscall.UTF16PtrFromString(`Local\ghaccel-gui-single-instance`)
	if err != nil {
		return 0, true
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return 0, true
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		syscall.CloseHandle(syscall.Handle(h))
		return 0, false
	}
	return syscall.Handle(h), true
}

func releaseSingleInstance(h syscall.Handle) {
	if h != 0 {
		syscall.CloseHandle(h)
	}
}

func showAlreadyRunning() {
	text, _ := syscall.UTF16PtrFromString("ghaccel 已经在运行，请从系统托盘或任务栏打开。")
	caption, _ := syscall.UTF16PtrFromString("ghaccel")
	procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(caption)),
		0x00000040, // MB_ICONINFORMATION
	)
}
