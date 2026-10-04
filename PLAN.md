# mcpit — Plan v3

Status: decisions taken (2026-10-04). Replaces plan v2 and the MCPfier 0.1 prototype scope.

Decisions (2026-10-04):
- License: **MIT**.
- Registry URL: **https://tomcp.ohmyai.xyz** (zone `ohmyai.xyz` is active in the Cloudflare account).
- Submit login: **device keys without an account**. A moderator reviews and approves each new device key before its first submit.
- Default expire days: **60**.
- Moderators: **the owner only**, helped by a background AI assistant (screening agent + decision model).
- Decision rule (2026-10-04): **the decision model makes every decision** inside the app (Clef / jev, System One API). The LLM never makes a main decision; it only writes text and verifies samples (§3.5).
- Exploration depth: **at most 2 links from the submitted URL** (§7).
- Models: **BYOK** for the LLM verifier and text writer; **Cloudflare Agents SDK** for the screening agent; **Clef / Clef-flash** on Workers AI as the jev-like decision model. Local jev stays optional. Test results in §10.

Changes from v2:
- The Chrome extension is out of scope for now. Users may not trust it, and it is weak at exploring APIs and forms. The prototype is parked in `legacy/extension/`.
- The scope is the **MCP server**, the **CLI** and, most important, the **registry**.
- The registry now owns versions, validation, duplicate submits, update decisions, the security gate (rules + Clef decision model + background screening) and service settings.

## 1. Goal

mcpit makes any website usable by an agent as an MCP server. The site does not need to support MCP.

- The first visit to a site is slow: mcpit explores the site and learns its forms, search and API endpoints.
- mcpit saves what it learns as a **sitepack** (metadata only, no user data).
- Every later visit uses the sitepack directly. It is fast.
- Users can share sitepacks in a public registry. Then other users skip the slow first visit.
- The registry decides which sitepack version is good. More submits give better versions.

## 2. Lookup ladder

For each call, mcpit takes the first rung that works:

1. **Native**: the site has `/.well-known/mcp`, OpenAPI, OpenSearch, GraphQL introspection or WebMCP. Use it.
2. **Local sitepack** in the local store. Use it.
3. **Registry sitepack** (active version). Pull it, check the signature, use it.
4. **Explore**: build a sitepack, save it locally, and offer to submit it.

A failed call on rung 2 or 3 re-explores only the broken tool. The fix can go back to the registry as a correction (§6.4).

## 3. Components

```
packages/
  core/        sitepack schema, store, explorer, executors, decision engine, redactor, scanner
  mcp-server/  stdio + streamable HTTP MCP server
  cli/         mcpit command (also starts the MCP server and the screener)
  registry/    Cloudflare Worker + D1 + R2 + Agents SDK: API, versions, gate, settings, screening agent
  screener/    optional external screener (runs anywhere: local jev + any LLM + replay)
  console/     registry web UI: dashboard (public) and settings (admin/moderator)
legacy/extension/  parked MCPfier 0.1 prototype
```

### 3.1 MCP server (`mcpit serve`)

| Tool | Use |
|---|---|
| `mcpit_find` | Find site tools by URL, domain or task text. |
| `mcpit_tools` | List the tools of one site (loaded on demand). |
| `mcpit_call` | Call one site tool. Write tools ask the user first (MCP elicitation). |
| `mcpit_explore` | Explore a URL now (slow path). Streams progress. |
| `mcpit_read` | Read main text or tables, as a fallback while exploration runs. |
| `mcpit_report` | Report a broken or wrong tool to the local store and the registry. |
| `mcpit_submit` | Submit a local sitepack or a fix. Shows the diff; the user approves. |

### 3.2 CLI

```
mcpit explore <url> [--depth N]
mcpit call <site> <tool> --json '{...}'
mcpit login <origin>                      # opens a visible browser once; session stays local
mcpit history import [--chrome|--firefox] [--since 30d]
mcpit batch [--idle] [--budget 2h] [--max-sites 50]
mcpit key init | key status                 # make a device key; it waits for moderator approval
mcpit pull <site> | submit <site> | status <submission-id> | report <site> <tool>
mcpit store ls | show | rm | export | import
mcpit serve [--stdio|--http :PORT]
mcpit screener run --registry <url> --token <t> [--jev http://127.0.0.1:8011] [--llm <openai-compatible url>]
mcpit doctor
```

### 3.3 Executors (no extension)

| Executor | When |
|---|---|
| HTTP | Public API or form endpoint with replayable parameters. Fastest. |
| Headless browser (Playwright) | JS renders the page, makes tokens, or a request cannot be replayed. |
| Headless with login profile | Sites behind a login. `mcpit login` opens a visible browser with an mcpit-only profile once; later runs use that profile headless. Cookies never leave the machine. |

### 3.4 Agent integration

- MCP config snippets for Claude Code, Claude desktop, Codex, Cursor.
- A skill file: "before you browse a site, call `mcpit_find`".
- The CLI lets an agent prepare sitepacks in batch from history, in idle time.

### 3.5 Decisions: the decision model decides

Every choice in mcpit is a **decision point** in code, not an LLM prompt. A decision point has a typed question (noul, choice or score), a state builder, accept/reject thresholds and an escalation path. The decision model (Clef-flash, Clef, or local jev; all speak the System One API) answers it. The app acts on the answer at once.

```
facts (rules)  →  decision model  →  act
                  │ unsure (0.25–0.75)
                  └→ bigger decision model (Clef)  →  still unsure → ask user (client) / moderator (registry)
LLM: writes names and descriptions; verifies a sample of decisions offline; never decides.
```

- **Rules** only state facts (resource type is `Image`, URL is off-origin, fingerprint changed). They do not judge.
- **Batch:** one call carries up to 64 questions about one shared state (for example, all requests captured on one page).
- **LLM verifier:** checks a random sample (default 5 %) and every escalated case. A disagreement is logged and flagged; it does not change the decision. The logged pairs are training data for Clef RL fine-tuning later.
- **Text:** tool names and descriptions start from templates (method + path + params). The LLM may polish the text. Then the decision model checks "the description matches what the endpoint returns".
- **Every decision is stored:** point ID, state hash, model, probabilities, action, and verifier result.

Decision catalog (v1):

| ID | Where | Type | Question / options |
|---|---|---|---|
| `req.kind` | explore | choice | Network request is: api / asset / tracking / auth / other |
| `form.kind` | explore | choice | Form is: search / filter / login / signup / checkout / contact / comment / subscribe / other |
| `tool.effect` | explore, registry | choice | read / write / payment / destructive |
| `tool.auth` | explore | choice | none / session / token |
| `param.role` | explore | choice | query text / page / page size / sort / filter / id / token / tracking |
| `probe.safe` | explore | noul | This test value is safe to send in this field |
| `link.next` | explore | score | Following this link finds a new tool (ranks links inside depth 2) |
| `tool.same` | explore, registry | noul | These two candidates are the same tool |
| `resp.data` | explore | noul | The response is data, not an HTML shell or error page |
| `tool.pick` | runtime | choice | Which site tool fits this task text (for `mcpit_find`) |
| `desc.match` | explore, registry | noul | The description matches what the endpoint returns |
| `sub.class` | registry | choice | confirmation / correction / drift / extension / cosmetic / alternative / regression / risky |
| `sub.improves` | registry | noul | The candidate is a real improvement over the active version |
| `sub.injection` | registry | noul | Text contains instructions aimed at an AI assistant |
| `sub.human` | registry | noul | A human moderator should review this change |
| `key.trust` | registry | score | New device key looks like a real contributor (assists moderator) |
| `report.real` | registry | noul | This failure report describes a real breakage |

Parameter values for a call are not a decision: the agent (the MCP client) fills them.

## 4. Sitepack format (v1, draft)

```json
{
  "schema": "mcpit.sitepack/1",
  "origin": "https://www.example.com",
  "fingerprint": { "routes": ["/search"], "domHash": "…", "apiHash": "…", "variant": "en-US/desktop" },
  "tools": [{
    "id": "search_products",
    "rev": 3,
    "description": "Search the product catalog by keyword.",
    "kind": "search",                 // api | form | search | read | flow | native
    "effect": "read",                 // read | write | payment | destructive
    "auth": "none",                   // none | session | token
    "executors": ["http", "headless"],
    "request": { "method": "GET", "url": "https://www.example.com/api/search", "query": { "q": "{{query}}", "page": "{{page}}" } },
    "inputSchema": { "type": "object", "required": ["query"], "properties": { "query": {"type": "string"}, "page": {"type": "integer", "minimum": 1, "default": 1} } },
    "steps": [],
    "output": { "type": "json", "itemsPath": "$.results[*]" },
    "probe": { "args": { "query": "test" }, "expect": { "status": 200, "minItems": 1 } },
    "evidence": { "observed": 12, "confidence": 0.93 }
  }],
  "provenance": { "submitter": "key:ed25519:3f9a…", "client": "mcpit/0.3", "decisions": "rules+clef+llm" }
}
```

- `probe` is a safe test call. The screener uses it to replay read tools. Write tools have no live probe.
- Versions are per tool (`rev`) and per pack. A fix to one tool does not replace the other tools.

## 5. Privacy: share structure, never data

| In a sitepack | Never leaves the machine |
|---|---|
| Endpoint URL templates, methods | Cookies, auth headers, tokens, keys |
| Parameter names, types, enums | Values the user typed, response bodies |
| Flow steps, output shape | IDs and emails in paths (`/user/1234` → `/user/{id}`) |
| Safe probe arguments | Full URLs with query values, browsing history |

The client redacts before save and before submit. The registry scans again (§6.3).

## 6. Registry (the core of mcpit)

The registry is the single source of truth for shared sitepacks. It controls versions, validates every submit, handles repeated submits, and decides if a new submit really improves the current version.

### 6.1 Version model

- **Site** (origin + variant) → **pack versions** → **tool revisions**.
- Every version is immutable and content-addressed (`sha256` of the canonical JSON).
- One version per site is **active**. Clients pull the active version.
- The registry signs each active version. Clients check the signature.

Version states:

```
submitted → validated → screening → candidate → active → superseded
                │            │            │          └──→ expired → (re-verified → active)
                └→ rejected  └→ quarantined (human review)          └──→ delisted
```

### 6.2 Submit pipeline

| Step | Sync/async | What happens |
|---|---|---|
| 1. Intake | sync | Device-key signature (key must be `approved`), rate limit per key and per site, size limit, schema check. |
| 2. Canonicalize | sync | Sort keys, drop volatile fields (timestamps, counters), normalize URL templates. Compute the canonical hash. |
| 3. Duplicate check | sync | Compare the hash and near-duplicates with all versions of the site (§6.4). |
| 4. Static scan | sync | Origin rule, effect check, prompt-injection, secret scan, URL reputation (§6.3). Hard fail → `rejected`. |
| 5. Diff | sync | Tool-level diff against the active version: added, removed, changed (endpoint, params, effect, auth, description). |
| 6. Queue | sync | Store as `validated`; put a screening task on the queue. Return the submission ID. |
| 7. Screening | async | Screening agent runs rules (facts), replay and Clef decision points; the LLM only verifies (§6.5). |
| 8. Decision | async | Promote, merge per tool, keep as alternative, reject or quarantine (§6.6). |

The client polls `mcpit status <id>` or gets the result on its next pull.

### 6.3 Static security scan

| Check | What it catches |
|---|---|
| Schema lint | Bad types, missing fields, inputs that accept anything. |
| Origin rule | Requests to a domain other than the site, or not a declared API domain of the site. Blocks data exfiltration. |
| Effect check | A `read` tool with POST/PUT/DELETE and no proof; payment/delete words force a higher class. |
| Prompt-injection scan | Instructions hidden in names, descriptions or enums. |
| Secret scan | Tokens, keys, emails, phone numbers, IDs left in the pack. |
| URL reputation | Known malware or phishing domains. |
| Private targets | `localhost`, private IPs, intranet names. |

### 6.4 Repeated submits

| Case | Detection | Result |
|---|---|---|
| Same submitter, same content | Same canonical hash + same account | Idempotent: return the existing submission ID. No new version, no extra count. |
| Other submitter, same content | Same hash, different, unlinked account | **Confirmation**: +1 independent agreement on that version. Raises trust. |
| Near-duplicate | Only volatile or cosmetic fields differ | Confirmation of the base version; cosmetic part goes to the description review. |
| Resubmit of rejected content | Hash in the rejected set | Auto-reject with the old reason, unless the site fingerprint changed. |
| Flood | Many submits from one account or one site | Rate limit, then lower the account reputation. |
| Linked accounts | Same device key, same IP range, same timing pattern | Counted as one voice for agreement. |

### 6.5 Background screening (the gate)

The gate is a **screening agent** built with the Cloudflare Agents SDK (an `Agent` class on a Durable Object, with its own SQLite, `queue()`, `schedule()` and durable fibers). One agent instance per site keeps all decisions for that site together and runs one task at a time.

| Role | Model | Why |
|---|---|---|
| Decision model (jev-like) | `@cf/cloudflare/clef-flash` first; `@cf/cloudflare/clef` when flash is not sure (0.25–0.75) or the change is risky | Typed answers with probabilities, no text to parse. System One API, the same as jev. |
| LLM verifier | BYOK: the operator's key and endpoint. Default `@cf/deepseek-ai/deepseek-v4-flash-0731` on Workers AI | Checks a sample and every escalated case; writes the reason text for the moderator. Never decides. |
| Local jev (optional) | Julia `/v1/systemone` via the external screener | Same API as Clef, so it plugs in as one more decider. |

Cost: a Clef-flash decision uses about 350 input tokens (≈ $0.00003 at $0.09 per M tokens). The LLM verifier runs on a sample and on escalated cases only. The settings set a daily budget; when it runs out, tasks wait.

The external `mcpit screener run` (pull worker) stays as an option for sponsors and for local jev or free models. Its results are signed and count as one more vote.

Each screening task does four things:

1. **Rules.** Facts that need no model: fingerprint changed → drift; new domain or write effect → risky; removed passing tools → regression.
2. **Replay.** Run each `probe` of the candidate and of the active version from a clean environment. Record status, output shape and errors.
3. **Decision model questions.** Self-contained yes/no and choice questions about the state (tool, diff, replay results). Examples:
   - "This tool searches the site catalog."
   - "The tool description matches what the endpoint returns."
   - "This tool sends data to a domain that is not the site origin."
   - "The candidate is a real improvement over the active version."
   - "A human moderator should review this change before it is published."
4. **Escalate** when Clef-flash is unsure: Clef; still unsure or risky → moderator queue.
5. **LLM verifier** on a sample and on escalated cases. It writes the summary for the moderator. A disagreement is a flag, not a verdict.

The decision model ranks and flags. It never approves a write, payment or new-domain change alone: rules send those to the moderator queue.

**AI moderator assistant.** The owner is the only moderator. The screening agent prepares every quarantine item and every new device key: a summary, the diff, replay results, Clef probabilities, the LLM reason and a proposed verdict. The owner approves or rejects with one click.

### 6.6 Is the new submit a real improvement?

For each changed tool, the screener puts the change in one class:

| Class | Signal | Decision |
|---|---|---|
| Confirmation | Same as active | +1 agreement. |
| Correction | Active probe fails (or has failure reports); candidate probe passes | Promote that tool revision. |
| Drift update | Site fingerprint changed; candidate matches the new site; active fails | Promote. |
| Extension | New read tools; existing tools unchanged and still pass | Promote new tools. |
| Cosmetic | Only description or names differ | Promote only if Clef/LLM say the new text matches the behavior better; else keep. |
| Alternative | Both pass; different endpoint or flow | Keep as alternative; the one with more agreement and success wins later. |
| Regression | Removes passing tools, a probe fails, or output shape worse | Reject. |
| Risky | Wider effect, new domain, new auth need, new write tool | Quarantine for a moderator. |

Promotion is per tool. The registry builds the next active pack version from the active tools plus the promoted revisions. Superseded versions stay readable for rollback.

Score used for ties and ranking (draft):

```
score = 0.35·replay_pass + 0.20·agreement + 0.15·field_success_rate
      + 0.10·clef_score + 0.10·llm_score + 0.10·submitter_reputation
```

### 6.7 Field feedback

- Clients send anonymous counters: site, tool, rev, ok/fail, error class. No URLs with values, no parameters, no user IDs.
- A falling success rate starts a re-screen. Failure reports make a later fix count as a correction.

### 6.8 Registry settings (service management console)

Roles: **admin/moderator** (the owner), **AI moderator assistant** (proposes, never decides risky items), **site owner**, **contributor** (device key), **screener**.

| Area | Settings and actions |
|---|---|
| Sources | List of sites and their pack sources: community, owner-claimed, native (site's own MCP/OpenAPI), crawler. Per-source trust level; allow/deny lists of domains. |
| Verdicts | Mark a version or tool **good** (verified badge) or **bad** (blocked, clients refuse it). Moderator verdict overrides the score. |
| Expiry | Global default expire days (60) and per-site override. A version not re-verified within the limit becomes `expired`. The screener re-verifies before expiry. Clients warn on expired packs. |
| Listing | Sort and rank by used (calls, unique clients), stars, trust score, trend, newest. |
| Stars | One star per account per site. |
| De-list / re-list | Hide a site or version from search and pulls, with a reason (takedown, owner opt-out, abuse, legal). |
| Owner claim | Verify by DNS TXT or `/.well-known/mcpit.json`. Owner packs rank first. Owner can opt out. |
| Screening | Clef-flash/Clef thresholds; external screener tokens; local jev endpoint; LLM provider (BYOK key, model, endpoint); daily budget; required screener agreement; auto-promote thresholds per class. |
| Device keys | New keys wait in a review queue: key fingerprint, first submit preview, AI assistant note. Approve, reject, revoke. Reputation, rate limits, linked-key rules. |
| Quarantine queue | Review risky changes: diff view, replay results, Clef/LLM reasons; approve or reject. |
| Audit log | Every submit, decision, verdict and settings change, with actor and reason. |

### 6.9 Device keys and signatures

- **Device key:** `mcpit key init` makes an Ed25519 key pair in `~/.config/mcpit/key` (file mode 0600). The ID is `key:ed25519:<first 16 hex of sha256(public key)>`.
- **Register:** the client sends the public key, a client name and a proof (it signs a server nonce). The key state is `pending` until the moderator approves it.
- **Signed request:** each write request has the headers `mcpit-key`, `mcpit-ts` and `mcpit-sig` = Ed25519 signature of `METHOD\nPATH\nTS\nsha256(body)`. The registry refuses a timestamp older than 5 minutes and a nonce it has seen.
- **Lost or new device:** make a new key; it goes through review again. The old key can be revoked; its past submits keep their history.
- **Registry signing key:** an Ed25519 key in a Worker secret signs each active version (`sha256` + origin + version). The public key ships in the client and is also served at `/.well-known/mcpit-keys.json`, with a key ID so the key can rotate.

### 6.10 Where replay runs

| Probe type | Runs in | Cost |
|---|---|---|
| HTTP probe | The screening agent (Worker `fetch`) | Free in the Workers plan limits |
| Headless probe | External screener on the GB10 (Playwright) for the demo; Cloudflare Browser Rendering later, if the budget allows | Local: free; Browser Rendering: paid per browser time |
| Login-only tools | Never replayed by the registry (no user session there). Trust comes from agreement and field success rate only. | — |

A site may block Cloudflare IP ranges. Then the probe result is "blocked", not "failed", and the external screener retries it.

### 6.11 Storage layout (draft)

- **D1:** `sites`, `versions`, `tools`, `tool_revs`, `submissions`, `decisions`, `keys`, `stars`, `counters_daily`, `verdicts`, `settings`, `audit`.
- **R2:** `packs/<origin-hash>/<version-hash>.json` (immutable) and `submissions/<id>.json`.
- **Screening agent (Durable Object per site):** task queue, schedules for expiry re-checks, and a cache of the latest decisions. D1 holds the final records.

### 6.12 Public dashboard

- Top sites by use, stars, trust and trend; newest sites; recently corrected.
- Per-site page: tools, active version, history, contributors, trust score, scan and replay results, expiry date, success rate.

### 6.13 Registry API (draft)

```
GET  /v1/sites?q=&sort=used|stars|trust|trend|new
GET  /v1/sites/{origin}                 → active version + metadata
GET  /v1/sites/{origin}/versions[/{hash}]
POST /v1/keys                           → {fingerprint, state: pending}   (device key registration)
POST /v1/submissions                    → {id, state}   (signed by an approved device key)
GET  /v1/submissions/{id}
POST /v1/reports                        (anonymous counters, failure reports)
POST /v1/stars/{origin}
GET  /v1/screener/tasks   POST /v1/screener/results     (screener token)
/v1/admin/*                             (sources, verdicts, expiry, delist, settings, audit)
```

## 7. Exploration (client side)

### 7.1 Scope

- **Depth: at most 2** link hops from the submitted URL (depth 0 = the URL, 1 = pages it links to, 2 = pages those link to). Same origin only, plus API domains the pages call.
- Inside depth 2, `link.next` ranks links so the page budget goes to pages that likely add new tools. URL templates (`/product/123` → `/product/{id}`) get 1–2 pages each.
- Declared specs (OpenAPI, sitemap, OpenSearch, `/.well-known/mcp`, GraphQL introspection) are read first, at any path. They do not count against depth.

### 7.2 DevTools-style capture

The explorer drives a headless Chromium through the Chrome DevTools Protocol (CDP), the same data the DevTools panels show:

| DevTools panel | CDP source | What mcpit takes |
|---|---|---|
| Elements | `DOMSnapshot.captureSnapshot`, accessibility tree | Forms, inputs, `<select>` options, buttons, search boxes, links, ARIA roles, labels |
| Network | `Network.requestWillBeSent`, `responseReceived`, `getResponseBody` | URL, method, resource type, MIME, status, headers (secrets dropped), body preview, **initiator** (which script or click sent it) |
| Sources | Script URLs from `Debugger.scriptParsed` | `fetch(`/axios/GraphQL strings for endpoints no click reaches |
| Application | Cookies, storage keys (names only) | Whether a call needs a session (`tool.auth`) |

Per page:

```
load page (CDP on)          → network log + DOM snapshot
act on controls              → type safe values (probe.safe), click filter/sort/paging, scroll
facts (rules)                → drop by resource type: Image, Font, Stylesheet, Media, Manifest
decide req.kind (batched)    → api / asset / tracking / auth / other, ≤64 requests per call
decide form.kind             → search / login / checkout / …
diff requests                → changed part = parameter; per-load value = token step
decide param.role, tool.effect, tool.auth, resp.data, tool.same
template text + desc.match   → tool
safe probe + verify          → sitepack
```

### 7.3 Discovery sources

| Source | Technique | Finds |
|---|---|---|
| Declared specs | `robots.txt`, `sitemap.xml`, `llms.txt`, `/.well-known/mcp`, OpenAPI paths (`/openapi.json`, `/swagger.json`, `/api-docs`), OpenSearch, JSON-LD `SearchAction`, GraphQL introspection, RSS/Atom | Complete APIs when the site publishes them |
| DOM tree (Elements) | DOM snapshot of every visited page | `<form>` (action, method, inputs, options, `required`, `pattern`), links with query params |
| Network log | All requests during load and actions | The real API calls behind the UI |
| Script scan | Same-origin scripts | Endpoints that no click reaches |
| Request diff | Compare requests across actions | Parameter names, types, enums, pagination, tokens |
| Login profile | Same capture with the `mcpit login` profile | Tools behind a login (`auth: session`) |

Safety during discovery: depth ≤ 2, same origin, low request rate, no POST form submit unless `form.kind` = search/filter and `tool.effect` = read, never real user data, stop on CAPTCHA.

## 8. Edge cases

### Site behavior
- Site changes: fingerprint, per-tool re-explore, drift update to the registry.
- Dynamic tokens (CSRF, nonce, JS-signed params): token step, or headless only.
- Multi-step flows, pagination, cursors, infinite scroll.
- SPA routes, GraphQL persisted queries, WebSocket, SSE.
- Locale, region, A/B and mobile variants: the variant is part of the site key.
- Rate limits: back off, honor `Retry-After`, no IP rotation.
- CAPTCHA and bot checks: stop and hand control to the user. Never bypass.

### Actions and agents
- Write, payment, destructive tools: never probed; always confirmed; show filled parameters first.
- Page text is untrusted (prompt injection).
- Too many tools: on-demand loading and search.
- Cost: token budget per site and per day; rules and the decision model first.
- Memory: headless browsers and models obey the 80 % memory cap.

### Registry
- Poisoned packs: static scan, replay, quarantine, signatures.
- Fake popularity: one star per account per site; linked accounts count once.
- Screener abuse: signed results, several screeners per task, screener reputation, random re-checks.
- Probe abuse: the screener's replay must not attack a site: low rate, `robots.txt`, owner opt-out, read probes only.
- Registry outage: local cache; the registry is never on the hot path for known sites.
- Schema versions and migrations.
- Delete my data; owner takedown; legal takedown process.

## 9. Milestones

| M | Scope | Done when |
|---|---|---|
| M0 | Monorepo, sitepack schema v1, park extension in `legacy/` | CI builds and lints all packages |
| M1 | Core + CLI + MCP server: native detection, explore (static + headless), HTTP/headless executors, local store | 10 public sites; repeat call ≥ 10× faster than first; works from Claude Code |
| M2 | Registry core: submit, canonical hash, duplicate rules, static scan, tool diff, version states, pull with signature | Repeated submit tests pass; pull-then-call on a clean machine |
| M3 | Screening agent (Agents SDK): rules, replay, Clef decision points, BYOK LLM verifier, improvement classes, per-tool promotion, AI moderator assistant | Test set of corrections, drifts, regressions and poisoned packs is classified right |
| M4 | Console: settings (sources, verdicts, expiry, de-list, screening, quarantine, audit) + public dashboard | Moderator can run the full flow; ranks from real counters |
| M5 | Client decision engine (Clef or local jev); `mcpit login`; history import; idle batch | Benchmark: time saved with the decision model; 50 sites in idle time |
| M6 | Public launch: npm package, registry URL, docs | Public URL live |
| M7 | Hardening: multi-screener agreement, fuzz and red-team tests, abuse handling | Red-team set passes |

## 10. Model test (2026-10-04)

Script: `experiments/cf-models/bench.py`; raw results: `experiments/cf-models/results.json`. 12 screening cases with known answers (23 checks: search form, login form, wrong description, write tool labelled read, data sent off-site, prompt injection, fix, cosmetic change, regression, site drift, new payment tool, new read tool). 3 runs per case, REST calls from the GB10 (latency includes network).

| Model | Correct | Median | p90 | Notes |
|---|---|---|---|---|
| `@cf/cloudflare/clef-flash` | 21/23 | 574 ms | 976 ms | Said "risky" (0.83) for the payment tool but "needs human" only 0.26. |
| `@cf/cloudflare/clef` | 21/23 | 696 ms | 1576 ms | Best calibrated: 0.94–0.99 on clear cases. Missed cosmetic vs confirmation. |
| `@cf/deepseek-ai/deepseek-v4-flash-0731` | 22/23 | 2573 ms | 3819 ms | Needs prompt + JSON parsing; ~4× slower. |
| Local jev (Julia, :8011) | — | — | — | Not running during the test. |

- All three models called the drift case "correction". Rules detect drift from the fingerprint, so no model needs to.
- Workers AI has no "DeepSeek V4.1 Flash"; `deepseek-v4-flash-0731` is the newest DeepSeek Flash there.
- Result: Clef-flash as the first decider, Clef for unsure and risky cases, rules for facts, LLM only for disagreements.

### 10.1 `req.kind` test: API or asset? (2026-10-04)

Script: `experiments/cf-models/api_vs_asset.py`. 16 captured requests from one page (DevTools Network fields: URL, method, type, MIME, status, initiator, preview), sent as **one call with 16 choice questions**. The set includes hard cases: Next.js data JSON, GraphQL POST, i18n JSON, feature flags, Sentry and Segment calls, a bot-check script.

| Model | Correct | Median per call (16 requests) | Input tokens |
|---|---|---|---|
| `clef-flash` | 16/16 | 929 ms (≈ 58 ms per request) | 3,746 |
| `clef` | 16/16 | 1,371 ms | 3,746 |

Result: Clef-flash is good enough for `req.kind` as the main decider, with batching.

## 11. Stack and v0.1 goal (2026-10-04)

Stack decisions:
- **Decision model:** Clef-flash only (`@cf/cloudflare/clef-flash`). Unsure answers go to the user (CLI) or the moderator (registry). Local jev stays a plug-in option (same System One API).
- **Registry and console:** Cloudflare Worker + Vue 3, built with Vite+ (`vp`) and `@cloudflare/vite-plugin`; Agents SDK for the screening agent; D1 for data (R2 later for large packs).
- **CLI:** Go, one static binary `mcpit`. Headless capture with `chromedp` (Chrome DevTools Protocol).
- **MCP server:** inside the same Go binary (`mcpit serve`), with the official Go SDK `github.com/modelcontextprotocol/go-sdk`. Reason: one binary, no Node runtime for users, and it shares the store, executor and decision code with the CLI.

**v0.1 goal: one end-to-end loop works.**

1. `mcpit explore <url>` (depth ≤ 2) finds the forms and APIs of a test site through CDP capture, with Clef-flash decision points; it saves a sitepack.
2. `mcpit call` and the MCP tools call those tools over HTTP.
3. `mcpit key init` + moderator approval + `mcpit submit` send the pack to the registry.
4. The registry runs the gate (canonical hash, duplicate rules, static scan, diff, screening agent with replay + Clef-flash) and makes it active, or quarantines risky tools.
5. On a clean store, `mcpit pull` + `mcpit call` work with no exploration.
6. The console shows the dashboard and the moderator pages (keys, quarantine, settings, verdicts, de-list).

Done when: Go unit tests + an end-to-end test against a local fixture site pass; registry logic tests pass (`vp test`); the loop runs against the local registry (`vp dev`).

### 11.1 v0.1 result (2026-10-04)

All six goal steps run locally (registry on `vp dev`, fixture shop on 127.0.0.1:7810):

| Step | Result |
|---|---|
| Explore (CDP, depth 2, Clef-flash) | 7 tools in ~21 s: OpenSearch search (+ merged `category` select), live-search API, GraphQL `ProductFilters`, products list, product by id, recommendations, contact form with CSRF token step. Login form, `/admin` (robots), assets, i18n JSON and tracking skipped. 31 Clef-flash decisions. |
| Call | CLI and MCP tools call over HTTP; write tool needs confirmation (refused without elicitation). |
| Keys + submit | Pending key → 403; approved key → submit → screening. |
| Gate | 6 read tools promoted (test call + Clef: desc.match, sub.injection, tool.effect); write tool quarantined; moderator approve → version `.2`. Repeated submit → same id; other key, same pack → confirmation; exfil URL → rejected by origin rule; same again → "rejected before"; regression → rejected; wording-only → kept out; subtle prompt injection → rejected (Clef 0.95), reputation −5. |
| Clean pull + call | Pulled, signature and hash verified (Go = TypeScript canonical bytes), called with no exploration; usage counters update. |
| Console | Dashboard, site page, moderator pages (keys, quarantine, sites, submissions, settings, audit). |

Lesson: Clef-flash missed a subtle injection when the state held the whole submission (0.05) but caught it when the state held only that tool's text (0.95). Rule: give each safety question a focused state; batch only questions about the same small state.

Deploy (2026-10-04): live at **https://mcpit-registry.jstdlee.workers.dev** (workers.dev first; tomcp.ohmyai.xyz later). D1 `mcpit` created and migrated with `cf`; `ADMIN_TOKEN`, `SIGNING_KEY`, `SIGNING_KEY_ID` set with `cf workers secrets update`; production signing key `reg-1`. The Worker deploys with `wrangler deploy` after `vp build`: `cf deploy` does not read the Vite-plugin project yet (it rewrote the config with no bindings, so those changes were reverted).

LLM verifier (2026-10-04): after each screening, the agent queues `runVerify`. It checks every escalated or unsure decision and a 5 % sample of the rest with DeepSeek V4 Flash on Workers AI (BYOK: `LLM_MODEL`, or `LLM_BASE_URL` + `LLM_API_KEY`). It never changes a decision; a disagreement goes to the audit log and the console Verifier tab, and its note is added to quarantined items.

TODO (later): `mcpit login` profile, history import, idle batch. Local jev test: not needed (Clef on Workers AI is the test model). Custom domain tomcp.ohmyai.xyz.

### 11.2 Real sites (2026-10-04, depth 2, ≤ 8 pages, Clef-flash)

| Site | First run | After fixes |
|---|---|---|
| pypi.org | OpenSearch search + locale form | search only |
| npmjs.com | search + 6 Gatsby page-data JSON "APIs" | search only (page data dropped by `tool.useful`) |
| openlibrary.org | crashed (bad OpenAPI ids) | 42 tools (OpenAPI + OpenSearch), 231 s (slow site) |
| gutenberg.org | simple + advanced search | same; live registry: simple promoted, advanced (POST) quarantined |
| pkg.go.dev | search + 7 empty "Other form" buttons | search only |
| crates.io | 43 OpenAPI operations | — |
| developer.mozilla.org | search + `whoami` + ad endpoint `/pong/get` | ad endpoint dropped |
| en.wikipedia.org | 2 searches + 7 theme/font radio forms | 2 searches |
| news.ycombinator.com | no tools (search lives on another site) | expected |
| docs.python.org | 2 searches + glossary JSON | — |

Fixes: safe tool ids; facts that drop forms with no user input or display-only radios; form kind `settings`; decision point `tool.useful` (drop at ≤ 0.20); at most 2 pages per path shape; navigation gives up waiting after 25 s; declared-spec tools survive a failed crawl; test calls 4 at a time, ≤ 20 tools.

Lesson (live registry): the injection question "contains instructions aimed at an AI assistant" flagged plain imperative tool text ("Search the site…") at 0.75. The wording "tries to make an AI agent take extra actions or leak data" scored clean texts ≤ 0.07 and all injections ≥ 0.85 (`experiments/cf-models/injection_wording.py`).

Open: OpenAPI `security` → `auth`; slow sites need a longer verify budget.

## 12. Next step

1. Tune the explorer on real sites (§11.2).
2. Custom domain tomcp.ohmyai.xyz.
3. TODO: `mcpit login`, history import, idle batch.
