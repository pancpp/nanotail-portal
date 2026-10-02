package access

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	VENDOR_STORAGE_PATH   = "/dev/mmcblk0"
	VENDOR_STORAGE_OFFSET = 0x380000
	VENDOR_SLOT_SIZE      = 65536
	VENDOR_SLOT_COUNT     = 4
	VENDOR_STORAGE_SIZE   = VENDOR_SLOT_SIZE * VENDOR_SLOT_COUNT
	VENDOR_DATA_OFFSET    = 1024
	VENDOR_DATA_SIZE      = VENDOR_SLOT_SIZE - VENDOR_DATA_OFFSET - 8
	VENDOR_MAX_ITEMS      = 126
	VENDOR_STORAGE_TAG    = 0x524b5644
	VENDOR_CREDENTIAL_ID  = 0x80
	DEVICE_ID_BYTES       = 8
	DEVICE_SIGNATURE_SIZE = 64
)

// GetDeviceCredentials reads the stored device ID and signature for the reporter.
// Signature authentication is performed by the server.
func GetDeviceCredentials() (string, string, error) {
	storage, err := os.Open(VENDOR_STORAGE_PATH)
	if err != nil {
		return "", "", fmt.Errorf("%w: open SD storage: %w", ErrDeviceCredentialsUnavailable, err)
	}
	defer storage.Close()
	if err := validateVendorStorageDevice(storage); err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrDeviceCredentialsUnavailable, err)
	}
	// Coordinate with the provisioner's exclusive lock. Closing storage releases
	// this lock even when reading or parsing fails; no bytes are ever written.
	for {
		err = unix.Flock(int(storage.Fd()), unix.LOCK_SH)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("%w: lock SD storage: %w", ErrDeviceCredentialsUnavailable, err)
	}
	return readDeviceCredentials(storage)
}

func validateVendorStorageDevice(storage *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(storage.Fd()), &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFBLK {
		return fmt.Errorf("%s is not a block device", VENDOR_STORAGE_PATH)
	}
	device := fmt.Sprintf("/sys/dev/block/%d:%d", unix.Major(uint64(stat.Rdev)), unix.Minor(uint64(stat.Rdev)))
	if _, err := os.Stat(device + "/partition"); err == nil {
		return fmt.Errorf("%s must be a whole SD device, not a partition", VENDOR_STORAGE_PATH)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	media, err := os.ReadFile(device + "/device/type")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(media)) != "SD" {
		return fmt.Errorf("%s is not an SD card", VENDOR_STORAGE_PATH)
	}
	sectorSize, err := unix.IoctlGetInt(int(storage.Fd()), unix.BLKSSZGET)
	if err != nil {
		return err
	}
	if sectorSize != 512 {
		return fmt.Errorf("unsupported SD logical sector size: %d", sectorSize)
	}
	return nil
}

func readDeviceCredentials(storage io.ReaderAt) (string, string, error) {
	area := make([]byte, VENDOR_STORAGE_SIZE)
	n, err := storage.ReadAt(area, VENDOR_STORAGE_OFFSET)
	if err != nil && !(n == len(area) && errors.Is(err, io.EOF)) {
		return "", "", fmt.Errorf("%w: read vendor storage: %w", ErrDeviceCredentialsUnavailable, err)
	}
	if n != len(area) {
		return "", "", fmt.Errorf("%w: read vendor storage: %w", ErrDeviceCredentialsUnavailable, io.ErrUnexpectedEOF)
	}
	return deviceCredentialsFromVendorStorage(area)
}

func deviceCredentialsFromVendorStorage(area []byte) (string, string, error) {
	if len(area) != VENDOR_STORAGE_SIZE {
		return "", "", fmt.Errorf("%w: expected %d bytes", ErrInvalidVendorStorage, VENDOR_STORAGE_SIZE)
	}

	// U-Boot selects the highest positive version with matching header/trailer.
	// Equal versions keep the first slot; interrupted, uncommitted slots are
	// ignored. Validate the selected slot instead of falling back to stale data.
	active, version := -1, uint32(0)
	for index := range VENDOR_SLOT_COUNT {
		slot := area[index*VENDOR_SLOT_SIZE : (index+1)*VENDOR_SLOT_SIZE]
		candidate := binary.LittleEndian.Uint32(slot[4:8])
		if binary.LittleEndian.Uint32(slot[:4]) == VENDOR_STORAGE_TAG && candidate > version &&
			candidate == binary.LittleEndian.Uint32(slot[VENDOR_SLOT_SIZE-4:]) {
			active, version = index, candidate
		}
	}
	if active < 0 {
		blank := area[0] == 0 || area[0] == 0xff
		for _, value := range area {
			if value != area[0] {
				blank = false
				break
			}
		}
		if blank {
			return "", "", ErrDeviceCredentialsUnavailable
		}
		return "", "", fmt.Errorf("%w: no completed vendor slot", ErrInvalidVendorStorage)
	}

	slot := area[active*VENDOR_SLOT_SIZE : (active+1)*VENDOR_SLOT_SIZE]
	count := int(binary.LittleEndian.Uint16(slot[10:12]))
	freeOffset := int(binary.LittleEndian.Uint16(slot[12:14]))
	freeSize := int(binary.LittleEndian.Uint16(slot[14:16]))
	if count > VENDOR_MAX_ITEMS || freeOffset > VENDOR_DATA_SIZE || freeOffset%64 != 0 || freeSize != VENDOR_DATA_SIZE-freeOffset {
		return "", "", fmt.Errorf("%w: invalid item count or free-space metadata", ErrInvalidVendorStorage)
	}
	type itemRange struct {
		id         uint16
		start, end int
	}
	items := make([]itemRange, 0, count)
	var credential []byte
	for index := range count {
		item := slot[16+index*8 : 24+index*8]
		itemID := binary.LittleEndian.Uint16(item[:2])
		offset := int(binary.LittleEndian.Uint16(item[2:4]))
		length := int(binary.LittleEndian.Uint16(item[4:6]))
		allocation := (length + 63) &^ 63
		end := offset + allocation
		if offset%64 != 0 || offset > freeOffset || end > freeOffset {
			return "", "", fmt.Errorf("%w: invalid item bounds or alignment", ErrInvalidVendorStorage)
		}
		for _, other := range items {
			if itemID == other.id || (end > offset && other.end > other.start && offset < other.end && other.start < end) {
				return "", "", fmt.Errorf("%w: duplicate IDs or overlapping items", ErrInvalidVendorStorage)
			}
		}
		items = append(items, itemRange{id: itemID, start: offset, end: end})
		if itemID == VENDOR_CREDENTIAL_ID {
			credential = slot[VENDOR_DATA_OFFSET+offset : VENDOR_DATA_OFFSET+offset+length]
		}
	}
	if credential == nil {
		return "", "", ErrDeviceCredentialsUnavailable
	}
	if len(credential) != DEVICE_ID_BYTES+DEVICE_SIGNATURE_SIZE {
		return "", "", fmt.Errorf("%w: expected a %d-byte device credential", ErrInvalidVendorStorage, DEVICE_ID_BYTES+DEVICE_SIGNATURE_SIZE)
	}
	// Decode the stored pair without comparing it to hardware or verifying the
	// signature. The reporter sends these values to the server for authentication.
	return hex.EncodeToString(credential[:DEVICE_ID_BYTES]), base64.StdEncoding.EncodeToString(credential[DEVICE_ID_BYTES:]), nil
}
