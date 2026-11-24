<script setup lang="ts">
import { useRoute } from 'vue-router'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useWorkbench } from '@/hooks/useWorkbench'
import { contentStatuses, dateLabel, hasSourceProblem, healthLabels, stages, type SpecView } from '@/services/workbench'
import StatusBadge from './StatusBadge.vue'

defineProps<{ specs: readonly SpecView[]; compact?: boolean }>()
const { snapshot } = useWorkbench()
const route = useRoute()
const projectName = (id: string) =>
  snapshot.value?.projects.find((project) => project.record.id === id)?.record.name ?? '未知工程'
</script>

<template>
  <Table scrollable scroll-label="Spec 记录" class="spec-table">
    <TableCaption class="sr-only">Spec 记录</TableCaption>
    <TableHeader>
      <TableRow>
        <TableHead scope="col">Spec</TableHead>
        <TableHead scope="col">阶段</TableHead>
        <TableHead scope="col">个人状态</TableHead>
        <TableHead scope="col">内容状态</TableHead>
        <TableHead v-if="!compact" scope="col">下一步</TableHead>
        <TableHead scope="col">来源</TableHead>
        <TableHead v-if="!compact" scope="col">更新</TableHead>
      </TableRow>
    </TableHeader>
    <TableBody>
      <TableRow v-for="spec in specs" :key="spec.record.id">
        <TableHead scope="row" class="spec-row-title">
          <RouterLink
            class="spec-title"
            :to="{ path: `/specs/${spec.record.id}`, query: { return: route.fullPath } }"
          >
            {{ spec.record.title }}
          </RouterLink>
          <span class="row-sub">{{ projectName(spec.record.projectId) }}</span>
        </TableHead>
        <TableCell>{{ stages[spec.record.stage] }}</TableCell>
        <TableCell><StatusBadge :status="spec.record.status" /></TableCell>
        <TableCell>{{ spec.content ? contentStatuses[spec.content.status] : '未标记' }}</TableCell>
        <TableCell v-if="!compact" class="truncate-cell" :title="spec.record.nextAction">
          {{ spec.record.nextAction || '—' }}
        </TableCell>
        <TableCell>
          <span :class="{ 'warning-text': hasSourceProblem(spec) }">
            {{ spec.sourceHealth === 'available' && hasSourceProblem(spec)
              ? '关联资源异常'
              : healthLabels[spec.sourceHealth] }}
          </span>
        </TableCell>
        <TableCell v-if="!compact" class="muted nowrap">
          <time :datetime="spec.record.updatedAt">{{ dateLabel(spec.record.updatedAt) }}</time>
        </TableCell>
      </TableRow>
    </TableBody>
  </Table>
  <Empty v-if="!specs.length"><EmptyDescription>暂无符合条件的 Spec</EmptyDescription></Empty>
</template>

<style scoped>
.spec-row-title {
  background: transparent;
  height: 52px;
  padding-block: 6px;
}
.spec-row-title .spec-title {
  white-space: normal;
  overflow-wrap: anywhere;
}
</style>
