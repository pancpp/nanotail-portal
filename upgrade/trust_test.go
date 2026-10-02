package upgrade

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestParseReleaseKeys(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	keys, err := ParsePublicKeys(append(append([]byte(nil), publicPEM...), publicPEM...))
	if err != nil || len(keys) != 2 || !bytes.Equal(keys[0], public) {
		t.Fatalf("public key bundle did not parse: %v", err)
	}
	key, err := ParsePrivateKey(privatePEM)
	if err != nil || !bytes.Equal(key, private) {
		t.Fatalf("private key did not parse: %v", err)
	}
	for name, data := range map[string][]byte{
		"empty":         nil,
		"private":       privatePEM,
		"trailing data": append(append([]byte(nil), publicPEM...), []byte("unrecognized trust data")...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePublicKeys(data); err == nil {
				t.Fatal("accepted invalid public key material")
			}
		})
	}
	if _, err := ParsePrivateKey(append(append([]byte(nil), privatePEM...), publicPEM...)); err == nil {
		t.Fatal("accepted trailing private key data")
	}
	ecdsaPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongDER, err := x509.MarshalPKIXPublicKey(&ecdsaPrivate.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePublicKeys(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: wrongDER})); err == nil {
		t.Fatal("accepted a non-Ed25519 release key")
	}
	if keys, err := TrustedKeys(); err != nil || len(keys) == 0 {
		t.Fatalf("embedded release trust key is invalid: %v", err)
	}
}

func TestKeyRotationAndMalformedKeys(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	oldPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	data := fixtureArchive(t, private, manifest, entries, false)
	keys := []ed25519.PublicKey{nil, oldPublic, public}
	if _, err := VerifyPackage(bytes.NewReader(data), keys, "linux", "arm64"); err != nil {
		t.Fatalf("valid rotating key was not accepted: %v", err)
	}
	if _, err := VerifyPackage(bytes.NewReader(data), keys[:2], "linux", "arm64"); err == nil {
		t.Fatal("accepted a signature with no matching trust key")
	}
}
