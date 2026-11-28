<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { useWorkbench } from '@/hooks/useWorkbench'
import { loadSpecRunActivities, type RunActivity } from '@/services/workbench-agent'
import { getExternalLinks, type ExternalLink } from '@/services/workbench-engineering'
import { jobStatusLabels, objectValue, terminalJob } from '@/services/workbench-platform'

const props = defineProps<{ specId: string; goalVersion: string; specDigest: string }>()
const { externalVersion, sourceState } = useWorkbench()
const route = useRoute()
const activities = shallowRef<RunActivity[]>([])
const links = shallowRef<ExternalLink[]>([])
const busy = shallowRef(false)
const failed = shallowRef({ local: false, external: false })
const retries = shallowRef(0)
const local = computed(() => activities.value.filter(({ status }) => !terminalJob(status)))
const runningStates = new Set(['action_required', 'created', 'in_progress', 'manual', 'pending', 'preparing', 'queued', 'requested', 'running', 'scheduled', 'waiting_for_resource'])
const finishedStates = new Set(['canceled', 'cancelled', 'closed', 'completed', 'failed', 'failure', 'merged', 'neutral', 'skipped', 'stale', 'success', 'succeeded', 'timed_out'])
const external = computed(() => links.value.flatMap((link) => {
  if (link.kind === 'issue' || (link.goalVersion && link.goalVersion !== props.goalVersion)) return []
  const attribution = link.goalVersion ? '' : ' · 轮次待核实'
  const checks = ['checks', 'pipelines', 'jobs'].flatMap((key) => {
    const collection = objectValue(link.data?.[key])
    if (!Array.isArray(collection.items)) return []
    return collection.items.map((entry, index) => {
      const item = objectValue(entry)
      return {
        id: `${link.id}:${key}:${index}`,
        name: String(item.name ?? item.ref ?? item.id ?? key),
        state: String(item.conclusion || item.status || item.state || '')
      }
    })
  })
  const jobs = link.kind === 'pipeline' ? [{
    id: link.id,
    name: `${link.project} · CI #${link.number}`,
    state: String(link.data?.conclusion || link.data?.status || link.data?.state || '')
  }, ...checks] : checks
  if (link.status !== 'current') return [{
    id: link.id,
    name: `${link.project} · ${link.kind === 'pipeline' ? 'CI' : 'PR/MR'} #${link.number}`,
    status: '状态未知' + attribution
  }]
  return jobs
    .filter((job) => !finishedStates.has(job.state))
    .map((job) => ({
      ...job,
      status: (runningStates.has(job.state) ? (jobStatusLabels[job.state] ?? job.state) : '状态未知') + attribution
    }))
}))

watch([
  () => [props.specId, props.goalVersion, props.specDigest],
  () => sourceState.value?.activityVersions?.[props.specId],
  externalVersion, retries
], async (_, __, onCleanup) => {
  const request = new AbortController()
  onCleanup(() => request.abort())
  const signal = request.signal
  busy.value = true
  const [runs, associations] = await Promise.allSettled([
    loadSpecRunActivities(props.specId, props.goalVersion, props.specDigest, signal),
    getExternalLinks(signal)
  ])
  if (signal.aborted) return
  activities.value = runs.status === 'fulfilled' ? runs.value.items : []
  links.value = associations.status === 'fulfilled'
    ? associations.value.filter((link) => link.specId === props.specId)
    : []
  failed.value = { local: runs.status === 'rejected', external: associations.status === 'rejected' }
  busy.value = false
}, { immediate: true })
</script>

<template>
  <section aria-label="运行中的作业">
    <h3>运行中的作业</h3>
    <p v-if="busy" class="muted" role="status">正在读取作业状态…</p>
    <ul v-if="local.length || external.length" class="list-disc ps-4">
      <li v-for="job in local" :key="job.jobId">
        本机 Agent · {{ jobStatusLabels[job.status] ?? '状态未知' }} ·
        <RouterLink class="text-link" :to="'/jobs/' + encodeURIComponent(job.jobId)">查看执行</RouterLink>
      </li>
      <li v-for="job in external" :key="job.id">
        {{ job.name }} · {{ job.status }} ·
        <RouterLink class="text-link" :to="{ path: route.path, query: { ...route.query, tab: 'development' } }">查看研发关联</RouterLink>
      </li>
    </ul>
    <p v-if="failed.local || failed.external" class="warning-text" role="status">
      {{ [failed.local && '本机作业', failed.external && '外部作业'].filter(Boolean).join('、') }}状态未知。
      <Button type="button" variant="ghost" @click="retries++">重试读取作业</Button>
    </p>
    <p v-else-if="!busy && !local.length && !external.length" class="muted">当前没有已知运行中的作业。</p>
  </section>
</template>
