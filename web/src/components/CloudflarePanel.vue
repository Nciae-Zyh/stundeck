<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '../api'
import type { CloudflareConnection, Zone } from '../types'

const props = defineProps<{ connections: CloudflareConnection[] }>()
const emit = defineEmits<{ changed: [] }>()

const name = ref('Cloudflare')
const token = ref('')
const zones = ref<Zone[]>([])
const zoneId = ref('')
const busy = ref(false)
const message = ref('')
const error = ref('')
const selectedZone = computed(() => zones.value.find((zone) => zone.id === zoneId.value))

async function validate() {
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    const result = await api<{ zones: Zone[] }>('/api/v1/cloudflare/validate', {
      method: 'POST', body: JSON.stringify({ token: token.value }),
    })
    zones.value = result.zones
    zoneId.value = result.zones[0]?.id ?? ''
    message.value = `Token 有效，可访问 ${result.zones.length} 个 Zone。`
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'Token 检测失败'
  } finally {
    busy.value = false
  }
}

async function save() {
  if (!selectedZone.value) return
  busy.value = true
  error.value = ''
  try {
    await api('/api/v1/cloudflare/connections', {
      method: 'POST',
      body: JSON.stringify({
        name: name.value, token: token.value, zoneId: selectedZone.value.id, zoneName: selectedZone.value.name,
      }),
    })
    token.value = ''
    zones.value = []
    zoneId.value = ''
    message.value = '连接已加密保存。'
    emit('changed')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '保存失败'
  } finally {
    busy.value = false
  }
}

async function remove(id: string) {
  if (!window.confirm('删除这个 Cloudflare 连接？正在使用它的服务会阻止删除。')) return
  try {
    await api(`/api/v1/cloudflare/connections/${id}`, { method: 'DELETE' })
    emit('changed')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '删除失败'
  }
}
</script>

<template>
  <section class="panel">
    <div class="panel-heading">
      <div><p class="eyebrow">PROVIDER</p><h2>Cloudflare 连接</h2></div>
      <span class="count-chip">{{ connections.length }}</span>
    </div>
    <div v-if="connections.length" class="compact-list">
      <div v-for="connection in connections" :key="connection.id" class="compact-row">
        <div><strong>{{ connection.name }}</strong><small>{{ connection.zoneName }}</small></div>
        <button class="text-button danger" type="button" @click="remove(connection.id)">删除</button>
      </div>
    </div>
    <details class="disclosure" :open="connections.length === 0">
      <summary>添加 API Token</summary>
      <div class="form-grid">
        <label>连接名称<input v-model.trim="name" maxlength="100" /></label>
        <label class="span-2">Cloudflare API Token<input v-model.trim="token" type="password" autocomplete="off" placeholder="不会写入日志" /></label>
        <button class="button secondary" type="button" :disabled="busy || !token" @click="validate">检测权限</button>
        <label v-if="zones.length" class="span-2">Zone
          <select v-model="zoneId"><option v-for="zone in zones" :key="zone.id" :value="zone.id">{{ zone.name }} · {{ zone.status }}</option></select>
        </label>
        <button v-if="zones.length" class="button primary" type="button" :disabled="busy || !zoneId" @click="save">加密保存</button>
      </div>
    </details>
    <p v-if="message" class="success-text">{{ message }}</p>
    <p v-if="error" class="error-text">{{ error }}</p>
  </section>
</template>
