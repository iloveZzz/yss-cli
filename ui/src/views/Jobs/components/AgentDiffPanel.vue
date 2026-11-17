<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import {
  getAgentDiff,
  acceptAgentFiles,
  type AgentFile,
  type AgentDiff
} from '@/services/workbench-agent'
import { ApiError, errorMessage } from '@/services/workbench'
import { objectValue, readableValue } from '@/services/workbench-platform'
import { useWorkbench } from '@/hooks/useWorkbench'
import AgentRebindPanel from './AgentRebindPanel.vue'
const props = defineProps<{ runId: string; running?: boolean }>()
const emit = defineEmits<{ accepted: [] }>()
const { snapshot } = useWorkbench()
const diff = shallowRef<AgentDiff>()
const selected = shallowRef<string[]>([])
const acknowledged = shallowRef<string[]>([])
const loading = shallowRef(false)
const bindingBusy = shallowRef(false)
const busy = computed(() => loading.value || bindingBusy.value)
const error = shallowRef('')
const result = shallowRef<unknown>()
const key = (file: AgentFile) => `${file.projectId}:${file.path}`
const files = computed(() => diff.value?.files ?? [])
const chosen = computed(() => files.value.filter((file) => selected.value.includes(key(file))))
const targets = computed(() => Object.entries(diff.value?.gitBaselines ?? {}))
const conflicts = computed(() => files.value.filter((file) => file.conflict).length)
const resultProjects = computed(() => {
  const projects = objectValue(result.value).projects
  return Array.isArray(projects) ? projects.map(objectValue) : []
})
function projectName(id: string) {
  return snapshot.value?.projects.find((project) => project.record.id === id)?.record.name ?? id
}
function selectSafeFiles() {
  selected.value = files.value.filter((file) => !file.conflict).map(key)
}
let generation = 0
async function refresh() {
  const own = ++generation
  loading.value = true
  error.value = ''
  selected.value = []
  acknowledged.value = []
  diff.value = undefined
  try {
    const value = await getAgentDiff(props.runId)
    if (own !== generation) return
    if (!value.reviewToken) throw new Error('服务端未提供审阅版本，请刷新工作台后重新读取差异。')
    diff.value = value
  } catch (cause) {
    if (own === generation) error.value = errorMessage(cause)
  } finally {
    if (own === generation) loading.value = false
  }
}
watch(
  () => [props.runId, props.running],
  () => {
    if (!props.running) void refresh()
  },
  { immediate: true }
)
onBeforeUnmount(() => {
  generation++
})
function bindingUpdated() {
  result.value = undefined
  void refresh()
}
async function accept() {
  const reviewed = diff.value
  if (busy.value || props.running || !reviewed?.reviewToken || !chosen.value.length) return
  const reviewedFiles = chosen.value
  const runId = props.runId
  loading.value = true
  error.value = ''
  try {
    const value = await acceptAgentFiles(runId, reviewed.reviewToken, reviewedFiles)
    if (runId !== props.runId) return
    result.value = value
    emit('accepted')
    await refresh()
  } catch (cause) {
    if (runId !== props.runId) return
    if (cause instanceof ApiError && cause.status === 409) {
      await refresh()
      error.value = `审阅版本已变化，已重新读取差异。请核对目标分支与文件后重新选择。${error.value ? ` ${error.value}` : ''}`
    } else error.value = errorMessage(cause)
  } finally {
    if (runId === props.runId) loading.value = false
  }
}
</script>
<template>
  <section class="platform-section">
    <div class="section-heading">
      <div>
        <h2>审阅与接收文件</h2>
        <p class="muted">选择已核对的文件，将结果接收到原工程。</p>
      </div>
      <Button type="button" variant="outline" :disabled="busy" @click="refresh">
        重新读取差异
      </Button>
    </div>
    <p v-if="error" role="alert" class="notice error">{{ error }}</p>
    <AgentRebindPanel
      :key="runId"
      :run-id="runId"
      :busy="loading || running"
      @busy="bindingBusy = $event"
      @rebound="bindingUpdated"
    />
    <p v-if="loading" role="status" class="muted">正在读取或接收文件…</p>
    <p v-if="running && !diff" class="muted">
      运行结束后可审阅并接收成果。也可手动读取当前工作区差异。
    </p>
    <section v-if="targets.length" class="accept-targets" aria-label="接收目标工程">
      <h3>接收目标</h3>
      <dl>
        <div v-for="[root, baseline] in targets" :key="root">
          <dt>{{ root }}</dt>
          <dd>
            {{ baseline.branch ? baseline.branch.replace(/^refs\/heads\//, '') : '分离 HEAD' }}
            <span class="path-text">· HEAD {{ baseline.head.slice(0, 12) }}</span>
            <details>
              <summary>完整 Git 基线</summary>
              <pre>{{ readableValue(baseline) }}</pre>
            </details>
          </dd>
        </div>
      </dl>
    </section>
    <div v-if="files.length" class="diff-toolbar">
      <span
        >{{ files.length }} 个文件<span v-if="conflicts">
          · {{ conflicts }} 项需要核对冲突</span
        ></span
      >
      <div class="inline-actions">
        <Button type="button" variant="ghost" :disabled="busy || running" @click="selectSafeFiles"
          >选择无冲突文件</Button
        >
        <Button
          type="button"
          variant="ghost"
          :disabled="busy || !selected.length"
          @click="selected = []"
          >清空选择</Button
        >
      </div>
    </div>
    <article v-for="file in files" :key="key(file)" class="diff-file">
      <div class="section-heading">
        <label class="check-label">
          <input
            v-model="selected"
            type="checkbox"
            :value="key(file)"
            :disabled="
              busy ||
              running ||
              !diff?.reviewToken ||
              (file.conflict && !acknowledged.includes(key(file)))
            "
          />
          <strong>{{ file.path }}</strong>
        </label>
        <span class="muted">{{ file.result ? (file.initial ? '修改' : '新增') : '删除' }}</span>
      </div>
      <p class="file-project muted">{{ projectName(file.projectId) }}</p>
      <p v-if="file.conflict" class="notice warning">
        原工程与运行基线已有差异，请同时核对当前原文件和运行结果。
      </p>
      <div v-if="file.binary" class="diff-binary">
        <p>二进制文件</p>
        <p class="path-text">原文件 {{ file.current?.sha256 ?? '不存在' }}</p>
        <p class="path-text">运行结果 {{ file.result?.sha256 ?? '删除' }}</p>
      </div>
      <details v-else>
        <summary>展开文件对照</summary>
        <div class="three-way">
          <section>
            <h3>运行起点</h3>
            <pre>{{ file.initialText ?? '文件不存在' }}</pre>
          </section>
          <section>
            <h3>原工程当前内容</h3>
            <pre>{{ file.currentText ?? '文件不存在' }}</pre>
          </section>
          <section>
            <h3>运行结果</h3>
            <pre>{{ file.resultText ?? '文件已删除' }}</pre>
          </section>
        </div>
      </details>
      <label v-if="file.conflict" class="check-label">
        <input
          v-model="acknowledged"
          type="checkbox"
          :value="key(file)"
          @change="selected = selected.filter((item) => item !== key(file))"
        />
        我已核对冲突，可用运行结果更新此文件
      </label>
    </article>
    <p v-if="diff && !files.length" class="empty">当前没有可接收的文件差异</p>
    <div class="form-actions">
      <Button
        type="button"
        :disabled="busy || running || !diff?.reviewToken || !chosen.length"
        @click="accept"
      >
        接收选中文件（{{ chosen.length }}）
      </Button>
    </div>
    <section v-if="result" class="acceptance-result" aria-label="最近接收结果">
      <h3>最近接收结果</h3>
      <p
        v-for="(project, index) in resultProjects"
        :key="index"
        :class="{ 'warning-text': project.status === 'failed' }"
      >
        {{
          projectName(
            String(
              project.projectId ?? (Array.isArray(project.projectIds) ? project.projectIds[0] : '')
            )
          )
        }}
        ·
        {{
          project.status === 'failed'
            ? '接收失败'
            : project.status === 'alreadyApplied'
              ? '此前已接收'
              : '已接收'
        }}
        <span v-if="objectValue(project.error).message">
          · {{ objectValue(project.error).message }}</span
        >
      </p>
      <p v-if="objectValue(result).refreshError" class="notice warning">
        文件接收已记录，Spec 刷新遇到问题。{{
          objectValue(objectValue(result).refreshError).message
        }}
      </p>
      <details class="result-details">
        <summary>完整接收回执</summary>
        <pre>{{ readableValue(result) }}</pre>
      </details>
    </section>
  </section>
</template>
<style scoped>
.accept-targets {
  margin: 18px 0;
  padding: 16px;
  background: #f7f8fa;
  border-radius: 8px;
}
.accept-targets dl {
  margin: 8px 0 0;
}
.accept-targets dl > div + div {
  margin-top: 12px;
}
.accept-targets dt {
  font-size: 12px;
  overflow-wrap: anywhere;
}
.accept-targets dd {
  margin: 4px 0 0;
  color: #62666c;
  overflow-wrap: anywhere;
}
.diff-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding-block: 10px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.file-project {
  margin: 4px 0 10px;
  font-size: 12px;
}
.acceptance-result {
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid var(--yss-colors-border);
}
.acceptance-result h3 {
  font-size: 14px;
  margin-bottom: 10px;
}
.acceptance-result p {
  overflow-wrap: anywhere;
}
</style>
