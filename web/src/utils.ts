export function formatEndpoint(ip?: string, port?: number) {
  if (!ip || !port) return '等待映射'
  const host = ip.includes(':') ? `[${ip}]` : ip
  return `${host}:${port}`
}

export function formatRelativeTime(value: string, now = Date.now()) {
  const seconds = Math.round((now - new Date(value).getTime()) / 1000)
  if (seconds < 60) return `${Math.max(0, seconds)} 秒前`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return new Date(value).toLocaleString('zh-CN')
}
