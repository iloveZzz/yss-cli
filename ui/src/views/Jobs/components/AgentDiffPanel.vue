<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { computed, onBeforeUnmount, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { Field, FieldLabel } from '@/components/ui/field'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import JobDisclosure from './JobDisclosure.vue'
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
const props = defineProps<{ runId: string; jobId: string; direct: boolean; running: boolean; visible: boolean }>()
const emit = defineEmits<{ accepted: [] }>()
const { snapshot } = useWorkbench()
const comparisonId = useId()
const diff = shallowRef<AgentDiff>()
const selected = shallowRef<string[]>([])
const expanded = shallowRef('')
const acknowledged = shallowRef<string[]>([])
const reading = shallowRef(false)
const accepting = shallowRef(false)
const readFailed = shallowRef(false)
const bindingBusy = shallowRef(false)
const busy = computed(() => reading.value || accepting.value || bindingBusy.value)
const canReview = computed(() => !!diff.value?.reviewToken && !readFailed.value)
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
function selectFile(file: AgentFile, checked: boolean) {
  const id = key(file)
  selected.value = checked ? [...selected.value, id] : selected.value.filter((item) => item !== id)
}
function acknowledgeFile(file: AgentFile, checked: boolean) {
  const id = key(file)
  acknowledged.value = checked
    ? [...acknowledged.value, id]
    : acknowledged.value.filter((item) => item !== id)
  selectFile(file, false)
}
let generation = 0
let acceptGeneration = 0
function clearReview() {
  diff.value = undefined
  selected.value = []
  acknowledged.value = []
  expanded.value = ''
  readFailed.value = false
}
async function refresh(resetSelection = false) {
  if (reading.value || (props.direct && props.running)) return
  const own = ++generation
  reading.value = true
  error.value = ''
  try {
    const value = await getAgentDiff(props.runId, props.jobId)
    if (own !== generation) return
    if (!value.reviewToken) throw new Error('服务端未提供审阅版本，请刷新工作台后重新读取差异。')
    if (resetSelection || diff.value?.reviewToken !== value.reviewToken) {
      selected.value = []
      acknowledged.value = []
    }
    if (!value.files.some((file) => key(file) === expanded.value)) expanded.value = ''
    diff.value = value
    readFailed.value = false
  } catch (cause) {
    if (own === generation) {
      error.value = errorMessage(cause)
      readFailed.value = true
    }
  } finally {
    if (own === generation) reading.value = false
  }
}
watch(
  () => [props.runId, props.jobId, props.running],
  () => {
    // A running view and a finished snapshot use different review baselines.
    generation++
    acceptGeneration++
    clearReview()
    error.value = ''
    reading.value = false
    accepting.value = false
    if (props.visible) void refresh()
  },
  { immediate: true }
)
watch(
  () => props.visible,
  (visible) => {
    if (visible && !diff.value && !reading.value) void refresh()
  }
)
onBeforeUnmount(() => {
  generation++
  acceptGeneration++
})
function bindingUpdated() {
  result.value = undefined
  clearReview()
  void refresh()
}
async function accept() {
  const reviewed = diff.value
  if (props.direct || busy.value || props.running || !canReview.value || !reviewed || !chosen.value.length) return
  const reviewedFiles = chosen.value
  const own = ++acceptGeneration
  accepting.value = true
  error.value = ''
  try {
    const value = await acceptAgentFiles(props.runId, reviewed.reviewToken, reviewedFiles, props.jobId)
    if (own !== acceptGeneration) return
    result.value = value
    emit('accepted')
    await refresh(true)
  } catch (cause) {
    if (own !== acceptGeneration) return
    if (cause instanceof ApiError && cause.status === 409) {
      await refresh(true)
      if (own !== acceptGeneration) return
      error.value = readFailed.value
        ? `审阅版本已变化，重新读取差异失败。${error.value}`
        : '审阅版本已变化，已重新读取差异。请核对目标分支与文件后重新选择。'
    } else error.value = errorMessage(cause)
  } finally {
    if (own === acceptGeneration) accepting.value = false
  }
}
</script>
<template>
  <section class="platform-section">
    <div class="section-heading">
      <div>
        <h2>{{ direct ? '运行期间的文件变化' : '变更文件' }}</h2>
      </div>
      <Button type="button" variant="outline" :aria-disabled="busy || (direct && running)" @click="!busy && refresh()">
        重新读取差异
      </Button>
    </div>
    <Alert v-if="error" variant="destructive" class="notice error">
      <AlertDescription class="text-current">
        <p>{{ error }}</p>
        <p v-if="readFailed && diff">{{ direct ? '当前显示上次读取的差异。' : '当前显示上次读取的差异，请重新读取后再接收文件。' }}</p>
      </AlertDescription>
    </Alert>
    <AgentRebindPanel
      v-if="!direct"
      :key="runId"
      :run-id="runId"
      :busy="reading || accepting || running"
      @busy="bindingBusy = $event"
      @rebound="bindingUpdated"
    />
    <LoadingSkeleton v-if="reading && !diff && !accepting" variant="table" :rows="4" label="正在读取变更文件" />
    <Alert v-if="running" role="status" class="notice">
      <AlertDescription class="text-current">{{ direct ? '运行结束后可查看运行期间的文件变化。' : '正在展示隔离工作区的变更。运行结束后可接收文件。' }}</AlertDescription>
    </Alert>
    <Card v-if="targets.length" class="accept-targets gap-2 py-4 shadow-none">
      <CardHeader class="px-4"><CardTitle>{{ direct ? '运行基线' : '接收目标' }}</CardTitle></CardHeader>
      <CardContent class="px-4">
        <dl>
          <div v-for="[root, baseline] in targets" :key="root">
            <dt>{{ root }}</dt>
            <dd>
              {{ baseline.branch ? baseline.branch.replace(/^refs\/heads\//, '') : '分离 HEAD' }}
              <span class="path-text">· HEAD {{ baseline.head.slice(0, 12) }}</span>
              <JobDisclosure>
                <template #label>完整 Git 基线</template>
                <pre>{{ readableValue(baseline) }}</pre>
              </JobDisclosure>
            </dd>
          </div>
        </dl>
      </CardContent>
    </Card>
    <div v-if="files.length" class="diff-toolbar">
      <span
        >{{ files.length }} 个文件<span v-if="!direct && conflicts">
          · {{ conflicts }} 项需要核对冲突</span
        ></span
      >
      <div v-if="!direct" class="inline-actions">
        <Button type="button" variant="ghost" :disabled="busy || running || !canReview" @click="selectSafeFiles"
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
    <div v-if="diff" class="diff-results">
      <div
        v-if="files.length"
        class="file-scroll"
        tabindex="0"
        role="region"
        aria-label="变更文件列表"
        :aria-busy="reading || accepting"
      >
        <Table class="files-table">
          <TableCaption class="sr-only">
            {{ direct ? '运行期间的文件变化' : '可审阅的文件变更与冲突状态' }}
          </TableCaption>
          <TableHeader>
            <TableRow>
              <TableHead v-if="!direct" scope="col" class="select-col">选择</TableHead>
              <TableHead scope="col">文件</TableHead>
              <TableHead scope="col">工程</TableHead>
              <TableHead scope="col">变更</TableHead>
              <TableHead v-if="!direct" scope="col">核对状态</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <template v-for="(file, index) in files" :key="key(file)">
              <TableRow>
                <TableCell v-if="!direct" class="select-col">
                  <Checkbox
                    :model-value="selected.includes(key(file))"
                    @update:model-value="selectFile(file, $event === true)"
                    :aria-label="`选择 ${file.path}`"
                    :disabled="
                      busy ||
                      running ||
                      !canReview ||
                      (file.conflict && !acknowledged.includes(key(file)))
                    "
                  />
                </TableCell>
                <TableHead scope="row">
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    class="file-trigger"
                    :aria-expanded="expanded === key(file)"
                    :aria-controls="`${comparisonId}-${index}`"
                    @click="expanded = expanded === key(file) ? '' : key(file)"
                  >
                    <span aria-hidden="true">{{ expanded === key(file) ? '▾' : '▸' }}</span>
                    <span>{{ file.path }}</span>
                  </Button>
                </TableHead>
                <TableCell>{{ projectName(file.projectId) }}</TableCell>
                <TableCell>{{ file.result ? (file.initial ? '修改' : '新增') : '删除' }}</TableCell>
                <TableCell v-if="!direct">
                  <Badge
                    :variant="file.conflict && !acknowledged.includes(key(file)) ? 'destructive' : 'secondary'"
                  >
                    {{ !file.conflict ? '无冲突' : acknowledged.includes(key(file)) ? '冲突已核对' : '有冲突' }}
                  </Badge>
                </TableCell>
              </TableRow>
              <TableRow v-show="expanded === key(file)" :id="`${comparisonId}-${index}`">
                <TableCell v-if="expanded === key(file)" :colspan="direct ? 3 : 5" class="comparison">
                  <div class="comparison-content">
                    <Alert v-if="!direct && file.conflict" role="note" class="notice warning">
                      <AlertDescription class="text-current">原工程与运行基线已有差异，请核对原工程当前内容和运行结果。</AlertDescription>
                    </Alert>
                    <p v-if="file.binary" class="muted">二进制文件 · SHA-256</p>
                    <div class="three-way">
                      <section>
                        <h3>运行起点</h3>
                        <pre>{{
                          (file.binary ? file.initial?.sha256 : file.initialText) ?? '文件不存在'
                        }}</pre>
                      </section>
                      <section v-if="!direct">
                        <h3>原工程当前内容</h3>
                        <pre>{{
                          (file.binary ? file.current?.sha256 : file.currentText) ?? '文件不存在'
                        }}</pre>
                      </section>
                      <section>
                        <h3>{{ direct ? '本轮结束' : '运行结果' }}</h3>
                        <pre>{{
                          (file.binary ? file.result?.sha256 : file.resultText) ?? '文件已删除'
                        }}</pre>
                      </section>
                    </div>
                    <Field v-if="!direct && file.conflict" orientation="horizontal" class="conflict-check">
                      <Checkbox
                        :id="`${comparisonId}-ack-${index}`"
                        :model-value="acknowledged.includes(key(file))"
                        :disabled="busy || running || !canReview"
                        @update:model-value="acknowledgeFile(file, $event === true)"
                      />
                      <FieldLabel :for="`${comparisonId}-ack-${index}`">
                        我已核对冲突，可用运行结果更新此文件
                      </FieldLabel>
                    </Field>
                  </div>
                </TableCell>
              </TableRow>
            </template>
          </TableBody>
        </Table>
      </div>
      <Empty v-if="!files.length" class="p-5">
        <EmptyDescription>{{ direct ? '本轮没有文件变更' : '当前没有可接收的文件差异' }}</EmptyDescription>
      </Empty>
      <LoadingIndicator :active="reading && !accepting" placement="overlay" label="正在重新读取差异" />
    </div>
    <div v-if="!direct" class="form-actions">
      <Button
        type="button"
        :disabled="busy || running || !canReview || !chosen.length"
        @click="accept"
      >
        <LoadingIndicator :active="accepting" />
        {{ accepting ? '正在接收…' : `接收选中文件（${chosen.length}）` }}
      </Button>
    </div>
    <section v-if="!direct && result" class="acceptance-result" aria-label="最近接收结果">
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
      <Alert v-if="objectValue(result).refreshError" class="notice warning">
        <AlertDescription class="text-current">
          文件接收已记录，Spec 刷新遇到问题。{{
            objectValue(objectValue(result).refreshError).message
          }}
        </AlertDescription>
      </Alert>
      <JobDisclosure class="result-details">
        <template #label>完整接收回执</template>
        <pre>{{ readableValue(result) }}</pre>
      </JobDisclosure>
    </section>
  </section>
</template>
<style scoped>
.section-heading button[aria-disabled='true'] {
  opacity: 0.65;
}
.accept-targets {
  margin: 18px 0;
  background: var(--brand-color-bg-layout);
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
.diff-results {
  position: relative;
}
.file-scroll {
  overflow: auto;
  max-height: 65vh;
  overscroll-behavior: contain;
  container-type: inline-size;
}
.files-table {
  width: 100%;
  min-width: 660px;
  border-spacing: 0;
  font-size: 13px;
}
.files-table th,
.files-table td {
  padding: 6px 10px;
  height: 40px;
  text-align: left;
  font-weight: 400;
  border-bottom: 1px solid var(--yss-colors-border);
}
.files-table thead th {
  position: sticky;
  top: 0;
  background: #f7f8fa;
  z-index: 1;
  color: var(--brand-color-text-secondary);
}
.files-table .select-col {
  width: 52px;
  text-align: center;
}
.files-table td {
  white-space: nowrap;
}
.file-trigger {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 8px;
  text-align: left;
  white-space: normal;
  overflow-wrap: anywhere;
  max-width: 100%;
  height: auto;
  min-height: 32px;
  cursor: pointer;
}
.files-table .comparison {
  white-space: normal;
  background: #f7f8fa;
  padding: 12px 16px;
}
.comparison-content {
  position: sticky;
  inset-inline-start: 16px;
  max-inline-size: min(100%, calc(100cqi - 32px));
}
.conflict-check {
  margin-top: 12px;
}
.conflict-check label {
  cursor: pointer;
  min-height: var(--yss-control-min-height);
  align-items: center;
}
@media (width < 720px) {
  .file-trigger {
    min-height: 44px;
  }
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
