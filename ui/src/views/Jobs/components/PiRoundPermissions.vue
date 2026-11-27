<script setup lang="ts">
import type { AgentProjectAccess } from '@/services/workbench-agent'
import { piCommandPermissionLabels, type PiCommandExecution } from '@/services/workbench-platform'

defineProps<{
  access: AgentProjectAccess[]
  execution?: PiCommandExecution | null
  names: Record<string, string>
}>()
</script>

<template>
  <details v-if="access.length" class="text-xs text-muted-foreground">
    <summary class="w-fit cursor-pointer py-1 font-medium">各工程运行权限</summary>
    <ul class="mt-1 grid gap-2 border-s pl-3">
      <li v-for="entry in access" :key="entry.projectId" class="grid gap-0.5 break-all">
        <span class="text-foreground">
          {{ names[entry.projectId] ?? entry.projectId }} ·
          {{ entry.mode === 'read' ? '只读' : entry.mode === 'edit' ? '编辑' : piCommandPermissionLabels[execution ?? 'sandbox'] }}
        </span>
        <span v-if="entry.expectedRootPath">{{ entry.expectedRootPath }}</span>
        <span v-if="entry.expectedHead" class="font-mono">
          {{ entry.expectedBranch ?? '分离 HEAD' }} · {{ entry.expectedHead }}
        </span>
      </li>
    </ul>
  </details>
</template>
