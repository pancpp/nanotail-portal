# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Go (Echo v5) backend plus React/Vite WebUI for managing Tailscale on a "nanotail" device (NanoPi Zero2). Device releases and upgrade packages target **Linux/ARM64 only**. `README.md` is the detailed behavioral spec (API contracts, upgrade/recovery semantics, factory reset, config). Behavior-changing commits usually update it, so check the relevant README section before changing behavior. Commit subjects use `feat:`, `fix:`, `refactor:`, `chore:`, and `ui:` prefixes.

## Commands

Backend (no frontend build needed):

```sh
go test -race ./...
go vet ./...
go test -race -run TestName ./app/...      # single test
go run . --data-dir ./data                 # serves on 127.0.0.1:7080; default login admin/admin
```

Embedded-WebUI build tag (`embedwebui`). Some tests exist only under this tag (`main_test.go`, `upgrade_recovery_test.go`, `app/embedded_webui_test.go`) and need `webui/dist` built first:

```sh
npm --prefix webui ci && npm --prefix webui run build
go test -race -tags embedwebui ./...
go vet -tags embedwebui ./...
```

`main_test.go` builds and re-execs a real portal binary (factory-reset process test). It is skipped with `-short`.

Frontend (`webui/`):

```sh
npm --prefix webui run dev                    # Vite; proxies /api to 127.0.0.1:7080
npm --prefix webui test                       # node --test on tests/*.test.mjs (imports src/*.ts directly)
node --experimental-strip-types --test webui/tests/upgrade.test.mjs   # single test file
npm --prefix webui run typecheck
npm --prefix webui run test:routing-browser   # headless Chrome; needs a prior build; CHROME_BIN overrides path
```

`routing.browser.mjs` is the only browser-test entry point. The other `*.browser.mjs` files export `check*` functions that it imports and runs against mocked API responses.

Python utilities (`scripts/`):

```sh
python3 -m pip install -r scripts/requirements.txt
python3 -m unittest discover -s scripts -p 'test_*.py'
python3 scripts/test_release_package.py      # single utility
```

Each utility's Python tests live in `scripts/test_*.py` and run with `unittest`.
Use temporary files and test keys, and mock device operations. Python scripts
are tested in Python; Go tests cover Go code.

GraphQL code generation (required after editing `app/graph/schema.graphqls`):

```sh
cd app && go tool gqlgen generate
```

Build/release:
- `./build.sh` builds the WebUI, regenerates GraphQL, and produces a static `embedwebui` binary at `build/releases/<version>/nanotail-portal` (default target linux/arm64; `GOOS`/`GOARCH` override it). Version metadata is injected into `conf.gVersion` and related vars via `-ldflags -X`.
- `./run.sh [args]` builds for the host and runs with `--data-dir ./data`.
- `./release.sh --key .release-signing/release-private.pem` builds, then signs and verifies an upgrade package with `scripts/release_package.py` (Python 3 and the `cryptography` package), writing `build/nanotail-portal-<version>-linux-arm64.tar.gz`. The embedded trust key is `upgrade/release-public.pem`.

## Architecture

**Startup sequence (`main.go`)**: `--upgrade-recover` mode runs `upgrade.RunRecovery` and nothing else → prepare data dir → `factoryreset.Files` takes the instance lock and resumes any pending reset cleanup → `conf.Init` → `logger.Init` → `upgrade.Installer.BeforeStartup` → `database.Init` → `migrations.Init` (auto-applied, flock-guarded) → `app.Init` → loop on `app.Start`. The loop waits for a server error, a signal, a factory-reset request, or an upgrade-install request. It then drains the runtime (`Runtime.Shutdown`) and either re-execs itself for reset cleanup (after `tailscale logout`) or activates the upgrade and exits so systemd restarts the new release.

**Maintenance gate**: `maintenance.Gate` is a single-owner lock shared by factory reset and upgrade install. While either is pending, `app.maintenanceMiddleware` returns 503 for everything except `POST /api/v1/query`. The GraphQL layer itself restricts that endpoint to `upgradeStatus`-only operations (`app/upgrade_graphql.go`).

**Global singletons**: `conf` (viper; flags are parsed in `init()`), `database.DB()` (bun + SQLite), `auth` (JWT signing key in `<data-dir>/nanotail-portal.key`), and `logger` are package-level globals initialized in order. Config comes only from YAML. Env vars do not override settings, except `NANOTAIL_DATA_DIR` for the data directory. Paths for config, DB, and logs resolve relative to the data dir (`conf/paths.go`).

**HTTP surface (`app/`)**: a few REST routes (`/api/health`, `/api/login`, `/api/system`, `/api/tailscale/*`, factory reset) and one authenticated GraphQL endpoint, `/api/v1/query`, which carries most features. `webui.Init` serves the SPA either from embedded `dist` (`embedwebui` tag) or from `webui/dist` on disk (untagged; run from the repo root).

**GraphQL (`app/graph/`)**: gqlgen with a single-file `generated.go` and follow-schema resolvers (`*.resolvers.go`, `preserve_resolver: true`). `resolver.go` defines small interfaces (`TailscaleRouter`, `DeviceStatusReader`, etc.) that `graph.Resolver` depends on. `app/graph.go` wires concrete implementations (`tailscale.Client`, `device.*`, `traffic.Store`, `upgrade.Service`), and tests substitute fakes. Upgrade types are bound to `upgrade.*` structs in `app/gqlgen.yml`. Mutations gate on `requireAdmin(ctx, Err...)`. Resolver errors are mapped to safe messages and `extensions.code` by the error presenter.

**Domain packages**:
- `tailscale/`: wraps the `tailscale` CLI (no shell) through an injectable `Runner` interface. Mutations are serialized through a 1-slot channel. It also handles OAuth route approval, peer-relay policy (HuJSON), and node-key renewal. `MaintainRouting` runs as a background worker.
- `device/`: Linux host reads and writes: status, hostname, LAN IPv4 via `nmcli`, routing/forwarding, and traffic counters.
- `traffic/`: minute-based recorder that persists 24h history and totals to SQLite. `activityled/` drives the VPN activity LED via sysfs.
- `access/`: reports device hostname and IPs to the remote API. Device credentials come from SD-card vendor storage (`/dev/mmcblk0`).
- `upgrade/`: GitHub release check → download → signed-package verify (Ed25519 manifest) → staged install → symlink swap (`current`/`previous`) → independent recovery service with a durable journal. Fixed paths live under `/srv/nanotail-portal` (`upgrade/paths.go`).
- `factoryreset/`: two-phase reset (intent marker → re-exec → cleanup on next start) restricted to allowed runtime files in the data dir.
- `migrations/`: bun migrations registered in `init()`. Filenames are `YYYYMMDDHHMMSS_name.go`, and the migration name derives from the filename. Code rollback never rolls back the DB, so migrations must stay compatible with the previous release.

**Testing conventions**: tests never touch host Tailscale, LEDs, or the network. They use fake `tailscale.Runner`s, fake sysfs dirs, temp data dirs, and in-memory/local HTTP transports. Subprocess tests compile a binary with a Go build `-overlay` that injects test-only `conf` defaults (fake tailscale binary, LED and access disabled) and isolates the fixed upgrade paths. Follow that pattern rather than adding env-var config.

**Frontend (`webui/src/`)**: React 19 + react-router with lazy-loaded pages (`pages/`). No GraphQL client library: feature modules (`upgrade.ts`, `routing.ts`, `api.ts`, …) build GraphQL requests with `fetch` and must check `errors` even on HTTP 200. Hooks (`use*.ts`) hold polling and flow state. Unit tests import these `.ts` modules directly.

**i18n**: English strings are the catalog keys; `t('English text')` is translated through `src/locales/zh-CN.ts`. `tests/localization.test.mjs` parses the source and fails if any static `t(...)` string lacks a zh-CN entry or a translation drops a placeholder. Every new UI string therefore needs a matching zh-CN entry.
