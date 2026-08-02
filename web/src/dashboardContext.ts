import { inject, type InjectionKey, type Ref } from 'vue'
import type { CloudflareConnection, EventItem, Service, SystemStatus, Webhook } from './types'

export interface DashboardContext {
  status: Ref<SystemStatus | undefined>
  services: Ref<Service[]>
  connections: Ref<CloudflareConnection[]>
  events: Ref<EventItem[]>
  webhooks: Ref<Webhook[]>
  reload: () => Promise<void>
}

export const dashboardContextKey: InjectionKey<DashboardContext> = Symbol('dashboard-context')

export function useDashboardContext() {
  const context = inject(dashboardContextKey)
  if (!context) throw new Error('Dashboard context is unavailable')
  return context
}
