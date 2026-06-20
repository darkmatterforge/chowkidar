// ── Auth ─────────────────────────────────────────────────────────────────────
export interface AuthStatus {
  enabled: boolean
  loggedIn: boolean
  username: string
}

// ── Health ────────────────────────────────────────────────────────────────────
export interface Health {
  status: string
  version: string
  latestVersion?: string
  latestVersionRelDate?: string
  lastScan?: string
  lastScanValid?: boolean
  monitoringStartsAt?: string
  encryptionKeyMismatch?: boolean
  retriesExhaustedIDs?: string[]
  startExited?: boolean
  totalExitedCount?: number
  exitedCount?: number
}

// ── Diagnostics ───────────────────────────────────────────────────────────────
export interface Diagnostics {
  uptime: string
  goroutines: number
  memAllocMB: number
  workerCount: number
  activeJobs: number
  dockerReachable?: boolean
  socketWritable?: boolean
  details?: string
}

// ── Containers ────────────────────────────────────────────────────────────────
export interface Container {
  id: string
  name: string
  image: string
  status: string
  state: string
  health?: string
  restartCount: number
  hostID?: string
  hostName?: string
  labels?: Record<string, string>
  // job coverage
  jobName?: string
  jobAction?: string
  jobGroup?: string
  checkEligible?: boolean
  cooldownActive?: boolean
  retriesExhausted?: boolean
}

export interface ContainersPayload {
  all: Container[]
  unhealthy: Container[]
  exited: Container[]
}

// ── Jobs ──────────────────────────────────────────────────────────────────────
export interface Job {
  id: string
  name: string
  group?: string
  enabled: boolean
  action: string
  script?: string
  healthCheckScript?: string
  containerNameFilter?: string
  containerLabelFilter?: string
  containerEnvVarFilter?: string
  monitorInterval: number
  actionTimeout: number
  notifications?: string[]
  dockerContexts?: string[]
  postActionWait?: number
}

export interface JobsResponse {
  jobs: Job[]
  total: number
}

// ── Notifications ─────────────────────────────────────────────────────────────
export interface NotificationProfile {
  id: string
  name: string
  provider: string
  enabled: boolean
  config: Record<string, string>
}

// ── Docker Hosts ──────────────────────────────────────────────────────────────
export interface DockerHost {
  id: string
  name: string
  connType: string
  host?: string
  enabled: boolean
}

export interface DockerHostStatus {
  id: string
  name: string
  connected: boolean
  enabled?: boolean
  error?: string
  version?: string
  containers?: number
}

export interface DockerHostsStatusResponse {
  hosts: DockerHostStatus[]
}

// ── Maintenance ───────────────────────────────────────────────────────────────
export interface MaintenanceWindow {
  id: string
  name: string
  description?: string
  active: boolean
  scheduleType: 'manual' | 'cron'
  cronExpr?: string
  duration?: number
  timezone?: string
  startDate?: string
  endDate?: string
}

export interface MaintenanceActiveResponse {
  windows: MaintenanceWindow[]
}

// ── System Alerts ─────────────────────────────────────────────────────────────
export interface SystemAlert {
  id: string
  type: string
  message: string
  severity: 'info' | 'warn' | 'error'
  timestamp: string
  dismissed: boolean
}

// ── History ───────────────────────────────────────────────────────────────────
export interface HistoryEntry {
  id: string
  timestamp: string
  containerName?: string
  containerId?: string
  hostID?: string
  hostName?: string
  action?: string
  status?: string
  reason?: string
  error?: string
  output?: string
  attempt?: number
  jobName?: string
  jobID?: string
}

export interface HistoryResponse {
  entries: HistoryEntry[]
  total: number
}

// Bar entries are lightweight time-series pulses used for sparkbars / uptime %
export interface BarEntry {
  containerName?: string
  containerId?: string
  status: string   // 'success' | 'failed' | 'healthy' | 'recovered' | 'skipped' | 'exhausted'
  timestamp: string
  action?: string
  reason?: string
  error?: string
}

export interface BarsResponse {
  entries: BarEntry[]
}

// ── Settings ──────────────────────────────────────────────────────────────────
export interface Settings {
  logLevel: string
  dashboardRefreshSeconds: number
  historyRetentionDays: number
  theme: 'light' | 'dark' | 'auto'
  externalHostname?: string
  primaryBaseURL?: string
  displayTimezone?: string
  serverTimezone?: string
  [key: string]: unknown
}
