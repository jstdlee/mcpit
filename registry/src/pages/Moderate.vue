<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { RouterLink } from 'vue-router';
import { ago, api, enc, getToken, host, setToken } from '../api';

const props = defineProps<{ tab?: string }>();
const tab = computed(() => props.tab || 'keys');
const tabs = [
  ['keys', 'Device keys'],
  ['quarantine', 'Quarantine'],
  ['sites', 'Sites'],
  ['submissions', 'Submissions'],
  ['verifier', 'Verifier'],
  ['settings', 'Settings'],
  ['audit', 'Audit log'],
];

const token = ref(getToken());
const tokenInput = ref('');
const authed = ref(false);
const error = ref('');
const msg = ref('');
const overview = ref<any>(null);
const rows = ref<any[]>([]);
const settings = ref<Record<string, string>>({});
const model = ref('');
const keyFilter = ref('pending');
const busy = ref(false);
const vstats = ref<any[]>([]);

async function signIn() {
  setToken(tokenInput.value.trim());
  token.value = getToken();
  await load();
}
function signOut() {
  setToken('');
  token.value = '';
  authed.value = false;
}

async function load() {
  error.value = '';
  if (!token.value) return;
  try {
    overview.value = await api('/v1/admin/overview', { admin: true });
    authed.value = true;
    if (tab.value === 'keys')
      rows.value = (
        await api(`/v1/admin/keys${keyFilter.value ? '?state=' + keyFilter.value : ''}`, { admin: true })
      ).keys;
    if (tab.value === 'quarantine') rows.value = (await api('/v1/admin/quarantine?state=open', { admin: true })).items;
    if (tab.value === 'sites') rows.value = (await api('/v1/admin/sites', { admin: true })).sites;
    if (tab.value === 'submissions') rows.value = (await api('/v1/admin/submissions', { admin: true })).submissions;
    if (tab.value === 'audit') rows.value = (await api('/v1/admin/audit', { admin: true })).audit;
    if (tab.value === 'verifier') {
      const v = await api('/v1/admin/verifier', { admin: true });
      vstats.value = v.stats;
      rows.value = v.decisions;
    }
    if (tab.value === 'settings') {
      const s = await api('/v1/admin/settings', { admin: true });
      settings.value = s.settings;
      model.value = s.model;
    }
  } catch (e) {
    error.value = String(e);
    if (String(e).includes('token')) authed.value = false;
  }
}
onMounted(load);
watch([tab, keyFilter], load);

async function act(path: string, body: unknown, note: string) {
  busy.value = true;
  msg.value = '';
  try {
    await api(path, { method: 'POST', body: JSON.stringify(body), admin: true });
    msg.value = note;
    await load();
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}

async function saveSettings() {
  busy.value = true;
  try {
    await api('/v1/admin/settings', { method: 'PUT', body: JSON.stringify(settings.value), admin: true });
    msg.value = 'Settings saved';
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}

function delist(origin: string) {
  const reason = prompt('Reason for de-listing ' + origin) ?? '';
  if (reason) act(`/v1/admin/sites/${enc(origin)}`, { delist: reason }, 'Site de-listed');
}
function setExpiry(origin: string, v: string) {
  act(`/v1/admin/sites/${enc(origin)}`, { expire_days: v === '' ? null : Number(v) }, 'Expiry updated');
}
const labels: Record<string, string> = {
  expire_days: 'Expire days (default)',
  submit_per_key_per_hour: 'Submits per key per hour',
  submit_per_site_per_hour: 'Submits per site per hour',
  max_pack_bytes: 'Max sitepack size (bytes)',
  auto_promote: 'Auto-promote passing tools (true/false)',
  unsure_low: 'Decision model unsure band: low',
  unsure_high: 'Decision model unsure band: high',
  verify_sample_percent: 'LLM verifier sample (%)',
  auto_approve_keys: 'Auto-approve new device keys (true/false)',
  keys_per_network_per_day: 'Auto-approved keys per network per day',
};
</script>

<template>
  <section class="hero">
    <h1>Moderate</h1>
    <p>
      Device keys are approved automatically (revoke them here); review quarantined tools, and manage sites. The
      screening agent (Clef-flash) prepares every item; you decide.
    </p>
  </section>

  <div v-if="!authed" class="card">
    <div class="head"><h2>Moderator sign-in</h2></div>
    <div class="body row">
      <input
        id="mod-token"
        v-model="tokenInput"
        type="password"
        placeholder="Moderator token"
        aria-label="Moderator token"
        style="min-width: 280px"
        @keyup.enter="signIn"
      />
      <button class="btn primary" type="button" @click="signIn">Sign in</button>
      <span v-if="error" class="err">{{ error }}</span>
    </div>
    <div class="body muted small">The token stays in this browser only.</div>
  </div>

  <template v-else>
    <div class="stats">
      <RouterLink to="/moderate/keys" class="card stat"
        ><div class="muted small">Keys waiting</div>
        <div class="v">{{ overview.pendingKeys }}</div></RouterLink
      >
      <RouterLink to="/moderate/quarantine" class="card stat"
        ><div class="muted small">Quarantine</div>
        <div class="v">{{ overview.openQuarantine }}</div></RouterLink
      >
      <RouterLink to="/moderate/sites" class="card stat"
        ><div class="muted small">Sites</div>
        <div class="v">{{ overview.sites }}</div></RouterLink
      >
      <RouterLink to="/moderate/submissions" class="card stat"
        ><div class="muted small">Submits, 24 h</div>
        <div class="v">{{ overview.submissions24h }}</div></RouterLink
      >
    </div>
    <p v-if="overview.devKey" class="pill warn">
      The registry uses a development signing key. Set the SIGNING_KEY secret before production.
    </p>

    <div class="card">
      <nav class="tabs">
        <RouterLink v-for="[k, label] in tabs" :key="k" :to="'/moderate/' + k" :class="{ on: tab === k }">{{
          label
        }}</RouterLink>
        <span class="grow"></span>
        <a href="#" @click.prevent="signOut">Sign out</a>
      </nav>
      <div v-if="msg || error" class="body small" :class="error ? 'err' : 'muted'">{{ error || msg }}</div>

      <!-- keys -->
      <template v-if="tab === 'keys'">
        <div class="body row">
          <div class="seg" role="group" aria-label="Key state">
            <button
              v-for="s in ['pending', 'approved', 'rejected', 'revoked', '']"
              :key="s"
              type="button"
              :aria-pressed="keyFilter === s"
              @click="keyFilter = s"
            >
              {{ s || 'all' }}
            </button>
          </div>
        </div>
        <div v-if="!rows.length" class="empty">No keys.</div>
        <div v-else class="scroll">
          <table class="table">
            <thead>
              <tr>
                <th>Key</th>
                <th>Name</th>
                <th>Assistant note</th>
                <th class="num">Rep.</th>
                <th>Created</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="k in rows" :key="k.id">
                <td class="mono small">
                  {{ k.id }} <span class="pill grey">{{ k.state }}</span>
                </td>
                <td>{{ k.name }}</td>
                <td class="small muted">{{ k.note }} · {{ k.submissions }} submit(s)</td>
                <td class="num">{{ k.reputation }}</td>
                <td class="muted">{{ ago(k.created_at) }}</td>
                <td class="row">
                  <button
                    v-if="k.state !== 'approved'"
                    class="btn primary"
                    :disabled="busy"
                    @click="act(`/v1/admin/keys/${enc(k.id)}`, { action: 'approve' }, 'Key approved')"
                  >
                    Approve
                  </button>
                  <button
                    v-if="k.state === 'pending'"
                    class="btn danger"
                    :disabled="busy"
                    @click="act(`/v1/admin/keys/${enc(k.id)}`, { action: 'reject' }, 'Key rejected')"
                  >
                    Reject
                  </button>
                  <button
                    v-if="k.state === 'approved'"
                    class="btn danger"
                    :disabled="busy"
                    @click="act(`/v1/admin/keys/${enc(k.id)}`, { action: 'revoke' }, 'Key revoked')"
                  >
                    Revoke
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <!-- quarantine -->
      <template v-if="tab === 'quarantine'">
        <div v-if="!rows.length" class="empty">Nothing waits for review.</div>
        <div v-for="q in rows" :key="q.id" class="body" style="border-bottom: 1px solid var(--line)">
          <div class="row">
            <strong>{{ host(q.origin) }}</strong
            ><code>{{ q.tool_id }}</code>
            <span class="pill" :class="q.tool_id === '_guide' ? 'grey' : q.tool.effect === 'read' ? 'ok' : 'warn'">{{
              q.tool_id === '_guide' ? 'guide + site map' : q.tool.effect
            }}</span>
            <span class="muted small">{{ ago(q.created_at) }}</span>
            <span class="grow"></span>
            <button
              class="btn primary"
              :disabled="busy"
              @click="act(`/v1/admin/quarantine/${q.id}`, { action: 'approve' }, 'Tool approved and published')"
            >
              Approve
            </button>
            <button
              class="btn danger"
              :disabled="busy"
              @click="act(`/v1/admin/quarantine/${q.id}`, { action: 'reject' }, 'Tool rejected')"
            >
              Reject
            </button>
          </div>
          <p class="small">{{ q.reason }}</p>
          <pre class="note">{{ q.summary }}</pre>
        </div>
      </template>

      <!-- sites -->
      <template v-if="tab === 'sites'">
        <div v-if="!rows.length" class="empty">No sites.</div>
        <div v-else class="scroll">
          <table class="table">
            <thead>
              <tr>
                <th>Site</th>
                <th>State</th>
                <th>Verdict</th>
                <th>Expire days</th>
                <th class="num">Calls</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="s in rows" :key="s.origin">
                <td>
                  <RouterLink :to="'/site/' + enc(s.origin)">{{ host(s.origin) }}</RouterLink>
                  <div class="small muted">{{ s.delist_reason }}</div>
                </td>
                <td>
                  <span class="pill" :class="s.state === 'listed' ? 'ok' : s.state === 'expired' ? 'warn' : 'bad'">{{
                    s.state
                  }}</span>
                </td>
                <td>
                  <div class="seg" role="group" aria-label="Verdict">
                    <button
                      v-for="v in ['good', 'suspicious', 'bad']"
                      :key="v"
                      type="button"
                      :aria-pressed="s.verdict === v"
                      @click="
                        act(
                          `/v1/admin/sites/${enc(s.origin)}`,
                          { verdict: s.verdict === v ? null : v },
                          'Verdict saved',
                        )
                      "
                    >
                      {{ v }}
                    </button>
                  </div>
                </td>
                <td>
                  <input
                    :id="'exp-' + s.origin"
                    type="number"
                    min="1"
                    :value="s.expire_days ?? ''"
                    placeholder="default"
                    style="width: 90px"
                    @change="setExpiry(s.origin, ($event.target as HTMLInputElement).value)"
                  />
                </td>
                <td class="num">{{ s.calls }}</td>
                <td class="row">
                  <button
                    class="btn"
                    :disabled="busy"
                    @click="act(`/v1/admin/sites/${enc(s.origin)}/reverify`, {}, 'Re-check queued')"
                  >
                    Re-check
                  </button>
                  <button v-if="s.state !== 'delisted'" class="btn danger" :disabled="busy" @click="delist(s.origin)">
                    De-list
                  </button>
                  <button
                    v-else
                    class="btn"
                    :disabled="busy"
                    @click="act(`/v1/admin/sites/${enc(s.origin)}`, { relist: true }, 'Site listed again')"
                  >
                    Re-list
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <!-- submissions -->
      <template v-if="tab === 'submissions'">
        <div v-if="!rows.length" class="empty">No submissions.</div>
        <div v-else class="scroll">
          <table class="table">
            <thead>
              <tr>
                <th>When</th>
                <th>Site</th>
                <th>Outcome</th>
                <th>Reason</th>
                <th class="hide-sm">Key</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="s in rows" :key="s.id">
                <td class="muted">{{ ago(s.created_at) }}</td>
                <td>{{ host(s.origin) }}</td>
                <td>
                  <span class="pill">{{ s.outcome ?? s.state }}</span>
                </td>
                <td class="small">{{ s.reason }}</td>
                <td class="hide-sm mono small">{{ s.key_id }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <!-- verifier -->
      <template v-if="tab === 'verifier'">
        <div class="body muted small">
          The LLM verifier checks every escalated decision and a sample of the rest. It never changes a decision;
          disagreements show first.
        </div>
        <div v-if="vstats.length" class="body row">
          <span v-for="s in vstats" :key="s.point" class="pill grey"
            >{{ s.point }}: {{ s.agreed }}/{{ s.checked }} agree</span
          >
        </div>
        <div v-if="!rows.length" class="empty">No checked decisions yet.</div>
        <div v-else class="scroll">
          <table class="table">
            <thead>
              <tr>
                <th>Tool</th>
                <th>Point</th>
                <th>Clef-flash</th>
                <th>Verifier</th>
                <th>Reason</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="d in rows" :key="d.id">
                <td>
                  {{ host(d.origin) }} <code>{{ d.subject }}</code>
                </td>
                <td>
                  <code>{{ d.point }}</code>
                </td>
                <td>{{ d.answer }}</td>
                <td>
                  <span class="pill" :class="d.verify_agree ? 'ok' : 'bad'">{{
                    d.verify_agree ? 'agrees' : 'disagrees'
                  }}</span>
                  {{ d.verify_answer }}
                </td>
                <td class="small">{{ d.verify_reason }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <!-- settings -->
      <template v-if="tab === 'settings'">
        <div class="body" style="display: grid; gap: 10px; max-width: 560px">
          <div class="muted small">
            Decision model: <code>{{ model }}</code>
          </div>
          <label v-for="(v, k) in settings" :key="k" class="row">
            <span class="grow">{{ labels[k] ?? k }}</span>
            <input :id="'set-' + k" v-model="settings[k]" style="width: 140px" />
          </label>
          <div><button class="btn primary" :disabled="busy" @click="saveSettings">Save settings</button></div>
        </div>
      </template>

      <!-- audit -->
      <template v-if="tab === 'audit'">
        <div v-if="!rows.length" class="empty">No actions yet.</div>
        <div v-else class="scroll">
          <table class="table">
            <thead>
              <tr>
                <th>When</th>
                <th>Action</th>
                <th>Target</th>
                <th>Detail</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="a in rows" :key="a.id">
                <td class="muted">{{ ago(a.created_at) }}</td>
                <td>
                  <code>{{ a.action }}</code>
                </td>
                <td>{{ a.target }}</td>
                <td class="small mono">{{ a.detail }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </div>
  </template>
</template>
