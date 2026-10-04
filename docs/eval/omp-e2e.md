# End-to-end evaluation with oh-my-pi (omp)

Date: 2026-10-04. Agent: omp 18.6.0 with the local model `ollama/qwen3.8:latest` (27B, Q4_K_M). mcpit: the GitHub release build, installed with `install.sh`, set up with `mcpit setup omp` (MCP server + skill), each test user with its own store and device key.

## Setup (what a new user does)

```bash
curl -fsSL https://github.com/jstdlee/mcpit/releases/latest/download/install.sh | sh
cd my-project && mcpit setup omp      # writes .omp/mcp.json (with a 5-minute MCP timeout) and .omp/skills/mcpit/SKILL.md
omp "Use mcpit to …"
```

## Use case

> Use mcpit to answer: what is the latest version of the Rust crate serde on crates.io, and how many downloads does it have in total? If mcpit has no tools for crates.io yet, explore https://crates.io/ with mcpit first. After you answer, share the crates.io sitepack with the mcpit registry: I, the user, agree to share it.

## Result: the effect of sharing

| | User A (first visit) | User B (after A shared) |
|---|---|---|
| Where the tools came from | `mcpit_explore` job: 126 s, 15 pages, 60 tools | registry: signed pack, verified hash |
| Agent steps (tool calls) | 19 (find, explore, 3 status polls, tools, 2 calls, submit) | 10 (find, tools, 2 calls, submit) |
| Wall time (local 27B model) | 470 s | 226 s (−52 %) |
| Answer | serde 1.0.229; 1,479,080,242 downloads | same (correct) |
| Non-mcpit workarounds | none | none |

User B never opened a browser or crawled the site. Most of B's time is the local model's own thinking; the mcpit calls take well under a second each.

## Registry review of A's submit (crates.io)

| Round | Promoted | Quarantined | Rejected | Note |
|---|---|---|---|---|
| 1 (shared Clef state for 59 tools) | 22 | 35 | 2 | `find_crate`, the tool the agent used, was quarantined as "unsure" |
| 2 (one focused Clef call per tool) | 26 (+10 confirmed) | 16 (mostly write tools) | 5 (page tools that 404 over plain HTTP) | `find_crate` and `get_crate_downloads` promoted |

## Problems the test found, and the fixes

| Found | Fix (release) |
|---|---|
| omp's MCP calls time out after 30 s; an explore takes 40–120 s, and the agent fell back to a backgrounded CLI explore that print mode never waited for | `mcpit_explore` starts a background job; new `mcpit_explore_status`; `mcpit setup omp` sets a 5-minute timeout (v0.3.7) |
| omp has no MCP elicitation, so `mcpit_submit` was refused | `user_confirmed: true` when the user agreed in the chat; device key created on first submit (v0.3.8) |
| PyPI `/search` answers non-browsers with a JavaScript challenge (HTTP 200); the explorer and the registry accepted it, so a broken tool went live | decision point `resp.data` (Clef-flash) on every test-call response; the registry retires live tools that fail twice (v0.3.8–0.3.9) |
| Forms on challenge pages ("answer" field) became junk tools | pages that are browser checks are marked and their forms ignored |
| The tool the agent needed (a project page) was missing | page-shape tools (`/project/{name}/` from crawled pages and sitemaps) |
| The headless executor never ran (chromedp bound the tab to a short context) | fixed; tools marked `["http","headless"]` switch to Chrome after a browser check |
| 59 tools in one Clef state → unsure answers | one focused Clef-flash call per tool in the registry |

## PyPI: an honest limit

PyPI's CDN serves popular, cached pages to everyone but challenges uncached pages and all `/search` requests from automated clients, including headless Chrome and the registry's Cloudflare network. mcpit now handles this correctly — the broken search tool was retired and unverifiable page tools were not published as working — but it cannot make PyPI work as tools. The agent then said so and fetched the documented JSON API itself.

## Other observations

- The agent read the skill first and followed the workflow (find → explore → status → tools → call → submit).
- It slept 20 s between status polls instead of passing `wait: true`; harmless.
- User B's submit was refused because its device key waited for review: more than 5 new keys came from this one network on the test day, so the per-network cap held new keys back as designed.
