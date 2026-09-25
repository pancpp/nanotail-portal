package graph

import (
	"context"
	"time"

	"github.com/pancpp/nanotail-portal/device"
	"github.com/pancpp/nanotail-portal/tailscale"
	"github.com/pancpp/nanotail-portal/traffic"
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

type NetworkActivityReader interface {
	Sample(context.Context) (device.TrafficSample, error)
}

type TailscaleRouter interface {
	Routing(context.Context) (tailscale.Routing, error)
	SetRouting(context.Context, []string) error
}

type RoutingHostReader interface {
	RoutingStatus(context.Context) (device.RoutingStatus, error)
}

type NetworkHistoryReader interface {
	History(context.Context, time.Time) (traffic.History, error)
}

type TailscaleConnector interface {
	Connection(context.Context) (tailscale.Connection, error)
	SetEnabled(context.Context, bool) error
	Logout(context.Context) error
}

type TailscaleKeyRenewer interface {
	KeyRenewal(context.Context) (tailscale.KeyRenewal, error)
	RenewNodeKey(context.Context) (tailscale.KeyRenewal, error)
	BeginNodeKeyRenewal(context.Context, string) (tailscale.KeyRenewal, error)
	CancelNodeKeyRenewal(context.Context, string) (tailscale.KeyRenewal, error)
}

type OAuthCredentialWriter interface {
	UpdateOAuthCredentials(context.Context, func(context.Context) error) error
}

type Resolver struct {
	CredentialWriter OAuthCredentialWriter
	Tailscale        TailscaleStatusReader
	Device           DeviceStatusReader
	DeviceConfig     DeviceIPConfigurator
	Traffic          NetworkActivityReader
	Routing          TailscaleRouter
	RoutingHost      RoutingHostReader
	TrafficHistory   NetworkHistoryReader
	Connection       TailscaleConnector
	KeyRenewer       TailscaleKeyRenewer
}
