import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

const backend = process.env.DF17_BACKEND ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      // ws + rewriteWsOrigin: /api/ws upgrades through the proxy and passes the backend's same-origin check.
      '/api': { target: backend, changeOrigin: true, ws: true, rewriteWsOrigin: true },
      '/healthz': { target: backend, changeOrigin: true },
    },
  },
  build: {
    // dist/app is gitignored; web/dist/index.html is the committed placeholder for builds without Node.
    outDir: 'dist/app',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
  },
})
