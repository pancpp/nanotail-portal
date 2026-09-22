# Fairnet Portal WebUI

React and TypeScript frontend for managing Tailscale on a NanoPi Zero2.

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

The production assets are written to `dist/`.

## Authentication flow

The login page submits `username` and `password` to `POST /api/user/login`.
The returned `access_token` is stored in browser local storage and grants access
to the protected dashboard route. Signing out clears the token and returns the
user to the login page.
