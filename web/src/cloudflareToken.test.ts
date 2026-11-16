import { describe, expect, it } from 'vitest'
import { cloudflareTokenTemplateURL, tokenPresets, type TokenPreset } from './cloudflareToken'

function template(preset: TokenPreset, options: Parameters<typeof cloudflareTokenTemplateURL>[1] = {}) {
  const url = new URL(cloudflareTokenTemplateURL(preset, options))
  return { url, groups: JSON.parse(url.searchParams.get('permissionGroupKeys')!) }
}

describe('Cloudflare token creation templates', () => {
  it('prefills the three Tunnel permissions accepted by the dashboard', () => {
    const { url, groups } = template('tunnel')
    expect(url.origin).toBe('https://dash.cloudflare.com')
    expect(url.pathname).toBe('/profile/api-tokens')
    expect(url.searchParams.get('name')).toBe('StunDeck Tunnel')
    expect(groups).toEqual([
      { key: 'zone', type: 'read' },
      { key: 'dns', type: 'edit' },
      { key: 'argotunnel', type: 'edit' },
    ])
  })

  it('limits each additional preset to the permissions used by that publishing mode', () => {
    expect(template('dns').groups).toEqual([{ key: 'zone', type: 'read' }, { key: 'dns', type: 'edit' }])
    expect(template('warp').groups).toEqual([{ key: 'zone', type: 'read' }, { key: 'argotunnel', type: 'edit' }])
    expect(template('workers').groups).toEqual([
      { key: 'zone', type: 'read' }, { key: 'dns', type: 'edit' },
      { key: 'workers_scripts', type: 'edit' }, { key: 'workers_routes', type: 'edit' },
    ])
    expect(template('spectrum').groups).toEqual([
      { key: 'zone', type: 'read' }, { key: 'dns', type: 'read' }, { key: 'zone_settings', type: 'edit' },
    ])
  })

  it('only adds zone-level Access when explicitly selected for a compatible mode', () => {
    const { url, groups } = template('tunnel', { includeAccess: true })
    expect(groups).toContainEqual({ key: 'access', type: 'edit' })
    expect(url.searchParams.get('name')).toBe('StunDeck Tunnel + Access')
    expect(template('tunnel').groups).not.toContainEqual({ key: 'access', type: 'edit' })
    for (const preset of ['warp', 'spectrum'] as const) {
      expect(template(preset, { includeAccess: true }).groups).toEqual(template(preset).groups)
      expect(template(preset, { includeAccess: true }).url.searchParams.get('name')).not.toContain('Access')
    }
  })

  it('keeps a known Zone scoped even when its Account is not yet known', () => {
    const generic = template('tunnel').url.searchParams
    expect(generic.get('accountId')).toBe('*')
    expect(generic.get('zoneId')).toBe('all')
    const zoneOnly = template('tunnel', { zoneId: 'test-zone' }).url.searchParams
    expect(zoneOnly.get('zoneId')).toBe('test-zone')
    expect(zoneOnly.get('accountId')).toBe('*')
    const scoped = template('tunnel', { zoneId: 'test-zone', accountId: 'test-account' }).url.searchParams
    expect(scoped.get('zoneId')).toBe('test-zone')
    expect(scoped.get('accountId')).toBe('test-account')
  })

  it('encodes scope values without adding or overriding query parameters', () => {
    const { url, groups } = template('tunnel', { zoneId: 'zone&zoneId=all', accountId: 'account&name=other#fragment' })
    expect(url.searchParams.getAll('zoneId')).toEqual(['zone&zoneId=all'])
    expect(url.searchParams.get('accountId')).toBe('account&name=other#fragment')
    expect(url.searchParams.get('name')).toBe('StunDeck Tunnel')
    expect(url.hash).toBe('')
    expect(groups).toEqual(template('tunnel').groups)
  })

  it('never requests token administration or includes secrets in the creation URL', () => {
    for (const preset of Object.keys(tokenPresets) as TokenPreset[]) {
      const { url, groups } = template(preset, { includeAccess: true })
      expect([...url.searchParams.keys()].sort()).toEqual(['accountId', 'name', 'permissionGroupKeys', 'zoneId'])
      expect(groups.some((group: { key: string }) => group.key.includes('api_tokens'))).toBe(false)
      expect(new Set(groups.map((group: { key: string }) => group.key)).size).toBe(groups.length)
    }
  })
})
