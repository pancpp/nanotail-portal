package upgrade

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// WritePackage creates a package from local release inputs, calculating the
// authenticated file list. The caller must discard output if this returns an
// error, including when an input changes between hashing and packaging.
func WritePackage(output io.Writer, manifest Manifest, sources map[string]string, key ed25519.PrivateKey) error {
	if len(key) != ed25519.PrivateKeySize {
		return errors.New("invalid release private key")
	}
	if !SupportedPlatform(manifest.OS, manifest.Arch) {
		return fmt.Errorf("unsupported package platform %s/%s; releases require %s/%s", manifest.OS, manifest.Arch, TARGET_OS, TARGET_ARCH)
	}
	manifest.FormatVersion = 1
	manifest.Application = APPLICATION
	manifest.Files = make(map[string]PackageFile, len(sources))
	names := make([]string, 0, len(sources))
	for name, source := range sources {
		limit, allowed := fileLimit(name)
		if !allowed {
			return fmt.Errorf("unsupported package file %q", name)
		}
		file, err := os.Open(source)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
			file.Close()
			return fmt.Errorf("release input for %q must be a regular file within its size limit", name)
		}
		digest := sha256.New()
		n, err := io.Copy(digest, io.LimitReader(file, limit+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || n != info.Size() {
			return fmt.Errorf("could not hash release input %q", name)
		}
		manifest.Files[name] = PackageFile{SHA256: hex.EncodeToString(digest.Sum(nil)), Size: n}
		names = append(names, name)
	}
	if err := validateManifest(&manifest, manifest.OS, manifest.Arch); err != nil {
		return err
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || len(manifestBytes) > int(MAX_MANIFEST_BYTES) {
		return errors.New("manifest exceeds package size limit")
	}
	manifestBytes = append(manifestBytes, '\n')
	limited := &packageWriter{writer: output, remaining: MAX_PACKAGE_BYTES}
	zipped := gzip.NewWriter(limited)
	archive := tar.NewWriter(zipped)
	writeEntry := func(name string, contents []byte) error {
		if err := archive.WriteHeader(&tar.Header{Name: PACKAGE_PREFIX + name, Mode: 0644, Size: int64(len(contents)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		_, err := archive.Write(contents)
		return err
	}
	if err := writeEntry("manifest.json", manifestBytes); err != nil {
		return err
	}
	if err := writeEntry("manifest.sig", ed25519.Sign(key, manifestBytes)); err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		mode := int64(0644)
		if name == "payload/nanotail-portal" {
			mode = 0755
		}
		entry := manifest.Files[name]
		if err := archive.WriteHeader(&tar.Header{Name: PACKAGE_PREFIX + name, Mode: mode, Size: entry.Size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		file, err := os.Open(sources[name])
		if err != nil {
			return err
		}
		digest := sha256.New()
		n, err := io.Copy(io.MultiWriter(archive, digest), io.LimitReader(file, entry.Size+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || n != entry.Size || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
			return fmt.Errorf("release input %q changed during packaging", name)
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return zipped.Close()
}

type packageWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *packageWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errors.New("package exceeds compressed size limit")
	}
	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	return n, err
}
