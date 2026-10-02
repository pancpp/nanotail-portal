#!/usr/bin/env python3
"""Test access QR generation without contacting a device."""

from contextlib import redirect_stderr, redirect_stdout
import importlib.util
import io
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

from PIL import Image


SPEC = importlib.util.spec_from_file_location(
    "access_qrcode", Path(__file__).with_name("access_qrcode.py"),
)
ACCESS_QRCODE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ACCESS_QRCODE)


class AccessQRCodeTests(unittest.TestCase):
    def run_cli(self, arguments, serial=b"ABCDEF0123456789\0", error=None):
        stdout, stderr = io.StringIO(), io.StringIO()
        response = subprocess.CompletedProcess([], 0, stdout=serial)
        with mock.patch.object(ACCESS_QRCODE.subprocess, "run", return_value=response, side_effect=error), \
                mock.patch.object(ACCESS_QRCODE.sys, "argv", ["access_qrcode.py", *arguments]), \
                redirect_stdout(stdout), redirect_stderr(stderr):
            status = ACCESS_QRCODE.main()
        return status, stdout.getvalue(), stderr.getvalue()

    def test_device_id_is_canonical_and_ssh_target_is_one_argument(self):
        response = subprocess.CompletedProcess([], 0, stdout=b"ABCDEF0123456789\0")
        with mock.patch.object(ACCESS_QRCODE.subprocess, "run", return_value=response) as run:
            self.assertEqual(ACCESS_QRCODE.read_device_id("user@nanotail"), "abcdef0123456789")
        run.assert_called_once_with(
            ["ssh", "-T", "--", "user@nanotail", "cat /sys/firmware/devicetree/base/serial-number"],
            stdout=subprocess.PIPE, check=True,
        )

    def test_rejects_malformed_device_ids(self):
        for serial in (b"", b"abcdef0123456789", b"abcdef0123456789\n", b"abcdef0123456789\0\0",
                       b"g123456789abcdef\0", b"abcdef\0"):
            with self.subTest(serial=serial):
                response = subprocess.CompletedProcess([], 0, stdout=serial)
                with mock.patch.object(ACCESS_QRCODE.subprocess, "run", return_value=response):
                    with self.assertRaises(ValueError):
                        ACCESS_QRCODE.read_device_id("nanotail")

    def test_terminal_output_contains_canonical_access_url_and_qr(self):
        status, stdout, stderr = self.run_cli(["--target", "nanotail"])
        self.assertEqual(status, 0, stderr)
        self.assertEqual(stderr, "")
        self.assertTrue(stdout.startswith(
            "Access URL: https://tailscale.fairkid.ca/redirect/abcdef0123456789\n",
        ))
        self.assertGreater(len(stdout.splitlines()), 10)
        self.assertNotIn("Saved QR code", stdout)

    def test_optional_output_is_a_png(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "access.png"
            status, stdout, stderr = self.run_cli(["--target", "nanotail", "--output", str(output)])
            self.assertEqual(status, 0, stderr)
            self.assertIn(f"Saved QR code to {output}", stdout)
            with Image.open(output) as image:
                self.assertEqual(image.format, "PNG")
                self.assertEqual(image.width, image.height)
                self.assertGreater(image.width, 100)
                image.verify()

    def test_ssh_error_does_not_create_output(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "access.png"
            status, stdout, stderr = self.run_cli(
                ["--target", "nanotail", "--output", str(output)],
                error=subprocess.CalledProcessError(255, ["ssh"]),
            )
            self.assertEqual(status, 1)
            self.assertEqual(stdout, "")
            self.assertIn("(access-qrcode)", stderr)
            self.assertFalse(output.exists())

    def test_blank_target_is_rejected_before_ssh(self):
        with mock.patch.object(ACCESS_QRCODE.subprocess, "run") as run, \
                mock.patch.object(ACCESS_QRCODE.sys, "argv", ["access_qrcode.py", "--target", " "]), \
                redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit) as result:
                ACCESS_QRCODE.main()
        self.assertEqual(result.exception.code, 2)
        run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
