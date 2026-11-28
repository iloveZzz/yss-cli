<script setup lang="ts">
import { CircleCheck, CircleDot } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import type { IssueStatus } from '@/services/workbench-issues'

defineProps<{ status: IssueStatus; chip?: boolean }>()
</script>

<template>
  <component :is="chip ? Badge : 'span'" class="issue-state" :data-status="status" v-bind="chip ? { variant: 'outline' } : {}">
    <component :is="status === 'open' ? CircleDot : CircleCheck" aria-hidden="true" />
    {{ status === 'open' ? '打开' : '已关闭' }}
  </component>
</template>

<style scoped>
.issue-state { display: inline-flex; align-items: center; gap: 5px; font-size: 12px; white-space: nowrap; }
.issue-state > svg { width: 14px; height: 14px; flex: none; }
.issue-state[data-status='open'] { color: #176141; }
.issue-state[data-status='closed'] { color: #654799; }
.issue-state[data-slot='badge'][data-status='open'] { background: #e7f4ec; border-color: #b5d9c1; }
.issue-state[data-slot='badge'][data-status='closed'] { background: #f1edf8; border-color: #d8c9ee; }
</style>
