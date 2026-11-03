import { escape as e, headline, safeURL, timeRange } from './delivery';
import { nextRun } from './time';
import type { Digest, Env, Recipient, Snapshot, Target } from './types';

const styles = `:root{color-scheme:light dark;font-family:system-ui,sans-serif;color:#171717;background:#fff}*{box-sizing:border-box}body{margin:0}main{max-width:800px;margin:auto;padding:48px 24px}a{color:inherit;text-underline-offset:3px}header,.row{display:flex;justify-content:space-between;align-items:baseline;gap:16px}h1{font-size:26px;letter-spacing:-.6px;margin:0 0 20px}h2{font-size:19px;margin:0 0 8px}p{line-height:1.6;margin:8px 0}small,.muted{color:#737373;font-size:14px}section{padding:24px 0;border-bottom:1px solid #e5e5e5}button,.button{font:inherit;font-size:14px;border:1px solid #d4d4d4;border-radius:6px;padding:8px 13px;cursor:pointer;background:transparent;color:inherit;text-decoration:none;display:inline-block}button[type=submit],.button{background:#171717;color:#fff}.actions{display:flex;flex-wrap:wrap;gap:8px;margin-top:16px}label{display:block;font-size:14px;font-weight:500;margin-top:22px}input:not([type=checkbox]),select{display:block;background:transparent;color:inherit;width:100%;border:1px solid #d4d4d4;border-radius:6px;padding:10px;font:inherit;margin:6px 0}.grid{display:grid;grid-template-columns:1fr 1fr;gap:20px}.badge{display:inline-block;font-size:12px;border:1px solid #d4d4d4;border-radius:4px;padding:4px 8px;margin:6px 6px 0 0}.notice{border:1px solid #fbbf24;background:#fffbeb;color:#92400e;border-radius:6px;padding:12px 16px;margin:20px 0}.error{border-color:#fca5a5;background:#fef2f2;color:#991b1b}.success{border-color:#86efac;background:#f0fdf4;color:#166534}.danger{color:#b91c1c}.event{display:grid;grid-template-columns:110px 1fr;gap:20px}.description{white-space:pre-line}footer{margin-top:32px;font-size:12px;color:#737373}form{margin:0}ul{padding-left:20px}button:hover,.button:hover{opacity:.75}button:focus-visible,a:focus-visible,input:focus-visible,select:focus-visible{outline:3px solid #0d9488;outline-offset:3px}@media(max-width:560px){main{padding:28px 20px}.grid{grid-template-columns:1fr;gap:0}.event{grid-template-columns:1fr;gap:4px}header{align-items:start}h1{font-size:24px}}@media(prefers-color-scheme:dark){:root{color:#f5f5f5;background:#0a0a0a}section{border-color:#262626}input:not([type=checkbox]),select,button,.badge{border-color:#525252}button[type=submit],.button{background:#f5f5f5;color:#171717}.notice{background:#291c05;color:#fde68a;border-color:#92400e}.error{background:#350a0a;color:#fecaca;border-color:#7f1d1d}.success{background:#052e16;color:#bbf7d0;border-color:#166534}.danger{color:#fca5a5}}`;

export function shell(title: string, body: string, admin = true): string {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow"><meta name="color-scheme" content="light dark"><title>${e(title)}</title><style>${styles}</style>${admin ? '<script type="module" src="/static/js/datastar.js"></script>' : ''}</head><body><main id="page">${body}</main></body></html>`;
}

export function message(text: string, ok = false): string {
  return text ? `<p class="notice ${ok ? 'success' : 'error'}" role="status">${e(text)}</p>` : '';
}

function postAction(path: string): string {
  return `@post('${path}', {headers: {'X-CSRF-Token': document.querySelector('main').dataset.csrf}})`;
}

export function postButton(path: string, label: string, confirm = ''): string {
  const action = `${confirm ? `if(!confirm(${JSON.stringify(confirm)})) return; ` : ''}${postAction(path)}`;
  return `<button type="button" data-on:click="${e(action)}">${e(label)}</button>`;
}

export async function home(env: Env, status = ''): Promise<string> {
  const [rs, ts, ss, states, jobs] = await env.DB.batch([
    env.DB.prepare('SELECT * FROM recipients ORDER BY name'),
    env.DB.prepare('SELECT * FROM notification_targets ORDER BY kind,id'),
    env.DB.prepare('SELECT id,recipient_id,digest_date,token,created_at,notified_at,generation FROM digest_snapshots s WHERE digest_date=(SELECT MAX(digest_date) FROM digest_snapshots WHERE recipient_id=s.recipient_id)'),
    env.DB.prepare('SELECT * FROM app_state'),
    env.DB.prepare('SELECT id,snapshot_id,target_id,attempts,max_attempts,last_error FROM outbox WHERE sent_at IS NULL ORDER BY id LIMIT 100'),
  ]);
  const snapshots = ss.results as unknown as Snapshot[];
  const targets = ts.results as unknown as Target[];
  let body = `<header><h1>calendar digest</h1><a class="button" href="/app/recipients/new">Add recipient</a></header>${message(status, true)}`;
  if (env.AUTOMATION_ENABLED !== 'true' || env.DELIVERY_ENABLED !== 'true') body += '<p class="notice">Automation or delivery is disabled. This Worker is not the active sender.</p>';
  const flags = states.results as { key: string; value: string; updated_at: string }[];
  body += `<p class="muted">${flags.map(s => `${e(s.key)}: ${e(s.value)} (${e(s.updated_at)})`).join('<br>') || 'No calendar checks or scheduled ticks recorded.'}</p>`;
  body += `<div class="actions">${postButton('/app/verify', 'Verify calendar access')}</div>`;
  for (const r of rs.results as unknown as Recipient[]) {
    const s = snapshots.find(s => s.recipient_id === r.id);
    let schedule: string;
    try { schedule = `Next run ${nextRun(new Date(), r)}`; } catch { schedule = 'Invalid schedule. This recipient will not receive digests.'; }
    body += `<section><div class="row"><h2>${e(r.name)} ${r.enabled ? '' : '<small>paused</small>'}</h2><a href="/app/recipients/${r.id}">Edit</a></div><p class="muted">Daily at ${e(r.notify_time)} · ${e(r.tz)}</p><p class="muted">${e(schedule)}</p>`;
    const channels = targets.filter(t => t.recipient_id === r.id);
    body += channels.length ? channels.map(t => `<span class="badge">${e(t.kind)} ${e(address(t))}${t.enabled ? '' : ' — off'}</span>`).join('') : '<p class="notice">No channels, so this recipient receives nothing.</p>';
    if (s) body += `<p class="muted">Last digest <a href="/d/${e(s.token)}">${e(s.digest_date)}</a> — ${s.notified_at ? 'delivered' : 'not yet delivered'}</p>`;
    body += '</section>';
  }
  if (!rs.results.length) body += '<p>No recipients configured yet.</p>';
  const stale = await env.DB.prepare(`SELECT s.digest_date,s.token,r.name FROM digest_snapshots s JOIN recipients r ON r.id=s.recipient_id
    WHERE s.notified_at IS NULL AND datetime(s.created_at)<datetime(?) ORDER BY s.created_at LIMIT 100`).bind(new Date(Date.now() - 3600000).toISOString()).all<{ digest_date: string; token: string; name: string }>();
  for (const s of stale.results) body += `<p class="notice error" role="alert">${e(s.name)}'s digest for <a href="/d/${e(s.token)}">${e(s.digest_date)}</a> reached nobody. Captured more than one hour ago.</p>`;
  if (jobs.results.length) {
    body += '<section><h2>Pending and failed deliveries</h2><p class="muted">Retries are per channel. Retrying can send a duplicate if a provider accepted a timed-out request.</p>';
    for (const job of jobs.results as { id: number; attempts: number; max_attempts: number; last_error: string | null }[]) {
      body += `<p>Delivery ${job.id}: ${job.attempts}/${job.max_attempts} attempts ${job.attempts >= job.max_attempts ? '— exhausted' : ''}</p><p class="muted">${e(job.last_error)}</p>${postButton(`/app/deliveries/${job.id}/retry`, 'Retry delivery')}`;
    }
    body += '</section>';
  }
  return shell('calendar digest', body + `<footer>${env.BASE_URL ? `Links use ${e(env.BASE_URL)}.` : 'BASE_URL is unset. Digests have no detail link.'}</footer>`);
}

export function address(t: Target): string {
  try { const c = JSON.parse(t.config); return c.chat_id || c.address || c.phone || ''; } catch { return '(invalid configuration)'; }
}

export async function recipientForm(env: Env, r?: Recipient, status = '', ok = false): Promise<string> {
  const editing = !!r?.id;
  const value = r ?? { id: 0, name: '', calendar_id: '', notify_time: '21:00', tz: 'Australia/Brisbane', enabled: 1 };
  const signals = { name: value.name, calendar_id: value.calendar_id, notify_time: value.notify_time, tz: value.tz, enabled: !!value.enabled, kind: 'telegram', address: '' };
  const input = (label: string, key: keyof typeof value, type = 'text') => `<label>${label}<input type="${type}" data-bind="${key}" value="${e(value[key])}"></label>`;
  let body = `<a href="/app">← All recipients</a>${message(status, ok)}<div data-signals="${e(JSON.stringify(signals))}"><form data-on:submit__prevent="${e(postAction(`/app/recipients${editing ? `/${r!.id}` : ''}`))}"><h1 style="margin-top:24px">${editing ? e(r!.name) : 'Add recipient'}</h1>${input('Name', 'name')}${input('Calendar IDs', 'calendar_id')}<small>Comma-separated calendars shared with the service account.</small><div class="grid">${input('Notify time', 'notify_time', 'time')}${input('Timezone', 'tz')}</div><small>The digest covers the next local day.</small><label><input type="checkbox" data-bind="enabled" ${value.enabled ? 'checked' : ''}> Enabled</label><div class="actions"><button type="submit">${editing ? 'Save' : 'Add recipient'}</button></div></form>`;
  if (r && editing) {
    const targets = await env.DB.prepare('SELECT * FROM notification_targets WHERE recipient_id=? ORDER BY kind,id').bind(r.id).all<Target>();
    body += `<section>${postButton(`/app/recipients/${r.id}/digest-now`, 'Send calendar digest now')}<p class="muted">Refreshes tomorrow’s events and queues every enabled channel, even if already sent.</p></section><section><h2>Channels</h2>`;
    if (!targets.results.length) body += '<p class="notice">None yet, so this recipient receives nothing.</p>';
    for (const t of targets.results) body += `<section><p><strong>${e(t.kind)}</strong> ${e(address(t))} ${t.enabled ? '' : '— off'}</p><div class="actions">${postButton(`/app/targets/${t.id}/toggle`, t.enabled ? 'Disable' : 'Enable')}${postButton(`/app/targets/${t.id}/test`, 'Send test')}${postButton(`/app/targets/${t.id}/delete`, 'Remove', 'Remove this channel?')}</div></section>`;
    body += `<form data-on:submit__prevent="${e(postAction(`/app/recipients/${r.id}/targets`))}"><div class="grid"><label>Channel<select data-bind="kind"><option value="telegram">Telegram</option><option value="email">Email</option><option value="sms">SMS</option></select></label><label>Chat ID, email address or phone number<input data-bind="address" autocomplete="off"></label></div><button type="submit">Add channel</button></form><p class="muted">SMS is not implemented and will not be marked delivered.</p></section><div class="actions">${postButton(`/app/recipients/${r.id}/delete`, 'Remove this recipient', 'Remove this recipient and all their digests? This cannot be undone.')}</div>`;
  }
  return shell(editing ? r!.name : 'Add recipient', body + '</div>');
}

export function detail(d: Digest, captured: string): string {
  let body = `<p class="muted">${e(d.name)} · ${e(d.tz)}</p><h1>${e(headline(d.date))}</h1><p class="muted">${d.events.length ? `${d.events.length} event${d.events.length === 1 ? '' : 's'}` : 'Nothing on.'}</p>`;
  for (const ev of d.events) {
    const link = (url: string | undefined, title: string) => safeURL(url) ? `<p><a href="${safeURL(url)}">${title}</a></p>` : '';
    body += `<section class="event"><p class="muted">${e(timeRange(ev, d.tz))}</p><div><h2>${e(ev.summary)} ${ev.status === 'tentative' ? '<small>tentative</small>' : ''} ${ev.recurring ? '<small>repeats</small>' : ''}</h2>${ev.location ? `<p>${e(ev.location)}</p>` : ''}${link(ev.conference_url, 'Join meeting')}${ev.description ? `<p class="description">${e(ev.description)}</p>` : ''}${ev.attendees?.length ? `<ul>${ev.attendees.map(a => `<li>${e(a.display_name || a.email)} — ${e(a.response)}${a.optional ? ' (optional)' : ''}</li>`).join('')}</ul>` : ''}${ev.organizer ? `<p class="muted">Organiser: ${e(ev.organizer)}</p>` : ''}${link(ev.html_link, 'Open in Google Calendar')}</div></section>`;
  }
  return shell(`${d.name} — ${headline(d.date)}`, body + `<footer>Captured ${e(captured)}. This is what the calendar said at the time; it may have changed since.</footer>`, false);
}
