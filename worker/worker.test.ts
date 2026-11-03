import { env } from 'cloudflare:workers';
import { applyD1Migrations } from 'cloudflare:test';
import { beforeAll, beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { issueCsrfToken } from '@danielmichaels/worker-kit/access';
import worker from './index';
import { AccessError, GoogleCalendar, convert } from './google';
import { capture, drain, tick, verify } from './jobs';
import { at, dueDates } from './time';
import type { Env as WorkerEnv, Recipient, Snapshot } from './types';

declare global {
  namespace Cloudflare { interface Env extends WorkerEnv { TEST_MIGRATIONS: import('@cloudflare/vitest-plugin').D1Migration[] } }
}

const bindings = env;
const csrfSecret = 'local-test-form-signing-key';
const email = 'owner@example.invalid';
const team = 'calendar-tests.cloudflareaccess.com';
let accessKeys: CryptoKeyPair;
let publicKey: JsonWebKey;
let assertion: string;

async function accessToken(claims: Record<string, unknown> = {}): Promise<string> {
  const encode = (value: string | Uint8Array) => btoa(String.fromCharCode(...(typeof value === 'string' ? new TextEncoder().encode(value) : value))).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
  const time = Math.floor(Date.now() / 1000);
  const data = `${encode(JSON.stringify({ alg: 'RS256', kid: 'current' }))}.${encode(JSON.stringify({ iss: `https://${team}`, aud: ['calendar-test'], email, iat: time - 10, exp: time + 300, ...claims }))}`;
  const signature = await crypto.subtle.sign('RSASSA-PKCS1-v1_5', accessKeys.privateKey, new TextEncoder().encode(data));
  return `${data}.${encode(new Uint8Array(signature))}`;
}

const r: Recipient = { id: 1, name: 'Alex', calendar_id: 'first, second', notify_time: '21:00', tz: 'Australia/Brisbane', enabled: 1 };
const now = new Date('2026-08-04T11:00:00Z');
const event = { id: 'a', summary: '<Team & planning>', start: '2026-08-05T09:00:00+10:00', end: '2026-08-05T10:00:00+10:00', all_day: false };
const fakeGoogle = { events: async () => [event] } as unknown as GoogleCalendar;
const enabled = (): WorkerEnv => ({ ...bindings, ACCESS_HOST: 'calendar.test', ACCESS_TEAM_DOMAIN: team, ACCESS_AUD: 'calendar-test', ACCESS_EMAILS: email, ADMIN_CSRF_SECRET: csrfSecret, DELIVERY_ENABLED: 'true', AUTOMATION_ENABLED: 'true', TELEGRAM_BOT_TOKEN: 'test-only', SNAPSHOT_RETENTION_DAYS: '90' });
const call = async (path: string, method = 'GET', body?: unknown, origin = 'https://calendar.test') => worker.fetch(new Request(`https://calendar.test${path}`, {
  method, headers: { 'Cf-Access-Jwt-Assertion': assertion, 'X-CSRF-Token': await issueCsrfToken(email, csrfSecret), Origin: origin, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined,
}), enabled());

beforeAll(async () => {
  await applyD1Migrations(bindings.DB, bindings.TEST_MIGRATIONS);
  accessKeys = await crypto.subtle.generateKey({ name: 'RSASSA-PKCS1-v1_5', modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: 'SHA-256' }, true, ['sign', 'verify']) as CryptoKeyPair;
  publicKey = await crypto.subtle.exportKey('jwk', accessKeys.publicKey) as JsonWebKey;
  assertion = await accessToken();
});
beforeEach(async () => {
  await bindings.DB.batch(['outbox', 'digest_snapshots', 'notification_targets', 'recipients', 'app_state'].map(t => bindings.DB.prepare(`DELETE FROM ${t}`)));
  await bindings.DB.prepare('INSERT INTO recipients VALUES(?,?,?,?,?,?)').bind(r.id, r.name, r.calendar_id, r.notify_time, r.tz, 1).run();
  await bindings.DB.prepare(`INSERT INTO notification_targets(id,recipient_id,kind,config) VALUES(1,1,'telegram','{"chat_id":"123"}'),(2,1,'sms','{"phone":"456"}')`).run();
  vi.stubGlobal('fetch', vi.fn().mockImplementation(async (input, init) => {
    const request = new Request(input, init);
    if (request.url === `https://${team}/cdn-cgi/access/certs` && request.redirect === 'manual') {
      return Response.json({ keys: [{ ...publicKey, kid: 'current', alg: 'RS256', use: 'sig' }] });
    }
    throw new Error('Unexpected outbound request');
  }));
});
afterEach(() => { vi.unstubAllGlobals(); });

describe('timezone scheduling and recovery', () => {
  it('keeps the notify boundary, midnight recovery window, disabled recipients and separate local dates', () => {
    expect(dueDates(new Date('2026-08-04T10:59:00Z'), r)).toEqual(['2026-08-04']);
    expect(dueDates(now, r)).toEqual(['2026-08-04', '2026-08-05']);
    expect(dueDates(new Date('2026-08-05T20:00:00+10:00'), r)).toEqual(['2026-08-05']);
    expect(dueDates(new Date('2026-08-06T22:00:00+10:00'), r)).toEqual(['2026-08-06', '2026-08-07']);
    expect(dueDates(now, { ...r, enabled: 0 })).toEqual([]);
    expect(dueDates(new Date('2026-08-04T14:00:00Z'), { ...r, tz: 'Pacific/Auckland' })).toEqual(['2026-08-05']);
    expect(dueDates(new Date('2026-08-04T14:00:00Z'), { ...r, tz: 'America/Los_Angeles' })).toEqual(['2026-08-04']);
  });
  it('matches Go gap, fold and midnight transition behavior and uses calendar-day windows', () => {
    expect(at('2026-09-27', '02:30', 'Pacific/Auckland').toISOString()).toBe('2026-09-26T14:30:00.000Z');
    expect(at('2026-04-05', '02:30', 'Pacific/Auckland').toISOString()).toBe('2026-04-04T14:30:00.000Z');
    expect(at('2026-03-08', '00:00', 'America/Havana').toISOString()).toBe('2026-03-08T04:00:00.000Z');
    expect(+at('2026-09-28', '00:00', 'Pacific/Auckland') - +at('2026-09-27', '00:00', 'Pacific/Auckland')).toBe(23 * 3600000);
  });
});

it('filters only cancelled, free and self-declined events and keeps storage metadata', () => {
  const item = { id: 'a', summary: 'Meeting', start: { date: '2026-08-05' }, end: { date: '2026-08-06' }, attendees: [{ email: 'other', responseStatus: 'declined' }], recurringEventId: 'series' };
  expect(convert(item, r.tz)).toMatchObject({ all_day: true, recurring: true, start: '2026-08-04T14:00:00.000Z', attendees: [{ email: 'other', response: 'declined' }] });
  expect(convert({ ...item, status: 'tentative' }, r.tz)?.status).toBe('tentative');
  expect(convert({ ...item, status: 'cancelled' }, r.tz)).toBeNull();
  expect(convert({ ...item, transparency: 'transparent' }, r.tz)).toBeNull();
  expect(convert({ ...item, attendees: [{ email: 'self', responseStatus: 'declined', self: true }] }, r.tz)).toBeNull();
});

it('atomically captures once, retains tokens on force refresh and retries only the failed channel', async () => {
  await Promise.all([capture(enabled(), fakeGoogle, r, '2026-08-05', false, now), capture(enabled(), fakeGoogle, r, '2026-08-05', false, now)]);
  expect((await bindings.DB.prepare('SELECT * FROM outbox').all()).results).toHaveLength(2);
  const original = await bindings.DB.prepare('SELECT * FROM digest_snapshots').first<Snapshot>();
  vi.mocked(fetch).mockResolvedValueOnce(Response.json({ ok: true }));
  await Promise.all([drain(enabled(), now), drain(enabled(), now)]);
  const deliveries = await bindings.DB.prepare('SELECT target_id,attempts,sent_at FROM outbox ORDER BY target_id').all();
  expect(deliveries.results[0]).toMatchObject({ target_id: 1, attempts: 1, sent_at: now.toISOString() });
  expect(deliveries.results[1]).toMatchObject({ target_id: 2, attempts: 1, sent_at: null });
  expect((await bindings.DB.prepare('SELECT notified_at FROM digest_snapshots').first())?.notified_at).toBe(now.toISOString());
  await drain(enabled(), new Date(+now + 300000));
  expect((await bindings.DB.prepare('SELECT attempts FROM outbox WHERE target_id=1').first())?.attempts).toBe(1);
  expect((await bindings.DB.prepare('SELECT attempts FROM outbox WHERE target_id=2').first())?.attempts).toBe(2);
  await capture(enabled(), fakeGoogle, r, '2026-08-05', true, now);
  const refreshed = await bindings.DB.prepare('SELECT * FROM digest_snapshots').first<Snapshot>();
  expect(refreshed).toMatchObject({ token: original!.token, generation: 2, notified_at: null });
  expect((await bindings.DB.prepare('SELECT * FROM outbox').all()).results).toHaveLength(4);
});

it('rolls back the snapshot if fan-out fails and recovers an expired delivery lease', async () => {
  await bindings.DB.exec("CREATE TRIGGER reject_outbox BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT,'test failure'); END");
  await expect(capture(enabled(), fakeGoogle, r, '2026-08-05', false, now)).rejects.toThrow();
  expect((await bindings.DB.prepare('SELECT COUNT(*) n FROM digest_snapshots').first())?.n).toBe(0);
  await bindings.DB.exec('DROP TRIGGER reject_outbox');
  await capture(enabled(), fakeGoogle, r, '2026-08-05', false, now);
  await bindings.DB.prepare('DELETE FROM notification_targets WHERE id=2').run();
  await bindings.DB.prepare('UPDATE outbox SET lease=?,lease_until=?').bind('crashed', +now + 180000).run();
  await drain(enabled(), now);
  expect((await bindings.DB.prepare('SELECT attempts FROM outbox').first())?.attempts).toBe(0);
  vi.mocked(fetch).mockResolvedValueOnce(Response.json({ ok: true }));
  await drain(enabled(), new Date(+now + 180001));
  expect((await bindings.DB.prepare('SELECT sent_at FROM outbox').first())?.sent_at).not.toBeNull();
});

it('sends email through Cloudflare and does not mark a permanent bounce as delivered', async () => {
  await bindings.DB.prepare('DELETE FROM notification_targets').run();
  await bindings.DB.prepare(`INSERT INTO notification_targets(id,recipient_id,kind,config) VALUES(1,1,'email','{"address":"alex@example.invalid"}')`).run();
  const emailEnv = { ...enabled(), EMAIL_FROM: 'digest@example.invalid', CLOUDFLARE_ACCOUNT_ID: 'account', CLOUDFLARE_API_TOKEN: 'test-only' };
  await capture(emailEnv, fakeGoogle, r, '2026-08-05', false, now);
  vi.mocked(fetch).mockResolvedValueOnce(Response.json({ success: true, result: { permanent_bounces: ['alex@example.invalid'] } }));
  await drain(emailEnv, now);
  const [input, init] = vi.mocked(fetch).mock.calls[0];
  expect(String(input)).toBe('https://api.cloudflare.com/client/v4/accounts/account/email/sending/send');
  const body = JSON.parse(String(init!.body));
  expect(body).toMatchObject({ to: 'alex@example.invalid', from: 'digest@example.invalid' });
  expect(body.text).toContain('09:00–10:00  <Team & planning>');
  expect(body.html).toContain('&lt;Team &amp; planning&gt;');
  expect((await bindings.DB.prepare('SELECT notified_at FROM digest_snapshots').first())?.notified_at).toBeNull();
  vi.mocked(fetch).mockResolvedValueOnce(Response.json({ success: true, result: {} }));
  await drain(emailEnv, new Date(+now + 300000));
  expect((await bindings.DB.prepare('SELECT sent_at FROM outbox').first())?.sent_at).not.toBeNull();
});

it('records calendar refusal and one recovery only after every calendar works', async () => {
  const refused = { verify: async (id: string) => { if (id === 'second') throw new AccessError('share revoked'); } } as unknown as GoogleCalendar;
  await verify(enabled(), refused, now);
  await verify(enabled(), refused, now);
  expect((await bindings.DB.prepare("SELECT value FROM app_state WHERE key='calendar_access'").first())?.value).toBe('failed');
  expect((await bindings.DB.prepare('SELECT COUNT(*) n FROM outbox').first())?.n).toBe(1);
  const healthy = { verify: async () => {} } as unknown as GoogleCalendar;
  await verify(enabled(), healthy, now);
  await verify(enabled(), healthy, now);
  expect((await bindings.DB.prepare('SELECT COUNT(*) n FROM outbox').first())?.n).toBe(2);
});

it('keeps staging inert and protects all routes and writes with Access and signed form tokens', async () => {
  await tick({ ...enabled(), AUTOMATION_ENABLED: 'false' }, now);
  expect((await bindings.DB.prepare('SELECT COUNT(*) n FROM digest_snapshots').first())?.n).toBe(0);
  for (const path of ['/', '/app', '/d/known-token', '/healthz', '/version', '/docs', '/openapi.json', '/static/js/datastar.js', '/%61pp', '//app']) {
    expect((await worker.fetch(new Request(`https://calendar.test${path}`), enabled())).status).toBe(403);
  }
  expect((await call('/app/recipients', 'POST', {}, 'https://evil.test')).status).toBe(403);
  await capture(enabled(), fakeGoogle, r, '2026-08-05', false, now);
  const s = (await bindings.DB.prepare('SELECT * FROM digest_snapshots').first<Snapshot>())!;
  expect((await worker.fetch(new Request(`https://calendar.test/d/${s.token}`), enabled())).status).toBe(403);
  const response = await call(`/d/${s.token}`);
  expect(response.status).toBe(200);
  expect(response.headers.get('Referrer-Policy')).toBe('no-referrer');
  expect(response.headers.get('Cache-Control')).toBe('no-store');
  const body = await response.text();
  expect(body).toContain('&lt;Team &amp; planning&gt;');
  expect(body).toContain('09:00–10:00');
  expect((await call('/d/unknown')).status).toBe(404);
  const rejected = await call('/app/recipients', 'POST', { name: 'Test', calendar_id: 'x', notify_time: '99:00', tz: r.tz, enabled: true });
  expect(await rejected.text()).toContain('X-CSRF-Token');
  const created = await call('/app/recipients', 'POST', { ...r, id: undefined, name: 'New', enabled: true });
  expect(await created.text()).toContain('window.location.href="/app/recipients/2"');
});

it('rejects forged identities, wrong claims, alternate hosts and missing Access configuration', async () => {
  const request = (token: string, host = 'https://calendar.test') => new Request(`${host}/app`, { headers: { 'Cf-Access-Jwt-Assertion': token, 'Cf-Access-Authenticated-User-Email': email } });
  expect((await worker.fetch(request('forged'), enabled())).status).toBe(403);
  const parts = assertion.split('.');
  parts[2] = (parts[2][0] === 'A' ? 'B' : 'A') + parts[2].slice(1);
  expect((await worker.fetch(request(parts.join('.')), enabled())).status).toBe(403);
  for (const claims of [{ aud: ['another-app'] }, { email: 'stranger@example.invalid' }, { exp: 1 }, { iss: 'https://other.cloudflareaccess.com' }]) {
    expect((await worker.fetch(request(await accessToken(claims)), enabled())).status).toBe(403);
  }
  for (const host of ['https://calendar-digest.example.workers.dev', 'https://alternate.test', 'http://calendar.test']) {
    expect((await worker.fetch(request(assertion, host), enabled())).status).toBe(403);
  }
  for (const key of ['ACCESS_HOST', 'ACCESS_AUD', 'ACCESS_TEAM_DOMAIN', 'ACCESS_EMAILS']) {
    expect((await worker.fetch(request(assertion), { ...enabled(), [key]: '' })).status).toBe(403);
  }
  const page = await call('/app/recipients/new');
  const token = (await page.text()).match(/data-csrf="([^"]+)"/)![1];
  const post = (csrf?: string) => worker.fetch(new Request('https://calendar.test/app/recipients', {
    method: 'POST', headers: { 'Cf-Access-Jwt-Assertion': assertion, Origin: 'https://calendar.test', 'Content-Type': 'application/json', ...(csrf ? { 'X-CSRF-Token': csrf } : {}) },
    body: JSON.stringify({ ...r, name: 'Access Test', enabled: true }),
  }), enabled());
  expect((await post()).status).toBe(403);
  expect((await post(await issueCsrfToken('stranger@example.invalid', csrfSecret))).status).toBe(403);
  expect((await post(token)).status).toBe(200);
  expect((await bindings.DB.prepare("SELECT COUNT(*) n FROM recipients WHERE name='Access Test'").first())?.n).toBe(1);
});

it('signs a read-only service-account JWT, paginates and stably merges all shared calendars', async () => {
  const key = await crypto.subtle.generateKey({ name: 'RSASSA-PKCS1-v1_5', modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: 'SHA-256' }, true, ['sign', 'verify']) as CryptoKeyPair;
  const exported = new Uint8Array(await crypto.subtle.exportKey('pkcs8', key.privateKey) as ArrayBuffer);
  const credential = JSON.stringify({ type: 'service_account', client_email: 'test@example.invalid', private_key: `-----BEGIN PRIVATE KEY-----\n${btoa(String.fromCharCode(...exported))}\n-----END PRIVATE KEY-----`, token_uri: 'https://untrusted.invalid/token' });
  const calendar = new GoogleCalendar(credential);
  const pages: string[] = [];
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const url = new URL(String(input));
    if (url.hostname === 'oauth2.googleapis.com') {
      const jwt = new URLSearchParams(init!.body as URLSearchParams).get('assertion')!;
      const [header, payload, signature] = jwt.split('.');
      const decode = (s: string) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0));
      expect(JSON.parse(new TextDecoder().decode(decode(payload)))).toMatchObject({ iss: 'test@example.invalid', aud: 'https://oauth2.googleapis.com/token', scope: 'https://www.googleapis.com/auth/calendar.readonly' });
      expect(await crypto.subtle.verify('RSASSA-PKCS1-v1_5', key.publicKey, decode(signature), new TextEncoder().encode(`${header}.${payload}`))).toBe(true);
      return Response.json({ access_token: 'test-token', expires_in: 3600 });
    }
    expect(url.hostname).toBe('www.googleapis.com');
    expect(new Headers(init!.headers).get('Authorization')).toBe('Bearer test-token');
    expect(url.searchParams.get('timeMin')).toBe('2026-08-04T14:00:00.000Z');
    expect(url.searchParams.get('timeMax')).toBe('2026-08-05T14:00:00.000Z');
    expect(url.searchParams.get('singleEvents')).toBe('true');
    pages.push(url.pathname + url.searchParams.get('pageToken'));
    const id = url.pathname.includes('/first/') ? (url.searchParams.has('pageToken') ? 'second-page' : 'first-page') : 'other-calendar';
    const hour = id === 'first-page' ? '15' : id === 'second-page' ? '10' : '08';
    return Response.json({ items: [{ id, summary: id, start: { dateTime: `2026-08-05T${hour}:00:00+10:00` }, end: { dateTime: `2026-08-05T${hour}:30:00+10:00` } }], nextPageToken: id === 'first-page' ? 'page2' : undefined });
  });
  expect((await calendar.events(r, '2026-08-05')).map(e => e.id)).toEqual(['other-calendar', 'second-page', 'first-page']);
  expect(pages).toHaveLength(3);
  vi.mocked(fetch).mockResolvedValue(Response.json({}, { status: 404 }));
  await expect(calendar.verify('not-shared')).rejects.toBeInstanceOf(AccessError);
});
