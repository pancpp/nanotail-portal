package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/golang-jwt/jwt/v5"
	echojwt "github.com/labstack/echo-jwt/v5"
	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/access"
	"github.com/pancpp/nanotail-portal/activityled"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/pancpp/nanotail-portal/factoryreset"
	"github.com/pancpp/nanotail-portal/tailscale"
	"github.com/pancpp/nanotail-portal/traffic"
	"github.com/pancpp/nanotail-portal/upgrade"
	"github.com/pancpp/nanotail-portal/webui"
)

type Runtime struct {
	server        *http.Server
	stopCollector context.CancelFunc
	collectorDone chan struct{}
	serverErrors  chan error
	ledDone       chan struct{}
	routingDone   chan struct{}
	accessDone    chan struct{}
}

// Init initializes application services in order. Call it once after reset
// recovery and database migrations, before starting HTTP services.
func Init(ctx context.Context) error {
	if err := auth.Init(); err != nil {
		return err
	}

	if err := access.Init(ctx); err != nil {
		log.Printf("(Device IP reporting) disabled: %v", err)
	}
	return nil
}

func Start(ctx context.Context, reset *factoryreset.Controller, installers ...*upgrade.Installer) (*Runtime, error) {
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

	upgrades, err := newUpgradeService(ctx)
	if err != nil {
		return nil, err
	}
	var installer *upgrade.Installer
	if len(installers) > 0 {
		installer = installers[0]
	}
	upgrades.SetInstaller(installer)
	client := newTailscaleClient()
	if err := initAPIsWithClient(e, client, graphQLServices{upgrades: upgrades, reset: reset, installer: installer}); err != nil {
		return nil, err
	}
	initFactoryResetAPI(e, reset)
	// Stop admitting new work before shutdown drains already-running handlers.
	e.Use(maintenanceMiddleware(reset, installer))

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
		routingDone:   make(chan struct{}),
		accessDone:    make(chan struct{}),
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

	go func() {
		defer close(runtime.routingDone)
		client.MaintainRouting(collectorCtx)
	}()
	go func() {
		defer close(runtime.accessDone)
		access.Run(collectorCtx)
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

func maintenanceMiddleware(reset *factoryreset.Controller, installer *upgrade.Installer) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			// GraphQL admits only upgradeStatus selections during maintenance,
			// after JWT authentication and operation parsing.
			if c.Request().Method == http.MethodPost && c.Request().URL.Path == "/api/v1/query" {
				return next(c)
			}
			if reset != nil && reset.Pending() {
				c.Response().Header().Set("Retry-After", "5")
				return echo.NewHTTPError(http.StatusServiceUnavailable, "Factory reset is in progress")
			}
			if installer != nil && installer.Pending() {
				c.Response().Header().Set("Retry-After", "5")
				return echo.NewHTTPError(http.StatusServiceUnavailable, "Upgrade installation is in progress")
			}
			return next(c)
		}
	}
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
	if r.routingDone != nil {
		select {
		case <-r.routingDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.accessDone != nil {
		select {
		case <-r.accessDone:
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
	return initAPIsWithClient(e, newTailscaleClient())
}

func initAPIsWithClient(e *echo.Echo, client *tailscale.Client, services ...graphQLServices) error {
	// Only login needs no header authentication
	e.POST("/api/login", handleLogin)

	initGraphQLAPI(e, newGraphQLServerWithClient(client, services...))
	return nil
}

func initGraphQLAPI(e *echo.Echo, gqlSrv *handler.Server) {
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
			var actionsMu sync.Mutex
			var afterResponse []func()
			defer func() {
				actionsMu.Lock()
				actions := append([]func(){}, afterResponse...)
				actionsMu.Unlock()
				if len(actions) == 0 {
					return
				}
				// The accepted installation is durable. Flush its GraphQL reply
				// before notifying main, even if the client has disconnected.
				_ = http.NewResponseController(c.Response()).Flush()
				for _, action := range actions {
					action()
				}
			}()
			ctx := context.WithValue(
				c.Request().Context(),
				graph.QUERY_CONTEXT_KEY,
				&graph.ContextValue{UserPID: claims.UserPID, AfterResponse: func(action func()) {
					actionsMu.Lock()
					afterResponse = append(afterResponse, action)
					actionsMu.Unlock()
				}})
			c.Response().Header().Set("Cache-Control", "no-store")
			gqlSrv.ServeHTTP(c.Response(), c.Request().WithContext(ctx))
			return nil
		},
		jwtMiddleware())
}
