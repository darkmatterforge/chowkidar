import { createQuery, createMutation, useQueryClient } from '@tanstack/solid-query'
import { For, Show } from 'solid-js'
import { dockerHosts } from '../../api/client'

export default function DockerHosts() {
  const qc = useQueryClient()

  const hostsQ  = createQuery(() => ({ queryKey: ['docker-hosts'], queryFn: dockerHosts.list }))
  const statusQ = createQuery(() => ({
    queryKey: ['docker-hosts-status'],
    queryFn: dockerHosts.status,
    refetchInterval: 15_000,
  }))

  const deleteMut = createMutation(() => ({
    mutationFn: dockerHosts.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['docker-hosts'] }),
  }))

  const statusById = () =>
    Object.fromEntries((statusQ.data?.hosts ?? []).map(h => [h.id, h]))

  return (
    <div class="flex flex-col gap-4">
      <div class="flex items-center justify-between">
        <h1 class="text-lg font-bold">Docker Hosts</h1>
        <button class="btn-primary text-sm">+ Add Host</button>
      </div>

      <div class="flex flex-col gap-3">
        <For each={hostsQ.data ?? []} fallback={
          <div class="card p-6 text-center text-sm text-[var(--muted)]">
            {hostsQ.isLoading ? 'Loading…' : 'No Docker hosts configured.'}
          </div>
        }>
          {host => {
            const s = () => statusById()[host.id]
            return (
              <div class="card p-4 flex flex-col sm:flex-row sm:items-center gap-3">
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-2 flex-wrap">
                    <span class="font-semibold">{host.name}</span>
                    <span class="badge-muted">{host.connType}</span>
                    <Show when={s()}>
                      {s()!.connected
                        ? <span class="badge-ok">connected</span>
                        : <span class="badge-danger">offline</span>}
                    </Show>
                    {!host.enabled && <span class="badge-muted">disabled</span>}
                  </div>
                  <Show when={s()?.error}>
                    <p class="text-xs text-[var(--danger)] mt-1">{s()!.error}</p>
                  </Show>
                  <Show when={host.host}>
                    <p class="text-xs text-[var(--muted)] mt-0.5">{host.host}</p>
                  </Show>
                </div>
                <div class="flex gap-2 shrink-0">
                  <button class="btn text-xs py-1">Edit</button>
                  <button
                    class="btn-danger text-xs py-1"
                    onClick={() => deleteMut.mutate(host.id)}
                    disabled={deleteMut.isPending}
                  >
                    Delete
                  </button>
                </div>
              </div>
            )
          }}
        </For>
      </div>
    </div>
  )
}
