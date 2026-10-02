package access

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// These fixtures encode the published Rockchip structs independently of the
// production parser. Offsets are relative to one 64 KiB slot; item offsets are
// relative to the payload area at byte 1024.
type vendorItemFixture struct {
	id    uint16
	value []byte
	flags uint16
}

func vendorSlotFixture(t *testing.T, version uint32, next uint16, items ...vendorItemFixture) []byte {
	t.Helper()
	if len(items) > 126 {
		t.Fatal("fixture has too many items")
	}
	slot := make([]byte, 65536)
	binary.LittleEndian.PutUint32(slot[0:4], 0x524b5644)
	binary.LittleEndian.PutUint32(slot[4:8], version)
	binary.LittleEndian.PutUint16(slot[8:10], next)
	binary.LittleEndian.PutUint16(slot[10:12], uint16(len(items)))
	used := 0
	for index, item := range items {
		allocation := (len(item.value) + 63) / 64 * 64
		if used+allocation > 64504 {
			t.Fatal("fixture payload exceeds vendor capacity")
		}
		descriptor := slot[16+index*8 : 24+index*8]
		binary.LittleEndian.PutUint16(descriptor[0:2], item.id)
		binary.LittleEndian.PutUint16(descriptor[2:4], uint16(used))
		binary.LittleEndian.PutUint16(descriptor[4:6], uint16(len(item.value)))
		binary.LittleEndian.PutUint16(descriptor[6:8], item.flags)
		copy(slot[1024+used:], item.value)
		used += allocation
	}
	binary.LittleEndian.PutUint16(slot[12:14], uint16(used))
	binary.LittleEndian.PutUint16(slot[14:16], uint16(64504-used))
	binary.LittleEndian.PutUint32(slot[65532:65536], version)
	return slot
}

func vendorAreaFixture(slots map[int][]byte) []byte {
	area := make([]byte, 262144)
	for index, slot := range slots {
		copy(area[index*65536:(index+1)*65536], slot)
	}
	return area
}

func vendorCredentialFixture(t *testing.T, deviceID string, signature []byte) []byte {
	t.Helper()
	id, err := hex.DecodeString(deviceID)
	if err != nil || len(id) != 8 || len(signature) != 64 {
		t.Fatal("invalid credential test fixture")
	}
	return append(id, signature...)
}

func requireVendorCredentials(t *testing.T, area []byte, deviceID string, expected []byte) {
	t.Helper()
	storedID, encoded, err := deviceCredentialsFromVendorStorage(area)
	if err != nil {
		t.Fatalf("credential parsing failed: %v", err)
	}
	if storedID != strings.ToLower(deviceID) {
		t.Fatal("stored device ID was not returned as lowercase 16-character hex")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(encoded) != 88 || !strings.HasSuffix(encoded, "==") || !bytes.Equal(decoded, expected) {
		t.Fatal("signature is not the unchanged 64 bytes encoded with padded standard Base64")
	}
}

func requireVendorError(t *testing.T, area []byte, expected error) {
	t.Helper()
	storedID, encoded, err := deviceCredentialsFromVendorStorage(area)
	if storedID != "" || encoded != "" || !errors.Is(err, expected) {
		t.Fatalf("credential error = %v, want %v and no credential pair", err, expected)
	}
}

func TestDeviceCredentialsFromVendorStoragePreservesStoredPair(t *testing.T) {
	t.Parallel()
	for _, deviceID := range []string{"957dadbb52b09f24", "00000000000000af", "957DADBB52B09F24", "AbCd0123456789Ef"} {
		t.Run(deviceID, func(t *testing.T) {
			t.Parallel()
			// A zero-filled value is not a valid Ed25519 signature. Reading
			// credentials must return it unchanged without authentication.
			signature := make([]byte, 64)
			credential := vendorCredentialFixture(t, deviceID, signature)
			area := vendorAreaFixture(map[int][]byte{0: vendorSlotFixture(t, 2, 1,
				vendorItemFixture{3, []byte{2, 0, 0, 0, 0, 1, 2, 0, 0, 0, 0, 2}, 0},
				vendorItemFixture{0x80, credential, 0xa55a})})
			requireVendorCredentials(t, area, deviceID, signature)
		})
	}
}

func TestDeviceCredentialsFromVendorStorageBinaryAndFullTable(t *testing.T) {
	t.Parallel()
	deviceID := "0000000000000000"
	signature := make([]byte, 64)
	for index := range signature {
		signature[index] = byte(index * 37)
	}
	signature[0], signature[1] = 0, 255
	items := make([]vendorItemFixture, 0, 126)
	for id := uint16(1); id <= 125; id++ {
		items = append(items, vendorItemFixture{id, []byte{byte(id)}, id})
	}
	items = append(items, vendorItemFixture{0x80, vendorCredentialFixture(t, deviceID, signature), 0xffff})
	area := vendorAreaFixture(map[int][]byte{2: vendorSlotFixture(t, 19, 3, items...)})
	requireVendorCredentials(t, area, deviceID, signature)
}

func TestDeviceCredentialsFromVendorStorageSlotSelection(t *testing.T) {
	t.Parallel()
	deviceID := "957dadbb52b09f24"
	first, second := bytes.Repeat([]byte{0x11}, 64), bytes.Repeat([]byte{0xe2}, 64)
	makeSlot := func(version uint32, next uint16, signature []byte) []byte {
		return vendorSlotFixture(t, version, next, vendorItemFixture{0x80, vendorCredentialFixture(t, deviceID, signature), 0})
	}
	interrupted := makeSlot(12, 2, second)
	binary.LittleEndian.PutUint32(interrupted[65532:], 11)
	badTag := makeSlot(12, 2, second)
	binary.LittleEndian.PutUint32(badTag, 0x12345678)
	reservedHash := makeSlot(12, 2, second)
	binary.LittleEndian.PutUint32(reservedHash[65528:65532], 0xdeadbeef)
	for _, test := range []struct {
		name  string
		slots map[int][]byte
		want  []byte
	}{
		{"highest version", map[int][]byte{0: makeSlot(2, 1, first), 3: makeSlot(12, 0, second)}, second},
		{"highest version first", map[int][]byte{0: makeSlot(12, 1, first), 3: makeSlot(2, 0, second)}, first},
		{"equal version first slot wins", map[int][]byte{0: makeSlot(12, 1, first), 3: makeSlot(12, 0, second)}, first},
		{"interrupted trailer ignored", map[int][]byte{0: makeSlot(2, 1, first), 1: interrupted}, first},
		{"wrong tag ignored", map[int][]byte{0: makeSlot(2, 1, first), 1: badTag}, first},
		{"zero version ignored", map[int][]byte{0: makeSlot(2, 1, first), 1: makeSlot(0, 2, second)}, first},
		{"maximum version readable", map[int][]byte{0: makeSlot(2, 1, first), 1: makeSlot(0xffffffff, 2, second)}, second},
		{"invalid next index readable", map[int][]byte{1: makeSlot(12, 0xffff, second)}, second},
		{"self next index readable", map[int][]byte{1: makeSlot(12, 1, second)}, second},
		{"reserved hash ignored", map[int][]byte{1: reservedHash}, second},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requireVendorCredentials(t, vendorAreaFixture(test.slots), deviceID, test.want)
		})
	}
}

func TestDeviceCredentialsFromVendorStorageUnavailableAndInvalid(t *testing.T) {
	t.Parallel()
	deviceID := "957dadbb52b09f24"
	signature := bytes.Repeat([]byte{0x5c}, 64)
	credential := vendorCredentialFixture(t, deviceID, signature)
	old := vendorSlotFixture(t, 2, 1, vendorItemFixture{0x80, credential, 0})
	invalid := make([]byte, 262144)
	invalid[1024] = 1
	for _, test := range []struct {
		name string
		area []byte
		err  error
	}{
		{"blank zero", make([]byte, 262144), ErrDeviceCredentialsUnavailable},
		{"blank erased", bytes.Repeat([]byte{255}, 262144), ErrDeviceCredentialsUnavailable},
		{"empty committed slot", vendorAreaFixture(map[int][]byte{0: vendorSlotFixture(t, 2, 1)}), ErrDeviceCredentialsUnavailable},
		{"missing credential", vendorAreaFixture(map[int][]byte{0: vendorSlotFixture(t, 2, 1, vendorItemFixture{3, []byte("MAC"), 0})}), ErrDeviceCredentialsUnavailable},
		{"newest missing credential does not fall back", vendorAreaFixture(map[int][]byte{0: old, 1: vendorSlotFixture(t, 3, 2)}), ErrDeviceCredentialsUnavailable},
		{"nonempty invalid area", invalid, ErrInvalidVendorStorage},
		{"empty input", nil, ErrInvalidVendorStorage},
		{"truncated input", make([]byte, 262143), ErrInvalidVendorStorage},
		{"oversized input", make([]byte, 262145), ErrInvalidVendorStorage},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requireVendorError(t, test.area, test.err)
		})
	}
	for _, length := range []int{0, 71, 73} {
		t.Run("wrong credential length "+strconv.Itoa(length), func(t *testing.T) {
			value := make([]byte, length)
			copy(value, credential)
			area := vendorAreaFixture(map[int][]byte{0: old, 1: vendorSlotFixture(t, 3, 2, vendorItemFixture{0x80, value, 0})})
			requireVendorError(t, area, ErrInvalidVendorStorage)
		})
	}
}

func TestDeviceCredentialsFromVendorStorageRejectsAllMalformedMetadata(t *testing.T) {
	t.Parallel()
	deviceID := "957dadbb52b09f24"
	credential := vendorCredentialFixture(t, deviceID, bytes.Repeat([]byte{0x5c}, 64))
	old := vendorSlotFixture(t, 2, 1, vendorItemFixture{0x80, credential, 0})
	for _, test := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"count exceeds table", func(slot []byte) { binary.LittleEndian.PutUint16(slot[10:], 127) }},
		{"free offset exceeds payload", func(slot []byte) { binary.LittleEndian.PutUint16(slot[12:], 64505) }},
		{"free offset unaligned", func(slot []byte) {
			binary.LittleEndian.PutUint16(slot[12:], 193)
			binary.LittleEndian.PutUint16(slot[14:], 64504-193)
		}},
		{"free size inconsistent", func(slot []byte) { binary.LittleEndian.PutUint16(slot[14:], 64504) }},
		{"later item unaligned", func(slot []byte) { binary.LittleEndian.PutUint16(slot[26:], 129) }},
		{"later item past used area", func(slot []byte) { binary.LittleEndian.PutUint16(slot[26:], 192) }},
		{"later item size overflows payload", func(slot []byte) { binary.LittleEndian.PutUint16(slot[28:], 65535) }},
		{"later duplicate credential", func(slot []byte) { binary.LittleEndian.PutUint16(slot[24:], 0x80) }},
		{"later item overlaps credential", func(slot []byte) { binary.LittleEndian.PutUint16(slot[26:], 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			newest := vendorSlotFixture(t, 3, 2, vendorItemFixture{0x80, credential, 0}, vendorItemFixture{3, []byte("MAC"), 0})
			test.mutate(newest)
			// Finding ID 0x80 first must not bypass validation of later records,
			// and malformed newest metadata must not select the older good slot.
			area := vendorAreaFixture(map[int][]byte{0: old, 1: newest})
			requireVendorError(t, area, ErrInvalidVendorStorage)
		})
	}
	duplicateUnrelated := vendorSlotFixture(t, 3, 2,
		vendorItemFixture{0x80, credential, 0}, vendorItemFixture{3, []byte("MAC"), 0}, vendorItemFixture{3, []byte("other"), 0})
	requireVendorError(t, vendorAreaFixture(map[int][]byte{0: old, 1: duplicateUnrelated}), ErrInvalidVendorStorage)
}

type vendorReaderFixture struct {
	area   []byte
	n      int
	err    error
	calls  int
	offset int64
	length int
}

func (reader *vendorReaderFixture) ReadAt(destination []byte, offset int64) (int, error) {
	reader.calls++
	reader.offset, reader.length = offset, len(destination)
	copy(destination, reader.area[:reader.n])
	return reader.n, reader.err
}

func TestReadDeviceCredentialsReadsFixedVendorArea(t *testing.T) {
	t.Parallel()
	deviceID := "957dadbb52b09f24"
	signature := bytes.Repeat([]byte{0x3b}, 64)
	area := vendorAreaFixture(map[int][]byte{0: vendorSlotFixture(t, 2, 1,
		vendorItemFixture{0x80, vendorCredentialFixture(t, deviceID, signature), 0})})
	readError := errors.New("injected storage read failure")
	for _, test := range []struct {
		name  string
		n     int
		err   error
		cause error
	}{
		{"complete", 262144, nil, nil},
		{"complete at EOF", 262144, io.EOF, nil},
		{"short EOF", 262143, io.EOF, io.EOF},
		{"short without error", 262143, nil, io.ErrUnexpectedEOF},
		{"I/O error", 0, readError, readError},
		{"complete with I/O error", 262144, readError, readError},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := &vendorReaderFixture{area: area, n: test.n, err: test.err}
			storedID, encoded, err := readDeviceCredentials(reader)
			if reader.calls != 1 || reader.offset != 0x380000 || reader.length != 262144 {
				t.Fatalf("unexpected storage read: calls=%d offset=%x length=%d", reader.calls, reader.offset, reader.length)
			}
			if test.cause != nil {
				if storedID != "" || encoded != "" || !errors.Is(err, ErrDeviceCredentialsUnavailable) || !errors.Is(err, test.cause) {
					t.Fatalf("read error = %v, want unavailable and preserved cause %v", err, test.cause)
				}
			} else if err != nil || storedID != deviceID || encoded != base64.StdEncoding.EncodeToString(signature) {
				t.Fatalf("complete read failed: %v", err)
			}
		})
	}
}

func TestStoredDeviceCredentialsAreSentTogetherAtLogin(t *testing.T) {
	t.Parallel()
	const STORED_DEVICE_ID = "000000000000abcf"
	// Deliberately invalid cryptographic bytes: reading and reporting must not
	// verify them or replace either part of the stored credential pair.
	signature := bytes.Repeat([]byte{0xff}, 64)
	area := vendorAreaFixture(map[int][]byte{0: vendorSlotFixture(t, 2, 1,
		vendorItemFixture{0x80, vendorCredentialFixture(t, STORED_DEVICE_ID, signature), 0})})
	deviceID, deviceSig, err := deviceCredentialsFromVendorStorage(area)
	if err != nil {
		t.Fatal(err)
	}
	reporter, err := NewIPReporter("https://example.invalid/api/device/v1", deviceID, deviceSig)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	reporter.httpClient.Transport = serviceRecoveryTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		defer request.Body.Close()
		if request.Method != http.MethodPost || request.URL.Path != "/api/device/v1/login" {
			t.Fatal("unexpected request while sending stored credentials")
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 2 || body["device_id"] != STORED_DEVICE_ID || body["device_sig"] != base64.StdEncoding.EncodeToString(signature) {
			t.Fatal("login did not send both stored credential values unchanged")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"token":"fixture-token"}`)),
		}, nil
	})
	token, err := reporter.login(t.Context())
	if err != nil || token != "fixture-token" || calls != 1 {
		t.Fatalf("stored-credential login failed: %v (calls=%d)", err, calls)
	}
}
