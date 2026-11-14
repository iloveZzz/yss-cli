<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useEventListener } from '@vueuse/core'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { useWorkbench } from '@/hooks/useWorkbench'
import { dateLabel } from '@/services/workbench'
const { snapshot, loading, error, expired, refresh } = useWorkbench()
const route = useRoute()
const recentProjects = computed(() => [...(snapshot.value?.projects ?? [])].filter(p => p.record.active).sort((a,b) => b.record.updatedAt.localeCompare(a.record.updatedAt)).slice(0,6))
const section = computed(() => route.path.startsWith('/projects') ? '项目' : route.path.startsWith('/specs') ? 'Spec' : '工作台')
function focusMain() { document.getElementById('main')?.focus() }
onMounted(refresh)
watch(() => route.path, refresh)
useEventListener(window, 'focus', refresh)
</script>
<template><a class="skip-link" href="#main" @click.prevent="focusMain">跳到主内容</a><header class="app-header"><RouterLink to="/workbench" class="brand"><span class="brand-mark">Y</span> YSS <span class="brand-sub">Spec 工作台</span></RouterLink><span class="local-label"><span aria-hidden="true">●</span> 个人 · 本机</span><Button variant="outline" :disabled="loading || expired" @click="refresh">{{ loading ? '刷新中…' : '刷新' }}</Button></header><aside class="sidebar"><div class="nav-caption">工作空间</div><nav aria-label="主导航"><RouterLink to="/workbench">⌂ <span>工作台</span></RouterLink><RouterLink to="/projects">▣ <span>项目</span></RouterLink><RouterLink to="/specs">☷ <span>Spec 与任务</span></RouterLink></nav><div class="nav-caption recent-caption">最近项目</div><nav aria-label="最近项目"><RouterLink v-for="p in recentProjects" :key="p.record.id" :to="`/projects/${p.record.id}`"><span class="project-dot">{{ p.record.name.slice(0,1).toUpperCase() }}</span><span class="ellipsis">{{ p.record.name }}</span></RouterLink></nav><div class="sidebar-foot">个人标记独立保存<br>来源文件只读</div></aside><main id="main" class="main" tabindex="-1"><div class="breadcrumb">个人工作空间 <span>/</span> {{ section }}</div><div v-if="expired" role="alert" class="notice error"><strong>会话已失效</strong><p>请从终端重新打开完整工作台链接。当前输入仍保留。</p></div><div v-else-if="error" role="alert" class="notice error">{{ error }}<p v-if="snapshot">显示上次快照：{{ dateLabel(snapshot.readAt) }}</p><Button variant="outline" :disabled="loading" @click="refresh">重试读取</Button></div><div v-if="loading && !snapshot" class="empty" role="status">正在读取本机项目…</div><RouterView v-if="snapshot" /><div v-else-if="!loading && !error && !expired" class="empty">尚未读取工作台</div><footer v-if="snapshot" class="page-footer">最近读取 {{ dateLabel(snapshot.readAt) }} · 个人进度不代表研发验收</footer></main></template>
