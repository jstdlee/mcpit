# mcpit

Make any website usable by an agent as an MCP server, with no change to the site.

- The first visit to a site is slow: mcpit explores it (at most 2 link hops) and saves a **sitepack** — its search, forms and APIs as tools. A sitepack holds structure only, never cookies, tokens, typed values or page content.
- Later calls use the sitepack directly and are fast.
- A shared **registry** controls versions, checks every submit, and serves signed sitepacks, so other users skip the slow first visit.
- Every decision in mcpit is made by a decision model (Clef-flash on Workers AI, or local jev) through typed decision points, not by LLM reasoning.

Status: v0.1. Plan: [PLAN.md](PLAN.md) · overview page: [docs/plan.html](docs/plan.html).

## Layout

| Path | What |
|---|---|
| `cli/` | Go: the `mcpit` CLI and MCP server (one binary). Explorer over the Chrome DevTools Protocol (chromedp), HTTP executor, local store, device keys, registry client. |
| `registry/` | Cloudflare Worker + Vue 3 console, built with Vite+. D1 data, Agents SDK screening agent, Clef-flash gate. |
| `experiments/cf-models/` | Decision-model tests (Clef-flash, Clef, DeepSeek) on screening questions. |
| `legacy/extension/` | The parked MCPfier 0.1 Chrome extension prototype. |

## CLI

Build (Go 1.27+, Chrome or Chromium for exploring):

```bash
cd cli && go build -o bin/mcpit ./cmd/mcpit
```

```bash
mcpit explore https://www.example.com/          # slow, once
mcpit tools example.com
mcpit call example.com search_api --args '{"q":"lamp"}'
mcpit serve                                     # MCP server on stdio
```

Add it to an agent, for example Claude Code:

```bash
claude mcp add mcpit -- mcpit serve
```

MCP tools: `mcpit_find`, `mcpit_tools`, `mcpit_call`, `mcpit_explore`, `mcpit_submit`, `mcpit_report`. Tools that change data ask the user to confirm (MCP elicitation) and fail closed without it.

Decision model: Clef-flash on Workers AI with your own Cloudflare account (`CLOUDFLARE_ACCOUNT_ID` + `CLOUDFLARE_API_TOKEN`, or an existing `cf` CLI login). Local jev: `mcpit config set decider systemone http://127.0.0.1:8011/v1/systemone`. With no model, rule fallbacks run. Check with `mcpit doctor`.

Share a sitepack:

```bash
mcpit key init             # Ed25519 device key; a moderator approves it
mcpit submit example.com
mcpit status <submission-id>
mcpit pull example.com     # signed active version from the registry
```

## Registry

```bash
cd registry
npm install
cp .dev.vars.example .dev.vars          # set ADMIN_TOKEN; ALLOW_PRIVATE=true for local fixture sites
npm run db:migrate:local
npx vp dev                              # API + console + screening agent (Workers AI is remote)
npx vp test run && npm run check
```

The submit gate: signature and rate limits → canonical hash → repeated-submit rules (idempotent, confirmation, rejected-before) → static scan (origin rule, secrets, injection, effect) → tool diff → screening agent (replay of read probes, Clef-flash decision points) → per-tool promote, keep, reject or quarantine. The moderator console approves device keys and quarantined tools, marks sites good or bad, sets expire days and de-lists sites.

## Tests

```bash
cd cli && go test ./...            # includes a headless-Chrome explore of the fixture shop
cd registry && npx vp test run
```

## License

MIT
