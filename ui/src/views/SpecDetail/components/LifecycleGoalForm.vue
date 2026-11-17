<script setup lang="ts">
import { computed, reactive, shallowRef, watch, useId, toRaw } from 'vue'
import ModalDialog from '@/components/ModalDialog.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  ApiError,
  errorMessage,
  lifecycleStages,
  roleLabels,
  stages,
  type LifecycleGoalInput,
  type ParticipantRole,
  type ProgressSummary
} from '@/services/workbench'
const props = defineProps<{
  specId: string
  projectId: string
  specDigest: string | null
  progress?: ProgressSummary
  confirmLeave: (dirty: boolean) => boolean | Promise<boolean>
}>()
const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ dirty: [value: boolean]; saved: [] }>()
const { snapshot, submit, saving } = useWorkbench()
const id = useId()
const error = shallowRef('')
const stale = shallowRef(false)
const notice = shallowRef('')
const acceptanceText = shallowRef('')
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
  Object.assign(
    draft,
    goal
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
  revision = snapshot.value?.revision ?? 0
  baseline.value = JSON.stringify([draft, acceptanceText.value])
  error.value = ''
  notice.value = ''
  stale.value = false
}
watch(open, (value) => {
  if (value) reload()
})
watch(dirty, (value) => emit('dirty', value), { flush: 'sync' })
async function save() {
  if (stale.value || saving.value) return
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
</script>
<template>
  <ModalDialog
    v-model="open"
    title="本次交付目标"
    :busy="saving"
    :before-close="() => confirmLeave(dirty)"
  >
    <form class="form-stack goal-form" @submit.prevent="save">
      <p class="muted">
        目标版本将绑定规格内容、适用阶段、参与工程和验收依据。
      </p>
      <label :for="id + '-scope'">
        交付范围
        <textarea :id="id + '-scope'" v-model="draft.scope" required rows="3" />
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
            <input v-model="entry.applicable" type="checkbox" />
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
        <textarea
          :id="id + '-acceptance'"
          v-model="acceptanceText"
          rows="3"
          placeholder="每行一个验收条目或引用"
        />
      </label>
      <p v-if="notice" role="status">{{ notice }}</p>
      <p v-if="error" class="notice error" role="alert">{{ error }}</p>
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
          :disabled="saving"
          @click="
            async () => {
              if (await confirmLeave(dirty)) open = false
            }
          "
        >
          取消
        </Button>
        <Button :disabled="saving || stale">保存目标</Button>
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
