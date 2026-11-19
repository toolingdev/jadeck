//go:build !windows

package netinfo

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuse lets the responder share UDP 5353 with the OS's own (macOS
// mDNSResponder, avahi on Linux).
func reuse(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if serr == nil {
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	})
	if err != nil {
		return err
	}
	return serr
}
