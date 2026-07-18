# rill-deploy-shim

A tiny reverse proxy that sits in front of [Rill](https://github.com/rilldata/rill)
Developer (`rill start`) so you can **catch the Rill "Deploy" button click** and route it to
your own publish flow instead of Rill Cloud.

It is meant to run behind an authenticating proxy (e.g. oauth2-proxy), which is its only
client. Everything except the two `/__ft/*` routes below is proxied verbatim to Rill.

## Why

Rill Developer's built-in **Deploy** button is upstream's *Rill Cloud* CTA. In local
`rill start` mode it navigates to `${loginUrl}?redirect=…?deploy=true`, where `loginUrl`
is hardcoded to `http://localhost:9009/auth` (Rill hardcodes `localhost`; there is no
flag/env to change it). For a self-hosted Rill that `localhost` points at the visitor's own
machine and is a dead end — and its intent is to deploy to Rill Cloud, which self-hosted
setups do not use.

This shim replaces that dead-end navigation with a call to a snapshot/publish service of
your choosing, so the Deploy button triggers your own publish flow.

## What it does

1. Injects `<script src="/__ft/deploy-shim.js">` into Rill's HTML. The script intercepts the
   Deploy click (capture phase) and calls the endpoint below instead of letting the
   `localhost:9009` navigation fire.
2. Serves `POST /__ft/deploy`, which calls a configurable snapshot/publish service — a Connect
   unary RPC `snapshot.v1.SnapshotService/TriggerSnapshot` over HTTP/1.1 — with a bearer token.

## Routes

| Route                      | Purpose                                                    |
|----------------------------|------------------------------------------------------------|
| `POST /__ft/deploy`        | Triggers the configured downstream publish service.        |
| `GET /__ft/deploy-shim.js` | The injected client script that hijacks the Deploy button. |
| `GET /healthz`             | Liveness check.                                            |

All other requests are proxied to Rill unchanged.

## Configuration (env)

| Var              | Default                     | Purpose                                                                                         |
|------------------|-----------------------------|-------------------------------------------------------------------------------------------------|
| `LISTEN_ADDR`    | `:9009`                     | Listen address (point your auth proxy's upstream here).                                         |
| `RILL_UPSTREAM`  | `http://rill:9009`          | The Rill editor's base URL.                                                                     |
| `SNAPSHOT_URL`   | `http://rill-snapshot:8484` | The snapshot/publish service base URL.                                                          |
| `SNAPSHOT_TOKEN` | —                           | Bearer token for the downstream service. Empty ⇒ `/__ft/deploy` returns 503; Rill still serves. |

## Build & release

Built to `ghcr.io/fairtier/rill-deploy-shim` by GitHub Actions on a `v<semver>` tag. Actions
authenticates to GHCR natively with `GITHUB_TOKEN` — no stored PAT.

```
git tag v0.1.0
git push origin v0.1.0
```

That builds and pushes `ghcr.io/fairtier/rill-deploy-shim:0.1.0` (immutable, no `:latest`).

**First release only:** GHCR creates the package private under the org — make it **public**
in Packages → `rill-deploy-shim` → Package settings → Change visibility → Public, or pulls
fail with 401.

Zero external Go dependencies (stdlib only) — keep it that way.
