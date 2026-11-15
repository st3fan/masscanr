import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: {
    proxy: {
      '/v1': 'http://127.0.0.1:18080',
      '/healthz': 'http://127.0.0.1:18080',
      '/openapi.json': 'http://127.0.0.1:18080',
      '/docs': 'http://127.0.0.1:18080',
    },
  },
})
