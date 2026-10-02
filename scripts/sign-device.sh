#!/usr/bin/env bash
set -euo pipefail

usage() {
    printf 'Usage: %s --target TARGET_DEVICE --key PRIVATE_KEY.pem\n' "${0##*/}"
    printf 'TARGET_DEVICE is an SSH destination (for example, nanotail.local or user@nanotail.local).\n'
}

target=''
private_key_path=''
while (($#)); do
    case "$1" in
        --target|--key)
            if (($# < 2)) || [[ "$2" == --* ]]; then
                printf '(sign) missing value for %s\n' "$1" >&2
                exit 1
            fi
            if [[ "$1" == --target ]]; then
                target=$2
            else
                private_key_path=$2
            fi
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            printf '(sign) unexpected argument: %s\n' "$1" >&2
            usage >&2
            exit 1
            ;;
    esac
done

if [[ -z "$target" ]]; then
    printf '(sign) --target is required\n' >&2
    exit 1
fi
if [[ -z "$private_key_path" ]]; then
    printf '(sign) --key is required\n' >&2
    exit 1
fi

# Read only public metadata to ensure OpenSSL uses the required key algorithm.
key_info=$(openssl pkey -in "$private_key_path" -passin pass: -noout -text_pub)
if [[ "$key_info" != 'ED25519 Public-Key:'* ]]; then
    printf '(sign) private key must be Ed25519\n' >&2
    exit 1
fi

umask 077
signing_dir=$(mktemp -d "${TMPDIR:-/tmp}/nanotail-sign.XXXXXXXX")
trap 'rm -rf -- "$signing_dir"' EXIT

# Keep the remote bytes in a file so Bash does not discard the NUL terminator.
if ! ssh -T -- "$target" 'cat /sys/firmware/devicetree/base/serial-number' > "$signing_dir/device-id"; then
    printf '(sign) read device ID from %s over SSH failed\n' "$target" >&2
    exit 1
fi

# Device-tree strings end with a NUL byte, which is not part of the ID.
device_id=''
if ! IFS= read -r -d '' device_id < "$signing_dir/device-id"; then
    printf '(sign) read NUL-terminated device ID from %s failed\n' "$target" >&2
    exit 1
fi
if [[ ! "$device_id" =~ ^[0-9a-fA-F]{16}$ ]]; then
    printf '(sign) device ID must contain exactly 16 hexadecimal characters\n' >&2
    exit 1
fi
# The stored eight bytes decode to lowercase hex in the reporter.
device_id=${device_id,,}

# Ed25519 needs the complete input; keep the NUL byte in a file, not a variable.
printf 'nanotail-server/auth/device/v1\0%s' "$device_id" > "$signing_dir/message"
openssl pkeyutl -sign -rawin -inkey "$private_key_path" -passin pass: \
    -in "$signing_dir/message" -out "$signing_dir/signature"
openssl base64 -A -in "$signing_dir/signature"
printf '\n'
