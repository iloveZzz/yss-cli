import { computed, ref, shallowRef, watch, type Ref } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute } from 'vue-router'
import { useEventListener } from '@vueuse/core'
import { useWorkbench } from '@/hooks/useWorkbench'
import { editFields, errorMessage, type EditSpec } from '@/services/workbench'
export function useSpecEditor(externalDirty?: Readonly<Ref<boolean>>) {
  const route = useRoute(); const { snapshot, submit, saving } = useWorkbench()
  const spec = computed(() => snapshot.value?.specs.find(s => s.record.id === route.params.specId))
  const draft = ref<EditSpec>(); const saved = shallowRef(''); const error = shallowRef(''); const notice = shallowRef(''); const initializing = shallowRef(false)
  let revision = 0; let generation = 0
  const dirty = computed(() => !!draft.value && JSON.stringify(draft.value) !== saved.value)
  function reset() { if (spec.value) { draft.value = editFields(spec.value.record); saved.value = JSON.stringify(draft.value); revision = snapshot.value?.revision ?? 0; error.value = '' } }
  watch(() => route.params.specId, async id => {
    const own = ++generation; initializing.value = true; draft.value = undefined; error.value = ''; notice.value = ''
    try { if (typeof id === 'string' && spec.value) await submit({ kind: 'visitSpec', specId: id }) }
    catch (e) { if (own === generation) error.value = `访问记录未更新：${errorMessage(e)}` }
    finally { if (own === generation) { reset(); initializing.value = false } }
  }, { immediate: true })
  const confirmOpen = shallowRef(false)
  let resolveConfirmation: ((value: boolean) => void) | undefined
  function decide(value: boolean) { const resolve = resolveConfirmation; resolveConfirmation = undefined; confirmOpen.value = false; resolve?.(value) }
  watch(confirmOpen, value => { if (!value) decide(false) })
  function allowLeave(required = dirty.value || !!externalDirty?.value): boolean | Promise<boolean> {
    if (!required) return true
    decide(false); confirmOpen.value = true
    return new Promise(resolve => { resolveConfirmation = resolve })
  }
  onBeforeRouteLeave(() => allowLeave()); onBeforeRouteUpdate((to,from) => allowLeave(to.params.specId===from.params.specId ? !!externalDirty?.value : dirty.value || !!externalDirty?.value))
  useEventListener(window, 'beforeunload', event => { if (dirty.value || externalDirty?.value) { event.preventDefault(); event.returnValue = '' } })
  async function save() {
    if (!draft.value || !spec.value) return
    if (draft.value.status === 'blocked' && !draft.value.blocker.trim()) { error.value = '请填写阻塞说明'; return }
    error.value = ''; notice.value = ''
    const submitted = JSON.stringify(draft.value)
    const own = generation
    const specId = spec.value.record.id
    try {
      const result = await submit({ kind: 'editSpec', specId, ...JSON.parse(submitted) as EditSpec }, revision)
      if (own !== generation) return
      saved.value = submitted
      revision = result.revision
      notice.value = dirty.value ? '提交时的标记已保存；当前新增修改尚未保存。' : '个人标记已保存。'
    } catch (e) { if (own === generation) error.value = errorMessage(e) }
  }
  async function reload() { if (await allowLeave(dirty.value)) { reset(); notice.value = '' } }
  return { spec, draft, dirty, error, notice, initializing, saving, save, reload, confirmOpen, decide, allowLeave, reset }
}
