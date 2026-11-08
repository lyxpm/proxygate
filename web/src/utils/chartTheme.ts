import { formatBytes, formatNumber } from '@/api/client'
import type { Totals, TrafficPoint } from '@/api/types'

export type RangeKey = '1h' | '6h' | '24h' | '7d' | '30d'
export type MetricKey = 'requests' | 'traffic' | 'latency'

export const RANGE_OPTIONS: { value: RangeKey; label: string }[] = [
  { value: '1h', label: '最近 1 小时' },
  { value: '6h', label: '最近 6 小时' },
  { value: '24h', label: '最近 24 小时' },
  { value: '7d', label: '最近 7 天' },
  { value: '30d', label: '最近 30 天' },
]

export const RANGE_LABELS: Record<RangeKey, string> = {
  '1h': '最近 1 小时',
  '6h': '最近 6 小时',
  '24h': '最近 24 小时',
  '7d': '最近 7 天',
  '30d': '最近 30 天',
}

export const CHART_COLORS = {
  requests: '#0EA5A4',
  inbound: '#6366F1',
  outbound: '#F59E0B',
  average: '#0EA5A4',
  p95: '#6366F1',
  p99: '#F59E0B',
  success2xx: '#10B981',
  status3xx: '#0EA5A4',
  status4xx: '#F59E0B',
  status5xx: '#EF4444',
  grid: '#EEF2F7',
  axis: '#94A3B8',
  text: '#475569',
}

export function rangeLabel(range: string): string {
  return RANGE_LABELS[range as RangeKey] ?? range
}

export function formatChartTime(value: string, range: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  const options: Intl.DateTimeFormatOptions =
    range === '1h' || range === '6h'
      ? { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }
      : range === '24h'
        ? { hour: '2-digit', minute: '2-digit', hour12: false }
        : { month: '2-digit', day: '2-digit' }
  return d.toLocaleString('zh-CN', options).replace(/\//g, '-')
}

export function formatFullTime(value: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

export function formatUpdatedAt(value: number): string {
  if (!value) return '尚未更新'
  return new Date(value).toLocaleTimeString('zh-CN', { hour12: false })
}

export function axisInterval(count: number, maxTicks = 7): number {
  return Math.max(1, Math.ceil(count / maxTicks)) - 1
}

export function averageRequestRate(requests: number, range: string): string {
  const minutes: Record<string, number> = { '1h': 60, '6h': 360, '24h': 1440, '7d': 10080, '30d': 43200 }
  const perMinute = requests / (minutes[range] ?? 1)
  if (perMinute >= 1) return `${perMinute.toFixed(1)} 次/分`
  return `${(perMinute * 60).toFixed(2)} 次/小时`
}

export function rangeRequests(points: TrafficPoint[]): number {
  return points.reduce((sum, p) => sum + (p.requests || 0), 0)
}

export function statusTotal(totals?: Totals | null): number {
  if (!totals) return 0
  return totals.status_2xx + totals.status_3xx + totals.status_4xx + totals.status_5xx
}

export function trafficTotal(points: TrafficPoint[]): number {
  return points.reduce((sum, p) => sum + (p.inbound_bytes || 0) + (p.outbound_bytes || 0), 0)
}

export function metricPeak(points: TrafficPoint[], metric: MetricKey): number {
  if (!points.length) return 0
  if (metric === 'requests') return Math.max(...points.map((p) => p.requests || 0))
  if (metric === 'latency') return Math.max(...points.map((p) => p.avg_ms || 0))
  return Math.max(...points.map((p) => Math.max(p.inbound_bytes || 0, p.outbound_bytes || 0)))
}

export function metricTotal(points: TrafficPoint[], metric: MetricKey): number {
  if (metric === 'requests') return rangeRequests(points)
  if (metric === 'latency') {
    const total = points.reduce((sum, p) => sum + (p.avg_ms || 0) * (p.requests || 0), 0)
    const requests = rangeRequests(points)
    return requests ? Math.round(total / requests) : 0
  }
  return points.reduce((sum, p) => sum + (p.inbound_bytes || 0) + (p.outbound_bytes || 0), 0)
}

export function formatMetricValue(value: number, metric: MetricKey): string {
  if (metric === 'traffic') return formatBytes(value)
  if (metric === 'latency') return `${formatNumber(value)} ms`
  return formatNumber(value)
}
