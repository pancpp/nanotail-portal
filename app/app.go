package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	echojwt "github.com/labstack/echo-jwt/v5"
	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/pancpp/nanotail-portal/traffic"
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

	// One collector per portal process, independent of logged-in browsers.
	go traffic.NewRecorder(device.NewTrafficReader(), traffic.NewStore(database.DB())).Run(ctx)

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
	// Only login needs no header authentication
	e.POST("/api/login", handleLogin)

	jwtMiddleware := echojwt.WithConfig(echojwt.Config{
		SigningKey:    auth.GetJwtSignKey(),
		ContextKey:    auth.JWT_CONTEXT_KEY_TOKEN,
		NewClaimsFunc: func(c *echo.Context) jwt.Claims { return new(auth.Claims) },
	})
	gqlSrv := newGraphQLServer()
	e.POST("/api/v1/query",
		func(c *echo.Context) error {
			token, ok := c.Get(auth.JWT_CONTEXT_KEY_TOKEN).(*jwt.Token)
			if !ok || token == nil || !token.Valid {
				return echo.NewHTTPError(http.StatusUnauthorized, ErrUnauthorized.Error())
			}
			claims, ok := token.Claims.(*auth.Claims)
			if !ok || claims == nil || claims.UserPID <= 0 {
				return echo.NewHTTPError(http.StatusUnauthorized, ErrUnauthorized.Error())
			}
			ctx := context.WithValue(
				c.Request().Context(),
				graph.QUERY_CONTEXT_KEY,
				&graph.ContextValue{UserPID: claims.UserPID})
			c.Response().Header().Set("Cache-Control", "no-store")
			gqlSrv.ServeHTTP(c.Response(), c.Request().WithContext(ctx))
			return nil
		},
		jwtMiddleware)

	return nil
}
