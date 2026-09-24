# Nanotail Portal

Go/Echo backend and React WebUI for managing Tailscale on nanotail.

## Run locally

Requires Go 1.26 and Node/npm for the WebUI build.

```sh
go run .
```

The backend listens on port `8080`. The initial database migration creates the
administrator account with **username `admin` and password `admin`**. Passwords
are stored as salted bcrypt hashes in SQLite, never as plaintext. Changing the
password persists across restarts.

Configuration is read from `nanotail-portal.yml` in the process working directory.
Create this file before starting; an empty file uses the built-in defaults.
The default database is `nanotail-portal.sqlite3`. When upgrading an existing
installation, stop the portal and copy your existing configuration and database
to these names, or set `database` in the new configuration to your existing
database path. The rename does not move existing runtime files automatically.

Paths are relative to the process working directory. Logs are written to
`logs/nanotail-portal.log`, with rotation at 10 MB and three backups.
Use `go run . --version` to print build metadata without starting services.

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
Tailscale version installed on nanotail. Writes use `tailscale set`, preserving
omitted preferences. See the [official CLI reference](https://tailscale.com/docs/reference/tailscale-cli).

Connect/disconnect uses `tailscale up` and `tailscale down`; it does not log the
device out. Commands are bounded by `tailscale_timeout` (15 seconds by default),
and writes are serialized. A timeout does not guarantee that a requested change
was rolled back; read status/configuration before retrying. If Tailscale needs
authentication, `/status` returns `backend_state` and `auth_url` when available.
Initial Tailscale enrollment is still performed with the CLI. Disconnecting or
changing routing can interrupt access to the portal through Tailscale.

The WebUI shows live connection status and manages OAuth credentials through
GraphQL. Other dashboard panels still show clearly labeled sample data; their
live integration is a separate step.

## API

Requests with JSON bodies require `Content-Type: application/json`. Protected routes
require `Authorization: Bearer <token>`. Query-string tokens are not accepted.
Login and JWT middleware errors use `{"message":"..."}`; GraphQL responses use
`data` and `errors` as described below.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/health` | Public portal liveness; does not indicate Tailscale health |
| POST | `/api/login` | Public login with `username` and `password`; returns a JWT in `token` |
| POST | `/api/v1/query` | Authenticated GraphQL for password changes, Tailscale status, and OAuth credentials |
| GET | `/api/system` | Hostname, OS, architecture, portal uptime and build metadata |
| GET | `/api/tailscale/status` | State, self, peers, health messages and traffic counters |
| GET | `/api/tailscale/peers` | Sorted peer array |
| GET | `/api/tailscale/config` | Current supported preferences |
| PATCH | `/api/tailscale/config` | Change only submitted preferences; returns 204 |
| POST | `/api/tailscale/up` | Connect an enrolled node; returns 204 |
| POST | `/api/tailscale/down` | Disconnect the node; returns 204 |

Login returns `{"token":"<JWT>"}`. JWTs expire after seven days. The current
stateless JWT implementation does not revoke existing tokens after a password
change; they remain valid until expiry.

```sh
curl -X POST http://localhost:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'
```

To change the password, send the following JSON to `POST /api/v1/query`,
with `Content-Type: application/json` and `Authorization: Bearer <token>` from
login. The account is selected exclusively from the JWT's user ID; do not supply
a username or user ID in the request. The current password must match, and new
passwords must be 8–72 bytes (bcrypt's limit, including UTF-8 bytes).

```json
{
  "operationName": "ChangePassword",
  "query": "mutation ChangePassword($passwords: ChangePassword!) { changePassword(passwords: $passwords) }",
  "variables": {
    "passwords": {
      "oldpassword": "admin",
      "newpassword": "choose-a-new-password"
    }
  }
}
```

Success returns `{"data":{"changePassword":true}}`. Clients must check the
GraphQL `errors` array even on HTTP 200 and require a true result before reporting
success. An incorrect current password returns a GraphQL error; it does not
invalidate the current browser session. JWT middleware rejects invalid tokens
with HTTP 401. Subsequent logins must use the new password. Concurrent valid
password changes are last-write-wins; the update matches the user ID only.
Internal failures are logged on the server and exposed only as
`Internal Server Error`; expected authentication and validation errors remain readable.

### Tailscale credential setup

The WebUI checks the device's `tailscaleStatus` GraphQL query and offers an OAuth
credential dialog when Tailscale reports `NeedsLogin`. Settings lets portal
administrators save, replace, or remove the device-wide client ID and secret.
The setup guide at `/#/tailscale-setup` includes a link to the Tailscale Trust
credentials console and explains the OAuth client creation process.

The `tailscaleClient` query returns safe metadata (`clientId`, `hasClientSecret`,
and timestamps), or `null` before setup. `setTailscaleCredential` accepts
`{clientId, clientSecret}`; omit the secret to retain it for the same ID.
`clearTailscaleCredential` removes the local credentials. Secrets and cached
tokens are never returned by GraphQL. Saving does not validate the credentials,
connect the device, or switch tailnets; removing does not revoke the remote client.

Apply the new migration with `./nanotail-portal db migrate` before using this
feature. Credentials are stored unencrypted in the device's SQLite database;
restrict access to that file and its backups and use trusted HTTPS for the WebUI.

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

Create a release for nanotail (Linux/ARM64) with:

```sh
./build.sh
```

The script installs frontend dependencies, builds the React WebUI, and embeds it
in a static Linux/ARM64 binary at `./nanotail-portal`. Version metadata is filled
automatically from Git and the build time. Go, Git, and Node/npm are required.
There are no arguments or target overrides.

For standalone development checks:

```sh
go test -race ./...
go vet ./...
```

Tests use isolated credential files and fake Tailscale runners, and exercise
authentication, revocation, persistence, validation, API protection, static asset
serving, startup failures and graceful shutdown. They never change the host's
Tailscale configuration. A live device test is still needed for device integration.
