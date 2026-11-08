export class ApiError extends Error {
  status: number
  code: number

  constructor(message: string, status: number, code: number) {
    super(message)
    this.status = status
    this.code = code
  }
}

interface Envelope<T> {
  code: number
  message: string
  data: T
}

export function csrfToken(): string {
  const match = document.cookie.split(';').map((v) => v.trim()).find((v) => v.startsWith('pg_csrf='))
  return match ? decodeURIComponent(match.slice('pg_csrf='.length)) : ''
}

function csrfHeaders(): Record<string, string> {
  const token = csrfToken()
  return token ? { 'X-CSRF-Token': token } : {}
}

async function request<T>(path: string, init: RequestInit = {}, retry = true): Promise<T> {
  const method = (init.method || 'GET').toUpperCase()
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...(init.headers as Record<string, string> | undefined),
  }
  if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    Object.assign(headers, csrfHeaders())
  }
  if (init.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json'
  }
  let res: Response
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 15_000)
  try {
    res = await fetch(`/api/v1${path}`, {
      credentials: 'include',
      ...init,
      headers,
      signal: controller.signal,
    })
  } catch (err) {
    window.clearTimeout(timeout)
    if (retry && method === 'GET' && !(err instanceof DOMException && err.name === 'AbortError')) {
      await new Promise((resolve) => setTimeout(resolve, 250))
      return request<T>(path, init, false)
    }
    throw new ApiError(err instanceof DOMException && err.name === 'AbortError' ? '请求超时，请稍后重试' : '网络连接失败，请稍后重试', 0, 0)
  }
  let text = ''
  try {
    text = await res.text()
  } catch {
    window.clearTimeout(timeout)
    throw new ApiError('读取响应失败，请稍后重试', res.status, 0)
  }
  window.clearTimeout(timeout)
  let body: Envelope<T> | null = null
  try {
    body = text ? (JSON.parse(text) as Envelope<T>) : null
  } catch {
    body = null
  }
  if (res.status === 401 && !path.startsWith('/auth/login')) {
    window.dispatchEvent(new CustomEvent('pm:unauthorized'))
  }
  if (!res.ok) {
    throw new ApiError(body?.message || `请求失败（HTTP ${res.status}）`, res.status, body?.code ?? res.status)
  }
  return (body?.data ?? null) as T
}

export const api = {
  get<T>(path: string): Promise<T> {
    return request<T>(path)
  },
  post<T>(path: string, body?: unknown): Promise<T> {
    return request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) })
  },
  put<T>(path: string, body?: unknown): Promise<T> {
    return request<T>(path, { method: 'PUT', body: body === undefined ? undefined : JSON.stringify(body) })
  },
  del<T>(path: string, body?: unknown): Promise<T> {
    return request<T>(path, { method: 'DELETE', body: body === undefined ? undefined : JSON.stringify(body) })
  },
}

export function formatBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : v >= 10 ? 1 : 2)} ${units[i]}`
}

export function formatNumber(n: number): string {
  return new Intl.NumberFormat('zh-CN').format(n ?? 0)
}

export function formatTime(value?: string | null): string {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '-'
  return d.toLocaleString('zh-CN', { hour12: false })
}

export function relativeTime(value?: string | null): string {
  if (!value) return '-'
  const diff = Date.now() - new Date(value).getTime()
  const abs = Math.abs(diff)
  const suffix = diff >= 0 ? '前' : '后'
  if (abs < 60_000) return `${Math.round(abs / 1000)} 秒${suffix}`
  if (abs < 3_600_000) return `${Math.round(abs / 60_000)} 分钟${suffix}`
  if (abs < 86_400_000) return `${Math.round(abs / 3_600_000)} 小时${suffix}`
  return `${Math.round(abs / 86_400_000)} 天${suffix}`
}
