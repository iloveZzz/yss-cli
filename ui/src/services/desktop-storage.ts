import { shallowRef } from 'vue'
import { apiRequest, sessionHeaders } from './workbench'

const candidate = new URL(location.href).searchParams.get('desktopEditor') ?? ''
const editor = /^[0-9a-f]{32}$/.test(candidate) ? candidate : ''
export const desktopStorageEnabled = !!editor
const prefix = 'yss:pi:'
export const desktopDraftError = shallowRef('')
let revision = 0
let dirty = false
const failedWrites = new Map<string, string | undefined>()
let timer: ReturnType<typeof setTimeout> | undefined
let pending: Promise<boolean> | undefined

interface StoredDesktopDraft {
  id: string
  editorSessionId: string
  targetType: string
  targetId: string
  revision: number
  payload: Record<string, unknown>
}

export async function restoreDesktopDrafts(): Promise<void> {
  if (!editor) return
  const saved = await apiRequest<StoredDesktopDraft | null>(`/api/drafts/${editor}`)
  if (!saved) return
  if (saved.id !== editor || saved.editorSessionId !== editor || saved.targetType !== 'desktopPi' ||
      saved.targetId !== editor || !Number.isSafeInteger(saved.revision) || !saved.payload ||
      typeof saved.payload !== 'object' || Array.isArray(saved.payload)) {
    throw new Error('桌面草稿身份或格式无效，请核对工作台数据。')
  }
  revision = saved.revision
  for (const [key, value] of Object.entries(saved.payload)) {
    if (key.startsWith(prefix) && typeof value === 'string' && sessionStorage.getItem(key) === null)
      sessionStorage.setItem(key, value)
  }
}

function snapshot(): Record<string, string> {
  const result: Record<string, string> = {}
  for (let index = 0; index < sessionStorage.length; index++) {
    const key = sessionStorage.key(index)
    if (key?.startsWith(prefix)) {
      const value = sessionStorage.getItem(key)
      if (value !== null) result[key] = value
    }
  }
  return result
}

export function writeDesktopStorage(key: string, value?: string): void {
  try {
    if (value === undefined) sessionStorage.removeItem(key)
    else sessionStorage.setItem(key, value)
    failedWrites.delete(key)
  } catch (cause) {
    if (editor && key.startsWith(prefix)) {
      failedWrites.set(key, value)
      desktopDraftError.value = '桌面草稿未能保存；当前输入仍保留，请重试后再切换或退出。'
    }
    throw cause
  }
  if (!editor || !key.startsWith(prefix)) return
  dirty = true
  clearTimeout(timer)
  timer = setTimeout(() => { void flushDesktopDrafts() }, 160)
}

export async function flushDesktopDrafts(): Promise<boolean> {
  if (!editor) return true
  clearTimeout(timer)
  for (;;) {
    for (const [key, value] of failedWrites) {
      try {
        if (value === undefined) sessionStorage.removeItem(key)
        else sessionStorage.setItem(key, value)
        failedWrites.delete(key)
        dirty = true
      } catch {
        return false
      }
    }
    if (!pending && dirty) {
      dirty = false
      pending = (async () => {
        try {
          const saved = await apiRequest<StoredDesktopDraft>(`/api/drafts/${editor}`, {
            method: 'PUT', body: {
              expectedRevision: revision, editorSessionId: editor, targetType: 'desktopPi',
              targetId: editor, baseToken: '', base: {}, payload: snapshot()
            }
          })
          revision = saved.revision
          desktopDraftError.value = ''
          return true
        } catch {
          dirty = true
          desktopDraftError.value = '桌面草稿未能保存；当前输入仍保留，请重试后再切换或退出。'
          return false
        }
      })()
    }
    if (!pending) return true
    const saving = pending
    const saved = await saving
    if (pending === saving) pending = undefined
    if (!saved) return false
  }
}

export function registerDesktopFlush(): void {
  if (!editor) return
  window.addEventListener('yss:desktop-flush', (event) => {
    const requestId = (event as CustomEvent<{ requestId?: unknown }>).detail?.requestId
    if (typeof requestId !== 'string' || !/^[0-9a-f]{32}$/.test(requestId)) return
    void (async () => {
      const barrier = { failed: false }
      window.dispatchEvent(new CustomEvent('yss:desktop-before-flush', { detail: barrier }))
      const ok = !barrier.failed && await flushDesktopDrafts()
      try {
        const response = await fetch('/api/desktop/flush', {
          method: 'POST', headers: { ...sessionHeaders(), 'Content-Type': 'application/json' },
          body: JSON.stringify({ requestId, editorId: editor, ok })
        })
        if (!response.ok) throw new Error('桌面回执保存失败')
      } catch {
        desktopDraftError.value = '无法确认草稿保存结果，请重试。'
      }
    })()
  })
}
