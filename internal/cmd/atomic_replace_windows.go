//go:build windows

package cmd

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

func atomicReplace(source, destination string) error {
	sourcep, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationp, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
		return windows.MoveFileEx(sourcep, destinationp, windows.MOVEFILE_WRITE_THROUGH)
	} else if err != nil {
		return err
	}
	result, _, callErr := replaceFile.Call(
		uintptr(unsafe.Pointer(destinationp)),
		uintptr(unsafe.Pointer(sourcep)),
		0,
		0,
		0,
		0,
	)
	if result == 0 {
		if callErr != syscall.Errno(0) {
			return callErr
		}
		return syscall.EINVAL
	}
	return nil
}
