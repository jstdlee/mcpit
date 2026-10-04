// ScreeningAgent: one Agents SDK instance per site. It queues screening and
// re-verification tasks durably and runs them one at a time.

import { Agent } from 'agents';
import type { Env } from './env';
import { clefDecider } from './lib/clef';
import { reverify, screenSubmission } from './screen';

export class ScreeningAgent extends Agent<Env> {
  async enqueueScreen(submissionId: string): Promise<string> {
    return this.queue('runScreen', { id: submissionId }, { id: 'screen:' + submissionId });
  }

  async enqueueReverify(origin: string): Promise<string> {
    return this.queue('runReverify', { origin }, { id: 'reverify:' + origin });
  }

  async runScreen(p: { id: string }) {
    await screenSubmission(this.env, p.id, clefDecider(this.env.AI, this.env.DECISION_MODEL));
  }

  async runReverify(p: { origin: string }) {
    await reverify(this.env, p.origin);
  }
}
