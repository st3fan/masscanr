//go:build webui

package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var files embed.FS

func Assets() fs.FS {
	root, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return root
}
