// Package buildinfo carries the version stamped in by the linker.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Set with -ldflags "-X .../internal/buildinfo.Version=... -X .../internal/buildinfo.Commit=...".
var (
	Version = "dev"
	Commit  = "none"
)

func init() {
	if Commit == "none" {
		Commit = vcsCommit()
	}
}

// vcsCommit is the revision go build records when it runs inside a checkout,
// so a plain go build or go run still says which code it is.
func vcsCommit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "none"
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "none"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

// GoVersion is the toolchain that built the binary.
func GoVersion() string { return runtime.Version() }
