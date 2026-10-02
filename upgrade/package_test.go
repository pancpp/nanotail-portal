package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testEntry struct {
	header tar.Header
	body   []byte
}

func packageFixture(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, Manifest, []testEntry) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("signed portal executable\n")
	digest := sha256.Sum256(payload)
	manifest := Manifest{
		FormatVersion: 1, Application: APPLICATION, Version: "v1.2.3", OS: "linux", Arch: "arm64",
		ReleaseNotes: "Improved portal upgrades.",
		Files:        map[string]PackageFile{"payload/nanotail-portal": {SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))}},
	}
	return public, private, manifest, []testEntry{{header: tar.Header{Name: PACKAGE_PREFIX + "payload/nanotail-portal", Mode: 0755, Size: int64(len(payload)), Typeflag: tar.TypeReg}, body: payload}}
}

func fixtureArchive(t *testing.T, private ed25519.PrivateKey, manifest Manifest, entries []testEntry, mutateSignature bool) []byte {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(private, data)
	if mutateSignature {
		signature[0] ^= 1
	}
	all := []testEntry{
		{header: tar.Header{Name: PACKAGE_PREFIX + "manifest.json", Mode: 0644, Size: int64(len(data)), Typeflag: tar.TypeReg}, body: data},
		{header: tar.Header{Name: PACKAGE_PREFIX + "manifest.sig", Mode: 0644, Size: int64(len(signature)), Typeflag: tar.TypeReg}, body: signature},
	}
	all = append(all, entries...)
	var output bytes.Buffer
	zipped := gzip.NewWriter(&output)
	archive := tar.NewWriter(zipped)
	for _, entry := range all {
		if err := archive.WriteHeader(&entry.header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestVerifySignedPackage(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	data := fixtureArchive(t, private, manifest, entries, false)
	got, err := VerifyPackage(bytes.NewReader(data), []ed25519.PublicKey{public}, "linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != manifest.Version || got.ReleaseNotes != manifest.ReleaseNotes {
		t.Fatalf("unexpected verified metadata: %#v", got)
	}
	if _, err := VerifyPackage(bytes.NewReader(data), []ed25519.PublicKey{public}, "linux", "amd64"); err == nil {
		t.Fatal("accepted a package for another architecture")
	}
	if _, err := VerifyPackage(bytes.NewReader(data), nil, "linux", "arm64"); err == nil {
		t.Fatal("accepted a package without trusted keys")
	}
}

func TestRejectUntrustedOrTamperedPackage(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data []byte
		key  ed25519.PublicKey
	}{
		{"wrong signing key", fixtureArchive(t, private, manifest, entries, false), otherPublic},
		{"changed signature", fixtureArchive(t, private, manifest, entries, true), public},
	}
	entries[0].body[0] ^= 1
	tests = append(tests, struct {
		name string
		data []byte
		key  ed25519.PublicKey
	}{"changed payload", fixtureArchive(t, private, manifest, entries, false), public})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := VerifyPackage(bytes.NewReader(test.data), []ed25519.PublicKey{test.key}, "linux", "arm64"); err == nil {
				t.Fatal("accepted tampered or untrusted package")
			}
		})
	}
}

func TestRejectUnsafeArchiveEntries(t *testing.T) {
	for _, name := range []string{"traversal", "absolute path", "unlisted", "duplicate", "symlink", "hardlink", "missing", "setuid", "signed traversal", "oversized file"} {
		t.Run(name, func(t *testing.T) {
			public, private, manifest, entries := packageFixture(t)
			switch name {
			case "traversal":
				entries[0].header.Name = PACKAGE_PREFIX + "payload/../../nanotail-portal"
			case "absolute path":
				entries[0].header.Name = "/nanotail-portal/payload/nanotail-portal"
			case "unlisted":
				entries = append(entries, testEntry{header: tar.Header{Name: PACKAGE_PREFIX + "install.sh", Mode: 0755, Typeflag: tar.TypeReg}})
			case "duplicate":
				entries = append(entries, entries[0])
			case "symlink", "hardlink":
				entries[0].header.Typeflag = tar.TypeSymlink
				if name == "hardlink" {
					entries[0].header.Typeflag = tar.TypeLink
				}
				entries[0].header.Linkname = "/etc/passwd"
				entries[0].header.Size = 0
				entries[0].body = nil
			case "missing":
				entries = nil
			case "setuid":
				entries[0].header.Mode = 04755
			case "signed traversal":
				manifest.Files["../../etc/passwd"] = manifest.Files["payload/nanotail-portal"]
			case "oversized file":
				file := manifest.Files["payload/nanotail-portal"]
				file.Size = MAX_UNPACKED_BYTES + 1
				manifest.Files["payload/nanotail-portal"] = file
			}
			data := fixtureArchive(t, private, manifest, entries, false)
			if _, err := VerifyPackage(bytes.NewReader(data), []ed25519.PublicKey{public}, "linux", "arm64"); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
}

func TestRejectDamagedOrTrailingCompression(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	data := fixtureArchive(t, private, manifest, entries, false)
	corrupt := append([]byte(nil), data...)
	corrupt[len(corrupt)-8] ^= 1
	for name, invalid := range map[string][]byte{
		"truncated": data[:len(data)-4],
		"checksum":  corrupt,
		"suffix":    append(append([]byte(nil), data...), []byte("unsigned extra content")...),
		"multigzip": append(append([]byte(nil), data...), data...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyPackage(bytes.NewReader(invalid), []ed25519.PublicKey{public}, "linux", "arm64"); err == nil {
				t.Fatal("accepted damaged or extended gzip")
			}
		})
	}
}

func TestCreateVerifyRoundTrip(t *testing.T) {
	public, private, manifest, _ := packageFixture(t)
	dir := t.TempDir()
	binary := filepath.Join(dir, "portal")
	notes := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(binary, []byte("local built executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte(manifest.ReleaseNotes), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WritePackage(&output, manifest, map[string]string{"payload/nanotail-portal": binary, "docs/release-notes.md": notes}, private); err != nil {
		t.Fatal(err)
	}
	got, err := VerifyPackage(bytes.NewReader(output.Bytes()), []ed25519.PublicKey{public}, "linux", "arm64")
	if err != nil || got.Version != manifest.Version || len(got.Files) != 2 {
		t.Fatalf("created package did not verify: %#v, %v", got, err)
	}
}

func TestRejectUnsupportedPackagePlatforms(t *testing.T) {
	public, private, manifest, entries := packageFixture(t)
	keys := []ed25519.PublicKey{public}
	validArchive := fixtureArchive(t, private, manifest, entries, false)
	binary := filepath.Join(t.TempDir(), "portal")
	if err := os.WriteFile(binary, entries[0].body, 0700); err != nil {
		t.Fatal(err)
	}
	for _, platform := range [][2]string{
		{"linux", "amd64"}, {"linux", "riscv64"}, {"linux", "arm"},
		{"darwin", "arm64"}, {"windows", "arm64"}, {"", "arm64"}, {"linux", ""},
	} {
		t.Run(platform[0]+"/"+platform[1], func(t *testing.T) {
			unsupported := manifest
			unsupported.OS, unsupported.Arch = platform[0], platform[1]
			var output bytes.Buffer
			if err := WritePackage(&output, unsupported, map[string]string{"payload/nanotail-portal": binary}, private); err == nil {
				t.Fatal("created a release for an unsupported platform")
			}
			if output.Len() != 0 {
				t.Fatal("wrote package bytes before rejecting an unsupported platform")
			}
			// A trusted signature cannot make an unsupported target acceptable,
			// even when the caller requests exactly the signed platform.
			unsupportedArchive := fixtureArchive(t, private, unsupported, entries, false)
			for _, target := range [][2]string{platform, {TARGET_OS, TARGET_ARCH}} {
				if _, err := VerifyPackage(bytes.NewReader(unsupportedArchive), keys, target[0], target[1]); err == nil {
					t.Fatalf("accepted unsupported signed platform for target %s/%s", target[0], target[1])
				}
			}
			if _, err := VerifyPackage(bytes.NewReader(validArchive), keys, platform[0], platform[1]); err == nil {
				t.Fatal("accepted a supported package on an unsupported device")
			}
		})
	}
}

func TestSemanticVersionComparison(t *testing.T) {
	for _, invalid := range []string{"", "latest", "v1", "1.2", "1.02.3", "1.2.3-01", "../1.2.3", strings.Repeat("1", 129) + ".2.3"} {
		if ValidVersion(invalid) {
			t.Errorf("accepted invalid semantic version %q", invalid)
		}
	}
	for _, test := range []struct {
		a, b string
		want int
	}{
		{"1.2.3", "v1.2.3+build.2", 0},
		{"1.2.3-rc.2", "1.2.3-rc.10", -1},
		{"1.2.3", "1.2.3-rc.1", 1},
		{"1.10.0", "1.9.0", 1},
	} {
		got, err := CompareVersions(test.a, test.b)
		if err != nil || got != test.want {
			t.Errorf("compare %q and %q: %d, %v", test.a, test.b, got, err)
		}
	}
}
