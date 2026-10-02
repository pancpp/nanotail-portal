#!/usr/bin/env python3
"""Read a device ID over SSH and print its locally generated signature."""

import argparse
import base64
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


def sign_device(target, private_key):
    # Read public metadata only; the private key remains on the signing host.
    key_info = subprocess.run(
        ["openssl", "pkey", "-in", str(private_key), "-passin", "pass:",
         "-noout", "-text_pub"],
        stdout=subprocess.PIPE, check=True,
    ).stdout
    if not key_info.startswith(b"ED25519 Public-Key:"):
        raise ValueError("private key must be Ed25519")

    serial = subprocess.run(
        ["ssh", "-T", "--", target,
         "cat /sys/firmware/devicetree/base/serial-number"],
        stdout=subprocess.PIPE, check=True,
    ).stdout
    if re.fullmatch(rb"[0-9a-fA-F]{16}\x00", serial) is None:
        raise ValueError("device ID must be exactly 16 hex characters and a NUL terminator")
    # Vendor storage holds eight raw ID bytes, which the reporter formats as
    # lowercase hex. Sign that text with the same domain prefix as the server.
    device_id = serial[:-1].lower()

    with tempfile.TemporaryDirectory(prefix="nanotail-sign.", dir="/tmp") as directory:
        work = Path(directory)
        message = work / "message"
        signature_file = work / "signature"
        message.write_bytes(b"nanotail-server/auth/device/v1\0" + device_id)
        subprocess.run(
            ["openssl", "pkeyutl", "-sign", "-rawin", "-inkey", str(private_key),
             "-passin", "pass:", "-in", str(message), "-out", str(signature_file)],
            check=True,
        )
        signature = signature_file.read_bytes()
        if len(signature) != 64:
            raise ValueError("Ed25519 signature must be exactly 64 bytes")
        return base64.b64encode(signature).decode("ascii")


def main():
    parser = argparse.ArgumentParser(
        description="Read a device ID over SSH and sign it locally with an Ed25519 key.",
        allow_abbrev=False,
    )
    parser.add_argument("--target", required=True, help="SSH destination, e.g. nanotail.local")
    parser.add_argument("--key", required=True, type=Path, help="Ed25519 private PEM key")
    args = parser.parse_args()
    if not args.target.strip():
        parser.error("--target must not be empty")

    os.umask(0o077)
    try:
        private_key = args.key.expanduser().resolve(strict=True)
        if not private_key.is_file():
            raise ValueError("private key must be a regular file")
        print(sign_device(args.target, private_key))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"(sign) {error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("(sign) interrupted", file=sys.stderr)
        return 130
    return 0


if __name__ == "__main__":
    sys.exit(main())
