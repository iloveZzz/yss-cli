<script setup lang="ts">
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Alert } from '@/components/ui/alert'
import { Checkbox } from '@/components/ui/checkbox'
import { Textarea } from '@/components/ui/textarea'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, ref, shallowRef, watch, onBeforeUnmount, useId, toRaw } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useServerDraft } from '@/hooks/useServerDraft'
import {
  loadLifecycle,
  contentEqual,
  stages,
  stageProgressLabels,
  dateLabel,
  errorMessage,
  ApiError,
  type LifecycleView,
  type Stage,
  type StageRecord,
  type StageProgress
} from '@/services/workbench'
import { getTarget, type JsonObject } from '@/services/workbench-platform'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import ModalDialog from '@/components/ModalDialog.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import DraftNotice from '@/components/DraftNotice.vue'
import ConflictResolver from '@/components/ConflictResolver.vue'
import ResourceEditor from './ResourceEditor.vue'
const props = defineProps<{
  specId: string
  initialStage?: Stage
  confirmLeave: (dirty: boolean) => boolean | Promise<boolean>
}>()
const emit = defineEmits<{ saved: []; dirty: [value: boolean]; select: [stage: Stage] }>()
const { snapshot, submit, saving } = useWorkbench()
const id = useId()
const view = shallowRef<LifecycleView>()
const error = shallowRef('')
const loading = shallowRef(false)
const notice = shallowRef('')
const selected = shallowRef<Stage>(
  props.initialStage && props.initialStage !== 'unmarked' ? props.initialStage : 'research'
)
const open = shallowRef(false)
const focusAfter = shallowRef(false)
type StageDraft = Omit<StageRecord, 'updatedAt'>
const draft = ref<StageDraft>({
  specId: props.specId,
  stage: selected.value,
  progress: 'unmarked',
  note: '',
  resources: []
})
const baseline = shallowRef('')
const dirty = computed(
  () => open.value && !contentEqual(draft.value, JSON.parse(baseline.value || 'null'))
)
const conflict = shallowRef<{
  base: JsonObject
  local: JsonObject
  current: JsonObject
  token: string
}>()
const drafts = useServerDraft<StageDraft>({
  targetType: 'stage',
  targetId: () => (open.value ? `${props.specId}:${draft.value.stage}` : ''),
  payload: computed(() => (open.value ? draft.value : undefined)),
  restore: (value) => {
    draft.value = value
  },
  refreshBase: () => getTarget<StageDraft>({ kind: 'setStageRecord', ...draft.value })
})
let controller: AbortController | undefined
let revision = 0
let generation = 0
watch(dirty, (value) => emit('dirty', value), { flush: 'sync' })
watch(
  () => props.initialStage,
  (stage) => {
    if (stage && stage !== 'unmarked') selected.value = stage
  }
)
watch(
  () => props.specId,
  () => {
    generation++
    view.value = undefined
    open.value = false
  }
)
watch(
  [() => props.specId, () => snapshot.value?.revision, () => snapshot.value?.progress?.[props.specId]?.evidenceDigest],
  async () => {
    controller?.abort()
    const own = new AbortController()
    controller = own
    loading.value = true
    try {
      const result = await loadLifecycle(props.specId, own.signal)
      if (!own.signal.aborted) {
        view.value = result
        error.value = ''
        if (!props.initialStage)
          selected.value =
            result.spec.record.stage === 'unmarked' ? 'research' : result.spec.record.stage
      }
    } catch (cause) {
      if (!own.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (controller === own) loading.value = false
    }
  },
  { immediate: true }
)
onBeforeUnmount(() => {
  controller?.abort()
  emit('dirty', false)
  generation++
})
const enabled = computed(
  () =>
    view.value?.spec.record.active &&
    snapshot.value?.projects.some(
      (project) => project.record.id === view.value?.spec.record.projectId && project.record.active
    )
)
function select(stage: Stage) {
  selected.value = stage
  emit('select', stage)
}
async function edit(stage = selected.value) {
  select(stage)
  const record = view.value?.stages.find((node) => node.stage === stage)?.record
  draft.value = record
    ? {
        specId: record.specId,
        stage: record.stage,
        progress: record.progress,
        note: record.note,
        resources: structuredClone(record.resources)
      }
    : { specId: props.specId, stage: selected.value, progress: 'unmarked', note: '', resources: [] }
  baseline.value = JSON.stringify(draft.value)
  revision = view.value?.revision ?? 0
  error.value = ''
  notice.value = ''
  conflict.value = undefined
  focusAfter.value = false
  open.value = true
  try {
    const target = await getTarget<StageDraft>({ kind: 'setStageRecord', ...draft.value })
    drafts.setBase({ ...toRaw(draft.value) }, target.token)
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}
async function focus(stage = selected.value) {
  const action = { kind: 'setWorkStage' as const, entity: 'spec' as const, id: props.specId, stage }
  try {
    const target = await getTarget(action)
    await submit(action, view.value?.revision, target.token)
    emit('saved')
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}
async function save() {
  const submitted = structuredClone(toRaw(draft.value))
  const own = generation
  error.value = ''
  await drafts.flush()
  try {
    const result = await submit(
      { kind: 'setStageRecord', ...submitted },
      revision,
      drafts.baseToken.value || undefined
    )
    if (own !== generation) return
    baseline.value = JSON.stringify(submitted)
    revision = result.revision
    const target = await getTarget<StageDraft>({ kind: 'setStageRecord', ...submitted })
    await drafts.accepted(submitted, submitted, target.token)
    emit('saved')
    if (focusAfter.value) await focus(submitted.stage)
    if (!dirty.value) open.value = false
    else notice.value = '阶段已保存，新增输入已保留。'
  } catch (cause) {
    if (own !== generation) return
    error.value = errorMessage(cause)
    if (cause instanceof ApiError && cause.status === 409) {
      try {
        const target = await getTarget<StageDraft>({ kind: 'setStageRecord', ...submitted })
        conflict.value = {
          base: JSON.parse(baseline.value),
          local: { ...toRaw(draft.value) },
          current: { ...target.values },
          token: target.token
        }
      } catch (readError) {
        error.value = errorMessage(readError)
      }
    }
  }
}
function merge(value: JsonObject) {
  if (!conflict.value) return
  baseline.value = JSON.stringify(conflict.value.current)
  drafts.setBase(conflict.value.current as unknown as StageDraft, conflict.value.token)
  draft.value = value as unknown as StageDraft
  revision = snapshot.value?.revision ?? revision
  conflict.value = undefined
  error.value = ''
}
async function close() {
  await drafts.flush()
  if (await props.confirmLeave(dirty.value)) open.value = false
}
</script>
<template>
  <div class="section-heading">
    <div>
      <h2>全生命周期</h2>
      <p class="muted">阶段可并行、跳过和重开，记录当前实际进展。</p>
    </div>
    <span v-if="loading && view" role="status" class="muted">正在刷新阶段记录…</span>
  </div>
  <Alert v-if="error && !open" role="alert" class="notice error">{{ error }}</Alert>
  <LoadingSkeleton v-if="loading && !view" variant="table" :rows="4" label="正在读取阶段记录…" />
  <template v-if="view">
    <div class="relative" :aria-busy="loading">
      <div
        class="panel table-scroll stage-table-wrap"
        tabindex="0"
        role="region"
        :aria-labelledby="id + '-stages'"
      >
        <Table class="spec-table stage-table">
          <TableCaption :id="id + '-stages'" class="sr-only">生命周期阶段记录</TableCaption>
          <TableHeader>
            <TableRow>
              <TableHead scope="col">阶段</TableHead>
              <TableHead scope="col">进度</TableHead>
              <TableHead scope="col">阶段说明</TableHead>
              <TableHead scope="col">资源</TableHead>
              <TableHead scope="col">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow
              v-for="node in view.stages"
              :key="node.stage"
              :class="{ 'selected-stage': selected === node.stage }"
            >
              <TableHead scope="row">
                <Button
                  type="button"
                  variant="ghost"
                  class="stage-name"
                  :aria-pressed="selected === node.stage"
                  @click="select(node.stage)"
                >
                  {{ stages[node.stage] }}
                </Button>
                <span v-if="view.spec.record.stage === node.stage" class="row-sub focus-label">
                  当前关注
                </span>
              </TableHead>
              <TableCell class="stage-progress">
                <span :data-progress="node.record?.progress ?? 'unmarked'">
                  {{ stageProgressLabels[node.record?.progress ?? 'unmarked'] }}
                </span>
                <time v-if="node.record" class="row-sub" :datetime="node.record.updatedAt">
                  {{ dateLabel(node.record.updatedAt) }}
                </time>
              </TableCell>
              <TableCell class="stage-note">
                <Collapsible v-if="node.record?.note">
                  <CollapsibleTrigger>{{ node.record.note.split('\n')[0] }}</CollapsibleTrigger>
                  <CollapsibleContent>
                    <p class="preserve-lines">{{ node.record.note }}</p>
                  </CollapsibleContent>
                </Collapsible>
                <span v-else class="muted">尚未记录</span>
              </TableCell>
              <TableCell class="stage-resources">
                <Collapsible v-if="node.record?.resources.length">
                  <CollapsibleTrigger>{{ node.record.resources.length }} 项资源</CollapsibleTrigger>
                  <CollapsibleContent>
                    <ul>
                      <li v-for="(resource, index) in node.record.resources" :key="index">
                        <strong>{{ resource.label }}</strong>
                        <a
                          v-if="resource.targetType === 'web' && /^https?:\/\//i.test(resource.target)"
                          :href="resource.target"
                          target="_blank"
                          rel="noopener noreferrer"
                        >
                          {{ resource.target }} ↗
                        </a>
                        <code v-else>{{ resource.target }}</code>
                      </li>
                    </ul>
                  </CollapsibleContent>
                </Collapsible>
                <span v-else class="muted">—</span>
              </TableCell>
              <TableCell>
                <div class="inline-actions stage-actions">
                  <Button
                    type="button"
                    variant="outline"
                    :disabled="!enabled || saving"
                    @click="edit(node.stage)"
                  >
                    记录
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    :disabled="!enabled || saving || view.spec.record.stage === node.stage"
                    @click="focus(node.stage)"
                  >
                    设为关注
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
      <LoadingIndicator :active="loading" placement="overlay" />
    </div>
  </template>
  <ModalDialog
    v-model="open"
    title="记录阶段进度"
    :busy="saving"
    :before-close="() => confirmLeave(dirty)"
  >
    <form class="form-stack" aria-label="阶段记录" @submit.prevent="save">
      <h3>{{ stages[draft.stage] }}</h3>
      <DraftNotice
        :status="drafts.status.value"
        :error="drafts.error.value"
        :drafts="drafts.recoverable.value"
        :conflict="drafts.recoveryConflict.value"
        @merge="drafts.mergeRecovered"
        @cancel-recovery="drafts.cancelRecovery"
        @restore="drafts.restore"
        @discard="drafts.discard"
      />
      <ConflictResolver
        v-if="conflict"
        :base="conflict.base"
        :local="conflict.local"
        :current="conflict.current"
        @resolve="merge"
        @close="conflict = undefined"
      />
      <label :for="id + 'progress'">
        阶段记录
        <SingleSelect
          :id="id + 'progress'"
          :model-value="draft.progress"
          :options="Object.entries(stageProgressLabels).map(([value, label]) => ({ value, label }))"
          aria-label="阶段记录"
          @update:model-value="draft.progress = $event as StageProgress"
        />
      </label>
      <label :for="id + 'note'">
        阶段说明{{ ['blocked', 'skipped'].includes(draft.progress) ? '（必填）' : '' }}
        <Textarea
          :id="id + 'note'"
          v-model="draft.note"
          name="note"
          rows="4"
          :required="['blocked', 'skipped'].includes(draft.progress)"
        />
      </label>
      <ResourceEditor v-model="draft.resources" />
      <label class="check-label">
        <Checkbox :model-value="focusAfter" @update:model-value="focusAfter = $event === true" name="focusAfter" />
        同时设为当前关注阶段
      </label>
      <p v-if="notice" role="status">{{ notice }}</p>
      <Alert v-if="error" role="alert" class="notice error">{{ error }}</Alert>
      <div class="form-actions">
        <Button type="button" variant="outline" :disabled="saving" @click="close">关闭</Button>
        <Button :disabled="saving || !dirty">
          <LoadingIndicator :active="saving" />
          {{ saving ? '正在保存…' : '保存阶段' }}
        </Button>
      </div>
    </form>
  </ModalDialog>
</template>

<style scoped>
.stage-table-wrap {
  margin: 14px 0 24px;
  max-width: 100%;
}
.stage-table {
  min-width: 740px;
  border-collapse: separate;
  border-spacing: 0;
}
.stage-table tbody th {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  background: #fff;
  min-width: 100px;
  padding: 13px 14px;
  text-align: left;
  vertical-align: top;
}
.stage-table td {
  vertical-align: top;
  font-size: 12px;
}
.stage-name {
  padding: 0;
  border: 0;
  background: none;
  color: #303743;
  font-size: 13px;
  font-weight: 550;
}
.selected-stage > * {
  background: #f4f7ff !important;
}
.stage-progress {
  min-width: 130px;
}
.stage-progress [data-progress='blocked'] {
  color: #9b5a0e;
}
.stage-progress [data-progress='done'] {
  color: #278064;
}
.stage-progress [data-progress='doing'] {
  color: #315dcf;
}
.stage-progress time {
  font-size: 10px;
}
.stage-note {
  min-width: 180px;
  max-width: 380px;
  overflow-wrap: anywhere;
}
.stage-note [data-slot='collapsible-trigger'] {
  cursor: pointer;
  color: #62666c;
}
.stage-note [data-slot='collapsible'][data-state='closed'] [data-slot='collapsible-trigger'] {
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 330px;
  white-space: nowrap;
}
.stage-note p {
  margin-top: 10px;
  line-height: 1.8;
}
.stage-resources {
  min-width: 100px;
  max-width: 250px;
}
.stage-resources [data-slot='collapsible-trigger'] {
  cursor: pointer;
  color: #62666c;
}
.stage-resources ul {
  list-style: none;
  margin: 10px 0 0;
  padding: 0;
}
.stage-resources li {
  margin: 8px 0;
  overflow-wrap: anywhere;
}
.stage-resources a,
.stage-resources code {
  display: block;
  margin-top: 5px;
  font-size: 11px;
}
.stage-resources a {
  color: #315dcf;
}
.stage-actions {
  flex-wrap: nowrap;
  gap: 4px;
}
</style>
