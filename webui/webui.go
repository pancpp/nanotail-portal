package webui

import (
	"github.com/labstack/echo/v5"
)

func Init(e *echo.Echo) error {
	webuiFS := getWebuiFS()
	e.StaticFS("/", webuiFS)
	return nil
}
