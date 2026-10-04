// Replay: run a read tool's probe from the registry, like the Go executor does.

import type { ReplayResult } from './gate';
import { siteOf } from './scan';
import type { Tool } from './sitepack';

const PH = /\{\{([a-zA-Z0-9_.-]+)\}\}/g;

export async function replay(
  origin: string,
  t: Tool,
  fetcher: typeof fetch = fetch,
  timeoutMs = 10000,
): Promise<ReplayResult> {
  if (t.effect !== 'read') return { status: 0, ok: false, skipped: 'not a read tool' };
  if (t.auth !== 'none') return { status: 0, ok: false, skipped: 'needs a login' };
  if (!t.probe) return { status: 0, ok: false, skipped: 'no probe' };
  if (!t.executors?.includes('http')) return { status: 0, ok: false, skipped: 'browser-only tool' };
  const vars: Record<string, string> = {};
  for (const [k, v] of Object.entries(t.probe.args ?? {})) vars[k] = String(v);
  const fill = (s: string, path = false) =>
    s.replace(PH, (_, k) => (path ? encodeURIComponent(vars[k] ?? '') : (vars[k] ?? '')));
  try {
    const cookies: string[] = [];
    for (const st of t.steps ?? []) {
      if (st.kind !== 'token') continue;
      guard(origin, st.url);
      const r = await timed(fetcher, st.url, { headers: ua() }, timeoutMs);
      const html = await r.text();
      const setCookie = r.headers.get('set-cookie');
      if (setCookie) cookies.push(setCookie.split(';')[0]);
      const name = st.extract.replace(/^input\[name=/, '').replace(/\]$/, '');
      const m = new RegExp(
        `<input[^>]*name=["']?${escapeRe(name)}["']?[^>]*value=["']([^"']*)["']|<input[^>]*value=["']([^"']*)["'][^>]*name=["']?${escapeRe(name)}["']?`,
        'i',
      ).exec(html);
      vars[st.as] = m?.[1] ?? m?.[2] ?? '';
    }
    const url = new URL(fill(t.request.url, true));
    guard(origin, url.toString());
    for (const [k, v] of Object.entries(t.request.query ?? {})) {
      const val = fill(v);
      if (v.match(PH) && val === '') continue;
      url.searchParams.set(k, val);
    }
    const init: RequestInit = {
      method: t.request.method,
      headers: { ...ua(), ...(cookies.length ? { cookie: cookies.join('; ') } : {}) },
    };
    if (t.request.method !== 'GET') {
      if (t.request.contentType === 'json') {
        init.body = fill(JSON.stringify(t.request.body ?? {}));
        (init.headers as Record<string, string>)['content-type'] = 'application/json';
      } else {
        const form = new URLSearchParams();
        for (const [k, v] of Object.entries((t.request.body as Record<string, unknown>) ?? {}))
          form.set(k, fill(String(v)));
        for (const st of t.steps ?? []) form.set(st.as, vars[st.as] ?? '');
        init.body = form.toString();
        (init.headers as Record<string, string>)['content-type'] = 'application/x-www-form-urlencoded';
      }
    }
    const res = await timed(fetcher, url.toString(), init, timeoutMs);
    const body = await res.text();
    return { status: res.status, ok: res.status === (t.probe.expect?.status ?? 200), preview: previewOf(body) };
  } catch (e) {
    return { status: 0, ok: false, skipped: /blocked|refused/.test(String(e)) ? String(e) : undefined };
  }
}

function guard(origin: string, raw: string) {
  const u = new URL(raw);
  if (siteOf(u.hostname) !== siteOf(new URL(origin).hostname)) throw new Error(`refused: ${u.hostname} is off-site`);
}

function ua() {
  return { 'user-agent': 'mcpit-registry/0.1 (+https://mcpit-registry.jstdlee.workers.dev)' };
}

async function timed(fetcher: typeof fetch, url: string, init: RequestInit, ms: number): Promise<Response> {
  const ctl = new AbortController();
  const t = setTimeout(() => ctl.abort(), ms);
  try {
    return await fetcher(url, { ...init, signal: ctl.signal, redirect: 'follow' });
  } finally {
    clearTimeout(t);
  }
}

function escapeRe(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** previewOf keeps the readable start of a response for the resp.data decision. */
export function previewOf(body: string): string {
  const t = body.trimStart().startsWith('<')
    ? body.replace(/<script[\s\S]*?<\/script>|<style[\s\S]*?<\/style>/gi, ' ').replace(/<[^>]+>/g, ' ')
    : body;
  return t.replace(/\s+/g, ' ').trim().slice(0, 1200);
}
