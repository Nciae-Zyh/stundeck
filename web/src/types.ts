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
  tunnelAvailable: boolean
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

export type PublishMode = 'direct' | 'redirect' | 'dns' | 'proxy' | 'tunnel' | 'spectrum' | 'quick' | 'warp' | 'workers'
export type TunnelProtocol = 'http' | 'https' | 'tcp' | 'ssh' | 'rdp'

export interface Zone {
  id: string
  name: string
  status: string
  account: { id: string; name: string }
}

export interface Service {
	privateNetwork?: string
	runtimeUrl?: string
  id: string
  name: string
  targetHost: string
  targetPort: number
  protocol: 'tcp' | 'udp'
  bindPort: number
  gatewayMode: 'none' | 'upnp' | 'natpmp'
  gatewayAddress: string
  scheme: 'http' | 'https'
  publishMode: PublishMode
  tunnelProtocol?: TunnelProtocol
  edgePort?: number
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

export interface NetworkDiagnosticReport {
  outcome: 'pass' | 'warning' | 'fail'
  natType: 'open_internet' | 'nat_detected' | 'endpoint_independent' | 'address_dependent' | 'address_port_dependent' | 'unknown'
  udpStun: boolean
  tcpStun: boolean
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

export interface CloudflareCapability {
  state: 'available' | 'read_only' | 'unverified' | 'unavailable' | 'error'
  permission: string
  message: string
}
export interface CloudflareDomain {
  hostname: string
  proxied: boolean
  accessEligible: boolean
  reason: string
  applicationId?: string
  managed: boolean
  serviceId?: string
}
export interface CloudflareApplication {
  id: string
  name: string
  domain: string
  managed?: boolean
  session_duration: string
  policies: { decision: string; include: { email?: { email: string } }[] }[]
}
export interface CloudflareInspection {
  zone: Zone
  capabilities: Record<string, CloudflareCapability>
  domains: CloudflareDomain[]
  applications: CloudflareApplication[]
  accessScope: 'accounts' | 'zones'
  checkedAt: string
}
export interface InspectionResponse {
  inspection: CloudflareInspection
  tunnelAvailable: boolean
}
