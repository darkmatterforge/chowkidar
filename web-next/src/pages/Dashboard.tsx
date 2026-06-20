import { createQuery, createMutation, useQueryClient } from '@tanstack/solid-query'
import { createSignal, For, Show, createMemo, Switch, Match } from 'solid-js'
import { health, containers, history, dockerHosts, jobs, maintenance, containerActions } from '../api/client'
import type { Container, HistoryEntry } from '../api/types'
import { CardsView, TableView, GridView, uptimePct, type DashLayout } from '../components/ContainerViews'
import RestartIcon from '../components/icons/RestartIcon'
import RefreshIcon from '../components/icons/RefreshIcon'

// ── Helpers ───────────────────────────────────────────────────────────────────

function timeAgo(iso: string) {
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60)  return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  return `${Math.floor(s / 3600)}h ago`
}

function fmtTimestamp(iso: string) {
  return new Date(iso).toLocaleString(undefined, {
    month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

function containerHealth(c: Container): 'ok' | 'warn' | 'danger' | 'muted' {
  const h = c.health?.toLowerCase()
  if (h === 'healthy' || (c.state === 'running' && !h))   return 'ok'
  if (h === 'unhealthy' || c.state === 'exited') return 'danger'
  if (c.state === 'restarting') return 'warn'
  return 'muted'
}

// ── Stat card ─────────────────────────────────────────────────────────────────

type StatColor = 'blue' | 'ok' | 'danger' | 'warn' | 'purple' | 'neutral'

const ACCENT: Record<StatColor, string> = {
  blue:    '#2f8fdd',
  ok:      'var(--ok)',
  danger:  'var(--danger)',
  warn:    'var(--warn)',
  purple:  '#7e6dd1',
  neutral: '#a0b4cc',
}

function StatCard(props: {
  label: string
  value: string | number
  color: StatColor
  sub?: string
  children?: any
}) {
  return (
    <div class="card relative overflow-hidden pt-1">
      {/* colored top bar */}
      <div class="absolute top-0 inset-x-0 h-1 rounded-t-2xl" style={{ background: ACCENT[props.color] }} />
      <div class="p-4 flex flex-col gap-0.5">
        <div class="text-2xl font-extrabold" style={{ color: ACCENT[props.color] }}>
          {props.value}
        </div>
        <div class="text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">
          {props.label}
        </div>
        <Show when={props.sub}>
          <div class="text-xs text-[var(--muted)]">{props.sub}</div>
        </Show>
        <Show when={props.children}>
          <div class="mt-1">{props.children}</div>
        </Show>
      </div>
    </div>
  )
}

// ── Trigger chip ──────────────────────────────────────────────────────────────

function TriggerChip(props: { reason?: string }) {
  const r = () => (props.reason || '').toLowerCase()

  if (r() === 'resume monitoring')
    return <span class="inline-block px-2 py-0.5 rounded-full text-xs font-semibold bg-[#e8f4fd] text-[#1a6fa8] border border-[#b3d9f5]">Resumed</span>
  if (r().startsWith('manual '))
    return <span class="inline-block px-2 py-0.5 rounded-full text-xs font-semibold bg-[#fff3e0] text-[#b05a00] border border-[#ffd08a]">Manual</span>
  if (r() === 'unhealthy')
    return <span class="inline-block px-2 py-0.5 rounded-full text-xs font-semibold bg-[#fdecea] text-[#b00020] border border-[#f5b3b8]">Auto</span>
  if (r() === 'exited')
    return <span class="inline-block px-2 py-0.5 rounded-full text-xs font-semibold bg-[#f3f0fb] text-[#5b3da8] border border-[#c9b9f0]">Exited</span>
  if (r() === 'container recovered')
    return <span class="inline-block px-2 py-0.5 rounded-full text-xs font-semibold bg-[#e7f8ef] text-[#147a47] border border-[#7dd4a8]">Healed</span>
  if (r() === 'stuck-restarting')
    return <span class="inline-block px-2 py-0.5 rounded-full text-xs font-semibold bg-[#fff3e0] text-[#b05a00] border border-[#ffd08a]">Stuck</span>
  if (r().startsWith('crash (exit ')) {
    const code = r().match(/\d+/)?.[0] ?? '?'
    return <><span class="inline-block px-2 py-0.5 rounded-full text-xs font-semibold bg-[#fdecea] text-[#7a0010] border border-[#f5b3b8]">Crash</span> <span class="text-[var(--muted)] text-xs">exit {code}</span></>
  }
  return <span class="text-[var(--muted)] text-xs">{props.reason || '—'}</span>
}

// ── Status chip ───────────────────────────────────────────────────────────────

function StatusChip(props: { status?: string }) {
  const s = props.status || ''
  const [cls, label] = ((): [string, string] => {
    if (s === 'success')   return ['badge-ok', 'OK']
    if (s === 'recovered') return ['badge-ok', 'Healthy']
    if (s === 'failed')    return ['badge-danger', 'FAIL']
    if (s === 'exhausted') return ['badge-danger', 'STOPPED']
    if (s === 'skipped')   return ['badge-warn', 'skipped']
    return ['badge-muted', s || '?']
  })()
  return <span class={cls}>{label}</span>
}

// ── Event history row ─────────────────────────────────────────────────────────

function EventRow(props: { entry: HistoryEntry }) {
  const e = props.entry
  const name = e.containerName || e.containerId || '?'
  const hasOutput = !!e.output

  return (
    <tr class="border-b border-[var(--line)] last:border-0 hover:bg-[var(--accent-2)] transition-colors align-top">
      <td class="px-3 py-2 text-sm font-semibold whitespace-nowrap">{name}</td>
      <td class="px-3 py-2"><StatusChip status={e.status} /></td>
      <td class="px-3 py-2"><TriggerChip reason={e.reason} /></td>
      <td class="px-3 py-2 text-xs text-[var(--muted)] whitespace-nowrap">
        {e.timestamp ? fmtTimestamp(e.timestamp) : '—'}
      </td>
      <td class="px-3 py-2 text-sm max-w-xs">
        <Show when={e.status === 'exhausted'}>
          <span class="text-xs font-semibold text-[#7a0010]">⛔ monitoring paused — max retries reached</span>
        </Show>
        <Show when={e.status !== 'exhausted' && e.error === 'cooldown active'}>
          <span class="text-xs text-[var(--warn)]">✗ cooldown active</span>
        </Show>
        <Show when={e.status !== 'exhausted' && e.error === 'retries exhausted'}>
          <span class="text-xs font-semibold text-[#7a0010]">✗ retries exhausted</span>
        </Show>
        <Show when={e.status !== 'exhausted' && e.error && e.error !== 'cooldown active' && e.error !== 'retries exhausted'}>
          <span>
            {e.action || '—'}
            {' '}
            <span class="text-[var(--danger)] text-xs break-words">✗ {e.error}</span>
          </span>
        </Show>
        <Show when={e.status !== 'exhausted' && !e.error}>
          <span>
            {e.action || '—'}
            {e.attempt && e.attempt > 1
              ? <span class="text-[var(--muted)] text-xs ml-1">·{e.attempt}</span>
              : null}
          </span>
        </Show>
        <Show when={hasOutput}>
          <pre class="mt-1 p-1.5 bg-[var(--bg)] border border-[var(--line)] rounded text-xs font-mono whitespace-pre-wrap break-all max-h-28 overflow-y-auto leading-relaxed">
            {e.output}
          </pre>
        </Show>
      </td>
    </tr>
  )
}

// ── Weekly Availability ───────────────────────────────────────────────────────

function localDateStr(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function dayLabel(dateStr: string): string {
  const [y, m, d] = dateStr.split('-').map(Number)
  const dow = new Date(y, m - 1, d).getDay()
  return ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'][dow]
}

// Separate component so createMemo inside is reactive per container switch.
// For loop items don't re-run their callback when the key stays the same,
// so pct would go stale if computed inline.
function DayRing(props: {
  dateStr: string
  entries: () => HistoryEntry[]
  todayStr: string
}) {
  const R = 20
  const STROKE = 4
  const CIRC = 2 * Math.PI * R

  const isToday  = () => props.dateStr === props.todayStr
  const isFuture = () => props.dateStr > props.todayStr
  const pct      = createMemo(() => uptimePct(props.entries()))
  const hasData  = () => pct() >= 0 && !isFuture()

  const arcColor = () => {
    if (!hasData()) return 'var(--line)'
    if (pct() >= 90) return 'var(--ok)'
    if (pct() >= 70) return 'var(--warn)'
    return 'var(--danger)'
  }

  const dashLen = () => hasData() ? (pct() / 100) * CIRC : 0

  return (
    <div
      class="flex flex-col items-center gap-1 flex-1 min-w-0"
      style={{ opacity: isFuture() ? '0.45' : '1' }}
      title={isFuture() ? 'No data yet — upcoming day' : !hasData() ? 'No data recorded' : `${pct()}% uptime on ${props.dateStr}`}
    >
      <span
        class="text-[10px] font-bold tracking-wide"
        style={{ color: isToday() ? 'var(--accent)' : 'var(--muted)' }}
      >
        {dayLabel(props.dateStr)}
      </span>

      <svg width="48" height="48" viewBox="0 0 48 48" style={{ overflow: 'visible' }}>
        {/* Track — solid grey for all days (past no-data and future alike) */}
        <circle
          cx="24" cy="24" r={R}
          fill="none"
          stroke="var(--line)"
          stroke-width={STROKE}
        />
        {/* Progress arc — only for days that have data */}
        <Show when={hasData()}>
          <circle
            cx="24" cy="24" r={R}
            fill="none"
            stroke={arcColor()}
            stroke-width={STROKE}
            stroke-linecap="round"
            stroke-dasharray={`${dashLen()} ${CIRC}`}
            transform="rotate(-90 24 24)"
          />
        </Show>
        <Show when={isToday() && hasData()}>
          <circle cx="24" cy="24" r={R - STROKE / 2 - 1} fill={arcColor()} opacity="0.12" />
        </Show>
        <text
          x="24" y="24"
          text-anchor="middle"
          dominant-baseline="central"
          font-size={pct() === 100 ? '9' : '10'}
          font-weight="700"
          font-family="inherit"
          fill={hasData() ? arcColor() : 'var(--muted)'}
        >
          {!hasData() ? '–' : `${pct()}%`}
        </text>
      </svg>

      <Show when={isToday()}>
        <div class="w-1 h-1 rounded-full" style={{ background: 'var(--accent)' }} />
      </Show>
    </div>
  )
}

function WeeklyAvailability(props: { entries: HistoryEntry[] }) {
  const todayStr = localDateStr(new Date())

  const byDate = createMemo(() => {
    const map = new Map<string, HistoryEntry[]>()
    for (const e of props.entries) {
      if (!e.timestamp) continue
      const key = localDateStr(new Date(e.timestamp))
      const list = map.get(key) ?? []
      list.push(e)
      map.set(key, list)
    }
    return map
  })

  // Always show 7 days starting from firstDataDate (or today-6 if data is older).
  // Future days are shown as greyed-out "no data" circles so the widget is always full.
  const weekDays = createMemo(() => {
    const dates = [...byDate().keys()].sort()
    const firstDataDate = dates[0]

    const sixDaysAgo = new Date()
    sixDaysAgo.setDate(sixDaysAgo.getDate() - 6)
    const sixDaysAgoStr = localDateStr(sixDaysAgo)

    const windowStart = (firstDataDate && firstDataDate > sixDaysAgoStr)
      ? firstDataDate
      : sixDaysAgoStr

    return Array.from({ length: 7 }, (_, i) => {
      const d = new Date(windowStart + 'T00:00:00')
      d.setDate(d.getDate() + i)
      return localDateStr(d)
    })
  })

  return (
    <div class="px-4 pt-3 pb-4 border-b border-[var(--line)]">
      <div class="text-[10px] font-bold uppercase tracking-wider text-[var(--muted)] mb-2">
        Availability — last 7 days
      </div>
      <div class="flex items-center gap-1">
        <For each={weekDays()}>
          {(dateStr) => (
            <DayRing
              dateStr={dateStr}
              entries={() => byDate().get(dateStr) ?? []}
              todayStr={todayStr}
            />
          )}
        </For>
      </div>
    </div>
  )
}

// ── Main Dashboard ────────────────────────────────────────────────────────────

export default function Dashboard() {
  const qc = useQueryClient()
  const [selectedContainer, setSelectedContainer] = createSignal<Container | null>(null)
  const [search, setSearch] = createSignal('')
  const [statusFilter, setStatusFilter] = createSignal('')
  const [hostFilter, setHostFilter] = createSignal('')
  const [actionFilter, setActionFilter] = createSignal('')
  const [tagFilter, setTagFilter] = createSignal('')
  const [showAdvanced, setShowAdvanced] = createSignal(false)
  const [actionMsg, setActionMsg] = createSignal('')
  const [histPage, setHistPage] = createSignal(1)
  const [histPageSize, setHistPageSize] = createSignal(25)
  // Mobile: toggle between list and detail panel
  const [mobilePanel, setMobilePanel] = createSignal<'list' | 'detail'>('list')
  // Container list layout — set in Settings > General, read from localStorage
  const [layout] = createSignal<DashLayout>(
    (localStorage.getItem('chowkidar-dash-layout') as DashLayout | null) ?? 'cards'
  )

  const healthQ = createQuery(() => ({
    queryKey: ['health'],
    queryFn: health.get,
    refetchInterval: 30_000,
  }))

  const containersQ = createQuery(() => ({
    queryKey: ['containers'],
    queryFn: containers.list,
    refetchInterval: 30_000,
  }))

  const hostsStatusQ = createQuery(() => ({
    queryKey: ['docker-hosts-status'],
    queryFn: dockerHosts.status,
    refetchInterval: 15_000,
  }))

  const jobsQ = createQuery(() => ({
    queryKey: ['jobs-count'],
    queryFn: () => jobs.list({ enabled: 'true', limit: 1 }),
    refetchInterval: 60_000,
  }))

  const maintQ = createQuery(() => ({
    queryKey: ['maintenance-active'],
    queryFn: maintenance.active,
    refetchInterval: 30_000,
  }))

  const historyQ = createQuery(() => ({
    queryKey: ['history', histPage(), histPageSize(), selectedContainer()?.name],
    queryFn: () => history.list({
      limit: histPageSize(),
      offset: (histPage() - 1) * histPageSize(),
    }),
    refetchInterval: 30_000,
  }))

  const barsQ = createQuery(() => ({
    queryKey: ['history-bars'],
    queryFn: () => history.list({ bars: true, limit: 2000 }),
    refetchInterval: 60_000,
    staleTime: 60_000,
  }))

  const actionMut = createMutation(() => ({
    mutationFn: ({ container, name, action }: { container: string; name: string; action: string }) =>
      containerActions.run(container, name, action),
    onSuccess: (data) => {
      if (data.queued) {
        setActionMsg('Action queued — refreshing in 3s…')
        setTimeout(() => {
          setActionMsg('')
          qc.invalidateQueries({ queryKey: ['containers'] })
          qc.invalidateQueries({ queryKey: ['history'] })
        }, 3000)
      } else {
        setActionMsg(`Action failed: ${data.error ?? 'unknown'}`)
      }
    },
    onError: (e: any) => setActionMsg(`Error: ${e.message}`),
  }))

  const cooldownMut = createMutation(() => ({
    mutationFn: (name: string) => containerActions.resetCooldown(name),
    onSuccess: (data) => {
      if (data.reset) {
        setActionMsg('Monitoring resumed — will retry on next scan.')
        setTimeout(() => {
          setActionMsg('')
          qc.invalidateQueries({ queryKey: ['containers'] })
        }, 2500)
      } else {
        setActionMsg(`Reset failed: ${data.error ?? 'unknown'}`)
      }
    },
  }))

  function doAction(action: string) {
    const c = selectedContainer()
    if (!c) return
    actionMut.mutate({ container: c.id, name: c.name, action })
  }

  // Derived stats
  const allC         = () => containersQ.data?.all ?? []
  const unhealthyC   = () => containersQ.data?.unhealthy ?? []
  const exhaustedIDs = () => new Set(healthQ.data?.retriesExhaustedIDs ?? [])

  const monitored = () => allC().filter(c => c.checkEligible).length
  const healthy   = () => allC().filter(c =>
    c.checkEligible && containerHealth(c) === 'ok').length
  const down      = () => unhealthyC().length
  const issues    = () => allC().filter(c =>
    c.checkEligible && containerHealth(c) !== 'ok' && c.state !== 'exited').length

  // Filtered + grouped container list
  const filteredContainers = createMemo(() => {
    const s = search().toLowerCase()
    return allC().filter(c => {
      if (statusFilter() === 'up'       && c.state !== 'running')  return false
      if (statusFilter() === 'down'     && containerHealth(c) !== 'danger') return false
      if (statusFilter() === 'shutdown' && c.state !== 'exited')   return false
      if (hostFilter()   && c.hostName !== hostFilter())            return false
      if (actionFilter() && c.jobAction !== actionFilter())         return false
      if (tagFilter()) {
        const labels = c.labels ?? {}
        if (!Object.values(labels).some(v => v === tagFilter()))    return false
      }
      if (s && !c.name.toLowerCase().includes(s) &&
          !(c.jobName?.toLowerCase().includes(s)) &&
          !(c.hostName?.toLowerCase().includes(s)))                 return false
      return true
    })
  })

  const hostNames = createMemo(() =>
    [...new Set(allC().map(c => c.hostName).filter(Boolean))] as string[]
  )

  const actionNames = createMemo(() =>
    [...new Set(allC().map(c => c.jobAction).filter(Boolean))] as string[]
  )

  const tagValues = createMemo(() => {
    const vals = new Set<string>()
    for (const c of allC()) {
      for (const v of Object.values(c.labels ?? {})) {
        if (v && v.length < 40) vals.add(v)
      }
    }
    return [...vals].slice(0, 30)
  })

  const hasActiveFilter = () =>
    !!statusFilter() || !!hostFilter() || !!actionFilter() || !!tagFilter() || !!search()

  function clearFilters() {
    setSearch('')
    setStatusFilter('')
    setHostFilter('')
    setActionFilter('')
    setTagFilter('')
  }

  // Index bar entries by container name for O(1) lookup in view components
  const barIndex = createMemo((): Map<string, HistoryEntry[]> => {
    const entries = barsQ.data?.entries ?? []
    const map = new Map<string, HistoryEntry[]>()
    for (const e of entries) {
      const key = e.containerName || e.containerId || ''
      if (!key) continue
      const list = map.get(key) ?? []
      list.push(e)
      map.set(key, list)
    }
    return map
  })

  // Docker hosts summary for stat card
  const hostsData = () => hostsStatusQ.data?.hosts ?? []
  const hostsConnected = () => hostsData().filter(h => h.enabled !== false && h.connected).length
  const hostsEnabled   = () => hostsData().filter(h => h.enabled !== false).length
  const hostsColor = (): StatColor =>
    hostsEnabled() === 0 ? 'neutral'
    : hostsConnected() === hostsEnabled() ? 'ok'
    : hostsConnected() === 0 ? 'danger' : 'warn'

  // History pagination
  const histTotal = () => historyQ.data?.total ?? 0
  const histPages = () => Math.max(1, Math.ceil(histTotal() / histPageSize()))

  // Build page number list with ellipsis: [1, 2, 3, ..., N] or [1, ..., 5, 6, 7, ..., N]
  function buildPageList(current: number, total: number): (number | '...')[] {
    if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1)
    const pages: (number | '...')[] = []
    const addPage = (n: number) => { if (!pages.includes(n)) pages.push(n) }
    addPage(1)
    if (current > 3) pages.push('...')
    for (let i = Math.max(2, current - 1); i <= Math.min(total - 1, current + 1); i++) addPage(i)
    if (current < total - 2) pages.push('...')
    addPage(total)
    return pages
  }

  function refreshAll() {
    qc.invalidateQueries({ queryKey: ['health'] })
    qc.invalidateQueries({ queryKey: ['containers'] })
    qc.invalidateQueries({ queryKey: ['history'] })
    qc.invalidateQueries({ queryKey: ['docker-hosts-status'] })
    qc.invalidateQueries({ queryKey: ['maintenance-active'] })
  }


  return (
    <div class="flex flex-col gap-3 pb-20 md:pb-2">

      {/* ── Stats bar ─────────────────────────────────────────────────────── */}
      <div class="grid grid-cols-3 sm:grid-cols-4 lg:grid-cols-7 gap-3">
        <StatCard label="Monitored"    value={monitored()}          color="blue"    />
        <StatCard label="Healthy"      value={healthy()}            color="ok"      />
        <StatCard label="Down"         value={down()}               color="danger"  />
        <Show when={healthQ.data && !healthQ.data.startExited}>
          <StatCard
            label="Shutdown"
            value={healthQ.data!.totalExitedCount ?? healthQ.data!.exitedCount ?? 0}
            color="neutral"
            sub="exit 0"
          />
        </Show>
        <StatCard label="Issues"       value={issues()}             color="warn"    />
        <StatCard label="Active Jobs"  value={jobsQ.data?.total ?? '–'} color="purple" />
        <StatCard
          label="Docker Hosts"
          value={hostsEnabled() <= 1
            ? (hostsConnected() > 0 ? '1' : '0')
            : `${hostsConnected()}/${hostsEnabled()}`}
          color={hostsColor()}
        >
          <Show when={hostsData().length > 1 && hostsData().length <= 5}>
            <div class="flex flex-col gap-0.5 mt-1">
              <For each={hostsData()}>
                {h => {
                  const disabled = h.enabled === false
                  const color = disabled ? 'var(--muted)' : h.connected ? 'var(--ok)' : 'var(--danger)'
                  return (
                    <div class="flex items-center gap-1.5 text-xs" style={{ opacity: disabled ? '0.5' : '1' }}>
                      <div class="w-1.5 h-1.5 rounded-full shrink-0" style={{ background: color }} />
                      <span class="truncate flex-1">{h.name}</span>
                      <span style={{ color }} class="shrink-0 text-[10px]">
                        {disabled ? 'Disabled' : h.connected ? 'Connected' : 'Offline'}
                      </span>
                    </div>
                  )
                }}
              </For>
            </div>
          </Show>
        </StatCard>
      </div>

      {/* ── System banners ────────────────────────────────────────────────── */}
      <Show when={healthQ.data?.encryptionKeyMismatch}>
        <div class="flex flex-col gap-1.5 px-4 py-3 bg-[#fdecea] border border-[#f5b3b8] rounded-xl text-sm text-[#7a0010]">
          <div class="flex items-center gap-2 font-semibold">
            <span>🔑</span>
            <span>Encryption key mismatch — some secrets could not be decrypted</span>
          </div>
          <div class="text-xs text-[#5a0008] leading-relaxed">
            <strong>CHOWKIDAR_SECRET_KEY</strong> appears to have changed since secrets were last saved.
            Affected notification agents have been cleared. Restore the original key or re-enter secrets in Notifications settings.
          </div>
        </div>
      </Show>

      <Show when={(maintQ.data?.windows?.length ?? 0) > 0}>
        <div class="px-4 py-3 bg-[#eef2ff] border border-[#c7d2fe] rounded-xl text-sm text-[#363a8a]">
          <strong>🛠 Maintenance active:</strong>{' '}
          {maintQ.data!.windows.map(w => w.name).join(', ')} — monitoring paused
        </div>
      </Show>

      {/* ── Toolbar ───────────────────────────────────────────────────────── */}
      <div class="flex items-center gap-2">
        <div class="flex-1" />
        <Show when={healthQ.data?.lastScanValid && healthQ.data?.lastScan}>
          <span class="text-xs text-[var(--muted)]">Last scan: {timeAgo(healthQ.data!.lastScan!)}</span>
        </Show>
        <button class="btn text-sm py-1.5 flex items-center gap-1.5" onClick={refreshAll}>
          <RefreshIcon size={14} />
          Refresh
        </button>
      </div>

      {/* ── Mobile panel toggle ───────────────────────────────────────────── */}
      <div class="md:hidden flex rounded-xl overflow-hidden border border-[var(--line)]">
        <button
          class="flex-1 py-2 text-sm font-semibold transition-colors"
          classList={{
            'bg-[var(--accent)] text-white': mobilePanel() === 'list',
            'bg-[var(--panel)] text-[var(--muted)]': mobilePanel() !== 'list',
          }}
          onClick={() => setMobilePanel('list')}
        >
          Containers
        </button>
        <button
          class="flex-1 py-2 text-sm font-semibold transition-colors"
          classList={{
            'bg-[var(--accent)] text-white': mobilePanel() === 'detail',
            'bg-[var(--panel)] text-[var(--muted)]': mobilePanel() !== 'detail',
          }}
          onClick={() => setMobilePanel('detail')}
        >
          Events
        </button>
      </div>

      {/* ── Main two-column layout ────────────────────────────────────────── */}
      <div class="flex flex-col md:flex-row gap-4" style={{ height: 'calc(100dvh - 210px)', 'min-height': '540px' }}>

        {/* ── Left: Container list ────────────────────────────────────────── */}
        <div
          class="card flex flex-col overflow-hidden w-full md:w-96 lg:w-[420px] shrink-0 h-full"
          classList={{ 'hidden md:flex': mobilePanel() === 'detail' }}
        >
          {/* Search */}
          <div class="px-3 pt-3 pb-2">
            <input
              class="input text-sm"
              placeholder="Search services, labels, jobs…"
              value={search()}
              onInput={e => setSearch(e.currentTarget.value)}
            />
          </div>

          {/* Primary filter row */}
          <div class="flex flex-wrap gap-1.5 px-3 pb-2">
            <select
              class="text-xs font-semibold px-2.5 py-1.5 rounded-full border border-[var(--line)] bg-[var(--panel)] text-[var(--text)] cursor-pointer appearance-none pr-5 relative"
              style={{ 'background-image': 'url("data:image/svg+xml,%3Csvg xmlns=\'http://www.w3.org/2000/svg\' width=\'10\' height=\'6\'%3E%3Cpath d=\'M0 0l5 6 5-6z\' fill=\'%23888\'/%3E%3C/svg%3E")', 'background-repeat': 'no-repeat', 'background-position': 'right 8px center' }}
              value={statusFilter()}
              onChange={e => setStatusFilter(e.currentTarget.value)}
            >
              <option value="">All Status</option>
              <option value="up">Up</option>
              <option value="down">Down</option>
              <option value="shutdown">Shutdown</option>
            </select>

            <select
              class="text-xs font-semibold px-2.5 py-1.5 rounded-full border border-[var(--line)] bg-[var(--panel)] text-[var(--text)] cursor-pointer appearance-none pr-5"
              style={{ 'background-image': 'url("data:image/svg+xml,%3Csvg xmlns=\'http://www.w3.org/2000/svg\' width=\'10\' height=\'6\'%3E%3Cpath d=\'M0 0l5 6 5-6z\' fill=\'%23888\'/%3E%3C/svg%3E")', 'background-repeat': 'no-repeat', 'background-position': 'right 8px center' }}
              value={tagFilter()}
              onChange={e => setTagFilter(e.currentTarget.value)}
            >
              <option value="">Tags</option>
              <For each={tagValues()}>{t => <option value={t}>{t}</option>}</For>
            </select>

            <select
              class="text-xs font-semibold px-2.5 py-1.5 rounded-full border border-[var(--line)] bg-[var(--panel)] text-[var(--text)] cursor-pointer appearance-none pr-5"
              style={{ 'background-image': 'url("data:image/svg+xml,%3Csvg xmlns=\'http://www.w3.org/2000/svg\' width=\'10\' height=\'6\'%3E%3Cpath d=\'M0 0l5 6 5-6z\' fill=\'%23888\'/%3E%3C/svg%3E")', 'background-repeat': 'no-repeat', 'background-position': 'right 8px center' }}
              value={actionFilter()}
              onChange={e => setActionFilter(e.currentTarget.value)}
            >
              <option value="">All Actions</option>
              <For each={actionNames()}>{a => <option value={a}>{a}</option>}</For>
            </select>

            <Show when={hostNames().length > 1}>
              <select
                class="text-xs font-semibold px-2.5 py-1.5 rounded-full border border-[var(--line)] bg-[var(--panel)] text-[var(--text)] cursor-pointer appearance-none pr-5"
                style={{ 'background-image': 'url("data:image/svg+xml,%3Csvg xmlns=\'http://www.w3.org/2000/svg\' width=\'10\' height=\'6\'%3E%3Cpath d=\'M0 0l5 6 5-6z\' fill=\'%23888\'/%3E%3C/svg%3E")', 'background-repeat': 'no-repeat', 'background-position': 'right 8px center' }}
                value={hostFilter()}
                onChange={e => setHostFilter(e.currentTarget.value)}
              >
                <option value="">All Hosts</option>
                <For each={hostNames()}>{h => <option value={h}>{h}</option>}</For>
              </select>
            </Show>
          </div>

          {/* Advanced + Clear row */}
          <div class="flex items-center gap-2 px-3 pb-2 border-b border-[var(--line)]">
            <button
              class="text-xs font-semibold px-2.5 py-1.5 rounded-full border border-[var(--line)] bg-[var(--panel)] text-[var(--text)] flex items-center gap-1"
              onClick={() => setShowAdvanced(v => !v)}
            >
              Advanced {showAdvanced() ? '▲' : '▼'}
            </button>
            <button
              class="text-xs font-semibold px-2.5 py-1.5 rounded-full border border-[var(--line)] bg-[var(--panel)] text-[var(--text)]"
              classList={{ 'text-[var(--accent)]': hasActiveFilter() }}
              onClick={clearFilters}
            >
              Clear
            </button>
            <span class="text-xs text-[var(--muted)] ml-auto">{filteredContainers().length} services</span>
          </div>

          <Show when={showAdvanced()}>
            <div class="px-3 py-2 border-b border-[var(--line)] text-xs text-[var(--muted)]">
              Advanced filters coming soon.
            </div>
          </Show>

          {/* Container list — three view modes */}
          <div class="flex-1 overflow-y-auto min-h-0">
            <Show
              when={!containersQ.isLoading}
              fallback={<p class="p-4 text-sm text-[var(--muted)]">Loading…</p>}
            >
              <Show
                when={filteredContainers().length > 0}
                fallback={<p class="p-4 text-sm text-[var(--muted)]">No containers match.</p>}
              >
                <Switch>
                  <Match when={layout() === 'cards'}>
                    <CardsView
                      containers={filteredContainers()}
                      barIndex={barIndex()}
                      exhaustedIDs={exhaustedIDs()}
                      selectedId={selectedContainer()?.id}
                      onSelect={c => { setSelectedContainer(c); setHistPage(1); setMobilePanel('detail') }}
                    />
                  </Match>
                  <Match when={layout() === 'table'}>
                    <TableView
                      containers={filteredContainers()}
                      barIndex={barIndex()}
                      exhaustedIDs={exhaustedIDs()}
                      selectedId={selectedContainer()?.id}
                      onSelect={c => { setSelectedContainer(c); setHistPage(1); setMobilePanel('detail') }}
                    />
                  </Match>
                  <Match when={layout() === 'grid'}>
                    <GridView
                      containers={filteredContainers()}
                      barIndex={barIndex()}
                      exhaustedIDs={exhaustedIDs()}
                      selectedId={selectedContainer()?.id}
                      onSelect={c => { setSelectedContainer(c); setHistPage(1); setMobilePanel('detail') }}
                    />
                  </Match>
                </Switch>
              </Show>
            </Show>
          </div>
        </div>

        {/* ── Right: Detail + Event feed ───────────────────────────────────── */}
        <div
          class="card flex flex-col overflow-hidden flex-1 min-w-0 w-full h-full"
          classList={{ 'hidden md:flex': mobilePanel() === 'list' }}
        >
          {/* Container detail header */}
          <Show when={selectedContainer()} fallback={
            <div class="px-5 py-3 text-sm text-[var(--muted)]">
              Select a service on the left to view its heartbeat, stats, and actions.
            </div>
          }>
            {c => (
              <div class="p-4 border-b border-[var(--line)]">
                <div class="flex flex-col sm:flex-row sm:items-start gap-3">
                  <div class="flex-1 min-w-0">
                    <div class="flex items-center gap-2 flex-wrap">
                      <span class="font-bold text-base">{c().name}</span>
                      <Show when={c().state === 'running' && (!c().health || c().health === 'healthy')}>
                        <span class="badge-ok">running</span>
                      </Show>
                      <Show when={c().health === 'unhealthy'}>
                        <span class="badge-danger">unhealthy</span>
                      </Show>
                      <Show when={c().state === 'exited'}>
                        <span class="badge-danger">exited</span>
                      </Show>
                      <Show when={c().state === 'restarting'}>
                        <span class="badge-warn">restarting</span>
                      </Show>
                      <Show when={exhaustedIDs().has(c().id)}>
                        <span class="badge-danger">⛔ monitoring paused</span>
                      </Show>
                      <Show when={c().cooldownActive}>
                        <span class="badge-warn">cooldown</span>
                      </Show>
                    </div>
                    <div class="text-xs text-[var(--muted)] mt-0.5 truncate">{c().image}</div>
                    <Show when={c().jobName}>
                      <div class="text-xs text-[var(--muted)]">
                        Job: <span class="font-semibold text-[var(--text)]">{c().jobName}</span>
                        {' · '}{c().jobAction}
                      </div>
                    </Show>
                    <Show when={c().hostName && c().hostName !== 'local'}>
                      <div class="text-xs text-[var(--muted)]">Host: {c().hostName}</div>
                    </Show>
                    <Show when={c().restartCount > 0}>
                      <div class="text-xs text-[var(--muted)]">Restarts: {c().restartCount}</div>
                    </Show>
                  </div>

                  {/* Action buttons */}
                  <div class="flex gap-2 flex-wrap shrink-0">
                    <button
                      class="btn-primary text-sm py-1.5 px-3 flex items-center gap-1.5"
                      onClick={() => doAction('restart')}
                      disabled={actionMut.isPending}
                    >
                      <RestartIcon size={14} />
                      Restart
                    </button>
                    <button
                      class="btn text-sm py-1.5 px-3"
                      onClick={() => doAction('start')}
                      disabled={actionMut.isPending}
                    >▶ Start</button>
                    <button
                      class="btn-danger text-sm py-1.5 px-3"
                      onClick={() => doAction('stop')}
                      disabled={actionMut.isPending}
                    >■ Stop</button>
                    <button
                      class="btn text-sm py-1.5 px-3"
                      onClick={() => cooldownMut.mutate(c().name)}
                      disabled={cooldownMut.isPending}
                      title="Clear cooldown so auto-monitoring retries this container on the next scan"
                    >⟳ Resume</button>
                  </div>
                </div>

                <Show when={actionMsg()}>
                  <div class="mt-2 text-sm text-[var(--accent)]">{actionMsg()}</div>
                </Show>
              </div>
            )}
          </Show>

          {/* Weekly availability — shown whenever a container is selected */}
          <Show when={selectedContainer()}>
            {c => (
              <WeeklyAvailability
                entries={barIndex().get(c().name) ?? []}
              />
            )}
          </Show>

          {/* Event history header — All Services Feed */}
          <div class="flex items-center gap-3 px-4 py-2 border-b border-[var(--line)] flex-wrap">
            <span class="text-xs font-bold uppercase tracking-wider text-[var(--text)]">
              All Services Feed
            </span>
            <Show
              when={selectedContainer()}
              fallback={
                <span class="px-2.5 py-0.5 rounded-full text-[11px] font-bold bg-[var(--warn)] text-white">
                  scope: all
                </span>
              }
            >
              {c => (
                <span class="px-2.5 py-0.5 rounded-full text-[11px] font-bold bg-[var(--warn)] text-white">
                  scope: {c().name}
                </span>
              )}
            </Show>
            <Show when={historyQ.isFetching}>
              <span class="text-[10px] text-[var(--muted)]">↻ refreshing…</span>
            </Show>
            <div class="ml-auto flex items-center gap-1.5">
              <span class="text-[10px] text-[var(--muted)]">per page:</span>
              <select
                class="text-[11px] bg-[var(--bg)] border border-[var(--line)] rounded px-1 py-0.5 text-[var(--text)] cursor-pointer"
                value={histPageSize()}
                onChange={e => { setHistPageSize(Number(e.currentTarget.value)); setHistPage(1) }}
              >
                <option value={10}>10</option>
                <option value={25}>25</option>
                <option value={50}>50</option>
                <option value={100}>100</option>
              </select>
            </div>
          </div>

          <div class="flex-1 overflow-auto min-h-0">
            <table class="w-full text-sm border-collapse min-w-[540px]">
              <thead>
                <tr class="border-b border-[var(--line)]">
                  {['Name','Status','Trigger','Date / Time','Action'].map(h => (
                    <th class="text-left px-3 py-2 text-xs font-semibold uppercase tracking-wider text-[var(--muted)] whitespace-nowrap">{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                <Show
                  when={(historyQ.data?.entries?.length ?? 0) > 0}
                  fallback={
                    <tr><td colspan="5" class="px-3 py-8 text-center text-sm text-[var(--muted)]">
                      {historyQ.isLoading ? 'Loading…' : 'No events recorded yet.'}
                    </td></tr>
                  }
                >
                  <For each={historyQ.data!.entries}>
                    {e => <EventRow entry={e} />}
                  </For>
                </Show>
              </tbody>
            </table>
          </div>

          {/* History pagination */}
          <Show when={histPages() >= 1 && histTotal() > 0}>
            <div class="flex items-center gap-1 px-3 py-2.5 border-t border-[var(--line)] flex-wrap">
              <button
                class="btn py-1 px-2 text-xs"
                onClick={() => setHistPage(p => p - 1)}
                disabled={histPage() === 1}
              >← Prev</button>

              <For each={buildPageList(histPage(), histPages())}>
                {(item) => (
                  <Show
                    when={item !== '...'}
                    fallback={<span class="text-[var(--muted)] text-xs px-1">…</span>}
                  >
                    <button
                      class="btn py-1 px-2.5 text-xs min-w-[32px]"
                      classList={{
                        'bg-[var(--accent)] text-white border-[var(--accent)]': item === histPage(),
                      }}
                      onClick={() => setHistPage(item as number)}
                    >
                      {item}
                    </button>
                  </Show>
                )}
              </For>

              <button
                class="btn py-1 px-2 text-xs"
                onClick={() => setHistPage(p => p + 1)}
                disabled={histPage() >= histPages()}
              >Next →</button>

              <span class="text-[10px] text-[var(--muted)] ml-2">
                {(histPage() - 1) * histPageSize() + 1}–{Math.min(histPage() * histPageSize(), histTotal())} of {histTotal()}
              </span>
            </div>
          </Show>
        </div>
      </div>
    </div>
  )
}
