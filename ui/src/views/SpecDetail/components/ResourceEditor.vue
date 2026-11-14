<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { ResourceRef } from '@/services/workbench'
const resources = defineModel<ResourceRef[]>({ required: true })
</script>
<template><div class="section-heading"><h3>关联资源</h3><Button type="button" variant="outline" @click="resources = [...resources, { kind: 'other', label: '', targetType: 'local', target: '' }]">＋ 添加</Button></div><fieldset v-for="(resource, i) in resources" :key="i" class="resource-editor"><legend>资源 {{ i + 1 }}</legend><div class="form-grid"><label>类型<select v-model="resource.kind"><option value="supplement">规格补充</option><option value="prototype">原型</option><option value="interaction">交互说明</option><option value="other">其他</option></select></label><label>位置<select v-model="resource.targetType"><option value="local">项目内文件</option><option value="web">HTTP(S) 链接</option></select></label></div><label>资源名称<Input v-model="resource.label" required /></label><label>{{ resource.targetType === 'local' ? '项目内相对路径' : '链接地址' }}<Input v-model="resource.target" required spellcheck="false" /></label><Button type="button" variant="ghost" @click="resources = resources.filter((_, index) => index !== i)">移除资源 {{ i + 1 }}</Button></fieldset><p class="muted">本地文件只显示路径；外部链接由你点击打开，工作台不探测。</p></template>
