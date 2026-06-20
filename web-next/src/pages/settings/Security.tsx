import { createSignal, Show } from 'solid-js'
import { auth } from '../../api/client'
import { authStatus, setAuthStatus } from '../../stores/auth'

export default function Security() {
  const [current,  setCurrent]  = createSignal('')
  const [next,     setNext]     = createSignal('')
  const [confirm,  setConfirm]  = createSignal('')
  const [msg,      setMsg]      = createSignal('')
  const [isErr,    setIsErr]    = createSignal(false)
  const [loading,  setLoading]  = createSignal(false)

  function flash(text: string, err = false) {
    setMsg(text); setIsErr(err)
    setTimeout(() => setMsg(''), 4000)
  }

  async function changePassword(e: Event) {
    e.preventDefault()
    if (next() !== confirm()) { flash('Passwords do not match', true); return }
    setLoading(true)
    try {
      await auth.changePassword(current(), next())
      flash('Password changed successfully')
      setCurrent(''); setNext(''); setConfirm('')
    } catch (err: any) {
      flash(err.message || 'Failed to change password', true)
    } finally { setLoading(false) }
  }

  async function setup(e: Event) {
    e.preventDefault()
    if (next() !== confirm()) { flash('Passwords do not match', true); return }
    setLoading(true)
    // setup reuses change-password endpoint — server creates auth on first call
    try {
      await auth.changePassword('', next())
      setAuthStatus(s => ({ ...s, enabled: true }))
      flash('Authentication enabled')
      setNext(''); setConfirm('')
    } catch (err: any) {
      flash(err.message || 'Failed to enable authentication', true)
    } finally { setLoading(false) }
  }

  return (
    <div class="flex flex-col gap-6">
      <h1 class="text-lg font-bold">Security</h1>

      <Show when={msg()}>
        <div class={`px-4 py-3 rounded-xl text-sm border ${isErr()
          ? 'bg-[var(--error-bg)] border-[var(--danger)] text-[var(--danger)]'
          : 'bg-[color-mix(in_srgb,var(--ok)_10%,transparent)] border-[var(--ok)] text-[var(--ok)]'}`}>
          {msg()}
        </div>
      </Show>

      <Show when={!authStatus().enabled}>
        <div class="card p-6 flex flex-col gap-4">
          <p class="text-sm text-[var(--muted)]">Authentication is currently disabled. Set a password to enable it.</p>
          <form onSubmit={setup} class="flex flex-col gap-3">
            <div class="flex flex-col gap-1">
              <label class="text-sm font-semibold">Username</label>
              <input class="input" value="admin" disabled />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-sm font-semibold">New Password</label>
              <input class="input" type="password" value={next()} onInput={e => setNext(e.currentTarget.value)} />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-sm font-semibold">Confirm Password</label>
              <input class="input" type="password" value={confirm()} onInput={e => setConfirm(e.currentTarget.value)} />
            </div>
            <button class="btn-primary w-fit" type="submit" disabled={loading()}>
              {loading() ? 'Enabling…' : 'Enable Authentication'}
            </button>
          </form>
        </div>
      </Show>

      <Show when={authStatus().enabled}>
        <div class="card p-6 flex flex-col gap-4">
          <div class="flex items-center gap-2">
            <span class="badge-ok">Enabled</span>
            <span class="text-sm text-[var(--muted)]">Logged in as <strong>{authStatus().username}</strong></span>
          </div>
          <form onSubmit={changePassword} class="flex flex-col gap-3">
            <div class="flex flex-col gap-1">
              <label class="text-sm font-semibold">Current Password</label>
              <input class="input" type="password" value={current()} onInput={e => setCurrent(e.currentTarget.value)} />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-sm font-semibold">New Password</label>
              <input class="input" type="password" value={next()} onInput={e => setNext(e.currentTarget.value)} />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-sm font-semibold">Confirm Password</label>
              <input class="input" type="password" value={confirm()} onInput={e => setConfirm(e.currentTarget.value)} />
            </div>
            <button class="btn-primary w-fit" type="submit" disabled={loading()}>
              {loading() ? 'Saving…' : 'Change Password'}
            </button>
          </form>
        </div>
      </Show>
    </div>
  )
}
