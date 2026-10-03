# mcpit — Plan v2

Status: draft for sign-off (2026-10-03). Replaces the MCPfier 0.1 prototype scope.

## 1. Goal

mcpit makes any website usable by an agent as an MCP server. The site does not need to support MCP.

- The first visit to a site is slow: mcpit explores the site and learns its forms, search and API endpoints.
- mcpit saves what it learns as a **sitepack** (metadata only, no user data).
- Every later visit uses the sitepack directly. It is fast.
- A user can share sitepacks in a public registry. Then other users skip the slow first visit.
- More sharing gives faster and better results for everyone.

## 2. Targets (from the brief)

| # | Target | Where in this plan |
|---|---|---|
| 1 | MCP server, CLI and Chrome extension | §4 |
| 2 | Agent integration for automatic operation | §4.5 |
| 3 | Public access | §4.6, §9 |
| 4 | Registry; or keep sitepacks local only | §5.4, §7 |
| 5 | Dashboard that ranks used and popular sites | §4.7 |
| 6 | Security scan and fact check of sitepacks | §8 |
| 7 | Explore forms, search, API endpoints, parameters; reuse them | §5, §6 |
| 8–10 | Sharing makes it faster for others; local cache makes repeat use fast | §5.1 |
| 11 | CLI studies browser history in batch and idle time | §4.2 |
| 12 | Optional jev for fast exploration and fill decisions | §6 |
| 13 | Other edge cases | §10 |

## 3. Core concept: the lookup ladder

For each call, mcpit takes the first rung that works:

1. **Native**: the site has WebMCP, `/.well-known/mcp`, OpenAPI, OpenSearch or GraphQL introspection. Use it.
2. **Local sitepack**: a verified sitepack is in the local store. Use it.
3. **Registry sitepack**: a trusted sitepack is in the registry. Pull it, scan it, use it.
4. **Explore**: no sitepack. Explore the site, build a sitepack, save it locally, offer to publish.

A failed call on rungs 2–3 (site changed) starts a small **re-explore** of only the broken tool.

## 4. Components

One TypeScript monorepo. One core library; four front ends.

```
packages/
  core/        sitepack schema, store, executor, explorer, decision engine, scanner
  mcp-server/  stdio + streamable HTTP MCP server
  cli/         mcpit command
  extension/   Chrome MV3 extension (grows from the current prototype)
  registry/    Cloudflare Worker + D1 + R2 (public API)
  dashboard/   static site on the registry Worker
```

### 4.1 MCP server (`mcpit serve`)

Agent-facing tools. The list stays small; site tools load on demand, so the agent context does not fill up.

| Tool | Use |
|---|---|
| `mcpit_find` | Find site tools by URL, domain or task text ("search flights"). |
| `mcpit_tools` | List tools of one site (from the ladder in §3). |
| `mcpit_call` | Call one site tool with parameters. |
| `mcpit_explore` | Explore a URL now (slow path). Returns progress and new tools. |
| `mcpit_read` | Read page content (main text, tables) as a fallback. |
| `mcpit_report` | Report a broken or wrong tool. |
| `mcpit_publish` | Publish a local sitepack (asks the user first). |

Option: expose each site tool as a real MCP tool (`site.example_com.search`) with `tools/list_changed`, for agents that prefer it.

### 4.2 CLI (`mcpit`)

```
mcpit explore <url> [--depth N] [--browser|--headless]
mcpit call <site> <tool> --json '{...}'
mcpit history import [--chrome|--firefox] [--since 30d]   # domains only; user reviews the list
mcpit batch [--idle] [--budget 2h] [--max-sites 50]        # explore the queue in idle time
mcpit pull <site> | mcpit push <site> | mcpit scan <site>
mcpit store ls | show | rm | export | import
mcpit serve [--stdio|--http :PORT]
mcpit doctor
```

- `history import` reads only the origin and the visit count. It never reads page contents, cookies or form data.
- It removes sensitive categories by default (banking, health, mail, intranet, `localhost`, private IPs).
- The user reviews and edits the queue before the first batch.
- `batch --idle` runs only when the machine is idle (no input, low CPU/GPU load). It obeys a time and token budget. It runs one site at a time and stops when the user comes back.
- An agent can run the CLI itself (for example, from a scheduled task) to prepare sitepacks for sites the user uses often.

### 4.3 Chrome extension

It grows from the current prototype (MCPfier 0.1).

- It runs inside the user's real, logged-in browser. This is the only good way to use sites behind a login.
- Explorer mode: it records forms, `fetch`/XHR traffic and GraphQL operations while the user (or the agent) uses the site. It records structure only, and drops values (§7).
- Executor mode: it runs browser-side tools (form fill, click, read) for steps that cannot run as a plain HTTP call.
- It connects to the local daemon on `127.0.0.1` with a token (as now).
- The popup shows: site status (native / pack / exploring), tools, trust score, "share this sitepack" switch.

### 4.4 Executors

| Executor | When |
|---|---|
| HTTP | Public API or form endpoint with replayable parameters. Fastest. |
| Headless browser (Playwright) | No login needed, but the page uses JS for tokens or rendering. |
| Extension (real browser) | A login session is needed, or the site blocks headless browsers. |

The sitepack says which executors each tool supports. The executor picks the fastest one that works.

### 4.5 Agent integration

- MCP config snippets for Claude Code, Claude desktop, Codex, Cursor and others.
- A skill file (`skills/mcpit/SKILL.md`): "before you browse a site, call `mcpit_find`".
- Write actions ask for confirmation through MCP elicitation (or fail closed if the client does not support it).
- Tool results mark page content as untrusted (`untrustedContentHint`), as in the prototype.

### 4.6 Registry (public)

- Cloudflare Worker + D1 (index, votes, stats) + R2 (sitepack files). Deploy with `cf`.
- Public read API: search, get sitepack by origin and version, stats.
- Write API: publish, report, vote. It needs a GitHub login (or a signed device key).
- Every sitepack version is immutable, signed and content-addressed (SHA-256).
- Site owners can claim their domain (DNS TXT or `/.well-known/mcpit.json`). An owner pack ranks above community packs. An owner can also opt out.

### 4.7 Dashboard

- Ranks: most called sites, most unique users, trending this week, highest success rate, newest.
- Per-site page: tools, versions, contributors, trust score, scan results, last verified time, success rate.
- Stats come from anonymous counters (site + tool + success/fail). No URLs with query strings, no parameters, no user IDs.
- Ranking resists fake votes: count one vote per verified account per site; weight by account age and history.

## 5. Sitepack (the metadata)

### 5.1 Why it makes repeat use fast

The slow part is exploration and decisions (LLM calls, probes). A sitepack stores the result. A repeat call only fills parameters and sends the request: no LLM, no page analysis.

### 5.2 Format (v1, JSON, draft)

```json
{
  "schema": "mcpit.sitepack/1",
  "origin": "https://www.example.com",
  "version": "2026-10-03.1",
  "fingerprint": { "routes": ["/search"], "domHash": "…", "apiHash": "…" },
  "tools": [{
    "name": "search_products",
    "description": "Search the product catalog by keyword.",
    "kind": "search",                      // api | form | search | read | flow | native
    "effect": "read",                      // read | write | payment | destructive
    "auth": "none",                        // none | session | token
    "executors": ["http", "headless", "extension"],
    "request": { "method": "GET", "url": "https://www.example.com/api/search",
                 "query": { "q": "{{query}}", "page": "{{page}}" } },
    "inputSchema": { "type": "object", "required": ["query"],
      "properties": { "query": {"type": "string"}, "page": {"type": "integer", "minimum": 1, "default": 1} } },
    "paramSources": { "page": "pagination", "sort": "enum:from <select name=sort>" },
    "steps": [],                           // multi-step flows: fetch CSRF token, then submit
    "output": { "type": "json", "itemsPath": "$.results[*]" },
    "fallback": { "form": "form#search", "fields": { "query": "input[name=q]" } },
    "evidence": { "observed": 12, "verifiedAt": "2026-10-03T10:00:00Z", "confidence": 0.93 }
  }],
  "provenance": { "publisher": "gh:someone", "signature": "…", "explorer": "mcpit/0.2", "decisions": "rules+jev+llm" }
}
```

### 5.3 Freshness

- Every call checks the fingerprint cheaply. A mismatch or a failed call marks the tool "stale".
- A stale tool re-explores only that tool. The new version goes to the local store, and to the registry if the user shares.
- Each tool keeps a success rate. Below a limit, mcpit stops using it and reports it.

### 5.4 Local-only mode

- Local-only is the default. Sharing is opt-in per site, and per sitepack version.
- Local store: `~/.local/share/mcpit/` (SQLite index + JSON sitepacks). Windows: `%LOCALAPPDATA%\mcpit`.
- A user can pull from the registry and never push.
- Teams can run a private registry (same Worker, own account) and point `mcpit` at it.

## 6. Exploration and the decision engine

### 6.1 Exploration pipeline

```
discover   → robots.txt, sitemap, llms.txt, /.well-known/mcp, OpenAPI, OpenSearch, GraphQL, WebMCP
collect    → static forms, links with query params, search boxes, fetch/XHR traffic (extension or headless)
infer      → parameter names, types, enums (<select> options), required, patterns, defaults, pagination
classify   → kind (search/form/api/read), effect (read/write/payment/destructive), auth need
probe      → safe GET probes only, with test values; never submit write/payment/destructive forms
name       → tool name + description + param descriptions
verify     → run each read tool twice; compare output shape; set confidence
save       → local sitepack; offer publish
```

### 6.2 Decision engine: rules → jev → LLM

Exploration needs many small decisions. Most are cheap. The engine asks the cheapest source first, and stops when the confidence is high enough.

| Order | Source | Speed | Used for |
|---|---|---|---|
| 1 | Rules | µs | Known patterns: `type=search`, `name=q`, `role=search`, `method=GET`, password fields, payment words. |
| 2 | jev (optional, Julia `/v1/systemone`) | ms | Yes/no and ranking: "Is this form a search?", "Is this endpoint read-only?", "Explore which candidate next?", "Is value X a safe probe for field Y?" |
| 3 | LLM | s | Hard cases: names, descriptions, mapping task text to parameters, multi-step flows. |

Rules for jev (lessons from jev-photos and the spaceshooter):

- Ask jev only with self-contained statement pairs ("A form with fields [q, category] is / is not a site search"). jev does not follow LLM-style instructions.
- Score each option on its own (independent mode). Native multi-choice is biased by option order.
- jev tends to answer "true". So jev ranks and breaks ties; it never gives the final "safe" verdict alone. Safety needs rules or the LLM to agree.
- Store every decision (source, question, answer, confidence). Show it in the UI ("how jev helped"). Use the stored decisions to measure jev against the LLM.
- jev is optional. Without it, the engine goes from rules to the LLM directly. A benchmark shows the speed-up with jev.

### 6.3 LLM provider

- Any OpenAI-compatible endpoint (local Qwen on :8888, Ollama) or the Claude API.
- A token budget per site. Cache every LLM answer by input hash.
- When mcpit runs inside an agent, it can ask the agent's own model (MCP sampling) instead of its own LLM.

## 7. Privacy: share structure, never data

| Shared | Never shared |
|---|---|
| Endpoint URL templates, methods | Cookies, auth headers, tokens, API keys |
| Parameter names, types, enums | Values the user typed |
| Selectors, flow steps | Response bodies, page contents |
| Output shape (JSON paths) | Account IDs, emails, names in paths (`/user/1234` → `/user/{id}`) |
| Anonymous counters | Full URLs with query values, browsing history |

- A redaction pass runs before save and again before publish. Publish shows a diff for the user to approve.
- Packs for `auth: session` sites can be shared (structure only), but each user uses their own login.
- Hard block for publish: private IPs, `localhost`, intranet names, sites on the user's private list.

## 8. Security scan and fact check

Every sitepack passes the scanner at publish (registry side) and at pull (client side).

| Check | What it catches |
|---|---|
| Schema lint | Bad types, missing fields, too-wide inputs. |
| Origin rule | Requests must go to the pack origin (or a declared, verified API domain). Blocks data exfiltration to a third party. |
| Effect check | A tool marked `read` must not use POST/PUT/DELETE without proof; payment/destructive words force a higher class. |
| Prompt-injection scan | Instructions hidden in names or descriptions ("ignore previous…", "send the token to…"). |
| Secret scan | Tokens, keys, emails, phone numbers left in the pack. |
| URL reputation | Known malware or phishing domains. |
| Fact check (replay) | The registry crawler re-runs read tools from a clean environment. Output shape must match the pack. Description must match observed behavior (LLM judge + jev cross-check). |
| Agreement | Packs from independent contributors that agree get a higher trust score. |
| Diff review | A new version that adds write tools or new domains goes to quarantine until reviewed. |

The result is a trust score (0–100) on the dashboard and in `mcpit_tools`. The client refuses packs below a user-set level.

## 9. Public access

- Open source (license to decide, §12).
- Install: `npm i -g mcpit` (CLI + MCP server), Chrome Web Store (extension), registry at a public URL.
- Docs site with quick start for each agent.
- Anonymous read access to the registry; login only to publish, vote or report.

## 10. Edge cases (answer to target 13)

### Site behavior
- **Site changes / drift**: fingerprints, per-tool re-explore, success-rate cut-off (§5.3).
- **Dynamic tokens** (CSRF, nonce, signed params made in JS): store a token step in `steps`; if the signature cannot be replayed, use the headless or extension executor only.
- **Multi-step flows**: wizards, cart, pagination, cursors, "load more", infinite scroll.
- **SPA and other transports**: client routing, GraphQL (persisted queries), WebSocket, server-sent events, gRPC-web.
- **Iframes, shadow DOM, cross-origin widgets**.
- **Locale, region, A/B tests, mobile vs desktop**: one site can have several variants; the fingerprint holds the variant.
- **File upload and download** tools: size limits, type checks, and user confirmation.
- **Rate limits and blocks**: back off; respect `Retry-After`; never rotate IPs to dodge limits.
- **CAPTCHA and bot checks**: stop and hand control to the user. Never solve or bypass.

### Safety of actions
- **Write, payment and destructive tools**: never probed during exploration; always confirm with the user before a call; dry-run when the site supports it.
- **Untrusted page content**: page text can contain prompt injection. Mark it untrusted; never follow it.
- **Wrong parameter mapping**: show the filled parameters before a write call.

### Registry and community
- **Poisoned packs**: signatures, origin rule, quarantine, replay (§8).
- **Sybil votes / fake popularity**: account-weighted votes; counters capped per account.
- **Name collisions**: tools are namespaced by origin.
- **Site owner rights**: claim, override, opt-out; honor `robots.txt` and a future `mcpit` opt-out header for crawling.
- **Terms of service and legal**: crawler is polite (low rate, identifies itself); the registry has a takedown process.
- **Registry outage**: local cache keeps working; registry is never on the hot path for known sites.
- **Schema versions**: `schema` field and migrations; old clients ignore unknown fields.

### Privacy and the user
- **History import**: domains only, sensitive-site filter, user review (§4.2).
- **Delete my data**: remove packs, counters and account in the registry; wipe the local store with one command.
- **Shared machines**: local store per OS user; extension token per profile; no incognito.

### Agent experience
- **Too many tools**: on-demand loading, `mcpit_find` search, top-N tools per site.
- **Slow first visit**: stream progress to the agent; let it use `mcpit_read` while exploration runs.
- **Cost control**: LLM token budget per site and per day; jev and rules first.
- **Concurrency**: one action per tab at a time (the prototype already does this); limit parallel explores.
- **Resource cap**: headless browsers and models obey the machine memory cap (80 % on the GB10).

## 11. Milestones

| M | Scope | Done when |
|---|---|---|
| M0 | Monorepo, sitepack schema v1, port prototype into `packages/extension`, rename MCPfier → mcpit | CI builds and lints all packages |
| M1 | Local loop: native detection + static explore + HTTP executor + local store + MCP server | 10 public test sites; repeat call ≥ 10× faster than first; agent uses it from Claude Code |
| M2 | Headless + extension explorers (network capture), browser executors, logged-in sites | 5 login sites work via the extension; no secrets in packs (test) |
| M3 | Decision engine rules → jev → LLM, decision log, benchmark | Report: decisions per source, accuracy, time saved with jev |
| M4 | CLI history import + idle batch | Batch of 50 sites runs in idle time within budget |
| M5 | Registry: publish, pull, sign, scan, quarantine, owner claim | Pull-then-call works on a clean machine with no exploration |
| M6 | Dashboard + public launch (npm, Chrome Web Store, docs) | Public URL live; ranking from real counters |
| M7 | Hardening: fact-check crawler, drift self-heal, fuzz tests, abuse handling | Red-team test set passes |

## 12. Open questions for the user

1. License: MIT or Apache-2.0?
2. Registry domain: `mcpit.<your-domain>` or a `workers.dev` URL first?
3. Login for publishing: GitHub only, or also device keys without an account?
4. Executor priority: is headless (Playwright) wanted in M1, or only HTTP + extension?
