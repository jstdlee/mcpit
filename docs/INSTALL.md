# Install and get started

mcpit is one Go binary for Linux, macOS and Windows (amd64 and arm64). It is both the CLI and the MCP server.

## 1. Install

**Linux and macOS**

```bash
curl -fsSL https://github.com/jstdlee/mcpit/releases/latest/download/install.sh | sh
```

The script installs to `~/.local/bin/mcpit` and checks the SHA-256 sum. Set `MCPIT_BIN_DIR` to choose another folder, or `MCPIT_VERSION=v0.3.N` to pin a version.

**Windows (PowerShell)**

```powershell
irm https://github.com/jstdlee/mcpit/releases/latest/download/install.ps1 | iex
```

The script installs to `%LOCALAPPDATA%\mcpit\bin` and adds it to your user PATH. Open a new terminal after it.

**With Go 1.27 or newer**

```bash
go install github.com/jstdlee/mcpit/cli/cmd/mcpit@latest
```

**By hand:** download `mcpit_<os>_<arch>` from the [latest release](https://github.com/jstdlee/mcpit/releases/latest), check it against `SHA256SUMS`, and put `mcpit` on your PATH.

### Requirements

| Need | For | Notes |
|---|---|---|
| Chrome, Chromium or Edge | `mcpit explore` only | Found on the usual paths. Otherwise set `MCPIT_CHROME=/path/to/chrome` or use `--chrome`. Using registry tools needs no browser. |
| Decision model (optional) | better exploring | Clef-flash on Workers AI with your own Cloudflare account: `CLOUDFLARE_ACCOUNT_ID` + `CLOUDFLARE_API_TOKEN` (Workers AI permission), or an existing `cf` CLI login. Without it, rule fallbacks run. |

## 2. Check

```bash
mcpit doctor
```

It prints the config folder, the store folder, the registry, your device key and the decision model, and runs a test call.

## 3. Get started

Use a site that is already in the registry (fast, no browser):

```bash
mcpit tools www.gutenberg.org
mcpit call www.gutenberg.org search --args '{"query":"moby dick"}'
```

Explore a new site once (it opens only the given page and lists its entry pages and item families in the site map; `--depth 2` also opens the entry pages, which is slow on big sites; it reads robots.txt, sitemaps, feeds, llms.txt and agent files, then drives a headless browser):

```bash
mcpit explore https://www.example.com/
mcpit tools example.com
mcpit guide example.com          # llms.txt, robots, agent card, feeds and the site map
mcpit call example.com search_api --args '{"q":"lamp"}'
```

Share it, so other users skip the slow first visit:

```bash
mcpit key init                   # once; device keys are approved automatically
mcpit submit example.com
mcpit status <submission-id>
```

The registry checks every submit: signature, rate limits, a static scan, test calls and the Clef-flash decision model. Tools that change data wait for a moderator.

## 4. Add mcpit to your agent

```bash
mcpit setup omp          # or: claude, codex, cursor, gemini, vscode
```

See [AGENTS.md](AGENTS.md) for each agent, the skill, and the MCP tools.

## Where mcpit keeps things

| What | Linux / macOS | Windows |
|---|---|---|
| Config and device key | `~/.config/mcpit/` | `%APPDATA%\mcpit\` |
| Sitepacks, decision log, status cache | `~/.local/share/mcpit/` | `%LOCALAPPDATA%\mcpit\` |

Override with `MCPIT_CONFIG_DIR` and `MCPIT_HOME`. Use another registry with `MCPIT_REGISTRY` or `mcpit config set registry <url>`.

## Safety

- Before each use of a registry pack, mcpit checks its signed hash and the registry status. De-listed, "bad" or "suspicious" sites are blocked until you confirm (`--force` in the CLI, a confirmation prompt in MCP clients).
- Tools that change data (write, payment, destructive) always ask first.
- A sitepack holds structure only: endpoints, parameters, the site guide and the site map. It never holds cookies, tokens, typed values or page content.

## Update and remove

Run the install command again to update. To remove: delete the `mcpit` binary, `~/.config/mcpit` and `~/.local/share/mcpit` (Windows: the two folders above).
