<script setup lang="ts">
const base = location.origin;
const mcp = JSON.stringify({ mcpServers: { mcpit: { command: 'mcpit', args: ['serve'] } } }, null, 2);
</script>

<template>
  <section class="hero">
    <h1>Use mcpit</h1>
    <p>
      mcpit is one Go binary: a CLI and an MCP server. It explores a site once, saves a sitepack, and reuses it. It
      pulls signed packs from this registry when they exist.
    </p>
  </section>
  <div class="card">
    <div class="head"><h2>1. Add the MCP server to your agent</h2></div>
    <div class="body">
      <pre class="note">{{ mcp }}</pre>
      <p class="muted small">Claude Code: <code>claude mcp add mcpit -- mcpit serve</code></p>
    </div>
  </div>
  <div class="card">
    <div class="head"><h2>2. Explore and call from the terminal</h2></div>
    <div class="body">
      <pre class="note">
mcpit explore https://www.example.com/      # slow, once; depth ≤ 2
mcpit tools example.com
mcpit call example.com search --args '{"q":"lamp"}'</pre>
    </div>
  </div>
  <div class="card">
    <div class="head"><h2>3. Share a sitepack</h2></div>
    <div class="body">
      <pre class="note">
mcpit config set registry {{ base }}   (default: https://mcpit-registry.jstdlee.workers.dev)
mcpit key init          # a moderator approves new device keys
mcpit submit example.com
mcpit status &lt;id&gt;</pre>
      <p class="muted small">
        A sitepack holds endpoints, parameters and flows. It never holds cookies, tokens, typed values or page content.
      </p>
    </div>
  </div>
</template>
