// Tool-level diff and the promotion rules. Facts come from rules and replay; the
// decision model's answers come in as probabilities. No LLM decides anything here.

import { canonicalJSON, canonicalTool, type Pack, type Tool } from './sitepack';

export type Change = 'unchanged' | 'added' | 'changed';

export interface ToolDiff {
  id: string;
  change: Change;
  candidate: Tool;
  active?: Tool;
}

export function diffPacks(active: Pack | null, candidate: Pack): ToolDiff[] {
  const byId = new Map((active?.tools ?? []).map((t) => [t.id, t]));
  return candidate.tools.map((t) => {
    const a = byId.get(t.id);
    if (!a) return { id: t.id, change: 'added', candidate: t };
    return {
      id: t.id,
      change: canonicalTool(a) === canonicalTool(t) ? 'unchanged' : 'changed',
      candidate: t,
      active: a,
    };
  });
}

export interface ReplayResult {
  status: number; // 0 = network error / blocked
  ok: boolean;
  skipped?: string; // why no replay ran
  preview?: string; // readable start of the response
  notData?: boolean; // resp.data said: error, captcha or browser check
}

/** Answers from the decision model for one tool (probabilities 0..1). */
export interface ModelView {
  descMatch?: number; // desc.match
  injection?: number; // sub.injection
  effectRead?: number; // tool.effect == read
  improves?: number; // sub.improves
  needsHuman?: number; // sub.human
  cls?: string; // sub.class
}

export type Verdict = 'promote' | 'keep' | 'reject' | 'quarantine' | 'confirm' | 'retire';

export interface ToolResult {
  id: string;
  change: Change;
  verdict: Verdict;
  class: string;
  reason: string;
}

export interface GateInput {
  diff: ToolDiff;
  risky: boolean; // static scan risky finding (write/payment/destructive, new domain, ...)
  fingerprintChanged: boolean;
  candidateReplay: ReplayResult;
  activeReplay?: ReplayResult;
  model: ModelView;
  broken?: string; // unchanged tool whose registry test call failed twice (reason)
  coveredBy?: string; // a template tool already covers this literal URL (fact)
  repeating?: string; // one of several literal tools that differ in one path segment (fact)
  low: number; // unsure band
  high: number;
}

const unsure = (p: number | undefined, low: number, high: number) => p !== undefined && p > low && p < high;

/** decideTool turns facts and model answers into one verdict per tool. */
export function decideTool(g: GateInput): ToolResult {
  const { diff: d, model: m, low, high } = g;
  const r = (verdict: Verdict, cls: string, reason: string): ToolResult => ({
    id: d.id,
    change: d.change,
    verdict,
    class: cls,
    reason,
  });

  if (d.change === 'unchanged') {
    if (g.broken) return r('retire', 'drift', `retired: ${g.broken}`);
    if (g.coveredBy) return r('retire', 'regression', `retired: the live template tool ${g.coveredBy} covers it`);
    return r('confirm', 'confirmation', 'same as the active version');
  }
  if (g.coveredBy)
    return d.change === 'changed'
      ? r('retire', 'regression', `retired: the live template tool ${g.coveredBy} covers it`)
      : r('reject', 'regression', `covered by the template tool ${g.coveredBy}`);
  if (g.repeating) return r('quarantine', 'risky', `repeating item endpoint (${g.repeating}): review as one template`);
  if ((m.injection ?? 0) >= 0.5)
    return r('reject', 'risky', `decision model: text aimed at an AI assistant (${fmt(m.injection)})`);
  if (g.risky) return r('quarantine', 'risky', 'write, payment, destructive or unproven effect: moderator review');
  if (m.effectRead !== undefined && m.effectRead < 0.5 && d.candidate.effect === 'read') {
    return r('quarantine', 'risky', `decision model doubts the read effect (${fmt(m.effectRead)})`);
  }
  if (unsure(m.effectRead, low, high) || unsure(m.injection, low, high)) {
    return r('quarantine', 'risky', 'decision model is unsure about safety: moderator review');
  }
  const cand = g.candidateReplay;
  const canReplay = !cand.skipped;

  if (d.change === 'added') {
    if (canReplay && !cand.ok)
      return r(
        'reject',
        'regression',
        cand.notData
          ? 'test call returned an error or browser-check page (resp.data)'
          : `test call failed (HTTP ${cand.status || 'error'})`,
      );
    if ((m.descMatch ?? 1) < low)
      return r('reject', 'cosmetic', `description does not match the endpoint (${fmt(m.descMatch)})`);
    if (unsure(m.descMatch, low, high) && !canReplay)
      return r('quarantine', 'extension', 'untested tool with an unclear description');
    return r('promote', 'extension', canReplay ? 'new tool; test call passed' : `new tool; ${cand.skipped}`);
  }

  // changed
  const act = g.activeReplay;
  if (canReplay && !cand.ok)
    return r(
      'reject',
      'regression',
      cand.notData
        ? 'candidate returned an error or browser-check page (resp.data)'
        : `candidate test call failed (HTTP ${cand.status || 'error'})`,
    );
  if (act && !act.skipped && !act.ok && (!canReplay || cand.ok)) {
    const cls = g.fingerprintChanged ? 'drift' : 'correction';
    return r(
      'promote',
      cls,
      `active tool fails (${act.notData ? 'browser-check or error page' : 'HTTP ' + (act.status || 'error')}); candidate ${canReplay ? 'passes' : (cand.skipped ?? 'is untested')}`,
    );
  }
  if (isCosmetic(d)) {
    if ((m.descMatch ?? 0) >= high && (m.improves ?? 0) >= high)
      return r('promote', 'cosmetic', 'better description (decision model)');
    return r('keep', 'cosmetic', 'wording change without a clear gain');
  }
  if ((m.improves ?? 0) >= high)
    return r('promote', 'alternative', `both work; decision model says it improves (${fmt(m.improves)})`);
  if (unsure(m.improves, low, high)) return r('keep', 'alternative', 'both work; kept as an alternative');
  return r('keep', 'alternative', 'both work; active version stays');
}

function isCosmetic(d: ToolDiff): boolean {
  if (!d.active) return false;
  const a = { ...d.active, description: '' };
  const c = { ...d.candidate, description: '' };
  return canonicalTool(a) === canonicalTool(c);
}

const fmt = (p?: number) => (p === undefined ? '?' : p.toFixed(2));

/** mergeTools builds the next active tool list: active tools, with promoted tools added or replaced. */
export function mergeTools(active: Pack | null, promoted: Tool[], retired: string[] = []): Tool[] {
  const out = new Map((active?.tools ?? []).map((t) => [t.id, t]));
  for (const id of retired) out.delete(id);
  for (const t of promoted) {
    const prev = out.get(t.id);
    out.set(t.id, { ...t, rev: (prev?.rev ?? 0) + 1 });
  }
  return [...out.values()].sort((a, b) => (a.id < b.id ? -1 : 1));
}

export function summarize(results: ToolResult[]): { state: string; outcome: string } {
  const n = (v: Verdict) => results.filter((r) => r.verdict === v).length;
  const changed = results.filter((r) => r.change !== 'unchanged' || r.verdict === 'retire');
  if (changed.length === 0) return { state: 'done', outcome: 'confirmation' };
  const moved = n('promote') + n('retire');
  if (moved > 0) return { state: 'done', outcome: moved === changed.length ? 'promoted' : 'partial' };
  if (n('quarantine') > 0) return { state: 'quarantined', outcome: 'quarantined' };
  if (n('keep') > 0) return { state: 'done', outcome: 'alternative' };
  return { state: 'rejected', outcome: 'rejected' };
}

/** metaChanged reports whether the guide or the site map differs from the active version. */
export function metaChanged(active: Pack | null, candidate: Pack): boolean {
  const m = (p: Pack | null) => canonicalJSON({ guide: p?.guide ?? null, pages: p?.pages ?? null });
  return m(active) !== m(candidate) && (!!candidate.guide || !!candidate.pages?.length);
}

/** coveredBy returns the id of a template tool whose URL pattern matches this literal tool's URL.
 *  For id-like parameters the literal value must hold a digit: /products/search is not /products/{{id}}. */
export function coveredBy(t: Tool, others: Tool[]): string | undefined {
  if (/\{\{/.test(t.request.url)) return undefined;
  for (const o of others) {
    if (o.id === t.id || o.request.method !== t.request.method || !/\{\{/.test(o.request.url)) continue;
    const names = [...o.request.url.matchAll(/\{\{([^}]+)\}\}/g)].map((x) => x[1]);
    const re = new RegExp(
      '^' +
        o.request.url
          .split(/\{\{[^}]+\}\}/)
          .map((x) => x.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
          .join('([^/]+)') +
        '$',
    );
    const m = re.exec(t.request.url);
    if (!m) continue;
    const idLike = (n: string) => /(^|_)id$|Id$/.test(n);
    if (names.some((n, i) => idLike(n) && !/\d/.test(m[i + 1]))) continue;
    return o.id;
  }
  return undefined;
}
