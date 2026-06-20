import { For, Show, createMemo, createSignal } from 'solid-js'
import type { Container, HistoryEntry } from '../api/types'

export type DashLayout = 'cards' | 'table' | 'grid'

export interface ViewProps {
  containers: Container[]
  barIndex: Map<string, HistoryEntry[]>
  exhaustedIDs: Set<string>
  selectedId: string | undefined
  onSelect: (c: Container) => void
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function timeAgo(iso: string) {
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (s < 60)   return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  return `${Math.floor(s / 3600)}h ago`
}

function containerHealth(c: Container): 'ok' | 'warn' | 'danger' | 'muted' {
  const h = c.health?.toLowerCase()
  if (h === 'healthy' || (c.state === 'running' && !h)) return 'ok'
  if (h === 'unhealthy' || c.state === 'exited')         return 'danger'
  if (c.state === 'restarting')                          return 'warn'
  return 'muted'
}

function statusDotColor(c: Container): string {
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
    e.status === 'healthy' || e.status === 'recovered'
  )
  if (meaningful.length === 0) return -1
  const ok = meaningful.filter(e =>
    e.status === 'success' || e.status === 'healthy' || e.status === 'recovered'
  ).length
  return Math.round((ok / meaningful.length) * 100)
}

function miniBarColor(status: string): string {
  if (status === 'success')                             return 'var(--accent)'
  if (status === 'recovered' || status === 'healthy')   return 'var(--ok)'
  if (status === 'failed'    || status === 'exhausted') return 'var(--danger)'
  if (status === 'skipped')                             return 'var(--warn)'
  return 'var(--line)'
}

function sparkHeight(status: string): string {
  if (status === 'success')                             return '100%'
  if (status === 'recovered' || status === 'healthy')   return '70%'
  if (status === 'failed'    || status === 'exhausted') return '40%'
  if (status === 'skipped')                             return '30%'
  return '15%'
}

function sparkColor(status: string): string {
  if (status === 'success' || status === 'recovered' || status === 'healthy') return 'var(--ok)'
  if (status === 'failed'  || status === 'exhausted')                          return 'var(--danger)'
  return 'var(--warn)'
}

// ── UptimeBadge ───────────────────────────────────────────────────────────────

function UptimeBadge(props: { pct: number }) {
  if (props.pct < 0) return <span class="badge-muted text-xs">N/A</span>
  const cls = () => props.pct >= 90 ? 'badge-ok' : props.pct >= 70 ? 'badge-warn' : 'badge-danger'
  return <span class={`${cls()} text-xs`}>{props.pct}%</span>
}

// ── MiniBar (25 heartbeat bars for Cards view) ────────────────────────────────

function MiniBar(props: { entries: HistoryEntry[]; count: number }) {
  const bars = () => {
    const n = props.count
    const last = props.entries.slice(-n)
    const pad: (HistoryEntry | null)[] = Array(Math.max(0, n - last.length)).fill(null)
    return [...pad, ...last]
  }

  return (
    <div class="flex items-stretch gap-px" style={{ height: '22px' }}>
      <For each={bars()}>
        {(entry) => (
          <div
            class="flex-1 rounded-sm"
            style={{ background: entry ? miniBarColor(entry.status ?? '') : 'var(--line)', opacity: entry ? '1' : '0.3' }}
          />
        )}
      </For>
    </div>
  )
}

// ── SparkBars (10 variable-height bars for Grid view) ─────────────────────────

function SparkBars(props: { entries: HistoryEntry[]; count: number }) {
  const bars = () => {
    const n = props.count
    const last = props.entries.slice(-n)
    const pad: (HistoryEntry | null)[] = Array(Math.max(0, n - last.length)).fill(null)
    return [...pad, ...last]
  }

  return (
    <div class="flex items-end gap-px h-5">
      <For each={bars()}>
        {(entry) => (
          <div
            class="flex-1 rounded-sm"
            style={{
              height:     entry ? sparkHeight(entry.status ?? '') : '15%',
              background: entry ? sparkColor(entry.status ?? '')  : 'var(--line)',
              opacity:    entry ? '1' : '0.25',
            }}
          />
        )}
      </For>
    </div>
  )
}

// ── LayoutToggle ──────────────────────────────────────────────────────────────

const LAYOUTS: { key: DashLayout; label: string; icon: string }[] = [
  { key: 'cards', label: 'Cards', icon: '⊟' },
  { key: 'table', label: 'Table', icon: '≡' },
  { key: 'grid',  label: 'Grid',  icon: '⊞' },
]

export function LayoutToggle(props: { layout: DashLayout; onChange: (l: DashLayout) => void }) {
  return (
    <div class="flex rounded-lg overflow-hidden border border-[var(--line)]">
      <For each={LAYOUTS}>
        {(opt) => (
          <button
            class="px-2.5 py-1 text-xs font-semibold transition-colors"
            classList={{
              'bg-[var(--accent)] text-white':                                props.layout === opt.key,
              'bg-[var(--panel)] text-[var(--muted)] hover:bg-[var(--line)]/30': props.layout !== opt.key,
            }}
            onClick={() => props.onChange(opt.key)}
            title={opt.label}
          >
            {opt.icon}
          </button>
        )}
      </For>
    </div>
  )
}

// ── CardsView ─────────────────────────────────────────────────────────────────

export function CardsView(props: ViewProps) {
  // Collapsed card IDs (per-card toggle)
  const [collapsedCards, setCollapsedCards] = createSignal(new Set<string>())
  // Collapsed group names
  const [collapsedGroups, setCollapsedGroups] = createSignal(new Set<string>())

  function toggleCard(id: string, e: MouseEvent) {
    e.stopPropagation()
    setCollapsedCards(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }

  function toggleGroup(name: string) {
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name); else next.add(name)
      return next
    })
  }

  const groups = createMemo<[string, Container[]][]>(() => {
    const map = new Map<string, Container[]>()
    for (const c of props.containers) {
      const g = c.jobGroup || c.labels?.['com.docker.compose.project'] || ''
      const list = map.get(g) ?? []
      list.push(c)
      map.set(g, list)
    }
    const named: [string, Container[]][] = []
    for (const [k, v] of map) if (k) named.push([k, v])
    const ungrouped = map.get('')
    if (ungrouped) named.push(['', ungrouped])
    return named
  })

  const showGroupHeaders = () =>
    groups().length > 1 || (groups().length === 1 && groups()[0][0] !== '')

  return (
    <div class="flex flex-col gap-2 p-2">
      <For each={groups()}>
        {(group) => {
          const groupName       = group[0]
          const groupContainers = group[1]
          const groupCollapsed  = () => collapsedGroups().has(groupName)

          return (
            <>
              <Show when={showGroupHeaders()}>
                <button
                  class="flex items-center gap-2 px-2 pt-3 pb-1.5 w-full text-left group"
                  onClick={() => toggleGroup(groupName)}
                >
                  <span class="text-[11px] font-bold uppercase tracking-widest text-[var(--muted)] flex-1 group-hover:text-[var(--text)] transition-colors">
                    {groupName || 'Ungrouped'}
                    {' '}
                    <span class="font-semibold">({groupContainers.length})</span>
                  </span>
                  <span class="text-[var(--muted)] text-xs">{groupCollapsed() ? '▶' : '▼'}</span>
                </button>
              </Show>

              <Show when={!groupCollapsed()}>
                <For each={groupContainers}>
                  {(c) => {
                    const entries     = () => props.barIndex.get(c.name) ?? []
                    const pct         = () => uptimePct(entries())
                    const borderCol   = () => statusDotColor(c)
                    const cardCollapsed = () => collapsedCards().has(c.id)

                    return (
                      <div
                        class="flex flex-col rounded-lg border border-[var(--line)] cursor-pointer transition-all overflow-hidden"
                        classList={{
                          'ring-2 ring-[var(--accent)]': props.selectedId === c.id,
                          'hover:border-[var(--muted)]/60': props.selectedId !== c.id,
                        }}
                        style={{
                          'border-left': `4px solid ${borderCol()}`,
                          'background': 'var(--bg)',
                        }}
                        onClick={() => props.onSelect(c)}
                      >
                        {/* Name row — always visible */}
                        <div class="flex items-center gap-2 px-3 pt-2.5 pb-2 min-w-0">
                          <span class="text-sm font-bold flex-1 truncate">{c.name}</span>
                          <Show when={props.exhaustedIDs.has(c.id)}>
                            <span class="text-xs text-[var(--danger)] shrink-0">⛔</span>
                          </Show>
                          <Show when={!cardCollapsed()}>
                            <UptimeBadge pct={pct()} />
                          </Show>
                          {/* Collapse toggle */}
                          <button
                            class="shrink-0 w-5 h-5 rounded-full flex items-center justify-center text-xs font-bold text-[var(--muted)] bg-[var(--line)] hover:bg-[var(--muted)]/30 transition-colors leading-none"
                            onClick={[toggleCard, c.id]}
                          >
                            {cardCollapsed() ? '+' : '−'}
                          </button>
                        </div>

                        {/* Expandable body */}
                        <Show when={!cardCollapsed()}>
                          <div class="px-3 pb-2.5 flex flex-col gap-1.5">
                            <MiniBar entries={entries()} count={25} />
                            <div class="flex items-center gap-2 text-xs text-[var(--muted)]">
                              <Show when={c.state !== 'running'}>
                                <span class="text-[var(--danger)] font-semibold">{c.state}</span>
                              </Show>
                              <Show when={c.jobName}>
                                <span class="truncate">{c.jobGroup || c.jobName} · {c.jobAction}</span>
                              </Show>
                            </div>
                          </div>
                        </Show>
                      </div>
                    )
                  }}
                </For>
              </Show>
            </>
          )
        }}
      </For>
    </div>
  )
}

// ── TableView ─────────────────────────────────────────────────────────────────

export function TableView(props: ViewProps) {
  return (
    <div class="overflow-x-auto">
      <table class="w-full text-sm border-collapse">
        <thead>
          <tr class="border-b border-[var(--line)]">
            <th class="w-8 px-3 py-2" />
            <th class="px-3 py-2 text-left text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">Container</th>
            <th class="px-3 py-2 text-left text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">Uptime</th>
            <th class="px-3 py-2 text-left text-xs font-semibold uppercase tracking-wider text-[var(--muted)] whitespace-nowrap">Last Event</th>
            <th class="px-3 py-2 text-left text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">Job</th>
          </tr>
        </thead>
        <tbody>
          <For
            each={props.containers}
            fallback={
              <tr>
                <td colspan="5" class="px-3 py-6 text-center text-sm text-[var(--muted)]">
                  No containers match.
                </td>
              </tr>
            }
          >
            {(c) => {
              const entries   = () => props.barIndex.get(c.name) ?? []
              const pct       = () => uptimePct(entries())
              const lastEntry = () => entries().at(-1)

              return (
                <tr
                  class="border-b border-[var(--line)] last:border-0 cursor-pointer transition-colors"
                  classList={{
                    'bg-[var(--accent-2)]':      props.selectedId === c.id,
                    'hover:bg-[var(--line)]/20': props.selectedId !== c.id,
                  }}
                  onClick={() => props.onSelect(c)}
                >
                  <td class="px-3 py-2.5">
                    <div class="w-2 h-2 rounded-full mx-auto" style={{ background: statusDotColor(c) }} />
                  </td>
                  <td class="px-3 py-2.5 font-semibold whitespace-nowrap">
                    <span>{c.name}</span>
                    <Show when={props.exhaustedIDs.has(c.id)}>
                      <span class="text-[var(--danger)] ml-1 text-xs">⛔</span>
                    </Show>
                    <Show when={c.state !== 'running'}>
                      <span class="ml-2 text-xs text-[var(--danger)]">{c.state}</span>
                    </Show>
                  </td>
                  <td class="px-3 py-2.5">
                    <UptimeBadge pct={pct()} />
                  </td>
                  <td class="px-3 py-2.5 text-xs text-[var(--muted)] whitespace-nowrap">
                    {lastEntry() ? timeAgo(lastEntry()!.timestamp) : '—'}
                  </td>
                  <td class="px-3 py-2.5 text-xs text-[var(--muted)]">
                    <Show when={c.jobName}>
                      <span class="truncate">{c.jobName}<span class="opacity-60"> · {c.jobAction}</span></span>
                    </Show>
                  </td>
                </tr>
              )
            }}
          </For>
        </tbody>
      </table>
    </div>
  )
}

// ── GridView ──────────────────────────────────────────────────────────────────

export function GridView(props: ViewProps) {
  return (
    <div
      class="grid gap-2 p-2"
      style={{ 'grid-template-columns': 'repeat(auto-fill, minmax(120px, 1fr))' }}
    >
      <For each={props.containers}>
        {(c) => {
          const entries = () => props.barIndex.get(c.name) ?? []
          const pct     = () => uptimePct(entries())
          const health  = () => containerHealth(c)
          const accent  = () => statusDotColor(c)

          const tileBg = () => {
            const h = health()
            if (h === 'ok')     return 'color-mix(in srgb, var(--ok)     12%, var(--panel))'
            if (h === 'danger') return 'color-mix(in srgb, var(--danger)  12%, var(--panel))'
            if (h === 'warn')   return 'color-mix(in srgb, var(--warn)    12%, var(--panel))'
            return 'var(--panel)'
          }

          return (
            <div
              class="flex flex-col gap-1.5 p-2.5 rounded-xl cursor-pointer border-2 transition-all hover:scale-[1.02]"
              classList={{ 'outline outline-2 outline-[var(--accent)] outline-offset-1': props.selectedId === c.id }}
              style={{ background: tileBg(), 'border-color': accent() }}
              onClick={() => props.onSelect(c)}
            >
              <div class="text-xs font-semibold leading-tight truncate" title={c.name}>{c.name}</div>

              <div class="text-xl font-extrabold leading-tight tabular-nums" style={{ color: accent() }}>
                {pct() >= 0 ? `${pct()}%` : '—'}
              </div>

              <SparkBars entries={entries()} count={10} />

              <Show when={c.jobAction}>
                <div class="text-[10px] text-[var(--muted)] truncate leading-tight">{c.jobAction}</div>
              </Show>

              <Show when={props.exhaustedIDs.has(c.id)}>
                <span class="text-[10px] text-[var(--danger)] font-semibold leading-tight">⛔ paused</span>
              </Show>
            </div>
          )
        }}
      </For>
    </div>
  )
}
