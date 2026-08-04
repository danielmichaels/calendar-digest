# calendar digest

Nightly per-recipient digest of tomorrow's Google Calendar events

> Replace the placeholder sections below as the project takes shape. What is
> here is what the template knows; the domain is yours to describe.

## What this app does

_One paragraph: who uses it and what problem it solves. Write this first — it
is the context every other decision is judged against._

## Build and run

```shell
task dev     # hot-reload server
task test    # no database required
task audit   # lint, align, format
```

Do **not** build a binary to check your work — `task dev` is already running
one, and `go build ./...` is enough to check compilation.

`task dev` regenerates templ output on every change. Never edit a `*_templ.go`
file: it is generated and gitignored.

## Layout

| Path | Contents |
|---|---|
| `assets/` | Embedded into the binary: migrations, static assets |
| `cmd/app/` | Entrypoint, CLI wiring |
| `internal/cmd/` | kong commands: `serve`, `migrate`, `healthcheck` |
| `internal/config/` | All configuration, decoded from the environment |
| `internal/jobs/` | River workers and job definitions |
| `internal/logging/` | slog setup, `trace_id` handler |
| `internal/server/` | Router, middleware, JSON API handlers |
| `internal/store/` | sqlc output, pool, migrations, advisory locks |
| `internal/ui/` | templ handlers and templates |
## Conventions

- **Configuration** is environment-only, decoded once in `config.Load`. Add a
  field with an `env:` tag; never read `os.Getenv` from application code.
- **Errors** use `errors.Is`/`errors.As`, never `err ==`. Wrap with a package
  prefix: `fmt.Errorf("boards: create: %w", err)`.
- **Logging** is `slog`. A record logged with a request context carries
  `trace_id` automatically. Access logs are httplog's job, not yours.
- **Migrations** are goose SQL under `assets/migrations/`, applied by the
  binary at boot. Editing an applied migration is a no-op — add a new one.
- **Queries** are sqlc; write SQL in `internal/store/queries/` and run
  `task sqlc`. Do not hand-write query code.
- **HTML** is templ; interactivity is Datastar. A handler that updates part of
  a page returns an SSE patch, not a redirect.
## Testing

Test-driven where practical: write or update the test first, then implement.

## Further reading

- `README.md` — running it, HTTP surface, configuration
- `docs/` — architecture notes and runbooks
