<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status: string }>()
const tone = computed(() => {
  if (['healthy', 'mapped', 'running'].includes(props.status)) return 'ok'
  if (['error', 'sync_error'].includes(props.status)) return 'error'
  if (['discovering', 'syncing'].includes(props.status)) return 'working'
  return 'idle'
})
</script>

<template><span class="status-badge" :data-tone="tone">{{ status }}</span></template>
