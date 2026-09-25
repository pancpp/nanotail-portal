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
days. There is no `/me` or `/logout` API, so ordinary sign-out only clears the local token.
The backend loads its signing key from `nanotail.key`, generating a new random
key if missing or empty. Normal restarts preserve sessions; factory reset
deletes this key so all old JWTs are rejected after restart.

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

### Factory reset

**Settings → Factory reset** shows two confirmations: acknowledge irreversible
data loss and access without Tailscale, then type `RESET` and enter the current administrator
password. Closing either step sends no reset request. The final action posts
`{ confirmed: true, confirmation: "RESET", password }` with bearer authentication
to `/api/v1/factory-reset`. Only HTTP 202 with `accepted: true` means accepted,
not completed. Duplicate clicks are blocked and requests are never retried.

Acceptance or an uncertain response removes `nanotail_access_token` and redirects
to login with recovery guidance. A definite rejection leaves the session usable
and clears the password/confirmation fields. The outcome message lives in the
authentication provider so the sign-out redirect does not erase it.

The backend logs out of Tailscale, clears the default `nanotail.yml`,
`nanotail.sqlite3` and `logs` targets, deletes `nanotail.key`, and restarts itself. If logout fails, it
preserves local data. Custom paths and unsafe filesystem targets are refused.
After a successful reset the account is `admin / admin`. Reopen the usual
nginx-served portal address and change the default password. The dialog warns
about temporary downtime and requires access that does not depend on Tailscale;
it does not direct users to the internal backend listener.
The new signing key revokes all existing JWTs, including those in other browsers.
LAN configuration reset and remote OAuth revocation are not part of this operation.

## Tailscale sign-in and credentials

The dashboard reads `tailscaleStatus` through GraphQL every 30 seconds. A device
in `NeedsLogin` automatically opens a browser sign-in guide on each Overview
visit, including new devices and expired logins. Dismissing the guide suppresses
it for that visit; ordinary refreshes do not reopen it. Returning to Overview
shows it again if sign-in is still needed. A **Sign in to Tailscale** button also
lets users reopen it manually. Paused/offline devices, pending machine approval,
and unavailable/loading status do not trigger the guide. Credential-query
errors do not block browser sign-in.

**KEY EXPIRY → Renew** also opens this sign-in guide when `haveNodeKey` is false,
regardless of the daemon's connection state. For an existing node key, Renew
opens the key-renewal panel instead. Unknown or failed status does not imply a
missing key.

Opening the guide only reads status. A portal administrator acknowledges the
instructions, chooses **Prepare sign-in**, and then **Sign in to Tailscale** to
authorize the device in a new browser tab. No OAuth client ID/secret is needed.
The existing cancellable prepare/start flow is shared with node-key renewal.
Use the top-right X to close the sign-in panel; it has no **Check status** or
**Close** buttons. After an error, reopen sign-in from Overview to check status
before retrying. Status is read on opening, and progress updates automatically
while sign-in is pending.
First-time login reports `SIGNED_IN` only after a usable node key and `Running`
state are reported, displaying **Signed in to Tailscale successfully**, refreshing
the overview and stopping polling. It does not claim an existing key was renewed.

Connection state comes from `backendState` and `self.online`; `currentTailnet`
and `self` can be `null` before login. The overview uses `tailscaleIPs` and the
live `peers` list, including device names, addresses, operating systems, and
online state. Unavailable status hides stale connection and peer information.

Access control supports saving, replacing, and removing credentials using
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

The backend initializes the database and applies pending migrations before
serving requests, including the credentials table on a new installation.
Connection status, peers, node-key expiry, device status, network activity,
and routing settings are live.

## Exit-node routing

**Overview → Routing → Configure** opens **Access control**, with the
current exit node, approved peer choices, and a local-LAN access checkbox.
Routing preferences refresh every 30 seconds and with **Refresh status**. The
form reads them again on entering the tab; background updates do not overwrite edits.
Offline nodes cannot be newly selected. A missing or offline current node can
still be cleared by choosing **None — use local gateway**. Read failures show
Unavailable rather than claiming traffic uses the local gateway.

The form sends the administrator-only GraphQL `setExitNode` mutation with a
stable peer ID and an explicit LAN-access boolean. LAN access defaults on when
selecting a node from the local-gateway mode. Changes require confirmation of
the connection warning. Only an error-free `true` result confirms saved
preferences; after an error, reload settings before retrying. Network failures
may mean the change already applied, and are never automatically retried.

This controls nanotail's own exit-node selection, not exit-node advertising or
forwarding other LAN devices' traffic. Tailscale persists the settings; the
portal database is not used. The form links to the official exit-node setup
guide when another device needs to be configured/approved first.

## Tailnet connection

**Overview → Tailscale status → Configure** opens **Network**, which also
contains LAN IPv4 settings. OAuth credentials are in **Access control**, below
exit-node configuration. **Settings** contains Change password and Factory reset. All sidebar tabs
support direct links and active navigation states.

**Network → Tailnet connection** reads `tailscaleConnection` on entry and with
**Reload connection**. On/Off reflects the saved `enabled` preference rather
than inferring it from online status; the daemon state is displayed separately.
The administrator-only `setTailscaleEnabled` mutation resumes or pauses an
already enrolled device without logging out or resetting routing preferences.
Unenrolled, expired, or unapproved devices must complete sign-in/approval first;
the stored OAuth secret is not used for automatic enrollment.

Changes require acknowledgment of the connectivity warning. Keep LAN or
console access available: after disabling Tailscale, reconnect over the LAN to
turn it on. There is no optimistic success state or automatic write retry.
Loading/read failures disable the control; rejected, timed-out, or disconnected
writes require reloading current settings before another attempt. The form
prevents duplicate submissions, and reads are canceled when leaving the tab.

The **Log out of Tailscale** button in the same panel uses the local-access
acknowledgement and sends the administrator-only `logoutTailscale` mutation.
It runs `tailscale logout`, disconnecting the device and requiring sign-in again
from Overview. Portal login and saved OAuth credentials are retained. Logout
clears pending renewal state and links, refreshes device status, and reloads the
connection panel. Only confirmed success displays the logged-out message;
uncertain outcomes require **Reload connection** before another attempt.

## Node key expiry

The Node key card uses `tailscaleStatus.haveNodeKey` and `self.keyExpiry` from
the existing GraphQL status query. The remaining days, hours, or minutes are
calculated from that timestamp; the exact expiration is displayed in the
browser's local timezone. Status is refreshed every 30 seconds; the countdown
also advances locally between refreshes. Expired keys show a warning.

Missing keys, missing self information, and status failures have explicit
states. A null expiry for an existing node key is shown as **Expiry disabled**,
without inventing an expiration date or lifetime. Failed status refreshes hide
stale expiry values. No progress percentage is shown because the status API
does not report the current key's issuance time or configured lifetime.

**Renew** opens the administrator-only **KEY RENEW** confirmation and sign-in dialog. Connect
over the LAN first: renewal can interrupt Tailscale, and completing login turns
the tailnet connection on. Sign in using the same Tailscale account and tailnet.
The dialog calls `renewTailscaleNodeKey` once after acknowledgement to prepare
a request, without changing Tailscale. **Sign in to Tailscale** calls
`beginTailscaleNodeKeyRenewal(attemptID)` and opens a new tab, which navigates to
the validated sign-in link when ready. A manual link is available if popups are
blocked. The renewal dialog uses the top-right X to close, with no **Check status**
or **Close** buttons. Before Sign in, X and Escape call
`cancelTailscaleNodeKeyRenewal(attemptID)` and wait for confirmed cancellation;
failed or uncertain cancellation keeps the dialog open for status recovery.
Prepared/cancelled requests never show a renewal success check mark. The shared
Tailscale provider then polls the read-only `tailscaleKeyRenewal` query every two
seconds while pending, even after closing the dialog or changing WebUI tabs. It
shows a private, validated Tailscale sign-in link, a device-approval step when
needed, and reports completion only when the backend sees a changed usable node
key and a running connection. Both the dialog and **KEY EXPIRY** panel show a
rotating indicator with **Waiting for renewal…**, then a green check mark and
**Node key renewed successfully** once verified. The overview refreshes on
completion, polling stops, and the success message remains for this session
until another renewal/status check changes it. Reduced-motion preferences are
respected, and state changes are announced by an accessible live status region.

Errors stop polling; close with X and reopen **Renew** to check status before
another explicit write. Requests have a 60-second deadline and mutations are
never automatically retried.
After Sign in has started, closing only dismisses the dialog: it does not cancel
the daemon's login flow or its completion checks. On errors, the panel shows
**Renewal not confirmed**; reopen **Renew**
to check status. Signing out stops monitoring and clears its state. Sign-in
URLs stay in memory only; automatic sign-in and manual renewal share a single
dialog, so prompts cannot stack. A portal restart loses prepared requests and the comparison
baseline. Prepare a new request and choose Sign in to resume an existing daemon
login link; review the overview's expiry when rotation cannot be verified.

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

## Network activity

The overview reads authenticated `networkActivity` snapshots from `tailscale0`
approximately every two seconds. It shows VPN-only download/upload rates, a
rolling one-minute chart, and separate saved 24-hour and all-time totals.
The **Live** view does not represent all LAN traffic or encrypted transport
overhead. Binary units (KiB, MiB, GiB) and byte-per-second rates are used.

RX/TX counters arrive as decimal strings; BigInt differences prevent precision
loss with large cumulative values. The first sample waits for a second reading
before showing rates. Counter/boot/interface resets and gaps reset the chart.
Unavailable data clears stale measurements and provides a retry button alongside
automatic retries. Requests have a five-second timeout. Polling pauses in hidden
tabs and stops on navigation/logout; HTTP 401 signs the user out.

**Last 24 hours** and **Total traffic** appear together in both views. They read
the hourly saved `totals` snapshot through `networkActivityHistory`, including
separate download/upload totals. Total traffic covers all usage recorded by
the portal and survives interface resets, restarts, and hourly record cleanup.
The “as of” timestamp identifies the last successful hourly save; reads do not
recalculate the saved window during downtime. Totals and their download/upload
breakdowns display `0 B` before measurements arrive; hourly coverage still marks
missing samples separately. Loading and request errors keep their own states.

The **Last 24 hours** chart shows 24 completed UTC hours ending at that saved
window boundary, with local-time labels, captured totals, and expandable
hourly details. Solid bars have full coverage; faded bars are partial. Missing
hours are dashed, not fabricated zero-traffic hours. A fully observed idle hour
is a real zero. Empty history explains that the first data arrives after the
next hourly save with valid samples.

Recording runs in the backend independently of browser sessions. It samples in
memory every minute and updates hourly history, the 24-hour totals, and all-time
totals in one database transaction per hour, also pruning old hourly records.
Persisted records survive restarts; the unfinished hour (and failed, pending
saves) can be lost on restart. Samples crossing hour boundaries are prorated by
elapsed time. The backend initializes or upgrades the history and totals tables
automatically at startup.
Upgrades seed all-time totals from retained history; already-pruned records
cannot be restored. The overview refreshes saved totals/history every minute,
pauses requests while hidden, and cancels them on navigation. Database errors
show a separate retry state without disabling the independent live sampler;
live sampling failures do not hide saved totals.

## LAN settings

**Network → LAN IPv4 settings** configures eth0 through `setDeviceIP` using
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
