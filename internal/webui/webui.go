//go:build !webui

package webui

import "io/fs"

// Assets is unavailable in a default Go build. Build with -tags webui after npm build.
func Assets() fs.FS { return nil }
