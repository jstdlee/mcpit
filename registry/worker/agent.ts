// ScreeningAgent: one Agents SDK instance per site. It queues screening and
// re-verification tasks durably and runs them one at a time.

import { Agent } from 'agents';
import type { Env } from './env';
import { clefDecider } from './lib/clef';
import { llmFromEnv } from './lib/llm';
import { reverify, screenSubmission } from './screen';
import { verifySubmission } from './verify';

export class ScreeningAgent extends Agent<Env> {
  async enqueueScreen(submissionId: string): Promise<string> {
    return this.queue('runScreen', { id: submissionId }, { id: 'screen:' + submissionId });
  }

  async enqueueReverify(origin: string): Promise<string> {
    return this.queue('runReverify', { origin }, { id: 'reverify:' + origin });
  }

  async runScreen(p: { id: string }) {
    await screenSubmission(this.env, p.id, clefDecider(this.env.AI, this.env.DECISION_MODEL));
    // The LLM verifier runs after the decision is made and never changes it.
    await this.queue('runVerify', { id: p.id }, { id: 'verify:' + p.id });
  }

  async runVerify(p: { id: string }) {
    await verifySubmission(this.env, p.id, llmFromEnv(this.env));
  }

  async runReverify(p: { origin: string }) {
    await reverify(this.env, p.origin);
  }
}
