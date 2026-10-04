// mcpit registry Worker: public API, signed device-key API, moderator API, cron.

import { getAgentByName } from 'agents';
import type { Env } from './env';
import { ScreeningAgent } from './agent';
import { b64, keyId, signingString, timingSafeEqual, verifyEd25519 } from './lib/crypto';
import { activeVersion, audit, now, publish, registryKey, settingNum } from './lib/db';
import { diffPacks, mergeTools } from './lib/gate';
import { hard, scan } from './lib/scan';
import { packHash, validate, type Pack, type Tool } from './lib/sitepack';

export { ScreeningAgent };

class HttpError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

const json = (v: unknown, status = 200) =>
  new Response(JSON.stringify(v), {
    status,
    headers: { 'content-type': 'application/json; charset=utf-8', 'access-control-allow-origin': '*' },
  });

type Handler = (req: Request, env: Env, p: string[], ctx: ExecutionContext) => Promise<Response>;
const routes: [string, RegExp, Handler][] = [];
const route = (method: string, pattern: string, h: Handler) =>
  routes.push([method, new RegExp('^' + pattern.replace(/:[a-z]+/g, '([^/]+)') + '$'), h]);

export default {
  async fetch(req: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(req.url);
    if (req.method === 'OPTIONS') {
      return new Response(null, {
        headers: {
          'access-control-allow-origin': '*',
          'access-control-allow-methods': 'GET,POST,PUT',
          'access-control-allow-headers': 'content-type,authorization,mcpit-key,mcpit-ts,mcpit-sig',
        },
      });
    }
    for (const [method, re, h] of routes) {
      const m = re.exec(url.pathname);
      if (m && method === req.method) {
        try {
          return await h(req, env, m.slice(1).map(decodeURIComponent), ctx);
        } catch (e) {
          if (e instanceof HttpError) return json({ error: e.message }, e.status);
          console.error(e);
          return json({ error: 'internal error' }, 500);
        }
      }
    }
    if (url.pathname.startsWith('/v1/') || url.pathname.startsWith('/.well-known/'))
      return json({ error: 'not found' }, 404);
    return env.ASSETS.fetch(req);
  },

  async scheduled(_ev: ScheduledController, env: Env, ctx: ExecutionContext) {
    ctx.waitUntil(cron(env));
  },
} satisfies ExportedHandler<Env>;

// ---------- public ----------

route('GET', '/v1/health', async (_r, env) => {
  const key = await registryKey(env);
  return json({ ok: true, model: env.DECISION_MODEL, signingKey: key.id, devKey: key.dev });
});

route('GET', '/.well-known/mcpit-keys.json', async (_r, env) => {
  const key = await registryKey(env);
  return json({ keys: [{ id: key.id, alg: 'Ed25519', public: key.publicB64 }] });
});

route('GET', '/v1/sites', async (req, env) => {
  const u = new URL(req.url);
  const q = (u.searchParams.get('q') ?? '').toLowerCase();
  const sort = u.searchParams.get('sort') ?? 'used';
  const since = new Date(Date.now() - 7 * 864e5).toISOString().slice(0, 10);
  const rows = await env.DB.prepare(
    `SELECT s.*, v.hash, v.verified_at, v.pack,
       (SELECT COUNT(*) FROM agreements a WHERE a.hash = v.hash) AS agreements,
       (SELECT COALESCE(SUM(ok + fail), 0) FROM counters_daily c WHERE c.origin = s.origin AND c.day >= ?) AS week
     FROM sites s LEFT JOIN versions v ON v.origin = s.origin AND v.version = s.active_version
     WHERE s.state != 'delisted' AND COALESCE(s.verdict, '') != 'bad' AND s.active_version IS NOT NULL`,
  )
    .bind(since)
    .all<SiteRow>();
  let sites = rows.results.filter((r) => !q || r.origin.toLowerCase().includes(q)).map(siteSummary);
  const by: Record<string, (a: SiteSummary, b: SiteSummary) => number> = {
    used: (a, b) => b.calls - a.calls,
    stars: (a, b) => b.stars - a.stars,
    trust: (a, b) => b.trust - a.trust,
    trend: (a, b) => b.week - a.week,
    new: (a, b) => (b.created_at > a.created_at ? 1 : -1),
  };
  sites = sites.sort(by[sort] ?? by.used).slice(0, 200);
  return json({ sites });
});

route('GET', '/v1/sites/:origin', async (_r, env, [origin]) => {
  const site = await env.DB.prepare('SELECT state, verdict FROM sites WHERE origin = ?')
    .bind(origin)
    .first<{ state: string; verdict: string | null }>();
  if (!site || site.state === 'delisted' || site.verdict === 'bad')
    throw new HttpError(404, 'site not in the registry');
  const v = await activeVersion(env, origin);
  if (!v) throw new HttpError(404, 'site has no active version');
  return json({
    pack: v.parsed,
    hash: v.hash,
    signature: v.signature,
    keyId: v.key_id,
    version: v.version,
    state: site.state === 'expired' ? 'expired' : 'active',
    verifiedAt: v.verified_at,
  });
});

route('GET', '/v1/sites/:origin/info', async (_r, env, [origin]) => {
  const site = await env.DB.prepare('SELECT * FROM sites WHERE origin = ?').bind(origin).first<SiteRow>();
  if (!site) throw new HttpError(404, 'unknown site');
  const versions = await env.DB.prepare(
    'SELECT version, hash, state, verified_at, created_at, from_submission FROM versions WHERE origin = ? ORDER BY created_at DESC LIMIT 20',
  )
    .bind(origin)
    .all();
  const active = await activeVersion(env, origin);
  const subs = await env.DB.prepare(
    'SELECT id, state, outcome, reason, created_at FROM submissions WHERE origin = ? ORDER BY created_at DESC LIMIT 20',
  )
    .bind(origin)
    .all();
  const counters = await env.DB.prepare(
    'SELECT day, SUM(ok) AS ok, SUM(fail) AS fail FROM counters_daily WHERE origin = ? GROUP BY day ORDER BY day DESC LIMIT 30',
  )
    .bind(origin)
    .all();
  const agreements = active
    ? await env.DB.prepare('SELECT COUNT(*) AS n FROM agreements WHERE hash = ?')
        .bind(active.hash)
        .first<{ n: number }>()
    : null;
  const expireDays = site.expire_days ?? (await settingNum(env, 'expire_days', 60));
  return json({
    site: {
      ...siteSummary({
        ...site,
        agreements: agreements?.n ?? 0,
        week: 0,
        verified_at: active?.verified_at ?? null,
        pack: active?.pack ?? null,
        hash: active?.hash ?? null,
      }),
      expire_days: expireDays,
      delist_reason: site.delist_reason,
    },
    tools: active?.parsed.tools ?? [],
    versions: versions.results,
    submissions: subs.results,
    counters: counters.results,
  });
});

route('GET', '/v1/submissions/:id', async (_r, env, [id]) => {
  const s = await env.DB.prepare(
    'SELECT id, origin, hash, state, outcome, reason, tools, created_at, updated_at FROM submissions WHERE id = ?',
  )
    .bind(id)
    .first<Record<string, string>>();
  if (!s) throw new HttpError(404, 'unknown submission');
  return json({ ...s, tools: s.tools ? JSON.parse(s.tools) : null });
});

route('POST', '/v1/reports', async (req, env) => {
  const r = (await req.json().catch(() => null)) as { origin?: string; tool?: string; ok?: boolean } | null;
  if (!r?.origin || !r.tool || typeof r.ok !== 'boolean') throw new HttpError(400, 'origin, tool and ok are required');
  const site = await env.DB.prepare('SELECT origin FROM sites WHERE origin = ?').bind(r.origin).first();
  if (!site) return json({ ok: true });
  const day = now().slice(0, 10);
  const [ok, fail] = r.ok ? [1, 0] : [0, 1];
  await env.DB.batch([
    env.DB.prepare(
      'INSERT INTO counters_daily (origin, tool, day, ok, fail) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO UPDATE SET ok = ok + excluded.ok, fail = fail + excluded.fail',
    ).bind(r.origin, r.tool.slice(0, 64), day, ok, fail),
    env.DB.prepare('UPDATE sites SET calls = calls + 1, ok = ok + ?, fail = fail + ? WHERE origin = ?').bind(
      ok,
      fail,
      r.origin,
    ),
  ]);
  return json({ ok: true });
});

// ---------- signed (device keys) ----------

interface Signed {
  keyId: string;
  body: Uint8Array;
}

async function verifySigned(req: Request, env: Env, publicKeyB64?: string): Promise<Signed> {
  const id = req.headers.get('mcpit-key');
  const ts = req.headers.get('mcpit-ts');
  const sig = req.headers.get('mcpit-sig');
  if (!id || !ts || !sig) throw new HttpError(401, 'signed request required (mcpit-key, mcpit-ts, mcpit-sig)');
  if (Math.abs(Date.now() / 1000 - Number(ts)) > 300) throw new HttpError(401, 'timestamp too old or in the future');
  const body = new Uint8Array(await req.arrayBuffer());
  const max = await settingNum(env, 'max_pack_bytes', 524288);
  if (body.length > max) throw new HttpError(413, `body larger than ${max} bytes`);
  let pub = publicKeyB64;
  if (!pub) {
    const k = await env.DB.prepare('SELECT public_key FROM keys WHERE id = ?').bind(id).first<{ public_key: string }>();
    if (!k) throw new HttpError(401, 'unknown device key: run `mcpit key init`');
    pub = k.public_key;
  } else if ((await keyId(pub)) !== id) {
    throw new HttpError(400, 'key id does not match the public key');
  }
  const msg = await signingString(req.method, new URL(req.url).pathname, ts, body);
  if (!(await verifyEd25519(pub, msg, sig))) throw new HttpError(401, 'bad signature');
  const fresh = await env.DB.prepare('INSERT OR IGNORE INTO nonces (sig, ts) VALUES (?, ?)')
    .bind(sig, Number(ts))
    .run();
  if (!fresh.meta.changes) throw new HttpError(401, 'replayed request');
  return { keyId: id, body };
}

async function keyState(env: Env, id: string): Promise<string> {
  const k = await env.DB.prepare('SELECT state FROM keys WHERE id = ?').bind(id).first<{ state: string }>();
  return k?.state ?? 'unknown';
}

route('POST', '/v1/keys', async (req, env) => {
  const peek = (await req
    .clone()
    .json()
    .catch(() => null)) as { publicKey?: string; name?: string } | null;
  if (!peek?.publicKey) throw new HttpError(400, 'publicKey is required');
  const s = await verifySigned(req, env, peek.publicKey);
  const ip = req.headers.get('cf-connecting-ip') ?? '';
  const ipHash = ip
    ? b64.enc(await crypto.subtle.digest('SHA-256', new TextEncoder().encode('mcpit:' + ip))).slice(0, 12)
    : null;
  const sameIp = ipHash
    ? await env.DB.prepare('SELECT COUNT(*) AS n FROM keys WHERE ip_hash = ?').bind(ipHash).first<{ n: number }>()
    : null;
  const note =
    sameIp && sameIp.n > 0 ? `${sameIp.n} other key(s) from the same network` : 'first key from this network';
  await env.DB.prepare(
    'INSERT OR IGNORE INTO keys (id, public_key, name, state, note, ip_hash, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)',
  )
    .bind(s.keyId, peek.publicKey, (peek.name ?? '').slice(0, 80), 'pending', note, ipHash, now())
    .run();
  return json({ id: s.keyId, state: await keyState(env, s.keyId) });
});

route('GET', '/v1/keys/me', async (req, env) => {
  const s = await verifySigned(req, env);
  const k = await env.DB.prepare('SELECT id, state, name, reputation FROM keys WHERE id = ?').bind(s.keyId).first();
  return json(k);
});

route('POST', '/v1/submissions', async (req, env, _p, ctx) => {
  const s = await verifySigned(req, env);
  const st = await keyState(env, s.keyId);
  if (st !== 'approved') throw new HttpError(403, `device key is ${st}: a moderator must approve it first`);
  const body = JSON.parse(new TextDecoder().decode(s.body)) as { pack?: Pack };
  const pack = body.pack;
  const errs = validate(pack);
  if (errs.length) throw new HttpError(400, 'invalid sitepack: ' + errs.join('; '));
  const p = pack as Pack;
  const site = await env.DB.prepare('SELECT state, verdict FROM sites WHERE origin = ?')
    .bind(p.origin)
    .first<{ state: string; verdict: string | null }>();
  if (site?.state === 'delisted' || site?.verdict === 'bad')
    throw new HttpError(403, 'this site is closed for submissions');

  const hour = new Date(Date.now() - 3600e3).toISOString();
  const perKey = await env.DB.prepare('SELECT COUNT(*) AS n FROM submissions WHERE key_id = ? AND created_at > ?')
    .bind(s.keyId, hour)
    .first<{ n: number }>();
  if ((perKey?.n ?? 0) >= (await settingNum(env, 'submit_per_key_per_hour', 20)))
    throw new HttpError(429, 'too many submits from this key; try later');
  const perSite = await env.DB.prepare('SELECT COUNT(*) AS n FROM submissions WHERE origin = ? AND created_at > ?')
    .bind(p.origin, hour)
    .first<{ n: number }>();
  if ((perSite?.n ?? 0) >= (await settingNum(env, 'submit_per_site_per_hour', 30)))
    throw new HttpError(429, 'too many submits for this site; try later');

  const hash = await packHash(p);
  // Repeated submit rules.
  const mine = await env.DB.prepare(
    'SELECT id, state, outcome, reason FROM submissions WHERE hash = ? AND key_id = ? ORDER BY created_at DESC LIMIT 1',
  )
    .bind(hash, s.keyId)
    .first<Record<string, string>>();
  if (mine) return json({ ...mine, hash, origin: p.origin, existing: true });
  const id = crypto.randomUUID();
  const ts = now();
  const insert = (state: string, outcome: string | null, reason: string | null, diff: unknown = null) =>
    env.DB.prepare(
      'INSERT INTO submissions (id, origin, hash, key_id, pack, state, outcome, reason, diff, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
    )
      .bind(
        id,
        p.origin,
        hash,
        s.keyId,
        JSON.stringify(p),
        state,
        outcome,
        reason,
        diff ? JSON.stringify(diff) : null,
        ts,
        ts,
      )
      .run();

  const active = await activeVersion(env, p.origin);
  if (active?.hash === hash) {
    await env.DB.prepare('INSERT OR IGNORE INTO agreements (hash, key_id, created_at) VALUES (?, ?, ?)')
      .bind(hash, s.keyId, ts)
      .run();
    await insert('done', 'confirmation', 'same as the active version: +1 agreement');
    return json({ id, state: 'done', outcome: 'confirmation', hash, origin: p.origin });
  }
  const rejected = await env.DB.prepare("SELECT reason FROM submissions WHERE hash = ? AND state = 'rejected' LIMIT 1")
    .bind(hash)
    .first<{ reason: string }>();
  if (rejected) {
    await insert('rejected', 'rejected', 'rejected before: ' + rejected.reason);
    return json({
      id,
      state: 'rejected',
      outcome: 'rejected',
      reason: 'rejected before: ' + rejected.reason,
      hash,
      origin: p.origin,
    });
  }
  const findings = scan(p, { allowPrivate: env.ALLOW_PRIVATE === 'true' });
  const h = hard(findings);
  if (h.length) {
    const reason = h.map((f) => `${f.tool ?? 'pack'}: ${f.check} (${f.detail})`).join('; ');
    await insert('rejected', 'rejected', reason);
    await env.DB.prepare('UPDATE keys SET reputation = reputation - 1 WHERE id = ?').bind(s.keyId).run();
    return json({ id, state: 'rejected', outcome: 'rejected', reason, hash, origin: p.origin });
  }
  const diff = diffPacks(active?.parsed ?? null, p).map((d) => ({ id: d.id, change: d.change }));
  if (diff.every((d) => d.change === 'unchanged')) {
    await insert('done', 'confirmation', 'no tool changed (subset of the active version)');
    return json({ id, state: 'done', outcome: 'confirmation', hash, origin: p.origin });
  }
  await insert('screening', null, null, diff);
  const agent = await getAgentByName(env.SCREENER, p.origin);
  ctx.waitUntil(agent.enqueueScreen(id));
  return json({ id, state: 'screening', hash, origin: p.origin }, 202);
});

route('POST', '/v1/stars/:origin', async (req, env, [origin]) => {
  const s = await verifySigned(req, env);
  const st = await keyState(env, s.keyId);
  if (st === 'rejected' || st === 'revoked') throw new HttpError(403, 'key cannot star');
  const r = await env.DB.prepare('INSERT OR IGNORE INTO stars (origin, key_id, created_at) VALUES (?, ?, ?)')
    .bind(origin, s.keyId, now())
    .run();
  if (r.meta.changes) await env.DB.prepare('UPDATE sites SET stars = stars + 1 WHERE origin = ?').bind(origin).run();
  return json({ ok: true });
});

// ---------- moderator ----------

function admin(req: Request, env: Env) {
  const tok = (req.headers.get('authorization') ?? '').replace(/^Bearer\s+/i, '');
  if (!env.ADMIN_TOKEN || !tok || !timingSafeEqual(tok, env.ADMIN_TOKEN))
    throw new HttpError(401, 'moderator token required');
}

route('GET', '/v1/admin/overview', async (req, env) => {
  admin(req, env);
  const one = async (sql: string) => (await env.DB.prepare(sql).first<{ n: number }>())?.n ?? 0;
  const day = new Date(Date.now() - 864e5).toISOString();
  return json({
    pendingKeys: await one("SELECT COUNT(*) AS n FROM keys WHERE state = 'pending'"),
    openQuarantine: await one("SELECT COUNT(*) AS n FROM quarantine WHERE state = 'open'"),
    sites: await one('SELECT COUNT(*) AS n FROM sites'),
    submissions24h:
      (
        await env.DB.prepare('SELECT COUNT(*) AS n FROM submissions WHERE created_at > ?')
          .bind(day)
          .first<{ n: number }>()
      )?.n ?? 0,
    devKey: (await registryKey(env)).dev,
  });
});

route('GET', '/v1/admin/keys', async (req, env) => {
  admin(req, env);
  const state = new URL(req.url).searchParams.get('state');
  const rows = await env.DB.prepare(
    `SELECT k.id, k.name, k.state, k.reputation, k.note, k.created_at, k.decided_at,
      (SELECT COUNT(*) FROM submissions s WHERE s.key_id = k.id) AS submissions
     FROM keys k ${state ? 'WHERE k.state = ?' : ''} ORDER BY k.created_at DESC LIMIT 200`,
  )
    .bind(...(state ? [state] : []))
    .all();
  return json({ keys: rows.results });
});

route('POST', '/v1/admin/keys/:id', async (req, env, [id]) => {
  admin(req, env);
  const { action } = (await req.json()) as { action: string };
  const state = { approve: 'approved', reject: 'rejected', revoke: 'revoked' }[action];
  if (!state) throw new HttpError(400, 'action must be approve, reject or revoke');
  const r = await env.DB.prepare('UPDATE keys SET state = ?, decided_at = ? WHERE id = ?').bind(state, now(), id).run();
  if (!r.meta.changes) throw new HttpError(404, 'unknown key');
  await audit(env, 'moderator', 'key.' + action, id);
  return json({ id, state });
});

route('GET', '/v1/admin/quarantine', async (req, env) => {
  admin(req, env);
  const state = new URL(req.url).searchParams.get('state') ?? 'open';
  const rows = await env.DB.prepare('SELECT * FROM quarantine WHERE state = ? ORDER BY created_at DESC LIMIT 200')
    .bind(state)
    .all<Record<string, string>>();
  return json({ items: rows.results.map((r) => ({ ...r, tool: JSON.parse(r.tool) })) });
});

route('POST', '/v1/admin/quarantine/:id', async (req, env, [id]) => {
  admin(req, env);
  const { action } = (await req.json()) as { action: string };
  if (action !== 'approve' && action !== 'reject') throw new HttpError(400, 'action must be approve or reject');
  const q = await env.DB.prepare("SELECT * FROM quarantine WHERE id = ? AND state = 'open'")
    .bind(id)
    .first<Record<string, string>>();
  if (!q) throw new HttpError(404, 'no open item');
  let published = null;
  if (action === 'approve') {
    const tool = JSON.parse(q.tool) as Tool;
    const active = await activeVersion(env, q.origin);
    const sub = await env.DB.prepare('SELECT pack FROM submissions WHERE id = ?')
      .bind(q.submission_id)
      .first<{ pack: string }>();
    const base = active?.parsed ?? (JSON.parse(sub!.pack) as Pack);
    published = await publish(env, base, mergeTools(active?.parsed ?? null, [tool]), q.submission_id);
  }
  await env.DB.prepare('UPDATE quarantine SET state = ?, decided_at = ? WHERE id = ?')
    .bind(action === 'approve' ? 'approved' : 'rejected', now(), id)
    .run();
  await audit(env, 'moderator', 'quarantine.' + action, `${q.origin} ${q.tool_id}`, published);
  return json({ ok: true, published });
});

route('GET', '/v1/admin/submissions', async (req, env) => {
  admin(req, env);
  const rows = await env.DB.prepare(
    'SELECT id, origin, key_id, state, outcome, reason, created_at FROM submissions ORDER BY created_at DESC LIMIT 100',
  ).all();
  return json({ submissions: rows.results });
});

route('GET', '/v1/admin/decisions', async (req, env) => {
  admin(req, env);
  const sub = new URL(req.url).searchParams.get('submission');
  const rows = sub
    ? await env.DB.prepare('SELECT * FROM decisions WHERE submission_id = ? ORDER BY id').bind(sub).all()
    : await env.DB.prepare('SELECT * FROM decisions ORDER BY id DESC LIMIT 200').all();
  return json({ decisions: rows.results });
});

route('GET', '/v1/admin/sites', async (req, env) => {
  admin(req, env);
  const rows = await env.DB.prepare('SELECT * FROM sites ORDER BY updated_at DESC LIMIT 500').all();
  return json({ sites: rows.results });
});

route('POST', '/v1/admin/sites/:origin', async (req, env, [origin]) => {
  admin(req, env);
  const b = (await req.json()) as {
    verdict?: string | null;
    delist?: string;
    relist?: boolean;
    expire_days?: number | null;
    source?: string;
  };
  const sets: string[] = [];
  const vals: unknown[] = [];
  if ('verdict' in b) {
    if (b.verdict !== null && b.verdict !== 'good' && b.verdict !== 'bad')
      throw new HttpError(400, 'verdict must be good, bad or null');
    sets.push('verdict = ?');
    vals.push(b.verdict);
  }
  if (b.delist) {
    sets.push("state = 'delisted'", 'delist_reason = ?');
    vals.push(b.delist.slice(0, 300));
  }
  if (b.relist) sets.push("state = 'listed'", 'delist_reason = NULL');
  if ('expire_days' in b) {
    sets.push('expire_days = ?');
    vals.push(b.expire_days === null ? null : Math.max(1, Math.min(3650, Number(b.expire_days))));
  }
  if (b.source) {
    sets.push('source = ?');
    vals.push(b.source);
  }
  if (!sets.length) throw new HttpError(400, 'nothing to change');
  sets.push('updated_at = ?');
  vals.push(now());
  const r = await env.DB.prepare(`UPDATE sites SET ${sets.join(', ')} WHERE origin = ?`)
    .bind(...vals, origin)
    .run();
  if (!r.meta.changes) throw new HttpError(404, 'unknown site');
  await audit(env, 'moderator', 'site.update', origin, b);
  return json({ ok: true });
});

route('POST', '/v1/admin/sites/:origin/reverify', async (req, env, [origin], ctx) => {
  admin(req, env);
  const agent = await getAgentByName(env.SCREENER, origin);
  ctx.waitUntil(agent.enqueueReverify(origin));
  await audit(env, 'moderator', 'site.reverify', origin);
  return json({ ok: true });
});

const SETTING_KEYS = [
  'expire_days',
  'submit_per_key_per_hour',
  'submit_per_site_per_hour',
  'max_pack_bytes',
  'auto_promote',
  'unsure_low',
  'unsure_high',
  'verify_sample_percent',
];

route('GET', '/v1/admin/settings', async (req, env) => {
  admin(req, env);
  const rows = await env.DB.prepare(
    `SELECT key, value FROM settings WHERE key IN (${SETTING_KEYS.map(() => '?').join(',')})`,
  )
    .bind(...SETTING_KEYS)
    .all<{ key: string; value: string }>();
  return json({ settings: Object.fromEntries(rows.results.map((r) => [r.key, r.value])), model: env.DECISION_MODEL });
});

route('PUT', '/v1/admin/settings', async (req, env) => {
  admin(req, env);
  const b = (await req.json()) as Record<string, string>;
  const stmts = Object.entries(b)
    .filter(([k]) => SETTING_KEYS.includes(k))
    .map(([k, v]) =>
      env.DB.prepare(
        'INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value',
      ).bind(k, String(v)),
    );
  if (!stmts.length) throw new HttpError(400, 'no known settings');
  await env.DB.batch(stmts);
  await audit(env, 'moderator', 'settings.update', '', b);
  return json({ ok: true });
});

route('GET', '/v1/admin/audit', async (req, env) => {
  admin(req, env);
  const rows = await env.DB.prepare('SELECT * FROM audit ORDER BY id DESC LIMIT 200').all();
  return json({ audit: rows.results });
});

// ---------- helpers ----------

interface SiteRow {
  origin: string;
  active_version: string | null;
  state: string;
  verdict: string | null;
  source: string;
  stars: number;
  calls: number;
  ok: number;
  fail: number;
  created_at: string;
  updated_at: string;
  expire_days: number | null;
  delist_reason: string | null;
  hash: string | null;
  verified_at: string | null;
  pack: string | null;
  agreements: number;
  week: number;
}

interface SiteSummary {
  origin: string;
  version: string | null;
  state: string;
  verdict: string | null;
  source: string;
  stars: number;
  calls: number;
  successRate: number | null;
  agreements: number;
  trust: number;
  week: number;
  tools: number;
  verified_at: string | null;
  created_at: string;
}

function siteSummary(r: SiteRow): SiteSummary {
  const rate = r.calls >= 5 ? r.ok / r.calls : null;
  let trust =
    r.verdict === 'bad'
      ? 0
      : 40 +
        10 * Math.min(r.agreements ?? 0, 3) +
        30 * (rate ?? 0.5) +
        (r.verdict === 'good' ? 20 : 0) -
        (r.state === 'expired' ? 20 : 0);
  trust = Math.max(0, Math.min(100, Math.round(trust)));
  return {
    origin: r.origin,
    version: r.active_version,
    state: r.state,
    verdict: r.verdict,
    source: r.source,
    stars: r.stars,
    calls: r.calls,
    successRate: rate,
    agreements: r.agreements ?? 0,
    trust,
    week: r.week ?? 0,
    tools: r.pack ? (JSON.parse(r.pack) as Pack).tools.length : 0,
    verified_at: r.verified_at,
    created_at: r.created_at,
  };
}

async function cron(env: Env) {
  await env.DB.prepare('DELETE FROM nonces WHERE ts < ?')
    .bind(Math.floor(Date.now() / 1000) - 900)
    .run();
  const def = await settingNum(env, 'expire_days', 60);
  // Re-verify versions in the last 7 days before expiry (and overdue ones).
  const rows = await env.DB.prepare(
    `SELECT v.origin, v.verified_at, COALESCE(s.expire_days, ?) AS days FROM versions v JOIN sites s ON s.origin = v.origin
     WHERE v.state = 'active' AND s.state != 'delisted'`,
  )
    .bind(def)
    .all<{ origin: string; verified_at: string; days: number }>();
  let n = 0;
  for (const r of rows.results) {
    const age = (Date.now() - Date.parse(r.verified_at)) / 864e5;
    if (age >= r.days - 7 && n < 50) {
      const agent = await getAgentByName(env.SCREENER, r.origin);
      await agent.enqueueReverify(r.origin);
      n++;
    }
  }
}
