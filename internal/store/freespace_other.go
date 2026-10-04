//go:build !unix

package store

func freeBytes(string) (int64, bool) { return 0, false }

// DiskSpaceOf is not available on this system.
func DiskSpaceOf(string) (DiskSpace, bool) { return DiskSpace{}, false }
