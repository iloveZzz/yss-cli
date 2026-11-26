<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { errorMessage } from '@/services/workbench'
import { getActivityDetail, type JobActivity } from '@/services/workbench-platform'

const props = defineProps<{ jobId: string; row: JobActivity; commandOutput?: boolean }>()
const detail = shallowRef<JobActivity>()
const commandDiagnostics = computed(() =>
  detail.value?.commandDiagnostics ?? props.row.commandDiagnostics
)
const error = shallowRef('')
const retry = shallowRef(0)
const retrying = shallowRef(false)
function retryDetail() {
  if (retrying.value) return
  retrying.value = true
  retry.value++
}
watch(
  () => [props.jobId, props.row.id, props.row.updatedEventId, retry.value],
  async (_, __, onCleanup) => {
    const controller = new AbortController()
    onCleanup(() => controller.abort())
    try {
      const result = await getActivityDetail(props.jobId, props.row.id, controller.signal)
      if (!controller.signal.aborted) {
        detail.value = result
        error.value = ''
      }
    } catch (cause) {
      if (!controller.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (!controller.signal.aborted) retrying.value = false
    }
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
        :aria-disabled="retrying"
        @click="retryDetail"
      >
        <LoadingIndicator :active="retrying" />
        {{ retrying ? '正在重试…' : '重试' }}
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
    <template v-if="detail.kind === 'command'">
      <strong>命令输出</strong>
      <p v-if="commandOutput && detail.exitCode === 0" class="muted">退出码 0</p>
      <pre class="terminal-output" tabindex="0" aria-label="命令输出">{{ detail.output || (detail.status === 'inProgress' ? '等待命令输出…' : '暂无已留存的输出') }}</pre>
    </template>
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
.terminal-output {
  padding: 10px 12px;
  border-radius: 7px;
  background: #242526;
  color: #f0f0f0;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  overscroll-behavior: contain;
}
</style>
