// release-package generates offline signing keys and signed portal packages.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/pancpp/nanotail-portal/upgrade"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "release-package:", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: release-package keygen|create|verify [flags]")
	}
	switch args[0] {
	case "keygen":
		return keygen(args[1:], output)
	case "create":
		return create(args[1:], output)
	case "verify":
		return verify(args[1:], output)
	default:
		return fmt.Errorf("unknown command %q; use keygen, create, or verify", args[0])
	}
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func keygen(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	privatePath := flags.String("private-key", "", "new PKCS8 private key path (mode 0600)")
	publicPath := flags.String("public-key", "", "new PKIX public key path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *privatePath == "" || *publicPath == "" || flags.NArg() != 0 {
		return errors.New("keygen requires -private-key and -public-key; existing files are never overwritten")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return err
	}
	if err := writeExclusive(*privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		return err
	}
	if err := writeExclusive(*publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0644); err != nil {
		os.Remove(*privatePath)
		return err
	}
	fmt.Fprintf(output, "Created release signing keys. Public key SHA-256: %s\n", upgrade.KeyFingerprint(public))
	return nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("input exceeds size limit")
	}
	return data, nil
}

func create(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	binaryPath := flags.String("binary", "", "built portal executable")
	version := flags.String("version", "", "release semantic version, for example v1.2.3")
	platformOS := flags.String("os", upgrade.TARGET_OS, "target operating system (linux only)")
	arch := flags.String("arch", upgrade.TARGET_ARCH, "target architecture (arm64 only)")
	privatePath := flags.String("private-key", "", "offline PKCS8 Ed25519 private key")
	outputPath := flags.String("output", "", "new .tar.gz package path")
	notesPath := flags.String("release-notes", "", "optional UTF-8 release notes file")
	servicePath := flags.String("service", "", "optional systemd service template")
	nginxPath := flags.String("nginx", "", "optional nginx configuration template")
	licensePath := flags.String("license", "", "optional license text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *binaryPath == "" || *version == "" || *privatePath == "" || *outputPath == "" || flags.NArg() != 0 {
		return errors.New("create requires -binary, -version, -private-key, and -output")
	}
	if !upgrade.SupportedPlatform(*platformOS, *arch) {
		return fmt.Errorf("unsupported package platform %s/%s; releases require %s/%s", *platformOS, *arch, upgrade.TARGET_OS, upgrade.TARGET_ARCH)
	}
	keyBytes, err := readLimited(*privatePath, 16<<10)
	if err != nil {
		return err
	}
	privateKey, err := upgrade.ParsePrivateKey(keyBytes)
	if err != nil {
		return err
	}
	manifest := upgrade.Manifest{Version: *version, OS: *platformOS, Arch: *arch}
	sources := map[string]string{"payload/nanotail-portal": *binaryPath}
	if *notesPath != "" {
		notes, err := readLimited(*notesPath, upgrade.MAX_NOTES_BYTES)
		if err != nil {
			return err
		}
		if !utf8.Valid(notes) {
			return errors.New("release notes must be UTF-8 text")
		}
		manifest.ReleaseNotes = string(notes)
		sources["docs/release-notes.md"] = *notesPath
	}
	for name, path := range map[string]string{
		"integration/nanotail-portal.service": *servicePath,
		"integration/nanotail-portal.nginx":   *nginxPath,
		"docs/LICENSE":                        *licensePath,
	} {
		if path != "" {
			sources[name] = path
		}
	}
	file, err := os.OpenFile(*outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(*outputPath)
		}
	}()
	if err := upgrade.WritePackage(file, manifest, sources, privateKey); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	fmt.Fprintf(output, "Created signed release package %s for %s/%s (%s).\n", *outputPath, *platformOS, *arch, *version)
	return nil
}

func verify(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	packagePath := flags.String("package", "", "signed .tar.gz package")
	publicPath := flags.String("public-key", "", "PKIX public key PEM; defaults to the embedded trust key")
	platformOS := flags.String("os", upgrade.TARGET_OS, "expected operating system (linux only)")
	arch := flags.String("arch", upgrade.TARGET_ARCH, "expected architecture (arm64 only)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *packagePath == "" || flags.NArg() != 0 {
		return errors.New("verify requires -package")
	}
	if !upgrade.SupportedPlatform(*platformOS, *arch) {
		return fmt.Errorf("unsupported package platform %s/%s; releases require %s/%s", *platformOS, *arch, upgrade.TARGET_OS, upgrade.TARGET_ARCH)
	}
	keys, err := upgrade.TrustedKeys()
	if *publicPath != "" {
		var data []byte
		data, err = readLimited(*publicPath, 64<<10)
		if err == nil {
			keys, err = upgrade.ParsePublicKeys(data)
		}
	}
	if err != nil {
		return err
	}
	file, err := os.Open(*packagePath)
	if err != nil {
		return err
	}
	defer file.Close()
	manifest, err := upgrade.VerifyPackage(file, keys, *platformOS, *arch)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Verified %s %s for %s/%s.\n", manifest.Application, manifest.Version, manifest.OS, manifest.Arch)
	return nil
}
