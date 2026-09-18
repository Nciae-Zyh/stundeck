import type { CloudflareConnection, Service, PublishMode, TunnelProtocol } from './types'

export interface ServiceDraft {
	privateNetwork: string
  name: string
  targetHost: string
  targetPort: number
  protocol: 'tcp' | 'udp'
  bindPort: number
  gatewayMode: 'none' | 'upnp' | 'natpmp'
  gatewayAddress: string
  scheme: 'http' | 'https'
  publishMode: PublishMode
  tunnelProtocol: TunnelProtocol
  edgePort: number
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
    privateNetwork: '',
    name: '',
    targetHost: '',
    targetPort: 80,
    protocol: 'tcp',
    bindPort: 0,
    gatewayMode: 'none',
    gatewayAddress: '',
    scheme: 'http',
    publishMode: 'direct',
    tunnelProtocol: 'http',
    edgePort: 443,
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
    privateNetwork: service.privateNetwork || '',
    name: service.name,
    targetHost: service.targetHost,
    targetPort: service.targetPort,
    protocol: service.protocol,
    bindPort: service.bindPort,
    gatewayMode: service.gatewayMode,
    gatewayAddress: service.gatewayAddress,
    scheme: service.scheme,
    publishMode: service.publishMode,
    tunnelProtocol: service.tunnelProtocol || 'http',
    edgePort: service.edgePort || 443,
    cloudflareConnectionId: service.cloudflareConnectionId,
    entryHostname: service.entryHostname,
    originHostname: service.originHostname,
    redirectStatus: service.redirectStatus,
    preservePath: service.preservePath,
    preserveQuery: service.preserveQuery,
    manageDns: service.manageDns,
  }
}
