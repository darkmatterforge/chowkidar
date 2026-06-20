import { createQuery, createMutation, useQueryClient } from '@tanstack/solid-query'
import { createSignal, For, Show } from 'solid-js'
import { jobs, notifications } from '../../api/client'

export default function Jobs() {
  const qc = useQueryClient()
  const [page, setPage] = createSignal(0)
  const [search, setSearch] = createSignal('')
  const PAGE_SIZE = 20

  const jobsQ = createQuery(() => ({
    queryKey: ['jobs', page(), search()],
    queryFn: () => jobs.list({ limit: PAGE_SIZE, offset: page() * PAGE_SIZE, q: search() || undefined }),
  }))

  const notifsQ = createQuery(() => ({ queryKey: ['notifications'], queryFn: notifications.list }))

  const deleteMut = createMutation(() => ({
    mutationFn: jobs.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jobs'] }),
  }))

  const profilesById = () => Object.fromEntries((notifsQ.data ?? []).map(p => [p.id, p]))
  const total = () => jobsQ.data?.total ?? 0
  const totalPages = () => Math.ceil(total() / PAGE_SIZE)

  return (
    <div class="flex flex-col gap-4">
      <div class="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
        <h1 class="text-lg font-bold">Jobs</h1>
        <button class="btn-primary text-sm">+ New Job</button>
      </div>

      <input
        class="input max-w-xs"
        placeholder="Search jobs…"
        value={search()}
        onInput={e => { setSearch(e.currentTarget.value); setPage(0) }}
      />

      <div class="card overflow-hidden">
        {/* Mobile: card list */}
        <div class="md:hidden flex flex-col divide-y divide-[var(--line)]">
          <For each={jobsQ.data?.jobs ?? []} fallback={
            <p class="p-4 text-sm text-[var(--muted)]">
              {jobsQ.isLoading ? 'Loading…' : 'No jobs found.'}
            </p>
          }>
            {job => (
              <div class="p-4 flex flex-col gap-2">
                <div class="flex items-start justify-between gap-2">
                  <div>
                    <p class="font-semibold text-sm">{job.name}</p>
                    <Show when={job.group}>
                      <p class="text-xs text-[var(--muted)]">{job.group}</p>
                    </Show>
                  </div>
                  {job.enabled
                    ? <span class="badge-ok shrink-0">enabled</span>
                    : <span class="badge-muted shrink-0">disabled</span>}
                </div>
                <div class="flex items-center gap-1 text-xs text-[var(--muted)]">
                  <span class="badge-muted">{job.action}</span>
                  <Show when={job.script}>
                    <span class="badge-muted">SCRIPT</span>
                  </Show>
                </div>
                <div class="flex gap-2 pt-1">
                  <button class="btn text-xs py-1">Edit</button>
                  <button
                    class="btn-danger text-xs py-1"
                    onClick={() => deleteMut.mutate(job.id)}
                    disabled={deleteMut.isPending}
                  >
                    Delete
                  </button>
                </div>
              </div>
            )}
          </For>
        </div>

        {/* Desktop: table */}
        <div class="hidden md:block overflow-x-auto">
          <table class="w-full text-sm border-collapse">
            <thead>
              <tr class="border-b border-[var(--line)]">
                {['Name','Action','Enabled','Group','Notifications',''].map(h => (
                  <th class="text-left px-4 py-2 text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              <For each={jobsQ.data?.jobs ?? []} fallback={
                <tr><td colspan="6" class="px-4 py-6 text-center text-[var(--muted)]">
                  {jobsQ.isLoading ? 'Loading…' : 'No jobs found.'}
                </td></tr>
              }>
                {job => (
                  <tr class="border-b border-[var(--line)] last:border-0 hover:bg-[var(--accent-2)] transition-colors">
                    <td class="px-4 py-2.5 font-semibold">{job.name}</td>
                    <td class="px-4 py-2.5">
                      <span class="badge-muted">{job.action}</span>
                      <Show when={job.script}>
                        <span class="badge-muted ml-1">SCRIPT</span>
                      </Show>
                    </td>
                    <td class="px-4 py-2.5">
                      {job.enabled
                        ? <span class="badge-ok">enabled</span>
                        : <span class="badge-muted">disabled</span>}
                    </td>
                    <td class="px-4 py-2.5 text-[var(--muted)]">{job.group ?? '—'}</td>
                    <td class="px-4 py-2.5 text-[var(--muted)]">
                      {job.notifications?.length
                        ? job.notifications.map(id => profilesById()[id]?.name ?? id).join(', ')
                        : '—'}
                    </td>
                    <td class="px-4 py-2.5">
                      <div class="flex gap-2 justify-end">
                        <button class="btn text-xs py-1">Edit</button>
                        <button
                          class="btn-danger text-xs py-1"
                          onClick={() => deleteMut.mutate(job.id)}
                          disabled={deleteMut.isPending}
                        >
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </div>

      {/* Pagination */}
      <Show when={totalPages() > 1}>
        <div class="flex items-center gap-2 text-sm">
          <button class="btn py-1" onClick={() => setPage(p => p - 1)} disabled={page() === 0}>← Prev</button>
          <span class="text-[var(--muted)]">Page {page() + 1} of {totalPages()}</span>
          <button class="btn py-1" onClick={() => setPage(p => p + 1)} disabled={page() + 1 >= totalPages()}>Next →</button>
        </div>
      </Show>
    </div>
  )
}
