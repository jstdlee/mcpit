const $ = id => document.getElementById(id);
const { connection, sites = [] } = await chrome.storage.local.get(['connection', 'sites']);
if (connection) { $('url').value = connection.url; $('token').value = connection.token; }
$('sites').textContent = sites.length ? sites.join(' · ') : 'No sites enabled yet. Open MCPfier on a website to enable it.';
async function status() {
  const output = await chrome.runtime.sendMessage({ action: 'connection' });
  $('status').textContent = output.result?.connected ? 'Connected' : 'Offline';
  $('status').classList.toggle('online', Boolean(output.result?.connected));
}
$('form').onsubmit = async event => {
  event.preventDefault();
  try {
    const url = new URL($('url').value);
    if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port || url.username || url.password) throw new Error('Use a local bridge at http://127.0.0.1:port.');
    const token = $('token').value.trim();
    if (!/^[a-f0-9]{64}$/.test(token)) throw new Error('Paste the complete pairing token shown by npm run pair.');
    await chrome.storage.local.set({ connection: { url: url.origin, token } });
    const result = await chrome.runtime.sendMessage({ action: 'reconnect' });
    if (result.error) throw new Error(result.error);
    $('demo').href = url.origin + '/demo/legacy';
    $('message').textContent = 'Settings saved. If offline, check that npm start is running and the pairing token matches.';
    $('message').classList.remove('error');
  } catch (error) { $('message').textContent = error.message; $('message').classList.add('error'); }
};
await status(); setInterval(status, 2000);
