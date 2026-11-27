<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { computed, onScopeDispose, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { errorMessage } from '@/services/workbench'
import { getActivityDetail, type JobActivity } from '@/services/workbench-platform'
import CommandTerminal from './CommandTerminal.vue'

const props = defineProps<{ jobId: string; row: JobActivity; commandOutput?: boolean; terminal?: boolean }>()
const detail = shallowRef<JobActivity>()
const commandDiagnostics = computed(() =>
  detail.value?.commandDiagnostics ?? props.row.commandDiagnostics
)
const error = shallowRef('')
const loading = shallowRef(false)
const followInitially = shallowRef(false)
const refreshInterval = 200
let activeRequest: AbortController | undefined
let refreshTimer: ReturnType<typeof setTimeout> | undefined
let refreshQueued = false
let refreshUrgent = false
let lastRefresh = -Infinity

function resetRefresh() {
  clearTimeout(refreshTimer)
  refreshTimer = undefined
  const previous = activeRequest
  activeRequest = undefined
  previous?.abort()
  refreshQueued = false
  refreshUrgent = false
  lastRefresh = -Infinity
  loading.value = false
}

function refresh() {
  if (!refreshQueued || activeRequest) return
  const wait = refreshUrgent ? 0 : refreshInterval - (performance.now() - lastRefresh)
  if (wait > 0) {
    refreshTimer ??= setTimeout(() => {
      refreshTimer = undefined
      refresh()
    }, wait)
    return
  }
  clearTimeout(refreshTimer)
  refreshTimer = undefined
  void loadDetail()
}

function queueRefresh(urgent: boolean) {
  refreshQueued = true
  refreshUrgent ||= urgent
  refresh()
}

async function loadDetail() {
  refreshQueued = false
  refreshUrgent = false
  lastRefresh = performance.now()
  const controller = new AbortController()
  activeRequest = controller
  loading.value = true
  try {
    const result = await getActivityDetail(props.jobId, props.row.id, controller.signal)
    if (activeRequest !== controller) return
    detail.value = result
    error.value = ''
  } catch (cause) {
    if (activeRequest === controller) error.value = errorMessage(cause)
  } finally {
    if (activeRequest === controller) {
      activeRequest = undefined
      loading.value = false
      refresh()
    }
  }
}

onScopeDispose(resetRefresh)
watch(
  [() => props.jobId, () => props.row.id, () => props.row.updatedEventId, () => props.row.status, () => props.terminal],
  ([jobId, id, , status, terminal], [previousJobId, previousId, , previousStatus, previousTerminal]) => {
    const changed = jobId !== previousJobId || id !== previousId
    if (changed) {
      resetRefresh()
      detail.value = undefined
      error.value = ''
      followInitially.value = status === 'inProgress' && !terminal
    }
    const finished = (status !== previousStatus && status !== 'inProgress') || (terminal && !previousTerminal)
    queueRefresh(changed || finished || props.row.kind !== 'command')
  },
  { immediate: true }
)
</script>
<template>
  <Alert v-if="error" variant="destructive" class="notice error">
    <AlertDescription class="text-current">
      <p>{{ error }}</p>
      <Button
        type="button"
        variant="ghost"
        :aria-disabled="loading"
        @click="!loading && queueRefresh(true)"
      >
        <LoadingIndicator :active="loading" />
        {{ loading ? '正在重试…' : '重试' }}
      </Button>
    </AlertDescription>
  </Alert>
  <p v-if="detail?.displayPath"><code>{{ detail.displayPath }}</code></p>
  <pre v-else-if="detail && (!commandOutput || detail.text !== row.text)">{{ detail.text || '暂无活动正文' }}</pre>
  <template v-if="commandDiagnostics?.length">
    <strong>命令诊断</strong>
    <ul class="command-diagnostics">
      <li v-for="diagnostic in commandDiagnostics" :key="diagnostic.code">
        <p>{{ diagnostic.message }}</p>
        <p v-if="diagnostic.hint && diagnostic.hint !== diagnostic.message" class="muted">{{ diagnostic.hint }}</p>
      </li>
    </ul>
  </template>
  <template v-if="detail">
    <CommandTerminal
      v-if="detail.kind === 'command'"
      :key="`${jobId}:${row.id}`"
      :row="detail"
      :terminal="terminal"
      :follow-initially="followInitially"
    />
    <Alert v-if="detail.error || detail.hint" variant="destructive" role="note" class="py-2" :data-error-code="detail.errorCode">
      <AlertDescription class="text-current">
        <p v-if="detail.error">{{ detail.error }}</p>
        <p v-if="detail.hint && detail.hint !== detail.error">{{ detail.hint }}</p>
      </AlertDescription>
    </Alert>
  </template>
  <LoadingSkeleton v-else-if="!error" variant="text" :rows="2" label="正在读取活动详情" />
</template>
<style scoped>
pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 24rem;
  overflow: auto;
  margin: 6px 0;
}
strong {
  display: block;
  margin-top: 12px;
}
.command-diagnostics {
  padding-inline-start: 20px;
  list-style-type: disc;
  overflow-wrap: anywhere;
}
.command-diagnostics li + li {
  margin-top: 4px;
}
</style>
