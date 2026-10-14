/// <reference types="vitest/config" />
import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'

const backend = 'http://127.0.0.1:8080'

export default defineConfig(({ mode }) => ({
  // The design gallery (/dev/design) is compiled in only for dev builds or
  // when VITE_DESIGN_PAGE=1; otherwise its chunk is dropped entirely.
  define: {
    __DESIGN_PAGE__: JSON.stringify(mode !== 'production' || process.env.VITE_DESIGN_PAGE === '1'),
  },
  plugins: [
    tanstackRouter({
      target: 'react',
      autoCodeSplitting: true,
      routesDirectory: 'src/routes',
      generatedRouteTree: 'src/routeTree.gen.ts',
      quoteStyle: 'single',
      semicolons: false,
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: backend,
        // tus answers with an absolute Location built from APP_BASE_URL
        // (the backend, :8080); point it back at the dev server so the
        // browser keeps using the proxy (and its cookies) for PATCH/HEAD.
        configure: (proxy) => {
          proxy.on('proxyRes', (proxyRes, req) => {
            const loc = proxyRes.headers.location
            if (typeof loc === 'string' && loc.startsWith(backend) && req.headers.host) {
              proxyRes.headers.location = loc.replace(backend, `http://${req.headers.host}`)
            }
          })
        },
      },
      '/healthz': backend,
      '/readyz': backend,
    },
  },
  build: {
    target: 'es2022',
    sourcemap: false,
    chunkSizeWarningLimit: 600,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/setupTests.ts'],
    css: false,
    include: ['src/**/*.test.{ts,tsx}'],
  },
}))
