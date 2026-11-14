<script setup lang="ts">
import { shallowRef, ref, computed, onBeforeUnmount } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { discoverSpecs, errorMessage, type DiscoverResult } from '@/services/workbench'
import { useWorkbench } from '@/hooks/useWorkbench'
const props = defineProps<{ projectId: string; directories: string[] }>()
const { submit, saving } = useWorkbench()
const directory = shallowRef(props.directories[0] ?? '.'); const manual = shallowRef(''); const result = shallowRef<DiscoverResult>(); const selected = ref<string[]>([])
const busy = shallowRef(false); const message = shallowRef(''); const errors = ref<string[]>([])
const unregistered = computed(() => result.value?.candidates.filter(c => !c.alreadyRegistered).map(c => c.sourcePath) ?? [])
let controller: AbortController | undefined
onBeforeUnmount(() => controller?.abort())
async function scan() {
  controller?.abort(); controller = new AbortController(); const current = controller
  busy.value = true; errors.value = []; message.value = ''; selected.value = []; result.value = undefined
  try { const next = await discoverSpecs({ projectId: props.projectId, directory: directory.value }, current.signal); if (!current.signal.aborted) result.value = next }
  catch (e) { if (!current.signal.aborted) errors.value = [errorMessage(e)] }
  finally { if (controller === current) busy.value = false }
}
async function add(paths: string[]) {
  busy.value = true; errors.value = []; let count = 0
  for (const path of paths) {
    try { await submit({ kind: 'addSpec', projectId: props.projectId, sourcePath: path }); count++; selected.value = selected.value.filter(p => p !== path); if (result.value) result.value = { ...result.value, candidates: result.value.candidates.map(c => c.sourcePath === path ? { ...c, alreadyRegistered: true } : c) } }
    catch (e) { errors.value.push(`${path}：${errorMessage(e)}`); break }
  }
  message.value = `已关联 ${count} 个 Spec${errors.value.length ? '，其余项未关联；请核实后再操作。' : '。'}`
  if (paths.length === 1 && count === 1 && paths[0] === manual.value) manual.value = ''
  busy.value = false
}
</script>
<template><section class="panel discover-panel"><h2>关联 Spec</h2><p class="muted">选择项目内目录查找 Markdown，勾选后才会关联。样例不会自动成为任务。</p><form class="inline-form" @submit.prevent="scan"><label class="grow">相对目录<Input v-model="directory" list="spec-directories" placeholder="例如 docs/specs" /></label><datalist id="spec-directories"><option v-for="dir in directories" :key="dir" :value="dir" /><option value="." /></datalist><Button variant="outline" :disabled="busy || saving">{{ busy ? '处理中…' : '发现文件' }}</Button></form><template v-if="result"><div class="notice" :class="{ warning: !result.complete }">{{ result.complete ? `发现 ${result.candidates.length} 个候选文件` : '发现结果不完整，请查看提示或缩小目录范围。' }}<ul v-if="result.warnings.length"><li v-for="warning in result.warnings" :key="warning">{{ warning }}</li></ul></div><div class="toolbar"><Button variant="outline" :disabled="busy||!unregistered.length" @click="selected=[...unregistered]">选择当前未关联项（{{unregistered.length}}）</Button><Button variant="ghost" :disabled="busy||!selected.length" @click="selected=[]">清空选择</Button><span class="muted">仅选择当前发现结果，不代表整个项目。</span></div><div class="candidate-list"><label v-for="c in result.candidates" :key="c.sourcePath" class="candidate"><input v-model="selected" type="checkbox" :value="c.sourcePath" :disabled="c.alreadyRegistered || busy"><span><strong>{{ c.title }}</strong><span class="row-sub">{{ c.sourcePath }}</span></span><span v-if="c.alreadyRegistered" class="muted">已关联（含停用）</span></label></div><Button :disabled="!selected.length || busy || saving" @click="add([...selected])">关联选中项（{{ selected.length }}）</Button></template><details><summary>手工关联文件</summary><form class="inline-form" @submit.prevent="add([manual])"><label class="grow">主规格相对路径<Input v-model="manual" required placeholder="docs/specs/my-spec.md" /></label><Button variant="outline" :disabled="busy || saving">关联</Button></form></details><p v-if="message" role="status" class="notice">{{ message }}</p><p v-for="error in errors" :key="error" role="alert" class="notice error">{{ error }}</p></section></template>
