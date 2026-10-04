package store

// DiskSpace is what one filesystem has left.
type DiskSpace struct {
	// Device tells two folders on the same filesystem apart from two on
	// different ones.
	Device string `json:"-"`
	Free   int64  `json:"freeBytes"`
	Total  int64  `json:"totalBytes"`
}
