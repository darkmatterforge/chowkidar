import { createSignal, Show, For, type ParentProps } from 'solid-js'
import { A, useLocation } from '@solidjs/router'
import { createQuery, createMutation, useQueryClient } from '@tanstack/solid-query'
import { authStatus } from '../stores/auth'
import { theme, cycleTheme } from '../stores/theme'
import { auth, health, containerActions } from '../api/client'
import { setAuthStatus } from '../stores/auth'
import SettingsIcon from './icons/SettingsIcon'

const TOP_NAV = [
  { href: '/',         label: 'Dashboard', exact: true  },
  { href: '/settings', label: 'Settings',  exact: false },
]

const NAV = [
  { href: '/',                     label: 'Dashboard',     icon: '⊞',  svgIcon: false },
  { href: '/settings',             label: 'Settings',      icon: null,  svgIcon: true,  exact: true },
  { href: '/settings/jobs',        label: 'Jobs',          icon: '⚙',  svgIcon: false },
  { href: '/settings/notify',      label: 'Notifications', icon: '🔔', svgIcon: false },
  { href: '/settings/hosts',       label: 'Docker Hosts',  icon: '🐳', svgIcon: false },
  { href: '/settings/maintenance', label: 'Maintenance',   icon: '🛠', svgIcon: false },
  { href: '/settings/security',    label: 'Security',      icon: '🔒', svgIcon: false },
  { href: '/settings/logs',        label: 'Logs',          icon: '📋', svgIcon: false },
]

function themeIcon(t: string) {
  if (t === 'light') return '☀'
  if (t === 'dark')  return '☾'
  return '⊙'
}

function BellIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
      <path d="M13.73 21a2 2 0 0 1-3.46 0" />
    </svg>
  )
}

function CheckAllIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor"
      stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M2 12l5 5L17 7" />
      <path d="M9 12l5 5L22 5" />
    </svg>
  )
}

export default function Layout(props: ParentProps) {
  const [drawerOpen, setDrawerOpen] = createSignal(false)
  const [bellOpen,   setBellOpen]   = createSignal(false)
  const [dismissed,  setDismissed]  = createSignal(new Set<string>())
  const [resumeMsg,  setResumeMsg]  = createSignal('')
  const location = useLocation()
  const qc = useQueryClient()

  const healthQ = createQuery(() => ({
    queryKey: ['health'],
    queryFn:  health.get,
    refetchInterval: 30_000,
  }))

  const exhausted      = () => healthQ.data?.retriesExhaustedIDs ?? []
  const exhaustedCount = () => exhausted().length
  const visible        = () => exhausted().filter(id => !dismissed().has(id))
  const visibleCount   = () => visible().length

  const cooldownMut = createMutation(() => ({
    mutationFn: (name: string) => containerActions.resetCooldown(name),
    onSuccess: (data, name) => {
      if (data.reset) {
        dismiss(name)
        setResumeMsg(`Monitoring resumed for ${name}`)
        setTimeout(() => setResumeMsg(''), 3000)
        qc.invalidateQueries({ queryKey: ['health'] })
        qc.invalidateQueries({ queryKey: ['containers'] })
      }
    },
  }))

  function dismiss(id: string) {
    setDismissed(s => new Set([...s, id]))
  }

  function clearAll() {
    setDismissed(new Set(exhausted()))
  }

  function isActive(href: string, exact?: boolean) {
    if (exact) return location.pathname === href
    return location.pathname === href || location.pathname.startsWith(href + '/')
  }

  async function logout() {
    await auth.logout()
    setAuthStatus({ enabled: true, loggedIn: false, username: '' })
  }

  return (
    <div class="flex flex-col min-h-dvh">

      {/* ── Topbar ── */}
      <header class="sticky top-0 z-30 border-b border-[var(--line)] bg-[var(--panel)]/90 backdrop-blur-sm">
        <div class="flex items-center gap-3 px-4 py-3 max-w-screen-2xl mx-auto">

          {/* Hamburger — wrapper div carries md:hidden so .btn's inline-flex doesn't win the cascade */}
          <div class="md:hidden">
            <button class="btn p-2 text-lg leading-none" onClick={() => setDrawerOpen(v => !v)} aria-label="Menu">
              ☰
            </button>
          </div>

          {/* Logo */}
          <A href="/" class="flex-shrink-0">
            <img src="/assets/logo-horizontal-light-transparent.svg" alt="Chowkidar" class="h-8 dark:hidden" />
            <img src="/assets/logo-horizontal-dark-transparent.svg"  alt="Chowkidar" class="h-8 hidden dark:block" />
          </A>

          <div class="flex-1 hidden md:block" />

          {/* Desktop nav */}
          <nav class="hidden md:flex items-center gap-1">
            {TOP_NAV.map(n => (
              <A
                href={n.href}
                class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-semibold border transition-colors"
                classList={{
                  'bg-[var(--accent)] text-white border-[var(--accent)]': isActive(n.href, n.exact),
                  'bg-transparent text-[var(--text)] border-[var(--line)] hover:bg-[var(--line)]/40': !isActive(n.href, n.exact),
                }}
              >
                {n.href === '/settings' && <SettingsIcon size={14} />}
                {n.label}
              </A>
            ))}
          </nav>

          {/* Right controls */}
          <div class="flex items-center gap-2 ml-auto md:ml-2">
            <button class="btn p-2 text-base leading-none" onClick={cycleTheme} title={`Theme: ${theme()}`}>
              {themeIcon(theme())}
            </button>

            {/* Bell */}
            <div class="relative">
              <button
                class="btn p-2 leading-none flex items-center justify-center"
                classList={{ 'text-[var(--danger)] border-[var(--danger)]/40': visibleCount() > 0 }}
                onClick={() => { setBellOpen(v => !v); setResumeMsg('') }}
                title="Alerts"
              >
                <BellIcon />
              </button>
              <Show when={visibleCount() > 0}>
                <span class="absolute -top-1 -right-1 min-w-[18px] h-[18px] rounded-full bg-[var(--danger)] text-white text-[10px] font-bold flex items-center justify-center px-1 pointer-events-none">
                  {visibleCount()}
                </span>
              </Show>

              {/* ── Alert panel ── */}
              <Show when={bellOpen()}>
                <div class="fixed inset-0 z-40" onClick={() => setBellOpen(false)} />

                <div class="absolute right-0 top-full mt-2 z-50 w-[420px] max-w-[calc(100vw-16px)] card overflow-hidden shadow-2xl">

                  {/* Panel header */}
                  <div class="px-4 py-3 border-b border-[var(--line)] flex items-center justify-between">
                    <div class="flex items-center gap-2">
                      <span class="font-bold text-sm">Alerts</span>
                      <Show when={visibleCount() > 0}>
                        <span class="min-w-[20px] h-5 rounded-full bg-[var(--danger)] text-white text-[10px] font-bold flex items-center justify-center px-1.5">
                          {visibleCount()}
                        </span>
                      </Show>
                    </div>
                    <div class="flex items-center gap-3">
                      <Show when={visibleCount() > 0}>
                        <button
                          class="flex items-center gap-1 text-xs text-[var(--muted)] hover:text-[var(--accent)] transition-colors"
                          onClick={clearAll}
                          title="Dismiss all"
                        >
                          <CheckAllIcon />
                          Clear all
                        </button>
                      </Show>
                      <button
                        class="text-[var(--muted)] hover:text-[var(--text)] text-xl leading-none transition-colors"
                        onClick={() => setBellOpen(false)}
                      >×</button>
                    </div>
                  </div>

                  {/* Resume success message */}
                  <Show when={resumeMsg()}>
                    <div class="px-4 py-2 bg-[var(--ok)]/10 border-b border-[var(--ok)]/20 text-xs text-[var(--ok)] font-semibold">
                      ✓ {resumeMsg()}
                    </div>
                  </Show>

                  {/* Alert list */}
                  <div class="max-h-[440px] overflow-y-auto">
                    <Show
                      when={visibleCount() > 0}
                      fallback={
                        <div class="flex flex-col items-center justify-center gap-2 px-4 py-10 text-center">
                          <div class="w-10 h-10 rounded-full bg-[var(--ok)]/10 flex items-center justify-center text-[var(--ok)] text-xl">✓</div>
                          <p class="text-sm font-semibold text-[var(--text)]">All clear</p>
                          <p class="text-xs text-[var(--muted)]">No active monitoring alerts.</p>
                        </div>
                      }
                    >
                      <For each={visible()}>
                        {(id) => (
                          <div class="border-b border-dashed border-[var(--line)] last:border-0">
                            <div class="px-4 py-3.5 flex items-start gap-3">

                              {/* Container icon */}
                              <div class="w-9 h-9 rounded-xl bg-[var(--danger)]/10 border border-[var(--danger)]/20 flex items-center justify-center shrink-0 text-base">
                                ⛔
                              </div>

                              {/* Content */}
                              <div class="flex-1 min-w-0">
                                <div class="flex items-center gap-1.5 flex-wrap">
                                  <span class="font-semibold text-sm text-[var(--text)] truncate">{id}</span>
                                  <span class="badge-danger text-[10px] shrink-0">paused</span>
                                </div>
                                <p class="text-xs text-[var(--muted)] mt-0.5 leading-relaxed">
                                  Retries exhausted — auto-recovery stopped
                                </p>
                                <div class="flex items-center gap-3 mt-2.5">
                                  <button
                                    class="btn text-xs py-1 px-2.5 border-[var(--accent)]/40 text-[var(--accent)] hover:bg-[var(--accent)] hover:text-white transition-colors flex items-center gap-1"
                                    onClick={() => cooldownMut.mutate(id)}
                                    disabled={cooldownMut.isPending}
                                  >
                                    ⟳ Resume
                                  </button>
                                  <button
                                    class="text-xs text-[var(--muted)] hover:text-[var(--danger)] transition-colors"
                                    onClick={() => dismiss(id)}
                                  >
                                    Dismiss
                                  </button>
                                </div>
                              </div>

                              {/* Unread dot + X */}
                              <div class="flex flex-col items-center gap-2 shrink-0 pt-0.5">
                                <div class="w-2 h-2 rounded-full bg-[var(--danger)]" />
                                <button
                                  class="text-[var(--muted)] hover:text-[var(--text)] text-lg leading-none transition-colors"
                                  onClick={() => dismiss(id)}
                                  title="Dismiss"
                                >×</button>
                              </div>
                            </div>
                          </div>
                        )}
                      </For>
                    </Show>
                  </div>

                  {/* Panel footer */}
                  <div class="px-4 py-2.5 border-t border-[var(--line)] flex items-center justify-between">
                    <span class="text-[10px] text-[var(--muted)]">
                      {exhaustedCount()} total · {exhaustedCount() - visibleCount()} dismissed
                    </span>
                    <A href="/" class="text-xs text-[var(--accent)] hover:underline font-semibold" onClick={() => setBellOpen(false)}>
                      Go to Dashboard →
                    </A>
                  </div>
                </div>
              </Show>
            </div>

            <Show when={authStatus().enabled && authStatus().loggedIn}>
              <button class="btn text-sm" onClick={logout}>Sign out</button>
            </Show>
          </div>
        </div>
      </header>

      {/* ── Mobile drawer ── */}
      <Show when={drawerOpen()}>
        <div class="fixed inset-0 z-40 bg-black/40 md:hidden" onClick={() => setDrawerOpen(false)} />
        <aside class="fixed top-0 left-0 z-50 h-full w-64 bg-[var(--panel)] border-r border-[var(--line)] flex flex-col md:hidden shadow-xl">
          <div class="flex items-center justify-between px-4 py-4 border-b border-[var(--line)]">
            <span class="font-bold text-base">Menu</span>
            <button class="btn p-1.5" onClick={() => setDrawerOpen(false)}>✕</button>
          </div>
          <nav class="flex flex-col gap-1 p-3 flex-1 overflow-y-auto">
            {NAV.map(n => (
              <A
                href={n.href}
                class="flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-semibold transition-colors"
                classList={{
                  'bg-[var(--accent-2)] text-[var(--accent)] border border-[#9ec8f1]': isActive(n.href, n.exact),
                  'text-[var(--text)] hover:bg-[var(--line)]/40': !isActive(n.href, n.exact),
                }}
                onClick={() => setDrawerOpen(false)}
              >
                <span class="w-5 flex items-center justify-center shrink-0">
                  {n.svgIcon ? <SettingsIcon size={16} /> : <span class="text-base">{n.icon}</span>}
                </span>
                {n.label}
              </A>
            ))}
          </nav>
          <Show when={authStatus().enabled && authStatus().loggedIn}>
            <div class="p-3 border-t border-[var(--line)]">
              <button class="btn w-full justify-center text-sm" onClick={logout}>
                Sign out ({authStatus().username})
              </button>
            </div>
          </Show>
        </aside>
      </Show>

      {/* ── Page content ── */}
      <main class="flex-1 w-full max-w-screen-2xl mx-auto px-4 py-4">
        {props.children}
      </main>

      {/* ── Mobile bottom nav ── */}
      <nav class="md:hidden fixed bottom-0 inset-x-0 z-30 bg-[var(--panel)]/95 backdrop-blur-sm border-t border-[var(--line)] flex">
        {[NAV[0], NAV[1], NAV[3], NAV[6]].map(n => (
          <A
            href={n.href}
            class="flex-1 flex flex-col items-center gap-0.5 py-2 text-xs font-semibold transition-colors"
            classList={{
              'text-[var(--accent)]': isActive(n.href, n.exact),
              'text-[var(--muted)]': !isActive(n.href, n.exact),
            }}
          >
            <span class="text-lg leading-none">{n.svgIcon ? <SettingsIcon size={18} /> : n.icon}</span>
            {n.label}
          </A>
        ))}
      </nav>

      <div class="h-16 md:hidden" />

      {/* ── Footer — matches old two-strip layout ── */}
      <footer class="hidden md:block shrink-0">
        {/* Accent-tinted strip */}
        <div style={{
          background: 'color-mix(in srgb, var(--accent) 8%, var(--panel))',
          'border-top': '1px solid color-mix(in srgb, var(--accent) 20%, var(--line))',
        }}>
          <div class="max-w-screen-2xl mx-auto px-5 py-2.5 flex items-center justify-center gap-2.5 flex-wrap">
            <span class="text-xs" style={{ color: 'var(--text)', opacity: '0.85' }}>
              🚧 Chowkidar is a work in progress. Found a bug or have an idea?
            </span>
            <a
              href="https://github.com/darkmatterforge/chowkidar/issues"
              target="_blank" rel="noopener noreferrer"
              class="text-xs font-semibold text-[var(--accent)] hover:underline whitespace-nowrap"
            >
              Open an issue on GitHub →
            </a>
          </div>
        </div>
        {/* Attribution strip */}
        <div class="text-center py-2.5 px-4 text-[11px] text-[var(--muted)]">
          A product by{' '}
          <a href="https://github.com/darkmatterforge" target="_blank" rel="noopener noreferrer"
            class="font-bold text-[var(--text)] hover:underline">
            Dark Matter Forge
          </a>
        </div>
      </footer>

    </div>
  )
}
