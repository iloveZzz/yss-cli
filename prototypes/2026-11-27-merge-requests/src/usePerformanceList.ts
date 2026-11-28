import { ref } from 'vue'
import type { MergeRequest, Repository } from './demo'

export type Order = 'updated' | 'created'
type ReadAction = 'query' | 'refresh' | 'retry'

// This fixture models source-provided bounds. These timestamps bound unseen
// rows even if the current filters leave that source with no matches.
const sourceBounds: Record<string, Record<Order, string>> = {
  web: { updated: '2026-11-27 10:18', created: '2026-11-26 13:31' },
  release: { updated: '2026-11-27 09:54', created: '2026-11-25 09:02' },
}

const sourceDelays: Record<string, number> = { core: 450, web: 1250, release: 4200 }

export function usePerformanceList() {
  const loaded = ref(new Set<string>())
  const failed = ref(new Set<string>())
  const pending = ref(new Set<string>())
  const action = ref<ReadAction | null>(null)
  const timers = new Set<number>()
  let generation = 0
  let recovered = false

  function stop() {
    generation++
    timers.forEach(timer => window.clearTimeout(timer))
    timers.clear()
    pending.value.clear()
    action.value = null
  }

  function reset() {
    stop()
    loaded.value.clear()
    failed.value.clear()
    recovered = false
  }

  function read(repositories: Repository[], kind: ReadAction) {
    // Older fixture responses may still arrive after the filters change.
    const readGeneration = ++generation
    if (kind === 'query') {
      loaded.value.clear()
      failed.value.clear()
    }
    const targets = repositories.filter(repo => kind !== 'retry' || failed.value.has(repo.id))
    action.value = targets.length ? kind : null
    pending.value = new Set(targets.map(repo => repo.id))

    for (const { id } of targets) {
      const timer = window.setTimeout(() => {
        timers.delete(timer)
        if (readGeneration !== generation) return

        if (id === 'release' && !recovered && kind !== 'retry') {
          failed.value.add(id)
        } else {
          if (id === 'release') recovered = true
          loaded.value.add(id)
          failed.value.delete(id)
        }
        pending.value.delete(id)
        if (!pending.value.size) action.value = null
      }, kind === 'retry' ? 1050 : (sourceDelays[id] ?? 650))
      timers.add(timer)
    }
  }

  function confirmedRows(rows: MergeRequest[], sort: Order) {
    if (action.value !== 'query') return rows
    // Strict comparison with the bounds leaves tied rows withheld.
    return rows.filter(row => [...pending.value].every(id => {
      const bound = sourceBounds[id]?.[sort]
      return bound !== undefined && row[sort] > bound
    }))
  }

  return { loaded, failed, pending, action, read, reset, stop, confirmedRows }
}
