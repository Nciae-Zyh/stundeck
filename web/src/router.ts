import { createRouter, createWebHistory } from 'vue-router'
import CloudflarePage from './pages/CloudflarePage.vue'
import EventsPage from './pages/EventsPage.vue'
import OverviewPage from './pages/OverviewPage.vue'
import SecurityPage from './pages/SecurityPage.vue'
import ServicesPage from './pages/ServicesPage.vue'
import WebhooksPage from './pages/WebhooksPage.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'overview', component: OverviewPage },
    { path: '/services', name: 'services', component: ServicesPage },
    { path: '/cloudflare', name: 'cloudflare', component: CloudflarePage },
    { path: '/webhooks', name: 'webhooks', component: WebhooksPage },
    { path: '/security', name: 'security', component: SecurityPage },
    { path: '/events', name: 'events', component: EventsPage },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
})
