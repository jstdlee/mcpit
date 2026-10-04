// Small fetch wrapper for the registry API. The moderator token lives in localStorage on this device only.

const TOKEN = 'mcpit.moderator';

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN) ?? '';
  } catch {
    return '';
  }
}

export function setToken(t: string) {
  try {
    if (t) localStorage.setItem(TOKEN, t);
    else localStorage.removeItem(TOKEN);
  } catch {
    /* private mode */
  }
}

export async function api<T = any>(path: string, init: RequestInit & { admin?: boolean } = {}): Promise<T> {
  const headers: Record<string, string> = {
    'content-type': 'application/json',
    ...(init.headers as Record<string, string>),
  };
  if (init.admin) headers.authorization = 'Bearer ' + getToken();
  const res = await fetch(path, { ...init, headers });
  const body = (await res.json().catch(() => ({}))) as { error?: string };
  if (!res.ok) throw new Error(body.error ?? `HTTP ${res.status}`);
  return body as T;
}

export const enc = (origin: string) => encodeURIComponent(origin);

export function ago(iso?: string | null): string {
  if (!iso) return '—';
  const s = (Date.now() - Date.parse(iso)) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

export const host = (origin: string) => origin.replace(/^https?:\/\//, '');
