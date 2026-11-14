import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import manifest from './app.json'

export default defineConfig({
  base: './',
  plugins: [vue(), {
    name: 'app-metadata',
    transformIndexHtml: html => ({
      html: html.replace('<title>应用</title>', `<title>${manifest.name['zh-CN'].replace(/[&<>"']/g, ch => `&#${ch.charCodeAt(0)};`)}</title>`),
      tags: [{ tag: 'meta', attrs: { name: 'tos-app-version', content: manifest.version }, injectTo: 'head' }],
    }),
  }],
  server: {
    port: 5173,
    proxy: { [`/v2/proxy/${manifest.id}/api`]: 'http://127.0.0.1:18947' },
  },
  build: { target: 'es2022', sourcemap: false, assetsInlineLimit: 0 },
})
