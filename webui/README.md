# Nanotail Portal WebUI

React and TypeScript frontend for managing Tailscale on nanotail.

## Development

Start the Go backend on its default port `7080`, then run:

```sh
npm install
npm run dev
```

Vite proxies requests under `/api` to `http://127.0.0.1:7080`.

## Build

```sh
npm run build
```

The production assets are written to `dist/`. Client routes use URL hashes
(for example, `/#/login`) so refreshing a page works with Go's static file server.

Run the API contract tests with `npm test` (Node 22.6+ is required for TypeScript
type stripping). No additional test dependencies are needed.

## Authentication flow

The login page submits `username` and `password` to `POST /api/login`.
The initial account is `admin` / `admin`. The returned `token` is stored in
browser local storage. Its JWT expiry is checked when restoring a session and
while the page is open. This client-side check only controls navigation; the
backend verifies the signature on protected requests. JWTs currently last seven
days. There is no `/me` or `/logout` API, so sign-out only clears the local token.

The browser storage key is `nanotail_access_token`. After upgrading from the old
project name, sign in again; existing account passwords are unchanged.

Open **Settings** in the sidebar, then use **Change password** to update the
signed-in account. The form sends the GraphQL mutation
`changePassword(passwords: ChangePassword!)` to `POST /api/v1/query`, with
`Authorization: Bearer <token>`. The `passwords` variable contains `oldpassword`
and `newpassword`; passwords are never interpolated into the GraphQL document.
The form validates the new password's 8–72-byte UTF-8 limit and confirmation.
Only `data.changePassword: true` without GraphQL errors confirms success.
GraphQL errors are displayed even on HTTP 200. An incorrect current password
does not sign the user out; an HTTP 401 from JWT middleware does.
Successful changes keep the current session because existing JWTs are not revoked.

## Tailscale credentials

The dashboard reads `tailscaleStatus` through GraphQL every 30 seconds. A device
in `NeedsLogin` prompts once per page session for its OAuth client ID and
secret. Stopped devices, pending machine approval, and unavailable status do not
trigger credential prompts. Reloading the page may show the prompt again;
ordinary status refreshes do not. Settings and the setup guide remain available.

Connection state comes from `backendState` and `self.online`; `currentTailnet`
and `self` can be `null` before login. The overview uses `tailscaleIPs` and the
live `peers` list, including device names, addresses, operating systems, and
online state. Unavailable status hides stale connection and peer information.

Settings supports saving, replacing, and removing credentials using
`setTailscaleCredential` and `clearTailscaleCredential`. These operations require
a portal administrator. The `tailscaleClient` query returns `null` before setup,
or the client ID, `hasClientSecret`, and timestamp; it never returns the secret.
Leave the secret blank to retain it for the same client ID. Changing IDs requires
a matching new secret. Removing credentials only removes the local copy.

Saving does not validate credentials, enroll the device, or switch tailnets.
Secrets are held only in form memory until submission/unmount, never in browser
storage. They are stored in the device's SQLite database without at-rest
encryption: protect the database/backups and serve the portal over trusted HTTPS.

The guide at `/#/tailscale-setup` links to the official
[Trust credentials console](https://console.tailscale.com/admin/settings/trust-credentials)
and [OAuth client documentation](https://tailscale.com/docs/features/oauth-clients).
It explains the `auth_keys` permission and device tags for future enrollment.

Apply the new database migration with `./nanotail-portal db migrate` before
using credential settings (run `db init` first on a new installation).
Connection status, peers, and device status are live; the routing, security,
and activity panels remain previews.

## Device status

The overview requests `deviceStatus` separately from Tailscale, every 30 seconds
and when **Refresh status** is clicked. It shows the hostname, one eth0 IPv4 and
IPv6 address with CIDR prefixes (`lanIP`/`lanIPv6`), their configuration modes
(`lanIPType`/`lanIPv6Type`), gateways (`gateway`/`gateway6`), DNS servers (`dns`),
eth0 MAC address, CPU and memory usage, uptime, and last restart in the browser's
local timezone. Missing addresses/gateways and empty DNS lists are explicitly
indicated. IPv6 `auto` is labeled **Automatic (SLAAC / DHCPv6)** rather than DHCP-only.
These network settings come from the backend's read-only NetworkManager queries;
`nmcli` must be installed on the device. Health is labeled as a placeholder
because the backend currently always returns `healthy`.

Loading and error states replace sample values. Failed refreshes clear stale
measurements and offer **Retry device status**, without hiding working Tailscale
information. HTTP 401 signs out; other errors leave the session usable. Pending
requests are canceled on refresh/unmount.

## LAN settings

**Settings → LAN IPv4 settings** configures eth0 through `setDeviceIP` using
authenticated GraphQL variables. Choose DHCP or static IPv4 with a CIDR prefix,
optional same-subnet gateway, and comma/space-separated IPv4 DNS servers.
IPv6 configuration is kept. Empty gateway/DNS fields clear their IPv4 settings;
DHCP clears manual IPv4 values and obtains them automatically. Only portal
administrators can apply changes, and the backend process needs NetworkManager
permission to modify and reapply the active eth0 profile.

The form loads current device values without overwriting edits on background
refresh. A connection-warning checkbox is required for each apply. The backend
persists settings immediately and attempts recovery if applying fails, but
there is no timed rollback when a successful change makes the device unreachable.
Keep local access available and choose an unused IP. A lost response is not
reported as success and is never retried automatically. Check the new static
address (a reconnect link is provided) or the router's DHCP client list before
retrying. Reconnect links do not transfer the login token. LAN mutation requests
time out after 60 seconds; the backend can finish applying or recovering even
if the browser disconnects.
