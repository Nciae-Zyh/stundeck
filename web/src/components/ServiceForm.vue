<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api'
import { createServiceDraft, serviceToDraft } from '../serviceForm'
import type { CloudflareConnection, Service } from '../types'

const props = withDefaults(defineProps<{
  connections: CloudflareConnection[]
  service?: Service
  restartAfterSave?: boolean
}>(), {
  service: undefined,
  restartAfterSave: false,
})
const emit = defineEmits<{
  saved: [serviceId: string]
  cancel: []
}>()

const form = ref(createServiceDraft(props.connections))
const busy = ref(false)
const error = ref('')
const editing = computed(() => Boolean(props.service))
const redirectMode = computed(() => form.value.publishMode === 'redirect')
const submitLabel = computed(() => {
  if (busy.value) return editing.value ? '保存中…' : '创建中…'
  if (editing.value && props.restartAfterSave) return '保存并重新启动'
  return editing.value ? '保存修改' : '创建服务'
})

watch(
  () => props.service?.id,
  () => {
    form.value = props.service ? serviceToDraft(props.service) : createServiceDraft(props.connections)
    error.value = ''
  },
  { immediate: true },
)

watch(
  () => props.connections[0]?.id,
  (connectionId) => {
    if (!editing.value && !form.value.cloudflareConnectionId && connectionId) {
      form.value.cloudflareConnectionId = connectionId
    }
  },
)

async function submit() {
  busy.value = true
  error.value = ''
  try {
    const path = props.service ? `/api/v1/services/${props.service.id}` : '/api/v1/services'
    const method = props.service ? 'PUT' : 'POST'
    const result = await api<{ service: Service }>(path, { method, body: JSON.stringify(form.value) })
    if (props.service && props.restartAfterSave) {
      await api(`/api/v1/services/${props.service.id}/start`, { method: 'POST' })
    }
    if (!props.service) form.value = createServiceDraft(props.connections)
    emit('saved', result.service.id)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : editing.value ? '服务保存失败' : '服务创建失败'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <details class="disclosure create-service" open>
    <summary>{{ editing ? `编辑服务 · ${service?.name}` : '添加局域网服务' }}</summary>
    <p v-if="editing" class="form-hint">
      修改会更新现有服务，不会创建重复条目。<template v-if="restartAfterSave">该服务已暂时停止，保存或取消后会自动恢复运行。</template>
    </p>
    <form class="form-grid" @submit.prevent="submit">
      <label>服务名称<input v-model.trim="form.name" required placeholder="家庭 NAS" /></label>
      <label>局域网 IP / 主机名<input v-model.trim="form.targetHost" required placeholder="192.168.1.20" /></label>
      <label>目标端口<input v-model.number="form.targetPort" type="number" min="1" max="65535" required /></label>
      <label>协议<select v-model="form.protocol"><option value="tcp">TCP</option><option value="udp">UDP</option></select></label>
      <label>监听端口<input v-model.number="form.bindPort" type="number" min="0" max="65535" /><small>0 表示自动分配</small></label>
      <label>路由器端口放行<select v-model="form.gatewayMode"><option value="none">不自动管理</option><option value="upnp">UPnP（推荐）</option><option value="natpmp">NAT-PMP</option></select><small>局域网运行时，用于补齐公网端口到穿透监听端口的映射</small></label>
      <label v-if="form.gatewayMode !== 'none'">网关 IP（可选）<input v-model.trim="form.gatewayAddress" inputmode="numeric" placeholder="自动发现，例如 192.168.1.1" /><small>留空自动发现；多路由环境建议明确填写</small></label>
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
        <span v-else />
        <div class="button-group">
          <button v-if="editing" class="button secondary" type="button" :disabled="busy" @click="emit('cancel')">取消</button>
          <button class="button primary" type="submit" :disabled="busy">{{ submitLabel }}</button>
        </div>
      </div>
    </form>
  </details>
</template>
