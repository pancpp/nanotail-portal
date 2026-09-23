package webui

import (
	"embed"

	"github.com/labstack/echo/v5"
)

//go:embed dist
var webuiFS embed.FS

func Init(e *echo.Echo) error {
	fs := echo.MustSubFS(webuiFS, "dist")

	e.StaticFS("/", fs)

	return nil
}
