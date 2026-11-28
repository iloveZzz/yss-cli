<script setup lang="ts">
import { Alert } from '@/components/ui/alert'
import { Checkbox } from '@/components/ui/checkbox'
import { Textarea } from '@/components/ui/textarea'
import { computed, reactive, shallowRef, watch, useId, toRaw } from 'vue'
import ModalDialog from '@/components/ModalDialog.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { Input } from '@/components/ui/input'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  ApiError,
  errorMessage,
  lifecycleStages,
  roleLabels,
  stages,
  targetLabel,
  type LifecycleGoalInput,
  type ParticipantRole,
  type ProgressSummary
} from '@/services/workbench'
import type { useLifecycleGoalChange } from '../hooks/useLifecycleGoalChange'
import LifecycleGoalRecovery from './LifecycleGoalRecovery.vue'
const props = defineProps<{
  specId: string
  projectId: string
  specDigest: string | null
  progress?: ProgressSummary
  round?: { change: ReturnType<typeof useLifecycleGoalChange>; canReopen: boolean }
  recoverInput?: boolean
  focusFallback: () => HTMLElement | null
  confirmLeave: (dirty: boolean) => boolean | Promise<boolean>
}>()
const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ dirty: [value: boolean]; saved: [] }>()
const { snapshot, submit, saving, refresh } = useWorkbench()
const id = useId()
const error = shallowRef('')
const stale = shallowRef(false)
const notice = shallowRef('')
const acceptanceText = shallowRef('')
const basisVersion = shallowRef<string>()
const previousScope = shallowRef('')
const pending = computed(() => props.round?.change.pending.value.find(({ body }) => body.action.kind === 'reopenLifecycleGoal'))
const busy = computed(() => saving.value || !!props.round?.change.busy.value || pending.value?.status === 'sending')
const changedRound = computed(() => !!props.round &&
  (!props.round.canReopen || basisVersion.value !== props.progress?.goalVersion))
const changedDigest = computed(() => !!props.round && !!draft.specDigest && draft.specDigest !== props.specDigest)
const canReviewRound = computed(() => !!props.round && !changedRound.value && !!props.specDigest &&
  (props.round.change.conflict.value || changedDigest.value))
const draft = reactive<LifecycleGoalInput>({
  specId: props.specId,
  scope: '',
  target: { kind: 'local', label: '', reference: '' },
  stages: [],
  participants: [],
  acceptanceRefs: [],
  specDigest: ''
})
let revision = 0
const baseline = shallowRef('')
const dirty = computed(
  () =>
    open.value &&
    JSON.stringify([draft, acceptanceText.value]) !== baseline.value
)
function reload() {
  const goal = props.progress?.goal
  const previous = pending.value?.body ?? (props.recoverInput ? props.round?.change.rejected.value?.body : undefined)
  const original = previous?.action
  basisVersion.value = original?.kind === 'reopenLifecycleGoal' ? original.input.goalVersion : props.progress?.goalVersion ?? undefined
  previousScope.value = basisVersion.value === goal?.goalVersion ? goal?.scope ?? '' : ''
  Object.assign(
    draft,
    original?.kind === 'reopenLifecycleGoal'
      ? structuredClone(original.input.goal)
      : goal
      ? {
          specId: props.specId,
          scope: goal.scope,
          target: structuredClone(goal.target),
          stages: lifecycleStages.map((stage) =>
            structuredClone(
              goal.stages.find((entry) => entry.stage === stage) ?? {
                stage,
                applicable: false,
                reason: '未纳入当前目标'
              }
            )
          ),
          participants: structuredClone(goal.participants),
          acceptanceRefs: [...goal.acceptanceRefs],
          specDigest: props.specDigest ?? ''
        }
      : {
          specId: props.specId,
          scope: '',
          target: { kind: 'local', label: '', reference: '' },
          stages: lifecycleStages.map((stage) => ({
            stage,
            applicable: true,
            reason: ''
          })),
          participants: [
            { projectId: props.projectId, role: 'spec', label: '' }
          ],
          acceptanceRefs: [],
          specDigest: props.specDigest ?? ''
        }
  )
  acceptanceText.value = draft.acceptanceRefs.join('\n')
  revision = previous?.expectedRevision ?? snapshot.value?.revision ?? 0
  baseline.value = JSON.stringify([draft, acceptanceText.value])
  error.value = ''
  notice.value = ''
  stale.value = false
  props.round?.change.reset(props.recoverInput && !pending.value)
}
watch(open, (value) => {
  if (value) reload()
})
watch(dirty, (value) => emit('dirty', value), { flush: 'sync' })
async function save() {
  if (stale.value || busy.value || pending.value || changedRound.value || canReviewRound.value) return
  notice.value = ''
  error.value = ''
  if (!draft.stages.some((stage) => stage.applicable)) {
    error.value = '请至少选择一个适用阶段。'
    return
  }
  if (!draft.specDigest) {
    error.value = '当前规格内容尚未读取，请刷新后重试。'
    return
  }
  try {
    const input: LifecycleGoalInput = {
      ...structuredClone(toRaw(draft)),
      acceptanceRefs: acceptanceText.value
        .split('\n')
        .map((value) => value.trim())
        .filter(Boolean)
    }
    input.participants = input.participants.map((participant) => ({
      ...participant,
      label:
        participant.label.trim() ||
        snapshot.value?.projects.find(
          (project) => project.record.id === participant.projectId
        )?.record.name ||
        participant.projectId
    }))
    const submitted = JSON.stringify([draft, acceptanceText.value])
    if (props.round && basisVersion.value) {
      if (await props.round.change.submit({
        kind: 'reopenLifecycleGoal',
        input: { specId: props.specId, goalVersion: basisVersion.value, goal: input }
      }, revision)) {
        baseline.value = submitted
        closeSaved()
      }
      return
    }
    const result = await submit({ kind: 'setLifecycleGoal', input }, revision)
    revision = result.revision
    baseline.value = submitted
    if (JSON.stringify([draft, acceptanceText.value]) === submitted)
      open.value = false
    else notice.value = '目标已保存，新增输入已保留。'
    emit('saved')
  } catch (cause) {
    error.value = errorMessage(cause)
    stale.value = cause instanceof ApiError && cause.status === 409
  }
}
async function reviewRound() {
  if (busy.value || !await refresh() || changedRound.value || !props.specDigest) return
  draft.specDigest = props.specDigest
  revision = snapshot.value?.revision ?? revision
  props.round?.change.reset()
  notice.value = '已读取当前规格版本，本次输入已保留。请再次核对后提交。'
}
function closeSaved() {
  open.value = false
  emit('saved')
}

function discard() {
  const rejected = props.round?.change.rejected.value?.body
  if (rejected?.action.kind !== 'reopenLifecycleGoal') return
  props.round?.change.discard(rejected.requestId)
  open.value = false
}
</script>
<template>
  <ModalDialog
    v-model="open"
    :title="round ? '开启新一轮' : '本次交付目标'"
    :busy="busy"
    :before-close="() => round ? true : confirmLeave(dirty)"
    :focus-fallback="focusFallback"
  >
    <form class="form-stack goal-form" @submit.prevent="save">
      <p v-if="round" class="muted break-all" tabindex="-1" autofocus>
        原目标版本 {{ basisVersion }}
        <template v-if="previousScope"> · {{ previousScope }}</template>
        <br />
        本轮采用规格摘要 {{ draft.specDigest || '待核实' }}
      </p>
      <p v-else class="muted">
        目标版本将绑定规格内容、适用阶段、参与工程和验收依据。
      </p>
      <p v-if="pending" class="break-all">
        提交范围 · {{ draft.scope }}<br />
        交付终点 · {{ targetLabel(draft.target) }}<br />
        参与工程 · {{ draft.participants.map(({ label, projectId }) => label || projectId).join('、') }}
      </p>
      <fieldset v-else class="goal-fields form-stack" :disabled="!!round && busy">
        <label :for="id + '-scope'">
          交付范围
          <Textarea :id="id + '-scope'" v-model="draft.scope" required rows="3" />
        </label>
        <div class="form-grid">
          <label :for="id + '-kind'">
            交付位置
            <SingleSelect
              :id="id + '-kind'"
              :model-value="draft.target.kind"
              :options="[
                { value: 'local', label: '本地产物' },
                { value: 'merged', label: '合并代码' },
                { value: 'environment', label: '指定环境' }
              ]"
              @update:model-value="
                draft.target.kind = $event as LifecycleGoalInput['target']['kind']
              "
            />
          </label>
          <label :for="id + '-target'">
            目标说明
            <Input :id="id + '-target'" v-model="draft.target.label" required />
          </label>
        </div>
        <label :for="id + '-reference'">
          目标引用
          <Input
            :id="id + '-reference'"
            :model-value="draft.target.reference ?? ''"
            @update:model-value="draft.target.reference = String($event)"
            placeholder="产物路径、目标分支或环境地址"
            :required="draft.target.kind === 'environment'"
          />
        </label>
        <fieldset>
          <legend>适用阶段</legend>
          <div
            v-for="entry in draft.stages"
            :key="entry.stage"
            class="scope-entry"
          >
            <label class="check-label">
              <Checkbox :model-value="entry.applicable" @update:model-value="entry.applicable = $event === true" />
              {{ stages[entry.stage] }}
            </label>
            <Input
              v-if="!entry.applicable"
              v-model="entry.reason"
              required
              :aria-label="stages[entry.stage] + '不适用原因'"
              placeholder="不适用原因"
            />
          </div>
        </fieldset>
        <fieldset>
          <legend>参与工程</legend>
          <div
            v-for="(participant, index) in draft.participants"
            :key="index"
            class="participant-entry"
          >
            <label :for="`${id}-project-${index}`">
              工程
              <SingleSelect
                :id="`${id}-project-${index}`"
                v-model="participant.projectId"
                :options="
                  (snapshot?.projects ?? []).map((project) => ({
                    value: project.record.id,
                    label: project.record.name
                  }))
                "
              />
            </label>
            <label :for="`${id}-role-${index}`">
              角色
              <SingleSelect
                :id="`${id}-role-${index}`"
                :model-value="participant.role"
                :options="
                  Object.entries(roleLabels).map(([value, label]) => ({
                    value,
                    label
                  }))
                "
                @update:model-value="participant.role = $event as ParticipantRole"
              />
            </label>
            <Button
              type="button"
              variant="ghost"
              :aria-label="'移除参与工程' + (index + 1)"
              @click="draft.participants.splice(index, 1)"
            >
              移除
            </Button>
          </div>
          <Button
            type="button"
            variant="outline"
            @click="
              draft.participants.push({ projectId, role: 'backend', label: '' })
            "
          >
            添加参与工程
          </Button>
        </fieldset>
        <label :for="id + '-acceptance'">
          验收引用
          <Textarea
            :id="id + '-acceptance'"
            v-model="acceptanceText"
            rows="3"
            placeholder="每行一个验收条目或引用"
          />
        </label>
      </fieldset>
      <p v-if="notice" role="status">{{ notice }}</p>
      <p v-if="round" role="status">{{ busy ? '正在核对或提交本次操作…' : '' }}</p>
      <Alert v-if="error" class="notice error" role="alert">{{ error }}</Alert>
      <Alert v-if="round?.change.error.value" class="notice error" role="alert">{{ round.change.error.value }}</Alert>
      <p v-if="!pending && changedRound" class="notice warning" role="status">
        当前轮次或可用性已变化，本次输入已保留。
      </p>
      <p v-else-if="!pending && changedDigest" class="notice warning" role="status">
        规格版本已变化，请重新读取并核对采用的版本。
      </p>
      <Button v-if="!pending && canReviewRound" type="button" variant="outline" :disabled="busy" @click="reviewRound">
        重新核对本轮输入
      </Button>
      <LifecycleGoalRecovery v-if="pending?.status === 'uncertain' && round" :change="round.change" kind="reopenLifecycleGoal" :on-accepted="closeSaved" />
      <p v-if="stale" class="notice warning">
        目标或工作台记录已变化，当前输入已保留。
        <Button type="button" variant="outline" @click="reload">
          载入最新目标
        </Button>
      </p>
      <div class="form-actions">
        <Button
          type="button"
          variant="outline"
          :disabled="busy"
          @click="
            async () => {
              if (round || await confirmLeave(dirty)) open = false
            }
          "
        >
          取消
        </Button>
        <Button v-if="recoverInput && !pending" type="button" variant="ghost" :disabled="busy" @click="discard">丢弃未保存输入</Button>
        <Button v-if="!pending" type="submit" :disabled="busy || stale || changedRound || canReviewRound">
          <LoadingIndicator :active="busy" />
          {{ busy ? '正在保存…' : round ? '确认开启新一轮' : '保存目标' }}
        </Button>
      </div>
    </form>
  </ModalDialog>
</template>
<style scoped>
fieldset {
  padding: 12px;
  border: 1px solid var(--yss-colors-border);
  border-radius: 6px;
  min-width: 0;
}
.goal-fields {
  padding: 0;
  border: 0;
}
legend {
  padding: 0 4px;
  font-size: 12px;
}
.scope-entry {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 8px;
}
.scope-entry > label {
  min-width: 70px;
}
.scope-entry > input {
  flex: 1;
  min-width: 160px;
}
.participant-entry {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 120px auto;
  gap: 8px;
  align-items: end;
  margin-bottom: 12px;
}
.goal-form {
  min-width: 0;
}
@media (width < 600px) {
  .participant-entry {
    grid-template-columns: minmax(0, 1fr);
  }
  .participant-entry > button {
    justify-self: start;
  }
}
</style>
