// Package web exposes the built single-page application to the Go server. The
// Vite build writes web/dist, which is embedded into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets returns the built SPA files and whether a build is actually present.
// During `make dev` there is no build: the SPA is served by the Vite dev
// server instead, and the Go server answers with a hint page.
func Assets() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return sub, false
	}
	return sub, true
}
