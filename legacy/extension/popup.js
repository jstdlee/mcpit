const $ = id => document.getElementById(id);
let currentTab, analysis;
function message(text, error = false) { $('message').textContent = text; $('message').classList.toggle('error', error); }
async function send(action, payload = {}) { const data = await chrome.runtime.sendMessage({ action, payload }); if (data.error) throw new Error(data.error); return data.result; }
function element(tag, text, className) { const el = document.createElement(tag); if (text) el.textContent = text; if (className) el.className = className; return el; }
function render(snapshot, analyzed) {
  analysis = analyzed;
  $('runtime').textContent = snapshot.runtime === 'webmcp' ? 'WebMCP' : 'Compatibility';
  $('tools').replaceChildren();
  if (!snapshot.tools.length) $('tools').append(element('p', 'No tools available yet. Select capabilities below to expose them.', 'empty'));
  for (const tool of snapshot.tools) {
    const row = element('div', null, 'tool');
    row.append(element('strong', tool.name), element('p', `${tool.source === 'native' ? 'Native' : 'Adapted'} · ${tool.annotations.readOnlyHint === true ? 'Read-only' : 'Unavailable in this release'} · ${tool.description}`));
    $('tools').append(row);
  }
  $('candidates').replaceChildren();
  for (const candidate of analyzed.candidates) {
    const label = element('label', null, 'candidate');
    const checkbox = element('input'); checkbox.type = 'checkbox'; checkbox.value = candidate.id; checkbox.checked = snapshot.selected.includes(candidate.id);
    const text = element('span', candidate.label); text.append(element('small', candidate.name)); label.append(checkbox, text); $('candidates').append(label);
  }
  if (!analyzed.candidates.length) $('candidates').append(element('p', 'No supported main content or tables found.', 'empty'));
  $('apply').disabled = false;
  if (snapshot.errors.length) message(snapshot.errors.join('\n'), true);
}
async function refresh() {
  message('Discovering page tools…');
  const snapshot = await send('discover', { tabId: currentTab.id });
  const analyzed = await send('analyze', { tabId: currentTab.id });
  message('Page capabilities updated.');
  render(snapshot, analyzed);
}
async function attempt(fn) { try { await fn(); } catch (error) { message(error.message, true); } }
$('enable').onclick = () => attempt(async () => {
  const origin = new URL(currentTab.url).origin;
  const granted = await chrome.permissions.request({ origins: [`${origin}/*`] });
  if (!granted) throw new Error('Access to this site has not been granted.');
  const { sites = [] } = await chrome.storage.local.get('sites');
  await chrome.storage.local.set({ sites: [...new Set([...sites, origin])] });
  await refresh();
});
$('refresh').onclick = () => attempt(refresh);
$('apply').onclick = () => attempt(async () => {
  const ids = [...$('candidates').querySelectorAll('input:checked')].map(el => el.value);
  const snapshot = await send('select', { tabId: currentTab.id, documentId: analysis.documentId, ids });
  render(snapshot, analysis); message(`Applied ${ids.length} capabilities. Your agent can discover and use them.`);
});
$('disable').onclick = () => attempt(async () => {
  const origin = new URL(currentTab.url).origin;
  const { sites = [], adapters = {} } = await chrome.storage.local.get(['sites', 'adapters']);
  // Unregister tools in each still-enabled tab before revoking access.
  for (const tab of await chrome.tabs.query({})) {
    if (tab.url?.startsWith(origin + '/')) {
      try { const fresh = await send('analyze', { tabId: tab.id }); await send('select', { tabId: tab.id, documentId: fresh.documentId, ids: [] }); } catch { /* A navigating tab will lose document tools. */ }
    }
  }
  for (const url of Object.keys(adapters)) if (new URL(url).origin === origin) delete adapters[url];
  await chrome.storage.local.set({ sites: sites.filter(s => s !== origin), adapters });
  // Chrome cannot remove the mandatory loopback bridge host permission.
  // The independent sites allowlist already blocks all future page operations.
  await chrome.permissions.remove({ origins: [`${origin}/*`] }).catch(() => false);
  $('tools').replaceChildren(element('p', 'Disabled. Existing map entries are retained, but your agent cannot access this site.', 'empty'));
  $('candidates').replaceChildren(); $('apply').disabled = true; message('Site disabled.');
});
await attempt(async () => {
  const status = await send('connection');
  $('connection').textContent = status.connected ? 'Bridge connected' : 'Bridge offline';
  $('connection').classList.toggle('online', status.connected);
  // Optional inspector URL opens the same UI in a tab for accessibility and QA.
  const target = new URL(location.href).searchParams.get('tabId');
  if (target && /^\d+$/.test(target)) currentTab = await chrome.tabs.get(Number(target));
  else [currentTab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!currentTab?.url || !/^https?:/.test(currentTab.url)) throw new Error('Open an HTTP or HTTPS website, then open MCPfier.');
  $('site').textContent = new URL(currentTab.url).hostname;
  const { sites = [] } = await chrome.storage.local.get('sites');
  if (sites.includes(new URL(currentTab.url).origin)) await refresh();
});
