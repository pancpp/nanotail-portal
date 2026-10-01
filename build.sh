#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

npm --prefix webui ci --include=dev --no-audit --no-fund
npm --prefix webui run build

# Generate GraphQL before recording the version, since generation may change sources.
(cd app && GOOS="$(go env GOHOSTOS)" GOARCH="$(go env GOHOSTARCH)" go tool gqlgen generate)

conf_package="github.com/pancpp/nanotail-portal/conf"
version="$(git describe --tags --always --dirty)"
build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
git_hash="$(git rev-parse --short HEAD)"
build_number="$(git rev-list --count HEAD)"
target_os="${GOOS:-linux}"
target_arch="${GOARCH:-arm64}"
release_dir="build/releases/$version"

if [[ ! "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*$ ]]; then
    echo "Version cannot be used as a release directory: $version" >&2
    exit 1
fi
mkdir -p "$release_dir"

# Build nanotail-portal
CGO_ENABLED=0 \
GOOS="$target_os" \
GOARCH="$target_arch" \
go build -trimpath \
    -ldflags="-s -w \
        -X $conf_package.gVersion=$version \
        -X $conf_package.gBuildTime=$build_time \
        -X $conf_package.gGitHash=$git_hash \
        -X $conf_package.gBuildNumber=$build_number \
        -X $conf_package.gUseEmbeddedWebUI=true" \
    -o "$release_dir/nanotail-portal"

echo "Built $release_dir/nanotail-portal ($target_os/$target_arch, embedded WebUI)"
