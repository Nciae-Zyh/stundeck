import type { CloudflareCapability, CloudflareInspection, PublishMode, Service } from './types'

export const modeLabels: Record<PublishMode, string> = {
  direct: '仅公网映射', dns: 'Cloudflare DNS 直连', redirect: 'Cloudflare Redirect',
  proxy: 'Cloudflare HTTP 代理', tunnel: 'Cloudflare Tunnel', spectrum: 'Cloudflare Spectrum',
  quick: 'Quick Tunnel · 临时分享', warp: 'WARP · 私网访问', workers: 'Workers · HTTPS 代理',
}
export const capabilityLabels: Record<string, string> = { dns: 'DNS / HTTP 代理', redirect: 'Redirect', tunnel: 'Tunnel', access: 'Access', spectrum: 'Spectrum', private_network: 'WARP 私网路由', workers: 'Workers 脚本', worker_routes: 'Workers 路由' }
export const usesConnector = (mode: PublishMode) => ['tunnel', 'quick', 'warp'].includes(mode)
export const usesCloudflareAccount = (mode: PublishMode) => !['direct', 'quick'].includes(mode)
export const capabilityStates: Record<CloudflareCapability['state'], string> = {
  available: '已确认写权限', read_only: '仅可读取', unverified: '写权限待确认', unavailable: '不可用', error: '检测失败',
}
export function canConfigure(capability?: CloudflareCapability) {
  return capability?.state === 'available' || capability?.state === 'unverified'
}
export function modeBlockReason(mode: PublishMode, report: CloudflareInspection | null, tunnelAvailable: boolean, manageDns = true) {
  if (mode === 'direct') return ''
  if (usesConnector(mode) && !tunnelAvailable) return '本机缺少 cloudflared，请安装或使用内置 cloudflared 的 Docker / fnOS 镜像'
  if (mode === 'quick') return ''
  if (!report) return '请先检测连接权限'
  if (mode !== 'warp' && report.zone.status !== 'active') return '所选 Zone 尚未激活'
  const required = mode === 'redirect' ? (manageDns ? ['redirect', 'dns'] : ['redirect'])
    : mode === 'tunnel' ? ['tunnel', 'dns'] : mode === 'warp' ? ['tunnel', 'private_network']
    : mode === 'workers' ? ['workers', 'worker_routes', 'dns'] : mode === 'spectrum' ? ['spectrum', 'dns'] : ['dns']
  for (const key of required) {
    // Spectrum needs DNS read to check conflicts, but Cloudflare owns its DNS writes.
    const cap = report.capabilities[key]
    if (mode === 'spectrum' && key === 'dns' && cap?.state === 'read_only') continue
    if (!canConfigure(cap)) return `${capabilityLabels[key]}：${cap?.message ?? '未检测'}；需要 ${cap?.permission ?? '相应权限'}`
  }
  return ''
}
export function serviceURL(service: Service) {
  if (service.publishMode === 'quick') return service.enabled && /^https:\/\/[a-z0-9-]+\.trycloudflare\.com$/.test(service.runtimeUrl || '') ? service.runtimeUrl! : ''
  if (service.publishMode === 'warp') return ''
  if (!service.entryHostname || service.protocol === 'udp') return ''
  if (service.publishMode === 'tunnel') {
    return ['http', 'https'].includes(service.tunnelProtocol || 'http') ? `https://${service.entryHostname}` : ''
  }
  if (service.publishMode === 'redirect' || service.publishMode === 'workers') return `https://${service.entryHostname}`
  if (['dns', 'proxy'].includes(service.publishMode) && service.publicPort) {
    return `${service.scheme}://${service.entryHostname}:${service.publicPort}`
  }
  return ''
}
