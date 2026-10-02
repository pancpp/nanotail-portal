// Package upgrade verifies release packages before they can be handed to an
// updater. Verification never extracts archive entries or executes their content.
package upgrade

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	MAX_PACKAGE_BYTES  int64 = 128 << 20
	MAX_UNPACKED_BYTES int64 = 256 << 20
	MAX_MANIFEST_BYTES int64 = 64 << 10
	MAX_NOTES_BYTES    int64 = 32 << 10
	APPLICATION              = "nanotail-portal"
	PACKAGE_PREFIX           = APPLICATION + "/"
	TARGET_OS                = "linux"
	TARGET_ARCH              = "arm64"
)

// SupportedPlatform identifies the only supported release and device target.
func SupportedPlatform(targetOS, targetArch string) bool {
	return targetOS == TARGET_OS && targetArch == TARGET_ARCH
}

// Manifest is authenticated by manifest.sig, an Ed25519 signature over the
// exact manifest.json bytes. Files are relative to the archive's fixed prefix.
type Manifest struct {
	FormatVersion int                    `json:"format_version"`
	Application   string                 `json:"application"`
	Version       string                 `json:"version"`
	OS            string                 `json:"os"`
	Arch          string                 `json:"arch"`
	ReleaseNotes  string                 `json:"release_notes,omitempty"`
	Files         map[string]PackageFile `json:"files"`
}

type PackageFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

var versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func ValidVersion(version string) bool {
	return len(version) <= 128 && versionPattern.MatchString(version) && semver.IsValid("v"+strings.TrimPrefix(version, "v"))
}

func CompareVersions(a, b string) (int, error) {
	if !ValidVersion(a) || !ValidVersion(b) {
		return 0, errors.New("versions must use major.minor.patch semantic versioning")
	}
	return semver.Compare("v"+strings.TrimPrefix(a, "v"), "v"+strings.TrimPrefix(b, "v")), nil
}

func fileLimit(name string) (int64, bool) {
	switch name {
	case "payload/nanotail-portal":
		return MAX_UNPACKED_BYTES - (1 << 20), true
	case "docs/release-notes.md":
		return MAX_NOTES_BYTES, true
	case "docs/LICENSE", "integration/nanotail-portal.service", "integration/nanotail-portal.nginx":
		return 64 << 10, true
	default:
		return 0, false
	}
}

func validateManifest(manifest *Manifest, targetOS, targetArch string) error {
	if manifest.FormatVersion != 1 || manifest.Application != APPLICATION {
		return errors.New("unsupported release package format or application")
	}
	if !ValidVersion(manifest.Version) {
		return errors.New("package version must use major.minor.patch semantic versioning")
	}
	if !SupportedPlatform(targetOS, targetArch) {
		return fmt.Errorf("unsupported device platform %s/%s; upgrades require %s/%s", targetOS, targetArch, TARGET_OS, TARGET_ARCH)
	}
	if !SupportedPlatform(manifest.OS, manifest.Arch) {
		return fmt.Errorf("unsupported package platform %s/%s; releases require %s/%s", manifest.OS, manifest.Arch, TARGET_OS, TARGET_ARCH)
	}
	if len(manifest.ReleaseNotes) > int(MAX_NOTES_BYTES) {
		return errors.New("release notes exceed package limit")
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > 5 {
		return errors.New("invalid package file list")
	}
	binary, ok := manifest.Files["payload/nanotail-portal"]
	if !ok || binary.Size <= 0 {
		return errors.New("package must contain a nonempty portal executable")
	}
	var total int64
	for name, file := range manifest.Files {
		limit, allowed := fileLimit(name)
		if !allowed || file.Size < 0 || file.Size > limit {
			return fmt.Errorf("invalid package file %q or size", name)
		}
		digest, err := hex.DecodeString(file.SHA256)
		if err != nil || len(digest) != sha256.Size || file.SHA256 != strings.ToLower(file.SHA256) {
			return fmt.Errorf("invalid SHA-256 checksum for %q", name)
		}
		total += file.Size
	}
	if total > MAX_UNPACKED_BYTES-(1<<20) {
		return errors.New("package contents exceed unpacked size limit")
	}
	return nil
}

func regularHeader(header *tar.Header) bool {
	return header.Typeflag == tar.TypeReg && header.Linkname == "" && len(header.PAXRecords) == 0 && header.Format != tar.FormatGNU && header.Mode&07000 == 0
}

func readMetadata(archive *tar.Reader, name string, limit int64) ([]byte, error) {
	header, err := archive.Next()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if header.Name != PACKAGE_PREFIX+name || !regularHeader(header) || header.Size < 0 || header.Size > limit {
		return nil, fmt.Errorf("expected a regular %s entry within its size limit", name)
	}
	return io.ReadAll(archive)
}

// VerifyPackage authenticates the manifest before interpreting it, then checks
// every payload checksum and archive boundary. The first two entries must be
// manifest.json and manifest.sig; all entries must be allowlisted regular files.
// Keys supplied inside a package are never used as a trust source.
func VerifyPackage(reader io.Reader, trustedKeys []ed25519.PublicKey, targetOS, targetArch string) (*Manifest, error) {
	if len(trustedKeys) == 0 {
		return nil, errors.New("no trusted release signing keys configured")
	}
	compressed := &io.LimitedReader{R: reader, N: MAX_PACKAGE_BYTES + 1}
	buffered := bufio.NewReader(compressed)
	zipped, err := gzip.NewReader(buffered)
	if err != nil {
		return nil, fmt.Errorf("invalid gzip package: %w", err)
	}
	defer zipped.Close()
	// Reject extra gzip members rather than accepting unsigned trailing archives.
	zipped.Multistream(false)
	unpacked := &io.LimitedReader{R: zipped, N: MAX_UNPACKED_BYTES + 1}
	archive := tar.NewReader(unpacked)
	manifestBytes, err := readMetadata(archive, "manifest.json", MAX_MANIFEST_BYTES)
	if err != nil {
		return nil, err
	}
	signature, err := readMetadata(archive, "manifest.sig", ed25519.SignatureSize)
	if err != nil {
		return nil, err
	}
	verified := false
	for _, key := range trustedKeys {
		if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, manifestBytes, signature) {
			verified = true
			break
		}
	}
	if !verified {
		return nil, errors.New("package signature is not from a trusted release signing key")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("invalid signed manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("unexpected content after signed manifest")
	}
	if err := validateManifest(&manifest, targetOS, targetArch); err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(manifest.Files))
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid package archive: %w", err)
		}
		name := strings.TrimPrefix(header.Name, PACKAGE_PREFIX)
		file, listed := manifest.Files[name]
		if !strings.HasPrefix(header.Name, PACKAGE_PREFIX) || !listed || seen[name] || !regularHeader(header) || header.Size != file.Size {
			return nil, fmt.Errorf("unlisted, duplicate, or unsafe archive entry %q", header.Name)
		}
		digest := sha256.New()
		if n, err := io.Copy(digest, archive); err != nil || n != file.Size {
			return nil, fmt.Errorf("incomplete package entry %q", name)
		}
		if hex.EncodeToString(digest.Sum(nil)) != file.SHA256 {
			return nil, fmt.Errorf("package checksum mismatch for %q", name)
		}
		seen[name] = true
	}
	if len(seen) != len(manifest.Files) {
		return nil, errors.New("package is missing files from its signed manifest")
	}
	// Fully drain the gzip stream to validate its checksum. Tar padding may only
	// contain zeroes, and it still counts towards the unpacked limit.
	padding := make([]byte, 32<<10)
	for {
		n, err := unpacked.Read(padding)
		for _, b := range padding[:n] {
			if b != 0 {
				return nil, errors.New("unexpected content after package archive")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid package compression checksum: %w", err)
		}
	}
	if unpacked.N <= 0 {
		return nil, errors.New("package exceeds unpacked size limit")
	}
	if _, err := buffered.ReadByte(); err != io.EOF {
		return nil, errors.New("unexpected content after compressed package")
	}
	if compressed.N <= 0 {
		return nil, errors.New("package exceeds compressed size limit")
	}
	return &manifest, nil
}
