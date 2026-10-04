// Static security scan: cheap rules that run at submit time, before any model call.
// A "hard" finding rejects the submission; a "risky" finding sends a tool to the moderator.

import { GUIDE_DOCS, type Pack, type Tool } from './sitepack';

export interface Finding {
  tool?: string;
  check: string;
  level: 'hard' | 'risky' | 'info';
  detail: string;
}

const PRIVATE_HOST =
  /^(localhost|.*\.local|.*\.internal|.*\.lan|127\.\d+\.\d+\.\d+|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|172\.(1[6-9]|2\d|3[01])\.\d+\.\d+|0\.0\.0\.0|\[::1\])$/i;

const SECRET_PATTERNS: [string, RegExp][] = [
  ['jwt', /eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/],
  ['api key', /\b(sk|pk|rk)_(live|test)_[A-Za-z0-9]{16,}\b/],
  ['aws key', /\bAKIA[0-9A-Z]{16}\b/],
  ['github token', /\bgh[pousr]_[A-Za-z0-9]{30,}\b/],
  ['bearer', /\bBearer\s+[A-Za-z0-9._~+/-]{20,}/],
  ['email', /\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b/],
  ['phone', /\+\d{1,3}[\s-]?\(?\d{2,4}\)?[\s-]?\d{3,4}[\s-]?\d{3,4}\b/],
];

const INJECTION =
  /(ignore (all |any )?(previous|prior|above) (instructions|prompts)|disregard (the )?(system|previous)|you are now|system prompt|as an ai (assistant|model)|send (the |your )?(cookie|session|token|password|api key)|exfiltrat|<\s*\/?\s*(system|instructions?)\s*>)/i;

const PAYMENT_WORDS =
  /(checkout|payment|purchase|order\/(submit|place|confirm)|pay\b|billing|subscribe\/confirm|card)/i;
const DESTRUCTIVE_WORDS = /(delete|remove|destroy|close[-_]?account|cancel[-_]?account|purge)/i;
const TRACKING_PARAMS = /^(utm_|gclid|fbclid|_ga|mc_eid)/i;

export function siteOf(host: string): string {
  if (/^[0-9.]+$/.test(host) || host === 'localhost') return host;
  const parts = host.split('.');
  return parts.length <= 2 ? host : parts.slice(-2).join('.');
}

export function scan(pack: Pack, opts: { allowPrivate?: boolean } = {}): Finding[] {
  const out: Finding[] = [];
  const origin = new URL(pack.origin);
  if (!opts.allowPrivate && PRIVATE_HOST.test(origin.hostname)) {
    out.push({ check: 'private-target', level: 'hard', detail: `${origin.hostname} is a private or local host` });
  }
  for (const t of pack.tools) out.push(...scanTool(pack, t, origin, opts));
  out.push(...scanGuide(pack, origin));
  return out;
}

/** The guide must come from the site, and its text must not carry obvious injections or secrets. */
function scanGuide(pack: Pack, origin: URL): Finding[] {
  const out: Finding[] = [];
  const g = pack.guide;
  if (!g) return out;
  const docs = GUIDE_DOCS.filter((k) => g[k]).map((k) => [k, g[k]!] as [string, { url: string; text: string }]);
  for (const f of g.feeds ?? []) docs.push(['feed', { url: f.url, text: f.title ?? '' }]);
  if (g.meta) docs.push(['meta', { url: pack.origin + '/', text: Object.values(g.meta).join(' ') }]);
  for (const [name, d] of docs) {
    try {
      if (siteOf(new URL(d.url).hostname) !== siteOf(origin.hostname))
        out.push({ tool: '_guide', check: 'origin-rule', level: 'hard', detail: `guide.${name} is not from the site` });
    } catch {
      out.push({ tool: '_guide', check: 'url', level: 'hard', detail: `guide.${name} has a bad URL` });
    }
    if (INJECTION.test(d.text))
      out.push({
        tool: '_guide',
        check: 'injection',
        level: 'risky',
        detail: `guide.${name} has text aimed at an AI assistant`,
      });
    for (const [kind, re] of SECRET_PATTERNS.slice(0, 5))
      if (re.test(d.text))
        out.push({
          tool: '_guide',
          check: 'secret',
          level: 'risky',
          detail: `guide.${name} looks like it holds a ${kind}`,
        });
  }
  return out;
}

function scanTool(pack: Pack, t: Tool, origin: URL, opts: { allowPrivate?: boolean }): Finding[] {
  const out: Finding[] = [];
  const add = (check: string, level: Finding['level'], detail: string) =>
    out.push({ tool: t.id, check, level, detail });

  // Origin rule: every URL the tool touches must be on the site.
  const urls = [t.request.url, ...(t.steps ?? []).map((s) => s.url)];
  for (const raw of urls) {
    let u: URL;
    try {
      u = new URL(raw.replace(/\{\{[^}]+\}\}/g, 'x'));
    } catch {
      add('url', 'hard', `bad URL ${raw}`);
      continue;
    }
    if (!['http:', 'https:'].includes(u.protocol)) add('url', 'hard', `scheme ${u.protocol} is not allowed`);
    if (siteOf(u.hostname) !== siteOf(origin.hostname))
      add('origin-rule', 'hard', `${u.hostname} is not part of ${origin.hostname}`);
    if (!opts.allowPrivate && PRIVATE_HOST.test(u.hostname)) add('private-target', 'hard', `${u.hostname} is private`);
    if (u.username || u.password) add('url', 'hard', 'credentials in URL');
  }
  if (
    /\{\{[^}]+\}\}/.test(new URL(t.request.url.replace(/\{\{[^}]+\}\}/g, 'x')).host) ||
    t.request.url.startsWith('{{')
  ) {
    add('origin-rule', 'hard', 'the host must not come from a parameter');
  }
  for (const [k, v] of Object.entries(t.request.headers ?? {})) {
    if (/^(authorization|cookie|x-api-key|x-auth-token)$/i.test(k) && !/^\{\{[^}]+\}\}$/.test(v)) {
      add('secret', 'hard', `fixed ${k} header`);
    }
  }

  // Secrets and personal data anywhere in the tool (descriptions, bodies, probes, constants).
  const text = JSON.stringify({ ...t, evidence: undefined });
  for (const [name, re] of SECRET_PATTERNS) {
    if (re.test(text)) add('secret', name === 'email' || name === 'phone' ? 'risky' : 'hard', `looks like a ${name}`);
  }

  // Prompt injection in text an agent will read.
  const prose = [
    t.description,
    ...Object.values((t.inputSchema?.properties as Record<string, { description?: string }>) ?? {}).map(
      (p) => p?.description ?? '',
    ),
  ].join(' ');
  if (INJECTION.test(prose)) add('injection', 'hard', 'instructions aimed at an AI assistant');

  // Effect check: method and words must fit the declared effect.
  const m = t.request.method;
  const where = t.request.url + ' ' + JSON.stringify(t.request.body ?? '');
  if (t.effect === 'read' && ['PUT', 'PATCH', 'DELETE'].includes(m)) add('effect', 'risky', `${m} tool marked read`);
  if (t.effect === 'read' && m === 'POST' && !isGraphQLQuery(t) && !/search|query|filter|lookup|find/i.test(where)) {
    add('effect', 'risky', 'POST tool marked read without proof');
  }
  if (t.effect !== 'payment' && PAYMENT_WORDS.test(where) && m !== 'GET')
    add('effect', 'risky', 'payment words in a non-payment tool');
  if (t.effect !== 'destructive' && DESTRUCTIVE_WORDS.test(where) && m !== 'GET')
    add('effect', 'risky', 'delete words in a non-destructive tool');
  if (t.effect !== 'read') add('effect', 'risky', `${t.effect} tool`);
  if (t.auth !== 'none') add('auth', 'info', `needs ${t.auth}; registry cannot replay it`);

  // Inputs that accept anything.
  const props = (t.inputSchema?.properties ?? {}) as Record<string, unknown>;
  if (Object.keys(props).length > 40) add('schema', 'hard', 'more than 40 parameters');
  if (t.inputSchema?.additionalProperties === true) add('schema', 'risky', 'additionalProperties is true');
  for (const k of Object.keys(t.request.query ?? {}))
    if (TRACKING_PARAMS.test(k)) add('tracking', 'info', `tracking parameter ${k}`);
  if (t.description.length < 3) add('schema', 'risky', 'no description');
  return out;
}

function isGraphQLQuery(t: Tool): boolean {
  const b = t.request.body as { query?: unknown } | undefined;
  return typeof b?.query === 'string' && /^\s*(query\b|\{)/.test(b.query);
}

export const hard = (f: Finding[]) => f.filter((x) => x.level === 'hard');
export const riskyTools = (f: Finding[]) => new Set(f.filter((x) => x.level === 'risky' && x.tool).map((x) => x.tool!));
