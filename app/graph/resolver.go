package graph

import (
	"context"

	"github.com/pancpp/nanotail-portal/device"
	"github.com/pancpp/nanotail-portal/tailscale"
)

// This file will not be regenerated automatically.
//
// It serves as dependency injection for your app, add any dependencies you require
// here.

type TailscaleStatusReader interface {
	Status(context.Context) (tailscale.Status, error)
}

type DeviceStatusReader interface {
	Status(context.Context) (device.Status, error)
}

type DeviceIPConfigurator interface {
	SetIP(context.Context, device.IPConfig) error
}

type Resolver struct {
	Tailscale    TailscaleStatusReader
	Device       DeviceStatusReader
	DeviceConfig DeviceIPConfigurator
}
