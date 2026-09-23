package graph

import "errors"

var (
	ErrInvalidPassword = errors.New("New password must contain between 8 and 72 bytes")
)
