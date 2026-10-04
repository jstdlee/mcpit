// Decision model client: Clef-flash on Workers AI (System One API).

export interface Question {
  type: 'noul' | 'choice' | 'score';
  instructions: string;
  criteria?: Record<string, string> | string[];
}

export interface Answer {
  type: string;
  noul?: number;
  choice?: string;
  probabilities?: Record<string, number>;
  confidence?: number;
  score?: number;
}

export interface Decider {
  model: string;
  ask(state: unknown, questions: Record<string, Question>): Promise<Record<string, Answer>>;
}

export function clefDecider(ai: Ai, modelId: string): Decider {
  const model = modelId.split('/').pop()!;
  return {
    model,
    async ask(state, questions) {
      const ids = Object.keys(questions);
      const out: Record<string, Answer> = {};
      for (let i = 0; i < ids.length; i += 64) {
        const batch = Object.fromEntries(ids.slice(i, i + 64).map((id) => [id, questions[id]]));
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const res = (await (ai as any).run(modelId, { model, state, questions: batch })) as {
          answers: Record<string, Answer>;
        };
        Object.assign(out, res.answers);
      }
      return out;
    },
  };
}

export const EFFECT_OPTIONS = {
  read: 'Only reads data; changes nothing.',
  write: 'Creates or changes data, sends a message, or adds to a cart.',
  payment: 'Pays, orders or transfers money.',
  destructive: 'Deletes data or closes an account.',
};

export const CLASS_OPTIONS = {
  confirmation: 'The candidate is the same as the active version.',
  correction: 'The active tool fails and the candidate fixes it.',
  drift: 'The site changed and the candidate matches the new site.',
  extension: 'The candidate only adds new read-only tools.',
  cosmetic: 'Only names or descriptions changed.',
  alternative: 'Both work; the candidate uses another endpoint or flow.',
  regression: 'The candidate removes working tools or breaks a tool.',
  risky: 'The candidate adds a write tool, a new domain or a wider effect.',
};
