import { closeSync, openSync } from 'node:fs'
import { defineConfig, type Plugin } from 'vitest/config'
import react from '@vitejs/plugin-react'

// go:embed refuses an empty dist; emptyOutDir would delete the placeholder.
function keepDistPlaceholder(): Plugin {
  return {
    name: 'keep-dist-placeholder',
    closeBundle() {
      closeSync(openSync('dist/.gitkeep', 'a'))
    },
  }
}

// The Go server's address; `make dev` passes HM_PROXY_TARGET so overridden
// ports reach the proxy too.
const server = process.env.HM_PROXY_TARGET ?? 'http://127.0.0.1:8091'

// The SPA is built into web/dist, which the Go binary embeds (web/embed.go).
export default defineConfig({
  plugins: [react(), keepDistPlaceholder()],
  server: {
    port: 5173,
    proxy: {
      '/api': server,
      '/healthz': server,
      '/ws': { target: server.replace(/^http/, 'ws'), ws: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Source maps are not embedded in the binary; use the dev server to debug.
    sourcemap: false,
    rollupOptions: {
      output: {
        // Libraries change far less often than the app, so a deploy keeps them cached.
        manualChunks(id) {
          if (/node_modules\/(echarts|zrender)\//.test(id)) return 'echarts'
          if (id.includes('node_modules')) return 'vendor'
        },
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
})
