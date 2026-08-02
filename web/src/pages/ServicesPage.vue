<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '../api'
import ServiceForm from '../components/ServiceForm.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { useDashboardContext } from '../dashboardContext'
import type { Service } from '../types'
import { formatEndpoint } from '../utils'

const { services, connections, reload } = useDashboardContext()
const busyService = ref('')
const editingServiceId = ref('')
const restartAfterEdit = ref(false)
const error = ref('')
const editingService = computed(() => services.value.find((service) => service.id === editingServiceId.value))

async function serviceAction(service: Service, action: 'start' | 'stop' | 'sync' | 'delete') {
  if (action === 'delete' && !window.confirm(`删除服务“${service.name}”？`)) return
  busyService.value = service.id
  error.value = ''
  try {
    await api(`/api/v1/services/${service.id}${action === 'delete' ? '' : `/${action}`}`, {
      method: action === 'delete' ? 'DELETE' : 'POST',
    })
    if (action === 'delete' && editingServiceId.value === service.id) editingServiceId.value = ''
    await reload()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '操作失败'
  } finally {
    busyService.value = ''
  }
}

async function beginEdit(service: Service) {
  if (service.enabled && !window.confirm(`编辑“${service.name}”前需要停止服务。现在停止并在保存后重新启动吗？`)) return
  busyService.value = service.id
  error.value = ''
  try {
    restartAfterEdit.value = service.enabled
    if (service.enabled) {
      await api(`/api/v1/services/${service.id}/stop`, { method: 'POST' })
      await reload()
    }
    editingServiceId.value = service.id
  } catch (cause) {
    restartAfterEdit.value = false
    error.value = cause instanceof Error ? cause.message : '无法进入编辑状态'
  } finally {
    busyService.value = ''
  }
}

async function finishEdit() {
  editingServiceId.value = ''
  restartAfterEdit.value = false
  await reload()
}

async function cancelEdit() {
  const serviceId = editingServiceId.value
  const shouldRestart = restartAfterEdit.value
  editingServiceId.value = ''
  restartAfterEdit.value = false
  if (!serviceId || !shouldRestart) return
  busyService.value = serviceId
  try {
    await api(`/api/v1/services/${serviceId}/start`, { method: 'POST' })
    await reload()
  } catch (cause) {
    error.value = cause instanceof Error ? `编辑已取消，但服务恢复失败：${cause.message}` : '编辑已取消，但服务恢复失败'
  } finally {
    busyService.value = ''
  }
}
</script>

<template>
  <div class="page-stack">
    <header class="page-heading"><div><p class="eyebrow">SERVICES</p><h1>映射服务</h1><p>创建、编辑和运行局域网服务的动态公网映射。</p></div><span class="count-chip">{{ services.length }}</span></header>
    <p v-if="error" class="global-error">{{ error }}</p>
    <section class="panel services-panel">
      <div v-if="services.length" class="service-list">
        <article v-for="service in services" :key="service.id" class="service-row" :data-editing="editingServiceId === service.id">
          <div class="service-signal"><i :data-active="['healthy', 'mapped', 'discovering'].includes(service.status)" /></div>
          <div class="service-main">
            <div class="service-title"><strong>{{ service.name }}</strong><StatusBadge :status="service.status" /></div>
            <p>{{ service.protocol.toUpperCase() }} · {{ service.targetHost }}:{{ service.targetPort }} → {{ formatEndpoint(service.publicIp, service.publicPort) }}</p>
            <small v-if="service.entryHostname">{{ service.redirectStatus }} · https://{{ service.entryHostname }}</small>
            <small v-if="service.lastError" class="error-text">{{ service.lastError }}</small>
          </div>
          <div class="service-actions">
            <button v-if="!service.enabled" class="button small primary" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'start')">启动</button>
            <button v-else class="button small secondary" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'stop')">停止</button>
            <button class="text-button" type="button" :disabled="busyService === service.id" @click="beginEdit(service)">编辑</button>
            <button v-if="service.publishMode === 'redirect' && service.publicIp" class="text-button" type="button" :disabled="busyService === service.id" @click="serviceAction(service, 'sync')">同步 CF</button>
            <button class="text-button danger" type="button" :disabled="service.enabled || busyService === service.id" @click="serviceAction(service, 'delete')">删除</button>
          </div>
        </article>
      </div>
      <div v-else class="empty-state"><span>NO SIGNALS YET</span><p>添加第一个局域网服务，StunDeck 会负责后续映射与同步。</p></div>
      <ServiceForm
        :key="editingService?.id ?? 'create'"
        :connections="connections"
        :service="editingService"
        :restart-after-save="restartAfterEdit"
        @saved="finishEdit"
        @cancel="cancelEdit"
      />
    </section>
  </div>
</template>
