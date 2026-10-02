#!/usr/bin/env python3
"""Create and verify signed ARM64 release packages with Python cryptography."""

import argparse
import base64
from contextlib import contextmanager
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import tarfile
import zlib

from cryptography.exceptions import InvalidSignature, UnsupportedAlgorithm
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519


APPLICATION = "nanotail-portal"
PACKAGE_PREFIX = APPLICATION + "/"
TARGET_OS = "linux"
TARGET_ARCH = "arm64"
MAX_PACKAGE_BYTES = 128 << 20
MAX_UNPACKED_BYTES = 256 << 20
MAX_MANIFEST_BYTES = 64 << 10
MAX_NOTES_BYTES = 32 << 10
CHUNK_BYTES = 64 << 10
FILE_LIMITS = {
    "payload/nanotail-portal": MAX_UNPACKED_BYTES - (1 << 20),
    "docs/release-notes.md": MAX_NOTES_BYTES,
    "docs/LICENSE": 64 << 10,
    "integration/nanotail-portal.service": 64 << 10,
    "integration/nanotail-portal.nginx": 64 << 10,
}
PUBLIC_DER_PREFIX = bytes.fromhex("302a300506032b6570032100")
VERSION_PATTERN = re.compile(
    r"v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
    r"(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
)


def valid_version(version):
    if not isinstance(version, str) or len(version) > 128:
        return False
    match = VERSION_PATTERN.fullmatch(version)
    if match is None:
        return False
    return not any(
        part.isdigit() and len(part) > 1 and part.startswith("0")
        for part in (match.group(4) or "").split(".")
    )


def require_platform(target_os, arch):
    if (target_os, arch) != (TARGET_OS, TARGET_ARCH):
        raise ValueError("unsupported package platform; releases require linux/arm64")


def identity(info):
    return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns)


@contextmanager
def regular_input(path, limit):
    # Nonblocking open lets us reject FIFOs without waiting for a writer.
    descriptor = os.open(path, os.O_RDONLY | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
            raise ValueError("release input must be a regular file within its size limit")
        yield source, identity(info)


def unchanged(source, path, original):
    if identity(os.fstat(source.fileno())) != original or identity(os.stat(path)) != original:
        raise ValueError("release input changed during packaging or verification")


def read_limited(path, limit):
    with regular_input(path, limit) as (source, original):
        data = source.read(limit + 1)
        if len(data) > limit:
            raise ValueError("input exceeds size limit")
        unchanged(source, path, original)
        return data


def remove_created(path, created):
    # Do not remove a different file if the caller's directory was changed.
    try:
        info = os.lstat(path)
        if (info.st_dev, info.st_ino) == created:
            os.unlink(path)
    except FileNotFoundError:
        pass


@contextmanager
def exclusive_output(path, mode):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    info = os.fstat(descriptor)
    created = (info.st_dev, info.st_ino)
    complete = False
    try:
        with os.fdopen(descriptor, "wb") as output:
            os.fchmod(output.fileno(), mode)
            yield output, created
            output.flush()
            os.fsync(output.fileno())
        complete = True
    finally:
        if not complete:
            remove_created(path, created)


def pem_blocks(data, label):
    marker = re.escape(label.encode("ascii"))
    pattern = re.compile(
        rb"-----BEGIN " + marker + rb"-----\r?\n"
        rb"([A-Za-z0-9+/=\r\n\t ]+)-----END " + marker + rb"-----"
    )
    blocks = []
    remaining = data.strip()
    while remaining:
        match = pattern.match(remaining)
        if match is None:
            raise ValueError(f"expected a valid {label} PEM without headers or extra content")
        try:
            der = base64.b64decode(re.sub(rb"\s", b"", match.group(1)), validate=True)
        except ValueError as error:
            raise ValueError("invalid key PEM encoding") from error
        blocks.append(der)
        remaining = remaining[match.end():].strip()
    if not blocks:
        raise ValueError("no release signing keys supplied")
    return blocks


def public_bytes(der):
    # Ed25519 PKIX has a fixed algorithm identifier and a 32-byte BIT STRING.
    if len(der) != len(PUBLIC_DER_PREFIX) + 32 or not der.startswith(PUBLIC_DER_PREFIX):
        raise ValueError("release signing key must be an Ed25519 PKIX public key")
    return der[len(PUBLIC_DER_PREFIX):]


def public_keys(data):
    keys = []
    for der in pem_blocks(data, "PUBLIC KEY"):
        public_bytes(der)
        try:
            key = serialization.load_der_public_key(der)
        except (ValueError, UnsupportedAlgorithm) as error:
            raise ValueError("invalid Ed25519 PKIX public key") from error
        if not isinstance(key, ed25519.Ed25519PublicKey):
            raise ValueError("release signing key must be Ed25519")
        keys.append(key)
    return keys


def complete_der_sequence(der):
    # Require one complete DER value before a deserializer interprets PKCS8.
    # This keeps trailing-data rejection independent of library behavior.
    if len(der) < 2 or der[0] != 0x30:
        return False
    length = der[1]
    offset = 2
    if length & 0x80:
        count = length & 0x7f
        if count == 0 or count > 4 or len(der) < 2 + count or der[2] == 0:
            return False
        length = int.from_bytes(der[2:2 + count], "big")
        offset += count
        if length < 128:
            return False
    return offset + length == len(der)


def private_key(data):
    keys = pem_blocks(data, "PRIVATE KEY")
    if len(keys) != 1 or not complete_der_sequence(keys[0]):
        raise ValueError("expected one Ed25519 private key in PKCS8 PEM format")
    try:
        key = serialization.load_der_private_key(keys[0], password=None)
    except (ValueError, TypeError, UnsupportedAlgorithm) as error:
        raise ValueError("invalid unencrypted Ed25519 PKCS8 private key") from error
    if not isinstance(key, ed25519.Ed25519PrivateKey):
        raise ValueError("release signing key must be Ed25519")
    return key


def keygen(args):
    try:
        key = ed25519.Ed25519PrivateKey.generate()
    except UnsupportedAlgorithm as error:
        raise ValueError("installed cryptography library does not support Ed25519") from error
    private = key.private_bytes(
        serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8,
        serialization.NoEncryption(),
    )
    public = key.public_key().public_bytes(
        serialization.Encoding.PEM, serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    raw_public = key.public_key().public_bytes(
        serialization.Encoding.Raw, serialization.PublicFormat.Raw,
    )
    fingerprint = hashlib.sha256(raw_public).hexdigest()
    with exclusive_output(args.private_key, 0o600) as (output, created):
        output.write(private)
    try:
        with exclusive_output(args.public_key, 0o644) as (output, _):
            output.write(public)
    except BaseException:
        remove_created(args.private_key, created)
        raise
    print(f"Created release signing keys. Public key SHA-256: {fingerprint}")


def validate_manifest(manifest):
    fields = {"format_version", "application", "version", "os", "arch", "release_notes", "files"}
    if not isinstance(manifest, dict) or set(manifest) - fields:
        raise ValueError("invalid signed manifest fields")
    if type(manifest.get("format_version")) is not int or manifest["format_version"] != 1 or manifest.get("application") != APPLICATION:
        raise ValueError("unsupported release package format or application")
    if not valid_version(manifest.get("version")):
        raise ValueError("package version must use major.minor.patch semantic versioning")
    require_platform(manifest.get("os"), manifest.get("arch"))
    notes = manifest.get("release_notes", "")
    if not isinstance(notes, str) or len(notes.encode("utf-8")) > MAX_NOTES_BYTES:
        raise ValueError("release notes exceed package limit or are not UTF-8 text")
    files = manifest.get("files")
    if not isinstance(files, dict) or not 1 <= len(files) <= 5:
        raise ValueError("invalid package file list")
    total = 0
    for name, entry in files.items():
        if name not in FILE_LIMITS or not isinstance(entry, dict) or set(entry) - {"sha256", "size"}:
            raise ValueError("invalid package file or checksum fields")
        size = entry.get("size", 0)
        digest = entry.get("sha256")
        if type(size) is not int or not 0 <= size <= FILE_LIMITS[name]:
            raise ValueError("invalid package file size")
        if not isinstance(digest, str) or re.fullmatch(r"[0-9a-f]{64}", digest) is None:
            raise ValueError("invalid package SHA-256 checksum")
        entry["size"] = size
        total += size
    if files.get("payload/nanotail-portal", {}).get("size", 0) <= 0:
        raise ValueError("package must contain a nonempty portal executable")
    if total > MAX_UNPACKED_BYTES - (1 << 20):
        raise ValueError("package contents exceed unpacked size limit")


def hash_source(path, limit, output=None, expected=None, capture=False):
    with regular_input(path, limit) as (source, original):
        if expected is not None and expected != original:
            raise ValueError("release input changed during packaging")
        digest = hashlib.sha256()
        size = 0
        contents = bytearray()
        while True:
            chunk = source.read(min(CHUNK_BYTES, original[2] - size + 1))
            if not chunk:
                break
            size += len(chunk)
            if size > original[2]:
                raise ValueError("release input changed during packaging")
            digest.update(chunk)
            if output is not None:
                output.write(chunk)
            if capture:
                contents.extend(chunk)
        unchanged(source, path, original)
        if size != original[2]:
            raise ValueError("release input changed during packaging")
        return {"sha256": digest.hexdigest(), "size": size}, original, contents


class LimitedWriter:
    def __init__(self, output):
        self.output = output
        self.size = 0

    def write(self, data):
        if self.size + len(data) > MAX_PACKAGE_BYTES:
            raise ValueError("package exceeds compressed size limit")
        written = self.output.write(data)
        self.size += written
        return written

    def flush(self):
        self.output.flush()


def write_header(output, name, size, mode=0o644):
    header = tarfile.TarInfo(PACKAGE_PREFIX + name)
    header.size = size
    header.mode = mode
    output.write(header.tobuf(format=tarfile.USTAR_FORMAT, encoding="utf-8"))


def write_padding(output, size):
    output.write(b"\0" * (-size % 512))


def create(args):
    require_platform(args.os, args.arch)
    if not valid_version(args.version):
        raise ValueError("package version must use major.minor.patch semantic versioning")
    sources = {"payload/nanotail-portal": args.binary}
    for name, path in (
        ("docs/release-notes.md", args.release_notes),
        ("docs/LICENSE", args.license),
        ("integration/nanotail-portal.service", args.service),
        ("integration/nanotail-portal.nginx", args.nginx),
    ):
        if path:
            sources[name] = path
    manifest = {
        "format_version": 1, "application": APPLICATION, "version": args.version,
        "os": args.os, "arch": args.arch,
    }
    entries = {}
    identities = {}
    for name in sorted(sources):
        entry, original, contents = hash_source(
            sources[name], FILE_LIMITS[name], capture=name == "docs/release-notes.md",
        )
        entries[name] = entry
        identities[name] = original
        if name == "docs/release-notes.md" and contents:
            manifest["release_notes"] = contents.decode("utf-8")
    manifest["files"] = entries
    validate_manifest(manifest)
    manifest_bytes = (json.dumps(manifest, indent=2, ensure_ascii=False) + "\n").encode("utf-8")
    if len(manifest_bytes) > MAX_MANIFEST_BYTES:
        raise ValueError("manifest exceeds package size limit")
    key = private_key(read_limited(args.private_key, 16 << 10))
    signature = key.sign(manifest_bytes)
    if len(signature) != 64:
        raise ValueError("Ed25519 signature must be exactly 64 bytes")
    with exclusive_output(args.output, 0o644) as (output, _):
        with gzip.GzipFile(filename="", mode="wb", fileobj=LimitedWriter(output), mtime=0) as zipped:
            for name, data in (("manifest.json", manifest_bytes), ("manifest.sig", signature)):
                write_header(zipped, name, len(data))
                zipped.write(data)
                write_padding(zipped, len(data))
            for name in sorted(sources):
                entry = entries[name]
                write_header(zipped, name, entry["size"], 0o755 if name == "payload/nanotail-portal" else 0o644)
                actual, _, _ = hash_source(sources[name], FILE_LIMITS[name], output=zipped, expected=identities[name])
                if actual != entry:
                    raise ValueError("release input changed during packaging")
                write_padding(zipped, entry["size"])
            zipped.write(b"\0" * 1024)
    print(f"Created signed release package {args.output} for linux/arm64 ({args.version}).")


def decompressed_chunks(source):
    decoder = zlib.decompressobj(16 + zlib.MAX_WBITS)
    compressed = 0
    unpacked = 0
    pending = b""
    while not decoder.eof:
        if not pending:
            pending = source.read(min(CHUNK_BYTES, MAX_PACKAGE_BYTES - compressed + 1))
            compressed += len(pending)
            if compressed > MAX_PACKAGE_BYTES:
                raise ValueError("package exceeds compressed size limit")
            if not pending:
                raise ValueError("truncated gzip package")
        data = decoder.decompress(pending, CHUNK_BYTES)
        pending = decoder.unconsumed_tail
        unpacked += len(data)
        if unpacked > MAX_UNPACKED_BYTES:
            raise ValueError("package exceeds unpacked size limit")
        if decoder.eof and (decoder.unused_data or source.read(1)):
            raise ValueError("unexpected content after compressed package")
        if data:
            yield data


class PackageReader:
    def __init__(self, source):
        self.chunks = iter(decompressed_chunks(source))
        self.buffer = bytearray()
        self.done = False

    def read(self, size):
        while len(self.buffer) < size and not self.done:
            chunk = next(self.chunks, None)
            if chunk is None:
                self.done = True
            else:
                self.buffer.extend(chunk)
        data = bytes(self.buffer[:size])
        del self.buffer[:size]
        return data

    def exact(self, size):
        data = self.read(size)
        if len(data) != size:
            raise ValueError("incomplete package archive")
        return data

    def header(self):
        block = self.read(512)
        if not block:
            return None
        if len(block) != 512:
            raise ValueError("incomplete package archive header")
        if block == b"\0" * 512:
            second = self.read(512)
            if second and second != b"\0" * 512:
                raise ValueError("unexpected content after package archive")
            return None
        # Parse one physical header ourselves: TarFile.next() would consume
        # PAX/GNU extension entries and conceal them from the allowlist checks.
        if block[257:265] not in (b"ustar\x0000", b"\0" * 8):
            raise ValueError("unsupported archive header format")
        if block[257:265] == b"\0" * 8 and any(block[345:500]):
            # Python reconstructs a USTAR prefix even for V7 headers, whereas
            # the device's Go reader correctly ignores that field for V7.
            raise ValueError("unexpected path prefix in a V7 archive header")
        header = tarfile.TarInfo.frombuf(block, "utf-8", "strict")
        if header.type not in (tarfile.REGTYPE, tarfile.AREGTYPE) or header.linkname or header.mode & 0o7000 or header.size < 0:
            raise ValueError("unsafe archive entry; only regular files are allowed")
        return header

    def padding(self, size):
        self.exact(-size % 512)

    def metadata(self, name, limit):
        header = self.header()
        if header is None or header.name != PACKAGE_PREFIX + name or header.size > limit:
            raise ValueError(f"expected a regular {name} entry within its size limit")
        contents = self.exact(header.size)
        self.padding(header.size)
        return contents


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate signed manifest field")
        result[key] = value
    return result


def invalid_json_constant(value):
    raise ValueError("invalid signed manifest number")


def verify_signature(manifest, signature, keys):
    if len(signature) != 64:
        raise ValueError("package signature must be exactly 64 bytes")
    for key in keys:
        try:
            key.verify(signature, manifest)
        except InvalidSignature:
            continue
        return
    raise ValueError("package signature is not from a trusted release signing key")


def verify(args):
    require_platform(args.os, args.arch)
    key_path = args.public_key or Path(__file__).resolve().parent.parent / "upgrade/release-public.pem"
    keys = public_keys(read_limited(key_path, 64 << 10))
    with regular_input(args.package, MAX_PACKAGE_BYTES) as (source, original):
        archive = PackageReader(source)
        manifest_bytes = archive.metadata("manifest.json", MAX_MANIFEST_BYTES)
        signature = archive.metadata("manifest.sig", 64)
        verify_signature(manifest_bytes, signature, keys)
        manifest = json.loads(
            manifest_bytes.decode("utf-8"), object_pairs_hook=unique_object,
            parse_constant=invalid_json_constant,
        )
        validate_manifest(manifest)
        seen = set()
        while True:
            header = archive.header()
            if header is None:
                break
            name = header.name[len(PACKAGE_PREFIX):]
            entry = manifest["files"].get(name)
            if not header.name.startswith(PACKAGE_PREFIX) or entry is None or name in seen or header.size != entry["size"]:
                raise ValueError("unlisted, duplicate, or unsafe archive entry")
            digest = hashlib.sha256()
            remaining = header.size
            while remaining:
                chunk = archive.exact(min(remaining, CHUNK_BYTES))
                digest.update(chunk)
                remaining -= len(chunk)
            if digest.hexdigest() != entry["sha256"]:
                raise ValueError("package checksum mismatch")
            archive.padding(header.size)
            seen.add(name)
        if seen != set(manifest["files"]):
            raise ValueError("package is missing files from its signed manifest")
        while True:
            padding = archive.read(CHUNK_BYTES)
            if not padding:
                break
            if any(padding):
                raise ValueError("unexpected content after package archive")
        unchanged(source, args.package, original)
    print(f"Verified {APPLICATION} {manifest['version']} for linux/arm64.")


def nonempty(value):
    if not value:
        raise argparse.ArgumentTypeError("value must not be empty")
    return value


def main():
    parser = argparse.ArgumentParser(
        description="Generate signing keys, create packages, or verify signed Linux ARM64 releases.",
        allow_abbrev=False,
    )
    commands = parser.add_subparsers(dest="command", required=True)

    def option(command, name, **kwargs):
        command.add_argument("-" + name, "--" + name, type=nonempty, **kwargs)

    keys = commands.add_parser("keygen", help="generate a new Ed25519 signing key pair", allow_abbrev=False)
    option(keys, "private-key", required=True, help="new PKCS8 private PEM file (0600)")
    option(keys, "public-key", required=True, help="new PKIX public PEM file")
    keys.set_defaults(run=keygen)

    package = commands.add_parser("create", help="sign an already built Linux ARM64 executable", allow_abbrev=False)
    for name in ("binary", "version", "private-key", "output"):
        option(package, name, required=True)
    for name in ("release-notes", "service", "nginx", "license"):
        option(package, name, default=None)
    package.set_defaults(run=create)

    check = commands.add_parser("verify", help="verify signature, checksums, and archive safety", allow_abbrev=False)
    option(check, "package", required=True)
    option(check, "public-key", default=None, help="PKIX PEM bundle; defaults to repository upgrade/release-public.pem")
    check.set_defaults(run=verify)
    for command in (package, check):
        option(command, "os", default=TARGET_OS, help="linux only (default: linux)")
        option(command, "arch", default=TARGET_ARCH, help="arm64 only (default: arm64)")
    args = parser.parse_args()
    os.umask(0o077)
    try:
        args.run(args)
    except (OSError, ValueError, tarfile.TarError, zlib.error, RecursionError) as error:
        print(f"release-package: {error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("release-package: interrupted", file=sys.stderr)
        return 130
    return 0


if __name__ == "__main__":
    sys.exit(main())
