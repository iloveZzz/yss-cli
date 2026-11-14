<script setup lang="ts">
import { computed, toRef, watch, useId } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { stages, statuses } from '@/services/workbench'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ModalDialog from '@/components/ModalDialog.vue'
import ResourceEditor from './ResourceEditor.vue'
import { useTaskEditor } from '../hooks/useTaskEditor'
import { parseTaskTitles } from '../schemas/tasks'
const props = defineProps<{specId:string;taskId:string|null;readonly?:boolean;confirmLeave:(dirty:boolean)=>boolean|Promise<boolean>}>()
const emit = defineEmits<{saved:[];close:[];dirty:[value:boolean]}>()
const {draft,dirty,error,notice,saving,save,reset} = useTaskEditor(toRef(props,'specId'),toRef(props,'taskId'))
const {snapshot} = useWorkbench(); const id=useId()
const titles=computed(()=>parseTaskTitles(draft.value.title))
const duplicates=computed(()=>titles.value.filter(title=>snapshot.value?.tasks.some(t=>t.record.specId===props.specId && t.record.title===title)).length)
const missing=computed(()=>!!props.taskId && !snapshot.value?.tasks.some(t=>t.record.id===props.taskId && t.record.specId===props.specId))
watch(dirty,value=>emit('dirty',value),{immediate:true,flush:'sync'})
async function close() { if(!saving.value && await props.confirmLeave(dirty.value)){emit('dirty',false);emit('close')} }
async function submit() { if(!props.readonly && await save()){emit('saved');if(!props.taskId && !dirty.value){emit('dirty',false);emit('close')}} }
async function reload() {if(await props.confirmLeave(dirty.value)) reset()}
</script>
<template><ModalDialog :model-value="true" :title="readonly?'查看任务':taskId ? '编辑任务' : '添加任务'" :busy="saving" :before-close="()=>confirmLeave(dirty)" @update:model-value="value=>{if(!value){emit('dirty',false);emit('close')}}"><p v-if="missing" class="notice warning">任务不存在或不属于当前 Spec。</p><form v-else class="form-stack" @submit.prevent="submit"><fieldset :disabled="readonly || (!taskId && saving)" class="form-stack plain-fieldset"><label :for="id+'title'">{{taskId ? '任务标题' : '任务标题（每行一项）'}}</label><Input v-if="taskId" :id="id+'title'" v-model="draft.title" required /><textarea v-else :id="id+'title'" v-model="draft.title" rows="5" required /><p v-if="!taskId" class="muted">预览 {{titles.length}} 项，新增状态为待处理；原文勾选不转换为完成状态。{{duplicates ? '已有 '+duplicates+' 项同名任务，请核实是否重复。' : ''}}</p><ol v-if="!taskId && titles.length" class="task-preview"><li v-for="(title,i) in titles" :key="i">{{title}}</li></ol><label :for="id+'stage'">任务阶段</label><select :id="id+'stage'" v-model="draft.stage"><option v-for="(label,key) in stages" :key="key" :value="key">{{label}}</option></select><template v-if="taskId"><label :for="id+'status'">任务状态</label><select :id="id+'status'" v-model="draft.status"><option v-for="(label,key) in statuses" :key="key" :value="key">{{label}}</option></select><label :for="id+'blocker'">{{draft.status==='blocked' ? '阻塞说明（必填）' : '阻塞说明（保留的上下文）'}}</label><textarea :id="id+'blocker'" v-model="draft.blocker" :required="draft.status==='blocked'" rows="2" /><label :for="id+'notes'">任务备注</label><textarea :id="id+'notes'" v-model="draft.notes" rows="3" /><label class="check-label"><input v-model="draft.active" type="checkbox">启用任务</label></template><ResourceEditor v-model="draft.resources" /></fieldset><p v-if="error" role="alert" class="notice error">{{error}} <Button type="button" variant="outline" @click="reload">载入最新任务</Button></p><p v-if="notice" role="status" class="notice">{{notice}}</p><div class="form-actions"><Button type="button" variant="outline" :disabled="saving" @click="close">关闭</Button><Button v-if="!readonly" :disabled="saving || (taskId ? !dirty : !titles.length || titles.length>200)">{{saving ? '保存中…' : taskId ? '保存任务' : '确认添加 '+titles.length+' 项'}}</Button></div></form></ModalDialog></template>
