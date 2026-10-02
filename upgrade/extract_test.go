package upgrade

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func extractionArchive(t *testing.T, contents []byte) *os.File {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "package.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	if _, err := file.Write(contents); err != nil {
		t.Fatal(err)
	}
	// Intentionally leave the caller's offset at EOF. Extraction must use the
	// pinned file descriptor from offset zero without consuming that position.
	return file
}

func assertExtractionParentEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("incomplete extraction left filesystem entries: %v, %v", entries, err)
	}
}

func TestExtractPackageLayoutAndModes(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	contents := map[string][]byte{
		"nanotail-portal":                     entries[0].body,
		"docs/release-notes.md":               []byte("Signed release notes\n"),
		"docs/LICENSE":                        []byte("Release license\n"),
		"integration/nanotail-portal.service": []byte("[Service]\n"),
		"integration/nanotail-portal.nginx":   []byte("server {}\n"),
	}
	entries[0].header.Mode = 0777
	for name, data := range contents {
		if name == "nanotail-portal" {
			continue
		}
		digest := sha256.Sum256(data)
		manifest.Files[name] = PackageFile{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
		entries = append(entries, testEntry{header: tar.Header{Name: PACKAGE_PREFIX + name, Size: int64(len(data)), Mode: 0777, Typeflag: tar.TypeReg}, body: data})
	}
	data := fixtureArchive(t, private, manifest, entries, false)
	archive := extractionArchive(t, data)
	parent := t.TempDir()
	destination := filepath.Join(parent, "v1.2.3")
	digest := sha256.Sum256(data)
	got, err := ExtractPackage(t.Context(), archive, destination, []ed25519.PublicKey{public}, "linux", "arm64", hex.EncodeToString(digest[:]))
	if err != nil || got.Version != manifest.Version {
		t.Fatalf("extraction failed: %+v, %v", got, err)
	}
	for name, want := range contents {
		path := filepath.Join(destination, name)
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, want) {
			t.Fatalf("wrong extracted %s: %q, %v", name, actual, err)
		}
		info, err := os.Stat(path)
		mode := os.FileMode(0644)
		if name == "nanotail-portal" {
			mode = 0755
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
			t.Fatalf("wrong permissions for %s: %v, %v", name, info, err)
		}
	}
	for _, name := range []string{".", "docs", "integration"} {
		info, err := os.Stat(filepath.Join(destination, name))
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0755 {
			t.Fatalf("wrong directory mode for %s: %v, %v", name, info, err)
		}
	}
	manifestBytes, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(destination, "manifest.sig"))
	if err != nil || !ed25519.Verify(public, manifestBytes, signature) {
		t.Fatalf("retained audit metadata is not authentic: %v", err)
	}
	var retained Manifest
	if err := json.Unmarshal(manifestBytes, &retained); err != nil || retained.Version != manifest.Version {
		t.Fatalf("invalid retained manifest: %v", err)
	}
	if position, err := archive.Seek(0, io.SeekCurrent); err != nil || position != int64(len(data)) {
		t.Fatalf("changed caller's file position: %d, %v", position, err)
	}
	files, err := os.ReadDir(parent)
	if err != nil || len(files) != 1 || files[0].Name() != "v1.2.3" {
		t.Fatalf("left private extraction files behind: %v, %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".archive")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private archive was retained: %v", err)
	}
}

func TestExtractPackageRejectsUnsafeInputsWithoutPublishing(t *testing.T) {
	for _, name := range []string{"signature", "payload", "wrong platform", "symlink", "hardlink", "traversal", "duplicate", "unlisted", "truncated", "gzip suffix"} {
		t.Run(name, func(t *testing.T) {
			public, private, manifest, entries := packageFixture(t)
			arch := "arm64"
			switch name {
			case "payload":
				entries[0].body[0] ^= 1
			case "wrong platform":
				arch = "amd64"
			case "symlink", "hardlink":
				entries[0].header.Typeflag = tar.TypeSymlink
				if name == "hardlink" {
					entries[0].header.Typeflag = tar.TypeLink
				}
				entries[0].header.Linkname = "/etc/passwd"
				entries[0].header.Size, entries[0].body = 0, nil
			case "traversal":
				entries[0].header.Name = PACKAGE_PREFIX + "../../outside"
			case "duplicate":
				entries = append(entries, entries[0])
			case "unlisted":
				entries = append(entries, testEntry{header: tar.Header{Name: PACKAGE_PREFIX + "install.sh", Mode: 0755, Typeflag: tar.TypeReg}})
			}
			data := fixtureArchive(t, private, manifest, entries, name == "signature")
			if name == "truncated" {
				data = data[:len(data)-8]
			}
			if name == "gzip suffix" {
				data = append(data, []byte("unverified extra data")...)
			}
			parent := t.TempDir()
			got, err := ExtractPackage(t.Context(), extractionArchive(t, data), filepath.Join(parent, "release"), []ed25519.PublicKey{public}, "linux", arch)
			if got != nil || !errors.Is(err, ErrInvalidPackage) {
				t.Fatalf("accepted invalid package: %+v, %v", got, err)
			}
			assertExtractionParentEmpty(t, parent)
		})
	}
}

func TestExtractPackagePreservesExistingDestination(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			public, private, manifest, entries := packageFixture(t)
			parent := t.TempDir()
			destination := filepath.Join(parent, "release")
			sentinel := filepath.Join(t.TempDir(), "keep")
			if err := os.WriteFile(sentinel, []byte("existing data"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(destination, 0700)
			case "file":
				err = os.WriteFile(destination, []byte("existing release"), 0600)
			case "symlink":
				err = os.Symlink(sentinel, destination)
			case "dangling symlink":
				err = os.Symlink(sentinel+".missing", destination)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(destination)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ExtractPackage(t.Context(), extractionArchive(t, fixtureArchive(t, private, manifest, entries, false)), destination, []ed25519.PublicKey{public}, "linux", "arm64")
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("existing destination was not rejected: %v", err)
			}
			after, err := os.Lstat(destination)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("existing destination changed: %v", err)
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "existing data" {
				t.Fatal("modified a symlink target")
			}
		})
	}
}

// Observe filesystem progress at cancellation checks, which lets these tests
// deterministically interrupt extraction after it has started writing files.
type extractionObservedContext struct {
	context.Context
	observe func()
}

func (ctx extractionObservedContext) Err() error {
	ctx.observe()
	return ctx.Context.Err()
}

func partialExtractionDirectory(parent string) string {
	matches, _ := filepath.Glob(filepath.Join(parent, ".extract-*", "manifest.json"))
	if len(matches) == 0 {
		return ""
	}
	return filepath.Dir(matches[0])
}

func TestExtractPackageCleansPartialCancellation(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	interrupted := false
	observed := extractionObservedContext{Context: ctx, observe: func() {
		if partialExtractionDirectory(parent) != "" {
			interrupted = true
			cancel()
		}
	}}
	_, err := ExtractPackage(observed, extractionArchive(t, fixtureArchive(t, private, manifest, entries, false)), filepath.Join(parent, "release"), []ed25519.PublicKey{public}, "linux", "arm64")
	if !interrupted || !errors.Is(err, context.Canceled) {
		t.Fatalf("did not cancel a partial extraction: interrupted=%v, error=%v", interrupted, err)
	}
	assertExtractionParentEmpty(t, parent)
}

func TestExtractPackagePinsVerifiedSnapshot(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	archive := extractionArchive(t, fixtureArchive(t, private, manifest, entries, false))
	parent := t.TempDir()
	changed := false
	observed := extractionObservedContext{Context: t.Context(), observe: func() {
		if changed || partialExtractionDirectory(parent) == "" {
			return
		}
		changed = true
		// Change both the open input inode and what its pathname now resolves
		// to, after verification but before payload extraction has completed.
		if err := archive.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(archive.Name(), archive.Name()+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archive.Name(), []byte("untrusted replacement"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	destination := filepath.Join(parent, "release")
	if _, err := ExtractPackage(observed, archive, destination, []ed25519.PublicKey{public}, "linux", "arm64"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "nanotail-portal"))
	if !changed || err != nil || !bytes.Equal(data, entries[0].body) {
		t.Fatalf("extraction did not use its authenticated snapshot: changed=%v data=%q error=%v", changed, data, err)
	}
}

func TestExtractPackageBindsSelectedDigestDuringSnapshotCopy(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	selected := fixtureArchive(t, private, manifest, entries, false)
	digest := sha256.Sum256(selected)
	archive := extractionArchive(t, selected)
	// This replacement is signed by the same trusted key, has the same version,
	// and passes ordinary package verification. Only the selected hash differs.
	entries[0].body = []byte("another trusted executable with the same release version")
	entries[0].header.Size = int64(len(entries[0].body))
	payloadHash := sha256.Sum256(entries[0].body)
	manifest.Files["payload/nanotail-portal"] = PackageFile{Size: entries[0].header.Size, SHA256: hex.EncodeToString(payloadHash[:])}
	alternative := fixtureArchive(t, private, manifest, entries, false)
	if _, err := VerifyPackage(bytes.NewReader(alternative), []ed25519.PublicKey{public}, "linux", "arm64"); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	changed := false
	observed := extractionObservedContext{Context: t.Context(), observe: func() {
		if changed {
			return
		}
		matches, _ := filepath.Glob(filepath.Join(parent, ".extract-*"))
		if len(matches) == 0 {
			return
		}
		changed = true
		if _, err := archive.WriteAt(alternative, 0); err != nil {
			t.Fatal(err)
		}
		if err := archive.Truncate(int64(len(alternative))); err != nil {
			t.Fatal(err)
		}
	}}
	_, err := ExtractPackage(observed, archive, filepath.Join(parent, "release"), []ed25519.PublicKey{public}, "linux", "arm64", hex.EncodeToString(digest[:]))
	if !changed || !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("extracted a different signed package than selected: changed=%v error=%v", changed, err)
	}
	assertExtractionParentEmpty(t, parent)
}

func TestExtractPackageRejectsInjectedSymlinkAndCleansPartialFiles(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	parent := t.TempDir()
	protected := filepath.Join(t.TempDir(), "protected")
	if err := os.WriteFile(protected, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	injected := false
	observed := extractionObservedContext{Context: t.Context(), observe: func() {
		partial := partialExtractionDirectory(parent)
		if injected || partial == "" {
			return
		}
		injected = true
		if err := os.Symlink(protected, filepath.Join(partial, "nanotail-portal")); err != nil {
			t.Fatal(err)
		}
	}}
	_, err := ExtractPackage(observed, extractionArchive(t, fixtureArchive(t, private, manifest, entries, false)), filepath.Join(parent, "release"), []ed25519.PublicKey{public}, "linux", "arm64")
	if !injected || err == nil {
		t.Fatalf("injected symlink was accepted: %v", err)
	}
	if data, err := os.ReadFile(protected); err != nil || string(data) != "keep this file" {
		t.Fatal("extraction followed a symlink")
	}
	assertExtractionParentEmpty(t, parent)
}

func TestExtractPackageNeverReplacesRacingDestination(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	parent := t.TempDir()
	destination := filepath.Join(parent, "release")
	created := false
	observed := extractionObservedContext{Context: t.Context(), observe: func() {
		if created || partialExtractionDirectory(parent) == "" {
			return
		}
		created = true
		if err := os.Mkdir(destination, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, "keep"), []byte("other release"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	_, err := ExtractPackage(observed, extractionArchive(t, fixtureArchive(t, private, manifest, entries, false)), destination, []ed25519.PublicKey{public}, "linux", "arm64")
	if !created || !errors.Is(err, os.ErrExist) {
		t.Fatalf("racing destination was replaced: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "keep")); err != nil || string(data) != "other release" {
		t.Fatal("racing destination contents changed")
	}
	files, err := os.ReadDir(parent)
	if err != nil || len(files) != 1 || files[0].Name() != "release" {
		t.Fatalf("partial extraction leaked: %v, %v", files, err)
	}
}

func TestExtractPackageRejectsNonregularAndOversizedArchives(t *testing.T) {
	parent := t.TempDir()
	archive, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if _, err := ExtractPackage(t.Context(), archive, filepath.Join(parent, "release"), nil, "linux", "arm64"); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("accepted directory input: %v", err)
	}
	archive = extractionArchive(t, []byte("oversized"))
	if err := archive.Truncate(MAX_PACKAGE_BYTES + 1); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractPackage(t.Context(), archive, filepath.Join(parent, "release"), nil, "linux", "arm64"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("accepted oversized input: %v", err)
	}
	assertExtractionParentEmpty(t, parent)
}
