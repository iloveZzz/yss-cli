<script setup lang="ts">
import { computed, nextTick, onScopeDispose, shallowRef, useTemplateRef, watch } from 'vue'
import { useSessionStorage } from '@vueuse/core'
import { FileDiff, FileText, RotateCw } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { ApiError, errorMessage } from '@/services/workbench'
import { requestId } from '@/services/workbench-platform'
import {
  acceptAgentFiles, getAgentAcceptance, getAgentDiff, verifyAgentAcceptance,
  type AcceptanceDisposition, type AgentAcceptance, type AgentDiff, type AgentFile,
  type AgentRun, type ReviewExcerpt
} from '@/services/workbench-agent'
import AgentRebindPanel from '@/views/Jobs/components/AgentRebindPanel.vue'
import RunArtifacts from '@/views/Jobs/components/RunArtifacts.vue'
import PiDiffFile from './PiDiffFile.vue'
import PiFileTree from './PiFileTree.vue'

const props = defineProps<{
  runId: string
  jobId: string
  latestId: string
  running: boolean
  specId: string
  run?: AgentRun
  projectNames: Record<string, string>
  focusFile?: { projectId: string; path: string }
}>()
const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{
  cite: [excerpt: ReviewExcerpt]; changed: []; latest: []; closed: [];
  reviewState: [state: { busy: boolean; pending: boolean }]
}>()
const diff = shallowRef<AgentDiff>()
const acceptance = shallowRef<AgentAcceptance | null>()
const expanded = shallowRef('')
const selected = shallowRef<string[]>([])
const error = shallowRef('')
const acceptanceError = shallowRef('')
const relatedJob = shallowRef('')
const notice = shallowRef('')
const reading = shallowRef(false)
const submitting = shallowRef(false)
const verifying = shallowRef(false)
const uncertainAttempts = useSessionStorage<Record<string, string>>('yss:pi-acceptance-requests', {}, {
  writeDefaults: false, flush: 'sync'
})
const scopeKey = computed(() => JSON.stringify([props.runId, props.jobId]))
const uncertainAttempt = computed(() => uncertainAttempts.value[scopeKey.value])
const uncertain = computed(() => !!uncertainAttempt.value)
const needsRefresh = shallowRef(false)
const body = useTemplateRef<HTMLElement>('body')
const fileTree = useTemplateRef<InstanceType<typeof PiFileTree>>('fileTree')
const tab = shallowRef('files')
const editable = computed(() => !props.running && props.jobId === props.latestId && diff.value?.jobId === props.jobId && !diff.value.historical)
const blocked = new Set<AcceptanceDisposition>(['recoveryRequired', 'committedPendingCleanup', 'unverified', 'writing'])
const selectedFiles = computed(() => diff.value?.files.filter((file) => selected.value.includes(key(file))) ?? [])
const labels: Record<AcceptanceDisposition, string> = {
  applied: '已接收', noWrite: '未发生写入', rolledBack: '已回滚', recoveryRequired: '需恢复',
  committedPendingCleanup: '已提交待清理', unverified: '结果待核实', verified: '已核对实际文件', writing: '正在接收'
}
const needsVerification = computed(() => uncertain.value || acceptance.value?.projects.some((report) =>
  blocked.has(report.disposition) && report.disposition !== 'writing'))
const busy = computed(() => submitting.value || verifying.value)
const latestPending = computed(() => !!uncertainAttempts.value[JSON.stringify([props.runId, props.latestId])])
watch([busy, latestPending], ([busy, pending]) => emit('reviewState', { busy, pending }), { immediate: true })
let sequence = 0
let acceptanceRead: AbortController | undefined
onScopeDispose(() => acceptanceRead?.abort())

function key(file: { projectId: string; path: string }) {
  return JSON.stringify([file.projectId, file.path])
}
function clearAttempt(scope: string, attempt: string) {
  if (uncertainAttempts.value[scope] === attempt) delete uncertainAttempts.value[scope]
}
function projectDisabled(file: AgentFile) {
  return !!acceptance.value?.projects.some((report) =>
    (report.projectId === file.projectId || report.root === file.gitRoot) && blocked.has(report.disposition))
}
function showAcceptance(value: AgentAcceptance | null) {
  acceptance.value = value
  selected.value = selectedFiles.value.filter((file) => !projectDisabled(file)).map(key)
}
function setSelected(file: AgentFile, checked: boolean) {
  selected.value = checked ? [...new Set([...selected.value, key(file)])] : selected.value.filter((item) => item !== key(file))
}
async function revealFile() {
  const target = props.focusFile
  if (!target || !diff.value) return
  const index = diff.value.files.findIndex((file) => key(file) === key(target))
  if (index < 0) {
    notice.value = '本轮成果中没有这个文件的差异。'
    return
  }
  const scope = scopeKey.value
  tab.value = 'files'
  expanded.value = key(target)
  await nextTick()
  if (scope !== scopeKey.value || !open.value || props.focusFile !== target) return
  fileTree.value?.reveal(target)
  await nextTick()
  if (scope !== scopeKey.value || !open.value || props.focusFile !== target) return
  const entry = body.value?.querySelector<HTMLElement>('[data-review-file="' + index + '"]')
  entry?.focus({ preventScroll: true })
  entry?.scrollIntoView({ block: 'nearest' })
}

async function read(clearError = true, signal?: AbortSignal) {
  const own = ++sequence
  const run = props.runId
  const job = props.jobId
  if (!run || !job) return
  reading.value = true
  if (clearError) {
    error.value = ''
    notice.value = ''
  }
  try {
    const data = await getAgentDiff(run, job, signal)
    if (own !== sequence || signal?.aborted) return
    if (data.reviewToken !== diff.value?.reviewToken) selected.value = []
    diff.value = data
    needsRefresh.value = false
    await revealFile()
  } catch (cause) {
    if (own === sequence && !signal?.aborted) {
      error.value = errorMessage(cause)
      needsRefresh.value = true
      selected.value = []
    }
  } finally {
    if (own === sequence) reading.value = false
  }
}

async function readAcceptance() {
  const run = props.runId
  const job = props.jobId
  if (!run || job !== props.latestId) return
  const scope = scopeKey.value
  const attempt = uncertainAttempt.value
  acceptanceRead?.abort()
  const request = new AbortController()
  acceptanceRead = request
  try {
    const value = await getAgentAcceptance(run, attempt, request.signal)
    if (request.signal.aborted || scope !== scopeKey.value || job !== props.latestId) return
    acceptanceError.value = ''
    if (attempt) {
      if (value?.attemptId !== attempt) return
      if (!value.projects.some((report) => report.disposition === 'writing')) {
        clearAttempt(scope, attempt)
        error.value = ''
        if (value.notReceived) notice.value = '本次接收未受理，请重新核对后提交。'
      }
    }
    if (!value?.notReceived) showAcceptance(value)
  } catch (cause) {
    if (!request.signal.aborted && scope === scopeKey.value) acceptanceError.value = errorMessage(cause)
  } finally {
    if (acceptanceRead === request) acceptanceRead = undefined
  }
}

watch([open, () => props.runId, () => props.jobId, () => props.running, () => props.latestId], ([visible, run, job], previous, onCleanup) => {
  acceptanceRead?.abort()
  if (run !== previous?.[1] || job !== previous?.[2]) {
    sequence++
    diff.value = undefined
    acceptance.value = undefined
    expanded.value = ''
    selected.value = []
    error.value = ''
    acceptanceError.value = ''
    relatedJob.value = ''
    notice.value = ''
    needsRefresh.value = false
    reading.value = false
    tab.value = 'files'
  }
  if (!visible) {
    selected.value = []
    return
  }
  const request = new AbortController()
  onCleanup(() => request.abort())
  void read(true, request.signal)
  void readAcceptance()
}, { immediate: true })
watch(() => props.focusFile, () => {
  if (open.value) void revealFile()
})

async function accept() {
  const data = diff.value
  if (!data || !editable.value || busy.value || needsRefresh.value || !selectedFiles.value.length || uncertain.value) return
  submitting.value = true
  acceptanceRead?.abort()
  error.value = ''
  relatedJob.value = ''
  notice.value = ''
  const run = props.runId
  const job = props.jobId
  const scope = scopeKey.value
  const attempt = requestId()
  uncertainAttempts.value[scope] = attempt
  try {
    const received = await acceptAgentFiles(run, data.reviewToken, selectedFiles.value, job, attempt)
    clearAttempt(scope, attempt)
    if (scope !== scopeKey.value) return
    showAcceptance(received)
    notice.value = received.refreshError?.message ?? ''
    selected.value = []
    await read(false)
  } catch (cause) {
    const rejected = cause instanceof ApiError && cause.status >= 400 && cause.status < 500 &&
      cause.status !== 408 && !cause.context.acceptanceUnverified
    if (rejected) clearAttempt(scope, attempt)
    if (scope !== scopeKey.value) return
    relatedJob.value = cause instanceof ApiError && cause.context.runId !== run &&
      typeof cause.context.jobId === 'string' ? cause.context.jobId : ''
    error.value = rejected ? errorMessage(cause) : '接收结果待核实。'
    needsRefresh.value = true
    selected.value = []
    if (!rejected) await readAcceptance()
  } finally {
    submitting.value = false
    emit('changed')
  }
}

async function verify() {
  if (busy.value) return
  verifying.value = true
  acceptanceRead?.abort()
  acceptanceError.value = ''
  const run = props.runId
  const job = props.jobId
  const scope = scopeKey.value
  const attempt = uncertainAttempt.value
  try {
    const result = await verifyAgentAcceptance(run, job, attempt)
    if (attempt && result?.attemptId === attempt) clearAttempt(scope, attempt)
    if (scope !== scopeKey.value) return
    if (result?.notReceived) {
      notice.value = '本次接收未受理，请重新核对后提交。'
      await readAcceptance()
    } else {
      showAcceptance(result)
    }
    error.value = ''
    await read(false)
  } catch (cause) {
    if (run === props.runId && job === props.jobId) acceptanceError.value = errorMessage(cause)
  } finally {
    verifying.value = false
    emit('changed')
  }
}
function dismiss() {
  emit('closed')
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="pi-review-dialog" @close-auto-focus.prevent="dismiss">
      <DialogHeader>
        <DialogTitle>执行成果</DialogTitle>
        <DialogDescription>轮次 {{ jobId }}<template v-if="jobId !== latestId"> · 历史快照</template></DialogDescription>
      </DialogHeader>
      <div ref="body" class="pi-review-body">
        <Tabs v-model="tab">
          <TabsList aria-label="执行成果类型">
            <TabsTrigger value="files"><FileDiff :size="15" /> 文件变更</TabsTrigger>
            <TabsTrigger value="artifacts"><FileText :size="15" /> 产物</TabsTrigger>
          </TabsList>
          <TabsContent value="files" class="pi-review-files">
            <div class="pi-review-actions">
              <Button type="button" size="sm" variant="outline" :disabled="reading || busy" @click="read(); readAcceptance()">
                <RotateCw v-if="!reading" :size="14" /><LoadingIndicator :active="reading" />重新核对
              </Button>
              <Button v-if="jobId !== latestId" type="button" size="sm" variant="ghost" @click="emit('latest')">查看最新成果</Button>
            </div>
            <p v-if="diff" class="pi-file-basis">核对版本 {{ diff.reviewToken.slice(0, 10) }}</p>
            <p v-if="error" class="pi-issue" role="alert">
              {{ error }}
              <RouterLink v-if="relatedJob && relatedJob !== jobId" :to="'/jobs/' + relatedJob">查看相关轮次</RouterLink>
            </p>
            <p v-if="notice" class="pi-file-basis" role="status">{{ notice }}</p>
            <p v-if="acceptanceError" class="pi-issue" role="status">{{ acceptanceError }}</p>
            <LoadingSkeleton v-if="reading && !diff" variant="text" :rows="4" label="正在读取成果差异" />
            <div v-if="acceptance?.projects.length || needsVerification" class="pi-acceptance" aria-label="接收结果">
              <div v-for="report in acceptance?.projects" :key="report.projectId" class="pi-acceptance-row">
                <strong>{{ projectNames[report.projectId] || report.projectId }} · {{ labels[report.disposition] }}</strong>
                <p v-if="report.jobId && report.jobId !== jobId">接收记录来自轮次 {{ report.jobId }}</p>
                <p v-if="report.error">{{ report.error.message }}</p>
                <p v-if="report.verificationError">{{ report.verificationError }}</p>
                <p v-if="report.transaction?.id">事务 {{ report.transaction.id }}</p>
                <ul v-if="report.observed?.length">
                  <li v-for="file in report.observed" :key="file.path">
                    {{ file.path }} · {{ file.matchesResult ? '与隔离结果一致' : file.matchesBefore ? '仍是接收前内容' : '原工程已有其他变更' }}
                  </li>
                </ul>
                <RouterLink v-if="['committedPendingCleanup', 'recoveryRequired', 'unverified'].includes(report.disposition)"
                  :to="{ path: '/settings', query: { section: 'engineering', operation: 'recover', root: report.root } }">
                  检查或恢复该 Git 工程
                </RouterLink>
              </div>
              <Button v-if="needsVerification" type="button" size="sm" variant="outline" :disabled="busy" @click="verify">
                <LoadingIndicator :active="verifying" />核实文件与事务
              </Button>
            </div>
            <PiFileTree v-if="diff?.files.length" :key="scopeKey" ref="fileTree" :files="diff.files" :project-names="projectNames">
              <template #file="{ file, index }">
                <div class="pi-review-file">
                  <button type="button" :data-review-file="index" :aria-expanded="expanded === key(file)"
                    class="pi-review-file-toggle" :title="file.path"
                    @click="expanded = expanded === key(file) ? '' : key(file)">
                    <FileDiff :size="16" />
                    <span>{{ file.path.split('/').at(-1) }}</span>
                    <small>{{ file.accepted ? '已接收' : file.conflict ? '冲突' : file.contentMatches ? '无需写入' : selected.includes(key(file)) ? '已选择' : '' }}</small>
                  </button>
                  <PiDiffFile v-if="diff && expanded === key(file)" :key="diff.reviewToken" :file="file" :diff="diff"
                    :disabled="reading || busy || uncertain || needsRefresh || projectDisabled(file)" :editable="editable"
                    :selected="selected.includes(key(file))" @select="setSelected(file, $event)" @cite="emit('cite', $event)" />
                </div>
              </template>
            </PiFileTree>
            <p v-else-if="diff" class="pi-file-basis">本轮没有文件变更。</p>
            <AgentRebindPanel v-if="error && run?.resumeIssue && !running && latestId === jobId" :run-id="runId"
              @rebound="read(); emit('changed')" />
            <details v-if="run?.rebindings?.length" class="pi-rebind-history">
              <summary>查看旧绑定的接收记录</summary>
              <div v-for="item in run.rebindings" :key="item.id">
                <p>{{ item.createdAt }}</p>
                <ul><li v-for="(file, index) in item.previousAppliedFiles" :key="index">
                  {{ projectNames[file.projectId] || file.projectId }} / {{ file.path }} · {{ file.acceptedAt }}
                </li></ul>
              </div>
            </details>
          </TabsContent>
          <TabsContent value="artifacts">
            <RunArtifacts v-if="runId && jobId" :key="jobId" :run-id="runId" :spec-id="specId" :running="running && latestId === jobId" :job-id="jobId" />
          </TabsContent>
        </Tabs>
      </div>
      <footer v-if="tab === 'files' && editable" class="pi-review-footer">
        <span>{{ selectedFiles.length }} 个文件已选</span>
        <Button type="button" :disabled="!selectedFiles.length || reading || busy || needsRefresh || uncertain" @click="accept">
          <LoadingIndicator :active="submitting" />接收所选文件
        </Button>
      </footer>
    </DialogContent>
  </Dialog>
</template>
