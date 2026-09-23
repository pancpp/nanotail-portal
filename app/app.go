package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/golang-jwt/jwt/v5"
	echojwt "github.com/labstack/echo-jwt/v5"
	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/webui"
)

func Init(ctx context.Context) error {
	e := echo.New()
	if e == nil {
		return fmt.Errorf("error to create echo context")
	}

	if err := webui.Init(e); err != nil {
		return err
	}

	if err := initAPIs(e); err != nil {
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

func initAPIs(e *echo.Echo) error {
	e.POST("/api/login", handleLogin)

	jwtMiddleware := echojwt.WithConfig(echojwt.Config{
		SigningKey:    gJwtSigningKey,
		ContextKey:    JWT_CONTEXT_KEY_TOKEN,
		NewClaimsFunc: func(c *echo.Context) jwt.Claims { return new(Claims) },
	})
	apiGroup := e.Group("/api", jwtMiddleware)
	apiGroup.POST("/change-password", handleChangePassword)

	return nil
}
