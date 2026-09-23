# Fairnet Portal WebUI

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

Use **Change password** in the sidebar to update the signed-in account. The form
sends `current_password` and `new_password` to `POST /api/change-password`, with
`Authorization: Bearer <token>`. It validates the new password's 8–72-byte UTF-8
limit and confirmation, handles the empty `204` success response, and displays
backend errors. An incorrect current password does not sign the user out.
Successful changes keep the current session because existing JWTs are not revoked.

The dashboard's network data remains illustrative. Tailscale routes are not yet
registered by the backend; connecting those panels is a separate step.
