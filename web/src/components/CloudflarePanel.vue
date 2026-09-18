<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api'
import { canConfigure, capabilityLabels, capabilityStates } from '../cloudflare'
import type { CloudflareConnection, CloudflareInspection, InspectionResponse, Zone } from '../types'

const props = defineProps<{ connections: CloudflareConnection[] }>()
const emit = defineEmits<{ changed: [] }>()
const name = ref('Cloudflare')
const token = ref('')
const editingId = ref('')
const zones = ref<Zone[]>([])
const zoneId = ref('')
const busy = ref(false)
const inspecting = ref(false)
const message = ref('')
const error = ref('')
const report = ref<CloudflareInspection | null>(null)
const activeConnection = ref('')
const tunnelAvailable = ref(false)
const hostname = ref('')
const emails = ref('')
const duration = ref('24h')
const filter = ref('')
let inspectionGeneration = 0
const selectedZone = computed(() => zones.value.find(zone => zone.id === zoneId.value))
const visibleDomains = computed(() => report.value?.domains.filter(domain => domain.hostname.includes(filter.value.toLowerCase())) ?? [])
const accessChoices = computed(() => report.value?.domains.filter(domain => domain.accessEligible) ?? [])
const accessReady = computed(() => canConfigure(report.value?.capabilities.access))
const permissionText = `基础：Zone > Zone > Read\nDNS / HTTP 代理：Zone > DNS > Edit\nRedirect：Zone > Single Redirect > Edit\nTunnel：Account > Cloudflare Tunnel > Edit，加 Zone > DNS > Edit\nWARP：Account > Cloudflare Tunnel > Edit；私网路由可用 Cloudflare One Networks > Edit\nWorkers：Account > Workers Scripts > Edit，加 Zone > Workers Routes > Edit 和 DNS Edit\nQuick Tunnel：不需要 Token\nAccess：Account 或 Zone > Access: Apps and Policies > Edit\nSpectrum：Zone > Zone Settings > Edit，加 DNS Read；需要对应套餐\n资源范围：指定 Zone；Tunnel / WARP / Workers / 账户级 Access 仅指定所属 Account\n无需 API Tokens Edit；检测只执行读取操作，不会为检测而创建资源。`
watch(token, () => { zones.value = []; zoneId.value = ''; if (!activeConnection.value) { report.value = null; inspectionGeneration++; inspecting.value = false } })
watch(zoneId, () => { if (!activeConnection.value) { report.value = null; inspectionGeneration++; inspecting.value = false } })
watch(hostname, value => {
  const app = report.value?.applications.find(app => app.domain === value && app.managed)
  emails.value = app?.policies.flatMap(policy => (policy.include ?? []).flatMap(rule => rule.email ? [rule.email.email] : [])).join('\n') ?? ''
  duration.value = app?.session_duration ?? '24h'
})

async function copyPermissions() {
  try { await navigator.clipboard.writeText(permissionText); message.value = '按功能授权清单已复制。' }
  catch { error.value = '请按页面清单手动配置权限。' }
}
async function inspect(connectionId = '') {
  const generation = ++inspectionGeneration
  inspecting.value = true
  activeConnection.value = connectionId
  report.value = null
  hostname.value = ''
  error.value = ''
  try {
    const result = connectionId
      ? await api<InspectionResponse>(`/api/v1/cloudflare/connections/${connectionId}/inspect`)
      : await api<InspectionResponse>('/api/v1/cloudflare/inspect', { method: 'POST', body: JSON.stringify({ token: token.value, zoneId: zoneId.value }) })
    if (generation !== inspectionGeneration) return
    report.value = result.inspection
    tunnelAvailable.value = result.tunnelAvailable
  } catch (cause) {
    if (generation === inspectionGeneration) error.value = cause instanceof Error ? cause.message : '权限检测失败'
  } finally { if (generation === inspectionGeneration) inspecting.value = false }
}
async function validate() {
  busy.value = true; error.value = ''; message.value = ''; activeConnection.value = ''; report.value = null
  const submittedToken = token.value
  try {
    const result = await api<{ zones: Zone[] }>('/api/v1/cloudflare/validate', { method: 'POST', body: JSON.stringify({ token: submittedToken }) })
    if (submittedToken !== token.value) return
    zones.value = result.zones
    const existing = props.connections.find(c => c.id === editingId.value)
    zoneId.value = existing?.zoneId ?? result.zones[0]?.id ?? ''
    if (!selectedZone.value) { error.value = 'Token 有效，但无法读取所选 Zone。请检查 Zone Read 和资源范围。'; return }
    message.value = `Token 有效，可见 ${result.zones.length} 个 Zone。选择 Zone 后可检测详细权限与域名。`
  } catch (cause) { error.value = cause instanceof Error ? cause.message : 'Token 检测失败' }
  finally { busy.value = false }
}
async function save() {
  if (!selectedZone.value) return
  busy.value = true; error.value = ''
  try {
    await api('/api/v1/cloudflare/connections', { method: 'POST', body: JSON.stringify({ id: editingId.value, name: name.value, token: token.value, zoneId: selectedZone.value.id, zoneName: selectedZone.value.name }) })
    token.value = ''; editingId.value = ''; report.value = null; activeConnection.value = ''
    message.value = '连接已加密保存。点击“权限与域名”查看可用功能。'; emit('changed')
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '保存失败' }
  finally { busy.value = false }
}
function edit(connection: CloudflareConnection) {
  editingId.value = connection.id; name.value = connection.name; token.value = ''; zones.value = []; zoneId.value = ''
  message.value = `正在更新 ${connection.zoneName} 的 Token；保留现有服务和资源绑定。`
  document.getElementById('token-setup')?.scrollIntoView({ behavior: 'smooth' })
}
async function remove(id: string) {
  if (!window.confirm('删除这个连接？已有服务或云端托管资源会阻止删除。')) return
  try {
    await api(`/api/v1/cloudflare/connections/${id}`, { method: 'DELETE' })
    if (activeConnection.value === id) { report.value = null; activeConnection.value = ''; inspectionGeneration++ }
    emit('changed')
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '删除失败' }
}
async function saveAccess() {
  busy.value = true; error.value = ''; message.value = ''
  const connectionId = activeConnection.value
  try {
    await api(`/api/v1/cloudflare/connections/${connectionId}/access`, { method: 'PUT', body: JSON.stringify({ hostname: hostname.value, emails: emails.value.split(/[\s,;，；]+/).filter(Boolean), sessionDuration: duration.value }) })
    message.value = 'Access 应用与邮箱允许策略已保存，并已从 Cloudflare 回读确认。'
    await inspect(connectionId)
  } catch (cause) { error.value = cause instanceof Error ? cause.message : 'Access 配置失败' }
  finally { busy.value = false }
}
async function removeAccess(domain: string) {
  if (!window.confirm(`移除 ${domain} 的 Access 登录保护？该域名将不再受这个应用的登录策略保护。`)) return
  busy.value = true; error.value = ''
  const connectionId = activeConnection.value
  try {
    await api(`/api/v1/cloudflare/connections/${connectionId}/access`, { method: 'DELETE', body: JSON.stringify({ hostname: domain }) })
    message.value = 'Access 应用已移除。'; await inspect(connectionId)
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '移除失败' }
  finally { busy.value = false }
}
</script>

<template>
  <section class="panel">
    <div class="panel-heading"><div><p class="eyebrow">CONNECTIONS</p><h2>Cloudflare 连接</h2></div><span class="count-chip">{{ connections.length }}</span></div>
    <div v-if="connections.length" class="compact-list">
      <div v-for="connection in connections" :key="connection.id" class="compact-row cf-connection">
        <div><strong>{{ connection.name }}</strong><small>{{ connection.zoneName }}</small></div>
        <div class="button-group"><button class="button small secondary" :disabled="busy || inspecting" @click="inspect(connection.id)">权限与域名</button><button class="text-button" :disabled="busy" @click="edit(connection)">更新 Token</button><button class="text-button danger" :disabled="busy" @click="remove(connection.id)">删除</button></div>
      </div>
    </div>
    <p v-if="inspecting" role="status">正在读取权限、DNS 和 Access 应用…</p>
    <section v-if="report" class="cf-inspection">
      <div class="panel-heading"><div><p class="eyebrow">CAPABILITIES</p><h3>{{ report.zone.name }} · {{ report.zone.status }}</h3><small>{{ report.zone.account?.name }} · 检测于 {{ new Date(report.checkedAt).toLocaleTimeString('zh-CN') }}</small></div><button class="text-button" :disabled="busy || inspecting" @click="inspect(activeConnection)">重新检测</button></div>
      <p class="form-hint">这里只执行读取操作。写权限待确认时可以提交配置，最终由 Cloudflare 校验；检测失败不代表缺少权限。</p>
      <div class="cf-capabilities"><article v-for="(cap, key) in report.capabilities" :key="key" :data-state="cap.state"><div><strong>{{ capabilityLabels[key] }}</strong><span>{{ capabilityStates[cap.state] }}</span></div><p>{{ cap.message }}</p><small>{{ cap.permission }}</small></article></div>
      <p class="form-hint">Tunnel 运行环境：{{ tunnelAvailable ? '已找到 cloudflared' : '尚未找到 cloudflared；请安装或使用内置 connector 的 Docker / fnOS 镜像' }}。Spectrum 还需套餐授权；列举成功不代表所选协议已获授权。</p>
      <div class="panel-heading"><div><h3>可见域名与 Access 条件</h3><small>仅列出所选 Zone 中的 A、AAAA、CNAME。新域名可在服务发布时创建。</small></div><input v-model="filter" aria-label="筛选域名" placeholder="筛选域名" /></div>
      <div class="cf-domain-list"><article v-for="domain in visibleDomains" :key="domain.hostname"><div><strong>{{ domain.hostname }}</strong><span>{{ domain.proxied ? 'Proxied' : 'DNS only' }}</span></div><p>{{ domain.reason }}</p><button v-if="activeConnection && domain.accessEligible && accessReady" class="text-button" :disabled="busy" @click="hostname = domain.hostname">{{ domain.managed ? '修改允许邮箱' : '配置 Access' }}</button></article></div>
      <p v-if="!visibleDomains.length" class="form-hint">{{ report.capabilities.dns?.state === 'unavailable' || report.capabilities.dns?.state === 'error' ? '无法读取域名清单，请检查 DNS Read / Edit 权限后重试。' : '没有匹配的域名。可先在服务中创建入口域名，再刷新。' }}</p>
      <template v-if="activeConnection">
        <div v-for="app in report.applications.filter(app => app.managed)" :key="app.id" class="compact-row"><div><strong>{{ app.domain }}</strong><small>StunDeck 管理的 Access 应用</small></div><button class="text-button danger" :disabled="busy || !accessReady" @click="removeAccess(app.domain)">移除保护</button></div>
        <form v-if="accessChoices.length && accessReady" class="form-grid cf-access-form" @submit.prevent="saveAccess">
          <div class="span-2"><h3>按域名启用 Cloudflare Access</h3><p class="form-hint">仅允许填写的邮箱登录。使用账户已有的身份提供商；请先在 Zero Trust 配置邮箱验证码或其他登录方式。</p></div>
          <label>保护域名<select v-model="hostname" required><option value="" disabled>选择域名</option><option v-for="domain in accessChoices" :key="domain.hostname" :value="domain.hostname">{{ domain.hostname }}</option></select></label>
          <label>登录有效期<select v-model="duration"><option value="1h">1 小时</option><option value="8h">8 小时</option><option value="24h">24 小时</option><option value="168h">7 天</option></select></label>
          <label class="span-2">允许登录的邮箱<textarea v-model="emails" required rows="3" placeholder="you@example.com&#10;teammate@example.com" /><small>每行一个，最多 50 个。未匹配用户默认拒绝。</small></label>
          <p class="form-hint span-2">Access 保护经过 Cloudflare 的请求。普通代理与 Workers 的源站仍需限制直连；Tunnel 的 TCP / SSH / RDP 访问者需要 cloudflared 客户端。Redirect 的第二跳和 DNS only 不受保护。</p>
          <button class="button primary" :disabled="busy || !hostname">{{ busy ? '保存中…' : '保存 Access 与允许策略' }}</button>
        </form>
      </template>
      <p v-else class="form-hint">保存连接后，即可在这里按域名配置 Access。</p>
    </section>
    <section id="token-setup" class="token-setup">
      <div class="token-intro"><div><span class="step-number">01</span><div><strong>{{ editingId ? '更新连接 Token' : '添加 Cloudflare 连接' }}</strong><p>按实际功能增加权限，Zone 和 Account 都限制到所需资源。</p></div></div><a class="button primary" href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noreferrer">打开 Token 配置</a></div>
      <div class="permission-card"><header><div><p class="eyebrow">PERMISSIONS</p><h3>按功能授权</h3></div><button class="text-button" @click="copyPermissions">复制清单</button></header><div class="permission-list"><p><strong>Zone · Zone · Read</strong><span>列出可见 Zone（基础）</span></p><p><strong>Zone · DNS · Edit</strong><span>DNS、HTTP 代理、Workers 与 Tunnel 域名绑定</span></p><p><strong>Zone · Single Redirect · Edit</strong><span>Redirect</span></p><p><strong>Account · Cloudflare Tunnel · Edit</strong><span>Tunnel 的创建、配置和 connector token</span></p><p><strong>Account · Cloudflare One Networks · Edit</strong><span>WARP 私网路由；Cloudflare Tunnel Edit 也可授权</span></p><p><strong>Workers Scripts / Workers Routes · Edit</strong><span>账户级脚本 + Zone 级路由；另需 DNS Edit</span></p><p><strong>Access: Apps and Policies · Edit</strong><span>按所选账户或 Zone 配置 Access</span></p><p><strong>Zone · Zone Settings · Edit</strong><span>Spectrum；另需 DNS Read 和套餐授权</span></p></div><p class="permission-note">无需 API Tokens Edit。读取成功不等于具备写权限；无需为了检测而增加 Token 管理权限。</p></div>
      <div class="form-grid"><label>连接名称<input v-model.trim="name" maxlength="100" :disabled="busy" /></label><label class="span-2">Cloudflare API Token<input v-model.trim="token" type="password" autocomplete="off" spellcheck="false" :disabled="busy" placeholder="加密保存，不回显完整 Token" /></label><button class="button secondary" :disabled="busy || !token" @click="validate">验证 Token 并列出 Zone</button><label v-if="zones.length" class="span-2">可见 Zone<select v-model="zoneId" :disabled="!!editingId || busy"><option v-for="zone in zones" :key="zone.id" :value="zone.id">{{ zone.name }} · {{ zone.status }}</option></select></label><div v-if="zones.length" class="button-group span-2"><button class="button secondary" :disabled="busy || inspecting || !zoneId" @click="inspect()">检测所选 Zone 权限与域名</button><button class="button primary" :disabled="busy || !selectedZone || !name" @click="save">{{ editingId ? '更新 Token' : '加密保存' }}</button></div><button v-if="editingId" class="text-button" :disabled="busy" @click="editingId = ''; token = ''; name = 'Cloudflare'">取消更新</button></div>
      <div v-if="connections.length" class="next-step"><span class="step-number">02</span><div><strong>发布局域网服务</strong><p>选择 DNS、Redirect、HTTP 代理、Tunnel、Quick Tunnel、WARP、Workers 或 Spectrum。</p></div><RouterLink class="button secondary" to="/services">进入服务</RouterLink></div>
    </section>
    <p v-if="message" class="success-text" role="status">{{ message }}</p><p v-if="error" class="error-text" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.cf-inspection { margin: 24px 0; padding: 24px; border: 1px solid var(--line); border-radius: 16px; }
.cf-capabilities { display: grid; grid-template-columns: repeat(auto-fit,minmax(220px,1fr)); gap: 12px; margin: 18px 0; }
.cf-capabilities article, .cf-domain-list article { padding: 16px; border: 1px solid var(--line); border-radius: 10px; }
.cf-capabilities article > div, .cf-domain-list article > div { display: flex; gap: 12px; align-items: center; justify-content: space-between; }
.cf-capabilities span, .cf-domain-list span { font-size: 11px; white-space: nowrap; color: var(--amber); }
.cf-capabilities article[data-state=available] span { color: var(--green); }
.cf-capabilities article[data-state=unavailable] span, .cf-capabilities article[data-state=error] span { color: var(--red); }
.cf-capabilities p, .cf-domain-list p { font-size: 12px; line-height: 1.7; margin: 10px 0; }
.cf-capabilities small { font-size: 11px; color: var(--muted); }
.cf-domain-list { display: grid; gap: 8px; max-height: 420px; overflow-y: auto; margin: 16px 0; }
.cf-domain-list strong { overflow-wrap: anywhere; }
.cf-inspection .panel-heading input { max-width: 240px; }
.cf-access-form { margin-top: 28px; }
textarea { width: 100%; font: inherit; border: 1px solid var(--line); border-radius: 8px; padding: 10px; resize: vertical; }
@media(max-width:700px) { .cf-inspection { padding: 14px; } .cf-connection, .panel-heading { flex-wrap: wrap; gap: 12px; } }
</style>
