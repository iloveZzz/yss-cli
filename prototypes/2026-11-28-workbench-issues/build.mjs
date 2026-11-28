import { mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'

// 从仓库根目录执行 pnpm --dir ui install --frozen-lockfile，然后 node 此文件。
const ui = new URL('../../ui/', import.meta.url)
const requireUI = createRequire(new URL('package.json', ui))
const { build } = requireUI('vite')
const vue = requireUI('@vitejs/plugin-vue').default
const tailwind = requireUI('@tailwindcss/vite').default

const [result] = await build({
  configFile: false,
  root: fileURLToPath(ui),
  publicDir: false,
  plugins: [vue(), tailwind()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('src', ui)) },
    dedupe: Object.keys(requireUI('./package.json').dependencies),
  },
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },
  build: {
    write: false,
    lib: {
      entry: fileURLToPath(new URL('./src/main.ts', import.meta.url)),
      name: 'WorkbenchIssues',
      formats: ['iife'],
      fileName: 'issues',
    },
    license: { fileName: 'licenses.txt' },
  },
})

const script = result.output.find(file => file.type === 'chunk').code
const styles = result.output.find(file => file.fileName.endsWith('.css')).source
const licenses = result.output.find(file => file.fileName === 'licenses.txt').source
const credits = await Promise.all(['SHADCN-LICENSE.txt', 'YSS-DESIGN-LICENSE.txt'].map(
  filename => readFile(new URL(`provenance/${filename}`, ui), 'utf8'),
))
const html = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Issues · YSS 工作台</title>
  <style>${styles}</style>
</head>
<body>
  <div id="app"></div>
  <script>${script.replace(/<\/script/gi, '<\\/script')}</script>
  <script type="text/plain" id="licenses">${[licenses, ...credits].join('\n\n').replace(/<\/script/gi, '<\\/script')}</script>
</body>
</html>
`
const destination = new URL('./dist/', import.meta.url)
await mkdir(destination, { recursive: true })
const pending = new URL(`.index-${process.pid}.tmp`, destination)
try {
  await writeFile(pending, html)
  await rename(pending, new URL('index.html', destination))
} finally {
  await rm(pending, { force: true })
}
console.log('已生成 prototypes/2026-11-28-workbench-issues/dist/index.html')
