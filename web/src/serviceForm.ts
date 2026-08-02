import type { CloudflareConnection, Service } from './types'

export interface ServiceDraft {
  name: string
  targetHost: string
  targetPort: number
  protocol: 'tcp' | 'udp'
  bindPort: number
  scheme: 'http' | 'https'
  publishMode: 'direct' | 'redirect'
  cloudflareConnectionId: string
  entryHostname: string
  originHostname: string
  redirectStatus: 302 | 307
  preservePath: boolean
  preserveQuery: boolean
  manageDns: boolean
}

export function createServiceDraft(connections: CloudflareConnection[] = []): ServiceDraft {
  return {
    name: '',
    targetHost: '',
    targetPort: 80,
    protocol: 'tcp',
    bindPort: 0,
    scheme: 'http',
    publishMode: 'direct',
    cloudflareConnectionId: connections[0]?.id ?? '',
    entryHostname: '',
    originHostname: '',
    redirectStatus: 302,
    preservePath: true,
    preserveQuery: true,
    manageDns: true,
  }
}

export function serviceToDraft(service: Service): ServiceDraft {
  return {
    name: service.name,
    targetHost: service.targetHost,
    targetPort: service.targetPort,
    protocol: service.protocol,
    bindPort: service.bindPort,
    scheme: service.scheme,
    publishMode: service.publishMode,
    cloudflareConnectionId: service.cloudflareConnectionId,
    entryHostname: service.entryHostname,
    originHostname: service.originHostname,
    redirectStatus: service.redirectStatus,
    preservePath: service.preservePath,
    preserveQuery: service.preserveQuery,
    manageDns: service.manageDns,
  }
}
