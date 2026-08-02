<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status: string }>()
const labels: Record<string, string> = {
  healthy: '已同步',
  mapped: '已映射',
  running: '运行中',
  discovering: '探测中',
  syncing: '同步中',
  sync_error: '同步失败',
  gateway_mapped: '路由已放行',
  gateway_error: '路由放行失败',
  stopped: '已停止',
  error: '运行错误',
}
const tone = computed(() => {
  if (['healthy', 'mapped', 'running', 'gateway_mapped'].includes(props.status)) return 'ok'
  if (['error', 'sync_error', 'gateway_error'].includes(props.status)) return 'error'
  if (['discovering', 'syncing'].includes(props.status)) return 'working'
  return 'idle'
})
const label = computed(() => labels[props.status] ?? props.status)
</script>

<template><span class="status-badge" :data-tone="tone">{{ label }}</span></template>
