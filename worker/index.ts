import datastar from '../assets/static/js/datastar.js';
import { authenticateAccess, issueCsrfToken, operatorHeaders, parseEmailList, verifyCsrfToken } from '@danielmichaels/worker-kit/access';
import { deliver } from './delivery';
import { GoogleCalendar } from './google';
import { capture, digestFrom, tick, verify } from './jobs';
import { addDays, localParts, validateRecipient } from './time';
import { detail, home, recipientForm, shell } from './ui';
import type { Env, Recipient, Snapshot, Target } from './types';

function html(body: string, status = 200): Response {
  return new Response(body, { status, headers: { 'Content-Type': 'text/html; charset=utf-8' } });
}

function patch(page: string): Response {
  const main = page.slice(page.indexOf('<main'), page.indexOf('</main>') + 7);
  return new Response(`event: datastar-patch-elements\n${main.split('\n').map(line => `data: elements ${line}\n`).join('')}\n`, { headers: { 'Content-Type': 'text/event-stream' } });
}

function redirect(path: string): Response {
  return new Response(`event: datastar-patch-elements\ndata: mode append\ndata: selector body\ndata: elements <script>window.location.href=${JSON.stringify(path)};document.currentScript.remove()</script>\n\n`, { headers: { 'Content-Type': 'text/event-stream' } });
}

async function route(req: Request, env: Env): Promise<Response> {
  const url = new URL(req.url);
  const path = url.pathname.replace(/\/$/, '') || '/';
  if (req.method === 'GET' && path === '/healthz') {
    await env.DB.prepare('SELECT 1').first();
    return Response.json({ status: 'ok' });
  }
  if (req.method === 'GET' && path === '/version') return Response.json({ version: 'worker-v1', runtime: 'cloudflare-workers' });
  if (req.method === 'GET' && path === '/openapi.json') return Response.json({
    openapi: '3.1.0', info: { title: 'calendar digest', version: 'worker-v1' },
    paths: {
      '/healthz': { get: { operationId: 'healthz', summary: 'D1 connectivity', responses: { '200': { description: 'Healthy' }, '500': { description: 'D1 unavailable' } } } },
      '/version': { get: { operationId: 'version', responses: { '200': { description: 'Runtime version' } } } },
    },
  });
  if (req.method === 'GET' && path === '/docs') return html(shell('API reference', '<h1>Calendar Digest API</h1><p><a href="/openapi.json">OpenAPI specification</a></p><p>GET /healthz checks D1 connectivity. GET /version reports the runtime version.</p>', false));
  if (req.method === 'GET' && path === '/static/js/datastar.js') return new Response(datastar, { headers: { 'Content-Type': 'text/javascript' } });
  const token = path.match(/^\/d\/([^/]+)$/);
  if (req.method === 'GET' && token) {
    const s = await env.DB.prepare('SELECT * FROM digest_snapshots WHERE token=?').bind(token[1]).first<Snapshot>();
    if (!s) return new Response('Not found', { status: 404 });
    const r = await env.DB.prepare('SELECT * FROM recipients WHERE id=?').bind(s.recipient_id).first<Recipient>();
    return r ? html(detail(digestFrom(r, s), s.created_at)) : new Response('Not found', { status: 404 });
  }
  if (req.method === 'GET' && path === '/') return Response.redirect(`${url.origin}/app`, 302);
  if (req.method === 'GET' && path === '/app') return html(await home(env));
  if (req.method === 'GET' && path === '/app/recipients/new') return html(await recipientForm(env));
  const recipientPath = path.match(/^\/app\/recipients\/(\d+)(?:\/(digest-now|targets|delete))?$/);
  const r = recipientPath ? await env.DB.prepare('SELECT * FROM recipients WHERE id=?').bind(+recipientPath[1]).first<Recipient>() : null;
  if (recipientPath && !r) return new Response('Not found', { status: 404 });
  if (req.method === 'GET' && r && !recipientPath![2]) return html(await recipientForm(env, r));
  if (req.method !== 'POST') return new Response('Not found', { status: 404 });
  if (req.headers.get('Origin') !== url.origin) return new Response('Same-origin writes only.', { status: 403 });
  if (Number(req.headers.get('Content-Length') ?? 0) > 16384) return new Response('Request too large', { status: 413 });
  const google = new GoogleCalendar(env.GOOGLE_SERVICE_ACCOUNT_JSON);
  if (path === '/app/verify') {
    try { await verify(env, google); } catch { return patch(await home(env, 'Calendar verification failed. Check the credential and provider health.')); }
    return patch(await home(env, 'Verification finished. See calendar_access status above.'));
  }
  if (path === '/app/recipients' || (r && !recipientPath![2])) {
    let input: Record<string, unknown>;
    try { input = await req.json(); } catch { return new Response('Invalid JSON', { status: 400 }); }
    const value: Omit<Recipient, 'id'> = {
      name: String(input.name ?? '').trim(), calendar_id: String(input.calendar_id ?? '').trim(),
      notify_time: String(input.notify_time ?? ''), tz: String(input.tz ?? '').trim(), enabled: input.enabled === true ? 1 : 0,
    };
    try { validateRecipient(value); } catch (error) {
      return patch(await recipientForm(env, { ...value, id: r?.id ?? 0 }, error instanceof Error ? error.message : 'Invalid recipient.'));
    }
    if (r) {
      await env.DB.prepare('UPDATE recipients SET name=?,calendar_id=?,notify_time=?,tz=?,enabled=? WHERE id=?')
        .bind(value.name, value.calendar_id, value.notify_time, value.tz, value.enabled, r.id).run();
      return redirect('/app');
    }
    const created = await env.DB.prepare('INSERT INTO recipients(name,calendar_id,notify_time,tz,enabled) VALUES(?,?,?,?,?) RETURNING id')
      .bind(value.name, value.calendar_id, value.notify_time, value.tz, value.enabled).first<{ id: number }>();
    return redirect(`/app/recipients/${created!.id}`);
  }
  if (r && recipientPath![2] === 'delete') {
    await env.DB.prepare('DELETE FROM recipients WHERE id=?').bind(r.id).run();
    return redirect('/app');
  }
  if (r && recipientPath![2] === 'digest-now') {
    if (env.DELIVERY_ENABLED !== 'true') return patch(await recipientForm(env, r, 'Delivery is disabled. No digest was captured or sent.'));
    try {
      await capture(env, google, r, addDays(localParts(new Date(), r.tz).date, 1), true);
      return patch(await recipientForm(env, r, 'Calendar read succeeded. The digest is queued for the next scheduled delivery tick.', true));
    } catch { return patch(await recipientForm(env, r, 'Calendar capture failed. Check the key and sharing permissions.')); }
  }
  if (r && recipientPath![2] === 'targets') {
    const input = await req.json<{ kind: string; address: string }>();
    const key = { telegram: 'chat_id', email: 'address', sms: 'phone' }[input.kind];
    if (!key || !input.address?.trim()) return patch(await recipientForm(env, r, 'Pick a channel and give it an address.'));
    await env.DB.prepare('INSERT INTO notification_targets(recipient_id,kind,config) VALUES(?,?,?)').bind(r.id, input.kind, JSON.stringify({ [key]: input.address.trim() })).run();
    return patch(await recipientForm(env, r));
  }
  const targetPath = path.match(/^\/app\/targets\/(\d+)\/(toggle|test|delete)$/);
  if (targetPath) {
    const target = await env.DB.prepare('SELECT * FROM notification_targets WHERE id=?').bind(+targetPath[1]).first<Target>();
    if (!target) return new Response('Not found', { status: 404 });
    const owner = (await env.DB.prepare('SELECT * FROM recipients WHERE id=?').bind(target.recipient_id).first<Recipient>())!;
    if (targetPath[2] === 'delete') await env.DB.prepare('DELETE FROM notification_targets WHERE id=?').bind(target.id).run();
    if (targetPath[2] === 'toggle') await env.DB.prepare('UPDATE notification_targets SET enabled=NOT enabled WHERE id=?').bind(target.id).run();
    if (targetPath[2] === 'test') {
      const snapshot = await env.DB.prepare('SELECT * FROM digest_snapshots WHERE recipient_id=? ORDER BY digest_date DESC LIMIT 1').bind(owner.id).first<Snapshot>();
      try { await deliver(env, target, digestFrom(owner, snapshot ?? undefined)); }
      catch (error) { return patch(await recipientForm(env, owner, error instanceof Error ? error.message : 'Test failed.')); }
      return patch(await recipientForm(env, owner, 'Sent.', true));
    }
    return patch(await recipientForm(env, owner));
  }
  const retry = path.match(/^\/app\/deliveries\/(\d+)\/retry$/);
  if (retry) {
    if (env.DELIVERY_ENABLED !== 'true') return patch(await home(env, 'Delivery is disabled.'));
    await env.DB.prepare('UPDATE outbox SET attempts=0,available_at=0,last_error=NULL WHERE id=? AND sent_at IS NULL AND lease_until<=?').bind(+retry[1], Date.now()).run();
    return patch(await home(env, 'Delivery queued for the next tick.'));
  }
  return new Response('Not found', { status: 404 });
}

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    let response: Response;
    try {
      const identity = await authenticateAccess(req, {
        host: env.ACCESS_HOST, teamDomain: env.ACCESS_TEAM_DOMAIN, audience: env.ACCESS_AUD,
        allow: { emails: parseEmailList(env.ACCESS_EMAILS) },
      });
      if (!identity) response = new Response('Cloudflare Access required.', { status: 403 });
      else if (req.method === 'POST' && !await verifyCsrfToken(req, identity.email, env.ADMIN_CSRF_SECRET, req.headers.get('X-CSRF-Token'))) {
        response = new Response('Invalid or expired form token. Reload the page.', { status: 403 });
      } else {
        response = await route(req, env);
        if (/^(text\/html|text\/event-stream)/.test(response.headers.get('Content-Type') ?? '')) {
          const token = await issueCsrfToken(identity.email, env.ADMIN_CSRF_SECRET);
          response = new Response((await response.text()).replace('<main', `<main data-csrf="${token}"`), response);
        }
      }
    }
    catch { console.error('Request failed', { path: new URL(req.url).pathname.startsWith('/d/') ? '/d/[token]' : new URL(req.url).pathname }); response = new Response('Request failed.', { status: 500 }); }
    const headers = new Headers(response.headers);
    for (const [name, value] of Object.entries(operatorHeaders({ script: "'self' 'unsafe-inline' 'unsafe-eval'", style: "'unsafe-inline'" }))) headers.set(name, value);
    headers.set('Content-Security-Policy', `${headers.get('Content-Security-Policy')}; connect-src 'self'`);
    headers.set('Referrer-Policy', 'no-referrer');
    headers.set('X-Robots-Tag', 'noindex, nofollow');
    return new Response(response.body, { status: response.status, headers });
  },
  async scheduled(_controller: ScheduledController, env: Env): Promise<void> { await tick(env); },
} satisfies ExportedHandler<Env>;
