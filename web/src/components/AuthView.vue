<script setup lang="ts">
import { computed, ref } from 'vue'
import { ApiError, api } from '../api'

const props = defineProps<{ mode: 'setup' | 'login' }>()
const emit = defineEmits<{ authenticated: [payload: { csrfToken: string }] }>()

const username = ref('')
const password = ref('')
const totpCode = ref('')
const showTOTP = ref(false)
const accessMode = ref<'local' | 'lan' | 'public'>('lan')
const allowedHosts = ref('')
const busy = ref(false)
const error = ref('')
const title = computed(() => (props.mode === 'setup' ? '初始化控制面' : '返回控制面'))
const subtitle = computed(() =>
  props.mode === 'setup'
    ? '创建本地管理员。Cloudflare Token 会在登录后单独配置。'
    : '使用本地管理员账号继续。',
)

async function submit() {
  busy.value = true
  error.value = ''
  try {
    const body: Record<string, unknown> = {
      username: username.value,
      password: password.value,
    }
    if (props.mode === 'login') body.totpCode = totpCode.value
    if (props.mode === 'setup') {
      body.accessMode = accessMode.value
      body.allowedHosts = allowedHosts.value.split(/[\n,]/).map((value) => value.trim()).filter(Boolean)
    }
    const result = await api<{ csrfToken: string }>(`/api/v1/auth/${props.mode}`, {
      method: 'POST',
      body: JSON.stringify(body),
    })
    password.value = ''
    totpCode.value = ''
    emit('authenticated', result)
  } catch (cause) {
    if (cause instanceof ApiError && cause.code === 'totp_required') showTOTP.value = true
    error.value = cause instanceof Error ? cause.message : '认证失败'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="auth-shell">
    <section class="auth-copy">
      <div class="brand-lockup"><span class="brand-mark">S</span><span>STUNDECK</span></div>
      <p class="eyebrow">NAT SIGNAL ORCHESTRATOR</p>
      <h1>把动态公网映射，变成可维护的服务。</h1>
      <p>STUN 探测、局域网转发、Cloudflare 同步和 Webhook，保持在同一个本地控制面内。</p>
      <div class="security-note">
        <span>LOCAL FIRST</span>
        <p>仓库和镜像不包含任何 API Token。凭据仅在本机加密保存。</p>
      </div>
    </section>
    <form class="auth-card" @submit.prevent="submit">
      <p class="eyebrow">{{ mode === 'setup' ? 'FIRST RUN' : 'AUTHENTICATION' }}</p>
      <h2>{{ title }}</h2>
      <p class="muted">{{ subtitle }}</p>
      <label>
        用户名
        <input v-model.trim="username" autocomplete="username" required maxlength="64" />
      </label>
      <label>
        密码
        <input
          v-model="password"
          type="password"
          :autocomplete="mode === 'setup' ? 'new-password' : 'current-password'"
          minlength="12"
          required
        />
        <small v-if="mode === 'setup'">至少 12 个字符</small>
      </label>
      <label v-if="mode === 'login' && showTOTP">
        验证器代码
        <input v-model.trim="totpCode" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required />
        <small>输入验证器应用中的 6 位动态代码</small>
      </label>
      <template v-if="mode === 'setup'">
        <label>
          访问模式
          <select v-model="accessMode">
            <option value="local">仅本机</option>
            <option value="lan">局域网（推荐）</option>
            <option value="public">公网</option>
          </select>
          <small>监听地址仍由部署配置控制；这里限制允许访问控制面的客户端网络。</small>
        </label>
        <label>
          允许访问的域名 / IP（可选）
          <textarea v-model="allowedHosts" rows="3" placeholder="panel.example.com, 192.168.1.10" />
          <small>逗号或换行分隔，支持 *.example.com。留空则不限制 Host。</small>
        </label>
        <p v-if="accessMode === 'public'" class="warning-text">公网模式必须填写域名/IP 白名单，并配合 HTTPS、强密码和 2FA。</p>
      </template>
      <p v-if="error" class="error-text">{{ error }}</p>
      <button class="button primary wide" type="submit" :disabled="busy">
        {{ busy ? '处理中…' : mode === 'setup' ? '创建并进入' : '登录' }}
      </button>
    </form>
  </main>
</template>
