'use strict'

const invoke = window.__TAURI__?.core?.invoke
const byId = id => document.getElementById(id)
const refreshButton = byId('refresh')
const actionButtons = Array.from(document.querySelectorAll('button[data-action]'))
const specOverview = document.querySelector('.spec-overview')
const specStatus = specOverview.querySelector('.spec-status')
const specButtons = specOverview.querySelectorAll('button[data-scope]')
const clockFormat = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
const numberFormat = new Intl.NumberFormat('zh-CN')
let presented = false
let refreshing = false
let refreshPending = false
let opening = false
let refreshTimer

function showConnection(state, title, description) {
  byId('connection').dataset.state = state
  byId('connection-title').textContent = title
  byId('connection-description').textContent = description
}

function validCount(value) {
  return Number.isSafeInteger(value) && value >= 0
}

function showCounts(counts, fields, prefix = '') {
  const available = fields.every(field => validCount(counts?.[field]))
  for (const field of fields) {
    const element = byId(prefix + field)
    element.textContent = available ? numberFormat.format(counts[field]) : '—'
  }
  return available
}

function showSpecOverview(specs) {
  const verification = specs?.verification
  const current = verification === 'current'
  const hasProjection = current || verification === 'stale' || verification === 'failed'
  let hasCounts = false
  for (const button of specButtons) {
    const count = hasProjection ? specs.buckets?.[button.dataset.scope]?.count : undefined
    const available = validCount(count)
    const value = available ? numberFormat.format(count) : '—'
    const countElement = button.querySelector('.spec-count')
    if (countElement.textContent !== value) countElement.textContent = value
    button.disabled = !current || !available || count === 0
    hasCounts ||= available
  }
  specStatus.hidden = current && hasCounts
  if (verification === 'pending') {
    specStatus.textContent = '正在核验…'
  } else if (verification === 'stale' && hasCounts) {
    specStatus.textContent = '上次核验结果'
  } else if (verification === 'failed') {
    specStatus.textContent = hasCounts ? '核验失败 · 上次结果' : '核验失败'
  } else {
    specStatus.textContent = '暂不可用'
  }
}

function showOverview(projects, mergeRequests) {
  const projectCounts = validCount(projects?.total) ? projects : null
  const projectsAvailable = showCounts(projectCounts, ['active', 'inactive'], 'project-')
  byId('project-total').textContent = projectsAvailable ? `登记 ${numberFormat.format(projects.total)}` : '暂不可用'
  const linksAvailable = showCounts(mergeRequests, ['github', 'gitlab'], 'merge-')
  byId('merge-status').textContent = linksAvailable ? '本机已关联' : '暂不可用'
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
  showSpecOverview(connected ? snapshot.specs : null)
  showOverview(connected ? snapshot.projects : null, connected ? snapshot.mergeRequests : null)
  byId('updated').textContent = '更新于 ' + clockFormat.format(new Date())
}

function updateVisibility() {
  const visible = window.__YSS_MENU_BAR_VISIBLE__ === true
  if (visible === presented) return
  presented = visible
  if (presented) {
    if (refreshing) refreshPending = true
    else void refresh()
  } else {
    clearTimeout(refreshTimer)
    refreshPending = false
  }
}

async function refresh(manual = false) {
  if (!presented || refreshing) return
  clearTimeout(refreshTimer)
  refreshing = true
  refreshButton.setAttribute('aria-disabled', 'true')
  specOverview.setAttribute('aria-busy', 'true')
  if (manual) byId('announcement').textContent = ''
  try {
    showSnapshot(await invoke('menubar_snapshot'))
    if (manual) byId('announcement').textContent = '状态已更新'
  } catch {
    showConnection('unavailable', '暂时无法读取状态', '请刷新重试，或打开桌面设置查看连接。')
    byId('service-version').hidden = true
    showSpecOverview(null)
    showOverview(null, null)
    byId('updated').textContent = '更新失败'
    if (manual) byId('announcement').textContent = '状态更新失败'
  } finally {
    specOverview.setAttribute('aria-busy', 'false')
    refreshButton.removeAttribute('aria-disabled')
    refreshing = false
    if (refreshPending) {
      refreshPending = false
      void refresh()
    } else if (presented) {
      refreshTimer = setTimeout(refresh, 3000)
    }
  }
}

async function runAction(action, scope) {
  if (opening && action !== 'dismiss') return
  const error = byId('action-error')
  error.hidden = true
  if (action !== 'dismiss') {
    opening = true
    for (const button of actionButtons) button.setAttribute('aria-disabled', 'true')
  }
  try {
    await invoke('menubar_action', scope ? { action, scope } : { action })
  } catch (failure) {
    if (action === 'dismiss') {
      error.textContent = '无法关闭菜单栏面板，请点按菜单栏图标。'
    } else if (action === 'specOverview' && typeof failure === 'string' && failure.trim()) {
      error.textContent = failure
    } else {
      error.textContent = '无法打开窗口，请稍后重试。'
    }
    error.hidden = false
    if (action === 'specOverview') void refresh()
  } finally {
    if (action !== 'dismiss') {
      opening = false
      for (const button of actionButtons) button.removeAttribute('aria-disabled')
    }
  }
}

refreshButton.addEventListener('click', () => void refresh(true))
for (const button of actionButtons) {
  button.addEventListener('click', () => {
    if (button.disabled || button.getAttribute('aria-disabled') === 'true') return
    void runAction(button.dataset.action, button.dataset.scope)
  })
}
window.addEventListener('keydown', event => {
  if (event.key !== 'Escape') return
  event.preventDefault()
  void runAction('dismiss')
})
window.addEventListener('yss:menu-bar-visibility', updateVisibility)
updateVisibility()
