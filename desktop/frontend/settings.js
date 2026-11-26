'use strict'
const invoke = window.__TAURI__.core.invoke
const byId = id => document.getElementById(id)
const tablist = document.querySelector('[role="tablist"]')
const tabs = Array.from(tablist.querySelectorAll('[role="tab"]'))
let busy = false
let settingsShown = true
let editingAutostart = false
let editingNotifications = false
let latestState = null

function setText(element, value) {
  const text = String(value)
  if (element.textContent !== text) element.textContent = text
}
function showMessage(id, message) {
  const element = byId(id)
  setText(element, message)
  element.hidden = !message
}
function selectTab(tab) {
  tablist.style.setProperty('--selected-tab', String(tabs.indexOf(tab)))
  for (const item of tabs) {
    const selected = item === tab
    item.setAttribute('aria-selected', String(selected))
    item.tabIndex = selected ? 0 : -1
    byId(item.getAttribute('aria-controls')).hidden = !selected
  }
  tab.focus({ preventScroll: true })
}
for (const tab of tabs) {
  tab.addEventListener('click', () => selectTab(tab))
  tab.addEventListener('keydown', event => {
    const index = tabs.indexOf(tab)
    let next
    if (event.key === 'ArrowRight') next = (index + 1) % tabs.length
    else if (event.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length
    else if (event.key === 'Home') next = 0
    else if (event.key === 'End') next = tabs.length - 1
    else return
    event.preventDefault()
    selectTab(tabs[next])
  })
}
function selectUpdateSettings() {
  if (!window.__yssShowUpdateSettings) return false
  delete window.__yssShowUpdateSettings
  selectTab(byId('tab-update'))
  return true
}
window.addEventListener('yss:settings-update', selectUpdateSettings)
function updateControls() {
  for (const control of document.querySelectorAll('.button, input[type="checkbox"]')) control.disabled = busy
  const back = byId('open')
  back.hidden = !latestState?.connected
  back.disabled = busy || back.hidden
  byId('stop-exit').hidden = latestState?.connectionKind !== 'owned'
  byId('pause-queues').disabled = busy || !latestState?.connected
  byId('install-update').disabled = busy || !latestState?.updateAvailable || latestState?.updateChecking
  byId('check-updates').disabled = busy || !!latestState?.updateChecking
  document.querySelector('main').setAttribute('aria-busy', String(busy))
}
async function refresh() {
  const state = await invoke('desktop_status')
  latestState = state
  byId('platform-notice').hidden = state.platform !== 'windows'
  setText(byId('status'), state.message)
  setText(byId('desktop-version'), state.desktopVersion)
  setText(byId('service-version'), state.serviceVersion || '—')
  setText(byId('connection-kind'), state.connectionKind === 'owned' ? '由桌面应用启动' : state.connectionKind === 'external' ? '已连接外部服务' : '—')
  setText(byId('exit-description'), state.connectionKind === 'owned'
    ? '关闭窗口后应用继续在后台运行。退出时会检查作业并关闭由桌面启动的服务。'
    : state.connectionKind === 'external'
      ? '关闭窗口后应用继续在后台运行。退出桌面应用会保留外部服务及其作业。'
      : '关闭窗口后应用继续在后台运行。')
  setText(byId('directory'), state.dataDir || '—')
  setText(byId('update-status'), state.updateMessage)
  const badge = byId('connection-badge')
  badge.dataset.connected = String(!!state.connected)
  setText(badge, state.connected ? '已连接' : '未连接')
  if (!editingAutostart) byId('autostart').checked = state.autostart
  if (!editingNotifications) byId('notifications').checked = state.notifications
  updateControls()
}
async function run(action, successMessage = '') {
  if (busy) return
  busy = true
  updateControls()
  showMessage('error', '')
  showMessage('notice', '')
  try {
    const result = await action()
    if (successMessage) showMessage('notice', typeof successMessage === 'function' ? successMessage(result) : successMessage)
  } catch (error) {
    showMessage('error', String(error))
  }
  try { await refresh() } catch (error) { showMessage('error', String(error)) }
  busy = false
  updateControls()
}
byId('retry').addEventListener('click', () => run(() => invoke('desktop_connect')))
function returnToWorkbench() {
  if (!latestState?.connected) return
  void run(() => invoke('desktop_open_workbench'))
}
byId('open').addEventListener('click', returnToWorkbench)
window.addEventListener('keydown', event => {
  if (event.key !== 'Escape' || event.defaultPrevented || !settingsShown || busy || !latestState?.connected) return
  event.preventDefault()
  returnToWorkbench()
})
byId('choose').addEventListener('click', () => run(() => invoke('desktop_select_directory')))
byId('autostart').addEventListener('change', () => { editingAutostart = true })
byId('startup-form').addEventListener('submit', event => {
  event.preventDefault()
  run(async () => { await invoke('desktop_set_autostart', { enabled: byId('autostart').checked }); editingAutostart = false }, '启动设置已保存。')
})
byId('notifications').addEventListener('change', () => { editingNotifications = true })
byId('notifications-form').addEventListener('submit', event => {
  event.preventDefault()
  run(async () => { await invoke('desktop_set_notifications', { enabled: byId('notifications').checked }); editingNotifications = false }, '通知设置已保存。')
})
byId('check-updates').addEventListener('click', () => run(() => invoke('desktop_check_updates')))
byId('install-update').addEventListener('click', () => run(() => invoke('desktop_install_update')))
byId('pause-queues').addEventListener('click', () => run(
  () => invoke('desktop_pause_queues'),
  result => result.paused > 0 ? `已暂停 ${result.paused} 项待派发任务，任务内容已保留。` : '没有需要暂停的待派发任务。'
))
byId('exit').addEventListener('click', () => run(() => invoke('desktop_exit', { stopJobs: false })))
byId('stop-exit').addEventListener('click', () => {
  if (window.confirm('停止当前工作台的活动作业并退出？排队任务将暂停并保留。')) run(() => invoke('desktop_exit', { stopJobs: true }))
})
function onSettingsShown() {
  settingsShown = true
  if (!selectUpdateSettings()) byId('page-title').focus({ preventScroll: true })
  if (!busy) refresh().catch(error => { showMessage('error', String(error)) })
}
window.addEventListener('yss:settings-shown', onSettingsShown)
window.addEventListener('yss:settings-hidden', () => { settingsShown = false })
onSettingsShown()
window.setInterval(() => {
  if (settingsShown && !busy && !document.hidden && document.hasFocus()) refresh().catch(() => {})
}, 2000)
