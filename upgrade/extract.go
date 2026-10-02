package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ExtractPackage authenticates a private snapshot of archive, then extracts a
// release into a new directory. The destination parent must already exist; an
// existing destination, including a symlink, is never replaced. The caller owns
// archive, which remains open and retains its current seek position.
//
// The snapshot is immediately unlinked and only this function holds its file
// descriptor. Replacing the input path or modifying its inode between signature
// verification and extraction cannot change the authenticated extraction input.
// Supplying one expectedDigest additionally binds the snapshot to the exact
// lowercase SHA-256 selected by the administrator, including across input writes.
func ExtractPackage(ctx context.Context, archive *os.File, dest string, keys []ed25519.PublicKey, targetOS, targetArch string, expectedDigest ...string) (_ *Manifest, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if archive == nil {
		return nil, ErrInvalidPackage
	}
	expectedHash := ""
	if len(expectedDigest) > 1 {
		return nil, fmt.Errorf("%w: expected one archive checksum", ErrInvalidPackage)
	}
	if len(expectedDigest) == 1 {
		expectedHash = expectedDigest[0]
		digest, decodeErr := hex.DecodeString(expectedHash)
		if decodeErr != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != expectedHash {
			return nil, fmt.Errorf("%w: invalid expected archive checksum", ErrInvalidPackage)
		}
	}
	info, err := archive.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("%w: archive must be a nonempty regular file", ErrInvalidPackage)
	}
	if info.Size() > MAX_PACKAGE_BYTES {
		return nil, ErrTooLarge
	}
	if dest == "" {
		return nil, errors.New("release destination must not be empty")
	}
	destination, err := filepath.Abs(dest)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(destination)
	if name == "." || name == string(filepath.Separator) {
		return nil, errors.New("release destination must name a new directory")
	}
	parent, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return nil, fmt.Errorf("open release parent: %w", err)
	}
	defer parent.Close()
	if _, err := parent.Lstat(name); err == nil {
		return nil, fmt.Errorf("release destination already exists: %w", os.ErrExist)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	parentDirectory, err := parent.Open(".")
	if err != nil {
		return nil, err
	}
	defer parentDirectory.Close()
	privateName := ".extract-" + rand.Text()
	if err := parent.Mkdir(privateName, 0700); err != nil {
		return nil, fmt.Errorf("create private extraction directory: %w", err)
	}
	cleanupName := privateName
	complete := false
	defer func() {
		if !complete {
			if cleanupErr := parent.RemoveAll(cleanupName); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove incomplete release: %w", cleanupErr))
			}
		}
	}()
	root, err := parent.OpenRoot(privateName)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	snapshot, err := root.OpenFile(".archive", os.O_RDWR|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	if err := root.Remove(".archive"); err != nil {
		return nil, err
	}
	// ReaderAt avoids sharing the caller's seek position. Input changes during
	// this bounded copy can only produce another valid signed package or fail
	// verification; extraction never reads the caller's inode again.
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(snapshot, digest), extractionReader{ctx: ctx, reader: io.NewSectionReader(archive, 0, MAX_PACKAGE_BYTES+1)})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if size > MAX_PACKAGE_BYTES {
		return nil, ErrTooLarge
	}
	if expectedHash != "" && hex.EncodeToString(digest.Sum(nil)) != expectedHash {
		return nil, fmt.Errorf("%w: archive does not match the selected checksum", ErrInvalidPackage)
	}
	verified, err := VerifyPackage(extractionReader{ctx: ctx, reader: io.NewSectionReader(snapshot, 0, size)}, keys, targetOS, targetArch)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	if err := extractSnapshot(ctx, snapshot, size, root, verified); err != nil {
		return nil, err
	}
	if err := snapshot.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Both names are confined to the same pinned parent directory. NOREPLACE
	// closes the final existence-check race without ever overwriting a release.
	if err := unix.Renameat2(int(parentDirectory.Fd()), privateName, int(parentDirectory.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
		return nil, fmt.Errorf("publish extracted release: %w", err)
	}
	cleanupName = name
	if err := parentDirectory.Sync(); err != nil {
		return nil, fmt.Errorf("sync release parent: %w", err)
	}
	complete = true
	return verified, nil
}

func extractSnapshot(ctx context.Context, snapshot *os.File, size int64, root *os.Root, manifest *Manifest) error {
	zipped, err := gzip.NewReader(extractionReader{ctx: ctx, reader: io.NewSectionReader(snapshot, 0, size)})
	if err != nil {
		return err
	}
	defer zipped.Close()
	zipped.Multistream(false)
	unpacked := &io.LimitedReader{R: zipped, N: MAX_UNPACKED_BYTES + 1}
	archive := tar.NewReader(unpacked)
	for _, metadata := range []struct {
		name  string
		limit int64
	}{{"manifest.json", MAX_MANIFEST_BYTES}, {"manifest.sig", ed25519.SignatureSize}} {
		data, err := readMetadata(archive, metadata.name, metadata.limit)
		if err != nil {
			return err
		}
		if err := writeExtractedFile(ctx, root, metadata.name, bytes.NewReader(data), int64(len(data)), "", 0644); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(manifest.Files))
	directories := make(map[string]bool)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(header.Name, PACKAGE_PREFIX)
		file, listed := manifest.Files[name]
		if !strings.HasPrefix(header.Name, PACKAGE_PREFIX) || !listed || seen[name] || !regularHeader(header) || header.Size != file.Size {
			return fmt.Errorf("%w: invalid verified archive entry", ErrInvalidPackage)
		}
		_, allowed := fileLimit(name)
		if !allowed {
			return fmt.Errorf("%w: invalid release file", ErrInvalidPackage)
		}
		destination := name
		mode := os.FileMode(0644)
		if name == "payload/nanotail-portal" {
			destination, mode = "nanotail-portal", 0755
		} else {
			directory := filepath.Dir(destination)
			if !directories[directory] {
				if err := root.Mkdir(directory, 0700); err != nil {
					return err
				}
				directories[directory] = true
			}
		}
		if err := writeExtractedFile(ctx, root, destination, archive, file.Size, file.SHA256, mode); err != nil {
			return err
		}
		seen[name] = true
	}
	if len(seen) != len(manifest.Files) || unpacked.N <= 0 {
		return fmt.Errorf("%w: incomplete verified archive", ErrInvalidPackage)
	}
	for directory := range directories {
		if err := syncExtractedDirectory(root, directory); err != nil {
			return err
		}
	}
	return syncExtractedDirectory(root, ".")
}

func writeExtractedFile(ctx context.Context, root *os.Root, name string, contents io.Reader, size int64, expectedHash string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("create release file %s: %w", name, err)
	}
	defer file.Close()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, digest), extractionReader{ctx: ctx, reader: io.LimitReader(contents, size+1)})
	if err != nil {
		return err
	}
	if n != size || (expectedHash != "" && hex.EncodeToString(digest.Sum(nil)) != expectedHash) {
		return fmt.Errorf("%w: extracted file checksum mismatch", ErrInvalidPackage)
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

func syncExtractedDirectory(root *os.Root, name string) error {
	directory, err := root.OpenFile(name, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Chmod(0755); err != nil {
		return err
	}
	return directory.Sync()
}

type extractionReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r extractionReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
