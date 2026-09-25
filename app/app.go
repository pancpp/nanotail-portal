package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	echojwt "github.com/labstack/echo-jwt/v5"
	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/activityled"
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/pancpp/nanotail-portal/factoryreset"
	"github.com/pancpp/nanotail-portal/traffic"
	"github.com/pancpp/nanotail-portal/webui"
)

type Runtime struct {
	server        *http.Server
	stopCollector context.CancelFunc
	collectorDone chan struct{}
	serverErrors  chan error
	ledDone       chan struct{}
}

func Start(ctx context.Context, reset *factoryreset.Controller) (*Runtime, error) {
	if len(auth.GetJwtSignKey()) == 0 {
		return nil, fmt.Errorf("JWT signing key must be initialized before starting HTTP services")
	}
	e := echo.New()
	if e == nil {
		return nil, fmt.Errorf("error to create echo context")
	}

	if err := webui.Init(e); err != nil {
		return nil, err
	}

	if err := initAPIs(e); err != nil {
		return nil, err
	}
	initFactoryResetAPI(e, reset)
	// Stop admitting new work before shutdown drains already-running handlers.
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if reset != nil && reset.Pending() {
				c.Response().Header().Set("Retry-After", "5")
				return echo.NewHTTPError(http.StatusServiceUnavailable, "Factory reset is in progress")
			}
			return next(c)
		}
	})

	listener, err := net.Listen("tcp", conf.GetString("http_listen_addr"))
	if err != nil {
		return nil, err
	}
	collectorCtx, stopCollector := context.WithCancel(ctx)
	runtime := &Runtime{
		server:        &http.Server{Handler: e, ReadHeaderTimeout: 10 * time.Second},
		stopCollector: stopCollector,
		collectorDone: make(chan struct{}),
		serverErrors:  make(chan error, 1),
		ledDone:       make(chan struct{}),
	}

	// One collector per portal process, independent of logged-in browsers.
	go func() {
		defer close(runtime.collectorDone)
		traffic.NewRecorder(device.NewTrafficReader(), traffic.NewStore(database.DB())).Run(collectorCtx)
	}()
	// A separate, lightweight sampler keeps LED response independent of the
	// minute-based history recorder. Failure only disables the indicator.
	enableTrafficLED := conf.GetBool("vpn_traffic_led")
	go func() {
		defer close(runtime.ledDone)
		if enableTrafficLED {
			if err := activityled.Run(collectorCtx, device.NewTrafficReader()); err != nil {
				log.Printf("(VPN traffic LED) disabled: %v", err)
			}
		}
	}()

	// Start echo server
	go func() {
		err := runtime.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped: %v", err)
			runtime.serverErrors <- err
		}
	}()

	return runtime, nil
}

func (r *Runtime) Errors() <-chan error { return r.serverErrors }

func (r *Runtime) Shutdown(ctx context.Context) error {
	r.stopCollector()
	if err := r.server.Shutdown(ctx); err != nil {
		return err
	}
	select {
	case <-r.collectorDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	if r.ledDone != nil {
		select {
		case <-r.ledDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func jwtMiddleware() echo.MiddlewareFunc {
	return echojwt.WithConfig(echojwt.Config{
		SigningKey:    auth.GetJwtSignKey(),
		ContextKey:    auth.JWT_CONTEXT_KEY_TOKEN,
		NewClaimsFunc: func(c *echo.Context) jwt.Claims { return new(auth.Claims) },
	})
}

func initAPIs(e *echo.Echo) error {
	// Only login needs no header authentication
	e.POST("/api/login", handleLogin)

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
		jwtMiddleware())

	return nil
}
