//go:build linux

package monitor

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// DropCapabilities runs after socket creation and before the HTTP listener starts.
// AllThreadsSyscall is intentional: Linux capabilities are per-thread. Builds
// must use CGO_ENABLED=0, so the Go runtime can update every OS thread safely.
func DropCapabilities() error {
	if _, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("set no_new_privs: %w", errno)
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	data := [2]unix.CapUserData{}
	_, _, errno := syscall.AllThreadsSyscall(unix.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0)
	runtime.KeepAlive(&header)
	runtime.KeepAlive(&data)
	if errno != 0 {
		return fmt.Errorf("drop capture capabilities: %w", errno)
	}
	return nil
}
