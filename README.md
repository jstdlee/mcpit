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

## Install

```bash
curl -fsSL https://github.com/jstdlee/mcpit/releases/latest/download/install.sh | sh      # Linux, macOS
irm https://github.com/jstdlee/mcpit/releases/latest/download/install.ps1 | iex           # Windows PowerShell
go install github.com/jstdlee/mcpit/cli/cmd/mcpit@latest                                  # Go 1.27+
```

Every push to `main` builds a release for Linux, macOS and Windows (amd64 and arm64).

- **[Install and get started](docs/INSTALL.md)** — requirements, first commands, where data lives, safety.
- **[mcpit for agents](docs/AGENTS.md)** — `mcpit setup <agent>` for omp, Claude Code, Codex, Cursor, VS Code and Gemini CLI; MCP tools; the skill; alerts.

## Get started

```bash
mcpit doctor
mcpit tools www.gutenberg.org                       # a site already in the registry
mcpit explore https://www.example.com/              # a new site: slow, once
mcpit call example.com search_api --args '{"q":"lamp"}'
mcpit setup omp                                     # add the MCP server + skill to an agent
mcpit key init && mcpit submit example.com          # share it (keys are approved automatically)
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
