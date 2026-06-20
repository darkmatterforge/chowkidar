import type { Container, HistoryEntry } from './api/types'

export function timeAgo(iso: string): string {
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60)   return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  return `${Math.floor(s / 3600)}h ago`
}

export function fmtTimestamp(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

export function containerHealth(c: Container): 'ok' | 'warn' | 'danger' | 'muted' {
  const h = c.health?.toLowerCase()
  if (h === 'healthy' || (c.state === 'running' && !h)) return 'ok'
  if (h === 'unhealthy' || c.state === 'exited')         return 'danger'
  if (c.state === 'restarting')                          return 'warn'
  return 'muted'
}

export function statusDotColor(c: Container): string {
  const h = containerHealth(c)
  if (h === 'ok')     return 'var(--ok)'
  if (h === 'danger') return 'var(--danger)'
  if (h === 'warn')   return 'var(--warn)'
  return 'var(--muted)'
}

export function uptimePct(entries: HistoryEntry[]): number {
  const last40 = entries.slice(-40)
  const meaningful = last40.filter(e =>
    e.status === 'success' || e.status === 'failed' ||
    e.status === 'healthy' || e.status === 'recovered',
  )
  if (meaningful.length === 0) return -1
  const ok = meaningful.filter(e =>
    e.status === 'success' || e.status === 'healthy' || e.status === 'recovered',
  ).length
  return Math.round((ok / meaningful.length) * 100)
}

/** Narrow an unknown catch value to a human-readable message. */
export function errorMessage(err: unknown, fallback = 'An unexpected error occurred'): string {
  if (err instanceof Error) return err.message
  if (typeof err === 'string') return err
  return fallback
}
