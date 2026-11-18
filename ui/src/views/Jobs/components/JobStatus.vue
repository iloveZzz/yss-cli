<script setup lang="ts">
import { jobStatusLabels } from '@/services/workbench-platform'
defineProps<{ status: string; stopping?: boolean }>()
</script>
<template>
  <span class="job-state" :data-state="status">{{
    stopping ? '正在停止…' : (jobStatusLabels[status] ?? status)
  }}</span>
</template>
<style scoped>
.job-state {
  display: inline-flex;
  align-items: center;
  min-height: 24px;
  padding: 2px 8px;
  border-radius: 5px;
  background: var(--brand-color-fill-secondary);
  font-size: 12px;
  white-space: nowrap;
}
.job-state[data-state='running'],
.job-state[data-state='preparing'] {
  color: var(--yss-color-primary-control);
  background: var(--yss-colors-primary-bg);
}
.job-state[data-state='failed'],
.job-state[data-state='recoveryRequired'] {
  color: var(--yss-colors-error-text);
}
.job-state[data-state='waitingInput'],
.job-state[data-state='waitingApproval'] {
  color: #92600a;
  background: #fff6e6;
}

.job-state[data-state='completed'],
.job-state[data-state='succeeded'] {
  color: #207153;
  background: #eaf5ef;
}
</style>
