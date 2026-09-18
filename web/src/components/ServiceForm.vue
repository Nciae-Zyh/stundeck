<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api'
import { createServiceDraft, serviceToDraft } from '../serviceForm'
import type { CloudflareConnection, Service, CloudflareInspection, InspectionResponse, SystemStatus } from '../types'
import { modeLabels, modeBlockReason, capabilityStates, usesConnector, usesCloudflareAccount } from '../cloudflare'

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
const cloudflareMode = computed(() => usesCloudflareAccount(form.value.publishMode))
const tunnelMode = computed(() => usesConnector(form.value.publishMode))
const inspection = ref<CloudflareInspection | null>(null)
const inspecting = ref(false)
const inspectionError = ref('')
const tunnelAvailable = ref(false)
const spectrumAcknowledged = ref(false)
const warpAcknowledged = ref(false)
let inspectionGeneration = 0
const blockedReason = computed(() => modeBlockReason(form.value.publishMode, inspection.value, tunnelAvailable.value, form.value.manageDns))
const selectedConnection = computed(() => props.connections.find(c => c.id === form.value.cloudflareConnectionId))
async function inspectConnection() {
  const generation = ++inspectionGeneration
  inspection.value = null
  inspectionError.value = ''
  if (form.value.publishMode === 'quick') {
    inspecting.value = true
    try {
      const status = await api<SystemStatus>('/api/v1/status')
      if (generation === inspectionGeneration) tunnelAvailable.value = status.tunnelAvailable
    } catch (cause) { if (generation === inspectionGeneration) inspectionError.value = cause instanceof Error ? cause.message : '运行环境检测失败' }
    finally { if (generation === inspectionGeneration) inspecting.value = false }
    return
  }
  if (!cloudflareMode.value || !form.value.cloudflareConnectionId) { inspecting.value = false; return }
  inspecting.value = true
  try {
    const result = await api<InspectionResponse>(`/api/v1/cloudflare/connections/${form.value.cloudflareConnectionId}/inspect`)
    if (generation !== inspectionGeneration) return
    inspection.value = result.inspection
    tunnelAvailable.value = result.tunnelAvailable
  } catch (cause) { if (generation === inspectionGeneration) inspectionError.value = cause instanceof Error ? cause.message : '权限检测失败' }
  finally { if (generation === inspectionGeneration) inspecting.value = false }
}
watch(() => [form.value.cloudflareConnectionId, form.value.publishMode], inspectConnection, { immediate: true })
watch(() => form.value.publishMode, mode => {
  if (['tunnel', 'quick', 'workers', 'proxy', 'redirect'].includes(mode)) form.value.protocol = 'tcp'
  if (mode === 'quick' && !['http', 'https'].includes(form.value.tunnelProtocol)) form.value.tunnelProtocol = 'http'
})
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
      <label>协议<select v-model="form.protocol" :disabled="['tunnel', 'quick', 'workers', 'proxy', 'redirect'].includes(form.publishMode)"><option value="tcp">TCP</option><option value="udp">UDP</option></select></label>
      <label v-if="!tunnelMode">监听端口<input v-model.number="form.bindPort" type="number" min="0" max="65535" /><small>0 表示自动分配</small></label>
      <label v-if="!tunnelMode">路由器端口放行<select v-model="form.gatewayMode"><option value="none">不自动管理</option><option value="upnp">UPnP（推荐）</option><option value="natpmp">NAT-PMP</option></select><small>局域网运行时，用于补齐公网端口到穿透监听端口的映射</small></label>
      <label v-if="!tunnelMode && form.gatewayMode !== 'none'">网关 IP（可选）<input v-model.trim="form.gatewayAddress" inputmode="numeric" placeholder="自动发现，例如 192.168.1.1" /><small>留空自动发现；多路由环境建议明确填写</small></label>
      <label>发布方式<select v-model="form.publishMode"><option v-for="(label, mode) in modeLabels" :key="mode" :value="mode">{{ label }}</option></select></label>
      <template v-if="form.publishMode === 'quick'">
        <label>本地服务类型<select v-model="form.tunnelProtocol"><option value="http">HTTP</option><option value="https">HTTPS</option></select></label>
        <p class="span-2 form-hint">无需 Token 或自有域名。启动后显示临时 trycloudflare.com 网址，重启会变化。仅适合测试：最多 200 个并发请求，不支持 SSE，不能在这里配置 Access。本地 HTTPS 证书需可信。</p>
        <p class="span-2 form-hint">{{ inspecting ? '检查 cloudflared…' : inspectionError || blockedReason || 'cloudflared 已就绪' }} <button type="button" class="text-button" :disabled="inspecting" @click="inspectConnection">重新检测</button></p>
      </template>
      <template v-if="cloudflareMode">
        <div v-if="connections.length === 0" class="span-2 inline-prerequisite"><div><strong>先配置 Cloudflare 连接</strong><p>验证 Token 后可以查看可用功能与域名。</p></div><RouterLink class="button small secondary" to="/cloudflare#token-setup">去配置</RouterLink></div>
        <label>Cloudflare 连接<select v-model="form.cloudflareConnectionId" required :disabled="connections.length === 0"><option value="" disabled>选择连接</option><option v-for="connection in connections" :key="connection.id" :value="connection.id">{{ connection.name }} · {{ connection.zoneName }}</option></select></label>
        <label v-if="form.publishMode !== 'warp'">入口域名<input v-model.trim="form.entryHostname" required :placeholder="`nas.${selectedConnection?.zoneName ?? 'example.com'}`" /><small>必须属于 {{ selectedConnection?.zoneName ?? '所选 Zone' }}；已有非本服务管理的 DNS 不会被接管</small></label>
        <div class="span-2 form-hint"><p v-if="inspecting">正在检查权限与运行环境…</p><p v-else-if="inspectionError" class="error-text">{{ inspectionError }}</p><p v-else-if="blockedReason" class="error-text">{{ blockedReason }}</p><p v-else>已读取连接。{{ Object.values(inspection?.capabilities ?? {}).some(c => c.state === 'unverified') ? capabilityStates.unverified + '的功能会在提交时由 Cloudflare 校验。' : '' }}</p><button class="text-button" type="button" :disabled="inspecting" @click="inspectConnection">重新检测权限</button></div>
        <template v-if="form.publishMode === 'warp'">
          <label>私网 CIDR（可选）<input v-model.trim="form.privateNetwork" placeholder="留空仅路由目标 IP，例如 192.168.1.20/32" /><small>目标需填私网 IP。CIDR 必须包含该 IP；使用默认虚拟网络，已有重叠路由会被拒绝。</small></label>
          <p class="span-2 form-hint">通过 Cloudflare One 客户端访问私网 TCP / UDP。需先注册客户端、将 CIDR 纳入 Split Tunnels，并配置 Gateway 访问策略；本功能不改全局客户端或身份策略。</p>
          <label class="span-2 check"><input v-model="warpAcknowledged" type="checkbox" required />我已配置客户端与访问策略；路由覆盖该 CIDR 内的所有端口，并非只限目标端口</label>
        </template>
        <template v-else-if="form.publishMode === 'tunnel'">
          <label>本地服务类型<select v-model="form.tunnelProtocol"><option value="http">HTTP</option><option value="https">HTTPS</option><option value="tcp">TCP</option><option value="ssh">SSH</option><option value="rdp">RDP</option></select></label>
          <p class="span-2 form-hint">Tunnel 直接连接局域网目标，无需 STUN 或路由器端口映射。HTTP / HTTPS 通过 HTTPS 域名访问；TCP / SSH / RDP 需要访问端运行 cloudflared，不提供裸 TCP / UDP 公网端口。</p>
          <p v-if="form.tunnelProtocol === 'https'" class="span-2 form-hint">本地 HTTPS 证书必须可信并覆盖目标主机名；不会自动关闭证书校验。</p>
        </template>
        <template v-else-if="form.publishMode === 'spectrum'">
          <label>Cloudflare 边缘端口<input v-model.number="form.edgePort" type="number" min="1" max="65535" required /></label>
          <p class="span-2 form-hint">Spectrum 将 TCP / UDP 入口转发到公网映射，仍需要公网能回连源站。权限检测无法确认具体协议的套餐授权和可用配额。</p>
          <label class="span-2 check"><input v-model="spectrumAcknowledged" type="checkbox" required />我已核对 Spectrum 套餐与费用，启用后按账户套餐使用</label>
        </template>
        <label v-else>目标协议<select v-model="form.scheme"><option value="http">HTTP</option><option value="https">HTTPS</option></select></label>
        <p v-if="form.publishMode === 'dns'" class="span-2 form-hint">DNS only 自动同步公网 IP，访问时仍需端口。流量直连公网映射，不经过 Cloudflare 代理或 Access。</p>
        <p v-if="form.publishMode === 'proxy'" class="span-2 form-hint">HTTP 代理仅支持 Cloudflare 公布的端口；公网映射端口不符合时同步会明确报错。若需要稳定的 HTTPS 入口，建议选择 Tunnel。</p>
        <template v-if="form.publishMode === 'workers'">
          <label class="span-2">独立源站域名<input v-model.trim="form.originHostname" required :placeholder="`origin.${selectedConnection?.zoneName ?? 'example.com'}`" /><small>同 Zone 的未占用域名，自动同步为 DNS only；HTTPS 源站证书必须覆盖此域名。</small></label>
          <p class="span-2 form-hint">固定 HTTPS 入口 → Worker → 公网映射端口，支持 HTTP、WebSocket 与流式响应。需要公网可回连，按 Workers 套餐与配额使用。入口可配置 Access；源站仍需防火墙或应用认证，防止绕过入口直连。</p>
        </template>
        <template v-if="redirectMode">
          <label>跳转状态<select v-model.number="form.redirectStatus"><option :value="302">302 Temporary</option><option :value="307">307 Preserve method</option></select></label>
          <label class="span-2">HTTPS 目标域名<input v-model.trim="form.originHostname" :required="form.scheme === 'https'" placeholder="origin.example.com" /><small>HTTPS 必填，目标服务证书必须覆盖此域名；自动 DNS 时也须属于所选 Zone</small></label>
          <label class="check"><input v-model="form.manageDns" type="checkbox" />自动管理 DNS</label><label class="check"><input v-model="form.preservePath" type="checkbox" />保留路径</label><label class="check"><input v-model="form.preserveQuery" type="checkbox" />保留查询参数</label>
        </template>
        <p v-if="['tunnel', 'proxy', 'workers'].includes(form.publishMode)" class="span-2 form-hint">发布后，前往 <RouterLink to="/cloudflare">Cloudflare → 权限与域名</RouterLink> 为符合条件的域名配置 Access。Tunnel 可以先“同步 CF”创建域名，配置 Access 后再启动 connector。</p>
      </template>
      <div class="span-2 form-actions">
        <p v-if="error" class="error-text">{{ error }}</p>
        <span v-else />
        <div class="button-group">
          <button v-if="editing" class="button secondary" type="button" :disabled="busy" @click="emit('cancel')">取消</button>
          <button class="button primary" type="submit" :disabled="busy || inspecting || !!blockedReason || !!inspectionError || (cloudflareMode && connections.length === 0) || (form.publishMode === 'spectrum' && !spectrumAcknowledged) || (form.publishMode === 'warp' && !warpAcknowledged)">{{ submitLabel }}</button>
        </div>
      </div>
    </form>
  </details>
</template>
