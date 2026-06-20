import { createSignal } from 'solid-js'
import { auth } from '../api/client'
import { setAuthStatus, setAuthChecked } from '../stores/auth'

export default function Login() {
  const [username, setUsername] = createSignal('')
  const [password, setPassword] = createSignal('')
  const [remember, setRemember] = createSignal(false)
  const [error, setError] = createSignal('')
  const [loading, setLoading] = createSignal(false)

  async function submit(e: Event) {
    e.preventDefault()
    if (!username() || !password()) { setError('Username and password required'); return }
    setLoading(true)
    setError('')
    try {
      await auth.login(username(), password(), remember())
      setAuthStatus({ enabled: true, loggedIn: true, username: username() })
      setAuthChecked(true)
    } catch (err: any) {
      setError(err.message || 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div class="min-h-dvh flex items-center justify-center px-4 bg-[var(--bg)]">
      <div class="card w-full max-w-sm p-8">
        <div class="flex justify-center mb-6">
          <img src="/assets/logo-stacked-light.svg" alt="Chowkidar" class="h-14 dark:hidden" />
          <img src="/assets/logo-stacked-dark.svg"  alt="Chowkidar" class="h-14 hidden dark:block" />
        </div>

        <form onSubmit={submit} class="flex flex-col gap-4">
          <div class="flex flex-col gap-1">
            <label class="text-sm font-semibold">Username</label>
            <input
              class="input"
              type="text"
              autocomplete="username"
              value={username()}
              onInput={e => setUsername(e.currentTarget.value)}
            />
          </div>
          <div class="flex flex-col gap-1">
            <label class="text-sm font-semibold">Password</label>
            <input
              class="input"
              type="password"
              autocomplete="current-password"
              value={password()}
              onInput={e => setPassword(e.currentTarget.value)}
            />
          </div>
          <label class="flex items-center gap-2 text-sm cursor-pointer">
            <input
              type="checkbox"
              checked={remember()}
              onChange={e => setRemember(e.currentTarget.checked)}
            />
            Remember me
          </label>
          {error() && (
            <p class="text-sm text-[var(--danger)] bg-[var(--error-bg)] rounded-lg px-3 py-2">
              {error()}
            </p>
          )}
          <button class="btn-primary w-full justify-center py-2.5" type="submit" disabled={loading()}>
            {loading() ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
      </div>
    </div>
  )
}
