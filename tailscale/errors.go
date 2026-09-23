package tailscale

import (
	"errors"
)

var (
	ErrUnavailable   = errors.New("Tailscale is unavailable; check the binary, daemon and operator permissions")
	ErrInvalidOutput = errors.New("Tailscale returned an invalid response")
	ErrInvalidConfig = errors.New("Invalid Tailscale configuration")
)
