import { createQuery } from '@tanstack/solid-query'
import { health } from '../../api/client'

export default function Logs() {
  const diagQ = createQuery(() => ({
    queryKey: ['diagnostics'],
    queryFn: health.diagnostics,
    refetchInterval: 10_000,
  }))

  return (
    <div class="flex flex-col gap-4">
      <h1 class="text-lg font-bold">Diagnostics</h1>
      <div class="grid grid-cols-2 sm:grid-cols-3 gap-4">
        {[
          { label: 'Uptime',      value: () => diagQ.data?.uptime      ?? '—' },
          { label: 'Goroutines',  value: () => diagQ.data?.goroutines  ?? '—' },
          { label: 'Memory (MB)', value: () => diagQ.data?.memAllocMB  ?? '—' },
          { label: 'Workers',     value: () => diagQ.data?.workerCount ?? '—' },
          { label: 'Active Jobs', value: () => diagQ.data?.activeJobs  ?? '—' },
        ].map(item => (
          <div class="card p-4 flex flex-col gap-1">
            <span class="text-xs font-semibold uppercase tracking-wider text-[var(--muted)]">{item.label}</span>
            <span class="text-2xl font-bold">{item.value()}</span>
          </div>
        ))}
      </div>
      <p class="text-xs text-[var(--muted)]">Auto-refreshes every 10 seconds.</p>
    </div>
  )
}
