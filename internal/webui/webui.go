package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embedded embed.FS

// FS returns the embedded dist/ tree rooted at "dist",
// so callers get paths like "index.html" not "dist/index.html".
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
