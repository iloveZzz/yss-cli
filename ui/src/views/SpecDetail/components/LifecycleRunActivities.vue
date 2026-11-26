<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { useWorkbench } from '@/hooks/useWorkbench'
import { errorMessage, type Stage } from '@/services/workbench'
import { loadSpecRunActivities, type RunActivity } from '@/services/workbench-agent'
import { jobStatusLabels } from '@/services/workbench-platform'

const props = defineProps<{
  specId: string
  stage: Stage
  goalVersion: string
  specDigest: string
}>()
const { sourceState, snapshot } = useWorkbench()
const activities = shallowRef<RunActivity[]>([])
const error = shallowRef('')
const retry = shallowRef(0)
const version = computed(() => sourceState.value?.activityVersions?.[props.specId])
const stageActivities = computed(() => activities.value.filter((activity) => activity.stage === props.stage))
const latest = computed(() => stageActivities.value[0])
const names = computed(() => new Map(snapshot.value?.projects.map((project) => [project.record.id, project.record.name])))
const labels: Record<RunActivity['acceptance'][number]['status'], string> = {
  writing: '接收中',
  accepted: '已接收所选文件',
  noWrite: '本次未写入',
  failed: '接收失败',
  needsVerification: '接收待核实'
}
const jobStatus = (activity: RunActivity) => activity.status === 'completed' ? '作业已结束' : (jobStatusLabels[activity.status] ?? activity.status)

watch([version, retry], async ([current], _, onCleanup) => {
  error.value = ''
  if (!current) {
    activities.value = []
    return
  }
  const request = new AbortController()
  onCleanup(() => request.abort())
  try {
    const result = await loadSpecRunActivities(props.specId, props.goalVersion, props.specDigest, request.signal)
    if (!request.signal.aborted) activities.value = result.items
  } catch (cause) {
    if (!request.signal.aborted) error.value = errorMessage(cause)
  }
}, { immediate: true })
</script>

<template>
  <div v-if="error" class="run-activities-error">
    <p role="status">运行活动刷新失败 · {{ error }}</p>
    <Button type="button" size="sm" variant="ghost" @click="retry++">重试</Button>
  </div>
  <details v-if="latest" class="run-activities">
    <summary>运行活动 · {{ jobStatus(latest) }}<span v-if="stageActivities.length > 1"> · {{ stageActivities.length }} 轮</span></summary>
    <ul>
      <li v-for="activity in stageActivities" :key="activity.jobId">
        <div class="run-activity-row">
          <span>{{ jobStatus(activity) }}<template v-if="activity.readOnly"> · 只读分析</template><template v-if="activity.resultAvailable"> · 有可审阅成果</template></span>
          <RouterLink :to="'/jobs/' + encodeURIComponent(activity.jobId)">查看执行</RouterLink>
        </div>
        <ul v-if="activity.acceptance.length" class="run-acceptance">
          <li v-for="project in activity.acceptance" :key="project.projectId">
            {{ names.get(project.projectId) ?? project.projectId }} · {{ labels[project.status] }}
          </li>
        </ul>
      </li>
    </ul>
  </details>
</template>

<style scoped>
.run-activities, .run-activities-error { margin-block: 12px; font-size: 13px; }
.run-activities > ul { display: grid; gap: 8px; padding-inline-start: 1rem; margin-block-start: 8px; }
.run-activities summary { cursor: pointer; }
.run-activity-row, .run-activities-error { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.run-activity-row a { color: var(--yss-color-primary-control); text-decoration: underline; }
.run-acceptance { color: var(--brand-color-text-secondary); margin-block-start: 4px; }
</style>
