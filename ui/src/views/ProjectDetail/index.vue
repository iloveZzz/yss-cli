<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import WorkItemRows from '@/components/WorkItemRows.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { defaultWorkQuery, isClosed } from '@/services/workbench'
import { useWorkItems } from '@/views/Specs/hooks/useWorkItems'
import { encodeWorkQuery } from '@/views/Specs/schemas/filter'
import ProjectForm from '@/views/Projects/components/ProjectForm.vue'
import DiscoverSpecs from './components/DiscoverSpecs.vue'
const route = useRoute(); const router = useRouter(); const { snapshot } = useWorkbench(); const edit = shallowRef(false)
const entity = computed({get:(): 'spec'|'task' => route.query.entity==='task'?'task':'spec',set:value=>{void router.replace({query:{...route.query,entity:value}})}})
const showInactive = computed({get:()=>route.query.activity==='all',set:value=>{void router.replace({query:{...route.query,activity:value?'all':undefined}})}})
const showClosed = computed({get:()=>route.query.showClosed==='true',set:value=>{void router.replace({query:{...route.query,showClosed:value?'true':undefined}})}})
const project = computed(() => snapshot.value?.projects.find(p => p.record.id === route.params.projectId))
const specs = computed(() => snapshot.value?.specs.filter(s => s.record.projectId === project.value?.record.id) ?? [])
const tasks = computed(() => snapshot.value?.tasks.filter(t => specs.value.some(s => s.record.id === t.record.specId)) ?? [])
const query = computed(() => ({...defaultWorkQuery(), entity:entity.value, projectIds:[String(route.params.projectId)], activity:showInactive.value?'all' as const:'active' as const, includeClosed:showClosed.value}))
const {result,loading,error,refresh} = useWorkItems(query)
const totals = computed(() => {
  const enabled=(id:string)=>!!project.value?.record.active&&!!specs.value.find(s=>s.record.id===id)?.record.active
  const entries=entity.value==='spec'?specs.value.map(s=>({active:enabled(s.record.id),status:s.record.status})):tasks.value.map(t=>({active:enabled(t.record.specId)&&t.record.active,status:t.record.status}))
  return {total:entries.length,inactive:entries.filter(e=>!e.active).length,closed:entries.filter(e=>e.active&&isClosed(e.status)).length}
})
const allLink = computed(() => ({path:'/specs',query:encodeWorkQuery({...query.value,activity:'all',includeClosed:true},'list','status')}))
</script>
<template><template v-if="project"><div class="page-heading"><div><RouterLink to="/projects" class="muted">← 项目</RouterLink><h1>{{project.record.name}}</h1><p class="path-text">{{project.record.rootPath}}</p></div><Button variant="outline" @click="edit=true">编辑项目</Button></div><div v-if="!project.available||!project.record.active" class="notice warning">{{!project.record.active?'项目已停用，记录保留。可通过“编辑项目”恢复。':`目录不可访问：${project.message}。可重新定位项目根。`}}</div><div class="section-heading"><h2>已纳管工作项</h2><RouterLink :to="allLink">查看项目全部记录 →</RouterLink></div><div class="toolbar"><div class="segmented" aria-label="项目工作项类型"><button :aria-pressed="entity==='spec'" @click="entity='spec'">Spec（{{specs.length}}）</button><button :aria-pressed="entity==='task'" @click="entity='task'">任务（{{tasks.length}}）</button></div><span>共 {{totals.total}} 项 · 启用且已关闭 {{totals.closed}} · 停用 {{totals.inactive}}</span><label class="check-label"><input v-model="showInactive" type="checkbox">显示停用记录</label><label class="check-label"><input v-model="showClosed" type="checkbox">显示已完成／已取消</label></div><p v-if="error" class="notice error" role="alert">{{error}}<Button variant="outline" @click="refresh">重试</Button></p><p v-if="loading" class="muted">正在更新项目工作项…</p><section v-if="result" class="panel"><WorkItemRows :items="result.items.slice(0,50)" /><p v-if="result.total>50" class="empty">当前显示前 50 项。<RouterLink :to="allLink">查看全部 {{result.total}} 项 →</RouterLink></p></section><p class="view-note muted">仅覆盖手动关联的 Spec 与录入的任务。尚未选择的文件、其他目录和外部平台不计入。</p><DiscoverSpecs v-if="project.available&&project.record.active" :key="project.record.id" :project-id="project.record.id" :directories="project.sourceDirectories" /><ProjectForm v-model="edit" :project="project.record" /></template><p v-else class="empty">项目不存在。<RouterLink to="/projects">返回项目列表</RouterLink></p></template>
