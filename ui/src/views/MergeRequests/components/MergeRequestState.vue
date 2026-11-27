<script setup lang="ts">
import { CircleHelp, CircleX, GitMerge, GitPullRequest } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import type { MergeRequestStatus } from '@/services/workbench-merge-requests'

defineProps<{ status: MergeRequestStatus; draft: boolean | null }>()
const states = {
  open: { label: '开放中', icon: GitPullRequest },
  merged: { label: '已合并', icon: GitMerge },
  closed: { label: '已关闭', icon: CircleX },
  unknown: { label: '状态不可用', icon: CircleHelp }
}
</script>

<template>
  <span class="request-state">
    <Badge variant="outline" class="request-status" :data-status="status">
      <component :is="states[status].icon" aria-hidden="true" />{{ states[status].label }}
    </Badge>
    <Badge v-if="draft === null || (status === 'open' && draft)" variant="secondary" class="draft-state">
      {{ draft === null ? '草稿状态不可用' : '草稿' }}
    </Badge>
  </span>
</template>

<style scoped>
.request-state { display: flex; flex-wrap: wrap; align-items: center; gap: 4px; }
.request-status, .draft-state { width: fit-content; font-size: 11px; }
.request-status { border-color: var(--yss-colors-border); }
.request-status[data-status='open'] { color: #176141; background: #ebf7ef; border-color: #ceead6; }
.request-status[data-status='merged'] { color: #634391; background: #f1ecfa; border-color: #e5dbf5; }
.request-status[data-status='closed'] { color: #954042; background: #fcf0f0; border-color: #f0dbdc; }
</style>
