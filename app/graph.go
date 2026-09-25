package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/pancpp/nanotail-portal/tailscale"
	"github.com/pancpp/nanotail-portal/traffic"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func newGraphQLServer() *handler.Server {
	timeout, err := time.ParseDuration(conf.GetString("tailscale_timeout"))
	if err != nil || timeout <= 0 {
		timeout = 15 * time.Second
	}
	client := tailscale.NewClient(conf.GetString("tailscale_binary"), conf.GetString("tailscale_socket"), timeout, nil)
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{
		Tailscale: client, Routing: client, Connection: client, Device: device.NewReader(), DeviceConfig: device.NewConfigurator(), Traffic: device.NewTrafficReader(),
		TrafficHistory: traffic.NewStore(database.DB()),
	}}))
	srv.SetErrorPresenter(presentGraphQLError)

	srv.AddTransport(transport.Options{})
	srv.AddTransport(transport.GET{})
	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.MultipartForm{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](1000))
	srv.Use(extension.Introspection{})
	srv.Use(extension.AutomaticPersistedQuery{Cache: lru.New[string](100)})

	return srv
}

func presentGraphQLError(ctx context.Context, err error) *gqlerror.Error {
	presented := graphql.DefaultErrorPresenter(ctx, err)
	if presented == nil {
		return nil
	}
	if errors.Is(err, auth.ErrUnauthorized) || errors.Is(err, graph.ErrInvalidPassword) ||
		errors.Is(err, graph.ErrInvalidCredential) || errors.Is(err, graph.ErrTailscaleAdmin) ||
		errors.Is(err, graph.ErrTailscaleStatus) || errors.Is(err, graph.ErrDeviceStatus) || errors.Is(err, graph.ErrNetworkActivity) ||
		errors.Is(err, graph.ErrNetworkActivityHistory) ||
		errors.Is(err, graph.ErrDeviceAdmin) || errors.Is(err, device.ErrInvalidIP) ||
		errors.Is(err, device.ErrConfigBusy) || errors.Is(err, device.ErrConfigUnavailable) ||
		errors.Is(err, device.ErrConfigApply) || errors.Is(err, device.ErrConfigRecovery) ||
		errors.Is(err, graph.ErrRoutingAdmin) || errors.Is(err, tailscale.ErrRoutingUnavailable) ||
		errors.Is(err, tailscale.ErrExitNodeInvalid) || errors.Is(err, tailscale.ErrRoutingStopped) ||
		errors.Is(err, tailscale.ErrRoutingAdvertised) || errors.Is(err, tailscale.ErrRoutingApply) ||
		errors.Is(err, graph.ErrConnectionAdmin) || errors.Is(err, tailscale.ErrConnectionUnavailable) ||
		errors.Is(err, tailscale.ErrConnectionLogin) || errors.Is(err, tailscale.ErrConnectionApply) {
		return presented
	}
	switch presented.Extensions["code"] {
	case errcode.ParseFailed, errcode.ValidationFailed, "PERSISTED_QUERY_NOT_FOUND":
		// Persisted-query clients need the cache-miss code to retry with the query.
		return presented
	}

	// Keep internal details in server logs, never in response messages or extensions.
	log.Printf("(graphql) request failed: %v", err)
	return &gqlerror.Error{
		Message:   http.StatusText(http.StatusInternalServerError),
		Path:      presented.Path,
		Locations: presented.Locations,
	}
}
