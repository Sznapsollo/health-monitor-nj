//go:build !(linux || darwin || freebsd || netbsd || openbsd)

package intake

// reusePortSupported says whether several readers can share one port. Where it
// is false the intake falls back to a single socket read by every reader.
const reusePortSupported = false

func setReusePort(fd uintptr) error { return nil }

const msgTrunc = 0
