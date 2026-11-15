import { defineConfig } from 'vitest/config'
import { fileURLToPath, URL } from 'node:url'
export default defineConfig({
  test: { environment: 'jsdom', exclude: ['e2e/**', 'node_modules/**'] },
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
})
