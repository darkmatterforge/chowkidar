import { createQuery, createMutation, useQueryClient } from '@tanstack/solid-query'
import { For, Show } from 'solid-js'
import { maintenance } from '../../api/client'

export default function Maintenance() {
  const qc = useQueryClient()

  const windowsQ = createQuery(() => ({ queryKey: ['maintenance'], queryFn: maintenance.list }))
  const activeQ  = createQuery(() => ({
    queryKey: ['maintenance-active'],
    queryFn: maintenance.active,
    refetchInterval: 30_000,
  }))

  const deleteMut = createMutation(() => ({
    mutationFn: maintenance.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['maintenance'] }),
  }))

  const activeWindows = () => activeQ.data?.windows ?? []
  const activeIds = () => new Set(activeWindows().map(w => w.id))

  return (
    <div class="flex flex-col gap-4">
      <div class="flex items-center justify-between">
        <h1 class="text-lg font-bold">Maintenance Windows</h1>
        <button class="btn-primary text-sm">+ New Window</button>
      </div>

      <Show when={activeWindows().length > 0}>
        <div class="flex items-center gap-2 px-4 py-3 rounded-xl bg-[var(--warn-bg)] border border-[var(--warn)] text-sm">
          <span class="text-[var(--warn)] font-bold">⚠</span>
          <span>{activeWindows().length} maintenance window{activeWindows().length > 1 ? 's' : ''} active — monitoring paused</span>
        </div>
      </Show>

      <div class="flex flex-col gap-3">
        <For each={windowsQ.data ?? []} fallback={
          <div class="card p-6 text-center text-sm text-[var(--muted)]">
            {windowsQ.isLoading ? 'Loading…' : 'No maintenance windows configured.'}
          </div>
        }>
          {w => (
            <div class="card p-4 flex flex-col sm:flex-row sm:items-start gap-3">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="font-semibold">{w.name}</span>
                  <span class="badge-muted">{w.scheduleType}</span>
                  <Show when={activeIds().has(w.id)}>
                    <span class="badge-warn">active</span>
                  </Show>
                  <Show when={w.active && !activeIds().has(w.id)}>
                    <span class="badge-ok">armed</span>
                  </Show>
                </div>
                <Show when={w.description}>
                  <p class="text-xs text-[var(--muted)] mt-0.5">{w.description}</p>
                </Show>
                <Show when={w.cronExpr}>
                  <p class="text-xs font-mono text-[var(--muted)] mt-0.5">{w.cronExpr} · {w.duration}s</p>
                </Show>
              </div>
              <div class="flex gap-2 shrink-0">
                <button class="btn text-xs py-1">Edit</button>
                <button
                  class="btn-danger text-xs py-1"
                  onClick={() => deleteMut.mutate(w.id)}
                  disabled={deleteMut.isPending}
                >
                  Delete
                </button>
              </div>
            </div>
          )}
        </For>
      </div>
    </div>
  )
}
