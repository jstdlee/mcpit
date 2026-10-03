let socket, heartbeat;
let connecting = false;
const busy = new Map();
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
const keyFor = raw => new URL(raw).href;
function validateUrl(raw) {
  const url = new URL(raw);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error('Only HTTP and HTTPS pages are supported.');
  return url;
}
async function allowed(raw) {
  const url = validateUrl(raw);
  return chrome.permissions.contains({ origins: [`${url.origin}/*`] });
}
async function enabled(raw) {
  const url = validateUrl(raw);
  const { sites = [] } = await chrome.storage.local.get('sites');
  return sites.includes(url.origin) && await allowed(raw);
}
async function getTab(tabId) {
  const tab = await chrome.tabs.get(tabId);
  if (tab.incognito) throw new Error('Incognito pages are not supported.');
  if (!tab.url || !await enabled(tab.url)) throw new Error('SITE_NOT_ENABLED: Open MCPfier on the target page and enable this site.');
  return tab;
}
async function evaluate(tabId, method, args) {
  const results = await chrome.scripting.executeScript({ target: { tabId }, world: 'MAIN', func: async (method, args) => {
    try { return { result: await globalThis.__mcpfier_v1[method](args) }; }
    catch (error) { return { error: error.message }; }
  }, args: [method, args ?? null] });
  const output = results[0]?.result;
  if (!output) throw new Error('The page returned no result. It may be navigating.');
  if (output.error) throw new Error(output.error);
  return output.result;
}
async function prepare(tabId) {
  const tab = await getTab(tabId);
  await chrome.scripting.executeScript({ target: { tabId }, world: 'MAIN', files: ['page-runtime.js'] });
  const { adapters = {} } = await chrome.storage.local.get('adapters');
  await evaluate(tabId, 'configure', adapters[keyFor(tab.url)] || []);
}
function serialized(tabId, fn) {
  const previous = busy.get(tabId) || Promise.resolve();
  const current = previous.catch(() => {}).then(fn);
  busy.set(tabId, current);
  current.finally(() => { if (busy.get(tabId) === current) busy.delete(tabId); }).catch(() => {});
  return current;
}
async function execute(action, payload = {}) {
  if (action === 'tabs') {
    const result = [];
    for (const tab of await chrome.tabs.query({})) if (tab.url && !tab.incognito && await enabled(tab.url).catch(() => false)) result.push({ tabId: tab.id, url: tab.url, title: tab.title });
    return { tabs: result };
  }
  if (action === 'visit') {
    validateUrl(payload.url);
    if (!await enabled(payload.url)) throw new Error('SITE_NOT_ENABLED: Open the target site in Chrome and enable it in MCPfier first.');
    const tabs = await chrome.tabs.query({});
    let tab = tabs.find(t => !t.incognito && t.url === payload.url);
    if (!tab) tab = await chrome.tabs.create({ url: payload.url, active: true });
    for (let i = 0; i < 80; i++) {
      const fresh = await chrome.tabs.get(tab.id);
      if (fresh.status === 'complete') { await getTab(tab.id); return { tabId: tab.id, url: fresh.url, next: 'Call mcpfier_discover even if the map had no match.' }; }
      await wait(200);
    }
    throw new Error('PAGE_LOAD_TIMEOUT');
  }
  if (!['analyze', 'discover', 'call', 'select'].includes(action)) throw new Error('Unsupported browser command.');
  return serialized(payload.tabId, async () => {
    await prepare(payload.tabId);
    if (action === 'select') {
      const analyzed = await evaluate(payload.tabId, 'analyze');
      if (analyzed.documentId !== payload.documentId) throw new Error('The page has changed. Analyze it again.');
      const recipes = analyzed.candidates.filter(c => payload.ids.includes(c.id));
      const { adapters = {} } = await chrome.storage.local.get('adapters');
      adapters[keyFor(analyzed.url)] = recipes;
      await chrome.storage.local.set({ adapters });
      const result = await evaluate(payload.tabId, 'configure', recipes);
      send({ type: 'snapshot', snapshot: result });
      return { ...result, tabId: payload.tabId };
    }
    const result = await evaluate(payload.tabId, action, action === 'call' ? payload : null);
    if (action === 'discover') send({ type: 'snapshot', snapshot: result });
    return { ...result, tabId: payload.tabId };
  });
}
function send(message) { if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(message)); }
async function connect() {
  if (connecting || socket?.readyState === WebSocket.OPEN || socket?.readyState === WebSocket.CONNECTING) return;
  connecting = true;
  try {
    const { connection } = await chrome.storage.local.get('connection');
    if (!connection?.token) return;
    const url = new URL(connection.url);
    if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port) throw new Error('The bridge must use http://127.0.0.1:port.');
    url.protocol = 'ws:'; url.pathname = '/extension'; url.search = ''; url.searchParams.set('token', connection.token);
    const ws = socket = new WebSocket(url.href);
    ws.onopen = () => {
      chrome.action.setBadgeText({ text: '' });
      clearInterval(heartbeat); heartbeat = setInterval(() => send({ type: 'ping' }), 20000);
    };
    ws.onmessage = async event => {
      let message;
      try {
        message = JSON.parse(event.data);
        if (!message.id) return;
        if (!['tabs', 'visit', 'discover', 'analyze', 'call'].includes(message.action)) throw new Error('Unsupported agent operation.');
        const result = await execute(message.action, message.payload);
        if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ id: message.id, result }));
      } catch (error) { if (message?.id && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ id: message.id, error: error.message })); }
    };
    ws.onerror = () => {};
    ws.onclose = () => { if (socket === ws) { clearInterval(heartbeat); socket = null; chrome.action.setBadgeText({ text: 'OFF' }); } };
  } finally { connecting = false; }
}
chrome.runtime.onMessage.addListener((message, sender, respond) => {
  // Only packaged extension UI may configure the bridge or choose adapters.
  if (sender.id !== chrome.runtime.id || !sender.url?.startsWith(chrome.runtime.getURL(''))) return;
  (async () => {
    if (message.action === 'connection') return { connected: socket?.readyState === WebSocket.OPEN };
    if (message.action === 'reconnect') { const old = socket; socket = null; old?.close(); clearInterval(heartbeat); await connect(); return {}; }
    return execute(message.action, message.payload);
  })().then(result => respond({ result })).catch(error => respond({ error: error.message }));
  return true;
});
chrome.tabs.onUpdated.addListener((tabId, change) => {
  if (change.status === 'complete' || change.url) execute('discover', { tabId }).catch(() => {});
});
chrome.tabs.onActivated.addListener(({ tabId }) => execute('discover', { tabId }).catch(() => {}));
chrome.alarms.onAlarm.addListener(alarm => { if (alarm.name === 'reconnect') connect().catch(() => {}); });
chrome.runtime.onStartup.addListener(() => connect().catch(() => {}));
chrome.runtime.onInstalled.addListener(() => chrome.alarms.create('reconnect', { periodInMinutes: 0.5 }));
chrome.alarms.create('reconnect', { periodInMinutes: 0.5 });
connect().catch(() => {});
