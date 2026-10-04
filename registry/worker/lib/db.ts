// Small D1 helpers shared by the API and the screening agent.

import type { Env } from '../env';
import { generateRegistryKey, loadRegistryKey, type RegistryKey } from './crypto';
import { packHash, type Pack, type Tool } from './sitepack';

export const now = () => new Date().toISOString();

export async function setting(env: Env, key: string, def: string): Promise<string> {
  const r = await env.DB.prepare('SELECT value FROM settings WHERE key = ?').bind(key).first<{ value: string }>();
  return r?.value ?? def;
}

export async function settingNum(env: Env, key: string, def: number): Promise<number> {
  const v = Number(await setting(env, key, String(def)));
  return Number.isFinite(v) ? v : def;
}

export async function audit(env: Env, actor: string, action: string, target: string, detail: unknown = null) {
  await env.DB.prepare('INSERT INTO audit (actor, action, target, detail, created_at) VALUES (?, ?, ?, ?, ?)')
    .bind(actor, action, target, detail === null ? null : JSON.stringify(detail), now())
    .run();
}

let cachedKey: RegistryKey | null = null;

/** The registry signing key: the SIGNING_KEY secret, or (dev only) a key stored in D1. */
export async function registryKey(env: Env): Promise<RegistryKey & { dev: boolean }> {
  if (cachedKey) return { ...cachedKey, dev: !env.SIGNING_KEY };
  if (env.SIGNING_KEY) {
    cachedKey = await loadRegistryKey(env.SIGNING_KEY, env.SIGNING_KEY_ID || 'reg-1');
    return { ...cachedKey, dev: false };
  }
  let row = await env.DB.prepare("SELECT value FROM settings WHERE key = 'dev_signing_key'").first<{ value: string }>();
  if (!row) {
    const k = await generateRegistryKey();
    await env.DB.prepare("INSERT OR IGNORE INTO settings (key, value) VALUES ('dev_signing_key', ?)")
      .bind(k.pkcs8)
      .run();
    row = await env.DB.prepare("SELECT value FROM settings WHERE key = 'dev_signing_key'").first<{ value: string }>();
  }
  cachedKey = await loadRegistryKey(row!.value, 'dev-1');
  return { ...cachedKey, dev: true };
}

export interface VersionRow {
  origin: string;
  version: string;
  hash: string;
  pack: string;
  state: string;
  signature: string;
  key_id: string;
  verified_at: string;
  created_at: string;
}

export async function activeVersion(env: Env, origin: string): Promise<(VersionRow & { parsed: Pack }) | null> {
  const r = await env.DB.prepare(
    'SELECT v.* FROM versions v JOIN sites s ON s.origin = v.origin AND s.active_version = v.version WHERE v.origin = ?',
  )
    .bind(origin)
    .first<VersionRow>();
  return r ? { ...r, parsed: JSON.parse(r.pack) as Pack } : null;
}

async function nextVersion(env: Env, origin: string): Promise<string> {
  const day = new Date().toISOString().slice(0, 10);
  const r = await env.DB.prepare('SELECT COUNT(*) AS n FROM versions WHERE origin = ? AND version LIKE ?')
    .bind(origin, day + '.%')
    .first<{ n: number }>();
  return `${day}.${(r?.n ?? 0) + 1}`;
}

/** publish writes a new signed active version and supersedes the old one. */
export async function publish(
  env: Env,
  base: Pack,
  tools: Tool[],
  submissionId: string | null,
): Promise<{ version: string; hash: string }> {
  const pack: Pack = { schema: base.schema, origin: base.origin, fingerprint: base.fingerprint, tools };
  const hash = await packHash(pack);
  const version = await nextVersion(env, base.origin);
  const key = await registryKey(env);
  const signature = await key.sign(`${hash}|${pack.origin}|${version}`);
  const ts = now();
  await env.DB.batch([
    env.DB.prepare("UPDATE versions SET state = 'superseded' WHERE origin = ? AND state = 'active'").bind(pack.origin),
    env.DB.prepare(
      'INSERT INTO versions (origin, version, hash, pack, state, signature, key_id, from_submission, verified_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
    ).bind(pack.origin, version, hash, JSON.stringify(pack), 'active', signature, key.id, submissionId, ts, ts),
    env.DB.prepare(
      "INSERT INTO sites (origin, active_version, created_at, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(origin) DO UPDATE SET active_version = excluded.active_version, updated_at = excluded.updated_at, state = CASE WHEN sites.state = 'expired' THEN 'listed' ELSE sites.state END",
    ).bind(pack.origin, version, ts, ts),
  ]);
  return { version, hash };
}

export async function logDecision(
  env: Env,
  d: {
    submission?: string;
    origin?: string;
    point: string;
    subject?: string;
    model: string;
    answer?: string;
    probs?: unknown;
    action?: string;
    state?: unknown;
  },
) {
  await env.DB.prepare(
    'INSERT INTO decisions (submission_id, origin, point, subject, model, answer, probs, action, state, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
  )
    .bind(
      d.submission ?? null,
      d.origin ?? null,
      d.point,
      d.subject ?? null,
      d.model,
      d.answer ?? null,
      d.probs ? JSON.stringify(d.probs) : null,
      d.action ?? null,
      d.state ? JSON.stringify(d.state).slice(0, 4000) : null,
      now(),
    )
    .run();
}
