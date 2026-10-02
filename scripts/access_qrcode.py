#!/usr/bin/env python3
"""Print a device's access QR code and optionally save it as a PNG image."""

import argparse
from pathlib import Path
import re
import subprocess
import sys

import qrcode
from qrcode.image.pil import PilImage


ACCESS_URL_PREFIX = "https://tailscale.fairkid.ca/redirect/"


def read_device_id(target):
    serial = subprocess.run(
        ["ssh", "-T", "--", target,
         "cat /sys/firmware/devicetree/base/serial-number"],
        stdout=subprocess.PIPE, check=True,
    ).stdout
    if re.fullmatch(rb"[0-9a-fA-F]{16}\x00", serial) is None:
        raise ValueError("device ID must be exactly 16 hex characters and a NUL terminator")
    return serial[:-1].decode("ascii").lower()


def main():
    parser = argparse.ArgumentParser(
        description="Read a device ID over SSH and display its access URL and QR code.",
        allow_abbrev=False,
    )
    parser.add_argument("--target", required=True, help="SSH destination, e.g. nanotail")
    parser.add_argument("--output", type=Path, help="optional PNG output file, e.g. qrcode.png")
    args = parser.parse_args()
    if not args.target.strip():
        parser.error("--target must not be empty")

    try:
        access_url = ACCESS_URL_PREFIX + read_device_id(args.target)
        qr = qrcode.QRCode(
            error_correction=qrcode.constants.ERROR_CORRECT_M,
            box_size=10,
            border=4,
        )
        qr.add_data(access_url)
        qr.make(fit=True)

        print(f"Access URL: {access_url}")
        qr.print_ascii(tty=sys.stdout.isatty(), invert=True)
        if args.output is not None:
            output = args.output.expanduser()
            qr.make_image(image_factory=PilImage).save(output, format="PNG")
            print(f"Saved QR code to {output}")
    except (OSError, ValueError, UnicodeError, subprocess.CalledProcessError) as error:
        print(f"(access-qrcode) {error}", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print("(access-qrcode) interrupted", file=sys.stderr)
        return 130
    return 0


if __name__ == "__main__":
    sys.exit(main())
