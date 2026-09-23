#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

npm --prefix webui ci --include=dev --no-audit --no-fund
npm --prefix webui run build

conf_package="github.com/pancpp/nanotail-portal/conf"
version="$(git describe --tags --always --dirty)"
build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
git_hash="$(git rev-parse --short HEAD)"
build_number="$(git rev-list --count HEAD)"

mkdir -p dist
CGO_ENABLED=0 \
GOOS=linux \
GOARCH=arm64 \
go build -trimpath \
    -ldflags="-s -w \
        -X $conf_package.gVersion=$version \
        -X $conf_package.gBuildTime=$build_time \
        -X $conf_package.gGitHash=$git_hash \
        -X $conf_package.gBuildNumber=$build_number \
        -X $conf_package.gUseEmbeddedWebUI=true" \
    -o nanotail-portal

echo "Built nanotail-portal (Linux ARM64, embedded WebUI)"

./nanotail-portal
