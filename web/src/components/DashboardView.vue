<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import type { CloudflareConnection, EventItem, Service, SystemStatus, Webhook } from '../types'
import { formatEndpoint, formatRelativeTime } from '../utils'
import CloudflarePanel from './CloudflarePanel.vue'
import ServiceForm from './ServiceForm.vue'
import StatusBadge from './StatusBadge.vue'
import WebhookPanel from './WebhookPanel.vue'

const emit = defineEmits<{ loggedOut: [] }>()

const status = ref<SystemStatus>()
const services = ref<Service[]>([])
const connections = ref<CloudflareConnection[]>([])
const events = ref<EventItem[]>([])
const webhooks = ref<Webhook[]>([])
const busyService = ref('')
const error = ref('')
let timer: number | undefined

const healthyCount = computed(() => services.value.filter((service) => ['healthy', 'mapped'].includes(service.status)).length)
const mappingCount = computed(() => services.value.filter((service) => Boolean(service.publicIp && service.publicPort)).length)

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

async function serviceAction(service: Service, action: 'start' | 'stop' | 'sync' | 'delete') {
  if (action === 'delete' && !window.confirm(`删除服务“${service.name}”？`)) return
  busyService.value = service.id
  error.value = ''
  try {
    await api(`/api/v1/services/${service.id}${action === 'delete' ? '' : `/${action}`}`, {
      method: action === 'delete' ? 'DELETE' : 'POST',
    })
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '操作失败'
  } finally {
    busyService.value = ''
  }
}

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
      <div class="brand-lockup"><span class="brand-mark">S</span><span>STUNDECK</span><small>CONTROL PLANE</small></div>
      <div class="topbar-actions">
        <span class="engine-state" :data-online="status?.engineAvailable"><i />{{ status?.engineAvailable ? 'NATMap ready' : 'NATMap missing' }}</span>
        <button class="text-button" type="button" @click="logout">退出</button>
      </div>
    </header>

    <main class="dashboard">
      <section class="hero-grid">
        <div class="hero-copy">
          <p class="eyebrow">LIVE NAT SIGNAL</p>
          <h1>局域网服务，<br><span>持续在线。</span></h1>
          <p class="hero-description">让动态 STUN 映射自动驱动 Cloudflare Redirect 与签名 Webhook。</p>
        </div>
        <div class="radar" aria-hidden="true"><span class="orbit orbit-1" /><span class="orbit orbit-2" /><span class="radar-core">{{ healthyCount.toString().padStart(2, '0') }}<small>LIVE</small></span><i class="satellite one" /><i class="satellite two" /></div>
      </section>

      <section class="metrics-grid">
        <article><p>服务</p><strong>{{ services.length.toString().padStart(2, '0') }}</strong><small>CONFIGURED</small></article>
        <article><p>公网映射</p><strong>{{ mappingCount.toString().padStart(2, '0') }}</strong><small>DISCOVERED</small></article>
        <article><p>正常运行</p><strong>{{ healthyCount.toString().padStart(2, '0') }}</strong><small>HEALTHY</small></article>
        <article><p>运行版本</p><strong class="version-number">{{ status?.version ?? '—' }}</strong><small>{{ status?.commit ?? '—' }}</small></article>
      </section>

      <p v-if="error" class="global-error">{{ error }}</p>

      <section class="content-grid">
        <div class="main-column">
          <section class="panel services-panel">
            <div class="panel-heading"><div><p class="eyebrow">SERVICES</p><h2>映射服务</h2></div><span class="count-chip">{{ services.length }}</span></div>
            <div v-if="services.length" class="service-list">
              <article v-for="service in services" :key="service.id" class="service-row">
                <div class="service-signal"><i :data-active="['healthy', 'mapped', 'discovering'].includes(service.status)" /></div>
                <div class="service-main">
                  <div class="service-title"><strong>{{ service.name }}</strong><StatusBadge :status="service.status" /></div>
                  <p>{{ service.protocol.toUpperCase() }} · {{ service.targetHost }}:{{ service.targetPort }} → {{ formatEndpoint(service.publicIp, service.publicPort) }}</p>
                  <small v-if="service.entryHostname">{{ service.redirectStatus }} · https://{{ service.entryHostname }}</small>
                  <small v-if="service.lastError" class="error-text">{{ service.lastError }}</small>
                </div>
                <div class="service-actions">
                  <button v-if="!service.enabled" class="button small primary" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'start')">启动</button>
                  <button v-else class="button small secondary" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'stop')">停止</button>
                  <button v-if="service.publishMode === 'redirect' && service.publicIp" class="text-button" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'sync')">同步 CF</button>
                  <button class="text-button danger" type="button" :disabled="service.enabled || busyService === service.id" @click="serviceAction(service, 'delete')">删除</button>
                </div>
              </article>
            </div>
            <div v-else class="empty-state"><span>NO SIGNALS YET</span><p>添加第一个局域网服务，StunDeck 会负责后续映射与同步。</p></div>
            <ServiceForm :connections="connections" @created="load" />
          </section>

          <section class="panel event-panel">
            <div class="panel-heading"><div><p class="eyebrow">EVENT STREAM</p><h2>最近事件</h2></div></div>
            <div class="timeline">
              <div v-for="event in events" :key="event.id" class="timeline-item" :data-level="event.level">
                <i /><div><strong>{{ event.type }}</strong><p>{{ event.message }}</p></div><time>{{ formatRelativeTime(event.createdAt) }}</time>
              </div>
              <div v-if="events.length === 0" class="empty-state compact"><p>事件将在服务启动后出现在这里。</p></div>
            </div>
          </section>
        </div>

        <aside class="side-column">
          <CloudflarePanel :connections="connections" @changed="load" />
          <WebhookPanel :webhooks="webhooks" @changed="load" />
          <section class="panel boundary-card">
            <p class="eyebrow">NETWORK BOUNDARY</p>
            <h2>STUN 不是中继</h2>
            <p>Redirect 的第二跳会直连公网映射，Cloudflare WAF 与 Access 不再保护目标。敏感管理服务建议使用 Tunnel。</p>
          </section>
        </aside>
      </section>
    </main>
  </div>
</template>
