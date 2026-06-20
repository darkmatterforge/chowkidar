import { createQuery, createMutation, useQueryClient } from '@tanstack/solid-query'
import { createSignal, Show, For } from 'solid-js'
import { settings, health } from '../../api/client'
import type { DashLayout } from '../../components/ContainerViews'

const LAYOUT_OPTIONS: { key: DashLayout; label: string; desc: string; preview: string }[] = [
  {
    key: 'cards',
    label: 'Cards',
    desc: 'Heartbeat bars + uptime per container',
    preview: [
      '● nginx    95%',
      '▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆',
      '● redis    100%',
      '▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆▆',
    ].join('\n'),
  },
  {
    key: 'table',
    label: 'Table',
    desc: 'Dense rows with uptime and last event',
    preview: [
      '● nginx    95%   2m ago   restart',
      '● redis   100%   5m ago   —',
      '● db       78%  12m ago   restart',
    ].join('\n'),
  },
  {
    key: 'grid',
    label: 'Grid',
    desc: 'Colour-coded tiles, ideal for TV dashboards',
    preview: [
      '┌───────┐ ┌───────┐ ┌───────┐',
      '│ nginx │ │ redis │ │  db   │',
      '│  95%  │ │ 100%  │ │  78%  │',
      '└───────┘ └───────┘ └───────┘',
    ].join('\n'),
  },
]

export default function General() {
  const qc = useQueryClient()
  const settingsQ = createQuery(() => ({ queryKey: ['settings'], queryFn: settings.get }))
  const healthQ   = createQuery(() => ({ queryKey: ['health'],   queryFn: health.get   }))

  const [saved, setSaved] = createSignal(false)
  const [dashLayout, setDashLayout] = createSignal<DashLayout>(
    (localStorage.getItem('chowkidar-dash-layout') as DashLayout | null) ?? 'cards'
  )
  function pickLayout(v: DashLayout) {
    localStorage.setItem('chowkidar-dash-layout', v)
    setDashLayout(v)
  }

  const updateMut = createMutation(() => ({
    mutationFn: settings.update,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['settings'] })
      setSaved(true)
      setTimeout(() => setSaved(false), 3000)
    },
  }))

  function submit(e: Event) {
    e.preventDefault()
    const form = e.currentTarget as HTMLFormElement
    const data = Object.fromEntries(new FormData(form))
    updateMut.mutate({
      logLevel: String(data.logLevel),
      dashboardRefreshSeconds: Number(data.dashboardRefreshSeconds),
      historyRetentionDays: Number(data.historyRetentionDays),
    })
  }

  return (
    <div class="card p-6 flex flex-col gap-6">
      <div class="flex items-center justify-between">
        <h1 class="text-lg font-bold">General Settings</h1>
        <Show when={healthQ.data?.version}>
          <span class="text-sm text-[var(--muted)]">v{healthQ.data!.version}</span>
        </Show>
      </div>

      <Show when={!settingsQ.isLoading} fallback={<p class="text-[var(--muted)] text-sm">Loading…</p>}>
        <form onSubmit={submit} class="flex flex-col gap-4">
          <div class="flex flex-col gap-1">
            <label class="text-sm font-semibold">Log Level</label>
            <select name="logLevel" class="input" value={settingsQ.data?.logLevel ?? 'info'}>
              {['debug','info','warn','error'].map(v => <option value={v}>{v}</option>)}
            </select>
          </div>
          <div class="flex flex-col gap-1">
            <label class="text-sm font-semibold">Dashboard Refresh (seconds)</label>
            <input name="dashboardRefreshSeconds" type="number" min="0" class="input"
              value={settingsQ.data?.dashboardRefreshSeconds ?? 30} />
            <span class="text-xs text-[var(--muted)]">0 disables auto-refresh</span>
          </div>
          <div class="flex flex-col gap-1">
            <label class="text-sm font-semibold">History Retention (days)</label>
            <input name="historyRetentionDays" type="number" min="1" class="input"
              value={settingsQ.data?.historyRetentionDays ?? 90} />
          </div>

          <div class="flex items-center gap-3 pt-2">
            <button class="btn-primary" type="submit" disabled={updateMut.isPending}>
              {updateMut.isPending ? 'Saving…' : 'Save'}
            </button>
            <Show when={saved()}>
              <span class="text-sm text-[var(--ok)]">Saved ✓</span>
            </Show>
            <Show when={updateMut.isError}>
              <span class="text-sm text-[var(--danger)]">{(updateMut.error as any)?.message}</span>
            </Show>
          </div>
        </form>
      </Show>

      {/* ── Container List Layout ───────────────────────────────────────────── */}
      <div class="flex flex-col gap-3 pt-2 border-t border-[var(--line)]">
        <div>
          <div class="text-sm font-semibold">Container List Layout</div>
          <div class="text-xs text-[var(--muted)] mt-0.5">How containers are displayed on the dashboard.</div>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <For each={LAYOUT_OPTIONS}>
            {(opt) => {
              const active = () => dashLayout() === opt.key
              return (
                <button
                  type="button"
                  class="flex flex-col gap-2 p-4 rounded-xl border-2 text-left transition-all"
                  classList={{
                    'border-[var(--accent)] bg-[var(--accent-2)]': active(),
                    'border-[var(--line)] hover:border-[var(--accent)]/50 bg-[var(--bg)]': !active(),
                  }}
                  onClick={() => pickLayout(opt.key)}
                >
                  <div class="flex items-center justify-between">
                    <span class="text-sm font-semibold">{opt.label}</span>
                    <Show when={active()}>
                      <span class="text-xs font-semibold text-[var(--accent)]">Active</span>
                    </Show>
                  </div>
                  <pre class="text-[10px] leading-relaxed text-[var(--muted)] font-mono whitespace-pre overflow-hidden">
                    {opt.preview}
                  </pre>
                  <span class="text-xs text-[var(--muted)]">{opt.desc}</span>
                </button>
              )
            }}
          </For>
        </div>
      </div>
    </div>
  )
}
