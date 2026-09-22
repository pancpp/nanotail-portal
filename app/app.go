package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/pancpp/fairnet-portal/conf"
	"github.com/pancpp/fairnet-portal/tailscale"
	"github.com/pancpp/fairnet-portal/user"
)

func Init(ctx context.Context) error {
	e := echo.New()
	if e == nil {
		return fmt.Errorf("error to create echo context")
	}

	if err := user.Init(ctx, e); err != nil {
		return err
	}

	if err := tailscale.Init(ctx, e); err != nil {
		return err
	}

	// Start echo server
	go func() {
		sc := echo.StartConfig{
			Address:         conf.GetString("http_listen_addr"),
			GracefulTimeout: 2 * time.Second,
		}
		if err := sc.Start(ctx, e); err != nil &&
			!errors.Is(err, context.Canceled) {
			log.Printf("HTTP server stopped: %v", err)
		}
	}()

	return nil
}
