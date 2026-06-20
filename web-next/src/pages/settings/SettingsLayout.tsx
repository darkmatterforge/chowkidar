import { type RouteSectionProps } from '@solidjs/router'
import { A, useLocation } from '@solidjs/router'

const TABS = [
  { href: '/settings',             label: 'General',      end: true },
  { href: '/settings/jobs',        label: 'Jobs'          },
  { href: '/settings/notify',      label: 'Notifications' },
  { href: '/settings/hosts',       label: 'Docker Hosts'  },
  { href: '/settings/maintenance', label: 'Maintenance'   },
  { href: '/settings/security',    label: 'Security'      },
  { href: '/settings/logs',        label: 'Logs'          },
]

export default function SettingsLayout(props: RouteSectionProps) {
  const location = useLocation()

  function isActive(href: string, end?: boolean) {
    if (end) return location.pathname === href
    return location.pathname === href || location.pathname.startsWith(href + '/')
  }

  return (
    <div class="flex flex-col md:flex-row gap-6">
      {/* Scrollable tab strip on mobile, vertical sidebar on desktop */}
      <aside class="md:w-48 shrink-0">
        <nav class="flex md:flex-col gap-1 overflow-x-auto pb-1 md:pb-0 md:overflow-x-visible">
          {TABS.map(t => (
            <A
              href={t.href}
              class="shrink-0 px-3 py-2 rounded-xl text-sm font-semibold transition-colors whitespace-nowrap"
              classList={{
                'bg-[var(--accent-2)] text-[var(--accent)] border border-[#9ec8f1]': isActive(t.href, t.end),
                'text-[var(--text)] hover:bg-[var(--line)]/40': !isActive(t.href, t.end),
              }}
            >
              {t.label}
            </A>
          ))}
        </nav>
      </aside>

      <div class="flex-1 min-w-0">
        {props.children}
      </div>
    </div>
  )
}
