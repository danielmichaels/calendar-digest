import { AccessError, GoogleCalendar } from './google';
import { deliver, telegram } from './delivery';
import { addDays, calendarIDs, dueDates, localParts } from './time';
import type { Digest, Env, Recipient, Snapshot, Target } from './types';

export async function capture(env: Env, google: GoogleCalendar, r: Recipient, date: string, force = false, now = new Date()): Promise<void> {
  const events = await google.events(r, date);
  const token = Array.from(crypto.getRandomValues(new Uint8Array(32)), b => b.toString(16).padStart(2, '0')).join('');
  const encoded = JSON.stringify(events);
  const write = force
    ? `INSERT INTO digest_snapshots(recipient_id,digest_date,token,events,created_at) VALUES(?,?,?,?,?)
       ON CONFLICT(recipient_id,digest_date) DO UPDATE SET events=excluded.events,created_at=excluded.created_at,notified_at=NULL,generation=generation+1`
    : `INSERT INTO digest_snapshots(recipient_id,digest_date,token,events,created_at) VALUES(?,?,?,?,?) ON CONFLICT(recipient_id,digest_date) DO NOTHING`;
  await env.DB.batch([
    env.DB.prepare(write).bind(r.id, date, token, encoded, now.toISOString()),
    env.DB.prepare(`INSERT INTO outbox(dedupe_key,snapshot_id,target_id,payload,available_at)
      SELECT s.id||':'||s.generation||':'||t.id,s.id,t.id,
        json_object('name',?,'date',s.digest_date,'token',s.token,'tz',?,'events',json(s.events)),?
      FROM digest_snapshots s JOIN notification_targets t ON t.recipient_id=s.recipient_id AND t.enabled=1
      WHERE s.recipient_id=? AND s.digest_date=? AND (?=1 OR s.token=?)
      ON CONFLICT(dedupe_key) DO NOTHING`).bind(r.name, r.tz, now.getTime(), r.id, date, force ? 1 : 0, token),
  ]);
}

export async function alert(env: Env, subject: string, detail: string, now: Date): Promise<void> {
  console.error(subject);
  await env.DB.prepare(`INSERT INTO outbox(dedupe_key,payload,max_attempts,available_at)
    VALUES(?,?,5,?) ON CONFLICT(dedupe_key) DO NOTHING`)
    .bind(`alert:${subject}:${now.toISOString().slice(0, 10)}`, JSON.stringify({ subject, detail }), now.getTime()).run();
}

export async function verify(env: Env, google: GoogleCalendar, now = new Date()): Promise<void> {
  const recipients = await env.DB.prepare('SELECT * FROM recipients WHERE enabled=1').all<Recipient>();
  let refused = false;
  for (const id of new Set(recipients.results.flatMap(r => calendarIDs(r.calendar_id)))) {
    try { await google.verify(id); } catch (error) {
      if (!(error instanceof AccessError)) throw error;
      refused = true;
      await alert(env, 'Calendar access refused', error.message, now);
    }
  }
  const previous = await env.DB.prepare("SELECT value FROM app_state WHERE key='calendar_access'").first<{ value: string }>();
  await env.DB.batch([
    env.DB.prepare(`INSERT INTO app_state(key,value,updated_at) VALUES('calendar_access',?,?)
      ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`).bind(refused ? 'failed' : 'ok', now.toISOString()),
    env.DB.prepare(`INSERT INTO app_state(key,value,updated_at) VALUES('verified_day',?,?)
      ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`).bind(now.toISOString().slice(0, 10), now.toISOString()),
  ]);
  if (!refused && previous?.value === 'failed') await alert(env, 'Calendar access restored', 'All enabled calendars can be read again. Dates that have not passed will be captured on the next tick.', now);
}

interface Outbox {
  id: number; snapshot_id: number | null; target_id: number | null; payload: string; attempts: number;
}

export async function drain(env: Env, now = new Date()): Promise<void> {
  if (env.DELIVERY_ENABLED !== 'true') return;
  const started = Date.now();
  for (let i = 0; i < 50 && Date.now() - started < 60000; i++) {
    const lease = crypto.randomUUID();
    const claimTime = now.getTime() + Date.now() - started;
    const item = await env.DB.prepare(`UPDATE outbox SET lease=?,lease_until=?,attempts=attempts+1
      WHERE id=(SELECT id FROM outbox WHERE sent_at IS NULL AND attempts<max_attempts
      AND available_at<=? AND lease_until<=? ORDER BY available_at,id LIMIT 1) RETURNING *`)
      .bind(lease, claimTime + 180000, claimTime, claimTime).first<Outbox>();
    if (!item) break;
    try {
      if (item.target_id !== null) {
        const target = await env.DB.prepare('SELECT * FROM notification_targets WHERE id=?').bind(item.target_id).first<Target>();
        if (!target) continue;
        await deliver(env, target, JSON.parse(item.payload) as Digest);
      } else {
        const payload = JSON.parse(item.payload) as { subject: string; detail: string };
        if (env.ALERT_TELEGRAM_CHAT_ID) await telegram(env, env.ALERT_TELEGRAM_CHAT_ID, `${payload.subject}\n\n${payload.detail}`);
      }
      await env.DB.batch([
        env.DB.prepare('UPDATE outbox SET sent_at=?,lease=NULL,lease_until=0,last_error=NULL WHERE id=? AND lease=?').bind(now.toISOString(), item.id, lease),
        env.DB.prepare(`UPDATE digest_snapshots SET notified_at=COALESCE(notified_at,?) WHERE id=?
          AND EXISTS(SELECT 1 FROM outbox WHERE id=? AND sent_at IS NOT NULL
            AND dedupe_key=digest_snapshots.id||':'||digest_snapshots.generation||':'||target_id)`).bind(now.toISOString(), item.snapshot_id, item.id),
      ]);
    } catch {
      // Transport errors can contain credential-bearing URLs. Store only a safe channel-independent message.
      await env.DB.prepare('UPDATE outbox SET available_at=?,lease=NULL,lease_until=0,last_error=? WHERE id=? AND lease=?')
        .bind(now.getTime() + Math.max(300, item.attempts ** 4) * 1000, 'Delivery failed; check channel configuration and provider health.', item.id, lease).run();
      console.error('Delivery attempt failed', { outbox_id: item.id, attempt: item.attempts });
    }
  }
}

export async function tick(env: Env, now = new Date()): Promise<void> {
  if (env.AUTOMATION_ENABLED !== 'true') return;
  const google = new GoogleCalendar(env.GOOGLE_SERVICE_ACCOUNT_JSON);
  let failures = 0;
  const verified = await env.DB.prepare("SELECT value FROM app_state WHERE key='verified_day'").first<{ value: string }>();
  const access = await env.DB.prepare("SELECT value FROM app_state WHERE key='calendar_access'").first<{ value: string }>();
  if (verified?.value !== now.toISOString().slice(0, 10) || access?.value === 'failed') {
    try { await verify(env, google, now); } catch { failures++; console.error('Calendar verification failed; will retry next tick.'); }
  }
  const recipients = await env.DB.prepare('SELECT * FROM recipients WHERE enabled=1 ORDER BY id').all<Recipient>();
  for (const r of recipients.results) {
    try {
      for (const date of dueDates(now, r)) {
        const exists = await env.DB.prepare('SELECT id FROM digest_snapshots WHERE recipient_id=? AND digest_date=?').bind(r.id, date).first();
        if (!exists) await capture(env, google, r, date, false, now);
      }
    } catch (error) {
      failures++;
      console.error('Digest capture failed', { recipient_id: r.id });
      if (error instanceof AccessError) {
        await env.DB.prepare(`INSERT INTO app_state(key,value,updated_at) VALUES('calendar_access','failed',?)
          ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`).bind(now.toISOString()).run();
        await alert(env, 'Calendar access refused', error.message, now);
      }
    }
  }
  await drain(env, now);
  const retention = Number(env.SNAPSHOT_RETENTION_DAYS);
  if (!Number.isInteger(retention) || retention < 1) throw new Error('Retention days must be positive.');
  await env.DB.batch([
    env.DB.prepare('DELETE FROM digest_snapshots WHERE digest_date<?').bind(addDays(now.toISOString().slice(0, 10), -retention)),
    env.DB.prepare('DELETE FROM outbox WHERE snapshot_id IS NULL AND available_at<?').bind(now.getTime() - retention * 86400000),
    env.DB.prepare(`INSERT INTO app_state(key,value,updated_at) VALUES('last_tick',?,?)
      ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`).bind(failures ? 'degraded' : 'ok', now.toISOString()),
  ]);
  if (failures) throw new Error(`${failures} calendar checks or captures failed; see operator overview.`);
}

export function digestFrom(r: Recipient, s?: Snapshot): Digest {
  let tz = r.tz;
  try { localParts(new Date(), tz); } catch { tz = 'UTC'; }
  return { name: r.name, date: s?.digest_date ?? localParts(new Date(), tz).date, token: s?.token ?? '', tz, events: JSON.parse(s?.events ?? '[]') ?? [] };
}
