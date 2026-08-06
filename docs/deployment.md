# Deployment Runbook

## Google Cloud Setup

### 1. Create a GCP Project

1. Go to [console.cloud.google.com](https://console.cloud.google.com) and create a new project.
2. Link a billing account if prompted (Google Calendar API is free for reasonable usage).

### 2. Enable the Google Calendar API

1. In the GCP Console, go to **APIs & Services > Library**.
2. Search for "Google Calendar API" and enable it.

### 3. Create a Service Account

1. Go to **APIs & Services > Credentials**.
2. Click **Create Credentials > Service Account**.
3. Give it a name (e.g., `calendar-digest`) and click **Create and continue**.
4. Skip the optional IAM roles step — Calendar access is controlled by calendar sharing, not project IAM.
5. Click **Done**.

### 4. Generate a JSON Key

1. Click on the newly created service account.
2. Go to the **Keys** tab.
3. Click **Add Key > Create new key**.
4. Choose **JSON** and download the file.
5. Store the key contents in your secrets manager (see [Secrets](#secrets) below).

The service account email follows the pattern:
```
{name}@{project-id}.iam.gserviceaccount.com
```
Note this email — it is needed for calendar sharing.

---

## Calendar Sharing (Per Recipient)

Each recipient must share their calendar with the service account. This is done in the Google Calendar UI and requires no GCP access for the recipient.

1. Open Google Calendar on a desktop browser.
2. Click **Settings > Settings for my calendars**.
3. Select the calendar to share.
4. Click **Share with specific people or groups**.
5. Click **Add people and groups**.
6. Enter the service account email (from step above).
7. Set permission to **"See all event details"** (read-only).
8. Click **Save**.

For a primary calendar, the Calendar ID is the owner's Gmail address (e.g., `dan@gmail.com`).
This ID is what goes in the `calendar_id` field when adding a recipient.

---

## Secrets

Never put credentials in the database or in plaintext files. The database replicates to R2 via Litestream; anything stored there rides along in every backup.

| Secret | Used for |
|---|---|
| `GOOGLE_SERVICE_ACCOUNT_JSON` | Service account key — set as environment variable |
| `TELEGRAM_BOT_TOKEN` | Bot for digests and operator alerts — set as environment variable |
| `ALERT_TELEGRAM_CHAT_ID` | Chat ID that receives access failure alerts — set as environment variable |
| `S3_*` | Litestream replication to R2 — set as environment variables |
| `EMAIL_FROM` | Onboarded sender address — set as an environment variable |
| `CLOUDFLARE_ACCOUNT_ID` | Cloudflare account for Email Sending — set as an environment variable |
| `CLOUDFLARE_API_TOKEN` | Cloudflare Email Sending token — set as a secret |

On Dokploy, store these in **Settings > Environment Variables** as secrets.

---

## Dokploy Deployment

### Prerequisites

- Docker image pushed to a container registry (GHCR, Docker Hub, etc.).
- R2 bucket created in Cloudflare with an R2 API token.
- Cloudflare Email Sending domain onboarded with SPF, DKIM, and DMARC, plus an API token with Email Sending: Edit permission. Email Routing is separate from outbound sending.
- Telegram bot created via [@BotFather](https://t.me/BotFather) and a chat ID obtained via [@userinfobot](https://t.me/userinfobot) or similar.

### docker-compose.yml

The project includes `docker-compose.yml` at the root. Mount the `db_data` volume for SQLite persistence across redeploys:

```yaml
services:
  app:
    volumes:
      - db_data:/calendar-digest/database:rw
volumes:
  db_data:
    driver: local
```

### Environment Variables

`APP_ENV=production` enables startup validation. Configure these two values
before starting the container; otherwise it exits instead of serving traffic:

```env
TRUSTED_ORIGINS=https://calendar.int.lookout.wiki
SESSION_COOKIE_SECURE=true
```

- `TRUSTED_ORIGINS` must list at least one origin, including its scheme (for
  example, `https://calendar.int.lookout.wiki`). Use a comma-separated list for
  multiple origins.
- `SESSION_COOKIE_SECURE` must be `true` when the application is served over
  HTTPS, which is the production setup.

Required application and service settings:

```env
APP_ENV=production
S3_DB_URL=s3://<account-id>/<bucket-name>
S3_ACCESS_KEY=<r2-api-token-id>
S3_SECRET_KEY=<r2-api-token-secret>
BASE_URL=https://calendar.int.lookout.wiki
GOOGLE_SERVICE_ACCOUNT_JSON=<full-json-on-one-line>
TELEGRAM_BOT_TOKEN=<bot-token>
ALERT_TELEGRAM_CHAT_ID=<chat-id>
EMAIL_PROVIDER=cloudflare
EMAIL_FROM=board@your-onboarded-domain.example
CLOUDFLARE_ACCOUNT_ID=<cloudflare-account-id>
CLOUDFLARE_API_TOKEN=<cloudflare-email-sending-token>
```

Optional (with defaults):

```env
CLOUDFLARE_API_URL=https://api.cloudflare.com/client/v4 # override for tests only
RIVER_UI_EMBEDDED=false
TICK_INTERVAL=5m
SNAPSHOT_RETENTION_DAYS=90
```

### Health Checks

The container exposes a health check at `/healthz`:

```yaml
healthcheck:
  test: ["CMD", "/usr/bin/app", "healthcheck"]
  interval: 30s
  timeout: 5s
  retries: 3
```

Dokploy uses this to determine when the container is ready.

The application sends through Cloudflare's REST endpoint using Go's standard
HTTP library; no Cloudflare Go SDK is required. After onboarding, perform one
real delivery test (for example, the admin digest command with `--email ...
--force`) and verify the recipient mailbox.

### Litestream Replication

Litestream runs inside the container, wrapping the app process. On first boot it restores the database from R2 if no local copy exists. Set `S3_DB_URL` to enable replication; without it the app runs without Litestream.

---

## SMS Webhook Contract (v2)

The SMS channel is not implemented in v1. When added later, it will POST to a configured webhook URL with this shape:

```http
POST {SMS_WEBHOOK_URL}
Authorization: {SMS_WEBHOOK_AUTH_PREFIX}{SMS_WEBHOOK_AUTH_HEADER}
Content-Type: application/json

{
  "to": "+61491570156",
  "body": "Tomorrow's calendar:\n09:00 Team standup\n14:00 Project review\n\nView details: https://calendar.int.lookout.wiki/d/abc123"
}
```

Environment variables for v2:

| Variable | Default | Description |
|---|---|---|
| `SMS_WEBHOOK_URL` | — | POST target for SMS sends |
| `SMS_WEBHOOK_TOKEN` | — | Static token sent as `Authorization` |
| `SMS_WEBHOOK_AUTH_HEADER` | `Authorization` | Header name |
| `SMS_WEBHOOK_AUTH_PREFIX` | `Bearer ` | Prefix before the token |
