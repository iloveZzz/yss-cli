<script setup lang="ts">
import { computed, ref, shallowRef, watch, onBeforeUnmount, useId } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useWorkbench } from '@/hooks/useWorkbench'
import { loadLifecycle, stages, stageProgressLabels, dateLabel, errorMessage, type LifecycleView, type Stage, type StageRecord } from '@/services/workbench'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import ResourceEditor from './ResourceEditor.vue'
const props=defineProps<{specId:string;confirmLeave:(dirty:boolean)=>boolean|Promise<boolean>}>()
const emit=defineEmits<{saved:[];dirty:[value:boolean]}>()
const {snapshot,submit,saving,refresh}=useWorkbench();const route=useRoute();const router=useRouter();const id=useId()
const view=shallowRef<LifecycleView>();const error=shallowRef('');const loading=shallowRef(false)
const selected=shallowRef<Stage>('research');const open=shallowRef(false)
const draft=ref<Omit<StageRecord,'updatedAt'>>({specId:props.specId,stage:'research',progress:'unmarked',note:'',resources:[]})
const baseline=shallowRef('');const dirty=computed(()=>open.value && JSON.stringify(draft.value)!==baseline.value)
let controller:AbortController|undefined;let revision=0;let generation=0
watch(dirty,v=>emit('dirty',v),{flush:'sync'})
watch(()=>props.specId,()=>{generation++;view.value=undefined;open.value=false;selected.value='research'})
watch([()=>props.specId,()=>snapshot.value?.revision],async()=>{
  controller?.abort();const own=new AbortController();controller=own;loading.value=true
  try {const result=await loadLifecycle(props.specId,own.signal);if(!own.signal.aborted){if(result.revision===snapshot.value?.revision){view.value=result;error.value=''}else await refresh()}}
  catch(e){if(!own.signal.aborted)error.value=errorMessage(e)}
  finally{if(controller===own)loading.value=false}
},{immediate:true})
onBeforeUnmount(()=>{controller?.abort();emit('dirty',false);generation++})
const phase=computed(()=>view.value?.stages.find(s=>s.stage===selected.value))
const tasks=computed(()=>view.value?.tasks.filter(t=>t.record.active && t.record.stage===selected.value)??[])
const enabled=computed(()=>view.value?.spec.record.active && snapshot.value?.projects.some(p=>p.record.id===view.value?.spec.record.projectId && p.record.active))
function edit(){const record=phase.value?.record;draft.value=record ? {specId:record.specId,stage:record.stage,progress:record.progress,note:record.note,resources:structuredClone(record.resources)} : {specId:props.specId,stage:selected.value,progress:'unmarked',note:'',resources:[]};baseline.value=JSON.stringify(draft.value);revision=view.value?.revision??0;error.value='';open.value=true}
async function save(){const encoded=JSON.stringify(draft.value);const own=generation;error.value='';try{const result=await submit({kind:'setStageRecord',...JSON.parse(encoded)},revision);if(own!==generation)return;baseline.value=encoded;revision=result.revision;emit('saved');if(!dirty.value)open.value=false}catch(e){if(own===generation)error.value=errorMessage(e)}}
async function focus(){try{await submit({kind:'setWorkStage',entity:'spec',id:props.specId,stage:selected.value},view.value?.revision);emit('saved')}catch(e){error.value=errorMessage(e)}}
async function close(){if(await props.confirmLeave(dirty.value))open.value=false}
function taskLink(taskId:string){void router.replace({query:{...route.query,taskId}})}
</script>
<template><div class="section-heading"><div><h2>全生命周期</h2><p class="muted">阶段可并行、跳过和重开；个人记录与任务完成互相独立。</p></div><span v-if="loading" class="muted">正在刷新…</span></div><p v-if="error && !open" role="alert" class="notice error">{{error}}</p><template v-if="view"><ol class="lifecycle-track"><li v-for="(node,i) in view.stages" :key="node.stage"><button :class="['phase-node',node.record?.progress??'unmarked',{selected:selected===node.stage}]" :aria-pressed="selected===node.stage" @click="selected=node.stage"><span class="phase-number">{{i+1}}</span><strong>{{stages[node.stage]}}</strong><span>{{stageProgressLabels[node.record?.progress??'unmarked']}}</span><small v-if="view.spec.record.stage===node.stage" class="focus-label">当前关注</small><small v-else>{{node.taskCounts.total}} 项任务</small></button></li></ol><section class="phase-detail panel"><div class="section-heading"><h3>{{stages[selected]}}</h3><div class="inline-actions"><Button variant="outline" :disabled="!enabled || saving || view.spec.record.stage===selected" @click="focus">设为关注阶段</Button><Button :disabled="!enabled || saving" @click="edit">记录阶段</Button></div></div><p>{{stageProgressLabels[phase?.record?.progress??'unmarked']}} <span v-if="phase?.record" class="muted">· 记录于 {{dateLabel(phase.record.updatedAt)}}</span></p><p class="preserve-lines">{{phase?.record?.note || '尚未记录该阶段的结论或说明。'}}</p><div v-if="phase?.record?.resources.length" class="phase-resources"><p v-for="(r,i) in phase.record.resources" :key="i"><strong>{{r.label}}</strong> · <a v-if="r.targetType==='web' && /^https?:\/\//i.test(r.target)" :href="r.target" target="_blank" rel="noopener noreferrer">{{r.target}} ↗</a><code v-else>{{r.target}}</code></p></div><p class="muted">该阶段任务：已完成 {{phase?.taskCounts.done??0}} / {{phase?.taskCounts.total??0}}，已取消 {{phase?.taskCounts.canceled??0}}。这些数量不代表阶段验收。</p><div class="task-list"><article v-for="task in tasks" :key="task.record.id" class="task-row"><button class="text-link" @click="taskLink(task.record.id)">{{task.record.title}}</button><StatusBadge :status="task.record.status" /></article><p v-if="!tasks.length" class="empty">该阶段暂无启用任务。</p></div></section><p v-if="!view.events.length" class="muted">接入前过程未记录。</p><p class="muted">实际操作记录见“变更历史”；关注阶段变化不代表其他阶段已完成。</p></template><ModalDialog v-model="open" title="记录阶段进度" :busy="saving" :before-close="()=>confirmLeave(dirty)"><form class="form-stack" @submit.prevent="save"><h3>{{stages[draft.stage]}}</h3><label :for="id+'progress'">阶段记录</label><select :id="id+'progress'" v-model="draft.progress"><option v-for="(label,key) in stageProgressLabels" :key="key" :value="key">{{label}}</option></select><label :for="id+'note'">阶段说明{{['blocked','skipped'].includes(draft.progress)?'（必填）':''}}</label><textarea :id="id+'note'" v-model="draft.note" rows="4" :required="['blocked','skipped'].includes(draft.progress)" /><ResourceEditor v-model="draft.resources" /><p class="muted">完成表示个人记录，不代表自动验收或发布。</p><p v-if="error" class="notice error" role="alert">{{error}} <Button type="button" variant="outline" @click="async()=>{if(await confirmLeave(dirty))edit()}">载入最新阶段</Button></p><div class="form-actions"><Button type="button" variant="outline" :disabled="saving" @click="close">关闭</Button><Button :disabled="saving || !dirty">保存阶段</Button></div></form></ModalDialog></template>
