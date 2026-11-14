<script setup lang="ts">
import { computed } from 'vue'
import SpecRows from '@/components/SpecRows.vue'
import WorkItemRows from '@/components/WorkItemRows.vue'
import ContentAttention from './components/ContentAttention.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { dateLabel, hasSourceProblem, defaultWorkQuery, stages, type ChangeEvent } from '@/services/workbench'
import { useWorkItems } from '@/views/Specs/hooks/useWorkItems'
const { activeSpecs, snapshot } = useWorkbench()
const taskQuery = computed(() => ({ ...defaultWorkQuery(), entity: 'task' as const }))
const { result: taskResult, loading: taskLoading, error: taskError, refresh: refreshTasks } = useWorkItems(taskQuery)
const taskGroups = computed(() => [
  { title: '进行中的任务', query: '/specs?entity=task&status=doing', items: taskResult.value?.items.filter(t => t.status === 'doing') ?? [] },
  { title: '阻塞任务', query: '/specs?entity=task&status=blocked', items: taskResult.value?.items.filter(t => t.status === 'blocked') ?? [] },
  { title: '待验证任务', query: '/specs?entity=task&stage=verification', items: taskResult.value?.items.filter(t => t.stage === 'verification') ?? [] },
])
const doing = computed(() => activeSpecs.value.filter(s => s.record.status === 'doing'))
const blocked = computed(() => activeSpecs.value.filter(s => s.record.status === 'blocked'))
const problems = computed(() => activeSpecs.value.filter(hasSourceProblem))
const recent = computed(() => activeSpecs.value.filter(s => s.record.lastVisitedAt).sort((a,b) => (b.record.lastVisitedAt ?? '').localeCompare(a.record.lastVisitedAt ?? '')).slice(0,5))
const activity = computed(() => [...(snapshot.value?.history ?? [])].reverse().slice(0,8))
const title = (specId: string | null, projectId: string) => specId ? snapshot.value?.specs.find(s => s.record.id === specId)?.record.title : snapshot.value?.projects.find(p => p.record.id === projectId)?.record.name
function target(event: ChangeEvent) {
  const parent = title(event.specId,event.projectId) ?? '未知记录'
  if(event.taskId) return `${snapshot.value?.tasks.find(t => t.record.id === event.taskId)?.record.title ?? '任务'} · ${parent}`
  return event.lifecycleStage ? `${parent} · ${stages[event.lifecycleStage]}阶段` : parent
}
function scrollProblems() { document.getElementById('source-problems')?.scrollIntoView({ behavior: 'instant' }) }
const fields: Record<string, string> = { name: '项目名', rootPath: '路径', active: '启用状态', title: '标题', sourcePath: '来源', stage: '阶段', status: '状态', nextAction: '下一步', blocker: '阻塞说明', notes: '备注', resources: '资源', progress: '阶段进度', note: '阶段说明' }
</script>
<template><div class="page-heading"><div><div class="eyebrow">PERSONAL WORKSPACE</div><h1>工作台</h1><p class="muted">继续手上的工作，找出需要你处理的阻塞与来源问题。</p></div><RouterLink to="/specs" class="text-link">查看全部 Spec 与任务 →</RouterLink></div><div v-if="!snapshot?.projects.length" class="empty panel welcome"><h2>把本机 Spec 汇集到这里</h2><p>先登记项目，再从 Markdown 文件中选择要跟踪的 Spec。</p><RouterLink to="/projects" class="primary-link">登记第一个项目 →</RouterLink></div><template v-else><div class="overview-strip"><RouterLink to="/specs?status=doing"><strong>{{ doing.length }}</strong><span>进行中 Spec</span></RouterLink><RouterLink to="/specs?status=blocked"><strong>{{ blocked.length }}</strong><span>阻塞 Spec</span></RouterLink><a href="#/workbench" @click.prevent="scrollProblems"><strong>{{ problems.length }}</strong><span>来源异常</span></a><RouterLink to="/projects"><strong>{{ snapshot.projects.filter(p => p.record.active).length }}</strong><span>本机项目</span></RouterLink></div><div class="workbench-grid"><div><ContentAttention :specs="activeSpecs" /><section class="work-section"><div class="section-heading"><h2>具体任务</h2><RouterLink to="/specs?entity=task">查看全部任务 →</RouterLink></div><p class="muted view-note">仅统计启用且未关闭的任务；待验证表示任务阶段为验证。</p><p v-if="taskError" role="alert" class="notice error">{{taskError}}<button @click="refreshTasks">重试</button></p><p v-if="taskLoading" class="muted">正在更新任务…</p><section v-for="group in taskGroups" :key="group.title" class="work-section"><div class="section-heading"><h3>{{group.title}} <span class="count">{{group.items.length}}</span></h3><RouterLink :to="group.query">查看列表</RouterLink></div><div class="panel"><WorkItemRows :items="group.items.slice(0,5)" /></div><p v-if="group.items.length>5" class="muted view-note">显示前 5 项，查看列表可继续处理。</p></section></section><section class="work-section"><div class="section-heading"><h2>进行中 <span class="count">{{ doing.length }}</span></h2><RouterLink to="/specs?status=doing">查看列表</RouterLink></div><div class="panel"><SpecRows :specs="doing" compact /></div></section><section class="work-section"><div class="section-heading"><h2>需要解除阻塞</h2></div><div class="panel blocked-list"><article v-for="s in blocked" :key="s.record.id"><RouterLink :to="{ path: `/specs/${s.record.id}`, query: { return: '/workbench' } }">{{ s.record.title }}</RouterLink><p>{{ s.record.blocker }}</p><p class="muted">下一步：{{ s.record.nextAction || '尚未填写' }}</p></article><p v-if="!blocked.length" class="empty">当前没有标记为阻塞的 Spec</p></div></section><section id="source-problems" class="work-section"><div class="section-heading"><h2>来源异常</h2><span class="muted">不改变个人状态</span></div><div class="panel"><SpecRows :specs="problems" compact /></div></section></div><aside><section class="panel side-section"><h2>最近访问</h2><RouterLink v-for="s in recent" :key="s.record.id" :to="{ path: `/specs/${s.record.id}`, query: { return: '/workbench' } }" class="recent-item"><strong>{{ s.record.title }}</strong><span>{{ dateLabel(s.record.lastVisitedAt) }}</span></RouterLink><p v-if="!recent.length" class="empty">打开 Spec 后显示在这里</p></section><section class="panel side-section"><h2>最近操作</h2><ol class="activity-list"><li v-for="event in activity" :key="event.id"><RouterLink :to="{path:event.specId ? `/specs/${event.specId}` : `/projects/${event.projectId}`,query:{taskId:event.taskId??undefined,return:'/workbench'}}">{{ target(event) }}</RouterLink><p>{{ Object.keys(event.changes).map(key => fields[key] ?? key).join('、') }}</p><time>{{ dateLabel(event.at) }}</time></li></ol><p v-if="!activity.length" class="empty">暂无个人变更</p></section></aside></div></template></template>
