<script setup lang="ts">
import { onMounted, ref } from 'vue'
import QRCode from 'qrcode'
import { api } from '../api'
import type { AccessPolicy, SecurityState } from '../types'

const state = ref<SecurityState>()
const mode = ref<AccessPolicy['mode']>('lan')
const hosts = ref('')
const pendingSecret = ref('')
const pendingURI = ref('')
const qrDataURL = ref('')
const confirmCode = ref('')
const disablePassword = ref('')
const disableCode = ref('')
const busy = ref(false)
const error = ref('')
const notice = ref('')

async function load() {
  const [security, access] = await Promise.all([
    api<SecurityState>('/api/v1/security'),
    api<{ policy: AccessPolicy }>('/api/v1/access-policy'),
  ])
  state.value = security
  mode.value = access.policy.mode
  hosts.value = access.policy.allowedHosts.join('\n')
}

async function run(action: () => Promise<void>) {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await action()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '安全设置更新失败'
  } finally {
    busy.value = false
  }
}

function saveAccess() {
  return run(async () => {
    const allowedHosts = hosts.value.split(/[\n,]/).map((value) => value.trim()).filter(Boolean)
    const result = await api<{ policy: AccessPolicy }>('/api/v1/access-policy', {
      method: 'PUT',
      body: JSON.stringify({ mode: mode.value, allowedHosts }),
    })
    mode.value = result.policy.mode
    hosts.value = result.policy.allowedHosts.join('\n')
    notice.value = '访问策略已保存。'
  })
}

function beginTOTP() {
  return run(async () => {
    const result = await api<{ secret: string; uri: string }>('/api/v1/security/totp/begin', { method: 'POST' })
    pendingSecret.value = result.secret
    pendingURI.value = result.uri
    qrDataURL.value = await QRCode.toDataURL(result.uri, {
      width: 200,
      margin: 1,
      color: { dark: '#061018', light: '#e9f2f5' },
    })
  })
}

function confirmTOTP() {
  return run(async () => {
    await api('/api/v1/security/totp/confirm', {
      method: 'POST',
      body: JSON.stringify({ secret: pendingSecret.value, code: confirmCode.value }),
    })
    pendingSecret.value = ''
    pendingURI.value = ''
    qrDataURL.value = ''
    confirmCode.value = ''
    notice.value = '两步验证已开启。'
    await load()
  })
}

function disableTOTP() {
  return run(async () => {
    await api('/api/v1/security/totp', {
      method: 'DELETE',
      body: JSON.stringify({ password: disablePassword.value, code: disableCode.value }),
    })
    disablePassword.value = ''
    disableCode.value = ''
    notice.value = '两步验证已关闭。'
    await load()
  })
}

onMounted(() => void run(load))
</script>

<template>
  <section class="panel security-panel">
    <div class="panel-heading"><div><p class="eyebrow">ACCESS & 2FA</p><h2>控制面安全</h2></div></div>

    <div class="security-section">
      <strong>访问范围</strong>
      <label>
        模式
        <select v-model="mode">
          <option value="local">仅本机</option>
          <option value="lan">局域网</option>
          <option value="public">公网</option>
        </select>
      </label>
      <label>
        允许域名 / IP
        <textarea v-model="hosts" rows="3" placeholder="panel.example.com" />
        <small>一行一个；支持 *.example.com。回环健康检查始终可用。</small>
      </label>
      <p v-if="mode === 'public'" class="warning-text">公网模式必须填写域名/IP 白名单并配合 HTTPS，强烈建议开启 2FA。</p>
      <button class="button secondary wide" type="button" :disabled="busy" @click="saveAccess">保存访问策略</button>
    </div>

    <div class="security-section">
      <div class="security-title"><strong>验证器 2FA</strong><span class="status-badge" :data-tone="state?.totpEnabled ? 'ok' : 'idle'">{{ state?.totpEnabled ? 'ENABLED' : 'OFF' }}</span></div>
      <template v-if="!state?.totpEnabled">
        <button v-if="!pendingSecret" class="button secondary wide" type="button" :disabled="busy" @click="beginTOTP">生成验证器密钥</button>
        <div v-else class="totp-setup">
          <p>在 1Password、Google Authenticator 或其他 TOTP 应用中添加：</p>
          <img v-if="qrDataURL" :src="qrDataURL" alt="StunDeck TOTP 配置二维码" width="200" height="200" />
          <code>{{ pendingSecret }}</code>
          <a :href="pendingURI">在验证器应用中打开</a>
          <label>6 位代码<input v-model.trim="confirmCode" inputmode="numeric" autocomplete="one-time-code" maxlength="6" /></label>
          <button class="button primary wide" type="button" :disabled="busy || confirmCode.length !== 6" @click="confirmTOTP">验证并开启</button>
        </div>
      </template>
      <div v-else class="totp-disable">
        <p class="muted">关闭 2FA 需要再次验证密码和动态代码。</p>
        <label>当前密码<input v-model="disablePassword" type="password" autocomplete="current-password" /></label>
        <label>6 位代码<input v-model.trim="disableCode" inputmode="numeric" autocomplete="one-time-code" maxlength="6" /></label>
        <button class="button secondary wide danger" type="button" :disabled="busy || !disablePassword || disableCode.length !== 6" @click="disableTOTP">关闭 2FA</button>
      </div>
    </div>
    <p v-if="notice" class="success-text">{{ notice }}</p>
    <p v-if="error" class="error-text">{{ error }}</p>
  </section>
</template>
