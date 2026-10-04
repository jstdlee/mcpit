<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router';
</script>

<template>
  <header class="top">
    <div class="wrap">
      <RouterLink to="/" class="brand"><img src="/favicon.svg" alt="" />mcpit registry</RouterLink>
      <nav class="nav">
        <RouterLink to="/" exact-active-class="router-link-active">Sites</RouterLink>
        <RouterLink to="/docs">Use it</RouterLink>
        <RouterLink to="/moderate">Moderate</RouterLink>
      </nav>
    </div>
  </header>
  <main class="wrap">
    <RouterView />
  </main>
</template>
