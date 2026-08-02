import { describe, expect, it } from 'vitest'
import { createServiceDraft, serviceToDraft } from './serviceForm'
import type { CloudflareConnection, Service } from './types'

describe('service form values', () => {
  it('uses the first Cloudflare connection for a new service', () => {
    const connection = { id: 'connection-1' } as CloudflareConnection
    expect(createServiceDraft([connection]).cloudflareConnectionId).toBe('connection-1')
  })

  it('copies every editable field from an existing service', () => {
    const service = {
      id: 'service-1',
      name: 'Private panel',
      targetHost: '10.1.2.191',
      targetPort: 5666,
      protocol: 'tcp',
      bindPort: 40123,
      gatewayMode: 'upnp',
      gatewayAddress: '10.1.0.1',
      scheme: 'http',
      publishMode: 'redirect',
      cloudflareConnectionId: 'connection-1',
      entryHostname: 'panel.example.com',
      originHostname: '',
      redirectStatus: 307,
      preservePath: false,
      preserveQuery: true,
      manageDns: false,
      enabled: false,
      status: 'stopped',
      createdAt: '2026-08-03T00:00:00Z',
      updatedAt: '2026-08-03T00:00:00Z',
    } satisfies Service

    expect(serviceToDraft(service)).toEqual({
      name: 'Private panel',
      targetHost: '10.1.2.191',
      targetPort: 5666,
      protocol: 'tcp',
      bindPort: 40123,
      gatewayMode: 'upnp',
      gatewayAddress: '10.1.0.1',
      scheme: 'http',
      publishMode: 'redirect',
      cloudflareConnectionId: 'connection-1',
      entryHostname: 'panel.example.com',
      originHostname: '',
      redirectStatus: 307,
      preservePath: false,
      preserveQuery: true,
      manageDns: false,
    })
  })
})
