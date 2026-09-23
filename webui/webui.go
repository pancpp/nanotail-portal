package webui

import (
	"embed"
	"io/fs"
	"os"

	"github.com/labstack/echo/v5"
	"github.com/pancpp/fairnet-portal/conf"
)

//go:embed dist
var webuiFS embed.FS

func Init(e *echo.Echo) error {
	var fs fs.FS
	if conf.UseEmbeddedWebUI() {
		fs = echo.MustSubFS(webuiFS, "dist")
	} else {
		fs = os.DirFS("dist")
	}

	e.StaticFS("/", fs)

	return nil
}
