#!/usr/bin/env python3
"""Release-package CLI tests using independent archives and temporary signing keys."""

import base64
import copy
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zlib

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec, ed25519


SCRIPT = Path(__file__).resolve().with_name("release_package.py")
PREFIX = "nanotail-portal/"
PAYLOAD = "payload/nanotail-portal"


def pem(label, der):
    encoded = base64.b64encode(der)
    lines = [encoded[index:index + 64] for index in range(0, len(encoded), 64)]
    return (f"-----BEGIN {label}-----\n".encode() + b"\n".join(lines)
            + f"\n-----END {label}-----\n".encode())


def public_pem(key):
    return key.public_bytes(serialization.Encoding.PEM,
                            serialization.PublicFormat.SubjectPublicKeyInfo)


def entry(name, body, kind=tarfile.REGTYPE, linkname="", pax=None):
    header = tarfile.TarInfo(name)
    header.size = len(body)
    header.mode = 0o755 if name == PREFIX + PAYLOAD else 0o644
    header.type = kind
    header.linkname = linkname
    header.pax_headers = pax or {}
    return header, body


class ReleasePackageTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="release-package-test-")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.cwd = self.root / "unrelated-working-directory"
        self.scratch = self.root / "temporary-files"
        self.empty_path = self.root / "empty-path"
        for directory in (self.cwd, self.scratch, self.empty_path):
            directory.mkdir(mode=0o700)
        self.key = ed25519.Ed25519PrivateKey.generate()
        self.private_pem = self.key.private_bytes(
            serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8,
            serialization.NoEncryption(),
        )
        self.public_pem = public_pem(self.key.public_key())
        self.private_path = self.write("private.pem", self.private_pem)
        self.public_path = self.write("public.pem", self.public_pem)
        self.payload = b"independently assembled portal payload\n"
        self.manifest = {
            "format_version": 1,
            "application": "nanotail-portal",
            "version": "v1.2.3",
            "os": "linux",
            "arch": "arm64",
            "files": {PAYLOAD: {"sha256": hashlib.sha256(self.payload).hexdigest(),
                                "size": len(self.payload)}},
        }

    def write(self, name, data):
        path = self.cwd / name
        path.write_bytes(data)
        path.chmod(0o600)
        return path

    def run_cli(self, *args, success=True, script=SCRIPT):
        environment = dict(os.environ, PATH=str(self.empty_path),
                           TMPDIR=str(self.scratch), PYTHONDONTWRITEBYTECODE="1")
        # An absolute interpreter and empty PATH ensure all cryptography works
        # without invoking another executable. The cwd contains only test data.
        result = subprocess.run(
            [str(Path(sys.executable).resolve()), str(script), *map(str, args)],
            cwd=self.cwd, env=environment, capture_output=True, text=True,
            encoding="utf-8", timeout=20,
        )
        output = result.stdout + result.stderr
        self.assertEqual(list(self.scratch.iterdir()), [], "temporary signing material leaked")
        if success:
            self.assertEqual(result.returncode, 0, output)
        else:
            self.assertNotEqual(result.returncode, 0, output)
            self.assertNotIn("Traceback", output)
            self.assertNotIn("-----BEGIN", output)
        return output

    def archive(self, manifest=None, entries=None, signature=None, tar_format=tarfile.USTAR_FORMAT):
        # Fixtures use stdlib tar/gzip and cryptography directly, with different
        # JSON formatting from the producer. No production helpers are imported.
        manifest = self.manifest if manifest is None else manifest
        encoded = json.dumps(manifest, ensure_ascii=False, separators=(",", ":")).encode() + b"\n"
        signature = self.key.sign(encoded) if signature is None else signature
        entries = [entry(PREFIX + PAYLOAD, self.payload)] if entries is None else entries
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode="w", format=tar_format) as archive:
            for header, body in [entry(PREFIX + "manifest.json", encoded),
                                 entry(PREFIX + "manifest.sig", signature), *entries]:
                archive.addfile(header, io.BytesIO(body))
        return gzip.compress(output.getvalue(), mtime=0)

    def assert_created_archive(self, path, version, files, notes=""):
        decoder = zlib.decompressobj(16 + zlib.MAX_WBITS)
        unpacked = decoder.decompress(path.read_bytes()) + decoder.flush()
        self.assertTrue(decoder.eof)
        self.assertEqual(decoder.unused_data, b"")
        with tarfile.open(fileobj=io.BytesIO(unpacked), mode="r:") as archive:
            members = archive.getmembers()
            self.assertEqual([item.name for item in members[:2]],
                             [PREFIX + "manifest.json", PREFIX + "manifest.sig"])
            self.assertEqual({item.name for item in members[2:]},
                             {PREFIX + name for name in files})
            self.assertEqual(len(members), len(files) + 2)
            for item in members:
                self.assertEqual(item.type, tarfile.REGTYPE)
                self.assertEqual(item.mode, 0o755 if item.name == PREFIX + PAYLOAD else 0o644)
            encoded = archive.extractfile(members[0]).read()
            signature = archive.extractfile(members[1]).read()
            self.assertEqual(len(signature), 64)
            self.key.public_key().verify(signature, encoded)
            manifest = json.loads(encoded)
            self.assertEqual(manifest["format_version"], 1)
            self.assertEqual(manifest["application"], "nanotail-portal")
            self.assertEqual(manifest["version"], version)
            self.assertEqual((manifest["os"], manifest["arch"]), ("linux", "arm64"))
            self.assertEqual(manifest.get("release_notes", ""), notes)
            self.assertEqual(set(manifest["files"]), set(files))
            for name, expected in files.items():
                self.assertEqual(archive.extractfile(PREFIX + name).read(), expected)
                self.assertEqual(manifest["files"][name], {
                    "size": len(expected), "sha256": hashlib.sha256(expected).hexdigest(),
                })

    def test_create_all_files_minimal_and_exclusive_output(self):
        notes = "Release notes with Unicode: 升级 <verified> & signed.\n"
        files = {
            PAYLOAD: self.payload,
            "docs/release-notes.md": notes.encode(),
            "integration/nanotail-portal.service": b"[Service]\nRestart=always\n",
            "integration/nanotail-portal.nginx": b"server { listen 80; }\n",
            "docs/LICENSE": b"Test license\n",
        }
        binary = self.write("portal", self.payload)
        output = self.cwd / "release.tar.gz"
        args = ["create", "--binary", binary, "-private-key", self.private_path,
                "--version", "v2.3.4-rc.1+build.5", "-output", output,
                "--release-notes", self.write("notes", files["docs/release-notes.md"]),
                "-service", self.write("service", files["integration/nanotail-portal.service"]),
                "--nginx", self.write("nginx", files["integration/nanotail-portal.nginx"]),
                "-license", self.write("license", files["docs/LICENSE"])]
        self.run_cli(*args)
        self.assert_created_archive(output, "v2.3.4-rc.1+build.5", files, notes)
        before, original = output.stat(), output.read_bytes()
        self.run_cli(*args, success=False)
        after = output.stat()
        self.assertEqual((after.st_ino, after.st_mtime_ns), (before.st_ino, before.st_mtime_ns))
        self.assertEqual(output.read_bytes(), original)
        minimal = self.cwd / "minimal.tar.gz"
        self.run_cli("create", "--binary", binary, "--version", "2.3.4",
                     "--private-key", self.private_path, "--output", minimal)
        self.assert_created_archive(minimal, "2.3.4", {PAYLOAD: self.payload})

    def test_verify_independent_archive_rotated_keys_and_default_trust(self):
        package = self.write("independent.tar.gz", self.archive())
        other_public = public_pem(ed25519.Ed25519PrivateKey.generate().public_key())
        bundle = other_public + self.public_pem
        self.public_path.write_bytes(bundle)
        args = ["verify", "-package", package, "--public-key", self.public_path,
                "-os", "linux", "--arch", "arm64"]
        self.assertIn("Verified nanotail-portal v1.2.3", self.run_cli(*args))
        layout = self.root / "isolated-repository"
        (layout / "scripts").mkdir(parents=True)
        (layout / "upgrade").mkdir()
        copied_script = layout / "scripts" / SCRIPT.name
        copied_script.write_bytes(SCRIPT.read_bytes())
        (layout / "upgrade" / "release-public.pem").write_bytes(bundle)
        self.run_cli("verify", "--package", package, script=copied_script)
        self.public_path.write_bytes(other_public)
        self.run_cli(*args, success=False)
        self.public_path.write_bytes(self.public_pem + b"unexpected trailing PEM content")
        self.run_cli(*args, success=False)

    def test_keygen_permissions_fingerprint_and_no_overwrite(self):
        private_path, public_path = self.cwd / "generated-private.pem", self.cwd / "generated-public.pem"
        args = ["keygen", "--private-key", private_path, "-public-key", public_path]
        output = self.run_cli(*args)
        private_bytes, public_bytes = private_path.read_bytes(), public_path.read_bytes()
        private = serialization.load_pem_private_key(private_bytes, password=None)
        public = serialization.load_pem_public_key(public_bytes)
        self.assertIsInstance(private, ed25519.Ed25519PrivateKey)
        self.assertIsInstance(public, ed25519.Ed25519PublicKey)
        self.assertEqual(public_pem(private.public_key()), public_bytes)
        raw_public = public.public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
        self.assertIn(hashlib.sha256(raw_public).hexdigest(), output)
        self.assertNotIn("BEGIN PRIVATE KEY", output)
        self.assertNotIn(private_bytes.decode(), output)
        self.assertEqual(stat.S_IMODE(private_path.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(public_path.stat().st_mode) & 0o022, 0)
        self.run_cli(*args, success=False)
        self.assertEqual(private_path.read_bytes(), private_bytes)
        self.assertEqual(public_path.read_bytes(), public_bytes)
        private_path.unlink()
        self.run_cli(*args, success=False)
        self.assertFalse(private_path.exists(), "failed keygen left an incomplete keypair")
        self.assertEqual(public_path.read_bytes(), public_bytes)

    def test_rejects_malformed_encrypted_and_non_ed25519_keys(self):
        package = self.write("independent.tar.gz", self.archive())
        binary = self.write("portal", self.payload)
        other = ec.generate_private_key(ec.SECP256R1())
        private_der = self.key.private_bytes(serialization.Encoding.DER,
                                             serialization.PrivateFormat.PKCS8,
                                             serialization.NoEncryption())
        public_der = self.key.public_key().public_bytes(
            serialization.Encoding.DER, serialization.PublicFormat.SubjectPublicKeyInfo)
        cases = {
            "malformed-private": (True, pem("PRIVATE KEY", b"\x30\x01\xff")),
            "ecdsa-private": (True, other.private_bytes(serialization.Encoding.PEM,
                serialization.PrivateFormat.PKCS8, serialization.NoEncryption())),
            "encrypted-private": (True, self.key.private_bytes(serialization.Encoding.PEM,
                serialization.PrivateFormat.PKCS8, serialization.BestAvailableEncryption(b"temporary-password"))),
            "trailing-private-der": (True, pem("PRIVATE KEY", private_der + b"\0")),
            "multiple-private": (True, self.private_pem * 2),
            "malformed-public": (False, pem("PUBLIC KEY", b"\x30\x01\xff")),
            "mixed-public-bundle": (False, self.public_pem + public_pem(other.public_key())),
            "trailing-public-der": (False, pem("PUBLIC KEY", public_der + b"\0")),
        }
        output = self.cwd / "rejected.tar.gz"
        for name, (private, data) in cases.items():
            with self.subTest(name=name):
                key_path = self.write("invalid-key.pem", data)
                if private:
                    self.run_cli("create", "--binary", binary, "--version", "v1.2.3",
                                 "--private-key", key_path, "--output", output, success=False)
                else:
                    self.run_cli("verify", "--package", package, "--public-key", key_path, success=False)
                self.assertFalse(output.exists())
                self.assertEqual(key_path.read_bytes(), data)

    def test_rejects_tampering_unsafe_tar_and_gzip_framing(self):
        valid = self.archive()
        signature = bytearray(self.key.sign(json.dumps(
            self.manifest, ensure_ascii=False, separators=(",", ":")).encode() + b"\n"))
        signature[0] ^= 1
        payload_entry = entry(PREFIX + PAYLOAD, self.payload)
        cases = {
            "signature": self.archive(signature=bytes(signature)),
            "payload": self.archive(entries=[entry(PREFIX + PAYLOAD, b"X" + self.payload[1:])]),
            "traversal": self.archive(entries=[entry(PREFIX + "payload/../../outside", self.payload)]),
            "duplicate": self.archive(entries=[payload_entry, payload_entry]),
            "symlink": self.archive(entries=[entry(PREFIX + PAYLOAD, b"", tarfile.SYMTYPE, "/etc/passwd")]),
            "pax": self.archive(entries=[entry(PREFIX + PAYLOAD, self.payload, pax={"comment": "unsigned extension"})],
                                tar_format=tarfile.PAX_FORMAT),
            "unlisted": self.archive(entries=[payload_entry, entry(PREFIX + "install.sh", b"unlisted")]),
            "truncated-gzip": valid[:-4],
            "concatenated-gzip": valid + valid,
            "trailing-compressed-data": valid + b"unsigned suffix",
            "trailing-tar-data": gzip.compress(gzip.decompress(valid) + b"unsigned suffix", mtime=0),
        }
        for name, field, value in (("signed-platform", "arch", "amd64"),
                                   ("signed-version", "version", "1.2.3-01"),
                                   ("signed-path", "files", {"../outside": self.manifest["files"][PAYLOAD]})):
            manifest = copy.deepcopy(self.manifest)
            manifest[field] = value
            cases[name] = self.archive(manifest=manifest)
        bad_crc = bytearray(valid)
        bad_crc[-8] ^= 1
        cases["gzip-crc"] = bytes(bad_crc)
        # V7 has no prefix field. TarInfo otherwise treats these bytes as a
        # USTAR prefix and could incorrectly accept the disguised metadata path.
        spoof = bytearray(gzip.decompress(valid))
        spoof[:100] = b"manifest.json".ljust(100, b"\0")
        spoof[257:265] = b"\0" * 8
        spoof[345:500] = b"nanotail-portal".ljust(155, b"\0")
        spoof[148:156] = b" " * 8
        spoof[148:156] = f"{sum(spoof[:512]):06o}\0 ".encode()
        cases["v7-prefix-spoof"] = gzip.compress(spoof, mtime=0)
        for name, archive in cases.items():
            with self.subTest(name=name):
                package = self.write("unsafe.tar.gz", archive)
                self.run_cli("verify", "--package", package, "--public-key", self.public_path, success=False)

    def test_unpacked_limit_counts_streamed_tar_padding(self):
        # Keep the test's memory bounded while expanding a valid signed tar
        # beyond 256 MiB. Zero padding must count towards the unpacked limit.
        unpacked = gzip.decompress(self.archive())
        package = self.cwd / "oversized.tar.gz"
        block = b"\0" * (32 << 10)
        with package.open("wb") as output:
            with gzip.GzipFile(fileobj=output, mode="wb", compresslevel=1, mtime=0) as compressed:
                compressed.write(unpacked)
                remaining = (256 << 20) + 1 - len(unpacked)
                while remaining:
                    count = min(remaining, len(block))
                    compressed.write(block[:count])
                    remaining -= count
        self.assertLess(package.stat().st_size, 2 << 20)
        output = self.run_cli("verify", "-package", package, "-public-key", self.public_path, success=False)
        self.assertIn("limit", output)

    def test_failed_create_cleans_output_and_validates_platform_first(self):
        binary = self.write("portal", self.payload)
        notes = self.write("invalid-notes", b"\xff\xfe")
        output = self.cwd / "release.tar.gz"
        args = ["create", "-binary", binary, "-version", "v1.2.3",
                "-private-key", self.private_path, "-output", output]
        cases = [
            ("--arch", "amd64"), ("--os", "darwin"),
            ("--version", "1.2.3-01"), ("--version", "../1.2.3"),
            ("--binary", self.cwd / "missing"), ("--private-key", self.public_path),
            ("--release-notes", notes),
        ]
        before = set(self.cwd.iterdir())
        for flags in cases:
            with self.subTest(flags=flags):
                message = self.run_cli(*args, *flags, success=False)
                self.assertNotIn("Created signed release package", message)
                self.assertEqual(set(self.cwd.iterdir()), before)
        missing = self.cwd / "missing"
        for command in (
            ["create", "--binary", missing, "--version", "v1.2.3", "--private-key", missing, "--output", output],
            ["verify", "--package", missing, "--public-key", missing],
        ):
            with self.subTest(command=command[0]):
                message = self.run_cli(*command, "--arch", "amd64", success=False)
                self.assertIn("platform", message)
                self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
