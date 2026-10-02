"""Device-signing tests with all SSH and OpenSSL commands replaced by fakes."""

import base64
import contextlib
import importlib.util
import io
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory
import unittest
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("sign_device.py")
SPEC = importlib.util.spec_from_file_location("sign_device_script", SCRIPT_PATH)
SIGN_DEVICE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SIGN_DEVICE)


class SignDeviceTests(unittest.TestCase):
    def setUp(self):
        self.directory = TemporaryDirectory(prefix="nanotail-sign-test-")
        self.addCleanup(self.directory.cleanup)
        self.private_key = Path(self.directory.name) / "fake offline key.pem"
        self.private_key.write_bytes(b"fake key fixture; never passed to real OpenSSL")
        self.target = "root@device.example"
        self.messages = []
        self.temporary_paths = []

        # Patch every subprocess call, including commands added to the script
        # later. An unexpected command fails the test instead of being executed.
        run_patch = mock.patch.object(SIGN_DEVICE.subprocess, "run")
        self.run = run_patch.start()
        self.addCleanup(run_patch.stop)

        def temporary_directory(*, prefix, dir):
            directory = TemporaryDirectory(prefix=prefix, dir=self.directory.name)
            self.temporary_paths.append(Path(directory.name))
            return directory

        temporary_patch = mock.patch.object(
            SIGN_DEVICE.tempfile, "TemporaryDirectory", side_effect=temporary_directory,
        )
        self.temporary_directory = temporary_patch.start()
        self.addCleanup(temporary_patch.stop)

    def fake_commands(self, *, serial=b"0123456789ABCDEF\0",
                      key_info=b"ED25519 Public-Key:\n", signature=bytes(range(64)),
                      fail_at=None):
        def run(command, **kwargs):
            self.assertTrue(kwargs.get("check"))
            self.assertNotIn("shell", kwargs)
            if command[:2] == ["openssl", "pkey"]:
                self.assertEqual(command, [
                    "openssl", "pkey", "-in", str(self.private_key),
                    "-passin", "pass:", "-noout", "-text_pub",
                ])
                self.assertEqual(kwargs.get("stdout"), subprocess.PIPE)
                if fail_at == "key":
                    raise subprocess.CalledProcessError(1, command)
                return subprocess.CompletedProcess(command, 0, stdout=key_info)
            if command and command[0] == "ssh":
                self.assertEqual(command, [
                    "ssh", "-T", "--", self.target,
                    "cat /sys/firmware/devicetree/base/serial-number",
                ])
                self.assertEqual(kwargs.get("stdout"), subprocess.PIPE)
                if fail_at == "ssh":
                    raise subprocess.CalledProcessError(255, command)
                return subprocess.CompletedProcess(command, 0, stdout=serial)
            if command[:2] == ["openssl", "pkeyutl"]:
                self.assertEqual(command[:8], [
                    "openssl", "pkeyutl", "-sign", "-rawin", "-inkey",
                    str(self.private_key), "-passin", "pass:",
                ])
                self.assertEqual(command[8], "-in")
                self.assertEqual(command[10], "-out")
                self.assertEqual(len(command), 12)
                message, signature_file = Path(command[9]), Path(command[11])
                self.assertEqual(message.parent, signature_file.parent)
                self.assertIn(message.parent, self.temporary_paths)
                self.messages.append(message.read_bytes())
                # A failed signing command may leave a partial output file.
                signature_file.write_bytes(signature)
                if fail_at == "sign":
                    raise subprocess.CalledProcessError(1, command)
                return subprocess.CompletedProcess(command, 0)
            self.fail(f"unexpected external command: {command!r}")

        self.run.side_effect = run

    def assert_temporary_files_removed(self):
        self.assertTrue(self.temporary_paths, "signing never created its work directory")
        for path in self.temporary_paths:
            self.assertFalse(path.exists(), f"signing left temporary files in {path}")

    def test_signs_lowercase_domain_message_and_returns_base64(self):
        signature = bytes(range(64))
        self.fake_commands(serial=b"aBcD0123eF456789\0", signature=signature)

        result = SIGN_DEVICE.sign_device(self.target, self.private_key)

        self.assertEqual(self.messages, [b"nanotail-server/auth/device/v1\0abcd0123ef456789"])
        self.assertEqual(result, base64.b64encode(signature).decode("ascii"))
        self.assertEqual(base64.b64decode(result, validate=True), signature)
        self.assertEqual(self.run.call_count, 3)
        self.assert_temporary_files_removed()

    def test_rejects_serials_without_exact_hex_length_and_one_nul(self):
        invalid_serials = (
            b"", b"0123456789abcdef", b"0123456789abcde\0",
            b"0123456789abcdef0\0", b"0123456789abcdeg\0",
            b"01234567\x0089abcdef\0", b"0123456789abcdef\0\0",
            b"0123456789abcdef\n", b"0123456789abcdef\0\n",
            b"0123456789abcdef\r\n", b" 0123456789abcdef\0",
            b"0123456789abcdef\0extra", "０１２３４５６７８９abcdef\0".encode("utf-8"),
        )
        for serial in invalid_serials:
            with self.subTest(serial=serial):
                self.run.reset_mock()
                self.fake_commands(serial=serial)
                with self.assertRaisesRegex(ValueError, "16 hex characters and a NUL"):
                    SIGN_DEVICE.sign_device(self.target, self.private_key)
                self.assertEqual(self.run.call_count, 2)
                self.temporary_directory.assert_not_called()
                self.assertEqual(self.messages, [])

    def test_rejects_non_ed25519_keys_before_contacting_device(self):
        for key_info in (b"", b"RSA Public-Key:\n", b"ED448 Public-Key:\n"):
            with self.subTest(key_info=key_info):
                self.run.reset_mock()
                self.fake_commands(key_info=key_info)
                with self.assertRaisesRegex(ValueError, "must be Ed25519"):
                    SIGN_DEVICE.sign_device(self.target, self.private_key)
                self.assertEqual(self.run.call_count, 1)
                self.temporary_directory.assert_not_called()

    def test_rejects_signatures_that_are_not_64_raw_bytes(self):
        for size in (0, 63, 65):
            with self.subTest(size=size):
                self.fake_commands(signature=b"s" * size)
                with self.assertRaisesRegex(ValueError, "exactly 64 bytes"):
                    SIGN_DEVICE.sign_device(self.target, self.private_key)
                self.assert_temporary_files_removed()

    def test_key_inspection_failure_stops_before_ssh(self):
        self.fake_commands(fail_at="key")
        with self.assertRaises(subprocess.CalledProcessError):
            SIGN_DEVICE.sign_device(self.target, self.private_key)
        self.assertEqual(self.run.call_count, 1)
        self.temporary_directory.assert_not_called()

    def test_ssh_failure_stops_before_creating_signing_files(self):
        self.fake_commands(fail_at="ssh")
        with self.assertRaises(subprocess.CalledProcessError):
            SIGN_DEVICE.sign_device(self.target, self.private_key)
        self.assertEqual(self.run.call_count, 2)
        self.temporary_directory.assert_not_called()

    def test_signing_failure_removes_message_and_partial_signature(self):
        self.fake_commands(fail_at="sign", signature=b"partial signature")
        with self.assertRaises(subprocess.CalledProcessError):
            SIGN_DEVICE.sign_device(self.target, self.private_key)
        self.assertEqual(self.run.call_count, 3)
        self.assert_temporary_files_removed()

    def test_main_prints_only_base64_signature(self):
        signature = b"s" * 64
        self.fake_commands(signature=signature)
        output, errors = io.StringIO(), io.StringIO()
        with mock.patch.object(sys, "argv", [
            str(SCRIPT_PATH), "--target", self.target, "--key", str(self.private_key),
        ]), mock.patch.object(SIGN_DEVICE.os, "umask"), \
                contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            result = SIGN_DEVICE.main()
        self.assertEqual(result, 0)
        self.assertEqual(output.getvalue(), base64.b64encode(signature).decode("ascii") + "\n")
        self.assertEqual(errors.getvalue(), "")
        self.assert_temporary_files_removed()


if __name__ == "__main__":
    unittest.main()
