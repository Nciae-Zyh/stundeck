<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api'
import type { Webhook } from '../types'

defineProps<{ webhooks: Webhook[] }>()
const emit = defineEmits<{ changed: [] }>()

const name = ref('Mapping events')
const url = ref('')
const allowPrivate = ref(false)
const secret = ref('')
const error = ref('')
const busy = ref(false)

async function create() {
  busy.value = true
  error.value = ''
  try {
    const result = await api<{ secret: string }>('/api/v1/webhooks', {
      method: 'POST', body: JSON.stringify({ name: name.value, url: url.value, allowPrivate: allowPrivate.value }),
    })
    secret.value = result.secret
    url.value = ''
    emit('changed')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'Webhook 创建失败'
  } finally {
    busy.value = false
  }
}

async function remove(id: string) {
  if (!window.confirm('删除这个 Webhook？')) return
  try {
    await api(`/api/v1/webhooks/${id}`, { method: 'DELETE' })
    emit('changed')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '删除失败'
  }
}
</script>

<template>
  <section class="panel">
    <div class="panel-heading"><div><p class="eyebrow">AUTOMATION</p><h2>Webhook</h2></div><span class="count-chip">{{ webhooks.length }}</span></div>
    <div class="compact-list">
      <div v-for="hook in webhooks" :key="hook.id" class="compact-row">
        <div><strong>{{ hook.name }}</strong><small>{{ hook.url }}</small></div>
        <button class="text-button danger" type="button" @click="remove(hook.id)">删除</button>
      </div>
    </div>
    <div v-if="secret" class="secret-callout"><strong>仅显示一次</strong><code>{{ secret }}</code></div>
    <details class="disclosure" :open="webhooks.length === 0">
      <summary>添加签名 Webhook</summary>
      <form class="form-grid" @submit.prevent="create">
        <label>名称<input v-model.trim="name" required /></label>
        <label class="span-2">URL<input v-model.trim="url" type="url" required placeholder="https://example.com/hooks/stundeck" /></label>
        <label class="check span-2"><input v-model="allowPrivate" type="checkbox" />允许访问局域网地址</label>
        <button class="button secondary" type="submit" :disabled="busy">创建</button>
      </form>
    </details>
    <p v-if="error" class="error-text">{{ error }}</p>
  </section>
</template>
