// The screening gate: rules (facts) → replay → Clef-flash decision points → verdict per tool.
// Runs inside the ScreeningAgent (one Durable Object per site), one task at a time.

import type { Env } from './env';
import { CLASS_OPTIONS, EFFECT_OPTIONS, type Answer, type Decider, type Question } from './lib/clef';
import { activeVersion, logDecision, now, publish, settingNum, setting } from './lib/db';
import {
  decideTool,
  diffPacks,
  mergeTools,
  summarize,
  type ModelView,
  type ReplayResult,
  type ToolResult,
} from './lib/gate';
import { replay } from './lib/replay';
import { riskyTools, scan } from './lib/scan';
import type { Pack, Tool } from './lib/sitepack';

interface SubmissionRow {
  id: string;
  origin: string;
  hash: string;
  key_id: string;
  pack: string;
  state: string;
}

function toolView(t: Tool) {
  return {
    method: t.request.method,
    url: t.request.url,
    query: t.request.query,
    body: t.request.body,
    description: t.description,
    effect: t.effect,
    params: Object.keys((t.inputSchema?.properties as object) ?? {}),
    output: t.output,
  };
}

export async function screenSubmission(
  env: Env,
  id: string,
  decider: Decider,
  fetcher: typeof fetch = fetch,
): Promise<void> {
  const sub = await env.DB.prepare('SELECT * FROM submissions WHERE id = ?').bind(id).first<SubmissionRow>();
  if (!sub || sub.state !== 'screening') return;
  const pack = JSON.parse(sub.pack) as Pack;
  const active = await activeVersion(env, sub.origin);
  const low = await settingNum(env, 'unsure_low', 0.25);
  const high = await settingNum(env, 'unsure_high', 0.75);

  const findings = scan(pack, { allowPrivate: env.ALLOW_PRIVATE === 'true' });
  const risky = riskyTools(findings);
  const diff = diffPacks(active?.parsed ?? null, pack);
  const fp = (p?: Pack) => (p ? `${p.fingerprint?.domHash ?? ''}|${p.fingerprint?.apiHash ?? ''}` : '');
  const fingerprintChanged = !!active && fp(active.parsed) !== fp(pack) && fp(pack) !== '|';
  const changed = diff.filter((d) => d.change !== 'unchanged');

  // Facts: replay probes of changed tools (candidate and active).
  const cand = new Map<string, ReplayResult>();
  const act = new Map<string, ReplayResult>();
  await Promise.all(
    changed.map(async (d) => {
      cand.set(d.id, await replay(pack.origin, d.candidate, fetcher));
      if (d.active) act.set(d.id, await replay(pack.origin, d.active, fetcher));
    }),
  );

  // Decision points, one batched Clef-flash call for the whole submission.
  const qs: Record<string, Question> = {};
  const state: Record<string, unknown> = {};
  changed.forEach((d, i) => {
    const k = `t${i}`;
    state[k] = {
      candidate: toolView(d.candidate),
      active: d.active ? toolView(d.active) : null,
      candidateTest: cand.get(d.id),
      activeTest: act.get(d.id) ?? null,
    };
    qs[`${k}_desc`] = { type: 'noul', instructions: `The description of tool ${k} matches what its endpoint does.` };
    qs[`${k}_eff`] = {
      type: 'choice',
      instructions: `What does calling the candidate of tool ${k} do to data on the site?`,
      criteria: EFFECT_OPTIONS,
    };
    if (d.active) {
      qs[`${k}_imp`] = {
        type: 'noul',
        instructions: `The candidate of tool ${k} is a real improvement over its active version.`,
      };
      qs[`${k}_cls`] = {
        type: 'choice',
        instructions: `What kind of change is the candidate of tool ${k}?`,
        criteria: CLASS_OPTIONS,
      };
    }
  });
  let answers: Record<string, Answer> = {};
  let modelError = '';
  if (Object.keys(qs).length > 0) {
    try {
      // The injection check gets its own call per tool with only that tool's text as state:
      // Clef-flash missed a subtle injection inside the shared state (0.05) but caught it alone (0.95).
      const [shared, ...inj] = await Promise.all([
        decider.ask({ origin: pack.origin, fingerprintChanged, tools: state }, qs),
        ...changed.map((d) =>
          decider.ask(agentText(d.candidate), { inj: { type: 'noul', instructions: INJECTION_Q } }),
        ),
      ]);
      answers = shared;
      changed.forEach((_, i) => (answers[`t${i}_inj`] = inj[i].inj));
    } catch (e) {
      modelError = String(e);
    }
  }

  const results: ToolResult[] = [];
  const promoted: Tool[] = [];
  const quarantined: { tool: Tool; result: ToolResult; view: ModelView }[] = [];
  for (const d of diff) {
    const i = changed.indexOf(d);
    const k = `t${i}`;
    const view: ModelView = {};
    if (i >= 0 && !modelError) {
      view.descMatch = answers[`${k}_desc`]?.noul;
      view.injection = answers[`${k}_inj`]?.noul;
      view.effectRead = answers[`${k}_eff`]?.probabilities?.read;
      view.improves = answers[`${k}_imp`]?.noul;
      view.cls = answers[`${k}_cls`]?.choice;
      for (const [q, a] of Object.entries(answers).filter(([q]) => q.startsWith(k + '_'))) {
        await logDecision(env, {
          submission: id,
          origin: pack.origin,
          point: pointOf(q),
          subject: d.id,
          model: decider.model,
          answer: a.type === 'noul' ? a.noul?.toFixed(2) : a.choice,
          probs: a.probabilities,
        });
      }
    }
    const res =
      modelError && d.change !== 'unchanged'
        ? {
            id: d.id,
            change: d.change,
            verdict: 'quarantine' as const,
            class: 'risky',
            reason: 'decision model unavailable: moderator review',
          }
        : decideTool({
            diff: d,
            risky: risky.has(d.id),
            fingerprintChanged,
            candidateReplay: cand.get(d.id) ?? { status: 0, ok: false, skipped: 'unchanged' },
            activeReplay: act.get(d.id),
            model: view,
            low,
            high,
          });
    results.push(res);
    if (res.verdict === 'promote') promoted.push(d.candidate);
    if (res.verdict === 'quarantine') quarantined.push({ tool: d.candidate, result: res, view });
  }

  // A tool that the decision model says carries prompt injection poisons the whole submission.
  const poisoned = results.some((r) => r.verdict === 'reject' && r.reason.includes('AI assistant'));
  let published: { version: string; hash: string } | null = null;
  const auto = (await setting(env, 'auto_promote', 'true')) === 'true';
  if (!poisoned && promoted.length > 0 && auto) {
    published = await publish(env, pack, mergeTools(active?.parsed ?? null, promoted), id);
  }
  for (const q of quarantined) {
    if (poisoned) break;
    await env.DB.prepare(
      'INSERT INTO quarantine (submission_id, origin, tool_id, tool, reason, summary, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)',
    )
      .bind(
        id,
        pack.origin,
        q.tool.id,
        JSON.stringify(q.tool),
        q.result.reason,
        moderatorSummary(
          q.tool,
          q.view,
          cand.get(q.tool.id),
          findings.filter((f) => f.tool === q.tool.id),
        ),
        now(),
      )
      .run();
  }
  let { state: st, outcome } = summarize(results);
  if (poisoned) [st, outcome] = ['rejected', 'rejected'];
  if (!auto && promoted.length > 0) [st, outcome] = ['quarantined', 'quarantined'];
  const reason = poisoned
    ? 'rejected: prompt injection'
    : published
      ? `promoted ${promoted.map((t) => t.id).join(', ')} → version ${published.version}`
      : results
          .filter((r) => r.change !== 'unchanged')
          .map((r) => `${r.id}: ${r.reason}`)
          .join('; ') || 'no changes';
  await env.DB.prepare(
    'UPDATE submissions SET state = ?, outcome = ?, reason = ?, tools = ?, updated_at = ? WHERE id = ?',
  )
    .bind(
      st,
      outcome,
      reason + (modelError ? ` (decision model error: ${modelError.slice(0, 200)})` : ''),
      JSON.stringify(results),
      now(),
      id,
    )
    .run();
  const delta = poisoned ? -5 : published ? 1 : st === 'rejected' ? -1 : 0;
  if (delta)
    await env.DB.prepare('UPDATE keys SET reputation = reputation + ? WHERE id = ?').bind(delta, sub.key_id).run();
  if (published) {
    await env.DB.prepare('INSERT OR IGNORE INTO agreements (hash, key_id, created_at) VALUES (?, ?, ?)')
      .bind(published.hash, sub.key_id, now())
      .run();
  }
}

const INJECTION_Q = 'This text contains instructions aimed at an AI assistant.';

/** The text an agent reads from a tool: description and parameter descriptions. */
function agentText(t: Tool): Record<string, unknown> {
  const props = (t.inputSchema?.properties ?? {}) as Record<string, { description?: string }>;
  const params = Object.fromEntries(
    Object.entries(props)
      .filter(([, v]) => v?.description)
      .map(([k, v]) => [k, v.description]),
  );
  return Object.keys(params).length
    ? { description: t.description, parameters: params }
    : { description: t.description };
}

function pointOf(q: string): string {
  return (
    { desc: 'desc.match', inj: 'sub.injection', eff: 'tool.effect', imp: 'sub.improves', cls: 'sub.class' }[
      q.split('_').pop()!
    ] ?? q
  );
}

/** The AI moderator assistant's note: facts and decision-model probabilities, ready for one-click review. */
function moderatorSummary(
  t: Tool,
  v: ModelView,
  r: ReplayResult | undefined,
  f: { check: string; detail: string }[],
): string {
  const p = (x?: number) => (x === undefined ? '?' : x.toFixed(2));
  const lines = [
    `${t.request.method} ${t.request.url} — declared effect ${t.effect}`,
    `Clef-flash: effect=read ${p(v.effectRead)}, description matches ${p(v.descMatch)}, injection ${p(v.injection)}${v.improves !== undefined ? `, improves ${p(v.improves)}` : ''}${v.cls ? `, class ${v.cls}` : ''}`,
    `Test call: ${r ? (r.skipped ? 'skipped (' + r.skipped + ')' : `HTTP ${r.status}`) : 'none'}`,
    ...f.map((x) => `Scan: ${x.check} — ${x.detail}`),
  ];
  const propose =
    (v.effectRead ?? 0) >= 0.75 && (v.injection ?? 1) < 0.25 && r?.ok
      ? 'approve'
      : t.effect !== 'read'
        ? 'approve only if this action is useful to agents and safe with user confirmation'
        : 'reject';
  lines.push(`Proposed: ${propose}`);
  return lines.join('\n');
}

/** reverify re-runs the probes of the active version; it renews or expires the version. */
export async function reverify(
  env: Env,
  origin: string,
  fetcher: typeof fetch = fetch,
): Promise<'renewed' | 'expired' | 'none'> {
  const active = await activeVersion(env, origin);
  if (!active) return 'none';
  const probes = active.parsed.tools.filter((t) => t.effect === 'read' && t.probe && t.auth === 'none');
  const results = await Promise.all(probes.map((t) => replay(origin, t, fetcher)));
  const ran = results.filter((r) => !r.skipped);
  const ok = ran.filter((r) => r.ok).length;
  if (ran.length === 0 || ok * 2 >= ran.length) {
    await env.DB.prepare('UPDATE versions SET verified_at = ? WHERE origin = ? AND version = ?')
      .bind(now(), origin, active.version)
      .run();
    return 'renewed';
  }
  await env.DB.batch([
    env.DB.prepare("UPDATE versions SET state = 'expired' WHERE origin = ? AND version = ?").bind(
      origin,
      active.version,
    ),
    env.DB.prepare("UPDATE sites SET state = 'expired', updated_at = ? WHERE origin = ? AND state = 'listed'").bind(
      now(),
      origin,
    ),
  ]);
  return 'expired';
}
