<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { getPiRuntime, testPiConnection } from '@/services/workbench-agent'
import { ApiError, errorMessage } from '@/services/workbench'
import type { PiModel, PiRuntimeStatus } from '@/services/workbench-platform'

const props = withDefaults(defineProps<{ active?: boolean; busy?: boolean; resumeRunId?: string }>(), {
  active: true
})
const readyModel = defineModel<PiModel>('readyModel')
const testDescription = useId()
const status = shallowRef<PiRuntimeStatus>()
const operation = shallowRef<'check' | 'test'>()
const checkError = shallowRef('')
const testResult = shallowRef<{ message: string; connected: boolean }>()
const model = computed(() => status.value?.available ? status.value.model : undefined)
const detail = computed(() =>
  checkError.value || status.value?.message || (status.value?.credentialsReady ? '已找到本机凭证。' : '')
)
let request: AbortController | undefined

async function checkRuntime() {
  if (request) return
  const current = new AbortController()
  request = current
  operation.value = 'check'
  checkError.value = ''
  testResult.value = undefined
  readyModel.value = undefined
  try {
    const result = await getPiRuntime(props.resumeRunId, current.signal)
    if (current.signal.aborted) return
    status.value = result
    if (result.available && result.model) readyModel.value = result.model
  } catch (cause) {
    if (current.signal.aborted) return
    status.value = undefined
    checkError.value = errorMessage(cause)
  } finally {
    if (request === current) {
      request = undefined
      operation.value = undefined
    }
  }
}

async function testConnection() {
  const target = model.value
  if (!target || request || props.busy) return
  const current = new AbortController()
  request = current
  operation.value = 'test'
  testResult.value = undefined
  readyModel.value = undefined
  try {
    await testPiConnection(target, props.resumeRunId, current.signal)
    if (current.signal.aborted) return
    testResult.value = { connected: true, message: '模型连接成功。' }
    readyModel.value = target
  } catch (cause) {
    if (!current.signal.aborted) {
      testResult.value = { connected: false, message: errorMessage(cause) }
      const issue = cause instanceof ApiError ? cause.context.piIssue : undefined
      if (!['subscription', 'credentials', 'modelAccess', 'modelUnavailable', 'modelChanged'].includes(String(issue))) {
        readyModel.value = target
      }
    }
  } finally {
    if (request === current) {
      request = undefined
      operation.value = undefined
    }
  }
}

watch([() => props.active, () => props.resumeRunId], ([active]) => {
  request?.abort()
  request = undefined
  operation.value = undefined
  status.value = undefined
  readyModel.value = undefined
  if (active) void checkRuntime()
}, { immediate: true })
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <div v-if="active" class="notice pi-readiness" :class="{ warning: checkError || status?.available === false || testResult?.connected === false }">
    <p class="model-label">
      <strong>{{ status?.version ? `Pi ${status.version}` : 'Pi' }}</strong>
      <span v-if="status?.model">{{ status.model.provider }} / {{ status.model.id }}</span>
    </p>
    <p v-if="operation === 'check'" class="muted">正在检查 Pi 版本、模型和本机凭证…</p>
    <p v-else-if="operation === 'test'" class="muted">正在请求模型…</p>
    <p role="status" aria-atomic="true">
      {{ testResult?.message || detail }}
    </p>
    <p v-if="model" :id="testDescription" class="muted">连接测试会发送一条简短请求，可能使用模型额度。</p>
    <div class="model-actions">
      <Button type="button" variant="outline" size="sm" :disabled="busy || !!operation" @click="checkRuntime">重新检测</Button>
      <Button
        v-if="model"
        type="button"
        variant="outline"
        size="sm"
        :aria-describedby="testDescription"
        :disabled="busy || !!operation"
        @click="testConnection"
      >测试模型连接</Button>
    </div>
  </div>
</template>

<style scoped>
.pi-readiness { display: grid; gap: 6px; }
.pi-readiness p { margin: 0; }
.model-label, .model-actions { display: flex; flex-wrap: wrap; gap: 6px 12px; }
.model-label span { min-width: 0; overflow-wrap: anywhere; }
.model-actions { margin-top: 4px; }
</style>
