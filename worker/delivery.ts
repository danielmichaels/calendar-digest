import { request } from './google';
import { localParts } from './time';
import type { CalendarEvent, Digest, Env, Target } from './types';

export function escape(value: unknown): string {
  return String(value ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
}

export function safeURL(value?: string): string {
  try { const u = new URL(value ?? ''); return ['http:', 'https:'].includes(u.protocol) ? escape(u.href) : ''; } catch { return ''; }
}

export function timeRange(ev: CalendarEvent, tz: string): string {
  if (ev.all_day) return 'All day';
  return `${localParts(new Date(ev.start), tz).time.slice(0, 5)}–${localParts(new Date(ev.end), tz).time.slice(0, 5)}`;
}

export function headline(date: string): string {
  return new Intl.DateTimeFormat('en-AU', { timeZone: 'UTC', weekday: 'long', day: 'numeric', month: 'long' }).format(new Date(`${date}T12:00:00Z`));
}

export function render(d: Digest, base: string): { subject: string; telegram: string; text: string; html: string } {
  const count = d.events.length ? `${d.events.length} event${d.events.length === 1 ? '' : 's'}` : 'nothing on';
  const title = headline(d.date);
  const link = base && d.token ? `${base.replace(/\/$/, '')}/d/${d.token}` : '';
  let telegram = `${d.name} — ${title}\n\n${d.events.length ? '' : 'Nothing on.\n'}`;
  let text = `${title} — ${count}\n`;
  let html = `<h1>${escape(title)}</h1><p>${count}</p>`;
  for (const ev of d.events) {
    const summary = `${timeRange(ev, d.tz)}  ${ev.summary}${ev.status === 'tentative' ? ' (tentative)' : ''}`;
    telegram += `${timeRange(ev, d.tz)}  ${ev.summary}${ev.location ? ` — ${ev.location}` : ''}${ev.status === 'tentative' ? ' (tentative)' : ''}\n`;
    const others = ev.attendees?.filter(a => !a.self).map(a => a.display_name || a.email).join(', ');
    const description = Array.from((ev.description ?? '').replace(/\s+/g, ' ').trim());
    const details = [ev.location, description.slice(0, 200).join('').trimEnd() + (description.length > 200 ? '…' : ''),
      ev.conference_url ? `Join: ${ev.conference_url}` : '', others ? `With: ${others}` : '',
      ev.organizer ? `Organiser: ${ev.organizer}` : '', ev.recurring ? 'Repeats' : ''].filter(Boolean);
    text += `\n${summary}\n${details.map(s => `    ${s}\n`).join('')}`;
    html += `<section><h2>${escape(summary)}</h2>${details.map(s => `<p>${escape(s)}</p>`).join('')}</section>`;
  }
  if (link) {
    telegram += `\n${link}\n`;
    text += `\nFull detail: ${link}\n`;
    html += `<p><a href="${safeURL(link)}">Full detail</a></p>`;
  }
  return { subject: `${d.name} — ${title}: ${count}`, telegram, text, html };
}

export async function telegram(env: Env, chat: string, text: string): Promise<void> {
  if (!env.TELEGRAM_BOT_TOKEN || !chat) throw new Error('Telegram token or chat ID is missing.');
  let response: Response;
  try {
    response = await request(`https://api.telegram.org/bot${env.TELEGRAM_BOT_TOKEN}/sendMessage`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ chat_id: chat, text }),
    });
  } catch { throw new Error('Telegram request failed.'); }
  const body = await response.json<{ ok: boolean }>();
  if (!response.ok || !body.ok) throw new Error(`Telegram rejected message (HTTP ${response.status}).`);
}

export async function deliver(env: Env, target: Target, digest: Digest): Promise<void> {
  if (env.DELIVERY_ENABLED !== 'true') throw new Error('Delivery is disabled.');
  const config = JSON.parse(target.config);
  const body = render(digest, env.BASE_URL);
  switch (target.kind) {
    case 'telegram':
      await telegram(env, config.chat_id, body.telegram);
      return;
    case 'email': {
      if (!config.address || !env.EMAIL_FROM || !env.CLOUDFLARE_ACCOUNT_ID || !env.CLOUDFLARE_API_TOKEN) throw new Error('Cloudflare email configuration is missing.');
      const response = await request(`https://api.cloudflare.com/client/v4/accounts/${encodeURIComponent(env.CLOUDFLARE_ACCOUNT_ID)}/email/sending/send`, {
        method: 'POST', headers: { Authorization: `Bearer ${env.CLOUDFLARE_API_TOKEN}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ to: config.address, from: env.EMAIL_FROM, subject: body.subject, text: body.text, html: body.html }),
      });
      const result = await response.json<{ success: boolean; result?: { permanent_bounces?: string[] } }>();
      if (!response.ok || !result.success || result.result?.permanent_bounces?.includes(config.address)) throw new Error(`Cloudflare email rejected message (HTTP ${response.status}).`);
      return;
    }
    case 'sms': throw new Error('SMS is not implemented; no message was sent.');
    default: throw new Error('Unsupported delivery channel.');
  }
}
