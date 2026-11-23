<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, shallowRef, useId, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import AgentCommandPermission from '@/components/AgentCommandPermission.vue'
import PiReadiness from '@/components/PiReadiness.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { startAgent } from '@/services/workbench-agent'
import { errorMessage, stages, type Stage } from '@/services/workbench'
import type { PiModel } from '@/services/workbench-platform'
const props = defineProps<{ specId: string; projectId: string; active: boolean; lifecycleStage?: Stage }>()
const router = useRouter()
const heading = useId()
const { snapshot } = useWorkbench()
const progress = computed(() => snapshot.value?.progress?.[props.specId])
const trackedStage = computed(() => {
  const stage = props.lifecycleStage ?? progress.value?.primaryStage
  return progress.value?.goalVersion && progress.value.stages.some((entry) => entry.stage === stage && entry.applicable)
    ? stage
    : undefined
})
const defaultPrompt = '请依据当前规格与已保存答复继续工作，并说明实际完成内容。'
const prompt = shallowRef(defaultPrompt)
const selected = shallowRef<string[]>([])
const carry = reactive<Record<string, string>>({})
const busy = shallowRef(false)
const allowCommands = shallowRef(false)
const error = shallowRef('')
const readyModel = shallowRef<PiModel>()
const primary = computed(() =>
  snapshot.value?.projects.find((project) => project.record.id === props.projectId)
)
const projects = computed(
  () =>
    snapshot.value?.projects.filter(
      (project) =>
        project.record.id !== props.projectId &&
        ((project.record.active && project.available) || selected.value.includes(project.record.id))
    ) ?? []
)
const projectIds = computed(() => [...new Set([props.projectId, ...selected.value])])
const participating = computed(() =>
  projectIds.value.flatMap((id) => {
    const project = snapshot.value?.projects.find((item) => item.record.id === id)
    return project ? [project] : []
  })
)
const unavailable = computed(() =>
  projectIds.value.some(
    (id) =>
      !snapshot.value?.projects.some(
        (project) => project.record.id === id && project.record.active && project.available
      )
  )
)
let generation = 0
onBeforeUnmount(() => {
  generation++
})
watch([() => props.specId, () => props.projectId], () => {
  generation++
  prompt.value = defaultPrompt
  selected.value = []
  for (const key of Object.keys(carry)) delete carry[key]
  error.value = ''
  busy.value = false
  allowCommands.value = false
})
async function run() {
  const expectedModel = readyModel.value
  if (busy.value || unavailable.value || !expectedModel) return
  const own = generation
  const specId = props.specId
  const ids = [...projectIds.value]
  busy.value = true
  error.value = ''
  try {
    const carryFiles = Object.fromEntries(
      ids.map((id) => [
        id,
        (carry[id] ?? '')
          .split('\n')
          .map((path) => path.trim())
          .filter(Boolean)
      ])
    )
    const job = await startAgent({
      specId, prompt: prompt.value, projectIds: ids, carryFiles, expectedModel,
      lifecycleStage: trackedStage.value, allowCommands: allowCommands.value
    })
    if (own === generation) await router.push({ path: `/jobs/${job.id}`, query: { run: job.id } })
  } catch (cause) {
    if (own === generation) error.value = errorMessage(cause)
  } finally {
    if (own === generation) busy.value = false
  }
}
</script>
<template>
  <section class="platform-section agent-launcher">
    <h2 :id="heading">运行 Pi Agent</h2>
    <p v-if="trackedStage" class="view-note muted">本次跟进 · {{ stages[trackedStage] }} · 已绑定当前交付目标</p>
    <form class="form-stack" :aria-labelledby="heading" @submit.prevent="run">
      <PiReadiness v-model:ready-model="readyModel" :active="active" :busy="busy" />
      <label>
        本次任务
        <textarea v-model="prompt" name="prompt" rows="4" required />
      </label>
      <section class="primary-project" aria-label="主规格所属工程">
        <div class="primary-label">
          <input type="checkbox" checked disabled aria-label="主规格工程始终参与" />
          <strong>{{ primary?.record.name ?? '主规格工程未登记' }}</strong>
          <span>主规格工程 · 始终参与</span>
        </div>
        <p class="path-text">{{ primary?.record.rootPath ?? projectId }}</p>
        <p v-if="!primary?.record.active || !primary?.available" class="warning-text">
          主工程当前停用或不可访问，请先在工程管理中核对登记路径。
        </p>
      </section>
      <details v-if="projects.length" class="optional-projects">
        <summary>
          其他参与工程{{ selected.length ? `（已选 ${selected.length}）` : '（可选）' }}
        </summary>
        <fieldset class="choice-fieldset">
          <legend class="sr-only">其他参与工程</legend>
          <label v-for="project in projects" :key="project.record.id" class="check-label">
            <input v-model="selected" type="checkbox" name="project" :value="project.record.id" />
            <span>
              {{ project.record.name }}
              <span v-if="!project.record.active || !project.available" class="warning-text">
                · 当前不可用，可取消选择
              </span>
              <span class="row-sub">{{ project.record.rootPath }}</span>
            </span>
          </label>
        </fieldset>
      </details>
      <p v-if="unavailable" class="notice warning">
        参与工程中存在停用或不可访问的路径，请核对主工程并调整其他参与工程。
      </p>
      <details class="carry-files">
        <summary>带入未提交文件（可选）</summary>
        <p class="muted">按工程填写需要带入的相对路径，每行一个。</p>
        <label v-for="project in participating" :key="project.record.id">
          {{ project.record.name }}
          <textarea
            v-model="carry[project.record.id]"
            :name="`carry-${project.record.id}`"
            rows="3"
            spellcheck="false"
            placeholder="src/example.ts"
          />
        </label>
      </details>
      <details class="run-help">
        <summary>运行方式与权限</summary>
        <p class="muted">
          参与工程需位于有提交的 Git 工作树。Pi 在隔离 worktree 中处理，完成后可逐文件预览并接收。模型与登录沿用本机 Pi 配置。
        </p>
      </details>
      <AgentCommandPermission
        v-model="allowCommands"
        :disabled="busy || !readyModel"
      />
      <p v-if="error" role="alert" class="notice error">{{ error }}</p>
      <div class="form-actions">
        <Button type="submit" :disabled="busy || unavailable || !readyModel">
          {{ busy ? '正在准备工作区…' : '运行 Pi Agent' }}
        </Button>
      </div>
    </form>
  </section>
</template>
<style scoped>
.primary-project {
  padding: 14px 16px;
  border: 1px solid #dce5fa;
  border-radius: 8px;
  background: #f7f9ff;
}
.primary-label {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.primary-label span {
  color: #717780;
  font-size: 11px;
}
.primary-project p {
  margin-top: 7px;
  font-size: 12px;
}
.optional-projects summary,
.carry-files summary,
.run-help summary {
  cursor: pointer;
  font-size: 12px;
  color: #62666c;
}
.optional-projects .choice-fieldset {
  margin-top: 10px;
}
.carry-files > p,
.run-help > p {
  margin: 10px 0;
  font-size: 12px;
}
.carry-files > label {
  margin-top: 12px;
}
.run-help > p {
  line-height: 1.8;
}
</style>
