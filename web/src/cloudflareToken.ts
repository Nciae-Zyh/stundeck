export const cloudflareTokenPage = 'https://dash.cloudflare.com/profile/api-tokens'

// User token templates require dashboard short keys (not API permission group
// IDs or "write"). Unknown keys are silently ignored by Cloudflare.
// https://developers.cloudflare.com/fundamentals/api/how-to/account-owned-token-template/
// "argotunnel" and "dynamic_redirect" were checked in the live creation form.
const permissions = {
  zone: { key: 'zone', type: 'read', label: 'Zone · Zone · Read' },
  dns: { key: 'dns', type: 'edit', label: 'Zone · DNS · Edit' },
  dnsRead: { key: 'dns', type: 'read', label: 'Zone · DNS · Read' },
  redirect: { key: 'dynamic_redirect', type: 'edit', label: 'Zone · Single Redirect · Edit' },
  tunnel: { key: 'argotunnel', type: 'edit', label: 'Account · Cloudflare Tunnel · Edit' },
  workers: { key: 'workers_scripts', type: 'edit', label: 'Account · Workers Scripts · Edit' },
  workerRoutes: { key: 'workers_routes', type: 'edit', label: 'Zone · Workers Routes · Edit' },
  zoneSettings: { key: 'zone_settings', type: 'edit', label: 'Zone · Zone Settings · Edit' },
  access: { key: 'access', type: 'edit', label: 'Zone · Access: Apps and Policies · Edit' },
} as const

type Permission = (typeof permissions)[keyof typeof permissions]
export type TokenPreset = 'redirect' | 'tunnel' | 'dns' | 'warp' | 'workers' | 'spectrum'

export const tokenPresets: Record<TokenPreset, {
  label: string
  name: string
  description: string
  access: boolean
  permissions: readonly Permission[]
}> = {
  redirect: {
    label: 'STUN + 302 跳转（含自动 DNS）', name: 'STUN 302', access: false,
    description: '把 STUN 映射得到的公网 IP 和动态端口同步为 302 / 307 跳转，并自动管理入口与源站 DNS。',
    permissions: [permissions.zone, permissions.dns, permissions.redirect],
  },
  tunnel: {
    label: 'Tunnel · 内网服务发布', name: 'Tunnel', access: true,
    description: '创建 Tunnel 并绑定域名，无需公网入站端口。',
    permissions: [permissions.zone, permissions.dns, permissions.tunnel],
  },
  dns: {
    label: 'DNS / HTTP 代理', name: 'DNS', access: true,
    description: '同步 DNS 记录或启用 HTTP 代理；源站需公网可回连。',
    permissions: [permissions.zone, permissions.dns],
  },
  warp: {
    label: 'WARP · 私网访问', name: 'WARP', access: false,
    description: '通过 Tunnel 管理私网路由；客户端和 Gateway 策略需另行配置。',
    permissions: [permissions.zone, permissions.tunnel],
  },
  workers: {
    label: 'Workers · HTTPS 代理', name: 'Workers', access: true,
    description: '发布 Worker、绑定路由并同步源站 DNS。',
    permissions: [permissions.zone, permissions.dns, permissions.workers, permissions.workerRoutes],
  },
  spectrum: {
    label: 'Spectrum · TCP / UDP 代理', name: 'Spectrum', access: false,
    description: '管理 Spectrum 并读取 DNS 冲突；仍需相应套餐和协议授权。',
    permissions: [permissions.zone, permissions.dnsRead, permissions.zoneSettings],
  },
}

export function tokenTemplatePermissions(preset: TokenPreset, includeAccess = false): readonly Permission[] {
  const profile = tokenPresets[preset]
  return includeAccess && profile.access ? [...profile.permissions, permissions.access] : profile.permissions
}

export function cloudflareTokenTemplateURL(preset: TokenPreset, options: {
  includeAccess?: boolean
  zoneId?: string
  accountId?: string
} = {}) {
  const includeAccess = !!options.includeAccess && tokenPresets[preset].access
  const query = new URLSearchParams({
    permissionGroupKeys: JSON.stringify(tokenTemplatePermissions(preset, includeAccess).map(({ key, type }) => ({ key, type }))),
    accountId: options.accountId || '*',
    zoneId: options.zoneId || 'all',
    name: `StunDeck ${tokenPresets[preset].name}${includeAccess ? ' + Access' : ''}`,
  })
  return `${cloudflareTokenPage}?${query}`
}
