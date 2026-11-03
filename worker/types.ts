export interface Env {
  DB: D1Database;
  AUTOMATION_ENABLED: string;
  DELIVERY_ENABLED: string;
  BASE_URL: string;
  SNAPSHOT_RETENTION_DAYS: string;
  ACCESS_HOST: string;
  ACCESS_TEAM_DOMAIN: string;
  ACCESS_AUD: string;
  ACCESS_EMAILS: string;
  ADMIN_CSRF_SECRET: string;
  GOOGLE_SERVICE_ACCOUNT_JSON: string;
  TELEGRAM_BOT_TOKEN?: string;
  ALERT_TELEGRAM_CHAT_ID?: string;
  EMAIL_FROM?: string;
  CLOUDFLARE_ACCOUNT_ID?: string;
  CLOUDFLARE_API_TOKEN?: string;
}
export interface Recipient {
  id: number;
  name: string;
  calendar_id: string;
  notify_time: string;
  tz: string;
  enabled: number;
}
export interface Target {
  id: number;
  recipient_id: number;
  kind: string;
  config: string;
  enabled: number;
}
export interface CalendarEvent {
  id: string;
  summary: string;
  description?: string;
  location?: string;
  start: string;
  end: string;
  all_day: boolean;
  status?: string;
  organizer?: string;
  attendees?: { email: string; display_name?: string; response?: string; optional?: boolean; self?: boolean }[];
  conference_url?: string;
  html_link?: string;
  recurring?: boolean;
}
export interface Snapshot {
  id: number;
  recipient_id: number;
  digest_date: string;
  token: string;
  events: string;
  created_at: string;
  notified_at: string | null;
  generation: number;
}
export interface Digest {
  name: string;
  date: string;
  token: string;
  tz: string;
  events: CalendarEvent[];
}
