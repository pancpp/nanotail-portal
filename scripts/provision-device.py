#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-or-later
"""Provision Rockchip SD vendor credentials using host files and streamed SSH I/O.

Vendor format follows vendor_storage/vendor_sd.c and its pinned U-Boot reference.
All record management and temporary files are on the host.
"""

import argparse
import base64
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import sys
import tempfile
import zlib


SLOT_SIZE = 65536
AREA_SIZE = 262144
AREA_OFFSET = 0x380000
DATA_OFFSET = 1024
DATA_SIZE = 64504
TAG = 0x524B5644
SLOT_COUNT = 4
MAX_ITEMS = 126
CREDENTIAL_ID = 0x80
CREDENTIAL_SIZE = 72


def _vendor_u16(data, offset):
    return struct.unpack_from("<H", data, offset)[0]


def _vendor_u32(data, offset):
    return struct.unpack_from("<I", data, offset)[0]


def _vendor_aligned(length):
    return (length + 63) & ~63


def validate_slot(slot):
    """Validate a committed 64 KiB slot and return (id, offset, size, flags)."""
    if not isinstance(slot, (bytes, bytearray, memoryview)) or len(slot) != SLOT_SIZE:
        raise ValueError("vendor slot must be exactly 65536 bytes")
    tag, version, _, count, free_offset, free_size = struct.unpack_from("<IIHHHH", slot)
    if tag != TAG or version == 0 or _vendor_u32(slot, SLOT_SIZE - 4) != version:
        raise ValueError("vendor slot has an invalid tag or mismatched version fields")
    if (count > MAX_ITEMS or free_offset > DATA_SIZE or free_offset % 64
            or free_size != DATA_SIZE - free_offset):
        raise ValueError("newest vendor slot has invalid item count or free-space metadata")
    items = []
    for index in range(count):
        item_id, offset, length, flags = struct.unpack_from("<HHHH", slot, 16 + index * 8)
        end = offset + _vendor_aligned(length)
        if offset % 64 or offset > free_offset or end > free_offset:
            raise ValueError("newest vendor slot contains an out-of-bounds or unaligned item")
        for other_id, other_offset, other_length, _ in items:
            other_end = other_offset + _vendor_aligned(other_length)
            if (item_id == other_id
                    or (end > offset and other_end > other_offset
                        and offset < other_end and other_offset < end)):
                raise ValueError("newest vendor slot has duplicate IDs or overlapping items")
        items.append((item_id, offset, length, flags))
    return items


def select_slot(area):
    """Select the newest committed slot, rejecting corrupt newest metadata.

    Return (-1, synthetic empty version-1 slot) only when the entire vendor
    area is uniformly zero-filled or erased (0xff). Equal versions select the
    first slot, matching U-Boot. An interrupted, mismatched trailer is ignored.
    """
    if not isinstance(area, (bytes, bytearray, memoryview)) or len(area) != AREA_SIZE:
        raise ValueError("vendor area must be exactly 262144 bytes")
    area = bytes(area)
    active, version = -1, 0
    for index in range(SLOT_COUNT):
        offset = index * SLOT_SIZE
        candidate = _vendor_u32(area, offset + 4)
        if (_vendor_u32(area, offset) == TAG and candidate > version
                and candidate == _vendor_u32(area, offset + SLOT_SIZE - 4)):
            active, version = index, candidate
    if active >= 0:
        current = area[active * SLOT_SIZE:(active + 1) * SLOT_SIZE]
        validate_slot(current)
        return active, current
    if area != bytes(AREA_SIZE) and area != b"\xff" * AREA_SIZE:
        raise ValueError("vendor region has no valid slot and is not blank; refusing to initialize")
    current = bytearray(SLOT_SIZE)
    struct.pack_into("<IIHHHH", current, 0, TAG, 1, 0, 0, 0, DATA_SIZE)
    struct.pack_into("<I", current, SLOT_SIZE - 4, 1)
    return -1, bytes(current)


def _vendor_compact(current, updated, items, target, payload):
    """Preserve item order/flags and compact payloads when growing a record."""
    updated[DATA_OFFSET:DATA_OFFSET + DATA_SIZE] = bytes(DATA_SIZE)
    used = 0
    for index, (_, offset, length, _) in enumerate(items):
        value = payload if index == target else current[DATA_OFFSET + offset:DATA_OFFSET + offset + length]
        allocation = _vendor_aligned(len(value))
        if allocation > DATA_SIZE - used:
            raise ValueError("vendor payload area is full")
        struct.pack_into("<HH", updated, 16 + index * 8 + 2, used, len(value))
        updated[DATA_OFFSET + used:DATA_OFFSET + used + len(value)] = value
        used += allocation
    struct.pack_into("<HH", updated, 12, used, DATA_SIZE - used)


def prepare_update(area, payload):
    """Prepare ID 0x80's exact 72-byte payload in the next inactive slot.

    Returns (slot index, complete slot bytes), or (None, None) for identical
    data. No input buffer is modified, and this function performs no I/O.
    """
    if not isinstance(payload, (bytes, bytearray, memoryview)) or len(payload) != CREDENTIAL_SIZE:
        raise ValueError("credential payload must be exactly 72 bytes")
    payload = bytes(payload)
    active, current = select_slot(area)
    items = validate_slot(current)
    version = _vendor_u32(current, 4)
    target = _vendor_u16(current, 8)
    if version == 0xFFFFFFFF:
        raise ValueError("vendor version is exhausted; firmware does not support wraparound")
    if target >= SLOT_COUNT or target == active:
        raise ValueError("next vendor slot is invalid or would overwrite the active slot")

    index = next((index for index, item in enumerate(items) if item[0] == CREDENTIAL_ID), len(items))
    if index < len(items):
        _, offset, old_length, _ = items[index]
        if old_length == len(payload) and current[DATA_OFFSET + offset:DATA_OFFSET + offset + old_length] == payload:
            return None, None
    elif len(items) == MAX_ITEMS:
        raise ValueError("vendor item table is full")

    updated = bytearray(current)
    if index == len(items):
        items.append((CREDENTIAL_ID, 0, len(payload), 0))
        struct.pack_into("<HHHH", updated, 16 + index * 8, CREDENTIAL_ID, 0, 0, 0)
        struct.pack_into("<H", updated, 10, len(items))
        free_offset, free_size = struct.unpack_from("<HH", updated, 12)
        allocation = _vendor_aligned(len(payload))
        if allocation <= free_size:
            struct.pack_into("<HH", updated, 16 + index * 8 + 2, free_offset, len(payload))
            updated[DATA_OFFSET + free_offset:DATA_OFFSET + free_offset + len(payload)] = payload
            struct.pack_into("<HH", updated, 12, free_offset + allocation, free_size - allocation)
        else:
            _vendor_compact(current, updated, items, index, payload)
    elif len(payload) <= _vendor_aligned(old_length):
        start = DATA_OFFSET + offset
        updated[start:start + _vendor_aligned(old_length)] = bytes(_vendor_aligned(old_length))
        updated[start:start + len(payload)] = payload
        struct.pack_into("<H", updated, 16 + index * 8 + 4, len(payload))
    else:
        _vendor_compact(current, updated, items, index, payload)

    struct.pack_into("<I", updated, 4, version + 1)
    struct.pack_into("<H", updated, 8, (target + 1) % SLOT_COUNT)
    struct.pack_into("<I", updated, SLOT_SIZE - 4, version + 1)
    validate_slot(updated)
    return target, bytes(updated)


# This program runs from SSH stdin with Python bytecode writes disabled. It only
# opens sysfs for reading and the SD block device; all buffers stay in memory.
REMOTE_PROGRAM = r'''
import base64
import fcntl
import json
import os
from pathlib import Path
import re
import stat
import struct
import sys

AREA_OFFSET = 0x380000
AREA_SIZE = 262144
SLOT_SIZE = 65536
TAG = 0x524B5644


def read_exact(fd, length, offset):
    data = bytearray()
    while len(data) < length:
        chunk = os.pread(fd, length - len(data), offset + len(data))
        if not chunk:
            raise OSError("unexpected end of SD storage")
        data.extend(chunk)
    return bytes(data)


def write_exact(fd, data, offset):
    written = 0
    while written < len(data):
        count = os.pwrite(fd, data[written:], offset + written)
        if count <= 0:
            raise OSError("short write to SD storage")
        written += count


def open_device(writable):
    fd = os.open("/dev/mmcblk0", (os.O_RDWR if writable else os.O_RDONLY) | os.O_CLOEXEC)
    try:
        info = os.fstat(fd)
        if not stat.S_ISBLK(info.st_mode):
            raise ValueError("/dev/mmcblk0 must be a block device")
        device = Path(f"/sys/dev/block/{os.major(info.st_rdev)}:{os.minor(info.st_rdev)}")
        if (device / "partition").exists():
            raise ValueError("use the whole SD device, not a partition")
        if (device / "device/type").read_text().strip() != "SD":
            raise ValueError("/dev/mmcblk0 is not an SD card")
        sector_size = struct.unpack("=I", fcntl.ioctl(fd, 0x1268, bytes(4)))[0]
        size = struct.unpack("=Q", fcntl.ioctl(fd, 0x80081272, bytes(8)))[0]
        if sector_size != 512 or size % 512 or not AREA_OFFSET + AREA_SIZE <= size <= 0x7FFFFFFFFFFFFFFF:
            raise ValueError("SD storage must use 512-byte sectors and contain the vendor area")
        return fd, size
    except BaseException:
        os.close(fd)
        raise


def read_device_id():
    serial = Path("/sys/firmware/devicetree/base/serial-number").read_bytes()
    if re.fullmatch(rb"[0-9a-fA-F]{16}\x00", serial) is None:
        raise ValueError("device ID must be exactly 16 hex characters and a NUL terminator")
    return serial[:-1].decode("ascii")


def snapshot(fd, size):
    serial = read_device_id()
    header = read_exact(fd, 512, 512)
    table_lba, count, entry_size = struct.unpack_from("<QII", header, 72)
    # Bound metadata reads here. The host fully validates both GPT CRCs and
    # all partition ranges before it prepares a write.
    table_size = count * entry_size
    if (header[:8] != b"EFI PART" or not 1 <= count <= 16384
            or not 128 <= entry_size <= 4096 or entry_size % 8
            or table_size > 16 * 1024 * 1024 or table_lba < 2
            or table_lba * 512 + table_size > size):
        raise ValueError("missing or invalid primary GPT metadata")
    entries = read_exact(fd, table_size, table_lba * 512)
    area = read_exact(fd, AREA_SIZE, AREA_OFFSET)
    return {
        "serial": serial, "disk_size": size,
        "header": base64.b64encode(header).decode("ascii"),
        "entries": base64.b64encode(entries).decode("ascii"),
        "area": base64.b64encode(area).decode("ascii"),
    }


def check_target(area, target, data):
    if type(target) is not int or not 0 <= target < 4 or len(data) != SLOT_SIZE:
        raise ValueError("invalid target slot or slot length")
    active, version, following = -1, 0, 0
    for index in range(4):
        start = index * SLOT_SIZE
        tag, candidate, next_index = struct.unpack_from("<IIH", area, start)
        trailer = struct.unpack_from("<I", area, start + SLOT_SIZE - 4)[0]
        if tag == TAG and candidate > version and candidate == trailer:
            active, version, following = index, candidate, next_index
    if active == -1:
        if area != bytes(AREA_SIZE) and area != b"\xff" * AREA_SIZE:
            raise ValueError("vendor storage has no valid slot and is not blank")
        version = 1
    if target != following or target == active or version == 0xFFFFFFFF:
        raise ValueError("unsafe vendor slot update")
    tag, new_version, next_index = struct.unpack_from("<IIH", data)
    trailer = struct.unpack_from("<I", data, SLOT_SIZE - 4)[0]
    if (tag != TAG or new_version != version + 1 or trailer != new_version
            or next_index != (target + 1) % 4):
        raise ValueError("invalid updated vendor slot header")


def remote_main(request):
    action = request["action"]
    if action == "read_id":
        sys.stdout.write(json.dumps({"serial": read_device_id()}))
        sys.stdout.flush()
        return
    if action not in ("read", "write", "readback"):
        raise ValueError("invalid storage action")
    fd, size = open_device(action == "write")
    try:
        fcntl.flock(fd, fcntl.LOCK_EX if action == "write" else fcntl.LOCK_SH)
        current = snapshot(fd, size)
        if action == "write":
            if current != request["expected"]:
                raise ValueError("device ID, disk layout, or vendor storage changed; refusing to write")
            area = base64.b64decode(current["area"], validate=True)
            updated_area = base64.b64decode(request["data"], validate=True)
            if len(updated_area) != AREA_SIZE:
                raise ValueError("updated vendor storage file must be exactly 262144 bytes")
            target = request["slot"]
            if target is not None:
                if type(target) is not int or not 0 <= target < 4:
                    raise ValueError("invalid target vendor slot")
                data = updated_area[target * SLOT_SIZE:(target + 1) * SLOT_SIZE]
                check_target(area, target, data)
                expected = bytearray(area)
                expected[target * SLOT_SIZE:(target + 1) * SLOT_SIZE] = data
                if updated_area != expected:
                    raise ValueError("updated file changes vendor slots other than the next slot")
                position = AREA_OFFSET + target * SLOT_SIZE
                # Publish the new slot only after its complete body is flushed.
                write_exact(fd, bytes(4), position + SLOT_SIZE - 4)
                os.fsync(fd)
                write_exact(fd, data[:-4], position)
                os.fsync(fd)
                write_exact(fd, data[-4:], position + SLOT_SIZE - 4)
                os.fsync(fd)
            elif updated_area != area:
                raise ValueError("updated file differs despite an unchanged record")
            current = {"status": "written" if target is not None else "unchanged"}
        # No credentials or diagnostics are mixed into this machine-readable stream.
        sys.stdout.write(json.dumps(current, separators=(",", ":")))
        sys.stdout.flush()
    finally:
        os.close(fd)
'''


def validate_gpt(header, entries, disk_size):
    """Validate the primary GPT and exclude every partition from the vendor area."""
    if (type(disk_size) is not int or disk_size % 512
            or not AREA_OFFSET + AREA_SIZE <= disk_size <= 0x7FFFFFFFFFFFFFFF
            or len(header) != 512):
        raise ValueError("invalid disk geometry or GPT header length")
    revision, header_size, crc, reserved = struct.unpack_from("<IIII", header, 8)
    if header[:8] != b"EFI PART" or revision != 0x10000 or not 92 <= header_size <= 512 or reserved:
        raise ValueError("missing or unsupported primary GPT header")
    checked = bytearray(header[:header_size])
    struct.pack_into("<I", checked, 16, 0)
    if zlib.crc32(checked) != crc:
        raise ValueError("primary GPT header CRC mismatch")
    current, backup, first, last = struct.unpack_from("<QQQQ", header, 24)
    table_lba, count, entry_size, table_crc = struct.unpack_from("<QIII", header, 72)
    if (current != 1 or not 1 < backup < disk_size // 512
            or not 2 <= first <= last < backup or not 1 <= count <= 16384
            or not 128 <= entry_size <= 4096 or entry_size % 8):
        raise ValueError("invalid GPT dimensions")
    table_size = count * entry_size
    table_sectors = (table_size + 511) // 512
    vendor_first, vendor_end = AREA_OFFSET // 512, (AREA_OFFSET + AREA_SIZE) // 512

    def overlaps_vendor(start, end):
        return start < vendor_end and vendor_first < end

    if (table_size > 16 * 1024 * 1024 or not 2 <= table_lba < first
            or table_lba + table_sectors > first or backup - table_sectors <= last
            or overlaps_vendor(table_lba, table_lba + table_sectors)
            or overlaps_vendor(backup - table_sectors, backup + 1)):
        raise ValueError("GPT metadata overlaps vendor storage or has invalid bounds")
    if len(entries) != table_size or zlib.crc32(entries) != table_crc:
        raise ValueError("GPT partition array length or CRC mismatch")
    ranges = []
    for index in range(count):
        entry = entries[index * entry_size:(index + 1) * entry_size]
        if entry[:16] == bytes(16):
            continue
        start, end = struct.unpack_from("<QQ", entry, 32)
        if not first <= start <= end <= last or overlaps_vendor(start, end + 1):
            raise ValueError("GPT partition has invalid bounds or overlaps vendor storage")
        ranges.append((start, end + 1))
    ranges.sort()
    if any(left[1] > right[0] for left, right in zip(ranges, ranges[1:])):
        raise ValueError("GPT partitions overlap each other")


def decode_snapshot(snapshot):
    if (not isinstance(snapshot, dict)
            or set(snapshot) != {"serial", "disk_size", "header", "entries", "area"}
            or not isinstance(snapshot["serial"], str)
            or re.fullmatch(r"[0-9a-fA-F]{16}", snapshot["serial"]) is None
            or any(not isinstance(snapshot[key], str) for key in ("header", "entries", "area"))):
        raise ValueError("invalid device snapshot")
    header, entries, area = (base64.b64decode(snapshot[key], validate=True)
                             for key in ("header", "entries", "area"))
    validate_gpt(header, entries, snapshot["disk_size"])
    select_slot(area)
    return area


def exchange(target, request):
    program = REMOTE_PROGRAM + (
        "\ntry:\n"
        f"    remote_main({request!r})\n"
        "except (OSError, ValueError, KeyError, TypeError) as error:\n"
        "    sys.exit(f'(provision) {error}')\n"
    )
    result = subprocess.run(
        ["ssh", "-T", "--", target, "sudo -n python3 -B -"],
        input=program.encode("utf-8"), stdout=subprocess.PIPE, check=True,
    )
    return json.loads(result.stdout)


def sync_parent(path):
    fd = os.open(str(path.parent), os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def write_host_file(path, data):
    with path.open("wb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def save_snapshot_file(snapshot, path):
    """Save downloaded bytes before validation, including a bad readback."""
    if not isinstance(snapshot, dict) or not isinstance(snapshot.get("area"), str):
        raise ValueError("invalid device snapshot")
    write_host_file(path, base64.b64decode(snapshot["area"], validate=True))
    return decode_snapshot(snapshot)


def update_vendor_file(path, payload):
    """Modify the downloaded file using the vendor item table and slot rotation."""
    target, updated = prepare_update(path.read_bytes(), payload)
    if target is not None:
        with path.open("r+b") as stream:
            stream.seek(target * SLOT_SIZE)
            stream.write(updated)
            stream.flush()
            os.fsync(stream.fileno())
    return target


def sign_device(device_id, private_key):
    """Return a raw Ed25519 signature; the private key stays on this machine."""
    key_info = subprocess.run(
        ["openssl", "pkey", "-in", str(private_key), "-passin", "pass:",
         "-noout", "-text_pub"],
        stdout=subprocess.PIPE, check=True,
    ).stdout
    if not key_info.startswith(b"ED25519 Public-Key:"):
        raise ValueError("private key must be Ed25519")

    with tempfile.TemporaryDirectory(prefix="nanotail-sign.", dir="/tmp") as directory:
        work = Path(directory)
        message = work / "message"
        signature_file = work / "signature"
        public_key = work / "public.pem"
        # Sign the original text and its exact case, not the 8 decoded bytes.
        message.write_bytes(b"nanotail-server/auth/device/v1\0" + device_id.encode("ascii"))
        subprocess.run(
            ["openssl", "pkeyutl", "-sign", "-rawin", "-inkey", str(private_key),
             "-passin", "pass:", "-in", str(message), "-out", str(signature_file)],
            check=True,
        )
        signature = signature_file.read_bytes()
        if len(signature) != 64:
            raise ValueError("Ed25519 signature must be exactly 64 bytes")
        subprocess.run(
            ["openssl", "pkey", "-in", str(private_key), "-passin", "pass:",
             "-pubout", "-out", str(public_key)],
            check=True,
        )
        subprocess.run(
            ["openssl", "pkeyutl", "-verify", "-rawin", "-pubin",
             "-inkey", str(public_key), "-in", str(message),
             "-sigfile", str(signature_file)],
            stdout=subprocess.DEVNULL, check=True,
        )
        return signature



def main():
    parser = argparse.ArgumentParser(
        description="Sign and provision vendor ID 0x80 through SSH, processing "
                    "the SD vendor area on this host without device temporary files.",
        allow_abbrev=False,
    )
    parser.add_argument("--target", required=True, help="SSH destination, e.g. nanotail.local")
    parser.add_argument("--key", required=True, type=Path, help="Ed25519 private PEM key")
    args = parser.parse_args()
    if not args.target.strip():
        parser.error("--target must not be empty")

    os.umask(0o077)
    work = None
    try:
        private_key = args.key.expanduser().resolve(strict=True)
        if not private_key.is_file():
            raise ValueError("private key must be a regular file")
        # 1. Read the device ID without opening vendor storage.
        identity = exchange(args.target, {"action": "read_id"})
        if (not isinstance(identity, dict) or not isinstance(identity.get("serial"), str)
                or re.fullmatch(r"[0-9a-fA-F]{16}", identity["serial"]) is None):
            raise ValueError("invalid device ID response")
        device_id = identity["serial"]

        # 2. Sign the exact ID text before reading any vendor-storage bytes.
        payload = bytes.fromhex(device_id) + sign_device(device_id, private_key)

        # 3. Download the complete vendor area to an actual host file under /tmp.
        work = Path(tempfile.mkdtemp(prefix="nanotail-provision.", dir="/tmp"))
        print(f"Host temporary files: {work}", flush=True)
        original_path = work / "original.bin"
        working_path = work / "vendor-storage.bin"
        readback_path = work / "readback.bin"
        original = exchange(args.target, {"action": "read"})
        area = save_snapshot_file(original, working_path)
        if original["serial"] != device_id:
            raise ValueError("device ID changed after signing; refusing to write")
        write_host_file(original_path, area)
        sync_parent(original_path)
        sync_parent(work)

        # 4. Update the downloaded file, preserving the original for recovery.
        slot = update_vendor_file(working_path, payload)

        # 5. Upload the complete updated file. The receiver commits its changed
        # slot in stages, preserving all unchanged bytes and the old active slot.
        written = exchange(args.target, {
            "action": "write", "expected": original, "slot": slot,
            "data": base64.b64encode(working_path.read_bytes()).decode("ascii"),
        })
        if written != {"status": "written" if slot is not None else "unchanged"}:
            raise ValueError("device did not acknowledge the vendor-storage write")

        # 6. Independently download the storage again, then compare whole files.
        result = exchange(args.target, {"action": "readback"})
        save_snapshot_file(result, readback_path)
        if (readback_path.read_bytes() != working_path.read_bytes()
                or any(result[key] != original[key] for key in original if key != "area")):
            raise ValueError("device identity, disk layout, or vendor-area readback did not match")

        # 7. Remove temporary files only after the independent readback matches.
        shutil.rmtree(work)
        work = None
        print(f"Verified ID 0x80: 72 bytes (8-byte device ID {device_id} + 64-byte signature).")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"(provision) {error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("(provision) interrupted", file=sys.stderr)
        return 130
    finally:
        if work is not None:
            print(f"Host temporary files retained for inspection/recovery: {work}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
