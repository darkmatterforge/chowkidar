import { createQuery, createMutation, useQueryClient } from '@tanstack/solid-query'
import { For } from 'solid-js'
import { notifications } from '../../api/client'

export default function Notifications() {
  const qc = useQueryClient()

  const notifsQ = createQuery(() => ({ queryKey: ['notifications'], queryFn: notifications.list }))

  const deleteMut = createMutation(() => ({
    mutationFn: notifications.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notifications'] }),
  }))

  const testMut = createMutation(() => ({ mutationFn: notifications.test }))

  return (
    <div class="flex flex-col gap-4">
      <div class="flex items-center justify-between">
        <h1 class="text-lg font-bold">Notifications</h1>
        <button class="btn-primary text-sm">+ New Profile</button>
      </div>

      <div class="flex flex-col gap-3">
        <For each={notifsQ.data ?? []} fallback={
          <div class="card p-6 text-center text-sm text-[var(--muted)]">
            {notifsQ.isLoading ? 'Loading…' : 'No notification profiles yet.'}
          </div>
        }>
          {profile => (
            <div class="card p-4 flex flex-col sm:flex-row sm:items-center gap-3">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="font-semibold">{profile.name}</span>
                  <span class="badge-muted">{profile.provider}</span>
                  {profile.enabled
                    ? <span class="badge-ok">enabled</span>
                    : <span class="badge-muted">disabled</span>}
                </div>
              </div>
              <div class="flex gap-2 shrink-0">
                <button
                  class="btn text-xs py-1"
                  onClick={() => testMut.mutate(profile.id)}
                  disabled={testMut.isPending}
                >
                  Test
                </button>
                <button class="btn text-xs py-1">Edit</button>
                <button
                  class="btn-danger text-xs py-1"
                  onClick={() => deleteMut.mutate(profile.id)}
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
