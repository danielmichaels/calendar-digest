# calendar digest

> Nightly per-recipient digest of tomorrow's Google Calendar events

## Production: Cloudflare Workers

The replacement runs on one Worker, D1 and a five-minute Cron Trigger. It keeps
per-recipient timezone schedules, multi-calendar Google reads, private digest
links, Telegram/email delivery, retries, access alerts and the operator UI.
Every HTTP route is protected by Cloudflare Access using Worker Kit, including
digest links and diagnostics. There is no public or Basic-auth fallback.
Production is deployed at `calendar.danielms.site`, behind the
`danielms.cloudflareaccess.com` organization. Automation and delivery were enabled
with Daniel's approval on 3 November 2026 after the final stopped-source backup
and D1 hash comparison. Dokploy is stopped; its volume and the Go app below are
retained for rollback. Do not restart it while the Worker is sending.
No staging environment was created.

| Runtime | Status | Code and configuration |
| --- | --- | --- |
| Workers + D1 + Cron | Active production; use this for new changes | `worker/`, `wrangler.jsonc`, `package.json` |
| Go + SQLite + River | Stopped legacy fallback, retained for rollback | `cmd/`, `internal/`, `assets/`, `Taskfile.yaml`, container files |

The Worker does not run Go or use the legacy SQLite volume, River or Litestream.
Keep the fallback until real Worker delivery has been verified and its removal
is approved. Retaining its source does not mean running both implementations.

See [the Worker runbook](docs/workers.md) for Amp-only management, secrets,
consistent SQLite transfer, rehearsal, cutover and rollback. No local-machine
Cloudflare login is required. `.agents/setup` prepares the Worker and Go
toolchains for orbs; `.agents/resume` performs fast readiness checks.

Before starting a local preview, create `.dev.vars` from `.dev.vars.example` if
it does not exist. Set `AUTOMATION_ENABLED=false` and `DELIVERY_ENABLED=false`
there. Supply development-only Access settings and a CSRF secret; the preview
rejects unauthenticated requests and has no authentication bypass. Do not copy
production provider credentials into a local preview.

```shell copy Copy Worker Development
npm install --ignore-scripts
npm run db:local
npm run dev
```

`wrangler.jsonc` now records the active production flags: a real deployment keeps
sending enabled. `npm run check`, `npm test`, and `npm run build` check the Worker;
the build command does not deploy.

## Legacy Go fallback

Everything below describes the **legacy Go implementation**, not Worker setup.
It is retained for recovery and reference, not as a second production service.

### Local development only

Use Go 1.26, Task, Air and Tailwind. Fresh Amp orbs install these through
`.agents/setup`; no manual installation is needed there. Outside an orb, install
Go and Task first, then use `task install_bins` for the other Go tools. Install
Tailwind separately or use the locked npm package. Template generation uses
`go tool templ`; sqlc and goose are needed only for query/schema development.

Start from a fresh checkout with no production `.env` or database. Copy
`.env.example` to `.env` without overwriting an existing file, then check it:

- Keep `APP_ENV=development` and `RIVER_UI_EMBEDDED=false`.
- Use a disposable `DATABASE_URL`, such as `database/go-dev.db`. Never use the
  stopped production volume, a backup containing real recipients or D1.
- Leave Google, Telegram, alert-chat, email and Cloudflare provider credentials
  empty, including any inherited shell/Amp environment values. Go starts its
  scheduler automatically and has **no Worker-style delivery-disable flags**.
- Do not expose this server publicly. Go does not include the Worker's Access
  guard; its digest links are bearer-token URLs and its operator UI is not gated.

```shell copy Copy Go Development
go mod download
go tool templ generate
task css
task dev
```

`task dev` uses Air to regenerate templates/CSS, build and start the Go server on
port 9898. Startup creates and migrates the configured local SQLite file. Stop it
with Ctrl+C. In an orb, run previews through a supervised Amp service and portal,
then stop that service when finished; do not leave a second sender running.
`task test` runs Go tests without a production database. `go build ./...` checks
compilation. `task init` also regenerates sqlc output and runs `go mod tidy`; it
is not required for normal startup of the checked-in application.

### Production rollback only

Do **not** run `docker compose up` or restart the old Dokploy container to roll
back directly. The retained Go database is stale once the Worker accepts edits
or sends messages. First follow [the state-aware rollback runbook](docs/workers.md#rollback):
disable Worker scheduling, settle and reconcile in-flight deliveries, export
current D1 application data, restore it into a fresh migrated Go database and
compare manifests. Re-establish whole-host Access at the proxy before routing
traffic to Go; Go does not validate Access JWTs itself. Enable exactly one sender
only after reconciliation. Preserve the old volume and backups until cleanup is
explicitly approved.

### Go layout

```
assets/            embedded into the binary: migrations, static files
  migrations/      goose SQL, applied automatically at boot
  sqlc.yaml        sqlc config, beside the schema it reads
cmd/app/         entrypoint and CLI wiring
internal/
  cmd/             kong commands: serve, migrate, healthcheck
  config/          all configuration, decoded from the environment
jobs/            River workers and job definitions
logging/         slog setup and the trace_id handler
server/          router, middleware, JSON API handlers
  store/           sqlc output, connection pool, migrations
ui/              templ handlers and templates
version/         build stamping
```

### Go database

SQLite lives at `database/data.db`. Migrations are applied by the binary at
boot; the `goose` tasks are dev conveniences for creating migrations and
inspecting status.

Litestream replicates the file to S3-compatible storage. It has to wrap the
process (`replicate -exec`), which is what the `entrypoint` script is for.

```shell
task db:migration:create -- add-widgets   # new migration
task db:migration:status                  # what has been applied
task clean                                # delete the local database
```

### Go configuration

Everything comes from the environment; `.env` is loaded for local development.
See `.env.example` for the full list. `config.Load` reports every problem at
once, so a misconfigured deployment takes one pass to fix rather than one
restart per missing variable.

Set `APP_ENV=production` in a deployment: it turns the development-friendly
defaults into hard startup errors. Before starting a production container, set
these production-only requirements:

- `TRUSTED_ORIGINS` must contain at least one allowed origin, such as
  `https://calendar.example.com` (comma-separated for multiple origins).
- `SESSION_COOKIE_SECURE` must be `true` when the site is served over HTTPS.

If any of these are missing or invalid, the process exits at startup instead of
running with an unsafe configuration. See [docs/deployment.md](./docs/deployment.md)
for the container environment example.

### Go HTTP surface

This table is **not** the Worker's security model. All Worker paths require
Cloudflare Access; the legacy Go routes below do not enforce it themselves.

| Path | What |
|---|---|
| `/healthz`, `/version` | Monitoring, also in the OpenAPI spec |
| `/docs`, `/openapi.json` | API reference |
| `/app` | The rendered UI |
| `/d/{token}` | Digest detail page — unguessable token, no auth, linked from each digest |
| `/static/*` | Embedded assets |
| `{{ RIVER_UI_PATH }}` | Job dashboard — off unless `RIVER_UI_EMBEDDED=true`, and unauthenticated until you gate it |

Pages are [templ](https://templ.guide) templates updated in place by
[Datastar](https://data-star.dev). Cross-origin writes are rejected by
`http.CrossOriginProtection`; list any legitimate cross-origin callers in
`TRUSTED_ORIGINS`.

### Go deployment reference

See [docs/deployment.md](./docs/deployment.md) for the full runbook: GCP project
setup, calendar sharing with the service account, Dokploy deployment, and the
SMS webhook contract for v2.

### Go container build only

Building an image does not deploy it or start a sender. Do not start it against
production data outside the rollback procedure above.

```shell copy Copy Go Container Build
docker build -t calendar-digest .
```
