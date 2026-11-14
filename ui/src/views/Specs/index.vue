<script lang="ts">
interface DisplayState { limit:number; columns:Record<string,number>; scrollTop:number; scrollLeft:number }
const displays=new Map<string,DisplayState>()
</script>
<script setup lang="ts">
import { computed,reactive,shallowRef,watch,nextTick,useId } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import WorkItemRows from '@/components/WorkItemRows.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { isClosed } from '@/services/workbench'
import { useSpecFilters } from './hooks/useSpecFilters'
import { useWorkItems } from './hooks/useWorkItems'
import SpecBoard from './components/SpecBoard.vue'
import SavedViews from './components/SavedViews.vue'
import WorkFilters from './components/WorkFilters.vue'
const {snapshot}=useWorkbench();const {filters,layout,groupBy,viewId,update,toggle,apply,clear,detachView}=useSpecFilters();const id=useId()
const {result,loading,error,refresh}=useWorkItems(filters)
const displayKey=computed(()=>JSON.stringify([filters.value,layout.value,groupBy.value]))
const display=computed(()=>{let value=displays.get(displayKey.value);if(!value){value=reactive({limit:50,columns:{},scrollTop:0,scrollLeft:0});displays.set(displayKey.value,value)}return value})
const restorePosition=shallowRef(true)
function rememberPosition(){display.value.scrollTop=window.scrollY}
onBeforeRouteLeave(rememberPosition)
watch(displayKey,(_next,previous)=>{const before=displays.get(previous);if(before)before.scrollTop=window.scrollY;restorePosition.value=true})
watch(result,async()=>{if(!restorePosition.value)return;const key=displayKey.value;await nextTick();if(key===displayKey.value){window.scrollTo({top:display.value.scrollTop,behavior:'instant'});restorePosition.value=false}},{flush:'post'})
const projects=computed(()=>snapshot.value?.projects??[])
const missingScope=computed(()=>filters.value.projectIds.some(id=>!projects.value.some(p=>p.record.id===id))||filters.value.specIds.some(id=>!snapshot.value?.specs.some(s=>s.record.id===id)))
const coverage=computed(()=>{
 const ss=(snapshot.value?.specs??[]).filter(s=>(!filters.value.projectIds.length||filters.value.projectIds.includes(s.record.projectId))&&(!filters.value.specIds.length||filters.value.specIds.includes(s.record.id)))
 const enabled=(id:string)=>{const s=ss.find(s=>s.record.id===id);return !!s?.record.active&&projects.value.some(p=>p.record.id===s.record.projectId&&p.record.active)}
 const entries=ss.map(s=>({active:enabled(s.record.id),status:s.record.status}))
 return {total:entries.length,inactive:entries.filter(e=>!e.active).length,closed:entries.filter(e=>e.active&&isClosed(e.status)).length}
})
function all(){update({activity:'all',includeClosed:true,statuses:[]})}
</script>
<template>
 <div class="page-heading specs-heading"><div><h1>全部 Spec</h1></div><RouterLink to="/projects" class="text-link">关联更多 Spec ↗</RouterLink></div>
 <div class="specs-viewbar">
  <SavedViews :query="filters" :layout="layout" :group-by="groupBy" :view-id="viewId" @apply="apply" @removed="detachView" />
 </div>
 <WorkFilters :query="filters" :projects="projects" :specs="snapshot?.specs??[]" @update="update" @toggle="toggle" @clear="clear" />
 <div class="results-toolbar">
  <span class="result-count" role="status" aria-live="polite"><strong>{{result?.total??0}}</strong> 项结果<span v-if="loading" class="muted"> · 刷新中</span></span>
  <div class="result-controls">
   <label class="sort-control" :for="id+'sort'"><span>排序</span><SingleSelect :id="id+'sort'" :model-value="filters.sort" :options="[{value:'updatedDesc',label:'最近更新'},{value:'titleAsc',label:'标题顺序'}]" aria-label="排序" class="sort-select" @update:model-value="update({sort:$event as 'updatedDesc'|'titleAsc'})" /></label>
   <div class="segmented" aria-label="显示方式"><button type="button" :aria-pressed="layout==='list'" @click="update({},{layout:'list'})">列表</button><button type="button" :aria-pressed="layout==='board'" @click="update({},{layout:'board'})">看板</button></div>
   <SingleSelect v-if="layout==='board'" aria-label="看板分组" :model-value="groupBy" :options="[{value:'status',label:'按状态'},{value:'stage',label:'按阶段'}]" @update:model-value="update({},{groupBy:$event as 'status'|'stage'})" />
  </div>
 </div>
 <p v-if="missingScope" class="notice warning">筛选引用的项目或 Spec 不存在，结果保持为空。请移除上方标记为“已失效”的条件。</p>
 <p v-if="error" role="alert" class="notice error">{{error}} 保留上次查询结果。<Button variant="outline" @click="refresh">重试</Button></p>
 <p v-else-if="loading && result" class="muted view-note">正在载入新结果，以下暂为上次查询。</p>
 <template v-if="result">
  <section v-if="layout==='list'" class="panel"><WorkItemRows :items="result.items.slice(0,display.limit)" /><div v-if="result.total>display.limit" class="load-more"><Button variant="outline" @click="display.limit+=50">加载更多（已显示 {{Math.min(display.limit,result.total)}} / {{result.total}}）</Button></div></section>
  <SpecBoard v-else v-model:limits="display.columns" v-model:scroll-left="display.scrollLeft" :items="result.items" :group-by="groupBy" :revision="result.revision" />
 </template>
 <div class="coverage-note"><span>当前项目范围已纳管 {{coverage.total}} 项 · 启用且已关闭 {{coverage.closed}} · 停用 {{coverage.inactive}}</span><Button type="button" variant="ghost" @click="all">查看全部记录</Button></div>
 <p v-if="!filters.includeClosed" class="muted view-note">已隐藏已完成与已取消记录；未登记目录和未接入平台不在当前覆盖范围。</p>
</template>

<style scoped>
.specs-viewbar { display:flex; align-items:center; flex-wrap:wrap; gap:12px 20px; margin-bottom:14px; min-width:0; }
.results-toolbar { display:flex; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:10px 16px; margin-bottom:12px; }
.result-count { color:#717780; font-size:12px; }
.result-count strong { color:#303743; font-size:15px; font-weight:600; }
.result-controls { display:flex; align-items:center; flex-wrap:wrap; gap:10px; }
.sort-control { display:flex; align-items:center; gap:8px; color:#717780; font-size:12px; }
.coverage-note { display:flex; align-items:center; flex-wrap:wrap; gap:4px 12px; margin-top:12px; color:#858d99; font-size:11px; }
.coverage-note :deep(button) { font-size:11px; color:#717780; }
@media (width < 720px) {
 .specs-heading { flex-direction:column; gap:12px; margin-bottom:20px; }.specs-viewbar { gap:12px; }.specs-viewbar :deep(.saved-views) { flex-basis:100%; }.result-controls { gap:8px; }.segmented button { min-height:36px; }.coverage-note { align-items:flex-start; }.sort-control :deep(.sort-select) { max-width:130px; }
}
</style>
