<script setup lang="ts">
import { computed, shallowRef, useId, useTemplateRef, watch } from 'vue'
import ModalDialog from '@/components/ModalDialog.vue'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Alert } from '@/components/ui/alert'
import { useWorkbench } from '@/hooks/useWorkbench'
import { targetLabel, type ProgressSummary } from '@/services/workbench'
import type { useLifecycleGoalChange } from '../hooks/useLifecycleGoalChange'
import LifecycleGoalRecovery from './LifecycleGoalRecovery.vue'
import LifecycleCancellationActivities from './LifecycleCancellationActivities.vue'

const props = defineProps<{
  specId: string
  progress?: ProgressSummary
  change: ReturnType<typeof useLifecycleGoalChange>
  canCancel: boolean
  recoverInput?: boolean
  focusFallback: () => HTMLElement | null
}>()
const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ saved: [] }>()
const { snapshot, refresh, saving } = useWorkbench()
const id = useId()
const reasonField = useTemplateRef<InstanceType<typeof Textarea>>('reason')
const reason = shallowRef('')
const reasonError = shallowRef('')
const basis = shallowRef<{ version: string; revision: number; goal: ProgressSummary['goal'] }>()
const pending = computed(() => props.change.pending.value.find(({ body }) => body.action.kind === 'cancelLifecycleGoal'))
const busy = computed(() => saving.value || props.change.busy.value || pending.value?.status === 'sending')
const changed = computed(() => !!basis.value && (!props.canCancel || basis.value.version !== props.progress?.goalVersion))
const needsReview = computed(() => changed.value || props.change.conflict.value)

function setBasis() {
  const version = props.progress?.goalVersion
  if (version && snapshot.value) basis.value = { version, revision: snapshot.value.revision, goal: props.progress?.goal ?? null }
}

watch(open, (value) => {
  if (!value) return
  props.change.reset(props.recoverInput && !pending.value)
  reasonError.value = ''
  basis.value = undefined
  const previous = pending.value?.body ?? (props.recoverInput ? props.change.rejected.value?.body : undefined)
  const original = previous?.action
  if (previous && original?.kind === 'cancelLifecycleGoal') {
    const version = original.input.goalVersion
    basis.value = {
      version,
      revision: previous.expectedRevision,
      goal: props.progress?.goalVersion === version ? props.progress.goal : null
    }
    reason.value = original.input.reason
  } else {
    setBasis()
    reason.value = ''
  }
})

async function review() {
  if (busy.value || !await refresh() || changed.value) return
  setBasis()
  props.change.reset()
}

async function cancelGoal() {
  if (!basis.value || needsReview.value || busy.value || pending.value) return
  if (!reason.value.trim()) {
    reasonError.value = '请填写取消原因。'
    reasonField.value?.focus()
    return
  }
  if (await props.change.submit({
    kind: 'cancelLifecycleGoal',
    input: { specId: props.specId, goalVersion: basis.value.version, reason: reason.value.trim() }
  }, basis.value.revision)) closeSaved()
}

function closeSaved() {
  open.value = false
  emit('saved')
}

function discard() {
  const rejected = props.change.rejected.value?.body
  if (rejected?.action.kind !== 'cancelLifecycleGoal') return
  props.change.discard(rejected.requestId)
  open.value = false
}
</script>

<template>
  <ModalDialog v-model="open" title="取消本轮目标" :busy="busy" :focus-fallback="focusFallback">
    <form class="form-stack" novalidate @submit.prevent="cancelGoal">
      <p v-if="basis" class="break-all">
        目标版本 {{ basis.version }}
        <template v-if="basis.goal"> · {{ targetLabel(basis.goal.target) }} · {{ basis.goal.scope }}</template>
      </p>
      <p class="muted">取消后保留本轮范围、成果与检查。运行中的作业通过各自入口处理。</p>
      <LifecycleCancellationActivities
        v-if="open && basis?.goal && !pending"
        :spec-id="specId"
        :goal-version="basis.version"
        :spec-digest="basis.goal.specDigest"
      />
      <label :for="id + '-reason'">
        取消原因
        <Textarea
          :id="id + '-reason'"
          ref="reason"
          v-model="reason"
          required autofocus rows="3"
          :readonly="busy || !!pending"
          :aria-invalid="!!reasonError"
          :aria-describedby="reasonError ? id + '-reason-error' : undefined"
          @update:model-value="reasonError = ''"
        />
      </label>
      <p :id="id + '-reason-error'" role="alert" class="warning-text">{{ reasonError }}</p>
      <p role="status">{{ busy ? '正在核对或提交本次操作…' : '' }}</p>
      <Alert v-if="change.error.value" role="alert" class="notice error">{{ change.error.value }}</Alert>
      <p v-if="!pending && changed" role="status" class="notice warning">当前轮次已变化，本次输入已保留。</p>
      <Button v-if="!pending && change.conflict.value && !changed" type="button" variant="outline" :disabled="busy" @click="review">重新核对目标</Button>
      <LifecycleGoalRecovery v-if="pending?.status === 'uncertain'" :change="change" kind="cancelLifecycleGoal" :on-accepted="closeSaved" />
      <div class="form-actions">
        <Button type="button" variant="outline" :disabled="busy" @click="open = false">关闭</Button>
        <Button v-if="recoverInput && !pending" type="button" variant="ghost" :disabled="busy" @click="discard">丢弃未保存输入</Button>
        <Button v-if="!pending" type="submit" variant="destructive" :disabled="busy || needsReview">确认取消本轮</Button>
      </div>
    </form>
  </ModalDialog>
</template>
