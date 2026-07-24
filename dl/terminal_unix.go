//go:build darwin || linux

package dl

import (
	"syscall"
	"unsafe"
)

type winsize struct {
	rows uint16
	cols uint16
	x    uint16
	y    uint16
}

// terminalWidthFD returns the column count for the terminal on fd, or 0 if fd is
// not a terminal (e.g. a pipe or file).
func terminalWidthFD(fd uintptr) int {
	ws := &winsize{}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 {
		return 0
	}
	return int(ws.cols)
}
