<script setup lang="ts">
import WebhookPanel from '../components/WebhookPanel.vue'
import { useDashboardContext } from '../dashboardContext'

const { webhooks, reload } = useDashboardContext()
</script>

<template>
  <div class="page-stack narrow-page">
    <header class="page-heading"><div><p class="eyebrow">WEBHOOKS</p><h1>Webhook 通知</h1><p>映射变化时向已签名的接收端发送事件。</p></div></header>
    <WebhookPanel :webhooks="webhooks" @changed="reload" />
  </div>
</template>
