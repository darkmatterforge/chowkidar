import type {
  AuthStatus, Health, ContainersPayload, JobsResponse, Job,
  NotificationProfile, DockerHost, DockerHostsStatusResponse,
  MaintenanceWindow, MaintenanceActiveResponse, SystemAlert,
  HistoryResponse, Settings, Diagnostics,
} from './types'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json', ...init?.headers },
    ...init,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    const err = new Error(body.error ?? res.statusText) as Error & { status: number }
    err.status = res.status
    throw err
  }
  return res.json() as Promise<T>
}

// ── Auth ──────────────────────────────────────────────────────────────────────
export const auth = {
  status: () => request<AuthStatus>('/api/auth/status'),
  login: (username: string, password: string, remember: boolean) =>
    request<void>('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password, remember }) }),
  logout: () => request<void>('/api/auth/logout', { method: 'POST' }),
  changePassword: (current: string, next: string) =>
    request<void>('/api/auth/change-password', { method: 'POST', body: JSON.stringify({ current, next }) }),
  disable: (password: string) =>
    request<void>('/api/auth/disable', { method: 'POST', body: JSON.stringify({ password }) }),
}

// ── Health / Diagnostics ──────────────────────────────────────────────────────
export const health = {
  get: () => request<Health>('/api/health'),
  diagnostics: () => request<Diagnostics>('/api/diagnostics'),
}

// ── Containers ────────────────────────────────────────────────────────────────
export const containers = {
  list: () => request<ContainersPayload>('/api/containers'),
  resetCooldown: (id: string) =>
    request<void>(`/api/containers/${id}/reset-cooldown`, { method: 'POST' }),
}

// ── History ───────────────────────────────────────────────────────────────────
export const history = {
  list: (params?: { limit?: number; offset?: number; bars?: boolean }) => {
    const q = new URLSearchParams()
    if (params?.limit)  q.set('limit',  String(params.limit))
    if (params?.offset) q.set('offset', String(params.offset))
    if (params?.bars)   q.set('bars',   'true')
    return request<HistoryResponse>(`/api/history?${q}`)
  },
  clear: () => request<void>('/api/history', { method: 'DELETE' }),
}

// ── Jobs ──────────────────────────────────────────────────────────────────────
export const jobs = {
  list: (params?: { q?: string; enabled?: string; action?: string; hostID?: string; limit?: number; offset?: number }) => {
    const q = new URLSearchParams()
    if (params?.q)       q.set('q',       params.q)
    if (params?.enabled) q.set('enabled', params.enabled)
    if (params?.action)  q.set('action',  params.action)
    if (params?.hostID)  q.set('hostID',  params.hostID)
    if (params?.limit)   q.set('limit',   String(params.limit))
    if (params?.offset)  q.set('offset',  String(params.offset))
    return request<JobsResponse>(`/api/jobs?${q}`)
  },
  get: (id: string) => request<Job>(`/api/jobs/${id}`),
  create: (job: Omit<Job, 'id'>) =>
    request<Job>('/api/jobs', { method: 'POST', body: JSON.stringify(job) }),
  update: (id: string, job: Partial<Job>) =>
    request<Job>(`/api/jobs/${id}`, { method: 'PUT', body: JSON.stringify(job) }),
  delete: (id: string) => request<void>(`/api/jobs/${id}`, { method: 'DELETE' }),
}

// ── Notifications ─────────────────────────────────────────────────────────────
export const notifications = {
  list: () => request<NotificationProfile[]>('/api/notifications'),
  create: (p: Omit<NotificationProfile, 'id'>) =>
    request<NotificationProfile>('/api/notifications', { method: 'POST', body: JSON.stringify(p) }),
  update: (id: string, p: Partial<NotificationProfile>) =>
    request<NotificationProfile>(`/api/notifications/${id}`, { method: 'PUT', body: JSON.stringify(p) }),
  delete: (id: string) => request<void>(`/api/notifications/${id}`, { method: 'DELETE' }),
  test: (id: string) => request<void>('/api/test-notification', { method: 'POST', body: JSON.stringify({ id }) }),
}

// ── Docker Hosts ──────────────────────────────────────────────────────────────
export const dockerHosts = {
  list: () => request<DockerHost[]>('/api/docker-hosts'),
  status: () => request<DockerHostsStatusResponse>('/api/docker-hosts/status'),
  create: (h: Omit<DockerHost, 'id'>) =>
    request<DockerHost>('/api/docker-hosts', { method: 'POST', body: JSON.stringify(h) }),
  update: (id: string, h: Partial<DockerHost>) =>
    request<DockerHost>(`/api/docker-hosts/${id}`, { method: 'PUT', body: JSON.stringify(h) }),
  delete: (id: string) => request<void>(`/api/docker-hosts/${id}`, { method: 'DELETE' }),
  test: (h: Partial<DockerHost>) =>
    request<{ ok: boolean; error?: string }>('/api/test-docker-host', { method: 'POST', body: JSON.stringify(h) }),
}

// ── Manual container actions ──────────────────────────────────────────────────
export const containerActions = {
  run: (container: string, name: string, action: string) =>
    request<{ queued: boolean; error?: string }>('/api/action', {
      method: 'POST', body: JSON.stringify({ container, name, action }),
    }),
  resetCooldown: (containerName: string) =>
    request<{ reset: boolean; error?: string }>(
      `/api/containers/${encodeURIComponent(containerName)}/reset-cooldown`,
      { method: 'POST' },
    ),
}

// ── Maintenance ───────────────────────────────────────────────────────────────
export const maintenance = {
  list: () => request<MaintenanceWindow[]>('/api/maintenance'),
  active: () => request<MaintenanceActiveResponse>('/api/maintenance/active'),
  create: (w: Omit<MaintenanceWindow, 'id'>) =>
    request<MaintenanceWindow>('/api/maintenance', { method: 'POST', body: JSON.stringify(w) }),
  update: (id: string, w: Partial<MaintenanceWindow>) =>
    request<MaintenanceWindow>(`/api/maintenance/${id}`, { method: 'PUT', body: JSON.stringify(w) }),
  delete: (id: string) => request<void>(`/api/maintenance/${id}`, { method: 'DELETE' }),
}

// ── System Alerts ─────────────────────────────────────────────────────────────
export const systemAlerts = {
  list: () => request<SystemAlert[]>('/api/system-alerts'),
  dismiss: (ids: string[]) =>
    request<void>('/api/system-alerts/dismiss', { method: 'POST', body: JSON.stringify({ ids }) }),
}

// ── Settings ──────────────────────────────────────────────────────────────────
export const settings = {
  get: () => request<Settings>('/api/settings'),
  update: (s: Partial<Settings>) =>
    request<Settings>('/api/settings', { method: 'PUT', body: JSON.stringify(s) }),
  setTheme: (theme: string) =>
    request<void>('/api/settings/theme', { method: 'POST', body: JSON.stringify({ theme }) }),
}

// ── Scripts ───────────────────────────────────────────────────────────────────
export const scripts = {
  dryRun: (script: string, containerID?: string) =>
    request<{ output: string; exitCode: number }>('/api/scripts/dry-run', {
      method: 'POST', body: JSON.stringify({ script, containerID }),
    }),
  cleanup: () => request<void>('/api/scripts/dry-run/cleanup', { method: 'POST' }),
}
