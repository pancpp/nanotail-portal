package graph

import (
	"context"

	"github.com/pancpp/nanotail-portal/tailscale"
)

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

type TailscaleStatusReader interface {
	Status(context.Context) (tailscale.Status, error)
}

type Resolver struct {
	Tailscale TailscaleStatusReader
}
