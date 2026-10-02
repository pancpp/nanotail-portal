//go:build embedwebui

package webui

import (
	"embed"
	"io/fs"

	"github.com/labstack/echo/v5"
)

//go:embed dist
var webuiFS embed.FS

func getWebuiFS() fs.FS {
	return echo.MustSubFS(webuiFS, "dist")
}
