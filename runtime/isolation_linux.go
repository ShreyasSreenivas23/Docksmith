//go:build linux
// +build linux

package runtime

import (
	"fmt"
	"syscall"
	"unsafe"
)

const prSetNoNewPrivs = 38

type capHeader struct {
	Version uint32
	Pid     int32
}

type capData struct {
	Effective   uint32
	Permitted   uint32
	Inheritable uint32
}

// dropCapabilities prevents privilege escalation and clears capability sets.
func dropCapabilities() error {
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, uintptr(prSetNoNewPrivs), 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %w", errno)
	}

	hdr := capHeader{Version: 0x20080522, Pid: 0}
	data := [2]capData{{0, 0, 0}, {0, 0, 0}}
	if _, _, errno := syscall.Syscall(
		syscall.SYS_CAPSET,
		uintptr(unsafe.Pointer(&hdr)),
		uintptr(unsafe.Pointer(&data[0])),
		2,
	); errno != 0 {
		return fmt.Errorf("capset: %w", errno)
	}
	return nil
}
