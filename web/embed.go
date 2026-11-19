// Package web holds the built UI (web/dist) embedded into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built UI (web/dist/app) or, when the UI was not built, the placeholder page in web/dist.
func Dist() fs.FS {
	root := "dist"
	if _, err := fs.Stat(dist, "dist/app/index.html"); err == nil {
		root = "dist/app"
	}
	sub, err := fs.Sub(dist, root)
	if err != nil {
		panic(err)
	}
	return sub
}
