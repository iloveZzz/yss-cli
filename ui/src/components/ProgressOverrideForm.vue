<script setup lang="ts">
import { computed, shallowRef, watch, useId } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { useEventListener } from '@vueuse/core'
import ModalDialog from './ModalDialog.vue'
import SingleSelect from './SingleSelect.vue'
import { Button } from './ui/button'
import { Alert } from './ui/alert'
import { Field, FieldLabel } from './ui/field'
import { Textarea } from './ui/textarea'
import LoadingIndicator from './LoadingIndicator.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  ApiError,
  errorMessage,
  progressLabels,
  stages,
  type Progress,
  type ProgressSummary,
  type Stage
} from '@/services/workbench'
const props = defineProps<{
  specId: string
  progress?: ProgressSummary
  stage: Stage
  projectId?: string
  initialProgress?: Progress
  confirmLeave?: (dirty: boolean) => boolean | Promise<boolean>
}>()
const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ saved: []; dirty: [value: boolean] }>()
const { snapshot, submit, saving } = useWorkbench()
const id = useId()
const value = shallowRef<Progress>('doing')
const reason = shallowRef('')
const baseline = shallowRef<{ progress: Progress; reason: string }>({
  progress: 'doing',
  reason: ''
})
const dirty = computed(
  () =>
    open.value &&
    (value.value !== baseline.value.progress ||
      reason.value !== baseline.value.reason)
)
const error = shallowRef('')
const submitting = shallowRef(false)
const notice = shallowRef('')
const leaveOpen = shallowRef(false)
let resolveLeave: ((value: boolean) => void) | undefined
const stale = shallowRef(false)
const goalVersion = shallowRef('')
let revision = 0
function reload() {
  value.value =
    props.initialProgress ??
    props.progress?.stages.find((stage) => stage.stage === props.stage)
      ?.progress ??
    'doing'
  reason.value = ''
  baseline.value = { progress: value.value, reason: '' }
  error.value = ''
  notice.value = ''
  stale.value = false
  revision = snapshot.value?.revision ?? 0
  goalVersion.value = props.progress?.goalVersion ?? ''
}
watch(
  open,
  (isOpen) => {
    if (isOpen) reload()
    else emit('dirty', false)
  },
  { immediate: true }
)
watch(dirty, (isDirty) => emit('dirty', isDirty), { flush: 'sync' })
function decideLeave(value: boolean) {
  leaveOpen.value = false
  resolveLeave?.(value)
  resolveLeave = undefined
}
function requestLeave(dirty: boolean): boolean | Promise<boolean> {
  if (props.confirmLeave) return props.confirmLeave(dirty)
  if (!dirty) return true
  if (resolveLeave) return false
  leaveOpen.value = true
  return new Promise<boolean>((resolve) => {
    resolveLeave = resolve
  })
}
async function close() {
  if (await requestLeave(dirty.value)) open.value = false
}
const guardNavigation = () =>
  props.confirmLeave ? true : requestLeave(dirty.value)
onBeforeRouteLeave(guardNavigation)
onBeforeRouteUpdate(guardNavigation)
useEventListener(window, 'beforeunload', (event) => {
  if (!props.confirmLeave && dirty.value) {
    event.preventDefault()
    event.returnValue = ''
  }
})
async function save() {
  if (stale.value || saving.value) return
  error.value = ''
  notice.value = ''
  if (!goalVersion.value) {
    error.value = '请先录入本次交付目标。'
    return
  }
  if (!reason.value.trim()) {
    error.value = '请填写人工更正原因。'
    return
  }
  submitting.value = true
  try {
    const submitted = { reason: reason.value, progress: value.value }
    const result = await submit(
      {
        kind: 'setProgressOverride',
        input: {
          specId: props.specId,
          goalVersion: goalVersion.value,
          stage: props.stage,
          progress: value.value,
          reason: reason.value,
          projectId: props.projectId
        }
      },
      revision
    )
    revision = result.revision
    baseline.value = submitted
    if (reason.value === submitted.reason && value.value === submitted.progress)
      open.value = false
    else notice.value = '更正已保存，新增输入已保留。'
    emit('saved')
  } catch (cause) {
    error.value = errorMessage(cause)
    stale.value = cause instanceof ApiError && cause.status === 409
  } finally {
    submitting.value = false
  }
}
</script>
<template>
  <ModalDialog
    v-model="open"
    title="人工更正进展"
    :busy="saving"
    :before-close="() => requestLeave(dirty)"
  >
    <form class="form-stack" :aria-busy="submitting" @submit.prevent="save">
      <p>
        更正 {{ stages[stage] }} 阶段，绑定目标版本
        <code class="version">{{ goalVersion || '尚未建立' }}</code>
        。
      </p>
      <p class="muted">
        自动依据继续保留。可在生命周期详情清除更正，恢复自动汇总。
      </p>
      <Field>
        <FieldLabel :for="id + '-progress'">更正为</FieldLabel>
        <SingleSelect
          :id="id + '-progress'"
          :model-value="value"
          :options="
            Object.entries(progressLabels).map(([value, label]) => ({
              value,
              label
            }))
          "
          @update:model-value="value = $event as Progress"
        />
      </Field>
      <Field>
        <FieldLabel :for="id + '-reason'">更正原因</FieldLabel>
        <Textarea :id="id + '-reason'" v-model="reason" required rows="4" />
      </Field>
      <p v-if="notice" role="status">{{ notice }}</p>
      <Alert v-if="error" class="notice error" variant="destructive">{{ error }}</Alert>
      <Alert v-if="stale" class="notice warning">
        依据已变化，当前输入已保留。
        <Button type="button" variant="outline" @click="reload">
          载入最新版本
        </Button>
      </Alert>
      <RouterLink
        v-if="!goalVersion"
        :to="{ path: '/specs/' + specId, query: { tab: 'lifecycle' } }"
      >
        前往设置交付目标 →
      </RouterLink>
      <div class="form-actions">
        <Button
          type="button"
          variant="outline"
          :disabled="saving"
          @click="close"
        >
          取消
        </Button>
        <Button type="submit" :disabled="saving || stale || !goalVersion">
          <LoadingIndicator :active="submitting" />
          {{ submitting ? '保存中…' : '保存更正' }}
        </Button>
      </div>
    </form>
  </ModalDialog>
  <ModalDialog
    v-model="leaveOpen"
    title="离开当前编辑？"
    :before-close="
      () => {
        decideLeave(false)
        return true
      }
    "
  >
    <p>当前编辑包含未保存输入，继续操作将离开编辑。</p>
    <div class="form-actions">
      <Button type="button" variant="outline" autofocus @click="decideLeave(false)">
        继续编辑
      </Button>
      <Button type="button" @click="decideLeave(true)">离开并继续</Button>
    </div>
  </ModalDialog>
</template>
<style scoped>
.version {
  overflow-wrap: anywhere;
  font-size: 11px;
}
</style>
