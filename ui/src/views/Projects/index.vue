<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { Button } from '@/components/ui/button'
import { useWorkbench } from '@/hooks/useWorkbench'
import ProjectForm from './components/ProjectForm.vue'
import type { ProjectRecord } from '@/services/workbench'
const { snapshot } = useWorkbench()
const open = shallowRef(false); const selected = shallowRef<ProjectRecord>(); const showInactive = shallowRef(false)
const projects = computed(() => snapshot.value?.projects.filter(p => showInactive.value || p.record.active) ?? [])
function edit(project?: ProjectRecord) { selected.value = project; open.value = true }
const count = (id: string) => snapshot.value?.specs.filter(s => s.record.projectId === id && s.record.active).length ?? 0
</script>
<template><div class="page-heading"><div><h1>项目</h1><p class="muted">汇总本机多个目录，保留每个项目自己的文件结构。</p></div><Button @click="edit()">＋ 登记项目</Button></div><div class="toolbar"><span>{{ projects.length }} 个项目</span><label class="check-label"><input v-model="showInactive" type="checkbox">显示停用项目</label></div><div class="project-grid"><article v-for="p in projects" :key="p.record.id" class="project-card"><div class="card-heading"><span class="project-icon">{{ p.record.name.slice(0,1).toUpperCase() }}</span><Button variant="ghost" :aria-label="`编辑 ${p.record.name}`" @click="edit(p.record)">编辑</Button></div><h2><RouterLink :to="`/projects/${p.record.id}`">{{ p.record.name }}</RouterLink></h2><p class="path-text">{{ p.record.rootPath }}</p><div class="card-foot"><span>{{ count(p.record.id) }} 个 Spec</span><span v-if="!p.record.active" class="muted">已停用</span><span v-else :class="p.available ? 'muted' : 'warning-text'">{{ p.available ? '目录可访问' : '目录不可访问' }}</span></div><p v-if="!p.available" class="warning-text">{{ p.message }}</p></article></div><div v-if="!projects.length" class="empty panel"><h2>从一个项目开始</h2><p>登记本机项目目录，再选择需要跟踪的 Markdown 文件。</p><Button @click="edit()">登记项目</Button></div><ProjectForm v-model="open" :project="selected" /></template>
