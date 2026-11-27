<script setup lang="ts">
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Alert } from '@/components/ui/alert'
import { Textarea } from '@/components/ui/textarea'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, nextTick, ref, shallowRef, watch, onBeforeUnmount, useId, toRaw } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useServerDraft } from '@/hooks/useServerDraft'
import {
  lifecycleStages,
  contentEqual,
  stages,
  stageProgressLabels,
  dateLabel,
  errorMessage,
  ApiError,
  type Stage,
  type StageRecord,
  type StageProgress
} from '@/services/workbench'
import { getTarget, type DraftRecord, type EditableTarget, type JsonObject } from '@/services/workbench-platform'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import ModalDialog from '@/components/ModalDialog.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
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
const spec = computed(() => snapshot.value?.specs.find(({ record }) => record.id === props.specId))
const stageNodes = computed(() => {
  const records = new Map(snapshot.value?.stageRecords
    .filter((record) => record.specId === props.specId)
    .map((record) => [record.stage, record]))
  return lifecycleStages.map((stage) => ({ stage, record: records.get(stage) }))
})
const error = shallowRef('')
const openingStage = shallowRef<Stage>()
const restoring = shallowRef(false)
const submitting = shallowRef(false)
const busy = computed(() => saving.value || submitting.value || restoring.value)
const notice = shallowRef('')
const selected = shallowRef<Stage>(
  props.initialStage && props.initialStage !== 'unmarked' ? props.initialStage : 'research'
)
const open = shallowRef(false)
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
  current: JsonObject
  token: string
}>()
let revision = 0
let generation = 0
let expectedTarget = ''
let restoreTarget: EditableTarget<StageDraft> | undefined
const drafts = useServerDraft<StageDraft>({
  targetType: 'stage',
  targetId: () => (open.value ? `${props.specId}:${draft.value.stage}` : ''),
  payload: computed(() => (open.value ? draft.value : undefined)),
  restore: (value) => {
    if (restoreTarget) {
      baseline.value = JSON.stringify(restoreTarget.values)
      expectedTarget = restoreTarget.token
      revision = snapshot.value?.revision ?? revision
      drafts.setBase(restoreTarget.values, restoreTarget.token)
      restoreTarget = undefined
    }
    draft.value = value
  },
  refreshBase: async () => {
    const own = generation
    const target = await getTarget<StageDraft>({ kind: 'setStageRecord', ...draft.value })
    if (own !== generation) throw new Error('当前 Spec 已切换。')
    restoreTarget = target
    return target
  }
})
async function restoreDraft(record: DraftRecord<StageDraft>) {
  if (busy.value || drafts.recoveryConflict.value) return
  const own = generation
  restoring.value = true
  try {
    await drafts.restore(record)
    if (own === generation && drafts.recoveryConflict.value && expectedTarget && baseline.value)
      drafts.setBase(JSON.parse(baseline.value), expectedTarget)
  } finally {
    if (own === generation) restoring.value = false
  }
}
function cancelRecovery() {
  drafts.cancelRecovery()
  restoreTarget = undefined
  if (expectedTarget && baseline.value) drafts.setBase(JSON.parse(baseline.value), expectedTarget)
}
watch(dirty, (value) => emit('dirty', value), { flush: 'sync' })
watch(
  () => props.initialStage,
  (stage) => {
    selected.value = stage && stage !== 'unmarked' ? stage : 'research'
  },
  { immediate: true }
)
onBeforeUnmount(() => {
  emit('dirty', false)
  generation++
})
const enabled = computed(
  () =>
    spec.value?.record.active &&
    snapshot.value?.projects.some(
      (project) => project.record.id === spec.value?.record.projectId && project.record.active
    )
)
function select(stage: Stage) {
  selected.value = stage
  emit('select', stage)
}
async function edit(stage = selected.value) {
  if (openingStage.value || busy.value || open.value) return
  const own = generation
  openingStage.value = stage
  select(stage)
  const record = stageNodes.value.find((node) => node.stage === stage)?.record
  const values: StageDraft = record
    ? {
        specId: record.specId,
        stage: record.stage,
        progress: record.progress,
        note: record.note,
        resources: structuredClone(record.resources)
      }
    : { specId: props.specId, stage, progress: 'unmarked', note: '', resources: [] }
  error.value = ''
  notice.value = ''
  conflict.value = undefined
  expectedTarget = ''
  restoreTarget = undefined
  try {
    const target = await getTarget<StageDraft>({ kind: 'setStageRecord', ...values })
    if (own !== generation) return
    draft.value = target.values
    baseline.value = JSON.stringify(target.values)
    revision = snapshot.value?.revision ?? 0
    open.value = true
    await nextTick()
    if (own === generation && open.value) {
      expectedTarget = target.token
      drafts.setBase(target.values, target.token)
    }
  } catch (cause) {
    if (own === generation) error.value = errorMessage(cause)
  } finally {
    if (own === generation) openingStage.value = undefined
  }
}
async function save() {
  if (!expectedTarget || busy.value || openingStage.value || conflict.value || drafts.recoveryConflict.value) return
  const submitted = structuredClone(toRaw(draft.value))
  const own = generation
  error.value = ''
  submitting.value = true
  let committed = false
  try {
    await drafts.flush()
    if (own !== generation) return
    const result = await submit(
      { kind: 'setStageRecord', ...submitted },
      revision,
      expectedTarget
    )
    if (own !== generation) return
    committed = true
    revision = result.revision
    const target = await getTarget<StageDraft>({ kind: 'setStageRecord', ...submitted })
    if (own !== generation) return
    const unchangedInput = contentEqual(draft.value, submitted)
    const changesSinceCommit = !contentEqual(target.values, submitted)
    const canAdopt = unchangedInput || !changesSinceCommit
    if (canAdopt) {
      baseline.value = JSON.stringify(target.values)
      expectedTarget = target.token
      if (unchangedInput && changesSinceCommit) draft.value = target.values
      await drafts.accepted(target.values, target.values, target.token)
    } else {
      await drafts.flush()
    }
    if (own !== generation) return
    emit('saved')
    if (!canAdopt) {
      error.value = '阶段已保存。读取到更新的阶段记录，请合并本次新增输入。'
      conflict.value = {
        base: { ...submitted },
        current: { ...target.values },
        token: target.token
      }
      return
    }
    if (!dirty.value) open.value = false
    else notice.value = '阶段已保存，新增输入已保留。'
  } catch (cause) {
    if (own !== generation) return
    error.value = committed ? `阶段已保存，重新读取编辑基线失败。${errorMessage(cause)}` : errorMessage(cause)
    if (!committed && cause instanceof ApiError && cause.status === 409) {
      try {
        const target = await getTarget<StageDraft>({ kind: 'setStageRecord', ...submitted })
        if (own !== generation) return
        conflict.value = {
          base: JSON.parse(baseline.value),
          current: { ...target.values },
          token: target.token
        }
      } catch (readError) {
        if (own === generation) error.value = errorMessage(readError)
      }
    }
  } finally {
    if (own === generation) submitting.value = false
  }
}
function merge(value: JsonObject) {
  if (!conflict.value) return
  baseline.value = JSON.stringify(conflict.value.current)
  expectedTarget = conflict.value.token
  drafts.setBase(conflict.value.current as unknown as StageDraft, conflict.value.token)
  draft.value = JSON.parse(JSON.stringify(value)) as StageDraft
  revision = snapshot.value?.revision ?? revision
  conflict.value = undefined
  error.value = ''
}
async function close() {
  if (busy.value) return
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
  </div>
  <Alert v-if="error && !open" role="alert" class="notice error">{{ error }}</Alert>
  <template v-if="spec">
    <div class="relative">
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
              v-for="node in stageNodes"
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
                <Button
                  type="button"
                  variant="outline"
                  :disabled="!enabled || busy || !!openingStage"
                  @click="edit(node.stage)"
                >
                  <LoadingIndicator :active="openingStage === node.stage" />
                  {{ openingStage === node.stage ? '正在读取…' : '记录' }}
                </Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>
  </template>
  <ModalDialog
    v-model="open"
    title="记录阶段进度"
    :busy="busy"
    :before-close="() => confirmLeave(dirty)"
  >
    <form class="form-stack" aria-label="阶段记录" @submit.prevent="save">
      <h3>{{ stages[draft.stage] }}</h3>
      <DraftNotice
        v-if="!submitting && !restoring"
        :status="drafts.status.value"
        :error="drafts.error.value"
        :drafts="drafts.recoverable.value"
        :conflict="drafts.recoveryConflict.value"
        @merge="drafts.mergeRecovered"
        @cancel-recovery="cancelRecovery"
        @restore="restoreDraft"
        @discard="drafts.discard"
      />
      <ConflictResolver
        v-if="conflict"
        :base="conflict.base"
        :local="{ ...draft }"
        :current="conflict.current"
        @resolve="merge"
        @close="conflict = undefined"
      />
      <fieldset class="plain-fieldset form-stack" :disabled="restoring || !!drafts.recoveryConflict.value">
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
      </fieldset>
      <p v-if="notice" role="status">{{ notice }}</p>
      <Alert v-if="error" role="alert" class="notice error">{{ error }}</Alert>
      <div class="form-actions">
        <Button type="button" variant="outline" :disabled="busy" @click="close">关闭</Button>
        <Button :disabled="busy || !dirty || !!conflict || !!drafts.recoveryConflict.value">
          <LoadingIndicator :active="submitting" />
          {{ submitting ? '正在保存…' : '保存阶段' }}
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
</style>
