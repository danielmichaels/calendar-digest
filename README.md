# calendar digest

> Nightly per-recipient digest of tomorrow's Google Calendar events

## Running it

Nothing needs installing first. SQLite is a local file under `database/`.

```shell
task init   # generate code and tidy modules — run once
task dev    # hot-reload server on http://localhost:9898
task test   # no database required
```

Tooling used by those tasks: [task](https://taskfile.dev),
[air](https://github.com/air-verse/air), [sqlc](https://sqlc.dev) and
[goose](https://github.com/pressly/goose). Install them with
`task install_bins`.

Tailwind is a standalone binary and is not installed by that task — see the
[Tailwind CLI docs](https://tailwindcss.com/blog/standalone-cli).


## Layout

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

## Database

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

## Configuration

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

## HTTP surface

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

## Deployment

See [docs/deployment.md](./docs/deployment.md) for the full runbook: GCP project
setup, calendar sharing with the service account, Dokploy deployment, and the
SMS webhook contract for v2.

## Container

```shell
docker build -t calendar-digest .
```
