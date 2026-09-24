# Nanotail Portal WebUI

React and TypeScript frontend for managing Tailscale on nanotail.

## Development

Start the Go backend on port `8080`, then run:

```sh
npm install
npm run dev
```

Vite proxies requests under `/api` to `http://127.0.0.1:8080`.

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
Connection status is live; other overview panels are clearly marked previews.
