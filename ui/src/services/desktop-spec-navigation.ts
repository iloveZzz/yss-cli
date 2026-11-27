import type { Router } from 'vue-router'
import { lifecycleStages } from './workbench-model'

const launchKey = 'desktopSpecRoute'
const navigateEvent = 'yss:desktop-spec-navigate'
const stages = new Set<string>(lifecycleStages)
const listFilters: ReadonlyMap<string, ReadonlySet<string>> = new Map([
  ['reminder', new Set(['ready'])],
  ['attention', new Set(['unverified', 'needsReview', 'awaitingConfirmation'])],
  ['showClosed', new Set(['true'])],
  ['status', new Set(['todo'])],
  ['stage', stages]
])

function specRoute(value: unknown): string | undefined {
  if (typeof value !== 'string' || /[#\r\n]/.test(value)) return
  const queryStart = value.indexOf('?')
  const path = queryStart === -1 ? value : value.slice(0, queryStart)
  const query = new URLSearchParams(queryStart === -1 ? '' : value.slice(queryStart))

  if (path === '/specs') {
    const seen = new Set<string>()
    for (const [key, filter] of query) {
      if (!listFilters.get(key)?.has(filter)) return
      const entry = key === 'attention' ? filter : key
      if (seen.has(entry)) return
      seen.add(entry)
    }
  } else if (path.length === 39 && /^\/specs\/[0-9a-f]{32}$/.test(path)) {
    const count = Array.from(query).length
    const tab = query.get('tab')
    const stage = query.get('stage')
    if (count && !(
      (tab === 'content' && count === 2 && query.get('category') === 'ready') ||
      (tab === 'lifecycle' && (count === 1 || (count === 2 && stage !== null && stages.has(stage))))
    )) return
  } else {
    return
  }

  const search = query.toString()
  return search ? `${path}?${search}` : path
}

export function registerDesktopSpecNavigation(router: Router): void {
  const desktop = window as Window & {
    __YSS_DESKTOP_SETTINGS_URL__?: string
    __YSS_DESKTOP_SPEC_ROUTE__?: unknown
  }
  if (desktop !== desktop.top || location.pathname !== '/' ||
      typeof desktop.__YSS_DESKTOP_SETTINGS_URL__ !== 'string') return

  const url = new URL(location.href)
  const launchRoutes = url.searchParams.getAll(launchKey)
  if (launchRoutes.length) {
    url.searchParams.delete(launchKey)
    history.replaceState(history.state, '', url)
  }
  let pending = launchRoutes.length === 1 ? specRoute(launchRoutes[0]) : undefined
  let active: string | undefined
  let navigating = false

  async function navigate() {
    if (navigating || !pending) return
    navigating = true
    try {
      await router.isReady()
      while (pending) {
        active = pending
        pending = undefined
        try {
          await router.push(active)
        } catch {
          // The workbench router reports route load failures.
        }
      }
    } catch {
      // The initial navigation failed. The workbench router handles recovery.
    } finally {
      active = undefined
      navigating = false
    }
  }

  function receive() {
    const route = specRoute(desktop.__YSS_DESKTOP_SPEC_ROUTE__)
    delete desktop.__YSS_DESKTOP_SPEC_ROUTE__
    if (route) pending = route === active ? undefined : route
    void navigate()
  }

  desktop.addEventListener(navigateEvent, receive)
  receive()
}
