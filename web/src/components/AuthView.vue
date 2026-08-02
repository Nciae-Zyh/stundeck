<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '../api'

const props = defineProps<{ mode: 'setup' | 'login' }>()
const emit = defineEmits<{ authenticated: [payload: { csrfToken: string }] }>()

const username = ref('admin')
const password = ref('')
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
    const result = await api<{ csrfToken: string }>(`/api/v1/auth/${props.mode}`, {
      method: 'POST',
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    password.value = ''
    emit('authenticated', result)
  } catch (cause) {
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
      <p v-if="error" class="error-text">{{ error }}</p>
      <button class="button primary wide" type="submit" :disabled="busy">
        {{ busy ? '处理中…' : mode === 'setup' ? '创建并进入' : '登录' }}
      </button>
    </form>
  </main>
</template>
