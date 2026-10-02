//go:build !embedwebui

package webui

import (
	"io/fs"
	"os"
)

func getWebuiFS() fs.FS {
	return os.DirFS("webui/dist")
}
