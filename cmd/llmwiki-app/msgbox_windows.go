//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// showError surfaces a fatal startup error in a native message box so a
// double-click that fails to open does not flash-and-exit silently.
func showError(title, msg string) {
	u := syscall.NewLazyDLL("user32.dll")
	msgBox := u.NewProc("MessageBoxW")
	text, _ := syscall.UTF16PtrFromString(msg)
	caption, _ := syscall.UTF16PtrFromString(title)
	// MB_ICONERROR (0x10) | MB_OK (0x0) | MB_TOPMOST (0x40000)
	msgBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), 0x40010)
}
