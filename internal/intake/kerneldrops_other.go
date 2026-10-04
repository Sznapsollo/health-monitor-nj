//go:build !linux

package intake

// kernelDrops is only available where the kernel exposes it.
func kernelDrops(port int) (int64, bool) { return 0, false }
