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
