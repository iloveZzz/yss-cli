import { computed, onScopeDispose, reactive, shallowRef, watch, type Ref } from 'vue'
import { useDebounceFn, useEventListener } from '@vueuse/core'
import type { AgentCiJob, AgentCiMode, AgentCiPreview, AgentContinuation, AgentStart, ProjectAccessMode, ReviewExcerpt } from '@/services/workbench-agent'
import type { Stage } from '@/services/workbench'
import type { PiModel, PiThinkingLevel } from '@/services/workbench-platform'
import type { PiCheckout } from './usePiProjects'

type DraftText = {
  prompt: string
  citations: ReviewExcerpt[]
  handoff?: Omit<AgentContinuation, 'prompt'>
  selected?: string[]
  ciRecovery?: PiCiRecovery
}
export type PiCiRecovery = Omit<AgentCiJob, 'expectedSha'>
export interface PiCiAttachment {
  job: AgentCiPreview
  mode: AgentCiMode
  checkout: PiCheckout
}
export function ciSelection(draft: Pick<PiDraft, 'ciAttachment' | 'ciRecovery'>): PiCiRecovery | undefined {
  const attachment = draft.ciAttachment
  if (!attachment) return draft.ciRecovery
  const { projectId, linkId, jobId } = attachment.job
  return { projectId, linkId, jobId, mode: attachment.mode }
}

export interface PiDraft extends DraftText {
  selected: string[]
  readOnly: boolean
  allowCommands: boolean
  projectGrants: Record<string, { mode: Exclude<ProjectAccessMode, 'read'>; checkout: PiCheckout }>
  ciAttachment?: PiCiAttachment
  selectedModel?: PiModel
  thinkingLevel?: PiThinkingLevel
  error: string
  stageSelection?: { stage: Stage; goalVersion: string } | null
}

export interface PendingPiRequest {
  id: string
  key: string
  input: AgentStart
  excerpts: ReviewExcerpt[]
}

type StorageKind = 'draft' | 'pending' | 'admitted'

function storageKey(kind: StorageKind, key: string) {
  return 'yss:pi:' + kind + ':1:' + encodeURIComponent(key)
}

function read<T>(kind: StorageKind, key: string): T | undefined {
  try {
    const serialized = sessionStorage.getItem(storageKey(kind, key))
    return serialized ? JSON.parse(serialized) as T : undefined
  } catch {
    return undefined
  }
}

function write(kind: StorageKind, key: string, value?: unknown): boolean {
  try {
    if (value === undefined) sessionStorage.removeItem(storageKey(kind, key))
    else sessionStorage.setItem(storageKey(kind, key), JSON.stringify(value))
    return true
  } catch {
    return false
  }
}

function draftText(value?: DraftText): DraftText {
  return {
    prompt: typeof value?.prompt === 'string' ? value.prompt : '',
    citations: Array.isArray(value?.citations) ? value.citations.filter((item) => item &&
      typeof item.projectId === 'string' && typeof item.path === 'string' &&
      typeof item.jobId === 'string' && typeof item.reviewToken === 'string' &&
      typeof item.note === 'string' && typeof item.snippet === 'string' &&
      (item.side === 'initial' || item.side === 'result') &&
      Number.isSafeInteger(item.startLine) && Number.isSafeInteger(item.endLine)
    ).slice(0, 12) : [],
    selected: Array.isArray(value?.selected) ? value.selected.filter((id) => typeof id === 'string') : undefined,
    ciRecovery: value?.ciRecovery && typeof value.ciRecovery.projectId === 'string' && typeof value.ciRecovery.linkId === 'string' &&
      Number.isSafeInteger(value.ciRecovery.jobId) && ['reproduce', 'compare'].includes(value.ciRecovery.mode) ? value.ciRecovery : undefined,
    handoff: value?.handoff && typeof value.handoff.runId === 'string' && typeof value.handoff.jobId === 'string' &&
      Array.isArray(value.handoff.projectIds) && Array.isArray(value.handoff.workspaces) ? value.handoff : undefined
  }
}

export function usePiDrafts(key: Readonly<Ref<string>>) {
  const drafts = reactive(new Map<string, PiDraft>())
  const admitted = new Map<string, { key: string; text: DraftText }>()
  const dirty = new Set<string>()
  const storageIssue = shallowRef('')
  const pendingVersion = shallowRef(0)
  const admittedVersion = shallowRef(0)

  function getDraft(id: string) {
    if (!drafts.has(id)) {
      const stored = read<DraftText>('draft', id)
      const text = draftText(stored)
      const selected = text.selected ?? text.handoff?.projectIds
      drafts.set(id, {
        ...text,
        selected: selected ?? [],
        projectGrants: {}, readOnly: true, allowCommands: false, error: ''
      })
    }
    return drafts.get(id)!
  }
  const draft = computed(() => getDraft(key.value))
  function flushDrafts() {
    let ok = true
    for (const id of dirty) {
      const value = drafts.get(id)
      if (!value) continue
      const text = draftText({ ...value, ciRecovery: ciSelection(value) })
      if (!write('draft', id, text.prompt || text.citations.length || text.handoff || text.ciRecovery || text.selected?.length ? text : undefined)) ok = false
    }
    dirty.clear()
    storageIssue.value = ok ? '' : '此标签页无法保存草稿，当前输入仍保留。'
    return ok
  }
  const persistSoon = useDebounceFn(flushDrafts, 160)
  function persistDraft(id: string) {
    dirty.add(id)
    return flushDrafts()
  }
  watch(() => [key.value, draft.value.prompt, draft.value.citations, draft.value.handoff, draft.value.selected, draft.value.ciRecovery, draft.value.ciAttachment] as const, ([id], prior) => {
    dirty.add(id)
    if (prior[0] !== id) flushDrafts()
    else void persistSoon()
  }, { deep: true, flush: 'sync' })
  useEventListener(window, 'pagehide', flushDrafts)
  onScopeDispose(() => {
    persistSoon.cancel()
    flushDrafts()
  })
  function removeDraft(id: string) {
    dirty.delete(id)
    write('draft', id)
    drafts.delete(id)
  }

  const pending = computed(() => {
    // Each request has its own durable key, so writing one draft never serializes other sessions.
    void pendingVersion.value
    const value = read<PendingPiRequest>('pending', key.value)
    if (value?.id && value.key === key.value && typeof value.input?.prompt === 'string' &&
      (Array.isArray(value.input.projectIds) || Array.isArray(value.input.projectAccess))) {
      const target = value.input.resumeRunId || (value.input.continuationSource
        ? 'handoff:' + value.input.continuationSource.jobId : 'new')
      if (value.input.specId + ':' + target === key.value) return value
    }
    return undefined
  })
  function setPending(id: string, value?: PendingPiRequest) {
    const saved = write('pending', id, value)
    pendingVersion.value++
    return saved
  }

  function rememberAdmitted(id: string, draftKey: string, text: DraftText) {
    const saved = { key: draftKey, text: draftText(text) }
    admitted.set(id, saved)
    admittedVersion.value++
    const stored = read<string[]>('admitted', 'recent')
    const recent = [...(Array.isArray(stored) ? stored.filter((entry) => entry !== id) : []), id]
    for (const expired of recent.slice(0, -12)) forgetAdmitted(expired)
    if (admitted.size > 12) admitted.delete(admitted.keys().next().value!)
    write('admitted', 'recent', recent.slice(-12))
    return write('admitted', id, saved)
  }
  function admittedDraft(id: string) {
    void admittedVersion.value
    const saved = admitted.get(id) ?? read<{ key: string; text: DraftText }>('admitted', id)
    return saved && typeof saved.key === 'string' ? { key: saved.key, text: draftText(saved.text) } : undefined
  }
  function forgetAdmitted(id: string) {
    admitted.delete(id)
    write('admitted', id)
    admittedVersion.value++
  }
  return { draft, getDraft, removeDraft, persistDraft, storageIssue, pending, setPending,
    rememberAdmitted, admittedDraft, forgetAdmitted }
}
