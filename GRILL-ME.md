# Grill Me Results

Generated: 2026-08-04T00:13:22.129Z

## Plan

(state was created by grill_record_turn; no plan recorded)

## Shared Understanding

Calendar Digest v1 decisions gathered from PLAN.md plus the daily detail-page extension.

## Questions and Answers

### 1. How should the management UI be exposed in v1?

**Recommended answer:** Tailscale-only

**User answer:** Tailscale-only

**Status:** resolved

**Notes:** Private household tool; avoids adding public authentication and keeps calendar-management UI off the public internet.

### 2. Which notification channels should v1 ship with?

**Recommended answer:** Telegram + email

**User answer:** Telegram, email, and SMS

**Status:** open

**Notes:** User wants SMS included too; SMS provider and credential/config shape still need choosing. This expands v1 beyond the current plan's launch implementations.

### 3. Which SMS provider should the notifier use for v1?

**Recommended answer:** Twilio

**User answer:** Generic webhook

**Status:** resolved

**Notes:** SMS is implemented as a webhook-backed notifier, avoiding a provider-specific SDK; webhook URL/auth and payload contract become implementation details to define.

### 4. What should the nightly digest message look like?

**Recommended answer:** Concise timeline

**User answer:** Concise timeline

**Status:** resolved

**Notes:** Use chronological events with time, title, and location; keep the core format portable across Telegram, email, and SMS.

### 5. What should happen when a recipient has no events tomorrow?

**Recommended answer:** Send confirmation

**User answer:** Send confirmation

**Status:** resolved

**Notes:** Send a short no-events confirmation so a successful run is distinguishable from a missing notification.

### 6. Should each recipient use one channel or receive via all configured channels?

**Recommended answer:** One active channel

**User answer:** All enabled channels

**Status:** resolved

**Notes:** Support fanout to every enabled target; the data model already permits multiple notification_targets, but the UI must support managing multiple targets rather than a single radio selection.

### 7. If one channel fails after another succeeds, how should retries behave?

**Recommended answer:** Retry all channels

**User answer:** Retry all channels

**Status:** resolved

**Notes:** Retain the simple recipient-level idempotency guard; duplicate sends are accepted as preferable to silently missing a digest.

### 8. Which calendar events should appear in the digest?

**Recommended answer:** Busy events only

**User answer:** Busy events only

**Status:** resolved

**Notes:** Include timed and all-day busy events; omit cancelled events and avoid exposing unnecessary event metadata.

### 9. If the app is down or Google is unavailable at the notify time, what should recovery do?

**Recommended answer:** Send when recovered

**User answer:** Send when recovered

**Status:** open

**Notes:** The current last_sent_date-only model cannot identify the missed digest after midnight; add an intended digest date/run state or explicitly define late recovery semantics before implementation.

### 10. Should each digest link to a full HTML page for that date?

**Recommended answer:** Yes

**User answer:** Yes; include a full-featured daily page with details omitted from terse notifications.

**Status:** open

**Notes:** This changes the plan: daily digest pages require event snapshot storage or regeneration, retention, and an access model; the notification should contain the page URL.

### 11. How should recipients access the detailed daily pages from notification links?

**Recommended answer:** Private Tailscale pages

**User answer:** Private via Tailscale or LAN; allow normal browser timeout when unreachable, with no special handling.

**Status:** resolved

**Notes:** Use ordinary private network URLs; no public signed-link system or special offline UX is needed.

### 12. How should daily detail pages obtain their event data?

**Recommended answer:** Snapshot at send time

**User answer:** Snapshot at send time

**Status:** resolved

**Notes:** Persist the event snapshot so the linked page remains consistent with the notification and does not depend on later Google API availability.

### 13. How long should detailed daily-page snapshots be retained?

**Recommended answer:** 90 days

**User answer:** 90 days

**Status:** resolved

**Notes:** Bound snapshot retention at 90 days; add cleanup logic and account for snapshots being included in SQLite/Litestream backups.

### 14. What contract should the generic SMS webhook use?

**Recommended answer:** JSON POST

**User answer:** JSON POST

**Status:** resolved

**Notes:** Use a documented JSON payload with phone number, message text, and detail-page URL; keep endpoint/auth configuration at app config or a safe target config boundary.

### 15. What details should the full daily page include beyond the terse digest?

**Recommended answer:** All visible event fields

**User answer:** All visible event fields

**Status:** resolved

**Notes:** Daily snapshots/pages should include all useful fields exposed by Calendar API, while terse channels remain compact.

### 16. For a missed run recovered after midnight, should it still send the original intended date's digest?

**Recommended answer:** Yes, original date

**User answer:** Yes, original date

**Status:** resolved

**Notes:** Track the intended local digest date independently of current date; late recovery must fetch/render that original date and then mark it sent.

## Agreed Decisions

- Management UI is private and reachable via Tailscale or LAN; no public auth layer is needed.
- V1 delivers through Telegram, email, and SMS.
- SMS uses a generic JSON POST webhook.
- Digest format is a concise chronological timeline with time, title, and location.
- Send a confirmation when there are no events.
- Fan out to all enabled notification targets per recipient.
- On partial delivery failure, retry all channels; duplicate successful sends are acceptable.
- Include busy timed and all-day events; omit cancelled events.
- Add a full HTML page for each digest date and link to it from notifications.
- Daily pages use snapshots captured at send time, not live regeneration.
- Retain daily snapshots for 90 days.
- Daily pages include all useful event fields exposed by Google Calendar.
- If a run is missed, recover by sending the original intended local date's digest, even after midnight.

## Open Risks

- The data model must be extended for daily event snapshots and intended/pending digest dates.
- The page base URL/hostname must be configured so links work on LAN and Tailscale; unreachable links may simply time out.
- Define SMS webhook authentication and exact JSON fields.
- Multiple enabled targets require a UI redesign from the plan's single target-kind radio selection.
- Retry-all fanout can duplicate messages after partial failure.
- Snapshots contain calendar details and will be included in SQLite/Litestream backups; 90-day cleanup must be reliable.

## Next Decision Needed

Define the SMS webhook authentication/configuration boundary and the snapshot/pending-run schema before implementation.
