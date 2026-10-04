//go:build linux || darwin || freebsd || netbsd || openbsd

package intake

import "golang.org/x/sys/unix"

// reusePortSupported says whether several readers can share one port.
const reusePortSupported = true

func setReusePort(fd uintptr) error {
	return unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
}

// msgTrunc marks a datagram the kernel cut to fit the read buffer.
const msgTrunc = unix.MSG_TRUNC
