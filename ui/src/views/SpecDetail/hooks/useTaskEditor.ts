import { computed, ref, shallowRef, watch, onBeforeUnmount, type Ref } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { errorMessage, type TaskEdit } from '@/services/workbench'
import { parseTaskTitles } from '../schemas/tasks'
export function useTaskEditor(specId: Readonly<Ref<string>>, taskId: Readonly<Ref<string | null>>) {
  const { snapshot, submit, saving } = useWorkbench()
  const draft = ref<TaskEdit>({ title:'',stage:'unmarked',status:'todo',blocker:'',notes:'',resources:[],active:true })
  const baseline = shallowRef(''); const error = shallowRef(''); const notice = shallowRef('')
  let revision = 0; let generation = 0
  const dirty = computed(() => JSON.stringify(draft.value) !== baseline.value)
  function reset() {
    const task = snapshot.value?.tasks.find(t => t.record.id === taskId.value && t.record.specId === specId.value)?.record
    const spec = snapshot.value?.specs.find(s => s.record.id === specId.value)?.record
    draft.value = task ? {title:task.title,stage:task.stage,status:task.status,blocker:task.blocker,notes:task.notes,resources:structuredClone(task.resources),active:task.active} : {title:'',stage:spec?.stage ?? 'unmarked',status:'todo',blocker:'',notes:'',resources:[],active:true}
    baseline.value = JSON.stringify(draft.value); revision = snapshot.value?.revision ?? 0; error.value = ''; notice.value = ''
  }
  watch([specId, taskId], () => { generation++; reset() }, {immediate:true})
  onBeforeUnmount(() => { generation++ })
  async function save() {
    const encoded = JSON.stringify(draft.value); const submitted = JSON.parse(encoded) as TaskEdit
    const own = generation; const id = taskId.value; const spec = specId.value
    error.value = ''; notice.value = ''
    const titles = parseTaskTitles(submitted.title)
    if (!id && (!titles.length || titles.length > 200)) { error.value = '请填写 1–200 行任务标题'; return false }
    if (id && submitted.status === 'blocked' && !submitted.blocker.trim()) { error.value = '请填写阻塞说明'; return false }
    try {
      const result = await submit(id ? {kind:'editTask',taskId:id,edit:submitted} : {kind:'addTasks',specId:spec,drafts:titles.map(title => ({title,stage:submitted.stage,resources:submitted.resources}))}, revision)
      if (own !== generation) return false
      baseline.value = encoded; revision = result.revision
      notice.value = dirty.value ? '提交内容已保存；新增输入尚未保存。' : '任务已保存。'
      return true
    } catch (e) { if (own === generation) error.value = errorMessage(e); return false }
  }
  return {draft,dirty,error,notice,saving,save,reset}
}
