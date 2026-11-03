# Cloudflare Worker runbook

## Scope and safety

The replacement is `worker/`: one TypeScript Worker, one D1 database, and a
five-minute Cron Trigger. It uses Worker Kit's `/access` authentication, CSRF and
response-header helpers. Its form-ingress/Queue framework is not used: calendar
scheduling and per-channel delivery remain application responsibilities.
There is no Queue, container, SMTP server or Litestream
process. D1 stores snapshots and a transactional delivery outbox. This suits the
current small, once-daily workload without losing retries or timezone schedules.

The checked-in configuration is **inactive**: automation and delivery are false,
public Worker URLs are disabled, and production is deployed at the new
`calendar.danielms.site` hostname behind whole-host Access. Its D1 schema is
initialized but contains no recipients, targets, snapshots or deliveries. No
staging resources were created. The Go application, container files and migrations
remain available for rollback. CI only checks code; it does not deploy.

## Provisioned production state — 3 November 2026

| Resource | Value |
| --- | --- |
| Worker | `calendar-digest` |
| Deployed version | `baa7bc92-d523-40de-acb0-6e41eeca3589` |
| Account | `838426fc87fcbeed8c0977bd7dccecad` |
| D1 database | `1682e65a-a930-4a63-bcb9-38f8984fddb1` (`calendar-digest`, Oceania) |
| Access application | `8db0d726-7d00-45ea-9c99-68c11d69d7e2` |
| Canonical hostname | `calendar.danielms.site` |
| Access organization | `danielms.cloudflareaccess.com` |
| Access policy | Only `dan@danielms.site`, existing identity provider, 24-hour session |
| Schedule | `*/5 * * * *`, with both safety flags false |

The random CSRF secret is configured; Google, Telegram and email credentials are
not. API readback confirmed the bindings, schedule, empty application tables and
foreign-key check. The initial migration is recorded in `d1_migrations`.
Unauthenticated HTTP checks redirect the app, digest links, assets and diagnostics
to the correct Access organization. Authenticated production UI/save and real
providers still need verification. The local signed-JWT UI and CSRF tests do not
replace those checks.

The existing Dokploy application, hostname and data are unchanged. This is a
deployed **inactive replacement**, not a completed data migration or cutover.
Do not point old links at the new host without an approved compatibility plan.
Before final import, use this empty production database for the approved
rehearsal, export evidence and then explicitly approve clearing only its imported
rehearsal rows, or create a fresh final D1 database and update the binding.

Deployment, remote database writes, secret changes, DNS/routes, integration
changes, stopping the old sender and deletion each need approval. Do not run two
active senders against the same recipients.

## Preserved contracts and changes

- Each enabled recipient keeps their IANA timezone, local notification time and
  comma-separated calendar IDs. Cron scans every five minutes, rather than
  assuming every recipient lives in the same UTC day. Recovery considers today
  and tomorrow only; it does not send expired dates. DST gaps and folds follow
  the existing Go schedule calculation.
- Google authentication uses the existing service-account JSON key and WebCrypto
  RS256 JWTs with the Calendar readonly scope. Keep the same service account and
  existing calendar sharing permissions. Project IAM roles do not grant calendar
  access. All calendars must remain shared with its `client_email`.
- Calendar reads use pagination and expanded recurring events. Events from all
  configured calendars are merged in stable time order. Cancelled, free and
  self-declined events are excluded. Snapshots retain event detail, participants,
  meeting links and original private digest tokens. Empty days still send.
- Snapshot creation and channel fanout commit atomically. A normal capture is
  unique per recipient/date. Send-now refreshes tomorrow and retains its token.
  Each channel gets its own frozen payload and 12 attempts. Claims have a
  three-minute lease; retry delay is max(300, attempt⁴) seconds, checked at the next
  tick. A drain processes at most 50 messages or 60 seconds. Provider timeouts
  can cause duplicates: delivery remains at least once, not exactly once.
- Telegram and Cloudflare Email Sending remain supported. Legacy SMTP was a
  development transport and is not ported. SMS remains explicitly unimplemented;
  it never reports a successful send. Existing email sending entitlement,
  verified sender and token permissions still need to be checked in the selected
  account. Paying for Workers does not itself prove email entitlement.
- Access checks run daily in UTC and retry while access is refused. Alerts are
  deduplicated by subject/UTC day and get five attempts. Recovery requires all
  enabled calendars to pass. Operator alerts use Telegram when its alert chat is
  set; otherwise errors remain visible in logs and the overview.
- Purging retains 90 days by default. `/app` includes undelivered snapshots,
  pending/exhausted deliveries and a manual retry action. River's dashboard and
  job tables are replaced by this outbox view, not imported.
- Recipient and channel controls, test-send, send-now, verification, private
  `/d/{token}`, `/healthz`, `/version`, `/docs` and `/openapi.json` remain, but
  **every HTTP path now requires Cloudflare Access**, including static assets,
  diagnostics and digest links. Basic authentication and public bearer-link
  access have been removed. An email/Telegram recipient must also be allowed
  by Access to open a detail link; knowing its token is no longer sufficient.
  POSTs require a matching HTTPS Origin and an identity-bound, signed form token.
  Responses retain no-store, no-referrer and noindex headers. Static CSS is now
  inline; Datastar is bundled unchanged.

## Cloudflare Access is mandatory

The vendored `@danielmichaels/worker-kit` 0.1.1 package is pinned in the lockfile;
see [vendor provenance](../worker/vendor/README.md). This is the kit's tested consumer
pattern, not a copied JWT implementation. It validates RS256 against the team's
JWKS, issuer, audience, expiry, canonical HTTPS host and explicit email allowlist.
Unsigned identity headers, alternate hostnames, HTTP, missing configuration and
unknown paths cannot bypass the guard. No local-development authentication bypass
exists. Cron events are not HTTP requests and do not need an Access session.

Before adding any route, inspect the account's existing Access applications and
policies through Amp. With approval, create or update a self-hosted application
covering the **entire canonical hostname**, not only `/app/*`. There must be no
Bypass/public policy for `/d/*`, `/healthz`, static files or alternate paths. Use
an explicit Allow policy and the intended identity provider. Keep `workers.dev`
and preview URLs disabled. Worker-side verification is a second gate if a route
or hostname is accidentally outside the edge Access application.

Set `ACCESS_HOST` to the hostname only. `ACCESS_TEAM_DOMAIN` is
`danielms.cloudflareaccess.com`, as returned by Daniel's personal account's
Access organization API and confirmed by Daniel, not ToolboxFM or `dansalt`.
Set `ACCESS_AUD` to the selected application's
audience tag, and `ACCESS_EMAILS` to the approved comma-separated email list.
Keep the application policy and Worker list aligned. Everyone on that list can
use the operator UI; this is not a per-recipient permissions system. Do not add
all channel destinations automatically. `ADMIN_CSRF_SECRET` is a separate random
secret used by Worker Kit for 30-minute identity-bound form tokens. The UI sends
it on each POST and refreshes it after page patches. CSP allows the inline
styles and expressions required by the existing Datastar client, plus same-origin
connections for SSE.

Deployment must prove unauthenticated requests go to the Access login/denial,
an approved signed-in user can read and save, and other identities cannot.
Monitoring needs an approved Access-authenticated method; `/healthz` is not a
public exemption. Calendar Digest now has its own application and audience;
Cycle Reminder and Untamed policies were not changed. Do not reuse ToolboxFM's
Access audience, allowlist or applications.

## Work entirely from Amp

No workstation or local Wrangler login is required. Amp can install, test and
bundle in its orb and use the connected **Cloudflare API** tools for account
inspection, D1, Worker uploads, secrets, schedules, routes and logs. Wrangler CLI
is an alternative when a scoped token is securely configured in the orb, not a
requirement for API management.

Use the project-scoped MCP named `cloudflare`. A successful read on 3 November
2026 verified `Dansult@pm.me's Account`, ID `838426fc87fcbeed8c0977bd7dccecad`,
and auth domain `danielms.cloudflareaccess.com`. The account also contains Cycle
Reminder. Both account ID and Access team domain are pinned in `wrangler.jsonc`.
The separate `cloudflare-api` connection exposes ToolboxFM's account
`0d9eab177b3141f8fb23970c0d3c2eb0`, which is **wrong for Calendar Digest**.
Do not create or deploy Calendar Digest resources there.

The project connection is authenticated. Account, organization and Access policy
checks, D1 schema initialization, Worker upload, secret configuration, schedule
and new custom-domain creation have succeeded through this connection. Cycle
Reminder supplied the identity-provider reference; its audience was not reused.
Calendar Digest has its own application, audience and allowlist. Email entitlement and
paid-plan status remain unverified. Never infer an account ID from an Access team
name. If the project MCP asks for login again, reconnect that exact project-scoped
connection in Amp rather than using the unrelated ToolboxFM connection.

After approval, an Amp agent can:

1. Inspect the intended account, existing script/routes and email entitlement
   with `cloudflare.search` and `cloudflare.execute`. Before any write,
   confirm GET `/accounts/{account}/access/organizations` returns
   `auth_domain: danielms.cloudflareaccess.com` for the pinned account.
2. Create a new D1 database with POST
   `/accounts/{account}/d1/database`. Record its ID, then apply
   `worker/migrations/0001_worker.sql` to that **new empty** database through its
   `/query` API. Record `0001_worker.sql` in Wrangler's `d1_migrations` table so
   later migration commands do not reapply it. Do not apply this schema to the old
   app database or reapply it to the already-initialized production D1 above.
3. Set the real D1 ID in `wrangler.jsonc`, set the intended HTTPS `BASE_URL`, and
   leave both safety flags false. Prepare an upload in the orb:

   ```shell copy Copy Upload Preparation
   npm run prepare:cloudflare -- ACCOUNT_ID D1_DATABASE_ID /tmp/calendar-upload.sql
   ```

   This command only bundles, prints the SQL MD5 and bundle SHA256, and writes
   owner-only SQL containing a disposable `_amp_deployment_bundle` table. It
   refuses an oversized transfer. No application data or secrets are included.
   The connected executor forbids fetching Amp temporary-file URLs. Instead use
   D1's `/import` API: `init` with the printed MD5 as `etag`, PUT this SQL file
   from the orb to its short-lived presigned upload URL, then `ingest` with that
   etag and returned filename. Poll an active import's bookmark until complete.
   This briefly blocks D1; do it only in an approved deployment window. The table
   name deliberately fails if an earlier transfer was not cleaned up.

   In `cloudflare.execute`, SELECT the sole `payload` from that table without
   printing it. Verify its SHA256 equals the orb's printed hash, then parse JSON.
   Check `account`, `script` and `team` against the pinned configuration and GET
   the account's Access organization to verify its `auth_domain` matches `team`.
   Create FormData with `metadata` as an application/json Blob of
   `JSON.stringify(payload.metadata)`. Append each `payload.modules` entry as
   a Blob of its `content`, with its `type` and `name`. PUT the form to
   `/accounts/{account}/workers/scripts/calendar-digest`. **Datastar must be a
   text/plain module despite its .js suffix**, not an ES module. The helper sets
   this correctly. Check the upload result before dropping the disposable table
   through `/query`. If upload fails, retain it for diagnosis, not a blind retry.
   This transfer path was exercised successfully without workstation credentials.

   The upload retains existing secret bindings but reapplies plain-text variables
   from `wrangler.jsonc`. Keep that configuration aligned with approved live flags
   on every later upload, or uploads will intentionally disable sending again.
4. Configure secrets with PUT to the script's `/secrets` endpoint and schedules
   with PUT to `/schedules`, body `[{"cron":"*/5 * * * *"}]`. Secrets must come
   from secure Amp inputs or approved server-to-orb transfer, not chat, commits,
   recordings or generated upload files. The account connection's credentials
   are not automatically available to the Worker for outgoing email.
5. Set the approved production custom domain separately. Uploading does not create
   Cron Triggers or routes. Keep both flags false until cutover.
   Check the API response, bindings, schedules and route
   after each operation; do not blindly retry a write with an unknown outcome.
   Also GET the script's `/subdomain` endpoint. To keep alternate URLs disabled,
   POST `{"enabled":false,"previews_enabled":false}` there if needed; enabling
   either public URL requires separate approval.

Required secret: `ADMIN_CSRF_SECRET`. Configure the four Access variables above
before HTTP use; there is no password fallback. For real calendar reads:
`GOOGLE_SERVICE_ACCOUNT_JSON`. For Telegram: `TELEGRAM_BOT_TOKEN`, optionally
`ALERT_TELEGRAM_CHAT_ID`. For email: `CLOUDFLARE_API_TOKEN` with the appropriate
email sending scope. Plain-text email settings: `EMAIL_FROM` and
`CLOUDFLARE_ACCOUNT_ID` (these may also be secret bindings). `BASE_URL` must be
the retained origin at final cutover so old and new digest links use the same host.
The new production hostname is currently isolated from the legacy hostname.

## Orb and local checks

`.agents/setup` installs pinned Go rollback tools, npm and locked Worker
dependencies, generates Go templates/CSS, and migrates only local D1. It copies
`.dev.vars.example` without overwriting existing values. It never authenticates
to Cloudflare or starts a server. `.agents/resume` is a fast tool check.
These scripts must reach the default branch before fresh orbs use them.

```shell copy Copy Worker Checks
npm run check
npm test
npm run build
python3 -m unittest discover -s scripts -p '*_test.py'
```

`npm run build` is a dry run, not a deployment. `amp orb services ensure` starts
the local preview declared in `.amp/services.yaml` and returns its portal URL.
Stop it with `amp orb service stop worker` when done. The example admin password
has been removed. Unconfigured local previews return 403 by design. Automated
tests use signed synthetic JWTs and a synthetic JWKS inside workerd; production
Access configuration is never disabled for development.

## Consistent backup and rehearsal

The live stocktake found `calendardigest-server-vtujhu-app-1` healthy on Dokploy,
Proxmox LXC 107 on vm3 (`192.168.100.252`, app host `192.168.100.133`). Volume
`calendardigest-server-vtujhu_db_data` holds a roughly 442 KB `data.db` plus an
active WAL. Its roughly 2.7 GB total is mostly `.data.db-litestream/ltx`.
**Do not copy the live main file alone.**

Use an approved SSH connection from the orb to make a backup where the live
database and its WAL are accessible. Python's SQLite backup API is provided:

```shell copy Copy Consistent Backup
python3 scripts/sqlite-transfer.py backup /path/to/live/data.db /secure/rehearsal.db
python3 scripts/sqlite-transfer.py export /secure/rehearsal.db /secure/rehearsal.sql
```

Alternatively use SQLite CLI `.backup` on the app host. The transfer script
opens the source read-only, checks integrity and foreign keys, refuses to
overwrite outputs and creates files with owner-only permissions. Transfer the
result securely to the orb, never through a public artifact. A running snapshot
is fine for rehearsal, but is **not** a final cutover export.

The export contains only recipients, channels, snapshots and app state, with
explicit IDs. It excludes River jobs, schema metadata and Litestream history.
Private tokens and event JSON remain unchanged. It writes a table-count/SHA256
manifest alongside SQL and guards against importing into nonempty application
tables. It refuses unfinished River sends or Worker deliveries: drain them or
reconcile each target explicitly before proceeding. `notified_at` alone proves
only that at least one channel succeeded, not that all channels succeeded.

No staging environment is required. Rehearse in the approved empty production D1
with both flags false and no real deliveries. The schema is already applied.
Import using D1's documented import flow (upload the SQL to
the presigned URL and poll the import bookmark), or execute these small INSERT
statements in ordered bounded batches via the D1 `/query` API. Do not split SQL
at semicolons: event text can contain them. Stop on any error. D1 bulk import is
not an all-or-nothing application transaction; if an import fails, obtain approval
to reset the rehearsal rows or use a new empty final database rather than
rerunning over partial data. Never clear legacy source data.

The exporter rejects individual statements over 95 KB; such snapshots need a
reviewed chunked transfer rather than silent truncation. Never import an output
without its completed manifest. Query every imported table ordered by primary
key with the exact columns in `scripts/sqlite-transfer.py`, serialize rows with
its `ensure_ascii=False, separators=(',', ':')` JSON convention and compare
counts/hashes. Alternatively export D1 SQL, load it into a fresh local SQLite
file with `sqlite3`, and run:

```shell copy Copy Manifest Check
python3 scripts/sqlite-transfer.py manifest /secure/imported-d1.db
```

Check foreign keys, private links, paused recipients, channel configuration,
timezones and multi-calendar event detail. Verify real calendar access before
sending. Test real delivery only with an explicitly approved test recipient and
no production recipients enabled. No real credential/provider or production
import test has been done by this implementation.

Configured legacy backup: B2 bucket `lookout-s3-backups`, prefix
`calendar-digest`, endpoint `https://s3.us-west-002.backblazeb2.com`. Restore
health was not tested. Before retirement, restore it into a disposable location
and check integrity, row counts and tokens. Keep the final consistent backup
independent of Litestream. Do not delete the volume, WAL or LTX files.

## Approved cutover window

1. Finish the rehearsal, real Google verification and approved test delivery.
   Record account/database IDs, Worker version, manifests and current DNS/routes.
   Confirm D1 Time Travel availability and retain a separate SQL export.
2. With specific approval, stop new scheduling on the old app, drain and inspect
   River sends, then quiesce the old container. Do not kill a running send and
   assume it failed. If deliveries cannot drain, reconcile ambiguous targets and
   their provider results before taking the final snapshot. Keep the old image,
   secrets, deployment configuration and volume intact.
3. Make a **new consistent final backup** after quiescence. Export and import it
   into a fresh production D1 schema, with Worker automation/delivery still false.
   Repeat all manifest and link checks. A rehearsal database is not the final
   source of truth. Never use a blanket conflict-ignore import.
4. With approval, route the existing public host to the Worker so retained
   `/d/{token}` URLs still work **after Access login**. First configure the
   whole-host Access application, Worker audience/allowlist, CSRF secret and
   exact `BASE_URL`. Check HTTPS, unauthenticated denial for every endpoint and
   authenticated digest/operator access before enabling the route.
5. Enable both automation and delivery only after the old sender is stopped.
   Install/verify the five-minute schedule. Observe a due local-time scan and
   independent channel delivery, access-check status, retries and empty-day
   behavior. Check logs and D1 outbox after the next full daily cycle before
   declaring retirement. Keep the legacy app stopped but recoverable.

## Rollback

Before Worker sending is enabled, route back to the old app and restart it only
after disabling the Worker. Its untouched database is valid at the final backup.
Once Worker sends or edits have occurred, **do not restart the stale Go database**:
it can duplicate messages or lose new settings/tokens.

For a later rollback, with approval disable Worker automation first, let
in-flight leases/sends settle and reconcile remaining per-target sends, then
disable delivery and remove/suspend its schedule. Export D1 consistently. Run
the transfer tool on that SQLite export; it refuses any unfinished target sends.
Create a fresh Go database with the retained app's migrations, import only the
application SQL and compare manifests. Worker generation defaults are excluded
and old snapshots/tokens remain compatible. Preserve both source databases.
Restore the route and start exactly one Go sender only after checking ambiguous
provider sends. The app may recover today/tomorrow, but not past dates. Manual
per-target reconciliation needs its own reviewed decision, not guessed stamps.

Rollback of code alone can use a previous Worker version, but D1 state/schema
must remain compatible. Do not combine a Worker rollback with blind D1 Time
Travel: rewinding successful delivery records can send duplicates.
