<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Button } from '@/components/ui/button'
import { dateLabel } from '@/services/workbench'
import { terminalJob, type LocalProjectTaskJob } from '@/services/workbench-platform'
import ActivityDetail from '@/views/Jobs/components/ActivityDetail.vue'
import JobStatus from '@/views/Jobs/components/JobStatus.vue'
import { useJobEvents } from '@/views/Jobs/hooks/useJobEvents'

const props = defineProps<{ job: LocalProjectTaskJob; projectName: string }>()
const emit = defineEmits<{ changed: [] }>()
const logOpen = shallowRef(false)
const { latest, error, retry } = useJobEvents(computed(() => logOpen.value ? props.job.id : ''), () => emit('changed'))
const command = computed(() => latest.value?.items.find((item) => item.kind === 'command'))
const checkout = computed(() => props.job.localProjectTask.expectedCheckout)
</script>

<template>
  <li class="grid min-w-0 gap-1 border-t border-border py-3 [overflow-wrap:anywhere]">
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
      <strong>{{ job.localProjectTask.action === 'test' ? '本机测试' : '本机构建' }}</strong>
      <JobStatus :status="job.status" role="status" />
      <time :datetime="job.createdAt" class="ml-auto text-xs text-muted-foreground">{{ dateLabel(job.createdAt) }}</time>
    </div>
    <p class="text-muted-foreground">
      {{ projectName }} · <code v-if="job.localProjectTask.directory">{{ job.localProjectTask.directory }}</code>
      <span v-else>工程根目录</span>
    </p>
    <p v-if="checkout" class="text-xs text-muted-foreground">
      <code>{{ checkout.rootPath }}</code> · {{ checkout.branch ?? '游离 HEAD' }} ·
      <code :title="checkout.head">{{ checkout.head.slice(0, 12) }}</code>
    </p>
    <p v-if="job.result?.exitCode != null">退出码 {{ job.result.exitCode }}</p>
    <p v-if="job.result?.terminationSignal">
      终止信号 {{ job.result.terminationSignal.name || job.result.terminationSignal.number }}
    </p>
    <p v-if="job.result?.nodeVersion && job.result.pnpmVersion" class="text-muted-foreground">
      Node.js {{ job.result.nodeVersion }} · pnpm {{ job.result.pnpmVersion }}
    </p>
    <p v-if="job.error" class="pi-issue">{{ job.error.message }}</p>
    <template v-if="job.result?.artifactPaths?.length">
      <p>产物路径（相对于工程根目录）</p>
      <ul aria-label="产物路径" class="list-disc ps-5">
        <li v-for="path in job.result.artifactPaths" :key="path"><code>{{ path }}</code></li>
      </ul>
    </template>
    <p v-else-if="job.localProjectTask.action === 'build' && job.result?.exitCode === 0" class="text-muted-foreground">
      本次未核实到新增或更新的 <code>dist</code> 产物。
    </p>
    <details @toggle="logOpen = ($event.currentTarget as HTMLDetailsElement).open">
      <summary class="w-fit cursor-pointer rounded-sm py-1">命令日志</summary>
      <div v-if="logOpen" data-workbench-reveal>
        <div v-if="error" role="alert" class="pi-issue">
          {{ error }} <Button variant="ghost" size="sm" type="button" @click="retry">重读日志</Button>
        </div>
        <template v-if="command">
          <p v-if="command.text"><code>{{ command.text }}</code></p>
          <ActivityDetail :key="command.id" :job-id="job.id" :row="command" :terminal="latest?.terminal || terminalJob(job.status)" command-output />
        </template>
        <LoadingSkeleton v-else-if="!latest && !error" variant="text" :rows="2" label="正在读取命令日志" />
        <p v-else-if="latest && !error" class="text-muted-foreground">
          {{ latest.terminal ? '暂无命令日志' : '等待命令开始…' }}
        </p>
      </div>
    </details>
    <RouterLink :to="{ path: '/jobs/' + job.id, query: { detail: '1', tab: 'log' } }" class="w-fit underline underline-offset-2">
      查看执行详情
    </RouterLink>
  </li>
</template>
