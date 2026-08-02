<script setup lang="ts">
import { onBeforeUnmount, onMounted, provide, ref } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import { api } from '../api'
import { dashboardContextKey } from '../dashboardContext'
import type { CloudflareConnection, EventItem, Service, SystemStatus, Webhook } from '../types'

const emit = defineEmits<{ loggedOut: [] }>()

const status = ref<SystemStatus>()
const services = ref<Service[]>([])
const connections = ref<CloudflareConnection[]>([])
const events = ref<EventItem[]>([])
const webhooks = ref<Webhook[]>([])
const error = ref('')
let timer: number | undefined

const navigation = [
  { to: '/', label: '运行概览', code: '01' },
  { to: '/services', label: '映射服务', code: '02' },
  { to: '/cloudflare', label: 'Cloudflare', code: '03' },
  { to: '/webhooks', label: 'Webhook', code: '04' },
  { to: '/security', label: '安全设置', code: '05' },
  { to: '/events', label: '事件记录', code: '06' },
]

async function load() {
  try {
    const [statusResult, serviceResult, connectionResult, eventResult, webhookResult] = await Promise.all([
      api<SystemStatus>('/api/v1/status'),
      api<{ services: Service[] }>('/api/v1/services'),
      api<{ connections: CloudflareConnection[] }>('/api/v1/cloudflare/connections'),
      api<{ events: EventItem[] }>('/api/v1/events'),
      api<{ webhooks: Webhook[] }>('/api/v1/webhooks'),
    ])
    status.value = statusResult
    services.value = serviceResult.services
    connections.value = connectionResult.connections
    events.value = eventResult.events
    webhooks.value = webhookResult.webhooks
    error.value = ''
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '控制面数据加载失败'
  }
}

provide(dashboardContextKey, { status, services, connections, events, webhooks, reload: load })

async function logout() {
  await api('/api/v1/auth/logout', { method: 'POST' })
  emit('loggedOut')
}

onMounted(() => {
  void load()
  timer = window.setInterval(load, 5000)
})
onBeforeUnmount(() => window.clearInterval(timer))
</script>

<template>
  <div class="app-shell">
    <header class="topbar">
      <RouterLink class="brand-lockup" to="/"><span class="brand-mark">S</span><span>STUNDECK</span><small>CONTROL PLANE</small></RouterLink>
      <div class="topbar-actions">
        <span class="engine-state" :data-online="status?.engineAvailable"><i />{{ status?.engineAvailable ? 'NATMap ready' : 'NATMap missing' }}</span>
        <button class="text-button" type="button" @click="logout">退出</button>
      </div>
    </header>

    <main class="control-layout">
      <aside class="control-sidebar">
        <div class="sidebar-heading"><p class="eyebrow">CONTROL INDEX</p><strong>控制台</strong></div>
        <nav class="control-nav" aria-label="控制台导航">
          <RouterLink
            v-for="item in navigation"
            :key="item.to"
            :to="item.to"
            :active-class="item.to === '/' ? '' : 'router-link-active'"
            exact-active-class="router-link-active"
          >
            <small>{{ item.code }}</small><span>{{ item.label }}</span><i>↗</i>
          </RouterLink>
        </nav>
        <div class="sidebar-status">
          <span :data-online="status?.engineAvailable" />
          <div><small>ENGINE</small><strong>{{ status?.engineAvailable ? 'ONLINE' : 'OFFLINE' }}</strong></div>
        </div>
      </aside>

      <section class="control-workspace">
        <p v-if="error" class="global-error">{{ error }}</p>
        <RouterView />
      </section>
    </main>
  </div>
</template>
