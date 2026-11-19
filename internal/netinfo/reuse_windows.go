package netinfo

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// reuse lets the responder share UDP 5353 with Windows' own mDNS.
func reuse(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
