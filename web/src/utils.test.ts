import { describe, expect, it } from 'vitest'
import { formatEndpoint, formatRelativeTime } from './utils'

describe('formatEndpoint', () => {
  it('formats IPv4 and IPv6 mappings safely', () => {
    expect(formatEndpoint('203.0.113.10', 45678)).toBe('203.0.113.10:45678')
    expect(formatEndpoint('2001:db8::1', 45678)).toBe('[2001:db8::1]:45678')
    expect(formatEndpoint()).toBe('等待映射')
  })
})

describe('formatRelativeTime', () => {
  it('formats recent events', () => {
    const now = new Date('2026-08-02T10:01:00Z').getTime()
    expect(formatRelativeTime('2026-08-02T10:00:30Z', now)).toBe('30 秒前')
    expect(formatRelativeTime('2026-08-02T09:51:00Z', now)).toBe('10 分钟前')
  })
})
