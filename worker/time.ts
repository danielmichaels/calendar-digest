import type { Recipient } from './types';

export function localParts(now: Date, tz: string): { date: string; time: string } {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(now);
  const get = (type: string) => parts.find(p => p.type === type)!.value;
  return { date: `${get('year')}-${get('month')}-${get('day')}`, time: `${get('hour')}:${get('minute')}:${get('second')}` };
}

export function addDays(date: string, days: number): string {
  const d = new Date(`${date}T12:00:00Z`);
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

function offset(at: number, tz: string): number {
  const p = localParts(new Date(at), tz);
  return Date.parse(`${p.date}T${p.time}Z`) - at;
}

export function at(date: string, time: string, tz: string): Date {
  const wall = Date.parse(`${date}T${time}:00Z`);
  const firstOffset = offset(wall, tz);
  const candidate = wall - firstOffset;
  const actualOffset = offset(candidate, tz);
  // Match Go time.Date: resolve gaps and repeated times using the zone at the candidate instant.
  return new Date(wall - actualOffset);
}

export function validateRecipient(r: Omit<Recipient, 'id'>): void {
  if (!r.name.trim() || !calendarIDs(r.calendar_id).length) throw new Error('Name and calendar IDs are required.');
  if (!/^\d{1,2}:\d{2}$/.test(r.notify_time) || +r.notify_time.split(':')[0] > 23 || +r.notify_time.split(':')[1] > 59) {
    throw new Error('Notify time must be HH:MM on a 24-hour clock.');
  }
  localParts(new Date(), r.tz);
}

export function calendarIDs(value: string): string[] {
  return value.split(',').map(s => s.trim()).filter(Boolean);
}

export function dueDates(now: Date, r: Recipient): string[] {
  validateRecipient(r);
  if (!r.enabled) return [];
  const today = localParts(now, r.tz).date;
  return [addDays(today, -1), today]
    .filter(day => now >= at(day, r.notify_time.padStart(5, '0'), r.tz))
    .map(day => addDays(day, 1));
}

export function nextRun(now: Date, r: Recipient): string {
  validateRecipient(r);
  let day = localParts(now, r.tz).date;
  if (at(day, r.notify_time.padStart(5, '0'), r.tz) <= now) day = addDays(day, 1);
  return `${day} at ${r.notify_time} (${r.tz})`;
}
