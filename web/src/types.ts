export interface AuthState {
  setupRequired: boolean
  authenticated: boolean
}

export interface SystemStatus {
  version: string
  commit: string
  uptimeSeconds: number
  initialized: boolean
  engineAvailable: boolean
}

export interface AccessPolicy {
  mode: 'local' | 'lan' | 'public'
  allowedHosts: string[]
}

export interface SecurityState {
  username: string
  totpEnabled: boolean
}

export interface CloudflareConnection {
  id: string
  name: string
  zoneId: string
  zoneName: string
  createdAt: string
  updatedAt: string
}

export interface Zone {
  id: string
  name: string
  status: string
}

export interface Service {
  id: string
  name: string
  targetHost: string
  targetPort: number
  protocol: 'tcp' | 'udp'
  bindPort: number
  gatewayMode: 'none' | 'upnp' | 'natpmp'
  gatewayAddress: string
  scheme: 'http' | 'https'
  publishMode: 'direct' | 'redirect'
  cloudflareConnectionId: string
  entryHostname: string
  originHostname: string
  redirectStatus: 302 | 307
  preservePath: boolean
  preserveQuery: boolean
  manageDns: boolean
  enabled: boolean
  status: string
  lastError?: string
  publicIp?: string
  publicPort?: number
  mappingChangedAt?: string
  createdAt: string
  updatedAt: string
}

export interface DiagnosticCheck {
  key: string
  label: string
  category: 'environment' | 'target' | 'stun' | 'gateway' | 'runtime' | 'external'
  status: 'pass' | 'warn' | 'fail' | 'info'
  message: string
  durationMs: number
}

export interface DiagnosticReport {
  serviceId: string
  outcome: 'pass' | 'warning' | 'fail'
  stunFeasible: boolean
  targetReady: boolean
  gatewayReady: boolean
  mappingActive: boolean
  externalInboundVerified: boolean
  checkedAt: string
  checks: DiagnosticCheck[]
}

export interface EventItem {
  id: string
  serviceId?: string
  type: string
  level: string
  message: string
  payload?: Record<string, unknown>
  createdAt: string
}

export interface Webhook {
  id: string
  name: string
  url: string
  allowPrivate: boolean
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export interface ApiErrorShape {
  error?: { code?: string; message?: string }
}
