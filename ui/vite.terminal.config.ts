import { fileURLToPath } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// Compile the pnpm-patched xterm sources for the terminal entry.
// These aliases, legacy decorators and class-field semantics match xterm's own build.
const xtermSource = new URL('./node_modules/@xterm/xterm/src/', import.meta.url)

export default defineConfig({
  root: fileURLToPath(new URL('./terminal', import.meta.url)),
  tsconfig: 'tsconfig.build.json',
  base: './',
  plugins: [vue()],
  resolve: {
    alias: [
      { find: /^@xterm\/xterm$/, replacement: fileURLToPath(new URL('browser/public/Terminal.ts', xtermSource)) },
      ...['browser', 'common', 'vs'].map((prefix) => ({
        find: prefix,
        replacement: fileURLToPath(new URL(prefix, xtermSource)),
      })),
    ],
  },
  oxc: {
    decorator: { legacy: true },
    assumptions: { setPublicClassFields: true },
    typescript: { removeClassFieldsWithoutInitializer: true },
  },
  build: {
    target: ['safari15.6', 'chrome107'],
    outDir: fileURLToPath(new URL('../desktop/target/terminal-web', import.meta.url)),
    emptyOutDir: true,
    modulePreload: { polyfill: false },
    rolldownOptions: {
      input: fileURLToPath(new URL('./terminal/terminal.html', import.meta.url)),
      output: {
        entryFileNames: 'assets/terminal-[hash].js',
        chunkFileNames: 'assets/terminal-[hash].js',
        assetFileNames: 'assets/terminal-[hash][extname]',
      },
    },
  },
})
