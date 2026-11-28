import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  build: {
    // The workbench CSP loads fonts as same-origin files.
    assetsInlineLimit: filePath => /\.(?:woff2?|ttf)$/iu.test(filePath) ? false : undefined,
  },
})
