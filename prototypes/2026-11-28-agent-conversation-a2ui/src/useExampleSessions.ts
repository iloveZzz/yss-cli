import { computed, reactive, ref, watch } from 'vue'
import { useEventListener } from '@vueuse/core'
import { examples, hosts, newSession, type Example, type Host, type Session } from './conversation'

const legacyKey = 'yss.conversation-cards.prototype.v1'
const sessionPrefix = 'yss.conversation-cards.prototype.v2.session.'
const viewKey = 'yss.conversation-cards.prototype.v2.view'
type BrowserStore = 'localStorage' | 'sessionStorage'

function read(store: BrowserStore, key: string) {
  try { return window[store].getItem(key) } catch { return undefined }
}

function write(store: BrowserStore, key: string, value: string) {
  try { window[store].setItem(key, value); return true } catch { return false }
}

function parse(raw: string | null | undefined): unknown {
  if (!raw) return undefined
  try { return JSON.parse(raw) } catch { return undefined }
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function question(value: unknown) { return value === 'scope' || value === 'owner' }

function valid(value: unknown): value is Session {
  if (!record(value) || typeof value.draft !== 'string' || !Array.isArray(value.rounds) || !value.rounds.length) return false
  const receipt = value.receipt
  if (receipt !== undefined && (!record(receipt) || typeof receipt.prompt !== 'string' ||
    typeof receipt.roundId !== 'number' || !Number.isSafeInteger(receipt.roundId) || receipt.roundId < 1)) return false
  if (value.sendError !== undefined && typeof value.sendError !== 'string') return false
  return value.rounds.every((round, i) => record(round) && round.id === i + 1 && typeof round.prompt === 'string' &&
    typeof round.reply === 'string' && Array.isArray(round.expanded) && round.expanded.every(question) &&
    (round.card === undefined || round.card === 'clarify' || round.card === 'compare') &&
    (round.selected === undefined || round.selected === 'closed' || round.selected === 'admin') &&
    (round.question === undefined || question(round.question)) && (round.basis === undefined || typeof round.basis === 'boolean') &&
    (round.replayStage === undefined || [0, 1, 2, 3, 4].some(stage => stage === round.replayStage)))
}

export function useExampleSessions() {
  const legacy = parse(read('localStorage', legacyKey))
  const oldSessions = record(legacy) && record(legacy.sessions) ? legacy.sessions : {}
  const tab = parse(read('sessionStorage', viewKey))
  const view = record(tab) ? tab : record(legacy) ? legacy : {}
  const host = ref<Host>(typeof view.host === 'string' && Object.hasOwn(hosts, view.host) ? view.host as Host : 'pi')
  const example = ref<Example>(typeof view.example === 'string' && Object.hasOwn(examples, view.example) ? view.example as Example : 'clarify')
  const sessions = reactive<Record<string, Session>>({})
  const sessionId = computed(() => `${host.value}-${example.value}`)
  const session = computed(() => sessions[sessionId.value])
  const known = new Map<string, { example: Example; raw: string | null | undefined; snapshot: string }>()

  // A card can be clicked before this tab receives the other tab's storage event.
  function refreshSession(id = sessionId.value) {
    const state = known.get(id)
    if (!state) return false
    const raw = read('localStorage', sessionPrefix + id)
    if (raw === undefined || raw === state.raw) return true
    state.raw = raw
    const newer = raw === null ? newSession(state.example) : parse(raw)
    if (!valid(newer)) return true
    state.snapshot = JSON.stringify(newer)
    sessions[id] = newer
    return false
  }

  for (const name of Object.keys(hosts)) {
    for (const item of Object.keys(examples) as Example[]) {
      const id = `${name}-${item}`
      const key = sessionPrefix + id
      let raw = read('localStorage', key)
      const current = parse(raw)
      const older = oldSessions[id]
      const migrated = raw == null && valid(older)
      const initial = valid(current) ? current : migrated ? older : newSession(item)
      const snapshot = JSON.stringify(initial)
      if (migrated && raw === null && write('localStorage', key, snapshot)) raw = snapshot
      const state = { example: item, raw, snapshot }
      known.set(id, state)
      sessions[id] = initial
      watch(() => JSON.stringify(sessions[id]), next => {
        if (next === state.snapshot || !refreshSession(id)) return
        if (write('localStorage', key, next)) {
          state.raw = next
          state.snapshot = next
        }
      })
    }
  }

  watch([host, example], () => {
    write('sessionStorage', viewKey, JSON.stringify({ host: host.value, example: example.value }))
    refreshSession()
  }, { flush: 'sync' })
  useEventListener(window, 'storage', event => {
    if (event.key === null) for (const id of known.keys()) refreshSession(id)
    else if (event.key.startsWith(sessionPrefix)) refreshSession(event.key.slice(sessionPrefix.length))
  })
  return { host, example, sessionId, session, refreshSession }
}
