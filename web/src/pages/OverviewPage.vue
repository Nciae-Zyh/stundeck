<script setup lang="ts">
import { computed } from 'vue'
import { useDashboardContext } from '../dashboardContext'

const { status, services } = useDashboardContext()
const healthyCount = computed(() => services.value.filter((service) => ['healthy', 'mapped'].includes(service.status)).length)
const mappingCount = computed(() => services.value.filter((service) => Boolean(service.publicIp && service.publicPort)).length)
</script>

<template>
  <div class="page-stack overview-page">
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
      <article><p>映射已同步</p><strong>{{ healthyCount.toString().padStart(2, '0') }}</strong><small>EXTERNAL UNVERIFIED</small></article>
      <article><p>运行版本</p><strong class="version-number">{{ status?.version ?? '—' }}</strong><small>{{ status?.commit ?? '—' }}</small></article>
    </section>

    <section class="panel boundary-card">
      <p class="eyebrow">NETWORK BOUNDARY</p>
      <h2>STUN 不是中继</h2>
      <p>Redirect 的第二跳会直连公网映射，Cloudflare WAF 与 Access 不再保护目标。敏感管理服务建议使用 Tunnel。</p>
    </section>
  </div>
</template>
