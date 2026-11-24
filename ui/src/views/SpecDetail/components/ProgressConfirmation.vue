<script setup lang="ts">
import { Alert } from '@/components/ui/alert'
import { computed, shallowRef } from 'vue'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import ModalDialog from '@/components/ModalDialog.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  dateLabel,
  errorMessage,
  type ConfirmationInput,
  type ProgressSummary,
  type StageSummary
} from '@/services/workbench'
const props = defineProps<{
  specId: string
  progress: ProgressSummary
  stage: StageSummary
}>()
const emit = defineEmits<{ content: []; saved: [] }>()
const { snapshot, submit, saving } = useWorkbench()
const open = shallowRef(false)
const error = shallowRef('')
const notice = shallowRef('')
const input = shallowRef<ConfirmationInput>()
const title = computed(() => {
  if (props.stage.stage === 'spec') return '确认规格范围'
  return props.stage.stage === 'delivery' ? '确认最终交付' : '确认验收结果'
})
let revision = 0
function begin() {
  if (!props.progress.goalVersion || props.progress.materialReview) return
  input.value = {
    specId: props.specId,
    goalVersion: props.progress.goalVersion,
    stage: props.stage.stage,
    scope: props.progress.goal?.scope ?? '',
    evidenceDigest: props.progress.evidenceDigest,
    source: 'workbench:user-click',
    decision: 'confirm'
  }
  revision = snapshot.value?.revision ?? 0
  error.value = ''
  open.value = true
}
async function confirm() {
  if (!input.value) return
  error.value = ''
  try {
    await submit({ kind: 'confirmProgress', input: input.value }, revision)
    open.value = false
    notice.value =
      props.stage.stage === 'spec'
        ? '已记录当前目标规格范围确认。'
        : props.stage.stage === 'delivery'
          ? '已记录用户最终交付确认。'
          : '已记录用户验收确认。'
    emit('saved')
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}
</script>
<template>
  <p v-if="stage.confirmation" class="confirmation-record">
    {{
      stage.confirmation.decision === 'confirm' ? '用户已确认' : '用户已退回'
    }}
    · {{ dateLabel(stage.confirmation.recordedAt) }} ·
    {{ stage.confirmation.source }}
  </p>
  <p v-if="notice" role="status" class="confirmation-record">{{ notice }}</p>
  <div v-if="stage.confirmationRequired" class="confirmation-actions">
    <Button
      v-if="stage.stage === 'spec' || progress.materialReview"
      type="button"
      variant="outline"
      @click="emit('content')"
    >
      {{ progress.materialReview ? '查看待核实材料' : '查看内容与业务确认' }}
    </Button>
    <Button
      v-if="
        ['spec', 'verification', 'delivery'].includes(stage.stage) &&
        stage.confirmation?.decision !== 'confirm'
      "
      type="button"
      :disabled="
        saving ||
        progress.materialReview ||
        !['done', 'skipped'].includes(stage.progress) ||
        stage.validity !== 'current' ||
        !progress.goalVersion
      "
      @click="begin"
    >
      {{ title }}
    </Button>
    <span
      v-if="
        stage.stage !== 'spec' &&
        (!['done', 'skipped'].includes(stage.progress) ||
          stage.validity !== 'current')
      "
      class="muted"
    >
      当前结果仍需补充或核实凭证。
    </span>
  </div>
  <ModalDialog v-model="open" :title="title" :busy="saving">
    <form class="form-stack" @submit.prevent="confirm">
      <p>{{ input?.scope }}</p>
      <p>此次确认将绑定当前目标版本和凭证组合，并记录本次用户点击。</p>
      <code class="confirmation-version">{{ input?.goalVersion }}</code>
      <Alert v-if="error" role="alert" class="notice error">
        {{ error }} 请关闭后重新核对当前依据。
      </Alert>
      <div class="form-actions">
        <Button
          type="button"
          variant="outline"
          :disabled="saving"
          @click="open = false"
        >
          取消
        </Button>
        <Button :disabled="saving">
          <LoadingIndicator :active="saving" />
          {{ saving ? '正在确认…' : title }}
        </Button>
      </div>
    </form>
  </ModalDialog>
</template>
<style scoped>
.confirmation-record {
  margin-top: 12px;
  font-size: 12px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.confirmation-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 16px;
  font-size: 12px;
}
.confirmation-version {
  font-size: 11px;
  overflow-wrap: anywhere;
}
</style>
