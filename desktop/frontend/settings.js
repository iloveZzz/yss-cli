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
let toolsState = null
let toolsRequest = null
let toolResults = []
let toolsLoading = false
let toolUpdates = new Map()
let toolPlan = []
const toolNames = { git: 'Git', gh: 'GitHub CLI (gh)', glab: 'GitLab CLI (glab)', pi: 'Pi Agent' }

function toolById(id) {
  return toolsState?.tools.find(tool => tool.id === id)
}
function updateFor(tool) {
  const update = toolUpdates.get(tool.id)
  if (!update || ((update.currentVersion ?? null) === (tool.version ?? null) && (update.currentPath ?? null) === (tool.path ?? null))) return update
  return { ...update, updateable: false, officialAvailable: false, message: '工作台检测到工具版本或路径变化，请重新检查更新。' }
}
function canUpdate(tool) {
  const update = updateFor(tool)
  return (tool.installed || tool.detected) && toolsState?.scope === 'workbench' && latestState?.connectionKind === 'owned' &&
    !!update?.release && !!(update.updateable || update.officialAvailable) && !tool.pendingConfirmation
}
function canUseOfficial(tool, update = updateFor(tool)) {
  return toolsState?.scope === 'workbench' && latestState?.connectionKind === 'owned' && !!update?.release &&
    !!update.officialAvailable && !tool.pendingConfirmation
}
function canInstallFromChannel(tool) {
  return !tool.installed && !tool.detected && !tool.pendingConfirmation &&
    !!(tool.installable ?? toolsState?.channelAvailable)
}
function canInstall(tool) {
  return !tool.installed && !tool.detected && (canInstallFromChannel(tool) || canUseOfficial(tool))
}
function selectedTools(action) {
  return Array.from(byId('tools-list').querySelectorAll(`input[data-action="${action}"]:checked:not(:disabled)`), input => input.value)
}

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
function selectToolSettings() {
  const request = window.__yssShowToolSettings
  if (!['git', 'gh', 'glab', 'pi', 'missing'].includes(request)) return false
  delete window.__yssShowToolSettings
  toolsRequest = request
  selectTab(byId('tab-connection'))
  byId('tools-heading').focus({ preventScroll: true })
  byId('tools-section').scrollIntoView({ block: 'start' })
  if (!busy) void refreshTools()
  return true
}
window.addEventListener('yss:settings-tools', selectToolSettings)
function updateToolControls() {
  const hasResults = !byId('tools-results').hidden
  for (const input of byId('tools-list').querySelectorAll('input')) {
    const tool = toolById(input.value)
    input.disabled = busy || toolsLoading || !tool || (input.dataset.action === 'update'
      ? !canUpdate(tool)
      : !canInstall(tool))
  }
  for (const button of byId('tools-list').querySelectorAll('.tool-update-actions button')) button.disabled = busy || toolsLoading || hasResults
  byId('tools-options').disabled = busy || toolsLoading || !toolsState || hasResults
  byId('install-tools').disabled = busy || toolsLoading || hasResults || !selectedTools('install').length
  byId('update-tools').hidden = !toolsState?.tools.some(canUpdate)
  byId('update-tools').disabled = busy || toolsLoading || hasResults || !selectedTools('update').length
  byId('check-tool-updates').disabled = busy || toolsLoading || hasResults || !toolsState
  byId('check-tools').disabled = busy || toolsLoading || hasResults
  byId('confirm-tools').disabled = busy || toolsLoading
  for (const button of byId('tools-results-list').querySelectorAll('button')) button.disabled = busy
}
function updateControls() {
  for (const control of document.querySelectorAll('.button, input[type="checkbox"]')) control.disabled = busy
  const back = byId('open')
  back.hidden = !latestState?.connected
  back.disabled = busy || back.hidden
  byId('stop-exit').hidden = latestState?.connectionKind !== 'owned'
  byId('pause-queues').disabled = busy || !latestState?.connected
  byId('install-update').disabled = busy || !latestState?.updateAvailable || latestState?.updateChecking
  byId('check-updates').disabled = busy || !!latestState?.updateChecking
  updateToolControls()
  document.querySelector('main').setAttribute('aria-busy', String(busy))
}
function renderTools() {
  const list = byId('tools-list')
  const previous = new Map(Array.from(list.querySelectorAll('input[data-selectable="true"]'), input => [`${input.dataset.action}:${input.value}`, input.checked]))
  list.replaceChildren()
  for (const tool of toolsState.tools) {
    const update = updateFor(tool)
    const row = document.createElement('div')
    row.className = 'tool-row'
    const input = document.createElement('input')
    input.type = 'checkbox'
    input.name = 'tool'
    input.value = tool.id
    input.id = `tool-${tool.id}`
    input.dataset.action = tool.installed || tool.detected ? 'update' : 'install'
    const selectable = input.dataset.action === 'update' ? canUpdate(tool) : canInstall(tool)
    input.dataset.selectable = String(!!selectable)
    input.disabled = !selectable
    const defaultSelected = input.dataset.action === 'install' && !!tool.selectedByDefault
    const requestedSelection = toolsRequest === 'missing' ? defaultSelected : input.dataset.action === 'install' && tool.id === toolsRequest
    input.checked = !input.disabled && (toolsRequest ? requestedSelection : (previous.get(`${input.dataset.action}:${tool.id}`) ?? defaultSelected))
    const content = document.createElement('div')
    const label = document.createElement('label')
    label.htmlFor = input.id
    label.textContent = toolNames[tool.id] || tool.id
    const description = document.createElement('p')
    const prerequisites = Array.isArray(tool.prerequisites) ? tool.prerequisites.join('、') : tool.prerequisites
    const capabilities = tool.id === 'pi' && tool.detected ? [
      typeof tool.basicReady === 'boolean' && (tool.basicReady ? '基础作业可用' : '基础作业不可用'),
      typeof tool.sandboxReady === 'boolean' && (tool.sandboxReady ? '离线命令可用' : '离线命令不可用'),
      tool.sandboxReady === false && tool.sandboxMessage
    ] : []
    const detection = toolsState.scope !== 'workbench' && tool.id === 'pi' ? '等待检测'
      : tool.installed ? '已安装' : tool.detected ? '已发现，检测失败' : '未安装'
    const status = tool.pendingConfirmation ? `${tool.pendingUpdate ? '更新' : '安装'}结果待确认` : detection
    description.textContent = [status, tool.version, tool.path, ...capabilities, tool.message,
      tool.source && `来源：${tool.source}`, prerequisites && `前置项：${prerequisites}`].filter(Boolean).join(' · ')
    description.id = `${input.id}-detail`
    input.setAttribute('aria-describedby', description.id)
    content.append(label, description)
    if (update) {
      const latest = document.createElement('p')
      latest.id = `${input.id}-update`
      latest.textContent = [update.release && `上游稳定版 ${update.release.version}`, update.message,
        update.release?.nodeRequirement && `Node.js ${update.release.nodeRequirement}`, ...(update.prerequisites ?? [])].filter(Boolean).join(' · ')
      content.append(latest)
      input.setAttribute('aria-describedby', `${description.id} ${latest.id}`)
    }
    const actions = document.createElement('div')
    actions.className = 'tool-update-actions'
    if (canInstallFromChannel(tool)) actions.append(toolButton('安装', tool.id, () => confirmTools([tool.id], 'install')))
    if (canUpdate(tool) && update?.updateable) actions.append(toolButton('更新', tool.id, () => confirmTools([tool.id], 'update')))
    if (canUseOfficial(tool, update) && selectable) {
      const action = input.dataset.action
      actions.append(toolButton(action === 'update' ? '用官方包更新' : '用官方包安装', tool.id, () => confirmTools([tool.id], action, 'official')))
    }
    if (update?.release) {
      actions.append(toolButton('官方发行', tool.id, () => openRelease(tool.id, update.release, false)))
      if (update.release.assetUrl) actions.append(toolButton('官方安装包', tool.id, () => openRelease(tool.id, update.release, true)))
    }
    if (actions.childElementCount) content.append(actions)
    if (tool.pendingConfirmation) {
      const confirm = document.createElement('button')
      confirm.type = 'button'
      confirm.className = 'button secondary tool-confirm'
      confirm.textContent = '确认程序已结束'
      confirm.setAttribute('aria-label', `确认 ${tool.id} 的工具操作程序已结束`)
      confirm.addEventListener('click', () => void acknowledgeTool(tool.id))
      content.append(confirm)
    }
    row.append(input, content)
    list.append(row)
  }
  toolsRequest = null
  updateToolControls()
}
function toolButton(text, id, action) {
  const button = document.createElement('button')
  button.type = 'button'
  button.className = 'button secondary'
  button.textContent = text
  button.setAttribute('aria-label', `${text} ${toolNames[id] || id}`)
  button.addEventListener('click', action)
  return button
}
async function openRelease(id, release, download) {
  try {
    await invoke('desktop_open_tool_release', { toolId: id, version: release.version, download })
  } catch (error) {
    setText(byId('tools-status'), String(error))
  }
}
async function checkToolUpdates() {
  if (busy || toolsLoading || !toolsState) return
  toolsLoading = true
  updateToolControls()
  setText(byId('tools-status'), '正在检查官方版本…')
  try {
    const results = await invoke('desktop_check_tool_updates')
    toolUpdates = new Map(results.map(update => [update.id, update]))
    renderTools()
    setText(byId('tools-status'), '版本检查结束，请查看各工具结果。')
  } catch (error) {
    toolUpdates.clear()
    renderTools()
    setText(byId('tools-status'), `无法检查工具更新：${String(error)}`)
  } finally {
    toolsLoading = false
    updateToolControls()
  }
}
function confirmTools(ids, operation, forcedSource = null) {
  const dialog = byId('confirm-tool-updates')
  if (busy || toolsLoading || dialog.open) return
  toolPlan = ids.flatMap(id => {
    const tool = toolById(id)
    const updating = operation === 'update'
    if (!tool || (updating ? !canUpdate(tool) : !canInstall(tool))) return []
    const update = updateFor(tool)
    const sources = new Map()
    if (forcedSource !== 'official' && (updating ? update.updateable : canInstallFromChannel(tool))) {
      sources.set('channel', tool.source || toolsState.channel)
    }
    if (forcedSource !== 'channel' && canUseOfficial(tool, update)) {
      sources.set('official', `官方包 ${update.release.version}（仅工作台使用）`)
    }
    if (!sources.size) return []
    return [{ id, operation, sources, current: tool.version ?? null, currentPath: tool.path ?? null,
      installationId: update?.installationId ?? null, target: update?.release?.version,
      nodePlan: updating ? update.nodePlan : tool.nodePlan,
      prerequisites: updating ? update.prerequisites : tool.prerequisites,
      source: sources.keys().next().value }]
  })
  if (!toolPlan.length) return
  const list = byId('confirm-tool-updates-list')
  list.replaceChildren(...toolPlan.map(plan => {
    const item = document.createElement('li')
    const summary = document.createElement('p')
    const title = plan.operation === 'update' ? `${toolNames[plan.id]} ${plan.current || '版本未知'} → ${plan.target}` : toolNames[plan.id]
    summary.textContent = [title, plan.currentPath, ...(plan.prerequisites ?? [])].filter(Boolean).join(' · ')
    item.append(summary)
    if (plan.sources.size > 1) {
      const choices = document.createElement('fieldset')
      choices.className = 'tool-source-options'
      const legend = document.createElement('legend')
      legend.textContent = `${toolNames[plan.id]} 的安装来源`
      choices.append(legend)
      for (const [value, title] of plan.sources) {
        const label = document.createElement('label')
        const radio = document.createElement('input')
        radio.type = 'radio'
        radio.name = `tool-source-${plan.id}`
        radio.value = value
        radio.checked = value === plan.source
        label.append(radio, document.createTextNode(title))
        choices.append(label)
      }
      item.append(choices)
    } else {
      const source = document.createElement('p')
      source.textContent = `来源 ${plan.sources.get(plan.source)}`
      item.append(source)
    }
    return item
  }))
  dialog.returnValue = ''
  dialog.showModal()
}
byId('confirm-tool-updates').addEventListener('close', event => {
  const dialog = event.currentTarget
  const confirmed = dialog.returnValue === 'confirm'
  dialog.returnValue = ''
  const plan = toolPlan.map(item => ({ ...item, source: dialog.querySelector(`input[name="tool-source-${item.id}"]:checked`)?.value || item.source }))
  toolPlan = []
  if (confirmed) void runTools(plan)
})
async function refreshTools() {
  if (toolsLoading) return false
  toolsLoading = true
  updateToolControls()
  try {
    let result = await invoke('desktop_tools_status')
    // A completed marker may outlive a view or desktop restart. Only clear it
    // after the tool has been detected and its version check has succeeded.
    const completed = result.tools.filter(tool => tool.pendingCompleted && tool.installed)
    for (const tool of completed) await invoke('desktop_acknowledge_tool', { toolId: tool.id })
    if (completed.length) result = await invoke('desktop_tools_status')
    toolsState = result
    setText(byId('tools-channel'), [result.channel, result.channelMessage, result.integrationMessage].filter(Boolean).join(' · '))
    renderTools()
    setText(byId('tools-status'), '工具状态已更新。')
    return true
  } catch (error) {
    toolsState = null
    toolUpdates.clear()
    byId('tools-list').replaceChildren()
    setText(byId('tools-channel'), '安装渠道状态不可用。')
    setText(byId('tools-status'), `无法检查工具：${String(error)}`)
    return false
  } finally {
    toolsLoading = false
    updateToolControls()
  }
}
function renderToolResults() {
  const list = byId('tools-results-list')
  list.replaceChildren()
  for (const result of toolResults) {
    const item = document.createElement('li')
    const text = document.createElement('span')
    const verb = result.operation === 'update' ? '更新' : '安装'
    text.textContent = `${toolNames[result.id] || result.id}：${result.uncertain ? '结果待确认' : result.acknowledged ? '已确认，待刷新检测' : result.success ? `${verb}步骤完成` : '未完成'} · ${result.message}`
    item.append(text)
    if (result.uncertain) {
      const confirm = document.createElement('button')
      confirm.type = 'button'
      confirm.className = 'button secondary'
      confirm.textContent = '确认程序已结束'
      confirm.setAttribute('aria-label', `确认 ${result.id} 的工具操作程序已结束`)
      confirm.addEventListener('click', () => void acknowledgeTool(result.id))
      item.append(confirm)
    } else if (!result.success && !result.acknowledged) {
      if (!result.requiresRefresh) {
        const retry = document.createElement('button')
        retry.type = 'button'
        retry.className = 'button secondary'
        retry.textContent = '重试'
        retry.setAttribute('aria-label', `重试${verb} ${result.id}`)
        retry.addEventListener('click', () => confirmTools([result.id], result.operation, result.source))
        item.append(retry)
      }
      const tool = toolById(result.id)
      if (tool && canUseOfficial(tool) && result.source !== 'official') {
        item.append(toolButton('改用官方包', result.id, () => confirmTools([result.id], result.operation, 'official')))
      }
    }
    list.append(item)
  }
  byId('tools-results').hidden = !toolResults.length
  updateToolControls()
}
async function acknowledgeTool(id) {
  if (busy || toolsLoading) return
  busy = true
  updateControls()
  try {
    await invoke('desktop_acknowledge_tool', { toolId: id })
    const previous = toolResults.find(item => item.id === id)
    const operation = previous?.operation || (toolById(id)?.pendingUpdate ? 'update' : 'install')
    toolResults = [...toolResults.filter(item => item.id !== id), {
      id, operation, source: previous?.source, success: false, acknowledged: true, message: '请复检并核对工具状态。'
    }]
    renderToolResults()
    if (await refreshTools()) setText(byId('tools-status'), '已确认程序结束。请核对工具检测结果。')
  } catch (error) {
    setText(byId('tools-status'), `无法确认工具操作结果：${String(error)}`)
  } finally {
    busy = false
    updateControls()
  }
}
function usesUpdateCommand(item) {
  return item.operation === 'update' || item.source === 'official'
}
async function runTools(plan) {
  if (busy || toolsLoading || !plan.length) return
  busy = true
  updateControls()
  byId('tools-progress').hidden = false
  const progress = byId('tools-progress-bar')
  progress.max = plan.length
  progress.value = 0
  for (const [index, item] of plan.entries()) {
    const { id, operation, current, currentPath, installationId, target, nodePlan, source } = item
    const verb = operation === 'update' ? '更新' : '安装'
    setText(byId('tools-progress-label'), `正在${verb} ${toolNames[id]}（${index + 1} / ${plan.length}）`)
    setText(byId('tools-status'), `正在${verb} ${toolNames[id]}，请等待。`)
    let result
    const request = { toolId: id, nodePlan: nodePlan ?? null }
    try {
      result = usesUpdateCommand(item)
        ? await invoke('desktop_update_tool', { ...request, version: target, currentVersion: current ?? null, currentPath: currentPath ?? null, installationId: installationId ?? null, ...(source === 'official' ? { source, nodePlan: null } : {}) })
        : await invoke('desktop_install_tool', request)
    }
    catch (error) { result = { id, success: false, uncertain: true, message: `调用结果未知，请先核对安装程序及系统安装服务是否已结束。${String(error)}` } }
    toolResults = toolResults.filter(item => item.id !== id)
    toolResults.push({ ...result, operation, source })
    progress.value = index + 1
    renderToolResults()
  }
  byId('tools-progress').hidden = true
  setText(byId('tools-status'), '工具操作已结束，请核对逐项结果。')
  if (plan.some(usesUpdateCommand)) await refresh().catch(() => {})
  busy = false
  updateControls()
}
byId('tools-form').addEventListener('submit', event => {
  event.preventDefault()
  confirmTools(selectedTools('install'), 'install')
})
byId('tools-list').addEventListener('change', updateToolControls)
byId('check-tools').addEventListener('click', () => void refreshTools())
byId('check-tool-updates').addEventListener('click', () => void checkToolUpdates())
byId('update-tools').addEventListener('click', () => confirmTools(selectedTools('update'), 'update'))
byId('confirm-tools').addEventListener('click', async () => {
  if (busy) return
  busy = true
  updateControls()
  try {
    const refreshService = toolResults.some(item => !usesUpdateCommand(item) && (item.success || item.uncertain || item.acknowledged))
    if (refreshService && latestState?.connectionKind === 'owned') await invoke('desktop_refresh_tools')
    else if (refreshService && latestState?.connectionKind === 'external') setText(byId('tools-status'), '请自行刷新外部服务后重新检查。')
    if (!await refreshTools()) return
    const remaining = []
    for (const result of toolResults) {
      if (result.success) {
        const tool = toolsState?.tools.find(item => item.id === result.id)
        if (!tool?.installed || tool.pendingConfirmation) {
          remaining.push({ ...result, success: false, uncertain: true,
            message: usesUpdateCommand(result)
              ? '更新程序已结束，工作台尚未检测到目标版本。请核对渠道中的版本和工作台使用的执行路径。'
              : '安装程序已结束，复检暂未发现该工具。请核对实际安装和 PATH，确认安装服务已结束后再处理。' })
        }
      } else if (result.uncertain) remaining.push(result)
    }
    toolResults = remaining
    renderToolResults()
    if (!remaining.length) await refreshTools()
    if (refreshService && latestState?.connectionKind === 'external') setText(byId('tools-status'), '请自行刷新外部服务后重新检查；当前显示的是现有服务的检测结果。')
  } catch (error) {
    setText(byId('tools-status'), `无法刷新服务：${String(error)}。请处理后重试，工具操作结果已保留。`)
  } finally {
    busy = false
    updateControls()
  }
})
async function refresh() {
  const state = await invoke('desktop_status')
  latestState = state
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
  const toolsHadConnection = toolsState?.scope !== 'local'
  if (toolsState && toolsHadConnection !== state.connected && !toolResults.length) void refreshTools()
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
byId('retry').addEventListener('click', async () => {
  await run(() => invoke('desktop_connect'))
  if (settingsShown && !toolResults.length) void refreshTools()
})
function returnToWorkbench() {
  if (!latestState?.connected) return
  void run(() => invoke('desktop_open_workbench'))
}
byId('open').addEventListener('click', returnToWorkbench)
window.addEventListener('keydown', event => {
  if (event.key !== 'Escape' || event.defaultPrevented || !settingsShown || busy || !latestState?.connected || byId('confirm-tool-updates').open) return
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
  const openedTools = selectToolSettings()
  if (!openedTools && !selectUpdateSettings()) byId('page-title').focus({ preventScroll: true })
  if (!busy) refresh().catch(error => { showMessage('error', String(error)) })
  if (!openedTools && !busy && (!toolsState || toolsState.scope === 'unavailableWorkbench') && !toolResults.length) void refreshTools()
}
window.addEventListener('yss:settings-shown', onSettingsShown)
window.addEventListener('yss:settings-hidden', () => { settingsShown = false })
onSettingsShown()
window.setInterval(() => {
  if (settingsShown && !busy && !document.hidden && document.hasFocus()) refresh().catch(() => {})
}, 2000)
