package app

import (
	"context"
	"errors"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

var (
	errGraphQLMaintenance = errors.New("Upgrade installation or factory reset is in progress")
	errInstallOperation   = errors.New("Install an upgrade in a separate mutation containing only installUpgrade")
)

func configureUpgradeOperations(server *handler.Server, services graphQLServices) {
	server.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		operation := graphql.GetOperationContext(ctx)
		fields := graphql.CollectFields(operation, operation.Operation.SelectionSet, nil)
		reject := func(err error) graphql.ResponseHandler {
			return graphql.OneShot(&graphql.Response{Errors: gqlerror.List{presentGraphQLError(ctx, err)}})
		}
		pending := (services.reset != nil && services.reset.Pending()) || (services.installer != nil && services.installer.Pending())
		if pending {
			if operation.Operation.Operation != ast.Query || len(fields) == 0 {
				return reject(errGraphQLMaintenance)
			}
			for _, field := range fields {
				if field.Name != "upgradeStatus" {
					return reject(errGraphQLMaintenance)
				}
			}
		}
		// A restart cannot safely share a mutation with other side effects.
		// Inspect collected fields, so aliases, fragments, and directives obey
		// the same rule as a plain installUpgrade selection.
		if operation.Operation.Operation == ast.Mutation && len(fields) != 1 {
			for _, field := range fields {
				if field.Name == "installUpgrade" {
					return reject(errInstallOperation)
				}
			}
		}
		return next(ctx)
	})
}
