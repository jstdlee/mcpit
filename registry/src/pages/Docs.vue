<script setup lang="ts">
const base = location.origin;
const mcp = JSON.stringify({ mcpServers: { mcpit: { command: 'mcpit', args: ['serve'] } } }, null, 2);
</script>

<template>
  <section class="hero">
    <h1>Use mcpit</h1>
    <p>
      mcpit is one Go binary for Linux, macOS and Windows: a CLI and an MCP server. It explores a site once, saves a
      sitepack, and reuses it. It pulls signed packs from this registry when they exist.
    </p>
  </section>
  <div class="card">
    <div class="head"><h2>1. Install</h2></div>
    <div class="body">
      <pre class="note">
# Linux, macOS
curl -fsSL https://github.com/jstdlee/mcpit/releases/latest/download/install.sh | sh
# Windows PowerShell
irm https://github.com/jstdlee/mcpit/releases/latest/download/install.ps1 | iex
# Go 1.27+
go install github.com/jstdlee/mcpit/cli/cmd/mcpit@latest</pre>
      <p class="muted small">
        Exploring needs Chrome, Chromium or Edge. Full guide:
        <a href="https://github.com/jstdlee/mcpit/blob/main/docs/INSTALL.md" target="_blank" rel="noopener"
          >INSTALL.md</a
        >
      </p>
    </div>
  </div>
  <div class="card">
    <div class="head"><h2>2. Add it to your agent</h2></div>
    <div class="body">
      <pre class="note">
mcpit setup omp        # or: claude, codex, cursor, gemini, vscode
# any MCP client:
{{ mcp }}</pre>
      <p class="muted small">
        Agent guide and skill:
        <a href="https://github.com/jstdlee/mcpit/blob/main/docs/AGENTS.md" target="_blank" rel="noopener">AGENTS.md</a>
      </p>
    </div>
  </div>
  <div class="card">
    <div class="head"><h2>3. Explore, call and share</h2></div>
    <div class="body">
      <pre class="note">
mcpit explore https://www.example.com/      # slow, once; depth ≤ 2
mcpit call example.com search_api --args '{"q":"lamp"}'
mcpit key init                              # approved automatically
mcpit submit example.com
mcpit status &lt;id&gt;</pre>
      <p class="muted small">
        Registry: {{ base }}. A sitepack holds endpoints, parameters, the site guide and the site map — never cookies,
        tokens, typed values or page content.
      </p>
    </div>
  </div>
</template>
