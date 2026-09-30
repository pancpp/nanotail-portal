package graph

import (
	"context"
	"errors"

	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/device"
)

var ErrDeviceStatus = errors.New("Unable to read device status. Check that Linux system metrics, eth0, and NetworkManager (nmcli) are available")
var ErrDeviceAdmin = errors.New("Only portal administrators can change LAN settings")
var ErrHostnameAdmin = errors.New("Only portal administrators can change the device hostname")

func (r *mutationResolver) setDeviceHostname(ctx context.Context, input string) (bool, error) {
	if err := requireAdmin(ctx, ErrHostnameAdmin); err != nil {
		return false, err
	}
	hostname, err := device.ValidateHostname(input)
	if err != nil {
		return false, err
	}
	if r.DeviceHostname == nil {
		return false, device.ErrHostnameApply
	}
	err = r.DeviceHostname.SetHostname(ctx, hostname)
	return err == nil, err
}

func (r *mutationResolver) setDeviceIP(ctx context.Context, input *model.DeviceIP) (bool, error) {
	if err := requireAdmin(ctx, ErrDeviceAdmin); err != nil {
		return false, err
	}
	if input == nil {
		return false, device.ErrInvalidIP
	}
	config, err := device.ValidateIPConfig(device.IPConfig{Type: input.Type, IP: input.IP, Gateway: input.Gateway, DNS: input.DNS})
	if err != nil {
		return false, err
	}
	if r.DeviceConfig == nil {
		return false, device.ErrConfigUnavailable
	}
	err = r.DeviceConfig.SetIP(ctx, config)
	return err == nil, err
}

func (r *queryResolver) deviceStatus(ctx context.Context) (*model.DeviceStatus, error) {
	if r.Device == nil {
		return nil, ErrDeviceStatus
	}
	status, err := r.Device.Status(ctx)
	if err != nil {
		return nil, ErrDeviceStatus
	}
	return &model.DeviceStatus{
		Hostname: status.Hostname, LanIPType: status.LANIPType, LanIP: status.LANIP,
		Gateway: status.Gateway, DNS: status.DNS, LanIPv6Type: status.LANIPv6Type,
		LanIPv6: status.LANIPv6, Gateway6: status.Gateway6, EthAddr: status.EthAddr,
		Cpuload: status.CPULoad, Memory: status.Memory, LastRestart: status.LastRestart,
		Uptime: int(status.Uptime), Health: status.Health,
	}, nil
}
