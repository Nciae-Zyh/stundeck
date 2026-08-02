<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '../api'
import type { CloudflareConnection } from '../types'

const props = defineProps<{ connections: CloudflareConnection[] }>()
const emit = defineEmits<{ created: [] }>()

const form = ref({
  name: '', targetHost: '', targetPort: 80, protocol: 'tcp', bindPort: 0,
  scheme: 'http', publishMode: 'direct', cloudflareConnectionId: '',
  entryHostname: '', originHostname: '', redirectStatus: 302,
  preservePath: true, preserveQuery: true, manageDns: true,
})
const busy = ref(false)
const error = ref('')
const redirectMode = computed(() => form.value.publishMode === 'redirect')

async function submit() {
  busy.value = true
  error.value = ''
  try {
    await api('/api/v1/services', { method: 'POST', body: JSON.stringify(form.value) })
    form.value = {
      name: '', targetHost: '', targetPort: 80, protocol: 'tcp', bindPort: 0,
      scheme: 'http', publishMode: 'direct', cloudflareConnectionId: props.connections[0]?.id ?? '',
      entryHostname: '', originHostname: '', redirectStatus: 302,
      preservePath: true, preserveQuery: true, manageDns: true,
    }
    emit('created')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '服务创建失败'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <details class="disclosure create-service" open>
    <summary>添加局域网服务</summary>
    <form class="form-grid" @submit.prevent="submit">
      <label>服务名称<input v-model.trim="form.name" required placeholder="家庭 NAS" /></label>
      <label>局域网 IP / 主机名<input v-model.trim="form.targetHost" required placeholder="192.168.1.20" /></label>
      <label>目标端口<input v-model.number="form.targetPort" type="number" min="1" max="65535" required /></label>
      <label>协议<select v-model="form.protocol"><option value="tcp">TCP</option><option value="udp">UDP</option></select></label>
      <label>监听端口<input v-model.number="form.bindPort" type="number" min="0" max="65535" /><small>0 表示自动分配</small></label>
      <label>发布方式<select v-model="form.publishMode"><option value="direct">仅公网映射</option><option value="redirect">Cloudflare Redirect</option></select></label>
      <template v-if="redirectMode">
        <label>Cloudflare 连接<select v-model="form.cloudflareConnectionId" required><option value="" disabled>选择连接</option><option v-for="connection in connections" :key="connection.id" :value="connection.id">{{ connection.name }} · {{ connection.zoneName }}</option></select></label>
        <label>入口域名<input v-model.trim="form.entryHostname" required placeholder="nas.example.com" /></label>
        <label>目标协议<select v-model="form.scheme"><option value="http">HTTP</option><option value="https">HTTPS</option></select></label>
        <label>跳转状态<select v-model.number="form.redirectStatus"><option :value="302">302 Temporary</option><option :value="307">307 Preserve method</option></select></label>
        <label class="span-2">HTTPS 目标域名（可选）<input v-model.trim="form.originHostname" placeholder="origin.example.com" /><small>HTTPS 必填，目标服务证书必须覆盖此域名</small></label>
        <label class="check"><input v-model="form.manageDns" type="checkbox" />自动管理 DNS</label>
        <label class="check"><input v-model="form.preservePath" type="checkbox" />保留路径</label>
        <label class="check"><input v-model="form.preserveQuery" type="checkbox" />保留查询参数</label>
      </template>
      <div class="span-2 form-actions">
        <p v-if="error" class="error-text">{{ error }}</p>
        <button class="button primary" type="submit" :disabled="busy">{{ busy ? '创建中…' : '创建服务' }}</button>
      </div>
    </form>
  </details>
</template>
