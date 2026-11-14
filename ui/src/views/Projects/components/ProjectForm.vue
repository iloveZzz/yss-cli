<script setup lang="ts">
import { reactive, shallowRef, watch } from 'vue'
import ModalDialog from '@/components/ModalDialog.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useWorkbench } from '@/hooks/useWorkbench'
import { errorMessage, type ProjectRecord } from '@/services/workbench'
const props = defineProps<{ project?: ProjectRecord }>()
const open = defineModel<boolean>({ required: true })
const { snapshot, submit, saving } = useWorkbench()
const form = reactive({ name: '', rootPath: '', active: true })
const error = shallowRef(''); let revision = 0
watch(open, value => { if (value) { Object.assign(form, { name: props.project?.name ?? '', rootPath: props.project?.rootPath ?? '', active: props.project?.active ?? true }); revision = snapshot.value?.revision ?? 0; error.value = '' } })
async function save() {
  error.value = ''
  try { await submit(props.project ? { kind: 'updateProject', projectId: props.project.id, ...form } : { kind: 'addProject', rootPath: form.rootPath, ...(form.name.trim() ? { name: form.name } : {}) }, revision); open.value = false }
  catch (e) { error.value = errorMessage(e) }
}
</script>
<template><ModalDialog v-model="open" :title="project ? '编辑项目' : '登记本机项目'" :busy="saving"><form class="form-stack" @submit.prevent="save"><fieldset :disabled="saving" class="form-stack plain-fieldset"><legend class="sr-only">项目信息</legend><label>项目名称<Input v-model="form.name" :required="!!project" placeholder="留空使用目录名称" autofocus /></label><label>本机绝对路径<Input v-model="form.rootPath" required placeholder="/Users/you/Projects/my-project" spellcheck="false" /></label><p class="muted">登记目录用于只读浏览。目录移动后可在这里重新定位，Spec 相对路径保持不变。</p><label v-if="project" class="check-label"><input v-model="form.active" type="checkbox">启用项目（停用后保留全部个人记录）</label><p v-if="error" class="notice error" role="alert">{{ error }}</p><div class="form-actions"><Button type="button" variant="outline" :disabled="saving" @click="open = false">取消</Button><Button :disabled="saving">{{ saving ? '保存中…' : '保存项目' }}</Button></div></fieldset></form></ModalDialog></template>
