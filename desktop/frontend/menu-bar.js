'use strict'

const invoke = window.__TAURI__?.core?.invoke
const byId = id => document.getElementById(id)
const refreshButton = byId('refresh')
const actionButtons = Array.from(document.querySelectorAll('button[data-action]'))
const activity = document.querySelector('.activity')
const countFields = ['running', 'attention', 'queued']
const clockFormat = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit' })
const numberFormat = new Intl.NumberFormat('zh-CN')
let refreshing = false
let opening = false
let refreshTimer

function showConnection(state, title, description) {
  byId('connection').dataset.state = state
  byId('connection-title').textContent = title
  byId('connection-description').textContent = description
}

function showActivity(counts, note, keepRows = false) {
  const available = counts && countFields.every(field => Number.isSafeInteger(counts[field]) && counts[field] >= 0)
  for (const field of countFields) {
    const element = byId(field)
    element.textContent = available ? numberFormat.format(counts[field]) : '—'
    element.dataset.present = String(available && counts[field] > 0)
  }
  byId('counts').hidden = !available && !keepRows
  byId('activity-note').hidden = !!available
  byId('activity-note').textContent = note
}

function showSnapshot(snapshot) {
  const connected = snapshot.connection === 'owned' || snapshot.connection === 'external'
  if (connected) {
    showConnection('connected', '服务已连接', snapshot.connection === 'owned' ? '由桌面应用启动' : '连接到外部工作台服务')
  } else {
    showConnection('unavailable', '服务未连接', snapshot.message || '可在桌面设置中重新连接。')
  }
  const version = byId('service-version')
  version.hidden = !connected || !snapshot.serviceVersion
  version.textContent = version.hidden ? '' : '服务 ' + snapshot.serviceVersion
  showActivity(connected ? snapshot.activity : null, connected ? snapshot.message || '暂时无法读取作业。' : '连接服务后显示作业状态。', connected)
  byId('updated').textContent = '更新于 ' + clockFormat.format(new Date())
}

function scheduleRefresh() {
  clearTimeout(refreshTimer)
  if (!document.hidden && document.hasFocus()) refreshTimer = setTimeout(refresh, 15000)
}

async function refresh(manual = false) {
  if (refreshing) return
  refreshing = true
  refreshButton.setAttribute('aria-disabled', 'true')
  activity.setAttribute('aria-busy', 'true')
  if (manual) byId('announcement').textContent = ''
  try {
    showSnapshot(await invoke('menubar_snapshot'))
    if (manual) byId('announcement').textContent = '状态已更新'
  } catch {
    showConnection('unavailable', '暂时无法读取状态', '请刷新重试，或打开桌面设置查看连接。')
    byId('service-version').hidden = true
    showActivity(null, '作业状态暂不可用。')
    byId('updated').textContent = '更新失败'
    if (manual) byId('announcement').textContent = '状态更新失败'
  } finally {
    activity.setAttribute('aria-busy', 'false')
    refreshButton.removeAttribute('aria-disabled')
    refreshing = false
    scheduleRefresh()
  }
}

async function runAction(action) {
  if (opening && action !== 'dismiss') return
  const error = byId('action-error')
  error.hidden = true
  if (action !== 'dismiss') {
    opening = true
    for (const button of actionButtons) button.setAttribute('aria-disabled', 'true')
  }
  try {
    await invoke('menubar_action', { action })
  } catch {
    error.hidden = false
    error.textContent = action === 'dismiss' ? '无法关闭菜单栏面板，请点按菜单栏图标。' : '无法打开窗口，请稍后重试。'
  } finally {
    if (action !== 'dismiss') {
      opening = false
      for (const button of actionButtons) button.removeAttribute('aria-disabled')
    }
  }
}

refreshButton.addEventListener('click', () => void refresh(true))
for (const button of actionButtons) button.addEventListener('click', () => void runAction(button.dataset.action))
window.addEventListener('keydown', event => {
  if (event.key !== 'Escape') return
  event.preventDefault()
  void runAction('dismiss')
})
window.addEventListener('focus', () => void refresh())
window.addEventListener('blur', () => clearTimeout(refreshTimer))
document.addEventListener('visibilitychange', () => {
  if (document.hidden) clearTimeout(refreshTimer)
  else if (document.hasFocus()) void refresh()
})
if (document.hasFocus()) void refresh()
