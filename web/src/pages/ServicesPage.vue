<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api'
import ServiceForm from '../components/ServiceForm.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { useDashboardContext } from '../dashboardContext'
import type { DiagnosticReport, Service } from '../types'
import { formatEndpoint } from '../utils'
import { modeLabels, serviceURL, usesConnector, usesCloudflareAccount } from '../cloudflare'

const { services, connections, reload } = useDashboardContext()
const busyService = ref('')
const editingServiceId = ref('')
const restartAfterEdit = ref(false)
const error = ref('')
const diagnosingService = ref('')
const diagnostics = ref<Record<string, DiagnosticReport>>({})
const editingService = computed(() => services.value.find((service) => service.id === editingServiceId.value))

async function serviceAction(service: Service, action: 'start' | 'stop' | 'sync' | 'delete' | 'cleanup') {
  if (action === 'cleanup' && !window.confirm(`清理“${service.name}”的云端发布？将移除此服务拥有的 DNS、Tunnel、私网路由、Workers、Redirect 或 Spectrum，Access 策略保留。`)) return
  if (action === 'delete' && !window.confirm(`删除服务“${service.name}”？`)) return
  busyService.value = service.id
  error.value = ''
  try {
    await api(`/api/v1/services/${service.id}${action === 'delete' ? '' : `/${action}`}`, {
      method: action === 'delete' ? 'DELETE' : 'POST',
    })
    if (action === 'delete' && editingServiceId.value === service.id) editingServiceId.value = ''
    await reload()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '操作失败'
  } finally {
    busyService.value = ''
  }
}

async function beginEdit(service: Service) {
  if (service.enabled && !window.confirm(`编辑“${service.name}”前需要停止服务。现在停止并在保存后重新启动吗？`)) return
  busyService.value = service.id
  error.value = ''
  try {
    restartAfterEdit.value = service.enabled
    if (service.enabled) {
      await api(`/api/v1/services/${service.id}/stop`, { method: 'POST' })
      await reload()
    }
    editingServiceId.value = service.id
  } catch (cause) {
    restartAfterEdit.value = false
    error.value = cause instanceof Error ? cause.message : '无法进入编辑状态'
  } finally {
    busyService.value = ''
  }
}

async function finishEdit() {
  editingServiceId.value = ''
  restartAfterEdit.value = false
  await reload()
}

async function cancelEdit() {
  const serviceId = editingServiceId.value
  const shouldRestart = restartAfterEdit.value
  editingServiceId.value = ''
  restartAfterEdit.value = false
  if (!serviceId || !shouldRestart) return
  busyService.value = serviceId
  try {
    await api(`/api/v1/services/${serviceId}/start`, { method: 'POST' })
    await reload()
  } catch (cause) {
    error.value = cause instanceof Error ? `编辑已取消，但服务恢复失败：${cause.message}` : '编辑已取消，但服务恢复失败'
  } finally {
    busyService.value = ''
  }
}

async function diagnose(service: Service) {
  diagnosingService.value = service.id
  error.value = ''
  try {
    const result = await api<{ diagnostic: DiagnosticReport }>(`/api/v1/services/${service.id}/diagnose`, { method: 'POST' })
    diagnostics.value = { ...diagnostics.value, [service.id]: result.diagnostic }
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'STUN 检测失败'
  } finally {
    diagnosingService.value = ''
  }
}

function diagnosticTitle(report: DiagnosticReport) {
  if (report.gatewayReady && report.targetReady) return '路由器端口已放行，等待公网复核'
  if (report.stunFeasible && report.targetReady) return '已取得映射候选，目标服务可用'
  if (report.stunFeasible) return '已取得映射候选，但目标服务配置有阻断项'
  return '本机 STUN 映射条件未通过'
}

function gatewayModeLabel(mode: Service['gatewayMode']) {
  if (mode === 'upnp') return 'UPnP'
  if (mode === 'natpmp') return 'NAT-PMP'
  return '未启用'
}
</script>

<template>
  <div class="page-stack">
    <header class="page-heading"><div><p class="eyebrow">SERVICES</p><h1>映射服务</h1><p>通过公网映射或 Cloudflare Tunnel 发布局域网服务。</p></div><span class="count-chip">{{ services.length }}</span></header>
    <p v-if="error" class="global-error">{{ error }}</p>
    <section v-if="connections.length === 0" class="prerequisite-callout"><div><p class="eyebrow">OPTIONAL PREREQUISITE</p><strong>通过 Cloudflare 发布，请先配置连接</strong><p>公网映射和 Quick Tunnel 可以直接使用；需要自有域名、私网或 Access 时，请先完成最小权限 Token 配置。</p></div><RouterLink class="button secondary" to="/cloudflare#token-setup">先配置 Cloudflare</RouterLink></section>
    <section class="panel services-panel">
      <div v-if="services.length" class="service-list">
        <div v-for="service in services" :key="service.id" class="service-block">
          <article class="service-row" :data-editing="editingServiceId === service.id">
            <div class="service-signal"><i :data-active="['healthy', 'mapped', 'gateway_mapped', 'discovering', 'tunnel_running', 'tunnel_starting'].includes(service.status)" /></div>
            <div class="service-main">
              <div class="service-title"><strong>{{ service.name }}</strong><StatusBadge :status="service.status" /></div>
              <p>{{ service.protocol.toUpperCase() }} · {{ service.targetHost }}:{{ service.targetPort }} → {{ usesConnector(service.publishMode) ? modeLabels[service.publishMode] : formatEndpoint(service.publicIp, service.publicPort) }}</p>
              <small v-if="!usesConnector(service.publishMode)">路由器放行：{{ gatewayModeLabel(service.gatewayMode) }}<template v-if="service.gatewayAddress"> · {{ service.gatewayAddress }}</template></small>
              <small>{{ modeLabels[service.publishMode] }}<template v-if="service.publishMode === 'redirect'"> · {{ service.redirectStatus }}</template></small>
              <small v-if="serviceURL(service)"><a :href="serviceURL(service)" target="_blank" rel="noreferrer">{{ serviceURL(service) }}</a></small>
              <small v-else-if="service.entryHostname">{{ service.entryHostname }}<template v-if="service.publishMode === 'spectrum'">:{{ service.edgePort }}</template></small>
              <small v-if="service.publishMode === 'tunnel'">进程运行不代表已连接云端，请在 Cloudflare Dashboard 核对 Tunnel 状态。</small>
              <small v-if="service.publishMode === 'tunnel' && !['http', 'https'].includes(service.tunnelProtocol || 'http')">客户端：cloudflared access {{ service.tunnelProtocol === 'ssh' ? 'ssh' : 'tcp' }} --hostname {{ service.entryHostname }}<template v-if="service.tunnelProtocol !== 'ssh'"> --url localhost:{{ service.targetPort }}</template></small>
              <small v-if="service.publishMode === 'quick'">临时网址重启会变化；获得网址不代表已完成外网连通性验证。</small>
              <small v-if="service.publishMode === 'warp'">私网路由 {{ service.privateNetwork }} · 使用已注册的 Cloudflare One 客户端访问目标 IP；请核对 Split Tunnels 和 Gateway 策略。</small>
              <small v-if="service.lastError" class="error-text">{{ service.lastError }}</small>
            </div>
            <div class="service-actions">
              <button v-if="!service.enabled" class="button small primary" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'start')">启动</button>
              <button v-else class="button small secondary" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'stop')">停止</button>
              <button class="text-button" type="button" :disabled="busyService === service.id" @click="beginEdit(service)">编辑</button>
              <button v-if="!usesConnector(service.publishMode)" class="text-button" type="button" :disabled="diagnosingService === service.id" @click="diagnose(service)">{{ diagnosingService === service.id ? '检测中…' : 'STUN 检测' }}</button>
              <button v-if="usesCloudflareAccount(service.publishMode) && (usesConnector(service.publishMode) || (service.enabled && service.publicIp))" class="text-button" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'sync')">同步 CF</button>
              <button v-if="usesCloudflareAccount(service.publishMode) && !service.enabled" class="text-button" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'cleanup')">清理云端发布</button>
              <button class="text-button danger" type="button" :disabled="service.enabled || busyService === service.id" @click="serviceAction(service, 'delete')">删除</button>
            </div>
          </article>
          <section v-if="diagnostics[service.id]" class="diagnostic-card" :data-outcome="diagnostics[service.id].outcome">
            <header>
              <div><p class="eyebrow">STUN DIAGNOSTIC</p><h3>{{ diagnosticTitle(diagnostics[service.id]) }}</h3></div>
              <span class="diagnostic-summary">{{ diagnostics[service.id].gatewayReady ? '待外网验证' : '需要处理' }}</span>
            </header>
            <div class="diagnostic-grid">
              <article v-for="check in diagnostics[service.id].checks" :key="check.key" class="diagnostic-check" :data-status="check.status">
                <i /><div><strong>{{ check.label }}</strong><p>{{ check.message }}</p></div><small>{{ check.durationMs }} ms</small>
              </article>
            </div>
            <p v-if="service.entryHostname" class="diagnostic-verify">蜂窝网络验证入口：<a :href="`https://${service.entryHostname}`" target="_blank" rel="noreferrer">https://{{ service.entryHostname }}</a></p>
            <p class="diagnostic-note">Cloudflare 302 是发布入口，不是隧道。局域网运行 StunDeck 时，通常还需要 UPnP/NAT-PMP、DMZ 或手动端口转发把公网端口放行到穿透监听端口；公网回连仍应使用手机蜂窝网络或独立外部探针复核。</p>
          </section>
        </div>
      </div>
      <div v-else class="empty-state"><span>NO SIGNALS YET</span><p>添加第一个局域网服务，StunDeck 会负责后续映射与同步。</p></div>
      <ServiceForm
        :key="editingService?.id ?? 'create'"
        :connections="connections"
        :service="editingService"
        :restart-after-save="restartAfterEdit"
        @saved="finishEdit"
        @cancel="cancelEdit"
      />
    </section>
  </div>
</template>
