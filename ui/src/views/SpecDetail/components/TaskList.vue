<script setup lang="ts">
import { computed, shallowRef, watch, onBeforeUnmount, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useWorkbench } from '@/hooks/useWorkbench'
import { stages } from '@/services/workbench'
import { Button } from '@/components/ui/button'
import StatusBadge from '@/components/StatusBadge.vue'
import TaskEditor from './TaskEditor.vue'
const props=defineProps<{specId:string;confirmLeave:(dirty:boolean)=>boolean|Promise<boolean>}>()
const emit=defineEmits<{saved:[];dirty:[value:boolean]}>()
const {snapshot}=useWorkbench();const route=useRoute();const router=useRouter()
const creating=shallowRef(false);const showInactive=shallowRef(false)
const selected=computed(()=>typeof route.query.taskId==='string'?route.query.taskId:null)
const spec=computed(()=>snapshot.value?.specs.find(s=>s.record.id===props.specId)?.record)
const enabled=computed(()=>spec.value?.active && snapshot.value?.projects.some(p=>p.record.id===spec.value?.projectId && p.record.active))
const all=computed(()=>snapshot.value?.tasks.filter(t=>t.record.specId===props.specId)??[])
const tasks=computed(()=>all.value.filter(t=>showInactive.value || t.record.active))
function edit(id:string){creating.value=false;void router.replace({query:{...route.query,taskId:id}})}
async function close(){const id=selected.value;creating.value=false;emit('dirty',false);if(id){const q={...route.query};delete q.taskId;await router.replace({query:q})}await nextTick();(document.getElementById('task-open-'+id)??document.getElementById('add-task'))?.focus()}
watch(()=>props.specId,()=>{creating.value=false;emit('dirty',false)})
onBeforeUnmount(()=>emit('dirty',false))
</script>
<template><div class="section-heading"><div><h2>具体任务 <span class="count">{{all.length}}</span></h2><p class="muted">每项任务独立推进；不会自动完成 Spec 或阶段。</p></div><Button id="add-task" :disabled="!enabled" @click="creating=true">＋ 添加任务</Button></div><p v-if="!enabled" class="notice warning">所属项目或 Spec 已停用，恢复后可编辑任务。</p><label class="check-label"><input v-model="showInactive" type="checkbox">显示停用任务（{{all.filter(t=>!t.record.active).length}}）</label><div class="task-list"><article v-for="task in tasks" :key="task.record.id" class="task-row"><div><button :id="'task-open-'+task.record.id" class="text-link" @click="edit(task.record.id)">{{task.record.title}}</button><p class="muted">{{stages[task.record.stage]}} · {{task.record.active?'启用':'已停用'}}</p><p v-if="task.record.status==='blocked'" class="blocker-text">{{task.record.blocker}}</p></div><StatusBadge :status="task.record.status" /><Button variant="outline" :aria-label="(enabled?'编辑任务 ':'查看任务 ')+task.record.title" @click="edit(task.record.id)">{{enabled?'编辑':'查看'}}</Button></article><p v-if="!tasks.length" class="empty">暂无任务。可单条或批量录入，并关联已有任务清单。</p></div><TaskEditor v-if="creating || selected" :readonly="!enabled" :spec-id="specId" :task-id="creating?null:selected" :confirm-leave="confirmLeave" @dirty="emit('dirty',$event)" @saved="emit('saved')" @close="close" /></template>
