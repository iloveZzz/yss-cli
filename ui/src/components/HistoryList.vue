<script setup lang="ts">
import { computed } from 'vue'
import { dateLabel, stages, statuses, stageProgressLabels, type ChangeEvent } from '@/services/workbench'
const props = defineProps<{ events: readonly ChangeEvent[] }>()
const specEvents = computed(() => props.events.filter(event => !event.taskId))
const labels: Record<string,string> = { name: '项目名', rootPath: '项目路径', active: '启用', title: '标题', sourcePath: '主规格路径', stage: '关注阶段', status: '状态', nextAction: '下一步', blocker: '阻塞说明', notes: '备注', resources: '关联资源', progress:'阶段记录',note:'阶段说明' }
function target(event:ChangeEvent){return event.lifecycleStage ? '阶段 · '+stages[event.lifecycleStage] : 'Spec / 项目'}
function value(key: string, v: unknown): string { if (v == null) return '未设置'; if (key==='progress' && typeof v==='string') return stageProgressLabels[v as keyof typeof stageProgressLabels]??v; if (key === 'stage' && typeof v === 'string') return stages[v as keyof typeof stages] ?? v; if (key === 'status' && typeof v === 'string') return statuses[v as keyof typeof statuses] ?? v; if (typeof v === 'boolean') return v ? '是' : '否'; return typeof v === 'string' ? (v || '空') : JSON.stringify(v) }
</script>
<template><ol class="history-list"><li v-for="event in specEvents" :key="event.id"><strong>{{target(event)}}</strong> <time>{{ dateLabel(event.at) }}</time><dl><div v-for="(change, field) in event.changes" :key="field"><dt>{{ labels[field] ?? field }}</dt><dd><span class="muted">{{ value(field, change.before) }}</span><span aria-label="改为"> → </span>{{ value(field, change.after) }}</dd></div></dl></li></ol><p v-if="!specEvents.length" class="empty">暂无个人变更记录；接入前过程未记录。</p></template>
