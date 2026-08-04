# PRD: Calendar Digest Notifier

## Summary
A small, self-hosted tool that reads tomorrow's events off two Google Calendars (Dan + wife) every evening and pushes a digest to each person's preferred channel (Telegram, email, or other). Built to replace native Google Calendar/OS notifications, which get silently swallowed under notification-limit settings.

## Problem
Native calendar app notifications are unreliable on at least one phone in the household — notifications that aren't acknowledged immediately get dropped rather than queued. This causes missed events. A separate, deliberate "here's everything tomorrow" push the night before is more reliable than relying on per-event OS notifications.

## Goals
- Nightly digest of tomorrow's events, sent the evening before, per recipient.
- Two recipients initially (Dan, wife), each with their own calendar and independently configurable notify time and channel.
- Notification channel is user-selectable and changeable without a redeploy: Telegram to start, email as a fallback/alternative, extensible to others (ntfy, Pushover, SMS) later.
- Low maintenance: no credential expiry to babysit, no service to patch beyond normal container updates.
- Manageable via a web page (not CLI — container exec on Dokploy is a poor workflow for routine changes).

## Non-goals
- Not a general-purpose calendar app or scheduling tool.
- Not multi-tenant / not built for more than a handful of recipients.
- No write access to calendars — read-only, digest only.
- No public sign-up flow — recipients are provisioned by Dan, not self-service registration.

## Users
- **Dan** — admin and only person touching the underlying infra.
- **Wife** — consumer of the digest; her only required action is a one-time calendar share (see Calendar Access below). She does not need Google Cloud Console access, a password shared with Dan, or an account in this system beyond a config row.

---

## Key Decisions & Rationale

| Decision | Chosen | Rejected | Why |
|---|---|---|---|
| Calendar access | Google **service account** + calendar sharing | Per-user OAuth ("Sign in with Google") web flow | OAuth needs the same GCP/consent-screen setup *plus* publishing to Production to dodge Testing mode's 7-day refresh-token expiry, *plus* a token-storage/refresh subsystem. Service account keys don't expire on their own and need zero user-facing consent flow. Trade-off accepted: Dan holds a key with standing read access to both calendars, rather than each person holding their own revocable grant. Acceptable for a 2-person household tool. |
| Scheduled re-auth via automated browser login | Rejected outright | — | Fights Google's bot detection with real account credentials on a server; worse security posture than either OAuth or service accounts; solves a problem (7-day expiry) that's actually a one-time config toggle, not a recurring fight. |
| Job/scheduling model | **`time.Ticker`, 15-min poll loop** | River (Postgres job queue) | The workload isn't job-shaped — there's no queue of discrete work items, just "does anything match its send-time right now." River's durability guarantee (resume an interrupted job) doesn't apply because there's nothing in-flight to resume; the DB row *is* the state, re-evaluated every tick. Self-healing by construction: a missed tick (e.g. container restart) is caught by the next one. |
| Storage | **SQLite** | Postgres | No concurrent-writer requirement, no need for a separate DB service, one file to back up. Reserve Postgres for if this ever gets folded into the existing photo-sharing app's stack for convenience. |
| Delivery abstraction | **`Notifier` interface**, pluggable by `kind` | Hardcoded single channel | Explicit requirement: channel must be switchable per-recipient without code changes or redeploys — she may not want Telegram. |
| Management interface | **Web UI** (templ + Datastar) | CLI | Rejected CLI specifically because routine edits via container exec on Dokploy is poor UX for a tool meant to be touched occasionally, not by someone at a terminal. |
| Redundancy | **Litestream → R2** | Multi-instance Postgres/River | Off-box continuous replication of the SQLite file gives disk-loss protection without adding a DB service; reuses R2 credentials/bucket already provisioned for the photo project. |
| Deployment shape | Single binary, single container on Dokploy | Separate web + cron services | One process runs both the HTTP server and the ticker loop — matches Dokploy's one-container-per-app model, avoids wiring a second scheduled task. |

---

## Calendar Access Setup

Google Cloud (one-time, Dan only):
1. Create a GCP project (personal Google account, no Workspace required).
2. Enable the Google Calendar API.
3. Create a Service Account (no IAM roles needed — Calendar access is governed entirely by calendar sharing, not project IAM).
4. Generate a JSON key for the service account. Store in OpenBao, not on disk in plaintext.
5. Note the service account's email: `{name}@{project-id}.iam.gserviceaccount.com`.

Per recipient (Dan and wife, done independently, no GCP involved):
1. Google Calendar → Settings for the relevant calendar → "Share with specific people or groups."
2. Add the service account's email with permission **"See all event details"** (read-only — least privilege).
3. Calendar ID for a primary calendar is just the account's Gmail address.

This is the entire ask of the non-technical user: one form, in the calendar UI she already knows.

---

## Data Model (SQLite)

```sql
CREATE TABLE recipients (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL,
    calendar_id     TEXT NOT NULL,        -- Gmail address == calendar ID
    notify_time     TEXT NOT NULL,        -- "21:00", local to their tz
    tz              TEXT NOT NULL,        -- "Australia/Brisbane"
    last_sent_date  TEXT,                 -- "2026-08-04" — idempotency guard
    enabled         INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE notification_targets (
    id              INTEGER PRIMARY KEY,
    recipient_id    INTEGER NOT NULL REFERENCES recipients(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('telegram','email','ntfy')),
    config          TEXT NOT NULL,        -- JSON, e.g. {"chat_id":"..."} / {"address":"..."}
    enabled         INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE send_log (
    id              INTEGER PRIMARY KEY,
    recipient_id    INTEGER NOT NULL REFERENCES recipients(id),
    sent_at         TEXT NOT NULL,        -- actual send time
    target_kind     TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('ok','error')),
    detail          TEXT,                 -- error text or short result summary
    message_body    TEXT                  -- what was actually sent
);
```

**Explicitly not stored:** calendar events themselves (fetched live per run — nothing to persist or go stale) and secrets (service account key, Telegram bot token) — those are app configuration (env vars / OpenBao), not application data, and are deliberately excluded from the DB so they don't ride along in the Litestream/R2 backup.

**Known future extension:** if either person ever wants more than one calendar folded into their digest (e.g. a shared family calendar), `recipients.calendar_id` becomes a join table `recipient_calendars(recipient_id, calendar_id)`. Not built until needed.

---

## Delivery

```go
type Notifier interface {
    Kind() string
    Send(ctx context.Context, target json.RawMessage, msg DigestMessage) error
}
```

Implementations at launch: `TelegramNotifier` (bot token + `chat_id`), `EmailNotifier` (reuses Resend, already integrated in the photo-sharing project). The nightly loop resolves each recipient's enabled target(s) and fans out through this interface — adding a channel later (ntfy, Pushover, SMS) means a new struct satisfying the interface, no changes to scheduling or storage.

## Scheduling Behavior

- Single process runs a `time.Ticker` on a 15-minute interval.
- Each tick: for every enabled recipient, compare current time in their `tz` against `notify_time`; if past due and `last_sent_date` ≠ today, fetch tomorrow's events for their calendar via the service account, format, send through their enabled target(s), and stamp `last_sent_date`.
- Failure handling: on send error, do **not** stamp `last_sent_date` — the next tick retries automatically. No separate retry/backoff machinery needed at this volume.
- Per-recipient independent notify times/timezones are supported natively by this model (no fixed cron slot to share).

## Web UI Scope

- `/` — recipient list: name, active channel, notify time, last sent status.
- `/recipients/:id/edit` — Datastar-driven form: notify time, timezone, target kind (radio: Telegram / email), conditional fields per kind, "Send test" button that invokes `Notifier.Send` immediately against the live path.
- No account system beyond the two provisioned recipients; not a public-facing app.

## Security & Secrets

- Service account JSON key and Telegram bot token: env vars or OpenBao reference, never in SQLite, never in the Litestream/R2 backup path.
- If deployed behind a real domain via Dokploy/Traefik (rather than Tailscale-only), add Traefik basic-auth middleware at minimum — the app holds a bot token and controls where calendar contents get sent, so it shouldn't be unauthenticated on the open internet. A Tailscale-only deployment avoids this requirement entirely.

## Deployment

- Single Go binary, single container, deployed via Dokploy.
- Persistent volume mounted for the SQLite file — a redeploy without one silently wipes recipients/preferences.
- Litestream sidecar/init process continuously replicates the SQLite file to R2 (existing bucket/credentials from the photo project) for disk-loss redundancy.
- Build via Taskfile, consistent with existing tooling.

## Open Questions
- Tailscale-only vs. Dokploy+Traefik with basic auth — deployment topology not yet finalized.
- Whether email (Resend) ships in v1 alongside Telegram, or Telegram-only first with email added once the `Notifier` interface is exercised once in anger.
- Format/verbosity of the digest message itself (plain list vs. grouped by time-of-day, etc.) — not yet specified.

## Out of Scope (v1)
- Calendar writes of any kind.
- More than two recipients.
- Self-service sign-up / OAuth-based onboarding (documented as a rejected alternative above, not a future phase).
