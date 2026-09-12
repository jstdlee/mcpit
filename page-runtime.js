(() => {
  if (globalThis.__mcpfier_v1) return;
  const adapters = new Map();
  const failures = [];
  let documentId = crypto.randomUUID(), lastUrl = location.href;
  const context = () => document.modelContext || navigator.modelContext || null;
  function transport() { const ctx = context(); return ctx && typeof ctx.getTools === 'function' && typeof ctx.executeTool === 'function' ? 'webmcp' : 'mcpfier-compat'; }
  const isVisible = el => el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
  const clean = text => String(text || '').replace(/[\t ]+/g, ' ').replace(/\n{3,}/g, '\n\n').trim();
  function identity() {
    if (location.href !== lastUrl) {
      for (const adapter of adapters.values()) adapter.controller?.abort();
      adapters.clear(); lastUrl = location.href; documentId = crypto.randomUUID();
    }
    return documentId;
  }
  async function fingerprint(value) {
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(JSON.stringify(value)));
    return Array.from(new Uint8Array(digest), x => x.toString(16).padStart(2, '0')).join('');
  }
  function selector(el) {
    if (el.id) return `#${CSS.escape(el.id)}`;
    const parts = [];
    while (el && el !== document.body) {
      const tag = el.localName;
      const peers = Array.from(el.parentElement?.children || []).filter(x => x.localName === tag);
      parts.unshift(`${tag}:nth-of-type(${peers.indexOf(el) + 1})`);
      el = el.parentElement;
    }
    return 'body > ' + parts.join(' > ');
  }
  function analyze() {
    identity();
    const candidates = [];
    const main = Array.from(document.querySelectorAll('main,article,[role="main"]')).find(isVisible);
    if (main && clean(main.innerText).length > 20) candidates.push({
      id: 'main', kind: 'text', selector: selector(main), label: 'Read main content',
      name: 'mcpfier.read_main', description: 'Read the currently loaded main article text.',
    });
    Array.from(document.querySelectorAll('table')).filter(isVisible).slice(0, 12).forEach((el, index) => {
      const title = clean(el.caption?.innerText || el.getAttribute('aria-label') || `Table ${index + 1}`).slice(0, 100);
      candidates.push({ id: `table_${index}`, kind: 'table', selector: selector(el), label: `Read ${title}`,
        name: `mcpfier.read_table_${index + 1}`, description: `Read visible rows from ${title}.` });
    });
    return { url: location.href, title: document.title, documentId, runtime: transport(), candidates };
  }
  function handler(recipe) {
    const expectedUrl = location.href;
    return async (args = {}) => {
      if (location.href !== expectedUrl) throw new Error('STALE_ADAPTER: route changed.');
      const el = document.querySelector(recipe.selector);
      if (!isVisible(el)) throw new Error('ADAPTER_UNAVAILABLE: selected content is no longer visible.');
      if (recipe.kind === 'table') {
        const limit = args.limit ?? 50;
        if (!Number.isInteger(limit) || limit < 1 || limit > 200) throw new Error('limit must be an integer from 1 to 200.');
        const allRows = Array.from(el.rows).filter(isVisible);
        const rows = allRows.slice(0, limit).map(row => Array.from(row.cells).filter(isVisible).map(cell => clean(cell.innerText).slice(0, 2000)));
        return { url: location.href, rows, truncated: allRows.length > limit, capturedAt: new Date().toISOString() };
      }
      const maxChars = args.maxChars ?? 12000;
      if (!Number.isInteger(maxChars) || maxChars < 100 || maxChars > 30000) throw new Error('maxChars must be an integer from 100 to 30000.');
      // Walk visible source text; do not collect form values or detached hidden text.
      const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
      const texts = [];
      let node;
      while ((node = walker.nextNode())) {
        const parent = node.parentElement;
        if (isVisible(parent) && !parent.closest('script,style,nav,aside,form,input,textarea,select,button,[contenteditable],[hidden],[aria-hidden="true"]')) texts.push(node.textContent);
      }
      const text = clean(texts.join('\n'));
      return { url: location.href, title: document.title, text: text.slice(0, maxChars), truncated: text.length > maxChars, capturedAt: new Date().toISOString() };
    };
  }
  async function nativeTools() {
    if (transport() !== 'webmcp') return [];
    const list = await context().getTools();
    // Top document only in v0.1; do not accidentally mix iframe tool identities.
    return list.filter(tool => !tool.window || tool.window === window);
  }
  async function configure(recipes) {
    identity(); failures.length = 0;
    const wanted = new Map(recipes.map(r => [r.id, r]));
    for (const [id, item] of adapters) {
      if (!wanted.has(id) || JSON.stringify(item.recipe) !== JSON.stringify(wanted.get(id))) { item.controller?.abort(); adapters.delete(id); }
    }
    let existing;
    try { existing = await nativeTools(); } catch (error) { failures.push(error.message); existing = []; }
    for (const recipe of recipes) {
      if (adapters.has(recipe.id)) continue;
      if (!['text', 'table'].includes(recipe.kind) || !/^mcpfier\.[a-z0-9_]+$/.test(recipe.name)) { failures.push('Invalid adapter recipe.'); continue; }
      let el;
      try { el = document.querySelector(recipe.selector); } catch { failures.push('Invalid selector.'); continue; }
      if (!el || !isVisible(el)) { failures.push(`${recipe.name}: selector no longer matches visible content.`); continue; }
      if (existing.some(t => t.name === recipe.name)) { failures.push(`${recipe.name}: native tool name conflict.`); continue; }
      const table = recipe.kind === 'table';
      const definition = {
        name: recipe.name, description: recipe.description,
        inputSchema: { type: 'object', properties: table ? { limit: { type: 'integer', minimum: 1, maximum: 200, default: 50 } } : { maxChars: { type: 'integer', minimum: 100, maximum: 30000, default: 12000 } }, additionalProperties: false },
        annotations: { readOnlyHint: true, untrustedContentHint: true }, execute: handler(recipe),
      };
      const controller = new AbortController();
      let registered = false;
      if (transport() === 'webmcp' && typeof context().registerTool === 'function') {
        try { await context().registerTool(definition, { signal: controller.signal }); registered = true; }
        catch (error) { failures.push(`${recipe.name}: WebMCP registration failed: ${error.message}`); continue; }
      }
      adapters.set(recipe.id, { recipe, definition, controller, registered });
    }
    return discover();
  }
  async function discover() {
    identity();
    let native = [];
    const errors = [...failures];
    try { native = await nativeTools(); } catch (error) { errors.push(`WebMCP discovery failed: ${error.message}`); }
    const currentAdapters = [...adapters.values()];
    const list = native.map(t => {
      const adapter = currentAdapters.find(a => a.registered && a.definition.name === t.name);
      return { raw: t, source: adapter ? 'adapter' : 'native', transport: 'webmcp' };
    });
    for (const item of currentAdapters.filter(a => !a.registered)) list.push({ raw: item.definition, source: 'adapter', transport: 'mcpfier-compat' });
    const tools = [];
    for (const t of list.slice(0, 200)) {
      const raw = t.raw;
      const inputSchema = typeof raw.inputSchema === 'string' ? JSON.parse(raw.inputSchema) : raw.inputSchema || { type: 'object', additionalProperties: false };
      const descriptor = { id: `${t.source}:${raw.name}`, name: raw.name, description: raw.description || '', inputSchema, annotations: { ...raw.annotations }, source: t.source, transport: t.transport };
      tools.push({ ...descriptor, fingerprint: await fingerprint(descriptor) });
    }
    return { url: location.href, title: document.title, documentId, runtime: transport(), tools, errors, selected: currentAdapters.map(a => a.recipe.id) };
  }
  async function call(args) {
    const snapshot = await discover();
    if (args.documentId !== snapshot.documentId) throw new Error('STALE_DOCUMENT');
    const tool = snapshot.tools.find(t => t.id === args.toolId);
    if (!tool || tool.fingerprint !== args.fingerprint) throw new Error('TOOL_CHANGED');
    if (tool.annotations.readOnlyHint !== true) throw new Error('READ_ONLY_RELEASE');
    let result;
    if (tool.transport === 'webmcp') {
      const registered = (await nativeTools()).find(t => t.name === tool.name);
      if (!registered) throw new Error('TOOL_UNAVAILABLE');
      // Chromium 153's implementation takes serialized JSON, despite the newer
      // draft describing an object. Do not retry a real tool with another dialect.
      result = await context().executeTool(registered, JSON.stringify(args.arguments || {}), { signal: AbortSignal.timeout(20000) });
    } else {
      const item = [...adapters.values()].find(a => a.definition.name === tool.name);
      result = await item.definition.execute(args.arguments || {});
    }
    const serialized = typeof result === 'string' ? result : JSON.stringify(result ?? null);
    if (serialized.length > 200000) return { result: serialized.slice(0, 200000), truncated: true };
    return { result, truncated: false };
  }
  Object.defineProperty(globalThis, '__mcpfier_v1', { value: Object.freeze({ analyze, configure, discover, call }), configurable: false });
})();
