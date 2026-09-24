package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/labstack/echo/v5"
)

// Register expects a group with authentication middleware already attached.
func (client *Client) Register(g *echo.Group) {
	g.GET("/status", func(c *echo.Context) error {
		status, err := client.Status(c.Request().Context())
		if err != nil {
			return httpError(err)
		}
		return c.JSON(http.StatusOK, status)
	})
	g.GET("/peers", func(c *echo.Context) error {
		status, err := client.Status(c.Request().Context())
		if err != nil {
			return httpError(err)
		}
		return c.JSON(http.StatusOK, status.Peers)
	})
	g.GET("/config", func(c *echo.Context) error {
		cfg, err := client.Config(c.Request().Context())
		if err != nil {
			return httpError(err)
		}
		return c.JSON(http.StatusOK, cfg)
	})
	g.PATCH("/config", func(c *echo.Context) error {
		var req ConfigUpdate
		if err := decodeConfig(c, &req); err != nil {
			return err
		}
		if err := client.UpdateConfig(c.Request().Context(), req); err != nil {
			return httpError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	g.POST("/up", func(c *echo.Context) error {
		if err := client.Up(c.Request().Context()); err != nil {
			return httpError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
	g.POST("/down", func(c *echo.Context) error {
		if err := client.Down(c.Request().Context()); err != nil {
			return httpError(err)
		}
		return c.NoContent(http.StatusNoContent)
	})
}

func decodeConfig(c *echo.Context, config *ConfigUpdate) error {
	mediaType, _, err := mime.ParseMediaType(c.Request().Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return echo.NewHTTPError(http.StatusUnsupportedMediaType, "Content-Type must be application/json")
	}
	body := http.MaxBytesReader(c.Response(), c.Request().Body, 64<<10)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(config); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid configuration JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return echo.NewHTTPError(http.StatusBadRequest, "Expected one JSON object")
	}
	return nil
}

func httpError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidConfig):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return echo.NewHTTPError(http.StatusGatewayTimeout, "Tailscale command timed out; check status before retrying")
	case errors.Is(err, ErrUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, ErrUnavailable.Error())
	case errors.Is(err, ErrInvalidOutput):
		return echo.NewHTTPError(http.StatusBadGateway, ErrInvalidOutput.Error())
	default:
		return err
	}
}
