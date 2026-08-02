import type { ApiErrorShape } from './types'

let csrfToken = ''

export class ApiError extends Error {
  status: number
  code: string

  constructor(message: string, status: number, code = 'request_failed') {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

export function setCSRFToken(value: string) {
  csrfToken = value
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body) headers.set('Content-Type', 'application/json')
  const method = (init.method ?? 'GET').toUpperCase()
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  if (!response.ok) {
    const payload = (await response.json().catch(() => ({}))) as ApiErrorShape
    throw new ApiError(payload.error?.message ?? `Request failed with ${response.status}`, response.status, payload.error?.code)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}
