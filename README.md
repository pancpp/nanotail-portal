# Fairnet Portal

Go/Echo backend and React WebUI for managing Tailscale on a NanoPi Zero2.

## Run locally

Requires Go 1.26 and Node/npm for the WebUI build.

```sh
go run .
```

The backend listens on port `8080`. On first start it creates the administrator
account with **username `admin` and password `admin`**. Credentials are stored as
a salted PBKDF2-SHA256 hash in `data/admin.json`; the plaintext password is not
stored. Changing the password persists across restarts.

Configuration is optional. Defaults work from the repository directory without
root permissions. To customize them, copy `fairnet-portal.example.yml` to
`fairnet-portal.yml`, or select another YAML file:

```sh
go run . --config /path/to/fairnet-portal.yml
```

All example configuration keys support `FAIRNET_` environment overrides, such as
`FAIRNET_HTTP_LISTEN_ADDR=127.0.0.1:8080`. Paths are relative to the process working
directory. The default log directory is `logs/`, with rotation at 10 MB and three
backups. Use `go run . --version` to print build metadata without starting services.

For frontend development, start the backend and run `npm run dev` in `webui/`.
Vite forwards `/api` to `127.0.0.1:8080`. Alternatively, build the frontend once:

```sh
npm --prefix webui ci
npm --prefix webui run build
go run .
```

Open `http://localhost:8080` to use the built UI. The backend serves `/`, `/login`,
and `/assets/` from `webui/dist`. It also works without a frontend build, exposing
only the API. A new frontend build is detected when the backend starts.

## Tailscale integration

Install and run `tailscaled` on the device, and authenticate the device with
Tailscale. The portal invokes the installed `tailscale` binary directly, without
a shell. The portal's OS user needs permission to manage the daemon, for example
through Tailscale's `--operator` setting. Configure `tailscale_binary` and
`tailscale_socket` when the executable or socket is not in its default location.

Status uses `tailscale status --json`. Preferences are read using `tailscale debug
prefs` for compatibility with clients predating `tailscale get`; only selected
fields are returned, never the daemon's `Persist` object or private keys. The
debug command's JSON is not a stable public API, so verify compatibility with the
Tailscale version installed on the NanoPi. Writes use `tailscale set`, preserving
omitted preferences. See the [official CLI reference](https://tailscale.com/docs/reference/tailscale-cli).

Connect/disconnect uses `tailscale up` and `tailscale down`; it does not log the
device out. Commands are bounded by `tailscale_timeout` (15 seconds by default),
and writes are serialized. A timeout does not guarantee that a requested change
was rolled back; read status/configuration before retrying. If Tailscale needs
authentication, `/status` returns `backend_state` and `auth_url` when available.
Initial Tailscale enrollment is still performed with the CLI. Disconnecting or
changing routing can interrupt access to the portal through Tailscale.

The WebUI login uses these backend sessions. Its dashboard still contains the
original sample network data; wiring the dashboard and settings controls to the
new Tailscale APIs is a separate frontend step.

## API

Requests with JSON bodies require `Content-Type: application/json`, reject unknown
fields, and are limited to 64 KiB. Errors use `{"message":"..."}`. Protected routes
require `Authorization: Bearer <access_token>`. Query-string tokens are not accepted.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/health` | Public portal liveness; does not indicate Tailscale health |
| POST | `/api/user/login` | Public login with `username` and `password` |
| GET | `/api/user/me` | Current username and session expiry |
| POST | `/api/user/logout` | Revoke the current session; returns 204 |
| PUT | `/api/user/password` | Change password; returns 204 and revokes all sessions |
| GET | `/api/system` | Hostname, OS, architecture, portal uptime and build metadata |
| GET | `/api/tailscale/status` | State, self, peers, health messages and traffic counters |
| GET | `/api/tailscale/peers` | Sorted peer array |
| GET | `/api/tailscale/config` | Current supported preferences |
| PATCH | `/api/tailscale/config` | Change only submitted preferences; returns 204 |
| POST | `/api/tailscale/up` | Connect an enrolled node; returns 204 |
| POST | `/api/tailscale/down` | Disconnect the node; returns 204 |

Login returns `access_token`, `token_type` (`Bearer`), `username` and `expires_at`.
Sessions last 12 hours by default and are revoked on logout, password change or
portal restart. At most 32 sessions are retained; a new login evicts the oldest
when full. Login/password-change requests share a global limit of 10 initial
attempts, replenishing one attempt every six seconds (`429` with `Retry-After`).

```sh
curl -X POST http://localhost:8080/api/user/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'
```

To change the password, send the following JSON to `PUT /api/user/password`, with
the bearer token from login. New passwords must be 8–1024 bytes. Sign in again
after a successful change.

```json
{"current_password":"admin","new_password":"choose-a-new-password"}
```

A configuration PATCH may include `hostname`, `accept_dns`, `accept_routes`,
`shields_up`, `exit_node`, `exit_node_allow_lan_access`, `advertise_routes`, and
`advertise_exit_node`. Booleans can be explicitly set to `false`; an omitted field
is preserved. Use `exit_node: ""` to stop using an exit node and
`advertise_routes: []` to clear subnet advertisements. Routes must be canonical
CIDRs such as `192.168.1.0/24`; use `advertise_exit_node` for default routes.
`exit_node` accepts an IP or DNS name. When reading preferences, `exit_node_id`
reports Tailscale's stable ID if the daemon has resolved the selection to an ID;
match it against peer `id` values to obtain the address/name for a later PATCH.

Status counters are cumulative byte counts from Tailscale, **not 24-hour totals**.
`portal_uptime_seconds` is the lifetime of the portal process, not device uptime.
Missing Tailscale/daemon access returns `503`, a command deadline returns `504`,
and invalid daemon JSON returns `502`. The portal can start without Tailscale
installed, so login and frontend development remain available.

## Verify and build

```sh
go test -race ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o fairnet-portal.arm64 .
```

Tests use isolated credential files and fake Tailscale runners, and exercise
authentication, revocation, persistence, validation, API protection, static asset
serving, startup failures and graceful shutdown. They never change the host's
Tailscale configuration. A live NanoPi test is still needed for device integration.
