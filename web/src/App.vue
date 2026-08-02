<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, setCSRFToken } from './api'
import AuthView from './components/AuthView.vue'
import DashboardView from './components/DashboardView.vue'
import type { AuthState } from './types'

type Phase = 'loading' | 'setup' | 'login' | 'dashboard'

const phase = ref<Phase>('loading')
const error = ref('')

async function resolveState() {
  error.value = ''
  try {
    const state = await api<AuthState>('/api/v1/auth/state')
    if (state.setupRequired) {
      phase.value = 'setup'
      return
    }
    if (!state.authenticated) {
      phase.value = 'login'
      return
    }
    const me = await api<{ csrfToken: string }>('/api/v1/auth/me')
    setCSRFToken(me.csrfToken)
    phase.value = 'dashboard'
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : 'Unable to connect to StunDeck'
  }
}

function authenticated(payload: { csrfToken: string }) {
  setCSRFToken(payload.csrfToken)
  phase.value = 'dashboard'
}

function loggedOut() {
  setCSRFToken('')
  phase.value = 'login'
}

onMounted(resolveState)
</script>

<template>
  <main v-if="phase === 'loading'" class="center-shell">
    <div class="boot-card">
      <span class="signal-dot" />
      <p>正在连接 StunDeck 控制面…</p>
      <button v-if="error" class="button secondary" type="button" @click="resolveState">重试</button>
      <p v-if="error" class="error-text">{{ error }}</p>
    </div>
  </main>
  <AuthView v-else-if="phase === 'setup' || phase === 'login'" :mode="phase" @authenticated="authenticated" />
  <DashboardView v-else @logged-out="loggedOut" />
</template>
