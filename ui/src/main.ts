import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import './styles/main.css'
import { registerDesktopFlush, restoreDesktopDrafts } from '@/services/desktop-storage'
import { restoreDesktopPendingRequests } from '@/services/workbench'

async function start() {
  try {
    await restoreDesktopDrafts()
    restoreDesktopPendingRequests()
    registerDesktopFlush()
    createApp(App).use(router).mount('#app')
  } catch {
    // No editable page was mounted, so a directory switch can safely detach it.
    registerDesktopFlush()
    const root = document.getElementById('app')
    if (root) {
      root.textContent = '无法读取桌面草稿，未打开工作台。请重试或选择其他数据目录；原数据已保留。'
      const retry = document.createElement('button')
      retry.type = 'button'
      retry.textContent = '重试'
      retry.addEventListener('click', () => location.reload())
      root.append(retry)
    }
  }
}
void start()
