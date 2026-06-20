import { createSignal } from 'solid-js'

type Theme = 'light' | 'dark' | 'auto'

function readStored(): Theme {
  return (localStorage.getItem('chowkidar-theme') as Theme) || 'auto'
}

function applyTheme(t: Theme) {
  if (t === 'auto') {
    document.documentElement.removeAttribute('data-theme')
  } else {
    document.documentElement.setAttribute('data-theme', t)
  }
}

const initial = readStored()
applyTheme(initial)

const [theme, setThemeSignal] = createSignal<Theme>(initial)

function setTheme(t: Theme) {
  localStorage.setItem('chowkidar-theme', t)
  applyTheme(t)
  setThemeSignal(t)
}

function cycleTheme() {
  const order: Theme[] = ['auto', 'light', 'dark']
  const next = order[(order.indexOf(theme()) + 1) % order.length]
  setTheme(next)
}

export { theme, setTheme, cycleTheme }
