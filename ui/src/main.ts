import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import './styles/main.css'
import { registerDesktopFlush, restoreDesktopDrafts } from '@/services/desktop-storage'
import { restorePendingRequests } from '@/services/workbench'

async function start() {
  try {
    await restoreDesktopDrafts()
    restorePendingRequests()
    registerDesktopFlush()
    createApp(App).use(router).mount('#app')
  } catch {
    // No editable page was mounted, so a directory switch can safely detach it.
    registerDesktopFlush()
    const root = document.getElementById('app')
    if (root) {
      root.textContent = '无法读取工作台草稿或待核对请求。请重试，原数据已保留。'
      const retry = document.createElement('button')
      retry.type = 'button'
      retry.textContent = '重试'
      retry.addEventListener('click', () => location.reload())
      root.append(retry)
    }
  }
}
void start()
