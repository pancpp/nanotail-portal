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

Before starting HTTP services or traffic collection, the portal initializes
missing database metadata and applies pending migrations automatically. Restart
the updated binary with the same configured database to upgrade it. Startup
stops on a migration failure, leaving that migration pending for retry after
the cause is fixed. Existing accounts, credentials, and traffic history are
preserved. Concurrent migration attempts are rejected; the process lock is
released automatically on exit. The old `db` subcommands are no longer supported.

### Factory reset

**Settings → Factory reset** is restricted to portal administrators. First
acknowledge the data-loss warning and access without Tailscale, then type `RESET` and enter
the current portal password to confirm. Closing either confirmation makes no
changes. The authenticated `POST /api/v1/factory-reset` endpoint requires both
confirmation fields and verifies the administrator's password on the server.

The portal does not monitor the **RCRY (Recovery)** button or use it to trigger
a reset. On NanoPi Zero2, FriendlyELEC's
[bootloader handles this button before Linux starts](https://github.com/friendlyarm/uboot-rockchip/blob/c5c053fa55/arch/arm/mach-rockchip/boot_rkimg.c#L299)
and can select recovery or USB download mode when held during power-on or reboot.
This firmware behavior is independent of the WebUI factory reset. On an OverlayFS
installation, recovery can boot the base system without the saved user-data
layer, making installed software and settings appear missing.

WebUI factory reset has no dedicated LED indication. The normal VPN traffic
LED worker follows the portal's usual shutdown and startup behavior; reset does
not override brightness or force an LED pattern.

Reset diagnostics use the normal configured logger. To follow
the default log file on the device:

```sh
sudo tail -F /srv/nanotail-portal/data/logs/nanotail-portal.log
```

The `[factory-reset]` messages report reset stages. With
`enable_console_log: true`, the normal logger also writes to the service journal,
which can be followed with `sudo journalctl -u nanotail-portal.service -b -f`
when using the supplied service unit.
Factory reset clears the portal's file logs and restores default configuration.

A reset clears `nanotail-portal.yml`, `nanotail-portal.sqlite3` (including SQLite journal/WAL
sidecars), and all contents of `logs`; deletes `nanotail-portal.key`; runs `tailscale logout`; clears the
initiating browser's saved JWT; and restarts the portal. The reset deliberately
supports only these default paths in the data directory. Custom storage paths,
symlinked targets, and hard-linked files are refused instead of risking unrelated
data. No automatic backup is made.

The portal acknowledges acceptance before disconnecting, drains active HTTP
requests, stops traffic recording, and verifies Tailscale logout **before**
clearing any files. If logout fails, local data is preserved and the portal
resumes with its existing settings. On success it closes SQLite and logging,
records reset intent, and replaces its own process (also works without systemd).
The new process clears the files before initialization. An interrupted cleanup
is resumed at the next startup; a cleanup error prevents serving partial state.
Only one portal process may use a data directory at a time.

The WebUI signs out on acceptance or an uncertain/disconnected response and
never automatically retries this destructive operation. Acceptance does not
guarantee completion: reopen your usual portal address over a connection that
does not depend on Tailscale and verify the outcome before retrying.
If the portal does not return, inspect its local service/journal output.

After reset, startup recreates the database and the `admin / admin` account.
Change that password immediately. Users continue to access the WebUI through
nginx at their usual portal address; factory reset does not change nginx
configuration. The portal is temporarily unavailable during restart, and
Tailscale access is lost, so have a connection that does not depend on Tailscale.

OS/LAN configuration, remote OAuth clients and authorizations, backups, and
Tailscale routing preferences are not additionally reset or revoked. Deleting
`nanotail-portal.key` causes startup to generate a new signing key, so all pre-reset
JWTs—including tokens saved in other browsers—are rejected after restart.

## Application layout and configuration

The installed layout separates versioned executables from persistent data:

```text
/srv/nanotail-portal/
├── releases/
│   ├── v0.1.0/nanotail-portal
│   └── v0.2.0/nanotail-portal
├── current  -> releases/v0.2.0
├── previous -> releases/v0.1.0
└── data/
    ├── nanotail-portal.yml
    ├── nanotail-portal.sqlite3
    ├── nanotail-portal.key
    └── logs/
```

The supplied `nanotail-portal.service` starts
`/srv/nanotail-portal/current/nanotail-portal` with its working directory set to
`/srv/nanotail-portal/data`. Application storage is resolved explicitly from the
data directory, independently of the executable location and launch directory.
The instance lock, reset recovery marker, and SQLite sidecars also live in `data/`.
Factory reset only clears its allowed runtime files there; it preserves releases
and the `current`/`previous` links.

The data directory is selected by `--data-dir`, then `NANOTAIL_DATA_DIR`, then the
default `/srv/nanotail-portal/data`. It is a startup option, not a YAML setting.
Relative overrides are resolved against the launch directory once at startup.
The portal creates a missing data directory with mode `0700`; existing directory
permissions are preserved. The service's working directory must exist before
systemd starts it.

Configuration defaults to `nanotail-portal.yml` inside the data directory.
`-c`/`--config` selects a different file; relative configuration paths are also
resolved inside the data directory. A missing file is created with mode `0600`;
an empty file uses the built-in defaults. See
[`nanotail-portal.example.yml`](nanotail-portal.example.yml) for the supported defaults.
`NANOTAIL_TAILSCALE_BINARY` overrides `tailscale_binary`, including after reset;
integration tests use it to keep all VPN commands on a disposable fake binary.

The default database is `data/nanotail-portal.sqlite3`; logs are written to
`data/logs/nanotail-portal.log`, with rotation at 10 MB and three backups.
Relative `database` and `log_dir` values are resolved inside the data directory;
absolute custom storage paths remain supported but disable WebUI factory reset.
The signing key always lives in the selected data directory.

Existing installations must be migrated explicitly while the portal is stopped:
back up their data, copy the configuration, database (including any sidecars),
signing key, and logs from `/srv/nanotail` into `/srv/nanotail-portal/data`, preserving
ownership and permissions, install the executable in a versioned release directory,
create `current`, and update the installed service paths. Preserve the signing key
to retain existing sessions. Older `nanotail.yml`, `nanotail.sqlite3`, and
`nanotail.key` files must also be renamed to their `nanotail-portal.*` equivalents.
This source change does not perform migration or manage release switching.
To keep using an existing data directory temporarily, pass
`--data-dir /srv/nanotail` explicitly.

Use `go run . --version` to print build metadata without starting services.

For frontend development, start the backend and run `npm run dev` in `webui/`.
Vite forwards `/api` to `127.0.0.1:7080`. Alternatively, build the frontend once:

```sh
npm --prefix webui ci
npm --prefix webui run build
go run -ldflags='-X github.com/pancpp/nanotail-portal/conf.gUseEmbeddedWebUI=true' . --data-dir ./data
```

Open `http://localhost:7080` to use the built UI. This command embeds the current
`webui/dist` build and stores local runtime files in `./data`. Rebuild/restart the
backend to embed a newer frontend build. For development with Vite, the separate
frontend server proxies API requests to port 7080.

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
this device’s exit-node and subnet-route advertisements. **Network** contains LAN IPv4 settings and
the tailnet connection control; **Access control** contains exit-node
and subnet-route advertisements. **Settings** contains OAuth client credentials, Change password, and
the administrator-only, double-confirmed Factory reset.

### Tailnet connection

**Tailscale status → Configure** opens **Network → Tailnet connection**.
The authenticated `tailscaleConnection` query returns this device's saved
`enabled` preference, `backendState`, and whether an existing login can be
resumed (`canEnable`). Enabled is not a claim that the daemon is connected.

Administrators can use `setTailscaleEnabled(enabled: true/false)` to resume or
pause the device. The backend validates fresh preferences/status, serializes
the change with routing writes, executes bare `tailscale up`/`tailscale down`,
and checks the saved preference afterward. It does not reset DNS, routing, or
login settings; see the [Tailscale CLI reference](https://tailscale.com/docs/reference/tailscale-cli#down).
Initial sign-in and expired-key reauthentication are available through the
Overview's browser sign-in guide. Device approval may still be required in the
tailnet admin console; OAuth route approval does not authorize or enroll devices.

The form requires acknowledgment that connectivity may be interrupted. Keep
LAN or console access available to re-enable Tailscale after disconnecting.
Command/request timeouts do not imply rollback. Unknown outcomes require a
fresh read before another attempt; mutations are never retried automatically.

Network has no standalone Tailscale logout button. **Settings → Factory reset**
logs out of Tailscale as part of the full reset, which also erases portal data
and restarts the portal. See [Factory reset](#factory-reset) for the confirmation
steps and data-loss warning.

### Exit node and subnet router

Overview shows separate **Exit node** and **Subnet routes** cards; **Configure**
opens **Access control**, where **Exit node** and **Subnet routes** have separate
cards. This device provides
internet/LAN access to other tailnet devices, rather than selecting another exit
node for its own traffic. **Exit-node advertising is always enabled**; there is
no switch to disable it. The panel shows the actual saved advertisement separately
from this fixed role.

The portal checks at startup and every 10 seconds. Once Tailscale is signed in,
it enables both exit-node default routes and clears any selected upstream exit
node. Each check reads current preferences first and writes only when needed;
existing subnet routes, DNS, SNAT, and other preferences remain untouched. Missing
Tailscale, pending sign-in/approval, or daemon errors do not prevent the portal
from starting. A later check retries after recovery; persistent failures are
logged. This never signs in, resumes a paused connection, or configures the OS.

**Subnet advertising is enabled by default.** On initial setup, the background
routing check advertises the live, masked IPv4/IPv6 prefixes on `eth0`, the LAN
interface managed by this portal. No browser visit or Save click is required.
For example, `192.168.42.8/24` becomes `192.168.42.0/24`. Duplicate prefixes,
link-local, loopback, and Tailscale addresses are excluded, and the same subnet
validation used for manual changes also applies to detected defaults.

Initial setup waits until Tailscale is signed in and running and usable LAN
addresses are available, then retries on the next check if necessary. The UI
checks **Advertise subnet routes** by default and displays **Pending** until the
advertisement has actually been saved. Missing LAN information shows a warning
and allows manual entry or an explicit opt-out, including while disconnected.

Existing advertised routes are adopted without replacement. A small SQLite
initialization marker remembers successful setup or an explicit user choice,
including disabling subnet advertising. Consequently, restarts and later LAN
changes never re-enable a saved opt-out or replace custom routes. Before any
explicit route command, the portal saves this marker; if that fails it sends no
command. An uncertain manual save is not retried automatically and also suppresses
initial defaults, so they cannot overwrite the user's choice. Default setup
failures retry only after reading fresh Tailscale preferences.

**Use local LAN** explicitly replaces the editable draft. Opening the page itself
does not send routing writes; initialization runs independently in the backend.
Factory reset clears the marker with the database. After signing in again, LAN
defaults are applied if there are no existing advertised subnet routes.

Routes accept up to 64 unique, canonical IPv4/IPv6 subnet CIDRs, one per line or
comma-separated. Host bits, default routes, and reserved/Tailscale ranges are
rejected. Default routes are managed by the mandatory exit-node role. Turning
subnet routing off clears only its advertisements; exit-node advertising remains
enabled. LAN address changes do not silently rewrite existing advertisements;
review and apply the new LAN prefixes afterward.

The administrator-only `setRouting(input: {subnetRoutes:
["192.168.42.0/24"]})` mutation replaces the old `setExitNode`
mutation. It uses one serialized `tailscale set` command with
`--advertise-exit-node=true` and `--advertise-routes`, clears legacy exit-node use
with `--exit-node=` and `--exit-node-allow-lan-access=false`, and verifies saved
preferences afterward. There is no exit-node boolean in the mutation input.
The UI warns before applying subnet changes and requires an
access/connectivity acknowledgement. DNS, route acceptance, SNAT, firewall,
credentials, and other Tailscale preferences are preserved. Settings persist in
Tailscale; only the initialization marker lives in SQLite. A stopped/logged-out daemon cannot start new
subnet advertisements from this form, but subnet routes can still be cleared.
The exit-node preference remains enabled without reconnecting the daemon.

**IP forwarding is an OS prerequisite, never configured by the portal.** The
read-only readiness display checks IPv4 and IPv6 separately; unreadable values
are unknown, not disabled. For manual Linux setup, persist these settings in
`/etc/sysctl.d/99-nanotail-forwarding.conf` and load them with
`sudo sysctl -p /etc/sysctl.d/99-nanotail-forwarding.conf`:

```ini
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1
```

If the uplink relies on IPv6 router advertisements, also configure
`net.ipv6.conf.<uplink>.accept_ra = 2` as appropriate; enabling forwarding
otherwise disables their acceptance. See the
[Linux IP sysctl documentation](https://docs.kernel.org/networking/ip-sysctl.html).
Firewall/return-route prerequisites remain the administrator's responsibility.
The UI surfaces Tailscale health warnings and warns if existing subnet SNAT is
disabled; it does not rewrite those settings.

**Advertised** means saved local intent, not approved, active, or reachable.
With saved OAuth credentials granting `devices:routes` write permission, the
portal automatically approves this device's exit node and advertised subnets.
**Tailnet approval** in Access control and the Overview cards distinguish
pending, confirmed, and failed OAuth approval. Without credentials, approve
routes in the Tailscale admin console or configure auto-approvers.
Allow the traffic in tailnet access rules, and select
the exit node or accept subnet routes on client devices. See the official
[exit-node guide](https://tailscale.com/docs/features/exit-nodes) and
[subnet-router guide](https://tailscale.com/docs/features/subnet-routers).
Route approval does not change tailnet policy, device authorization, or other clients.
Enabling **Peer relay** separately adds a `tailscale.com/cap/relay` grant from
`["*"]` to this device's Tailscale IP in the admin console policy. This requires
saved OAuth credentials with `policy_file` write permission and its required
`devices:posture_attributes` and `devices:core:read` dependencies. Existing policy
rules and comments are preserved, matching grants are reused, and concurrent
policy edits are protected with ETag checks. The grant is confirmed before the
local listener is enabled. Disabling the relay leaves grants intact. See
[peer relay behavior](webui/README.md#peer-relay) for details.

Approval runs in the background after the startup/10-second advertisement check,
including after saving credentials, applying routes, and recovering sign-in.
It requests an OAuth token scoped to `devices:routes`, addresses only the local
daemon's stable self ID, waits for the control plane to report the advertisements,
and verifies approval with a fresh read. Tokens stay in memory and are refreshed
before expiry; HTTP redirects and upstream diagnostic bodies are not exposed.
Failures retry after a minute, always reading existing approvals before writing.
Successful approvals are rechecked every five minutes. New credentials, node
identity, or advertisements trigger an earlier check. Cloud failures do not undo
local routing changes or prevent the portal from starting.

Approval is additive: existing approvals (including pre-approved routes) are
preserved. Disabling a subnet withdraws its local advertisement; it does not
revoke its cloud approval. Removing credentials stops automatic approval and
clears cached tokens, without revoking existing approvals. An administrator's
manual revocation of an actively advertised route can be re-approved at the
next check while OAuth credentials remain configured.

Keep local access available: changing roles or removing advertisements can
disconnect this browser, SSH, and other clients. Unknown outcomes require a fresh
read before retrying; browser subnet mutations are never retried or rolled back
automatically. The background routing check also reads fresh state before any
retry, so a lost response does not blindly repeat a successful write.
Factory reset continues to leave OS forwarding/firewall configuration unchanged.

## API

Requests with JSON bodies require `Content-Type: application/json`. Protected routes
require `Authorization: Bearer <token>`. Query-string tokens are not accepted.
Login and JWT middleware errors use `{"message":"..."}`; GraphQL responses use
`data` and `errors` as described below.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/health` | Public portal liveness; does not indicate Tailscale health |
| POST | `/api/login` | Public login with `username` and `password`; returns a JWT in `token` |
| POST | `/api/v1/query` | Authenticated GraphQL for password changes, device/Tailscale status, VPN traffic, tailnet connection, LAN/routing settings, and OAuth credentials |
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

The JWT signing key is stored in `nanotail-portal.key` in the selected data directory.
Startup reads an existing nonempty key (ignoring surrounding whitespace).
If the file is missing, empty, or whitespace-only, it generates a cryptographically
random 256-bit key, stores its hexadecimal representation atomically with
owner-only permissions (`0600`), and uses that key for signing and verification.
Read/write errors, unsafe file types, and files larger than 4096 bytes stop startup;
there is no built-in fallback key. The key is never returned by the API or logged,
and key files are ignored by Git. Protect this file and any copies of it.

Normal restarts retain the same key and existing sessions. Factory reset deletes
the key before initialization so the new key invalidates every previous JWT.
The first upgrade from the old built-in signing key also requires signing in
again; account passwords are unchanged.

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
expiration for an existing node key is shown as **Expiry disabled**; no fixed lifetime or progress
percentage is assumed. Loading, absent keys, and failed status requests do not
display fabricated or stale expiry values. See [Tailscale key expiry](https://tailscale.com/docs/features/access-control/key-expiry).

When Tailscale reports `NeedsLogin`, entering **Overview** automatically opens a
browser sign-in guide. This works for first-time setup and expired logins, with
no OAuth client credentials required. Confirm the instructions, choose
**Prepare sign-in**, then **Sign in to Tailscale** and authorize the device in
the new tab. Opening the popup never starts authentication on its own. Use the
top-right X to close the sign-in panel; it has no **Check status** or **Close**
buttons. Reopening it checks status automatically. Closing
suppresses the prompt until the next Overview visit; **Sign in to Tailscale**
also reopens it manually. Paused/offline devices and status errors do not prompt.
First-time login shows **Signed in to Tailscale successfully** only after
Tailscale reports a usable node key and `Running` state (`SIGNED_IN`); this does
not claim that a previous key was rotated. The overview then refreshes.

**KEY EXPIRY → Renew** opens the **KEY RENEW** dialog for a portal administrator to force reauthentication,
including for an expired key. **Renew is disabled when node-key expiry is
disabled**, including for already-prepared requests. The backend checks again
before starting renewal. First-time sign-in remains available when the device
has no node key. Open the portal
over the LAN first: renewal can disconnect Tailscale. Confirm the warning to
prepare a request, then choose **Sign in to Tailscale** to start reauthentication.
Until Sign in is clicked, the top-right X button or Escape cancels the prepared
backend request without changing Tailscale. The dialog waits for confirmed
cancellation before closing. Authenticate with the **same account and tailnet**. Signing
in turns the tailnet connection on, even if it was stopped. Device approval may
also be required. The dialog checks progress and refreshes the overview only
after a new, unexpired (or non-expiring) key is reported with `Running` state.
The panel and dialog show a rotating **Waiting for renewal…** indicator while
pending, followed by a green check mark and **Node key renewed successfully**.
After Sign in, completion checks continue when the dialog is closed or another WebUI tab is
selected. Errors show **Renewal not confirmed** instead of a success indicator.

`renewTailscaleNodeKey` only prepares an in-memory first-login/renewal request and returns its
`attemptID`. `cancelTailscaleNodeKeyRenewal(attemptID)` cancels that request
before sign-in, even if tailscaled is unavailable. IDs prevent stale requests
from modifying another attempt. `beginTailscaleNodeKeyRenewal(attemptID)` starts
Tailscale's `StartLoginInteractive` operation via
`tailscale debug localapi POST /localapi/v0/login-interactive`; the installed CLI
must support `debug localapi`, and the portal needs permission to write to the
tailscaled socket. It does not reset routing preferences, log out, or enroll
using saved OAuth credentials. `tailscaleKeyRenewal` is a read-only admin query
for progress and the sign-in URL. URLs are validated for Tailscale-hosted login,
are not stored in the database/browser storage, and are hidden from non-admins
in the general status query too. Custom control-server sign-in URLs are not
supported by this dialog.

If a request is interrupted, reconnect over the LAN, close the dialog with X,
and reopen **Renew** to check status before retrying; writes are never automatically retried.
The renewal dialog has no **Check status** or **Close** buttons. Once Sign in starts, closing
only dismisses the dialog: Tailscale has no dedicated cancel-login API, so the
portal does not log out or disconnect the device to imitate cancellation.
Reopen **Renew** to recover a pending link. Prepared requests and the comparison
baseline are in memory. After a portal restart, prepare and choose Sign in again
to resume an existing login link; without the old baseline the portal cannot
certify that a prior renewal completed. Review the current expiry in the overview.

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

#### VPN activity LED

On **NanoPi Zero2**, the portal automatically uses **LED1** for combined VPN
receive/transmit activity. The separate SYS LED is left untouched. Board detection
uses the device-tree compatible string, not just the presence of an LED name.
The adapter supports the vendor kernel's `user_led` and upstream kernel's
`green:status` sysfs layouts, corresponding to GPIO4_PB1 in the
[vendor device tree](https://github.com/friendlyarm/kernel-rockchip/blob/nanopi6-v6.1.y/arch/arm64/boot/dts/rockchip/rk3528-nanopi-rev01.dts)
and [upstream device tree](https://github.com/torvalds/linux/blob/master/arch/arm64/boot/dts/rockchip/rk3528-nanopi-zero2.dts).

A backend worker samples the same `tailscale0` counters every 250 ms, independently
of browsers and the history recorder. More traffic produces faster 50 ms flashes:

| Combined RX + TX rate | Flashes per second |
| --- | --- |
| Idle or unavailable | Off |
| Greater than zero, below 16 KiB/s | 1 |
| 16 KiB/s to below 128 KiB/s | 2 |
| 128 KiB/s to below 1 MiB/s | 4 |
| 1 MiB/s to below 8 MiB/s | 8 |
| 8 MiB/s or more | 10 |

These are approximate visual throughput tiers, not per-packet flashes or a
connection-status indicator. Missing interfaces and counter resets clear the
baseline; blinking resumes after two valid samples. Userspace-networking mode
without `tailscale0` has no activity indication.

Unsupported boards and missing LEDs are a no-op. The service user needs write
access to LED1's sysfs attributes; permission/LED failures disable only this
worker and are logged, without interrupting the portal or VPN. Restart the portal
after correcting a hardware/permission problem. No packages, GPIO exports, or
persistent OS LED settings are changed.

The worker temporarily takes over the LED and restores its brightness, trigger,
and supported trigger parameters on graceful shutdown, including factory reset.
The standard `none`, `default-on`, `timer`, and `heartbeat` triggers are supported;
other active triggers are left unchanged to avoid disrupting another service.
See the [Linux LED interface documentation](https://docs.kernel.org/leds/leds-class.html).
Avoid another service controlling LED1 concurrently. Forced termination or power
loss cannot run the restoration step.

Enabled by default; set `vpn_traffic_led: false` in `nanotail-portal.yml` or
`NANOTAIL_VPN_TRAFFIC_LED=false` in the service environment to opt out.
For new hardware, implement `activityled.Platform` and `activityled.LED` and add
an exact board match to detection. Platform adapters select the indicator and
handle its interface; traffic sampling and blink timing stay hardware-independent.

#### Persistent 24-hour history and total traffic

The history and totals tables are initialized or upgraded automatically before
the application starts. Use the same configuration/database as the existing
installation. **Network activity → Last 24 hours** reads the authenticated
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

### Device hostname

In **Network → Device hostname**, administrators can save a new system hostname.
The form loads the current hostname, preserves unsaved edits during refreshes,
and refreshes device status after a confirmed save. English and Simplified Chinese
are supported.

The authenticated `setDeviceHostname(hostname: String!)` mutation accepts a single
DNS label of 1–63 ASCII letters, digits, or hyphens, starting and ending with a
letter or digit. Names are trimmed and lowercased; `localhost` and `localhost6`
are reserved. For example:

```graphql
mutation { setDeviceHostname(hostname: "nanotail-office") }
```

The portal uses [NetworkManager's persistent hostname command](https://networkmanager.pages.freedesktop.org/NetworkManager/NetworkManager/nmcli.html)
(`nmcli general hostname …`) and verifies the saved value before reporting success.
The process needs permission to change the hostname without an interactive prompt.
Hostname and LAN writes share a lock; hostname commands have a 15-second deadline
and finish even if the browser disconnects. The UI never retries writes automatically.
This updates the system hostname without setting a Tailscale hostname override or
restarting network connections. If accessing the portal by name, use the new name
or the device's IP address; name resolution can take time to update.

### LAN IPv4 configuration

In **Network → LAN IPv4 settings**, administrators can choose DHCP or a static
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

### WebUI language

The login page and dashboard header offer **English** and **简体中文**.
On the first visit, the WebUI selects English or Simplified Chinese from the
browser's preferred languages; unsupported languages fall back to English.
The selected language is saved per browser/origin in `nanotail_language` and
survives sign-out and reloads. Switching languages does not reload the app,
discard form edits, or change device settings. If browser storage is blocked,
the selection still works for the current page.

Navigation, forms, dialogs, setup guides, known status/validation messages, and
dates/durations follow the selected language. Technical values (addresses,
credentials, commands, filenames, and the `RESET` confirmation phrase) are
unchanged. Unrecognized backend diagnostics retain their original text.

Translations live in `webui/src/locales/zh-CN.ts`, keyed by English source
messages. Use `useI18n().t` for text and `T` for rich messages with React-node
placeholders; do not translate user/device data or render translations as HTML.
Unit tests check catalog coverage and placeholder parity. The mocked browser
suite also checks both languages, persistence, mobile layouts, and form safety.

### Tailscale credential setup

The WebUI offers browser sign-in when Tailscale reports `NeedsLogin`.
After the portal observes a new device enrollment or a change of tailnet, it
opens **Add your client credentials** with the OAuth creation link and credential
form. **Skip for now**, X, or Escape first shows a reminder to approve the device
(when required), approve its exit-node and advertised subnet routes (unless
already approved by policy), and manually configure peer relay. Credentials can
also be added later in **Settings**. Saving or acknowledging the reminder
completes the prompt for that enrollment; ordinary refreshes do not reopen it.
OAuth credentials remain optional, separate settings: Settings lets portal
administrators save, replace, or remove the device-wide client ID and secret.
To remove them, choose **Remove credentials** beside **Save credentials**, then
**Confirm removal**. This clears the saved credentials and cached tokens on this
device without disconnecting Tailscale or revoking the OAuth client in Tailscale.
**Setup guides** at `/#/tailscale-setup` covers first-time sign-in and device
approval, subnet-route approval, exit-node approval, and OAuth client creation.
Sign-in and routing settings link directly to the relevant approval guide in a
new tab, preserving in-progress sign-in and routing edits. Device approval is
needed only when the tailnet requires it and is separate from route approval;
saved OAuth route credentials do not authorize a new device. The route guides
explain manual console approval and when OAuth or policy-based approval lets you
skip those steps. The OAuth guide includes the Tailscale Trust credentials link.

The `tailscaleClient` query returns safe metadata (`clientId`, `hasClientSecret`,
and timestamps), or `null` before setup. `setTailscaleCredential` accepts
`{clientId, clientSecret}`; omit the secret to retain it for the same ID.
`clearTailscaleCredential` removes the local credentials. Secrets and cached
tokens are never returned by GraphQL. Saving queues automatic approval of current
advertisements; the mutation confirms storage, not successful OAuth authentication
or approval. Check Tailnet approval for the result. Saving does not connect the
device or switch tailnets; removing does not revoke the remote client.

Create the OAuth client in the same tailnet with `devices:routes` write permission
(not just `auth_keys`). This scope can manage routes across that tailnet, although
the portal only targets its own device. Do not grant unrelated permissions.
See [Tailscale's scope reference](https://tailscale.com/docs/reference/trust-credentials)
and [OAuth client documentation](https://tailscale.com/docs/features/oauth-clients).

The credentials table is initialized or upgraded automatically at startup.
Credentials are stored unencrypted in the device's SQLite database;
restrict access to that file and its backups and use trusted HTTPS for the WebUI.

A configuration PATCH may include `hostname`, `accept_dns`, `accept_routes`,
`shields_up`, `exit_node`, `exit_node_allow_lan_access`, `advertise_routes`, and
`advertise_exit_node`. Omitted fields are preserved. To enforce the appliance's
role, `advertise_exit_node: false`, a nonempty `exit_node`, and
`exit_node_allow_lan_access: true` are rejected. Use `exit_node: ""` to stop using an exit node and
`advertise_routes: []` to clear subnet advertisements. Routes must be canonical
CIDRs such as `192.168.1.0/24`; use `advertise_exit_node` for default routes.
`exit_node_id` may report a legacy upstream selection until the automatic
exit-node check clears it.

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
in a static Linux/ARM64 binary at `build/releases/<version>/nanotail-portal`. Version metadata is filled
automatically from Git and the build time. Go, Git, and Node/npm are required.
The build output contains the executable, including its WebUI and database
migrations. It does not create runtime data or change installed release links.
`GOOS` and `GOARCH` can override the default Linux/ARM64 target.

For a local development build and run, use `./run.sh`. It builds for the host
architecture and starts with `--data-dir <checkout>/data`. Additional application
arguments are forwarded, for example `./run.sh --data-dir /tmp/portal-data`.
Both `build/` and `data/` are ignored by Git.

Routing UI checks:

```sh
npm --prefix webui test
npm --prefix webui run build
npm --prefix webui run test:routing-browser
```

The browser check uses headless Chrome with a disposable profile and mocked API
responses, covering desktop/mobile layout and the actual form workflow. Set
`CHROME_BIN` if Chrome is not at its default Linux path. It does not start the
portal backend or change Tailscale/OS settings.

For standalone development checks:

```sh
go test -race -ldflags='-X github.com/pancpp/nanotail-portal/conf.gUseEmbeddedWebUI=true' ./...
go vet ./...
```

Tests use isolated credential files and fake Tailscale runners, and exercise
authentication, revocation, persistence, validation, API protection, static asset
serving, startup failures and graceful shutdown. They never change the host's
Tailscale configuration. LED tests use fake sysfs files. Subprocess tests disable
the hardware LED through an environment setting that survives reset. A live
device test is still needed for device integration.
