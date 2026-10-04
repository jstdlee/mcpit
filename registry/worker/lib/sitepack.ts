// Sitepack format (mcpit.sitepack/1), validation and the canonical hash.
// The canonical form must match the Go client byte for byte (cli/internal/sitepack).

export const SCHEMA = 'mcpit.sitepack/1';

export interface Tool {
  id: string;
  rev?: number;
  description: string;
  kind: string;
  effect: 'read' | 'write' | 'payment' | 'destructive';
  auth: 'none' | 'session' | 'token';
  executors: string[];
  request: {
    method: string;
    url: string;
    query?: Record<string, string>;
    headers?: Record<string, string>;
    body?: unknown;
    contentType?: string;
  };
  inputSchema: Record<string, unknown>;
  steps?: { kind: string; url: string; extract: string; as: string }[];
  output: { type: string; itemsPath?: string };
  probe?: { args: Record<string, unknown>; expect: { status: number } };
  evidence?: { observed: number; confidence: number; source?: string };
}

export interface GuideDoc {
  url: string;
  text: string;
}

/** What the site publishes for crawlers and agents. Agents read it as guidance (data, not instructions). */
export interface Guide {
  description?: string;
  robots?: GuideDoc;
  llms?: GuideDoc;
  agentCard?: GuideDoc;
  apiCatalog?: GuideDoc;
  aiPlugin?: GuideDoc;
  mcp?: GuideDoc;
  sitemaps?: string[];
}

export interface Page {
  path: string;
  title?: string;
  category: string;
  source: string;
}

export const GUIDE_DOCS = ['robots', 'llms', 'agentCard', 'apiCatalog', 'aiPlugin', 'mcp'] as const;

export interface Pack {
  schema: string;
  origin: string;
  version?: string;
  fingerprint: { routes?: string[]; domHash?: string; apiHash?: string; variant?: string };
  tools: Tool[];
  guide?: Guide;
  pages?: Page[];
  provenance?: Record<string, unknown>;
  registry?: unknown;
}

const TOOL_ID = /^[a-z][a-z0-9_]{0,63}$/;
const KINDS = new Set(['api', 'form', 'search', 'read', 'flow', 'native']);
const EFFECTS = new Set(['read', 'write', 'payment', 'destructive']);
const AUTHS = new Set(['none', 'session', 'token']);
const METHODS = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE']);

export function validate(p: unknown): string[] {
  const errs: string[] = [];
  if (!p || typeof p !== 'object') return ['pack must be an object'];
  const pack = p as Pack;
  if (pack.schema !== SCHEMA) errs.push(`schema must be ${SCHEMA}`);
  let origin: URL | null = null;
  try {
    origin = new URL(pack.origin);
  } catch {
    /* handled below */
  }
  if (!origin || !['http:', 'https:'].includes(origin.protocol) || origin.origin !== pack.origin) {
    errs.push('origin must be scheme://host with no path');
  }
  if (!Array.isArray(pack.tools) || (pack.tools.length === 0 && !pack.guide)) {
    errs.push('at least one tool or a guide is required');
    return errs;
  }
  if (pack.pages !== undefined) {
    if (!Array.isArray(pack.pages) || pack.pages.length > 500) errs.push('pages must be a list of at most 500');
    else
      pack.pages.forEach((pg, i) => {
        if (typeof pg?.path !== 'string' || !pg.path.startsWith('/') || pg.path.length > 500)
          errs.push(`pages[${i}].path is invalid`);
        if (pg?.title !== undefined && (typeof pg.title !== 'string' || pg.title.length > 200))
          errs.push(`pages[${i}].title is invalid`);
      });
  }
  if (pack.guide !== undefined) {
    if (!pack.guide || typeof pack.guide !== 'object') errs.push('guide must be an object');
    else {
      for (const k of GUIDE_DOCS) {
        const d = pack.guide[k];
        if (d === undefined) continue;
        if (typeof d?.url !== 'string' || typeof d?.text !== 'string' || d.text.length > 12000)
          errs.push(`guide.${k} is invalid`);
      }
      if (typeof pack.guide.description === 'string' && pack.guide.description.length > 500)
        errs.push('guide.description is too long');
    }
  }
  if (pack.tools.length > 100) errs.push('at most 100 tools');
  const seen = new Set<string>();
  pack.tools.forEach((t, i) => {
    const at = `tools[${i}]`;
    if (!t || typeof t !== 'object') {
      errs.push(`${at} must be an object`);
      return;
    }
    if (!TOOL_ID.test(t.id)) errs.push(`${at}.id must match ${TOOL_ID}`);
    if (seen.has(t.id)) errs.push(`${at}.id is a duplicate`);
    seen.add(t.id);
    if (!KINDS.has(t.kind)) errs.push(`${at}.kind is invalid`);
    if (!EFFECTS.has(t.effect)) errs.push(`${at}.effect is invalid`);
    if (!AUTHS.has(t.auth)) errs.push(`${at}.auth is invalid`);
    if (!t.request || !METHODS.has(t.request.method)) errs.push(`${at}.request.method is invalid`);
    if (!t.request?.url) errs.push(`${at}.request.url is required`);
    if (typeof t.description !== 'string' || t.description.length > 500)
      errs.push(`${at}.description must be a string of at most 500 characters`);
    if (!t.output || typeof t.output.type !== 'string') errs.push(`${at}.output.type is required`);
    if (!t.inputSchema || typeof t.inputSchema !== 'object') errs.push(`${at}.inputSchema is required`);
  });
  return errs;
}

/** canonicalJSON: sorted keys, no spaces; same bytes as Go CanonicalJSON (no HTML escaping). */
export function canonicalJSON(v: unknown): string {
  if (v === null || typeof v !== 'object') return JSON.stringify(v);
  if (Array.isArray(v)) return '[' + v.map(canonicalJSON).join(',') + ']';
  const o = v as Record<string, unknown>;
  const keys = Object.keys(o)
    .filter((k) => o[k] !== undefined)
    .sort(byCodePoint);
  return '{' + keys.map((k) => JSON.stringify(k) + ':' + canonicalJSON(o[k])).join(',') + '}';
}

// Go sorts map keys by bytes (UTF-8); this matches for all keys we use.
function byCodePoint(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/** Remove volatile fields (version, provenance, registry, tools[].evidence, tools[].rev); sort tools by id. */
export function canonicalPack(p: Pack): string {
  const c = structuredClone(p) as unknown as Record<string, unknown>;
  delete c.version;
  delete c.provenance;
  delete c.registry;
  const tools = (c.tools as Record<string, unknown>[]).map((t) => {
    const x = { ...t };
    delete x.evidence;
    delete x.rev;
    return x;
  });
  tools.sort((a, b) => byCodePoint(String(a.id), String(b.id)));
  c.tools = tools;
  return canonicalJSON(c);
}

export async function sha256hex(s: string | Uint8Array): Promise<string> {
  const data = typeof s === 'string' ? new TextEncoder().encode(s) : s;
  const d = await crypto.subtle.digest('SHA-256', data as BufferSource);
  return [...new Uint8Array(d)].map((b) => b.toString(16).padStart(2, '0')).join('');
}

export async function packHash(p: Pack): Promise<string> {
  return sha256hex(canonicalPack(p));
}

/** Canonical form of one tool, for tool-level diffs. */
export function canonicalTool(t: Tool): string {
  const x = { ...t } as Record<string, unknown>;
  delete x.evidence;
  delete x.rev;
  return canonicalJSON(x);
}
