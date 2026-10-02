#!/usr/bin/env python3
"""Host-only tests for vendor credential preparation; never access a device."""

import base64
from contextlib import redirect_stderr, redirect_stdout
import importlib.util
import io
from pathlib import Path
import struct
import subprocess
import unittest
from unittest import mock
import zlib


SCRIPT = Path(__file__).with_name("provision_device.py")
SPEC = importlib.util.spec_from_file_location("provision_device_under_test", SCRIPT)
provision = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(provision)

DEVICE_ID = "A1B2C3D4E5F60708"
SIGNATURE = bytes(range(64))
PAYLOAD = bytes.fromhex(DEVICE_ID) + SIGNATURE


def slot(version=1, following=1, records=()):
    """Make committed test records with independent 64-byte allocations."""
    result = bytearray(provision.SLOT_SIZE)
    used = 0
    for index, (item_id, value, flags) in enumerate(records):
        struct.pack_into("<HHHH", result, 16 + index * 8, item_id, used, len(value), flags)
        result[provision.DATA_OFFSET + used:provision.DATA_OFFSET + used + len(value)] = value
        used += ((len(value) + 63) // 64) * 64
    struct.pack_into("<IIHHHH", result, 0, provision.TAG, version, following,
                     len(records), used, provision.DATA_SIZE - used)
    struct.pack_into("<I", result, provision.SLOT_SIZE - 4, version)
    return bytes(result)


def area_with(slots):
    area = bytearray(provision.AREA_SIZE)
    for index, contents in slots.items():
        start = index * provision.SLOT_SIZE
        area[start:start + provision.SLOT_SIZE] = contents
    return area


def records_from(contents):
    return [
        (item_id, contents[provision.DATA_OFFSET + offset:provision.DATA_OFFSET + offset + size], flags)
        for item_id, offset, size, flags in provision.validate_slot(contents)
    ]


def gpt(partitions=((2048, 4095), (8192, 16383))):
    disk_size = 16 * 1024 * 1024
    entries = bytearray(4 * 128)
    for index, (start, end) in enumerate(partitions):
        entries[index * 128:index * 128 + 16] = bytes([index + 1]) * 16
        struct.pack_into("<QQ", entries, index * 128 + 32, start, end)
    header = bytearray(512)
    header[:8] = b"EFI PART"
    struct.pack_into("<IIII", header, 8, 0x10000, 92, 0, 0)
    struct.pack_into("<QQQQ", header, 24, 1, disk_size // 512 - 1, 2048, disk_size // 512 - 3)
    struct.pack_into("<QIII", header, 72, 2, 4, 128, zlib.crc32(entries))
    struct.pack_into("<I", header, 16, zlib.crc32(header[:92]))
    return bytes(header), bytes(entries), disk_size


def snapshot():
    header, entries, disk_size = gpt()
    return {
        "serial": DEVICE_ID,
        "disk_size": disk_size,
        "header": base64.b64encode(header).decode("ascii"),
        "entries": base64.b64encode(entries).decode("ascii"),
        "area": base64.b64encode(bytes(provision.AREA_SIZE)).decode("ascii"),
    }


class VendorStorageTests(unittest.TestCase):
    def test_blank_storage_initializes_one_complete_inactive_slot(self):
        for fill in (0, 255):
            with self.subTest(fill=fill):
                area = bytearray([fill]) * provision.AREA_SIZE
                original = bytes(area)
                active, empty = provision.select_slot(area)
                self.assertEqual(active, -1)
                self.assertEqual(provision.validate_slot(empty), [])
                target, updated = provision.prepare_update(area, PAYLOAD)
                self.assertEqual(area, original)
                self.assertEqual(target, 0)
                self.assertEqual(len(updated), 65536)
                self.assertEqual(struct.unpack_from("<IIHHHH", updated),
                                 (provision.TAG, 2, 1, 1, 128, provision.DATA_SIZE - 128))
                self.assertEqual(struct.unpack_from("<I", updated, 65532)[0], 2)
                self.assertEqual(records_from(updated), [(0x80, PAYLOAD, 0)])

    def test_selects_first_newest_committed_slot_and_ignores_interrupted_write(self):
        interrupted = bytearray(slot(version=9, following=0))
        struct.pack_into("<I", interrupted, provision.SLOT_SIZE - 4, 0)
        selected = slot(version=8, following=2)
        area = area_with({0: slot(version=7), 1: selected, 2: slot(version=8), 3: interrupted})
        self.assertEqual(provision.select_slot(area), (1, selected))

    def test_corrupt_newest_slot_never_falls_back_to_older_credentials(self):
        newest = bytearray(slot(version=8, following=2))
        struct.pack_into("<H", newest, 10, provision.MAX_ITEMS + 1)
        with self.assertRaisesRegex(ValueError, "item count"):
            provision.select_slot(area_with({0: slot(version=7), 1: newest}))
        nonblank = bytearray(provision.AREA_SIZE)
        nonblank[-1] = 1
        with self.assertRaisesRegex(ValueError, "not blank"):
            provision.select_slot(nonblank)

    def test_rejects_invalid_item_table_and_free_space_metadata(self):
        good = slot(records=((1, b"first", 0), (2, b"second", 0)))
        mutations = {
            "tag": ("<I", 0, 0),
            "uncommitted trailer": ("<I", provision.SLOT_SIZE - 4, 0),
            "too many items": ("<H", 10, provision.MAX_ITEMS + 1),
            "unaligned free space": ("<H", 12, 127),
            "incorrect free size": ("<H", 14, 1),
            "duplicate item ID": ("<H", 24, 1),
            "overlapping item": ("<H", 26, 0),
            "unaligned item": ("<H", 26, 65),
            "out of bounds item": ("<H", 28, 129),
        }
        for name, (format_, offset, value) in mutations.items():
            with self.subTest(name=name):
                damaged = bytearray(good)
                struct.pack_into(format_, damaged, offset, value)
                with self.assertRaises(ValueError):
                    provision.validate_slot(damaged)
        for invalid in (b"", good[:-1], "not bytes"):
            with self.subTest(invalid_type=type(invalid).__name__), self.assertRaises(ValueError):
                provision.validate_slot(invalid)

    def test_replacement_preserves_other_records_flags_and_active_slot(self):
        records = ((7, b"factory calibration", 0xA5), (0x80, b"x" * 72, 9), (8, b"identity", 3))
        active = slot(version=20, following=0, records=records)
        area = area_with({1: slot(version=18, following=2), 3: active})
        original = bytes(area)
        target, updated = provision.prepare_update(area, PAYLOAD)
        self.assertEqual(target, 0)
        self.assertEqual(area, original)
        self.assertEqual(records_from(updated), [records[0], (0x80, PAYLOAD, 9), records[2]])
        self.assertEqual(struct.unpack_from("<IH", updated, 4), (21, 1))
        credential_offset = provision.validate_slot(updated)[1][1]
        padding = updated[provision.DATA_OFFSET + credential_offset + 72:
                          provision.DATA_OFFSET + credential_offset + 128]
        self.assertEqual(padding, bytes(56))
        area[:provision.SLOT_SIZE] = updated
        self.assertEqual(area[provision.SLOT_SIZE:], original[provision.SLOT_SIZE:])
        self.assertEqual(provision.select_slot(area), (0, updated))

    def test_identical_credentials_do_not_rotate_or_modify_storage(self):
        area = area_with({1: slot(version=4, following=2, records=((0x80, PAYLOAD, 5),))})
        original = bytes(area)
        self.assertEqual(provision.prepare_update(area, PAYLOAD), (None, None))
        self.assertEqual(area, original)

    def test_growing_record_compacts_without_losing_neighbor_data_or_flags(self):
        records = ((3, b"left", 1), (0x80, b"old", 7), (4, b"right" * 10, 2))
        area = area_with({0: slot(version=5, following=1, records=records)})
        target, updated = provision.prepare_update(area, PAYLOAD)
        self.assertEqual(target, 1)
        self.assertEqual(records_from(updated), [records[0], (0x80, PAYLOAD, 7), records[2]])
        self.assertEqual([item[1] for item in provision.validate_slot(updated)], [0, 64, 192])
        self.assertEqual(struct.unpack_from("<HH", updated, 12), (256, provision.DATA_SIZE - 256))

    def test_refuses_active_slot_overwrite_exhausted_version_or_capacity(self):
        cases = {
            "active target": slot(following=0),
            "invalid target": slot(following=4),
            "exhausted version": slot(version=0xFFFFFFFF),
            "full item table": slot(records=tuple((index, b"", 0) for index in range(126))),
            "full payload area": slot(records=((1, bytes(64448), 0),)),
        }
        for name, current in cases.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                provision.prepare_update(area_with({0: current}), PAYLOAD)
        for payload in (b"", bytes(71), bytes(73), "a" * 72, None):
            with self.subTest(payload_type=type(payload).__name__), self.assertRaises(ValueError):
                provision.prepare_update(bytes(provision.AREA_SIZE), payload)


class DiskLayoutTests(unittest.TestCase):
    def test_gpt_accepts_partitions_outside_vendor_storage_and_checks_both_crcs(self):
        header, entries, size = gpt()
        provision.validate_gpt(header, entries, size)
        bad_header = bytearray(header)
        bad_header[56] ^= 1
        with self.assertRaisesRegex(ValueError, "header CRC"):
            provision.validate_gpt(bad_header, entries, size)
        bad_entries = bytearray(entries)
        bad_entries[-1] ^= 1
        with self.assertRaisesRegex(ValueError, "array length or CRC"):
            provision.validate_gpt(header, bad_entries, size)

    def test_gpt_rejects_vendor_overlap_partition_overlap_and_invalid_geometry(self):
        vendor_start = provision.AREA_OFFSET // 512
        cases = {
            "vendor overlap": ((vendor_start, vendor_start + 1),),
            "partition overlap": ((2048, 4095), (3000, 5000)),
            "outside usable range": ((1, 10),),
            "reversed range": ((9000, 8000),),
        }
        for name, partitions in cases.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                provision.validate_gpt(*gpt(partitions))
        header, entries, size = gpt()
        for invalid_size in (True, size + 1, provision.AREA_OFFSET):
            with self.subTest(size=invalid_size), self.assertRaises(ValueError):
                provision.validate_gpt(header, entries, invalid_size)
        with self.assertRaises(ValueError):
            provision.validate_gpt(header[:-1], entries, size)

    def test_snapshot_requires_valid_identity_encoding_layout_and_vendor_area(self):
        good = snapshot()
        self.assertEqual(provision.decode_snapshot(good), bytes(provision.AREA_SIZE))
        for field, value in (
            ("serial", "0" * 15), ("serial", "G" * 16), ("serial", b"0" * 16),
            ("header", "not base64!"), ("area", base64.b64encode(b"short").decode()),
            ("disk_size", True), ("unexpected", "extra field"),
        ):
            with self.subTest(field=field, value_type=type(value).__name__), self.assertRaises(ValueError):
                provision.decode_snapshot(dict(good, **{field: value}))


class ProvisionSigningGuardsTests(unittest.TestCase):
    def test_signs_canonical_id_and_verifies_signature_before_returning(self):
        calls = []
        temporary = []

        def run(arguments, **kwargs):
            self.assertEqual(arguments[0], "openssl")
            self.assertTrue(kwargs["check"])
            calls.append(arguments)
            if "-text_pub" in arguments:
                return subprocess.CompletedProcess(arguments, 0, b"ED25519 Public-Key:\n")
            if "-sign" in arguments:
                message = Path(arguments[arguments.index("-in") + 1])
                temporary.append(message.parent)
                self.assertEqual(message.read_bytes(), b"nanotail-server/auth/device/v1\0a1b2c3d4e5f60708")
                Path(arguments[arguments.index("-out") + 1]).write_bytes(SIGNATURE)
            elif "-pubout" in arguments:
                Path(arguments[arguments.index("-out") + 1]).write_bytes(b"fake public key")
            elif "-verify" in arguments:
                self.assertIn("-rawin", arguments)
                self.assertIn("-pubin", arguments)
                self.assertEqual(Path(arguments[arguments.index("-sigfile") + 1]).read_bytes(), SIGNATURE)
            else:
                self.fail("unexpected external command")
            return subprocess.CompletedProcess(arguments, 0)

        with mock.patch.object(provision.subprocess, "run", side_effect=run):
            self.assertEqual(provision.sign_device(DEVICE_ID, Path("unused-test-key.pem")), SIGNATURE)
        self.assertEqual(len(calls), 4)
        self.assertIn("-verify", calls[-1])
        self.assertTrue(temporary)
        self.assertTrue(all(not path.exists() for path in temporary))

    def test_invalid_identity_stops_before_signing_or_opening_vendor_storage(self):
        for response in ({"serial": "bad"}, {"serial": "G" * 16}, {"serial": b"0" * 16}, None):
            with self.subTest(response=response):
                with mock.patch.object(provision.sys, "argv", [str(SCRIPT), "--target", "test-device", "--key", "unused.pem"]), \
                     mock.patch.object(provision.Path, "resolve", return_value=Path("/unused-test-key.pem")), \
                     mock.patch.object(provision.Path, "is_file", return_value=True), \
                     mock.patch.object(provision.os, "umask"), \
                     mock.patch.object(provision, "exchange", return_value=response) as exchange, \
                     mock.patch.object(provision, "sign_device") as sign, \
                     mock.patch.object(provision.tempfile, "mkdtemp") as temporary, \
                     redirect_stderr(io.StringIO()) as stderr, redirect_stdout(io.StringIO()):
                    self.assertEqual(provision.main(), 1)
                self.assertIn("invalid device ID", stderr.getvalue())
                exchange.assert_called_once_with("test-device", {"action": "read_id"})
                sign.assert_not_called()
                temporary.assert_not_called()


if __name__ == "__main__":
    unittest.main()
