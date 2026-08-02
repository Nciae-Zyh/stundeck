<script setup lang="ts">
import CloudflarePanel from '../components/CloudflarePanel.vue'
import { useDashboardContext } from '../dashboardContext'

const { connections, reload } = useDashboardContext()
</script>

<template>
  <div class="page-stack narrow-page">
    <header class="page-heading"><div><p class="eyebrow">CLOUDFLARE</p><h1>Cloudflare 连接</h1><p>管理 Zone 凭据、DNS 与动态重定向所使用的连接。</p></div></header>
    <CloudflarePanel :connections="connections" @changed="reload" />
  </div>
</template>
