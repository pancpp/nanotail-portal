package app

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func newGraphQLServer() *handler.Server {
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: &graph.Resolver{}}))
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
	if errors.Is(err, auth.ErrUnauthorized) || errors.Is(err, graph.ErrInvalidPassword) {
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
