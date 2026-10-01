#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

# Development uses the host architecture and keeps runtime data out of releases.
GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" ./build.sh
version="$(git describe --tags --always --dirty)"
exec "$(pwd)/build/releases/$version/nanotail-portal" --data-dir "$(pwd)/data" "$@"
