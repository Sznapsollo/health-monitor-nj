//go:build linux

package intake

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// kernelDrops reads how many datagrams the kernel threw away before the
// process ever saw them, which is the loss a receive-buffer counter inside Go
// can never show.
func kernelDrops(port int) (int64, bool) {
	var total int64
	var found bool
	for _, path := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		n, ok := dropsFrom(path, port)
		if ok {
			total += n
			found = true
		}
	}
	return total, found
}

func dropsFrom(path string, port int) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()

	want := fmt.Sprintf(":%04X", port)
	var total int64
	var found bool

	scanner := bufio.NewScanner(f)
	scanner.Scan() // header
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout
		// inode ref pointer drops
		if len(fields) < 13 {
			continue
		}
		if !strings.HasSuffix(fields[1], want) {
			continue
		}
		n, err := strconv.ParseInt(fields[12], 10, 64)
		if err != nil {
			continue
		}
		total += n
		found = true
	}
	return total, found
}
