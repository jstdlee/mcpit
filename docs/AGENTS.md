# mcpit for agents: MCP server, skill and CLI

An agent can use mcpit in two ways:

- **MCP server** (`mcpit serve`, stdio): the agent gets the tools listed below. This is the best way.
- **CLI with the skill**: the agent runs `mcpit …` in its shell. The skill tells it when and how.

Install mcpit first ([INSTALL.md](INSTALL.md)).

## One command

```bash
mcpit setup <agent> [--scope project|user] [--no-skill] [--env KEY=VALUE]
```

It adds the `mcpit` MCP server to the agent's config, keeps the other servers, and writes the skill. `project` (the default) changes only the current folder; `user` changes it for all projects.

| Agent | `mcpit setup …` writes | By hand |
|---|---|---|
| oh-my-pi (`omp`) | `.omp/mcp.json` and `.omp/skills/mcpit/SKILL.md` (user: `~/.omp/agent/…`) | — |
| Claude Code (`claude`) | `.mcp.json` and `.claude/skills/mcpit/SKILL.md` (user: skill in `~/.claude/skills/`) | user scope: `claude mcp add --scope user mcpit -- mcpit serve` |
| Cursor (`cursor`) | `.cursor/mcp.json` (user: `~/.cursor/mcp.json`) | — |
| VS Code agent mode (`vscode`) | `.vscode/mcp.json` | user scope: "MCP: Add Server" → command `mcpit serve` |
| Gemini CLI (`gemini`) | `.gemini/settings.json` (user: `~/.gemini/settings.json`) | — |
| OpenAI Codex CLI (`codex`) | skill in `.codex/skills/mcpit/` | MCP: add the block below to `~/.codex/config.toml` |

Codex `~/.codex/config.toml`:

```toml
[mcp_servers.mcpit]
command = "mcpit"
args = ["serve"]
```

Any other MCP client:

```json
{ "mcpServers": { "mcpit": { "command": "mcpit", "args": ["serve"] } } }
```

Restart the agent after setup. Test it with a prompt such as: *"Use mcpit to find the latest version of the Python package requests on pypi.org."*

## MCP tools

| Tool | Use |
|---|---|
| `mcpit_find` | Tools for a site (local store, then the registry); with `task`, the best tool. Shows integrity alerts. |
| `mcpit_tools` | Each tool's input schema. |
| `mcpit_guide` | The site's guide (llms.txt, robots.txt, agent card, API catalog, meta, feeds) and site map. |
| `mcpit_call` | Call one tool. Write tools and blocked sites ask the user (MCP elicitation); without elicitation the call is refused. |
| `mcpit_explore` | Explore a site now (30 s to a few minutes). |
| `mcpit_submit` | Share the local sitepack with the registry (asks the user first). |
| `mcpit_report` | Report a broken tool. |

`mcpit serve --http 127.0.0.1:7801` serves the same tools over streamable HTTP (localhost only).

## Alerts the agent must obey

- `⛔ MCPIT ALERT (blocked)`: the pack failed its signed-hash check, or the registry de-listed the site or marked it "bad" or "suspicious". The agent must not use the site's tools on its own; it shows the alert and waits for explicit confirmation.
- `⚠ MCPIT ALERT (warning)`: for example an expired registry version. The agent may continue and tells the user.

## The skill

[`cli/internal/agentsetup/SKILL.md`](../cli/internal/agentsetup/SKILL.md) is the skill that `mcpit setup` writes. To place it yourself:

```bash
mcpit setup skill --dir ~/.claude/skills/mcpit     # or any skills folder your agent reads
```

## Isolated test setup

To test without touching your normal store or key, give the MCP server its own folders:

```bash
mcpit setup omp --env MCPIT_HOME=$PWD/.mcpit/home --env MCPIT_CONFIG_DIR=$PWD/.mcpit/config
```
