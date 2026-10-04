package intake

import (
	"errors"
	"net"
	"net/netip"

	"golang.org/x/net/ipv4"
)

// batchReader pulls several datagrams per syscall. On Linux x/net/ipv4 turns
// this into recvmmsg; elsewhere it degrades to one read per call, which is
// the intended fallback.
type batchReader struct {
	pc    *ipv4.PacketConn
	msgs  []ipv4.Message
	conn  *net.UDPConn
	batch int
	// senders says whether anything routes by sender address; converting it
	// for every datagram costs an allocation nobody uses otherwise.
	senders bool
	tap     *Tap
}

// newBatchReader prepares a reader that holds batch buffers of size bytes.
func newBatchReader(conn *net.UDPConn, batch, size int, senders bool, tap *Tap) *batchReader {
	if batch <= 0 {
		batch = 64
	}
	msgs := make([]ipv4.Message, batch)
	for i := range msgs {
		msgs[i] = ipv4.Message{Buffers: [][]byte{make([]byte, size)}}
	}
	return &batchReader{pc: ipv4.NewPacketConn(conn), msgs: msgs, conn: conn, batch: batch, senders: senders, tap: tap}
}

// Read hands each datagram of one batch to fn, with whether the kernel had to
// cut it short. The buffers are reused, so fn must not keep the slice.
func (r *batchReader) Read(fn func(raw []byte, from netip.Addr, truncated bool)) error {
	n, err := r.pc.ReadBatch(r.msgs, 0)
	if err != nil {
		return err
	}
	senders := r.senders || r.tap.watched()
	for i := 0; i < n; i++ {
		m := r.msgs[i]
		if m.N <= 0 {
			continue
		}
		var from netip.Addr
		if senders {
			from = addrOf(m.Addr)
		}
		fn(m.Buffers[0][:m.N], from, msgTrunc != 0 && m.Flags&msgTrunc != 0)
	}
	return nil
}

// Close releases the batch reader. The connection itself belongs to the caller.
func (r *batchReader) Close() error {
	if r.pc == nil {
		return nil
	}
	err := r.pc.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func addrOf(a net.Addr) netip.Addr {
	if ua, ok := a.(*net.UDPAddr); ok {
		if ap, ok := netip.AddrFromSlice(ua.IP); ok {
			return ap.Unmap()
		}
	}
	return netip.Addr{}
}
