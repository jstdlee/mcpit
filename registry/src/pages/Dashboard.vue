<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import { RouterLink } from 'vue-router';
import { ago, api, enc, host } from '../api';

interface Site {
  origin: string;
  version: string;
  state: string;
  verdict: string | null;
  stars: number;
  calls: number;
  successRate: number | null;
  agreements: number;
  trust: number;
  week: number;
  tools: number;
  verified_at: string | null;
}

const sorts = [
  ['used', 'Most used'],
  ['trend', 'Trending'],
  ['stars', 'Stars'],
  ['trust', 'Trust'],
  ['new', 'Newest'],
] as const;
const sort = ref<string>('used');
const q = ref('');
const sites = ref<Site[]>([]);
const loading = ref(true);
const error = ref('');

async function load() {
  loading.value = true;
  error.value = '';
  try {
    sites.value = (await api<{ sites: Site[] }>(`/v1/sites?sort=${sort.value}&q=${encodeURIComponent(q.value)}`)).sites;
  } catch (e) {
    error.value = String(e);
  } finally {
    loading.value = false;
  }
}
onMounted(load);
watch(sort, load);
let t: ReturnType<typeof setTimeout>;
watch(q, () => {
  clearTimeout(t);
  t = setTimeout(load, 250);
});
</script>

<template>
  <section class="hero">
    <h1>Websites as agent tools, explored once and shared</h1>
    <p>
      Each site below has a signed sitepack: its search, forms and APIs as MCP tools. Agents that run
      <code>mcpit</code> use these packs directly, with no slow first visit. The registry checks every submit before it
      goes live.
    </p>
  </section>

  <div class="card">
    <div class="head">
      <div class="seg" role="group" aria-label="Sort">
        <button v-for="[k, label] in sorts" :key="k" type="button" :aria-pressed="sort === k" @click="sort = k">
          {{ label }}
        </button>
      </div>
      <div class="grow"></div>
      <input id="site-search" v-model="q" type="search" placeholder="Filter sites" aria-label="Filter sites" />
    </div>
    <div v-if="error" class="empty err">{{ error }}</div>
    <div v-else-if="!loading && sites.length === 0" class="empty">
      No sites yet. Run <code>mcpit explore &lt;url&gt;</code> and <code>mcpit submit</code> to add one.
    </div>
    <div v-else class="scroll">
      <table class="table">
        <thead>
          <tr>
            <th>Site</th>
            <th class="hide-sm">Tools</th>
            <th>Trust</th>
            <th class="num">Calls</th>
            <th class="num hide-sm">7 days</th>
            <th class="num hide-sm">Success</th>
            <th class="num">Stars</th>
            <th class="hide-sm">Verified</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in sites" :key="s.origin">
            <td>
              <RouterLink :to="'/site/' + enc(s.origin)">{{ host(s.origin) }}</RouterLink>
              <span v-if="s.verdict === 'good'" class="pill ok" style="margin-left: 6px">verified</span>
              <span v-if="s.state === 'expired'" class="pill warn" style="margin-left: 6px">expired</span>
              <span v-if="s.verdict === 'suspicious'" class="pill bad" style="margin-left: 6px">suspicious</span>
            </td>
            <td class="hide-sm">{{ s.tools }}</td>
            <td>
              <span class="trust"
                ><span class="bar"><span :style="{ width: s.trust + '%' }"></span></span>{{ s.trust }}</span
              >
            </td>
            <td class="num">{{ s.calls }}</td>
            <td class="num hide-sm">{{ s.week }}</td>
            <td class="num hide-sm">{{ s.successRate === null ? '—' : Math.round(s.successRate * 100) + '%' }}</td>
            <td class="num">{{ s.stars }}</td>
            <td class="hide-sm muted">{{ ago(s.verified_at) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
