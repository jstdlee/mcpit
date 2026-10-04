// LLM client for the verifier (BYOK). Default: DeepSeek V4 Flash on Workers AI.
// With LLM_BASE_URL + LLM_API_KEY set, any OpenAI-compatible endpoint is used instead.

export interface LLM {
  model: string;
  complete(prompt: string): Promise<string>;
}

export interface LLMEnv {
  AI: Ai;
  LLM_MODEL?: string;
  LLM_BASE_URL?: string;
  LLM_API_KEY?: string;
}

export const DEFAULT_LLM = '@cf/deepseek-ai/deepseek-v4-flash-0731';

export function llmFromEnv(env: LLMEnv): LLM {
  const model = env.LLM_MODEL || DEFAULT_LLM;
  if (env.LLM_BASE_URL && env.LLM_API_KEY) {
    const base = env.LLM_BASE_URL.replace(/\/$/, '');
    return {
      model,
      async complete(prompt) {
        const r = await fetch(`${base}/chat/completions`, {
          method: 'POST',
          headers: { 'content-type': 'application/json', authorization: `Bearer ${env.LLM_API_KEY}` },
          body: JSON.stringify({
            model,
            messages: [{ role: 'user', content: prompt }],
            temperature: 0,
            max_tokens: 1200,
          }),
        });
        if (!r.ok) throw new Error(`LLM HTTP ${r.status}`);
        const j = (await r.json()) as { choices?: { message?: { content?: string } }[] };
        return j.choices?.[0]?.message?.content ?? '';
      },
    };
  }
  return {
    model,
    async complete(prompt) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const r = (await (env.AI as any).run(model, {
        messages: [{ role: 'user', content: prompt }],
        temperature: 0,
        max_tokens: 1200,
      })) as {
        response?: unknown;
        choices?: { message?: { content?: string } }[];
      };
      if (typeof r.response === 'string') return r.response;
      if (r.response && typeof r.response === 'object') return JSON.stringify(r.response);
      return r.choices?.[0]?.message?.content ?? '';
    },
  };
}

/** parseVerdict pulls {agree, answer, reason} out of an LLM reply. */
export function parseVerdict(text: string): { agree: boolean; answer: string; reason: string } | null {
  const m = /\{[\s\S]*\}/.exec(text);
  if (!m) return null;
  try {
    const j = JSON.parse(m[0]) as { agree?: unknown; answer?: unknown; reason?: unknown };
    if (typeof j.agree !== 'boolean') return null;
    return { agree: j.agree, answer: String(j.answer ?? ''), reason: String(j.reason ?? '').slice(0, 400) };
  } catch {
    return null;
  }
}
