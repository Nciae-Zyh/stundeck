import { describe, expect, it } from 'vitest'
import { canConfigure, modeBlockReason, serviceURL } from './cloudflare'
import { createServiceDraft, serviceToDraft } from './serviceForm'
import type { CloudflareInspection, Service } from './types'

describe('Cloudflare publishing', () => {
  const report: CloudflareInspection = { zone: { id: 'z', name: 'example.com', status: 'active', account: { id: 'a', name: 'Example' } }, capabilities: { dns: { state: 'available', permission: 'DNS Edit', message: 'confirmed' }, tunnel: { state: 'unverified', permission: 'Tunnel Edit', message: 'unverified' }, access: { state: 'read_only', permission: 'Access Edit', message: 'read only' } }, domains: [], applications: [], accessScope: 'accounts', checkedAt: '2026-09-18T00:00:00Z' }
  it('gates configuration without treating read permission as write permission', () => {
    expect(canConfigure(report.capabilities.access)).toBe(false)
    expect(canConfigure(report.capabilities.tunnel)).toBe(true)
    expect(modeBlockReason('tunnel', report, false)).toContain('cloudflared')
    expect(modeBlockReason('tunnel', report, true)).toBe('')
    expect(modeBlockReason('direct', null, false)).toBe('')
    expect(modeBlockReason('proxy', null, true)).not.toBe('')
  })
  it('keeps port numbers for DNS / proxy and avoids HTTP links for raw TCP tunnels', () => {
    const service = { ...createServiceDraft(), entryHostname: 'nas.example.com', publicPort: 8080, publishMode: 'proxy' } as Service
    expect(serviceURL(service)).toBe('http://nas.example.com:8080')
    service.publishMode = 'tunnel'; service.tunnelProtocol = 'http'
    expect(serviceURL(service)).toBe('https://nas.example.com')
    service.tunnelProtocol = 'ssh'
    expect(serviceURL(service)).toBe('')
    service.publishMode = 'spectrum'
    expect(serviceURL(service)).toBe('')
  })
  it('round-trips tunnel protocol and Spectrum port through edits', () => {
    const service = { ...createServiceDraft(), publishMode: 'tunnel', tunnelProtocol: 'ssh', edgePort: 2222 } as Service
    expect(serviceToDraft(service)).toMatchObject({ publishMode: 'tunnel', tunnelProtocol: 'ssh', edgePort: 2222 })
  })
  it('gates WARP and Workers by their own permissions while Quick needs no token', () => {
    expect(modeBlockReason('quick', null, true)).toBe('')
    expect(modeBlockReason('quick', null, false)).toContain('cloudflared')
    expect(modeBlockReason('warp', report, true)).toContain('WARP')
    expect(modeBlockReason('workers', report, true)).toContain('Workers')
    const capabilities = { ...report.capabilities, private_network: report.capabilities.tunnel!, workers: report.capabilities.tunnel!, worker_routes: report.capabilities.tunnel! }
    expect(modeBlockReason('warp', { ...report, capabilities: { ...capabilities, dns: report.capabilities.access! } }, true)).toBe('')
    expect(modeBlockReason('workers', { ...report, capabilities }, false)).toBe('')
  })
  it('only displays active Quick URLs and preserves private CIDR on edit', () => {
    const service = { ...createServiceDraft(), publishMode: 'quick', enabled: true, runtimeUrl: 'https://test.trycloudflare.com' } as Service
    expect(serviceURL(service)).toBe(service.runtimeUrl)
    service.enabled = false
    expect(serviceURL(service)).toBe('')
    service.enabled = true; service.runtimeUrl = 'https://test.trycloudflare.com.evil.test'
    expect(serviceURL(service)).toBe('')
    service.publishMode = 'warp'; service.privateNetwork = '10.0.0.0/24'
    expect(serviceToDraft(service).privateNetwork).toBe('10.0.0.0/24')
    expect(serviceURL(service)).toBe('')
  })
})
