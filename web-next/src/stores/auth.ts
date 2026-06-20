import { createSignal } from 'solid-js'
import type { AuthStatus } from '../api/types'

const [authStatus, setAuthStatus] = createSignal<AuthStatus>({
  enabled: false,
  loggedIn: false,
  username: '',
})

const [authChecked, setAuthChecked] = createSignal(false)

export { authStatus, setAuthStatus, authChecked, setAuthChecked }
