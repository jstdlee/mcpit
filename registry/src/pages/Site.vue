<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { ago, api, enc, host } from '../api';

const props = defineProps<{ origin: string }>();
const origin = computed(() => decodeURIComponent(props.origin));
const data = ref<any>(null);
const error = ref('');

onMounted(async () => {
  try {
    data.value = await api(`/v1/sites/${enc(origin.value)}/info`);
  } catch (e) {
    error.value = String(e);
  }
});

const effectPill = (e: string) => ({ read: 'ok', write: 'warn', payment: 'bad', destructive: 'bad' })[e] ?? 'grey';
const outcomePill = (o: string) =>
  ({ promoted: 'ok', partial: 'ok', confirmation: 'grey', alternative: 'grey', quarantined: 'warn', rejected: 'bad' })[
    o
  ] ?? 'grey';
const guide = computed(() => data.value?.guide ?? null);
const pages = computed<any[]>(() => data.value?.pages ?? []);
const guideDocs = computed(() =>
  ['llms', 'agentCard', 'apiCatalog', 'aiPlugin', 'mcp', 'jsonLd', 'securityTxt', 'robots']
    .filter((k) => guide.value?.[k])
    .map((k) => ({ name: k, doc: guide.value[k] })),
);
const params = (t: any) => Object.keys(t.inputSchema?.properties ?? {}).join(', ') || '—';
</script>

<template>
  <div v-if="error" class="card empty err">{{ error }}</div>
  <template v-else-if="data">
    <section class="hero">
      <h1>{{ host(origin) }}</h1>
      <p class="row">
        <span
          class="pill"
          :class="data.site.state === 'expired' ? 'warn' : data.site.state === 'delisted' ? 'bad' : 'ok'"
          >{{ data.site.state }}</span
        >
        <span v-if="data.site.verdict" class="pill" :class="data.site.verdict === 'good' ? 'ok' : 'bad'">{{
          data.site.verdict === 'good' ? 'verified' : 'blocked'
        }}</span>
        <span
          >version <code>{{ data.site.version }}</code></span
        >
        <span
          >verified {{ ago(data.site.verified_at) }} · expires after {{ data.site.expire_days }} days without
          re-check</span
        >
      </p>
    </section>

    <div class="stats">
      <div class="card stat">
        <div class="muted small">Trust</div>
        <div class="v">{{ data.site.trust }}</div>
      </div>
      <div class="card stat">
        <div class="muted small">Calls</div>
        <div class="v">{{ data.site.calls }}</div>
      </div>
      <div class="card stat">
        <div class="muted small">Success</div>
        <div class="v">{{ data.site.successRate === null ? '—' : Math.round(data.site.successRate * 100) + '%' }}</div>
      </div>
      <div class="card stat">
        <div class="muted small">Independent agreements</div>
        <div class="v">{{ data.site.agreements }}</div>
      </div>
      <div class="card stat">
        <div class="muted small">Stars</div>
        <div class="v">{{ data.site.stars }}</div>
      </div>
    </div>

    <div class="card">
      <div class="head">
        <h2>Tools</h2>
        <span class="muted small"
          >use with <code>mcpit tools {{ host(origin) }}</code></span
        >
      </div>
      <div class="scroll">
        <table class="table">
          <thead>
            <tr>
              <th>Tool</th>
              <th>Effect</th>
              <th>Description</th>
              <th class="hide-sm">Parameters</th>
              <th class="hide-sm">Rev</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in data.tools" :key="t.id">
              <td class="mono">{{ t.id }}</td>
              <td>
                <span class="pill" :class="effectPill(t.effect)">{{ t.effect }}</span>
              </td>
              <td>{{ t.description }}</td>
              <td class="hide-sm mono small">{{ params(t) }}</td>
              <td class="hide-sm">{{ t.rev ?? 1 }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="guide" class="card">
      <div class="head">
        <h2>Site guide</h2>
        <span class="muted small">what the site publishes for agents — read as data</span>
      </div>
      <div class="body" style="display: grid; gap: 10px">
        <p v-if="guide.description" style="margin: 0">{{ guide.description }}</p>
        <div v-if="guide.meta" class="row small">
          <span v-for="(v, k) in guide.meta" :key="k" class="pill grey">{{ k }}: {{ String(v).slice(0, 60) }}</span>
        </div>
        <div v-if="guide.feeds?.length" class="small">
          Feeds:
          <a
            v-for="f in guide.feeds"
            :key="f.url"
            :href="f.url"
            target="_blank"
            rel="noopener"
            style="margin-right: 10px"
            >{{ f.title || f.url }} ({{ f.items }})</a
          >
        </div>
        <details v-for="d in guideDocs" :key="d.name">
          <summary>
            <code>{{ d.name }}</code> <a :href="d.doc.url" target="_blank" rel="noopener">{{ d.doc.url }}</a>
            <span class="muted small">{{ d.doc.text.length }} chars</span>
          </summary>
          <pre class="note">{{ d.doc.text }}</pre>
        </details>
      </div>
    </div>

    <div v-if="pages.length" class="card">
      <div class="head">
        <h2>Site map</h2>
        <span class="muted small">{{ pages.length }} pages</span>
      </div>
      <div class="scroll">
        <table class="table">
          <thead>
            <tr>
              <th>Category</th>
              <th>Path</th>
              <th>Title</th>
              <th class="hide-sm">Source</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="pg in pages" :key="pg.path">
              <td>{{ pg.category }}</td>
              <td class="mono small">
                <span v-if="pg.pattern">{{ pg.path }}</span>
                <a v-else :href="origin + pg.path" target="_blank" rel="noopener">{{ pg.path }}</a>
              </td>
              <td>
                {{ pg.title }}
                <div v-if="pg.pattern && pg.examples?.length" class="small muted">
                  e.g. {{ pg.examples.join(', ') }}
                </div>
              </td>
              <td class="hide-sm muted">{{ pg.source }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="card">
      <div class="head"><h2>Submissions</h2></div>
      <div v-if="!data.submissions.length" class="empty">No submissions.</div>
      <div v-else class="scroll">
        <table class="table">
          <thead>
            <tr>
              <th>When</th>
              <th>Outcome</th>
              <th>Reason</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in data.submissions" :key="s.id">
              <td class="muted">{{ ago(s.created_at) }}</td>
              <td>
                <span class="pill" :class="outcomePill(s.outcome ?? '')">{{ s.outcome ?? s.state }}</span>
              </td>
              <td class="small">{{ s.reason }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="card">
      <div class="head"><h2>Versions</h2></div>
      <div class="scroll">
        <table class="table">
          <thead>
            <tr>
              <th>Version</th>
              <th>State</th>
              <th class="hide-sm">Hash</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="v in data.versions" :key="v.version">
              <td class="mono">{{ v.version }}</td>
              <td>
                <span class="pill" :class="v.state === 'active' ? 'ok' : v.state === 'expired' ? 'warn' : 'grey'">{{
                  v.state
                }}</span>
              </td>
              <td class="hide-sm mono small">{{ v.hash.slice(0, 16) }}…</td>
              <td class="muted">{{ ago(v.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </template>
</template>
