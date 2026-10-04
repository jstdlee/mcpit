import { createApp } from 'vue';
import { createRouter, createWebHistory } from 'vue-router';
import App from './App.vue';
import Dashboard from './pages/Dashboard.vue';
import Site from './pages/Site.vue';
import Moderate from './pages/Moderate.vue';
import Docs from './pages/Docs.vue';
import './style.css';

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: Dashboard },
    { path: '/site/:origin(.*)', component: Site, props: true },
    { path: '/moderate/:tab?', component: Moderate, props: true },
    { path: '/docs', component: Docs },
  ],
});

createApp(App).use(router).mount('#app');
