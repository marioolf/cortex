//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// showFatal shows a plain message box. Best effort: any failure is ignored.
func showFatal(title, msg string) {
	t, err1 := syscall.UTF16PtrFromString(title)
	m, err2 := syscall.UTF16PtrFromString(msg)
	if err1 != nil || err2 != nil {
		return
	}
	const mbOKIconError = 0x00000010
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbOKIconError)
}
