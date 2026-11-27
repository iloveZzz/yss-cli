import { computed, nextTick, onMounted, onScopeDispose, shallowReactive, shallowRef } from 'vue'
import { errorMessage, invoke, listen, outputChannel, type TerminalContext, type TerminalSession } from './native'
import { TerminalRenderer } from './renderer'

interface ProjectSession {
  session?: TerminalSession
  renderer?: TerminalRenderer
  action?: 'starting' | 'closing'
  error?: string
}

function sessionStatus(project: ProjectSession | undefined) {
  const session = project?.session
  if (project?.action === 'starting') return '正在启动…'
  if (project?.action === 'closing' || session?.state === 'closing') return '正在关闭…'
  if (session?.state === 'cleanupPending') return '终端清理未确认'
  if (session?.state === 'running') return project?.renderer?.id === session.id ? '运行中' : '连接已断开'
  if (session?.state === 'failed') return '会话失败'
  if (session?.state === 'exited') {
    return session.exitCode == null ? '已退出' : `已退出 · 退出码 ${session.exitCode}`
  }
  return ''
}

export function useTerminalSessions(getHost: (projectId: string) => HTMLElement | undefined) {
  const catalog = shallowRef<Pick<TerminalContext, 'connectionId' | 'projects' | 'windowsPty'>>()
  const records = shallowReactive(new Map<string, ProjectSession>())
  const selectedId = shallowRef('')
  const contextError = shallowRef('')
  const unlisten: Array<() => void> = []
  let ready = false
  let initializing = false
  let disposed = false
  let reading = false
  let contextRevision = 0
  let localSelectionRevision = 0
  let hostSelectionAt: number | undefined

  const loaded = computed(() => !!catalog.value)
  const projects = computed(() => catalog.value?.projects ?? [])
  const projectsById = computed(() => new Map(projects.value.map((project) => [project.id, project])))
  const tabs = computed(() => {
    const openIds = new Set([...records].filter(([, record]) => record.session || record.action || record.renderer)
      .map(([id]) => id))
    if (selectedId.value) openIds.add(selectedId.value)
    return [...openIds].map((id) => {
      const project = projectsById.value.get(id)
      const record = records.get(id)
      const path = record?.session?.rootPath ?? project?.rootPath ?? ''
      return {
        id,
        path,
        name: project?.name ?? (path.split(/[\\/]/).filter(Boolean).at(-1) || '项目终端'),
        state: record?.session?.state,
        hasRenderer: !!record?.renderer,
        starting: record?.action === 'starting',
      }
    })
  })
  const active = computed(() => {
    const tab = tabs.value.find(({ id }) => id === selectedId.value)
    if (!tab) return undefined
    const record = records.get(tab.id)
    return {
      ...tab,
      status: sessionStatus(record),
      error: record?.error ?? record?.session?.error,
      canStart: !contextError.value && !record?.action && projectsById.value.has(tab.id)
        && (!tab.state || tab.state === 'failed' || tab.state === 'exited'),
      canClose: !!tab.state && tab.state !== 'closing' && !record?.action,
    }
  })

  function ensureRecord(id: string) {
    let record = records.get(id)
    if (!record) {
      record = shallowReactive<ProjectSession>({})
      records.set(id, record)
    }
    return record
  }

  function disposeRenderer(record: ProjectSession) {
    const renderer = record.renderer
    record.renderer = undefined
    renderer?.dispose()
  }

  function clearRecords() {
    for (const record of records.values()) disposeRenderer(record)
    records.clear()
  }

  function applyContext(next: TerminalContext) {
    if (catalog.value?.connectionId !== next.connectionId) clearRecords()
    catalog.value = { connectionId: next.connectionId, projects: next.projects, windowsPty: next.windowsPty }
    const sessions = new Map(next.sessions.map((session) => [session.projectId, session]))
    for (const id of sessions.keys()) ensureRecord(id)

    for (const [id, record] of records) {
      const session = sessions.get(id)
      if (!session && record.session?.state === 'cleanupPending') record.error = undefined
      record.session = session
      // A starting renderer can receive bytes before its session ID is returned.
      if (record.renderer?.id) {
        if (session && record.renderer.id === session.id) {
          record.renderer.updateState(session.state)
        } else disposeRenderer(record)
      }
      if (!session && !record.action && (!record.error || !projectsById.value.has(id))) records.delete(id)
    }

    const listed = (id: string) => projectsById.value.has(id) || sessions.has(id)
    const hostId = next.selectedProjectId
    if (hostSelectionAt === localSelectionRevision && hostId && listed(hostId)) {
      selectedId.value = hostId
    } else if (!listed(selectedId.value) && !records.get(selectedId.value)?.action) {
      selectedId.value = next.sessions[0]?.projectId ?? ''
    }
    hostSelectionAt = undefined
    contextError.value = ''
  }

  async function readContext() {
    reading = true
    let revision: number
    do {
      revision = contextRevision
      try {
        const next = await invoke('terminal_context', undefined)
        if (!disposed && revision === contextRevision) applyContext(next)
      } catch (cause) {
        if (!disposed && revision === contextRevision) {
          contextError.value = errorMessage(cause, '无法获取终端会话')
        }
      }
    } while (!disposed && revision !== contextRevision)
    reading = false
  }

  function refresh() {
    if (!ready || disposed) return
    // A read crossing a host notice or a completed command cannot replace newer state.
    contextRevision++
    if (!reading) void readContext()
  }

  function hostContextChanged() {
    if (disposed) return
    hostSelectionAt = localSelectionRevision
    refresh()
  }

  async function retry() {
    if (disposed || initializing) return
    if (ready) {
      refresh()
      return
    }
    initializing = true
    contextError.value = ''
    hostSelectionAt = localSelectionRevision
    try {
      const removeContext = await listen('terminal:context-changed', hostContextChanged)
      if (disposed) return removeContext()
      unlisten.push(removeContext)
      const removeSession = await listen('terminal:session-changed', refresh)
      if (disposed) return removeSession()
      unlisten.push(removeSession)
      await invoke('terminal_ready', undefined)
      if (disposed) return
      ready = true
      refresh()
    } catch (cause) {
      for (const remove of unlisten.splice(0)) remove()
      if (!disposed) contextError.value = errorMessage(cause, '终端初始化失败')
    } finally {
      initializing = false
    }
  }

  function selectProject(id: string) {
    if (!projectsById.value.has(id) && !records.get(id)?.session && !records.get(id)?.action) return
    localSelectionRevision++
    selectedId.value = id
  }

  function removeSession(record: ProjectSession, sessionId: string) {
    if (record.session?.id === sessionId) record.session = undefined
    if (record.renderer?.id === sessionId) disposeRenderer(record)
  }

  async function start() {
    const tab = active.value
    const context = catalog.value
    if (!tab?.canStart || !context) return false
    const { connectionId, windowsPty } = context
    const projectId = tab.id
    const record = ensureRecord(projectId)
    const current = () => !disposed && records.get(projectId) === record
    const previous = record.session
    record.action = 'starting'
    record.error = undefined
    let renderer: TerminalRenderer | undefined
    try {
      if (previous) {
        await invoke('terminal_close', { connectionId, sessionId: previous.id })
        if (!current()) return false
        removeSession(record, previous.id)
        refresh()
      }
      await nextTick()
      if (!current()) return false
      const host = getHost(projectId)
      if (!host) throw new Error('终端显示区域不可用')

      const provisional = new TerminalRenderer(host, (message) => {
        if (current() && record.renderer === provisional) record.error = message
      }, windowsPty)
      renderer = provisional
      record.renderer = provisional
      const size = provisional.dimensions()
      const output = outputChannel((data) => provisional.writeOutput(data))
      const session = await invoke('terminal_start', { connectionId, projectId, ...size, output })
      if (!current()) return false
      // A fast exit may reach the context read before the start response.
      record.session ??= session
      if (record.session.id !== session.id) {
        disposeRenderer(record)
        return false
      }
      provisional.bind(record.session, size)
      return record.session.state === 'running'
    } catch (cause) {
      if (current()) {
        if (renderer && record.renderer === renderer) disposeRenderer(record)
        record.error = errorMessage(cause, '无法启动终端')
      }
      return false
    } finally {
      if (current()) {
        record.action = undefined
        refresh()
      }
    }
  }

  async function close() {
    const tab = active.value
    const connectionId = catalog.value?.connectionId
    if (!tab?.canClose || !connectionId) return false
    const record = records.get(tab.id)
    if (!record?.session) return false
    const session = record.session
    const current = () => !disposed && records.get(tab.id) === record
    record.action = 'closing'
    record.error = undefined
    record.renderer?.setClosePending(true)
    try {
      await invoke('terminal_close', { connectionId, sessionId: session.id })
      if (!current()) return false
      removeSession(record, session.id)
      return true
    } catch (cause) {
      if (current()) {
        if (record.session?.id !== session.id) return true
        record.error = errorMessage(cause, '关闭终端失败')
      }
      return false
    } finally {
      if (current()) {
        record.action = undefined
        record.renderer?.setClosePending(false)
        refresh()
      }
    }
  }

  function fitActive() {
    records.get(selectedId.value)?.renderer?.fit()
  }

  function focusActive() {
    const record = records.get(selectedId.value)
    if (record?.session?.state !== 'running' || record.action === 'closing'
      || record.renderer?.id !== record.session.id) return false
    record.renderer.focus()
    return true
  }

  onMounted(() => void retry())
  onScopeDispose(() => {
    disposed = true
    for (const remove of unlisten.splice(0)) remove()
    clearRecords()
  })

  return {
    projects, tabs, active, loaded, contextError: computed(() => contextError.value),
    retry, refresh, selectProject, start, close, fitActive, focusActive,
  }
}
