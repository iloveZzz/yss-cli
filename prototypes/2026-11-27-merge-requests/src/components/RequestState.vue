<script setup lang="ts">
import { CircleX, GitMerge, GitPullRequest } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { tabs, type Status } from '@/demo'

defineProps<{ status: Status; draft: boolean }>()

const icons = { open: GitPullRequest, merged: GitMerge, closed: CircleX }
</script>

<template>
  <div class="request-state">
    <Badge variant="outline" class="state-badge" :data-status="status">
      <component :is="icons[status]" aria-hidden="true" />
      {{ tabs.find(tab => tab.id === status)?.label }}
    </Badge>
    <Badge v-if="draft" variant="outline" class="draft-badge">草稿</Badge>
  </div>
</template>
