import { addDays, at, calendarIDs } from './time';
import type { CalendarEvent, Recipient } from './types';

export class AccessError extends Error {}

export async function request(url: string, init?: RequestInit): Promise<Response> {
  return fetch(url, { ...init, signal: AbortSignal.timeout(15000) });
}

function base64url(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
}

export class GoogleCalendar {
  private token?: { value: string; expires: number };
  constructor(private credential: string) {}

  private async accessToken(): Promise<string> {
    if (this.token && this.token.expires > Date.now() + 60000) return this.token.value;
    let sa;
    try { sa = JSON.parse(this.credential); } catch { throw new AccessError('Service account JSON is missing or invalid.'); }
    if (sa.type !== 'service_account' || !sa.client_email || !sa.private_key) throw new AccessError('A service_account key is required.');
    const encode = (v: unknown) => base64url(new TextEncoder().encode(JSON.stringify(v)));
    const now = Math.floor(Date.now() / 1000);
    const assertion = `${encode({ alg: 'RS256', typ: 'JWT' })}.${encode({
      iss: sa.client_email, scope: 'https://www.googleapis.com/auth/calendar.readonly',
      aud: 'https://oauth2.googleapis.com/token', iat: now, exp: now + 3600,
    })}`;
    let key: CryptoKey;
    try {
      const pem = sa.private_key.replace(/-----[^-]+-----|\s/g, '');
      key = await crypto.subtle.importKey('pkcs8', Uint8Array.from(atob(pem), c => c.charCodeAt(0)),
        { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' }, false, ['sign']);
    } catch { throw new AccessError('Service account private key is invalid.'); }
    const signature = await crypto.subtle.sign('RSASSA-PKCS1-v1_5', key, new TextEncoder().encode(assertion));
    const response = await request('https://oauth2.googleapis.com/token', {
      method: 'POST', body: new URLSearchParams({ grant_type: 'urn:ietf:params:oauth:grant-type:jwt-bearer', assertion: `${assertion}.${base64url(new Uint8Array(signature))}` }),
    });
    if (!response.ok) {
      if (response.status >= 400 && response.status < 500 && response.status !== 429) throw new AccessError('Google token exchange rejected the service account.');
      throw new Error(`Google token exchange HTTP ${response.status}`);
    }
    const body = await response.json<{ access_token: string; expires_in: number }>();
    if (!body.access_token) throw new Error('Google token exchange returned no token.');
    this.token = { value: body.access_token, expires: Date.now() + body.expires_in * 1000 };
    return body.access_token;
  }

  private async list(id: string, params: URLSearchParams): Promise<GooglePage> {
    const response = await request(`https://www.googleapis.com/calendar/v3/calendars/${encodeURIComponent(id)}/events?${params}`, {
      headers: { Authorization: `Bearer ${await this.accessToken()}` },
    });
    if ([401, 403, 404].includes(response.status)) throw new AccessError(`Calendar ${id}: access refused (HTTP ${response.status}). Check calendar sharing and the key.`);
    if (!response.ok) throw new Error(`Google Calendar HTTP ${response.status}`);
    return response.json<GooglePage>();
  }

  async verify(id: string): Promise<void> {
    await this.list(id, new URLSearchParams({ maxResults: '1' }));
  }

  async events(r: Recipient, date: string): Promise<CalendarEvent[]> {
    const events: CalendarEvent[] = [];
    for (const id of calendarIDs(r.calendar_id)) {
      const params = new URLSearchParams({
        timeMin: at(date, '00:00', r.tz).toISOString(),
        timeMax: at(addDays(date, 1), '00:00', r.tz).toISOString(),
        singleEvents: 'true', orderBy: 'startTime', maxResults: '2500',
      });
      let pages = 0;
      do {
        const page = await this.list(id, params);
        for (const item of page.items ?? []) {
          const event = convert(item, r.tz);
          if (event) events.push(event);
        }
        if (!page.nextPageToken) break;
        if (++pages >= 100) throw new Error('Calendar pagination exceeds the household Worker limit.');
        params.set('pageToken', page.nextPageToken);
      } while (true);
    }
    return events.sort((a, b) => Date.parse(a.start) - Date.parse(b.start));
  }
}

interface GoogleEvent {
  id: string; summary?: string; description?: string; location?: string;
  start?: { date?: string; dateTime?: string }; end?: { date?: string; dateTime?: string };
  status?: string; transparency?: string; organizer?: { email: string };
  attendees?: { email: string; displayName?: string; responseStatus?: string; optional?: boolean; self?: boolean }[];
  hangoutLink?: string; htmlLink?: string; recurringEventId?: string;
}
interface GooglePage { items?: GoogleEvent[]; nextPageToken?: string }

export function convert(item: GoogleEvent, tz: string): CalendarEvent | null {
  if (item.status === 'cancelled' || item.transparency === 'transparent' || item.attendees?.some(a => a.self && a.responseStatus === 'declined')) return null;
  const resolve = (v: GoogleEvent['start']) => v?.date ? at(v.date, '00:00', tz).toISOString() : new Date(v?.dateTime ?? '').toISOString();
  try {
    return {
      id: item.id, summary: item.summary ?? '', description: item.description, location: item.location,
      start: resolve(item.start), end: resolve(item.end), all_day: !!item.start?.date,
      status: item.status, organizer: item.organizer?.email,
      attendees: item.attendees?.map(a => ({ email: a.email, display_name: a.displayName, response: a.responseStatus, optional: a.optional, self: a.self })),
      conference_url: item.hangoutLink, html_link: item.htmlLink, recurring: !!item.recurringEventId,
    };
  } catch { return null; }
}
