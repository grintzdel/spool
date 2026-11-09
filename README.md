# Spool

**A local webhook inbox that tells you _why_ your signature check fails.**

Spool is a single Go binary. It captures webhooks on your machine, keeps them in a SQLite history, forwards them to your app, replays them, and diagnoses signature failures. No runtime, no service to run, nothing in the cloud.

The most common webhook bug is not a wrong secret: the app parses or re-serializes the JSON body before verifying the HMAC. Spool stores the body byte for byte, so it can tell whether the sender, the secret or your app is at fault, and name the cause.

```
$ spool verify 01M70X7AFJQB7T44PTK0TBQ7TC
✗ BODY_REFORMATTED  Same JSON value, different bytes
  provider: stripe   request: 01M70X7AFJQB7T44PTK0TBQ7TC
  → The body was parsed then re-serialized before verification. Verify against the raw bytes.

  first divergent byte at offset 1
    raw: "{\n  \"id\": \"evt_smoke\",\n  "…
    app: "{\"id\":\"evt_smoke\",\"type\":"…

  Fix by framework:
    Express  app.post('/webhook', express.raw({ type: 'application/json' }), handler) and verify req.body (a Buffer)
    NestJS   NestFactory.create(AppModule, { rawBody: true }) then verify req.rawBody
    ...
```

## Install

```sh
go install github.com/grintzdel/spool/cmd/spool@latest
```

Prebuilt binaries for Linux, macOS and Windows (amd64, arm64) are attached to each [release](https://github.com/grintzdel/spool/releases). A Homebrew tap (`brew install grintzdel/tap/spool`) is published by the release pipeline.

## Quick start with Stripe

```sh
# terminal 1: capture on 127.0.0.1:7777, forward to your app, UI on 127.0.0.1:7778
export SPOOL_STRIPE_SECRET=whsec_...        # the secret printed by `stripe listen`
spool serve --forward http://localhost:3000

# terminal 2: Spool sits behind the Stripe CLI tunnel
stripe listen --forward-to localhost:7777/webhooks/stripe
stripe trigger payment_intent.succeeded

# your app answers 400 "invalid signature"?
spool ls
spool verify <id>               # OK_RAW: sender and secret are fine, your app alters the body
# fix the app, then send the same event again with a fresh signature
spool replay <id> --resign
```

Spool does not open a public tunnel: put it behind `stripe listen`, `ngrok` or any tunnel you already use.

## Commands

Only `serve` runs as a process. Every other command reads the same SQLite database directly, so they work even when `serve` is stopped.

| Command | Role | Key options |
|---|---|---|
| `spool serve` | Capture, forward, UI | `--port 7777`, `--bind 127.0.0.1`, `--forward URL`, `--respond ack\|passthrough`, `--ui-port 7778` |
| `spool ls` | List requests, most recent first | `--provider`, `--event`, `--failed`, `--limit 20`, `--before ID`, `--json` |
| `spool show <id>` | A request, its attempts and diagnosis | `--raw`, `--headers`, `--hex`, `--json` |
| `spool verify <id>` | Signature diagnosis (exit 1 unless `OK_RAW`) | `--provider`, `--secret-env NAME`, `--body-file`, `--tolerance 300s`, `--json` |
| `spool replay <id>` | Send a stored request again | `--to URL`, `--times N`, `--resign`, `--set-header K=V`, `--no-marker` |
| `spool export <id>` | Reusable output | `--format curl\|json\|http`, `--to URL` |
| `spool prune` | Purge the history | `--older-than 7d`, `--keep 1000` |

Ids are ULIDs; any unique prefix works (`spool show 01M70X`).

**Response modes.** `ack` (default) answers `200` at once, like a healthy receiver. `passthrough` waits for your app and relays its status, which is useful to test the provider's retry behaviour, but a slow or failing app makes the provider retry and creates duplicates.

**Forward and replay** keep the method, path, query, headers and raw body. Hop-by-hop headers are dropped, `Host` and `Content-Length` are recomputed, and `X-Spool-Id` is added (plus `X-Spool-Replay` on replays unless `--no-marker`). Timeout is 10 s, with at most 4 concurrent sends.

## Signature diagnosis

| Provider | Header | Signed content | Algorithm |
|---|---|---|---|
| Stripe | `Stripe-Signature: t=<ts>,v1=<sig>[,v1=...]` | `<ts>.<raw body>` | HMAC-SHA256 hex, several `v1` during secret rotation |
| GitHub | `X-Hub-Signature-256: sha256=<sig>` | raw body | HMAC-SHA256 hex |
| Generic | configurable | `{body}` or `{ts}.{body}` | SHA-1/256/512, hex or base64, optional prefix |

The timestamp is checked against the time Spool **received** the request, not the current time, so an old event is judged as it was.

| Code | Meaning | Hint |
|---|---|---|
| `OK_RAW` | Signature valid on the raw body | Sender and secret are right; if the app refuses, it alters the body or uses another secret |
| `HEADER_MISSING` | No signature header | Secret not configured on the provider, or a proxy strips the header |
| `HEADER_MALFORMED` | Header cannot be parsed | Wrong provider selected |
| `TIMESTAMP_EXPIRED` | Outside the tolerance window | Replay of an old event: use `--resign` |
| `SECRET_MISMATCH` | HMAC differs on the raw body | Wrong secret (test vs live, dashboard vs `stripe listen`) |
| `BODY_REFORMATTED` | Same JSON value, different bytes | Body parsed then re-serialized before verification |
| `BODY_WHITESPACE` | Only line endings, BOM or final newline differ | Text or encoding middleware |
| `BODY_UNICODE` | `é` rewritten as `é` (or the reverse) | JSON re-serialization |
| `BODY_CHANGED` | Content really differs | Middleware modifies or truncates the body; first divergent byte shown |

The `BODY_*` codes need the body as your app saw it. Either pass it with `spool verify <id> --body-file app-body.json`, or add the companion middleware and let Spool collect it.

### Companion middleware (Express, NestJS)

[`middleware/node`](middleware/node) holds `@spool/echo`. One line in your app, development only:

```ts
import { echo } from '@spool/echo'
app.use(echo())
```

When a forwarded request (it carries `X-Spool-Id`) ends with a status ≥ 400, the middleware sends the body as the app saw it to `POST 127.0.0.1:7778/api/requests/{id}/seen`. Spool stores it and diagnoses the request on its own. The middleware does nothing when `NODE_ENV=production` or when Spool is not running.

## Configuration

Spool reads `.spool.toml` in the current directory (or `--config`). Every key is optional; see [`spool.example.toml`](spool.example.toml).

**Secrets are never accepted as CLI arguments and never stored in the database.** They are read, in order, from `--secret-env NAME`, `$SPOOL_<PROVIDER>_SECRET`, the `secret_env` of the provider in `.spool.toml`, then its `secret` (keep `.spool.toml` in your `.gitignore`).

Any other HMAC scheme is a few lines of config:

```toml
[providers.acme]
header = "X-Acme-Signature"
timestamp_header = "X-Acme-Timestamp"
template = "{ts}.{body}"
algorithm = "sha256"
encoding = "hex"
prefix = "sha256="
```

The history lives in `$SPOOL_DB`, or `~/.local/share/spool/spool.db` (`%LOCALAPPDATA%\spool` on Windows), created with mode `0600`. Retention defaults to 30 days or 10,000 requests and is applied when `serve` starts.

## Web UI

`serve` also exposes a UI on `127.0.0.1:7778`, never on the network even with `--bind 0.0.0.0`: a live list over SSE, request detail (headers, pretty/raw/hex body, diagnosis, attempts) and actions (replay, replay re-signed, verify, copy as curl). It is plain `html/template` plus htmx, embedded in the binary.

| Route | Role |
|---|---|
| `GET /`, `GET /r/{id}` | List and detail pages |
| `GET /api/requests`, `GET /api/requests/{id}` | JSON list (paginated) and detail |
| `POST /api/requests/{id}/replay` | Replay (`{"resign":true,"to":"...","times":2}`) |
| `POST /api/requests/{id}/verify` | Diagnosis |
| `POST /api/requests/{id}/seen` | Body seen by the app (companion middleware) |
| `GET /api/events` | SSE stream of new requests, attempts and verifications |

## Architecture

Clean architecture: dependencies point inwards only.

```
cmd/spool/                  entry point, version injected with -ldflags
internal/
├── domain/                 pure Go, standard library only
│   ├── webhook/            Request, Attempt, Verification, ordered Headers
│   ├── signature/          Provider interface, registry, reason codes, HMAC helpers
│   │   ├── stripe/ github/ generic/
│   └── diagnose/           byte diff and BODY_* classification
├── usecase/                application logic and ports (Store, Sender, Publisher, Secrets, Clock, IDs)
│                           capture, forward + dispatcher, replay, verify, seen, export, queries, prune
├── adapter/
│   ├── capture/            inbound: HTTP handler on the capture port
│   ├── web/                inbound: UI, JSON API, SSE
│   ├── cli/                inbound: cobra commands
│   ├── sqlite/             outbound: Store on SQLite (modernc, no CGO), embedded migrations
│   ├── sender/             outbound: HTTP client with a concurrency limit
│   ├── bus/                outbound: in-memory pub/sub for the UI
│   ├── config/             .spool.toml, env, secrets
│   └── system/             clock and ULIDs
└── bootstrap/              composition root
middleware/node/            @spool/echo, published separately
```

Rule of thumb: **persist first, forward second**. The capture handler writes the request to SQLite before anything else, so an event is never lost even if your app is down.

Adding a provider means implementing `signature.Provider` (`Name`, `Detect`, `EventType`, `Verify`, `Sign`) in its own package and calling `signature.Register` from `init()`.

## Development

```sh
make test      # go test ./...
make race      # go test -race ./...
make lint      # golangci-lint
make fuzz      # fuzz the Stripe-Signature parser
make bench     # capture throughput, forward latency
make golden    # regenerate the diagnosis golden files
make build     # ./spool with the git version
```

CI runs golangci-lint, `go test -race` on Linux, macOS and Windows, the middleware tests and a GoReleaser snapshot on every PR. Pushing a `vX.Y.Z` tag publishes the release.

## Roadmap

- v0.1 Capture: `serve`, raw storage, `ls`, `show`
- v0.2 Diagnosis: Stripe, GitHub, generic HMAC, `verify`, reason codes, Express/NestJS middleware
- v0.3 Replay: live forward, `replay`, `--times`, `--resign`, `export`, `prune`
- v0.4 UI and release: htmx + SSE UI, GoReleaser, Homebrew, multi-OS CI
- Later: Svix, Shopify, Slack; Fastify, Go and Rails middleware; Bubble Tea TUI; edit a payload in `$EDITOR`; full-text search; diff of two requests

## License

MIT
