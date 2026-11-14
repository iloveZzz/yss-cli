import { computed, shallowReadonly, shallowRef } from 'vue'
import { ApiError, errorMessage, executeCommand, expired, loadSnapshot, type Action, type Snapshot } from '@/services/workbench'
const snapshot = shallowRef<Snapshot | null>(null)
const loading = shallowRef(false)
const saving = shallowRef(false)
const error = shallowRef('')
let controller: AbortController | undefined
async function refresh() {
  if (expired.value) return
  controller?.abort()
  const current = new AbortController(); controller = current
  loading.value = true
  try { const next = await loadSnapshot(current.signal); if (!current.signal.aborted) { snapshot.value = next; error.value = '' } }
  catch (e) { if (!current.signal.aborted) error.value = errorMessage(e) }
  finally { if (controller === current) loading.value = false }
}
async function submit(action: Action, expectedRevision = snapshot.value?.revision) {
  if (saving.value) throw new Error('请等待当前保存完成')
  if (expectedRevision === undefined) throw new Error('请先读取工作台')
  saving.value = true
  try {
    const result = await executeCommand({ action, expectedRevision })
    await refresh()
    return result
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) await refresh()
    throw e
  } finally { saving.value = false }
}
const activeSpecs = computed(() => snapshot.value?.specs.filter(s => s.record.active && snapshot.value?.projects.some(p => p.record.id === s.record.projectId && p.record.active)) ?? [])
export function useWorkbench() { return { snapshot: shallowReadonly(snapshot), loading: shallowReadonly(loading), saving: shallowReadonly(saving), error: shallowReadonly(error), expired: shallowReadonly(expired), activeSpecs, refresh, submit } }
