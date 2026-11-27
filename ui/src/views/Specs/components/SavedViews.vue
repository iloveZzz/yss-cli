<script setup lang="ts">
import { Alert } from '@/components/ui/alert'
import { computed, nextTick, shallowRef, useId } from 'vue'
import { ChevronDown } from '@lucide/vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import { ApiError, errorMessage, type WorkQuery, type SavedView } from '@/services/workbench'
import ModalDialog from '@/components/ModalDialog.vue'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import { Input } from '@/components/ui/input'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { savedSpecQuery } from '../schemas/filter'
const props = defineProps<{ query: WorkQuery; viewId?: string }>()
const emit = defineEmits<{ apply: [view: SavedView]; removed: [] }>()
const { submit, saving } = useWorkbench()
const { catalog, refreshCatalog, catalogLoading } = useWorkbenchCatalog()
const id = useId()
const open = shallowRef(false)
const name = shallowRef('')
const error = shallowRef('')
const editing = shallowRef<string | null>(null)
const removing = shallowRef(false)
const stale = shallowRef(false)
const committedView = shallowRef<string>()
const views = computed(() => catalog.value?.savedViews.filter((view) => view.query.entity === 'spec') ?? [])
const current = computed(() => views.value.find((view) => view.id === props.viewId))
let revision = 0
let submittedQuery: WorkQuery
const changed = computed(() => {
  if (!current.value) return false
  const saved = savedSpecQuery(current.value)
  return (Object.keys(props.query) as (keyof WorkQuery)[]).some((key) =>
    JSON.stringify(saved[key]) !== JSON.stringify(props.query[key]))
})
function begin(update: boolean, remove = false) {
  if (!catalog.value) return
  revision = catalog.value.revision
  submittedQuery = structuredClone(props.query)
  editing.value = update ? current.value?.id ?? null : null
  name.value = update ? current.value?.name ?? '' : ''
  error.value = ''
  stale.value = false
  committedView.value = undefined
  removing.value = remove
  open.value = true
}
async function reloadBase() {
  if (!await refreshCatalog() || !catalog.value) return
  if (editing.value && !views.value.some((view) => view.id === editing.value)) {
    error.value = '这条视图已移除。当前名称和筛选条件已保留。'
    return
  }
  revision = catalog.value.revision
  stale.value = false
  error.value = ''
}
function applySaved(viewId: string) {
  const view = views.value.find((item) => item.id === viewId)
  if (!view) return false
  emit('apply', view)
  open.value = false
  return true
}
async function rereadSaved() {
  const viewId = committedView.value
  if (viewId && await refreshCatalog() && !applySaved(viewId)) error.value = '这条视图已经移除。'
}
async function save() {
  if (stale.value || committedView.value) return
  error.value = ''
  try {
    if (removing.value && editing.value) {
      await submit({ kind: 'removeView', viewId: editing.value }, revision, undefined, refreshCatalog)
      emit('removed')
      open.value = false
    } else {
      const result = await submit({
        kind: 'saveView', viewId: editing.value, name: name.value, query: submittedQuery,
        layout: 'list', groupBy: 'status'
      }, revision, undefined, refreshCatalog)
      if (result.viewId && !applySaved(result.viewId)) {
        committedView.value = result.viewId
        error.value = '视图已保存。请重新读取视图列表。'
      }
    }
  } catch (cause) {
    error.value = errorMessage(cause)
    stale.value = cause instanceof ApiError && cause.status === 409
  }
}
const viewOptions = computed(() => [
  { value: 'temporary', label: '临时查询' },
  ...(props.viewId && props.viewId !== 'temporary' && !current.value
    ? [{ value: props.viewId, label: catalog.value ? '视图已失效' : '正在读取视图' }]
    : []),
  ...views.value.map((view) => ({ value: view.id, label: view.name }))
])
function select(value: string) {
  if (value === 'temporary') {
    emit('removed')
    return
  }
  const view = views.value.find((item) => item.id === value)
  if (view) emit('apply', view)
}
let action: { id: string; type: 'update' | 'remove' } | undefined
function chooseAction(type: 'update' | 'remove') {
  if (current.value) action = { id: current.value.id, type }
}
async function runMenuAction() {
  const selected = action
  action = undefined
  await nextTick()
  if (!selected || current.value?.id !== selected.id || saving.value) return
  if (selected.type === 'update') begin(true)
  else begin(true, true)
}
</script>
<template>
 <div class="saved-views">
  <label :for="id+'views'" class="view-label">视图</label>
  <SingleSelect :id="id+'views'" :model-value="viewId??'temporary'" :options="viewOptions" aria-label="视图" class="view-select" @update:model-value="select" />
  <span v-if="changed" class="view-changed">已调整</span>
  <Button type="button" variant="ghost" :disabled="saving || !catalog" class="save-button" @click="begin(false)"><svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M8 3v10M3 8h10" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" /></svg>保存视图</Button>
  <DropdownMenu v-if="current">
    <DropdownMenuTrigger as-child>
      <Button type="button" variant="outline" :disabled="saving">管理视图 <ChevronDown :size="14" aria-hidden="true" /></Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" class="view-menu" @close-auto-focus="runMenuAction">
      <DropdownMenuLabel class="view-help">筛选调整仅临时生效，更新后才会保存到此视图。</DropdownMenuLabel>
      <DropdownMenuSeparator />
      <DropdownMenuItem class="view-action" :disabled="saving" @select="chooseAction('update')">更新当前视图</DropdownMenuItem>
      <DropdownMenuItem class="view-action remove-view" :disabled="saving" @select="chooseAction('remove')">移除此视图</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
 </div>
 <ModalDialog v-model="open" :title="removing?'移除保存的视图':'保存当前视图'" :busy="saving">
  <form class="form-stack" @submit.prevent="save">
   <p v-if="removing">仅移除这条查询收藏，保留所有 Spec 和历史。</p>
   <template v-else><label :for="id+'name'">视图名称</label><Input :id="id+'name'" v-model="name" required maxlength="80" /><p class="muted">保存当前筛选和排序。</p></template>
   <Alert v-if="error" class="notice error" role="alert">{{ error }}</Alert>
   <Button v-if="stale" type="button" variant="outline" :disabled="catalogLoading" @click="reloadBase">采用最新记录，保留输入</Button>
   <Button v-if="committedView" type="button" variant="outline" :disabled="catalogLoading" @click="rereadSaved">重读视图</Button>
   <div class="form-actions"><Button type="button" variant="outline" :disabled="saving" @click="open=false">取消</Button>
    <Button :disabled="saving || stale || !!committedView">
      <LoadingIndicator :active="saving" />
      {{ saving ? removing ? '正在移除…' : '正在保存…' : removing ? '确认移除' : '确认保存' }}
    </Button>
   </div>
  </form>
 </ModalDialog>
</template>

<style scoped>
.saved-views { display:flex; align-items:center; flex-wrap:wrap; gap:8px; margin:0; min-width:0; }
.view-label { color:var(--brand-color-text-secondary); font-size:12px; }
.saved-views :deep(.view-select) { width:180px; max-width:100%; min-width:0; height:32px; color:#555d6a; }
.view-changed { color:#96601e; background:#fff5e5; border-radius:4px; padding:2px 5px; font-size:10px; }
.save-button { gap:4px; color:#626d80; padding:0 7px; }
.view-menu { width: 216px; max-width: var(--reka-dropdown-menu-content-available-width, calc(100vw - 24px)); }
.view-help { padding:6px 8px; color:var(--brand-color-text-secondary); font-size:12px; font-weight:400; }
.view-action { width:100%; justify-content:flex-start; height:34px; }
.remove-view { color:#a12830; }
@media (width < 720px) { .saved-views { margin-left:0; }.saved-views :deep(.view-select) { width:auto; flex:1; max-width:220px; }.save-button { margin-left:auto; }.view-action { min-height:44px; } }
</style>
