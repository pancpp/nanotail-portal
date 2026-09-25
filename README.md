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

The WebUI shows live connection, peer, node-key, device, routing, and VPN traffic
data through GraphQL. It also manages OAuth credentials, LAN IPv4 settings, and
the exit node used by this device.

### Exit-node routing

On Overview, **Routing → Configure** opens an exit-node selection dialog. The
authenticated `tailscaleRouting` query reads saved preferences and approved
exit nodes visible to this device. Select an online exit node, choose whether to
allow local-LAN access, acknowledge the connectivity warning, and apply. Choose
**None — use local gateway** to clear a saved selection, including a missing or
offline exit node. If no exit nodes are available, follow the linked
[Tailscale setup guide](https://tailscale.com/docs/features/exit-nodes/how-to/setup)
to configure and approve one on another device.

`setExitNode(input: {exitNodeID: "<stable peer ID>", allowLANAccess: true})` is
administrator-only. Clearing uses an empty ID and `allowLANAccess: false`.
The backend validates the peer against fresh daemon status, changes only
`--exit-node` and `--exit-node-allow-lan-access` via `tailscale set`, and checks
the saved preferences before returning success. The portal process needs
permission to manage tailscaled (root or a configured Tailscale operator).
These settings persist in Tailscale, not in the portal database.

This configures **nanotail's use of another exit node**. It does not advertise
nanotail as an exit node, configure LAN-client forwarding/subnet routes, change
tailnet policy, or start a stopped Tailscale daemon. An advertising exit node
cannot simultaneously use another exit node. Changing routes can disconnect
the browser or SSH; successful changes are not automatically reverted. After a
timeout or lost connection, reconnect and reload settings before retrying.
Readback confirms preferences, not end-to-end internet reachability.

## API

Requests with JSON bodies require `Content-Type: application/json`. Protected routes
require `Authorization: Bearer <token>`. Query-string tokens are not accepted.
Login and JWT middleware errors use `{"message":"..."}`; GraphQL responses use
`data` and `errors` as described below.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/health` | Public portal liveness; does not indicate Tailscale health |
| POST | `/api/login` | Public login with `username` and `password`; returns a JWT in `token` |
| POST | `/api/v1/query` | Authenticated GraphQL for password changes, device/Tailscale status, VPN traffic, LAN/routing settings, and OAuth credentials |
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

### Node key expiry

The WebUI Node key card reads `tailscaleStatus.haveNodeKey` and
`tailscaleStatus.self.keyExpiry`, which are populated from `tailscale status
--json`. It calculates remaining time from the reported expiration timestamp,
shows the exact date in the browser's timezone, and warns when expired. A null
expiration is shown as **No expiry reported**; no fixed lifetime or progress
percentage is assumed. Loading, absent keys, and failed status requests do not
display fabricated or stale expiry values. See [Tailscale key expiry](https://tailscale.com/docs/features/access-control/key-expiry).

### Device status

The authenticated `deviceStatus` query reports Linux device metrics:

```graphql
query {
  deviceStatus {
    hostname
    lanIPType
    lanIP
    gateway
    dns
    lanIPv6Type
    lanIPv6
    gateway6
    ethAddr
    cpuload
    memory
    lastRestart
    uptime
    health
  }
}
```

`lanIP` and `lanIPv6` report one address per family assigned to `eth0`, with
CIDR prefixes (for example, `192.0.2.2/24` and `fd00::2/64`). Non-link-local
unicast addresses are preferred, followed by lexical CIDR order; link-local
addresses are a fallback. A missing address is an empty string. These are
representative interface addresses, not a destination-specific source-address
selection. `ethAddr` is the MAC address of `eth0`.

The backend requires NetworkManager and `nmcli` in its PATH. Read-only queries
of `eth0` supply `gateway`, `gateway6`, and `dns`; gateways have no prefix and
are empty strings when absent. DNS is the deduplicated list of servers for
this link, not `/etc/resolv.conf`'s local stub or Tailscale's resolver settings.
IPv6 gateways may be link-local, with `eth0` as the implied interface.

`lanIPType` and `lanIPv6Type` describe the active NetworkManager profile:
`manual` maps to `static`, IPv4 `auto` maps to `DHCP`, and IPv6 `dhcp` maps to
`DHCP`. IPv6 `auto` remains `auto` because it can use SLAAC and/or DHCPv6.
Other modes (`disabled`, `ignore`, `link-local`, `shared`) remain explicit;
an absent active profile or unrecognized method is `unknown`. See the
[NetworkManager CLI reference](https://networkmanager.dev/docs/api/latest/nmcli.html).
NetworkManager reads share a three-second timeout and respect request cancellation;
failures produce a sanitized error rather than fabricated network settings.

`cpuload` is CPU utilization across all cores sampled over 200 ms, rounded to
an integer percentage. `memory` is `(MemTotal - MemAvailable) / MemTotal * 100`,
also rounded. Boot time and uptime come from Linux `/proc` statistics;
`lastRestart` is the system boot time in UTC and `uptime` is whole seconds,
not the age of the portal process. See the [Linux proc documentation](https://docs.kernel.org/filesystems/proc.html).

`health` currently returns the fixed placeholder `"healthy"`; it does not
indicate that health checks have run. If `eth0`, NetworkManager, or required system metrics
cannot be read, the query returns a sanitized GraphQL error instead of zeroed
metrics. CPU sampling respects request cancellation. The WebUI's device panel
displays these fields and refreshes every 30 seconds or via **Refresh status**.
Restart time is displayed in the browser's local timezone. Device-query errors
are shown separately from Tailscale errors, with a retry button; failed updates
clear stale device measurements. The fixed health value is labeled as a placeholder.

### Live VPN network activity

The authenticated `networkActivity` GraphQL query returns `interfaceName`,
`rxBytes`, `txBytes`, `sampledAt`, and `counterEpoch`. The backend reads only
Linux `tailscale0` RX/TX byte counters; it does not sum LAN interfaces or invoke
Tailscale/NetworkManager commands for each sample. This measures VPN IP traffic,
including routed VPN traffic, not eth0 traffic or encrypted transport overhead.
See the [Linux interface statistics documentation](https://docs.kernel.org/networking/statistics.html).

Byte counters are decimal strings to preserve the full unsigned 64-bit values
in JavaScript. They are cumulative since the interface was created/reset, not
24-hour totals. The boot ID and interface index form `counterEpoch`; changing
either invalidates previous rate samples. A missing interface (including
userspace-networking mode) produces an explicit error, never fake zero traffic.

The overview samples approximately every two seconds and computes download/RX
and upload/TX bytes per second using counter differences and sample timestamps.
It displays a rolling one-minute chart collected while the overview is visible.
The live view's first sample establishes a baseline;
restarts, decreasing counters, clock changes, long gaps, and failed requests
reset the baseline to prevent misleading rates. Polling pauses in hidden tabs,
stops when leaving the overview/signing out, and times out requests after five
seconds. Errors clear stale data, retry automatically, and offer a retry button.

#### Persistent 24-hour history and total traffic

Apply the history and totals migrations before running this version:

```sh
./nanotail-portal db migrate
```

Use the same configuration/database as the running portal (`db init` first on
a new installation). **Network activity → Last 24 hours** reads the authenticated
`networkActivityHistory` query, which returns `windowStart`, `windowEnd`,
hourly `startedAt`, `rxBytes`, `txBytes`, and `observedSeconds` records, and a
`totals` snapshot. Both **Last 24 hours** and **Total traffic** are displayed
together above the Live/history selector, with download/upload breakdowns.

A single backend recorder samples `tailscale0` in memory once a minute, whether
or not any browser is open. At each UTC hour boundary it saves the completed
hour's RX/TX byte totals and measured duration to `network_activity_hours`.
In the same hourly transaction it updates `network_activity_totals` with the
last 24 completed hours and the all-time totals, and prunes older hourly rows.
Queries never save traffic data. The unfinished hour is excluded from both
saved totals. Byte totals remain exact decimal strings, including values larger
than 64-bit integers; retries cannot count the same hour twice.

**Total traffic** means all VPN traffic recorded by this portal, not the current
interface's lifetime counter. It survives hourly history cleanup and restarts.
On upgrade, it is initialized from existing retained hourly records; history
already deleted before the upgrade cannot be recovered. Each saved snapshot
includes its window end and earliest measured hour. The WebUI shows an explicit
“as of” time: while recording is stopped or saves fail, reads retain the last
saved 24-hour window instead of silently recalculating it.

Saved hours survive process/device restarts. The unfinished hour remains in
memory and may be lost on restart; there are no extra startup or shutdown writes.
Missing interfaces, resets, backwards clocks, and long sampling gaps do not
create spikes or invented zero traffic. Partial hours retain only measured bytes
and coverage, without extrapolation; missing hours are shown explicitly. Samples
straddling hour boundaries are prorated by elapsed time, preserving byte totals.
Hours are stored in UTC and labeled in the browser's local timezone.

Failed saves remain in bounded memory and retry at the next hourly boundary,
not on every sample; a restart can lose those pending records too. The WebUI
checks for new saved totals/history once a minute while the overview is visible. History
read failures (including missing migrations) do not disable live traffic rates.

### LAN IPv4 configuration

In **Settings → LAN IPv4 settings**, administrators can choose DHCP or a static
IPv4 address with CIDR prefix, optional gateway, and up to eight IPv4 DNS servers.
The form starts with current device values and keeps unsaved edits during status
refreshes. IPv6 settings are not changed. Device-status reads remain read-only.

The authenticated `setDeviceIP(deviceIP: DeviceIP)` mutation accepts, for example:

```graphql
mutation {
  setDeviceIP(deviceIP: {
    type: "static"
    ip: "192.168.1.20/24"
    gateway: "192.168.1.1"
    dns: ["192.168.1.1", "1.1.1.1"]
  })
}
```

For DHCP, send `{type: "DHCP", ip: "", gateway: "", dns: []}`. Static addresses
must use a /1–/32 prefix and cannot be subnet network/broadcast addresses (except
/31 point-to-point networks). A gateway, if supplied, must be a different usable
address in the same subnet. Empty gateway/DNS values clear those IPv4 settings.

The portal process needs NetworkManager permission to modify and reapply the
active `eth0` connection profile without an interactive authorization prompt.
It persists the IPv4 properties with `nmcli connection modify uuid …`, then
applies them with `nmcli device reapply eth0`; it does not bring the link down
or alter IPv6 properties. Manual secondary IPv4 addresses are replaced by the
single submitted address. Use this feature only on a device with an active,
NetworkManager-managed `eth0` profile.

The UI requires acknowledgment that changing networking can disconnect browser
and SSH sessions. Use an unused address on the correct subnet and retain local
access to recover from mistakes. The backend validates inputs, rejects concurrent
changes, and attempts to restore the saved IPv4 properties if applying fails.
Writes/recovery have bounded timeouts and continue if the browser disconnects.
**A successful apply is not a connectivity test and has no automatic timed
rollback.** Duplicate addresses and unreachable gateways are not detected.
If the response is lost, check the device before retrying. For static addresses,
the UI offers a link using the existing protocol/port; DHCP addresses can be
found in the router's client list. A changed origin requires signing in again,
and HTTPS needs a certificate valid for the new address.

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
