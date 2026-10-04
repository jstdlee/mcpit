import type { ScreeningAgent } from './agent';

export interface Env {
  DB: D1Database;
  AI: Ai;
  SCREENER: DurableObjectNamespace<ScreeningAgent>;
  ASSETS: Fetcher;
  ADMIN_TOKEN?: string;
  SIGNING_KEY?: string;
  SIGNING_KEY_ID?: string;
  PUBLIC_URL: string;
  DECISION_MODEL: string;
  /** LLM verifier (BYOK): Workers AI model id, or an OpenAI-compatible endpoint + key. */
  LLM_MODEL?: string;
  LLM_BASE_URL?: string;
  LLM_API_KEY?: string;
  /** Dev only: allow localhost/private origins (for the fixture site). */
  ALLOW_PRIVATE?: string;
}
