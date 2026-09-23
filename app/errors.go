package app

import "errors"

var (
	ErrInvalidCredentials = errors.New("Invalid username or password")
	ErrUnauthorized       = errors.New("Unauthorized error")
	ErrInvalidPassword    = errors.New("New password must contain between 8 and 72 bytes")
)
