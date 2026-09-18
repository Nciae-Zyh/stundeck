<script setup lang="ts">
import CloudflarePanel from '../components/CloudflarePanel.vue'
import { useDashboardContext } from '../dashboardContext'

const { connections, reload } = useDashboardContext()
</script>

<template>
  <div class="page-stack cloudflare-page">
    <header class="page-heading"><div><p class="eyebrow">STEP 01 · CLOUDFLARE</p><h1>Cloudflare 发布与访问</h1><p>验证 Token 可用功能，列出域名，按需配置 Tunnel 与 Access。</p></div></header>
    <CloudflarePanel :connections="connections" @changed="reload" />
  </div>
</template>
