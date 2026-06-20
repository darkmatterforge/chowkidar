import { Show, onMount } from 'solid-js'
import { Router, Route, type RouteSectionProps } from '@solidjs/router'
import { QueryClient, QueryClientProvider } from '@tanstack/solid-query'
import Layout from './components/Layout'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import SettingsLayout from './pages/settings/SettingsLayout'
import General from './pages/settings/General'
import Jobs from './pages/settings/Jobs'
import Notifications from './pages/settings/Notifications'
import DockerHosts from './pages/settings/DockerHosts'
import Maintenance from './pages/settings/Maintenance'
import Security from './pages/settings/Security'
import Logs from './pages/settings/Logs'
import { auth } from './api/client'
import { authStatus, setAuthStatus, authChecked, setAuthChecked } from './stores/auth'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 10_000, retry: 1 },
  },
})

// Root component receives router context — safe to use <A> and useLocation here.
// Handles auth gate and wraps all routes in the shared Layout.
function AppRoot(props: RouteSectionProps) {
  onMount(async () => {
    try {
      const status = await auth.status()
      setAuthStatus(status)
    } catch {
      setAuthStatus({ enabled: false, loggedIn: false, username: '' })
    } finally {
      setAuthChecked(true)
    }
  })

  return (
    <Show
      when={authChecked()}
      fallback={
        <div class="min-h-dvh flex items-center justify-center text-[var(--muted)]">
          Loading…
        </div>
      }
    >
      <Show when={!authStatus().enabled || authStatus().loggedIn} fallback={<Login />}>
        <Layout>{props.children}</Layout>
      </Show>
    </Show>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Router root={AppRoot}>
        <Route path="/" component={Dashboard} />
        <Route path="/settings" component={SettingsLayout}>
          <Route path=""             component={General}       />
          <Route path="/jobs"        component={Jobs}          />
          <Route path="/notify"      component={Notifications} />
          <Route path="/hosts"       component={DockerHosts}   />
          <Route path="/maintenance" component={Maintenance}   />
          <Route path="/security"    component={Security}      />
          <Route path="/logs"        component={Logs}          />
        </Route>
      </Router>
    </QueryClientProvider>
  )
}
