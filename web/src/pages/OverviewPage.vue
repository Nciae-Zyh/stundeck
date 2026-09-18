<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api'
import { useDashboardContext } from '../dashboardContext'
import type { NetworkDiagnosticReport } from '../types'

const { status, services, connections } = useDashboardContext()
const healthyCount = computed(() => services.value.filter((service) => ['healthy', 'mapped'].includes(service.status)).length)
const mappingCount = computed(() => services.value.filter((service) => Boolean(service.publicIp && service.publicPort)).length)
const network = ref<NetworkDiagnosticReport>()
const networkBusy = ref(false)
const networkError = ref('')

const natType = computed(() => {
  const values: Record<NetworkDiagnosticReport['natType'], { label: string; detail: string }> = {
    open_internet: { label: '公网直连', detail: '未检测到地址转换' },
    nat_detected: { label: '已检测到 NAT', detail: '当前 STUN 服务器未提供更详细的映射类型' },
    endpoint_independent: { label: '端点独立型', detail: '公网映射较稳定，仍需外网回连验证' },
    address_dependent: { label: '地址依赖型', detail: '映射会随目标地址变化' },
    address_port_dependent: { label: '地址/端口依赖型', detail: '映射限制较强，直连成功率较低' },
    unknown: { label: '暂未识别', detail: 'STUN 服务器未提供详细类型，或探测未完成' },
  }
  return values[network.value?.natType ?? 'unknown']
})

async function diagnoseNetwork() {
  networkBusy.value = true
  networkError.value = ''
  try {
    const result = await api<{ diagnostic: NetworkDiagnosticReport }>('/api/v1/diagnostics/network', { method: 'POST' })
    network.value = result.diagnostic
  } catch (cause) {
    networkError.value = cause instanceof Error ? cause.message : '网络检测失败'
  } finally {
    networkBusy.value = false
  }
}

onMounted(diagnoseNetwork)
</script>

<template>
  <div class="page-stack overview-page">
    <section class="hero-grid">
      <div class="hero-copy">
        <p class="eyebrow">LIVE NAT SIGNAL</p>
        <h1>先看网络，<br><span>再发布服务。</span></h1>
        <p class="hero-description">首页直接确认 NAT 映射类型与 TCP / UDP STUN 支持，再继续 Cloudflare 和映射配置。</p>
        <div class="hero-actions">
          <RouterLink class="button primary" to="/cloudflare#token-setup">先配置 Cloudflare</RouterLink>
          <RouterLink class="button secondary" to="/services">查看映射服务</RouterLink>
        </div>
      </div>
      <section class="network-card" :data-outcome="network?.outcome ?? 'loading'">
        <header>
          <div><p class="eyebrow">NETWORK READINESS</p><h2>NAT 与 STUN</h2></div>
          <button class="text-button" type="button" :disabled="networkBusy" @click="diagnoseNetwork">{{ networkBusy ? '检测中…' : '重新检测' }}</button>
        </header>
        <div class="nat-result">
          <small>NAT 类型</small>
          <strong>{{ networkBusy && !network ? '检测中' : natType.label }}</strong>
          <p>{{ networkError || natType.detail }}</p>
        </div>
        <div class="stun-status-grid">
          <article :data-ready="network?.udpStun"><span>UDP STUN</span><strong>{{ network ? (network.udpStun ? '支持' : '不可用') : '待检测' }}</strong></article>
          <article :data-ready="network?.tcpStun"><span>TCP STUN</span><strong>{{ network ? (network.tcpStun ? '支持' : '不可用') : '待检测' }}</strong></article>
        </div>
        <div v-if="network?.checks.length" class="network-checks">
          <p v-for="check in network.checks" :key="check.key" :data-status="check.status"><i />{{ check.label }}：{{ check.message }}</p>
        </div>
        <p class="network-footnote">类型按 RFC 5780 映射行为展示；它不是公网回连成功证明。</p>
      </section>
    </section>

    <section class="metrics-grid">
      <article><p>服务</p><strong>{{ services.length.toString().padStart(2, '0') }}</strong><small>CONFIGURED</small></article>
      <article><p>公网映射</p><strong>{{ mappingCount.toString().padStart(2, '0') }}</strong><small>DISCOVERED</small></article>
      <article><p>映射已同步</p><strong>{{ healthyCount.toString().padStart(2, '0') }}</strong><small>EXTERNAL UNVERIFIED</small></article>
      <article><p>运行版本</p><strong class="version-number">{{ status?.version ?? '—' }}</strong><small>{{ status?.commit ?? '—' }}</small></article>
    </section>

    <section class="panel setup-path">
      <div class="panel-heading"><div><p class="eyebrow">RECOMMENDED ORDER</p><h2>三步完成发布</h2></div></div>
      <div class="setup-steps">
        <RouterLink to="/cloudflare#token-setup" :data-ready="connections.length > 0"><small>01</small><div><strong>配置 Cloudflare</strong><p>{{ connections.length ? `已连接 ${connections.length} 个 Zone` : '创建最小权限 Token 并选择 Zone' }}</p></div><span>{{ connections.length ? '已完成' : '去配置' }}</span></RouterLink>
        <button type="button" :data-ready="network?.udpStun" @click="diagnoseNetwork"><small>02</small><div><strong>检查 NAT / STUN</strong><p>{{ network ? natType.label : '自动检测 TCP 与 UDP 支持' }}</p></div><span>{{ network?.udpStun ? '已检测' : '检测' }}</span></button>
        <RouterLink to="/services" :data-ready="services.length > 0"><small>03</small><div><strong>创建映射服务</strong><p>{{ services.length ? `已配置 ${services.length} 个服务` : '选择公网映射、代理或 Cloudflare Tunnel' }}</p></div><span>{{ services.length ? '管理' : '去创建' }}</span></RouterLink>
      </div>
    </section>

    <section class="panel boundary-card">
      <p class="eyebrow">NETWORK BOUNDARY</p>
      <h2>STUN 不是中继</h2>
      <p>Redirect 的第二跳会直连公网映射，Cloudflare WAF 与 Access 不再保护目标。敏感管理服务建议使用 Tunnel。</p>
    </section>
  </div>
</template>
