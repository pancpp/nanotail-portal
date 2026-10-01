package access

import "errors"

var (
	ErrInvalidDeviceID            = errors.New("Invalid Device ID error")
	ErrDeviceSignatureUnavailable = errors.New("Device signature is not available yet")
	ErrInvalidAPIURL              = errors.New("Invalid access API prefix")
	ErrInvalidInterface           = errors.New("Access network interface is empty")
	ErrReportRejected             = errors.New("Server rejected IP report")
	ErrFindLanIP                  = errors.New("Failed to find the LAN IP")
	ErrAPIHTTPStatus              = errors.New("Unexpected API HTTP status")
	ErrAPIResponseTooLarge        = errors.New("API response exceeds maximum size")
	ErrInvalidAPIResponse         = errors.New("Invalid API response")
	ErrInvalidAddressDump         = errors.New("Invalid address metadata")
	ErrIncompleteAddressDump      = errors.New("Incomplete address metadata")
)
