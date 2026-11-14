<script setup lang="ts">
import { useRoute } from 'vue-router'
import StatusBadge from './StatusBadge.vue'
import { stages, healthLabels, dateLabel, hasSourceProblem, type SpecView } from '@/services/workbench'
import { useWorkbench } from '@/hooks/useWorkbench'
defineProps<{ specs: readonly SpecView[]; compact?: boolean }>()
const { snapshot } = useWorkbench()
const route = useRoute()
const projectName = (id: string) => snapshot.value?.projects.find(p => p.record.id === id)?.record.name ?? '未知项目'
</script>
<template><div class="table-scroll"><table class="spec-table"><thead><tr><th>Spec</th><th>阶段</th><th>状态</th><th v-if="!compact">下一步</th><th>来源</th><th v-if="!compact">更新</th></tr></thead><tbody><tr v-for="s in specs" :key="s.record.id"><td><RouterLink class="spec-title" :to="{ path: `/specs/${s.record.id}`, query: { return: route.fullPath } }">{{ s.record.title }}</RouterLink><span class="row-sub">{{ projectName(s.record.projectId) }}</span></td><td>{{ stages[s.record.stage] }}</td><td><StatusBadge :status="s.record.status" /></td><td v-if="!compact" class="truncate-cell" :title="s.record.nextAction">{{ s.record.nextAction || '—' }}</td><td><span :class="{ 'warning-text': hasSourceProblem(s) }">{{ s.sourceHealth === 'available' && hasSourceProblem(s) ? '关联资源异常' : healthLabels[s.sourceHealth] }}</span></td><td v-if="!compact" class="muted nowrap">{{ dateLabel(s.record.updatedAt) }}</td></tr></tbody></table><p v-if="!specs.length" class="empty">暂无符合条件的 Spec</p></div></template>
