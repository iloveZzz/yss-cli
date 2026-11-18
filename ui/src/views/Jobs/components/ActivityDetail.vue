<script setup lang="ts">
import { shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { errorMessage } from '@/services/workbench'
import { getActivityDetail, type JobActivity } from '@/services/workbench-platform'

const props = defineProps<{ jobId: string; row: JobActivity; commandOutput?: boolean }>()
const detail = shallowRef<JobActivity>()
const error = shallowRef('')
const retry = shallowRef(0)
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
    }
  },
  { immediate: true }
)
</script>
<template>
  <p v-if="error" role="alert">
    {{ error }} <Button variant="ghost" @click="retry++">重试</Button>
  </p>
  <template v-if="detail">
    <pre v-if="!commandOutput || detail.text !== row.text">{{ detail.text || '暂无活动正文' }}</pre>
    <template v-if="detail.kind === 'command'">
      <strong>命令输出</strong>
      <p v-if="commandOutput && detail.exitCode !== null" class="muted">退出码 {{ detail.exitCode }}</p>
      <pre>{{ detail.output || '暂无已留存的输出' }}</pre>
    </template>
    <p v-if="detail.error" role="alert">{{ detail.error }}</p>
  </template>
  <p v-else-if="!error" role="status">正在读取活动详情…</p>
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
</style>
