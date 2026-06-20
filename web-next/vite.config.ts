import { defineConfig } from 'vite'
import solid from 'vite-plugin-solid'
import UnoCSS from 'unocss/vite'
import { presetUno, presetIcons } from 'unocss'
import transformerDirectives from '@unocss/transformer-directives'

export default defineConfig({
  plugins: [
    UnoCSS({
      presets: [
        presetUno(),
        presetIcons({ scale: 1.2 }),
      ],
      transformers: [transformerDirectives()],
      theme: {
        colors: {
          bg:       'var(--bg)',
          panel:    'var(--panel)',
          line:     'var(--line)',
          text:     'var(--text)',
          muted:    'var(--muted)',
          ok:       'var(--ok)',
          warn:     'var(--warn)',
          danger:   'var(--danger)',
          accent:   'var(--accent)',
          accent2:  'var(--accent-2)',
        },
        breakpoints: {
          sm: '480px',
          md: '768px',
          lg: '1024px',
          xl: '1280px',
        },
      },
    }),
    solid(),
  ],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    host: '0.0.0.0', // needed so the container port is reachable from the host
    proxy: {
      '/api': process.env.API_TARGET ?? 'http://localhost:8080',
    },
  },
})
