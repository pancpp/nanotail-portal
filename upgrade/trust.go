package upgrade

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/pem"
	"errors"
)

// The public release signing key is part of the application, independent of the
// per-device JWT key and of metadata supplied by an upgrade package or feed.
//
//go:embed release-public.pem
var embeddedPublicKeys []byte

func TrustedKeys() ([]ed25519.PublicKey, error) {
	return ParsePublicKeys(embeddedPublicKeys)
}

// ParsePublicKeys supports a PEM bundle for deliberate release key rotation.
func ParsePublicKeys(data []byte) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for len(bytes.TrimSpace(data)) > 0 {
		if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("-----BEGIN PUBLIC KEY-----")) {
			return nil, errors.New("expected an Ed25519 public key in PKIX PEM format")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 {
			return nil, errors.New("invalid public key PEM")
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid PKIX public key")
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("release signing key must be Ed25519")
		}
		keys = append(keys, key)
		data = rest
	}
	if len(keys) == 0 {
		return nil, errors.New("no release public keys supplied")
	}
	return keys, nil
}

func ParsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("-----BEGIN PRIVATE KEY-----")) {
		return nil, errors.New("expected an Ed25519 private key in PKCS8 PEM format")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid PKCS8 private key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("release signing key must be Ed25519")
	}
	return key, nil
}

func KeyFingerprint(key ed25519.PublicKey) string {
	digest := sha256.Sum256(key)
	return hex.EncodeToString(digest[:])
}
