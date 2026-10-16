package state

import (
	"context"
	"errors"
	"syscall"
	"time"
	"unsafe"
)

// Opening without sharing holds an exclusive lock released by the kernel on exit.
func lockDraft(ctx context.Context, path string) (func(), error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	for {
		handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return func() { _ = syscall.CloseHandle(handle) }, nil
		}
		if !errors.Is(err, syscall.Errno(32)) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

var moveDraftFile = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceDraftFile(source, target string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	const replaceExistingAndWriteThrough = 0x1 | 0x8
	result, _, callErr := moveDraftFile.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), replaceExistingAndWriteThrough)
	if result == 0 {
		return callErr
	}
	return nil
}
