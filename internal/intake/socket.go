package intake

import (
	"context"
	"fmt"
	"net"
	"syscall"
)

// listenUDP opens one UDP socket. With reuse set, several sockets share the
// same address and the kernel load-balances datagrams across them, which is
// what lets the readers scale without a lock between them.
func listenUDP(ctx context.Context, addr string, reuse bool, readBuffer int) (*net.UDPConn, error) {
	lc := net.ListenConfig{}
	if reuse {
		lc.Control = func(network, address string, c syscall.RawConn) error {
			var opErr error
			err := c.Control(func(fd uintptr) {
				opErr = setReusePort(fd)
			})
			if err != nil {
				return err
			}
			return opErr
		}
	}
	pc, err := lc.ListenPacket(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		_ = pc.Close()
		return nil, fmt.Errorf("intake: %s is not a UDP socket", addr)
	}
	if readBuffer > 0 {
		// Best effort: the kernel silently clamps this to net.core.rmem_max,
		// which the ops docs tell the host to raise.
		_ = conn.SetReadBuffer(readBuffer)
	}
	return conn, nil
}
