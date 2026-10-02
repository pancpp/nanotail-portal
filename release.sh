#!/usr/bin/env bash
set -euo pipefail

usage() {
    printf 'Usage: %s --key PRIVATE_KEY.pem\n' "${0##*/}"
    printf 'Build, package, and sign a fresh Linux/ARM64 release.\n'
    printf 'The key path is relative to the directory where you invoke this command.\n'
    printf 'The verified archive is saved under the repository build/ directory.\n'
}

private_key=''
while (($#)); do
    case "$1" in
        --key)
            if (($# < 2)) || [[ -z "$2" || "$2" == --* ]]; then
                printf '(release) --key requires a private key path\n' >&2
                exit 1
            fi
            private_key=$2
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            printf '(release) unexpected argument: %s\n' "$1" >&2
            usage >&2
            exit 1
            ;;
    esac
done

if [[ -n "${GOOS+x}" && "${GOOS-}" != linux ]]; then
    printf '(release) nanotail supports only Linux/ARM64; GOOS must be linux (got %q)\n' "${GOOS-}" >&2
    exit 1
fi
if [[ -n "${GOARCH+x}" && "${GOARCH-}" != arm64 ]]; then
    printf '(release) nanotail supports only Linux/ARM64; GOARCH must be arm64 (got %q)\n' "${GOARCH-}" >&2
    exit 1
fi

if [[ -z "$private_key" ]]; then
    usage >&2
    exit 1
fi
if [[ "$private_key" != /* ]]; then
    private_key="$PWD/$private_key"
fi
if [[ ! -f "$private_key" || ! -r "$private_key" ]]; then
    printf '(release) private key is not a readable file: %s\n' "$private_key" >&2
    exit 1
fi

cd "$(dirname "${BASH_SOURCE[0]}")"
repo_dir=$PWD
if ! command -v python3 >/dev/null 2>&1; then
    printf '(release) required command not found: python3\n' >&2
    exit 1
fi
valid_version() {
    local version=$1 prerelease identifier
    local -a identifiers
    local pattern='^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
    [[ ${#version} -le 128 && "$version" =~ $pattern ]] || return 1
    prerelease=${BASH_REMATCH[5]}
    IFS=. read -r -a identifiers <<< "$prerelease"
    for identifier in "${identifiers[@]}"; do
        if [[ "$identifier" =~ ^[0-9]+$ && ${#identifier} -gt 1 && "$identifier" == 0* ]]; then
            return 1
        fi
    done
}

# A clean semantic-version tag is a release. Other working trees get a unique
# development version that the signed-package verifier can also understand.
tag=$(git describe --tags --exact-match 2>/dev/null || true)
tagged_release=false
if valid_version "$tag" && [[ -z "$(git status --porcelain --untracked-files=normal)" ]]; then
    version=$tag
    tagged_release=true
else
    git_hash=$(git rev-parse --short HEAD)
    build_number=$(git rev-list --count HEAD)
    version="v0.0.0-dev.$build_number.$(date -u +%Y%m%d%H%M%S)+g$git_hash"
    if [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
        version+=.dirty
    fi
fi

mkdir -p "$repo_dir/build"
output="$repo_dir/build/nanotail-portal-$version-linux-arm64.tar.gz"
if [[ -e "$output" || -L "$output" ]]; then
    printf '(release) output already exists; refusing to overwrite: %s\n' "$output" >&2
    exit 1
fi
work_dir=$(mktemp -d "$repo_dir/build/.nanotail-release.XXXXXXXX")
trap 'rm -rf -- "$work_dir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

printf '(release) Building %s for linux/arm64\n' "$version"
NANOTAIL_BUILD_VERSION="$version" NANOTAIL_BUILD_DIR="$work_dir/build" \
    GOOS=linux GOARCH=arm64 ./build.sh
if $tagged_release && [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
    printf '(release) generated sources changed the tagged working tree; review them and rerun\n' >&2
    exit 1
fi

# Sign on the build machine. Neither key material nor this tool is packaged.
package_args=(
    create --binary "$work_dir/build/nanotail-portal"
    --version "$version" --os linux --arch arm64
    --private-key "$private_key" --output "$work_dir/package.tar.gz"
    --service "$repo_dir/nanotail-portal.service"
    --nginx "$repo_dir/nanotail-portal.nginx"
)
if [[ -f "$repo_dir/LICENSE" ]]; then
    package_args+=(--license "$repo_dir/LICENSE")
fi
python3 "$repo_dir/scripts/release_package.py" "${package_args[@]}"
python3 "$repo_dir/scripts/release_package.py" verify --package "$work_dir/package.tar.gz" \
    --os linux --arch arm64

# Publish only a verified archive. A hard link on this same filesystem is
# atomic and refuses an existing destination, including a racing release.
ln -T -- "$work_dir/package.tar.gz" "$output"
printf '(release) Signed package: %s\n' "$output"
