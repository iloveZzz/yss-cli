<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount } from 'vue'
import { useEventListener } from '@vueuse/core'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { useWorkbench } from '@/hooks/useWorkbench'
import { dateLabel } from '@/services/workbench'
const { snapshot, loading, error, expired, refresh, startChanges, stopChanges, checkChanges } =
  useWorkbench()
const route = useRoute()
const recentProjects = computed(() =>
  (snapshot.value?.projects ?? [])
    .filter((project) => project.record.active)
    .toSorted((a, b) => b.record.updatedAt.localeCompare(a.record.updatedAt))
    .slice(0, 6)
)
const navigation = [
  {
    path: '/workbench',
    label: '工作台',
    icon: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z'
  },
  { path: '/projects', label: '产品与工程', icon: 'M3 7h7l2 2h9v11H3z M3 7V4h7l2 3' },
  { path: '/specs', label: 'Spec', icon: 'M6 3h9l4 4v14H6z M14 3v5h5 M9 12h7 M9 16h7' },
  { path: '/jobs', label: '执行记录', icon: 'M4 5h16v15H4z M8 3v4 M16 3v4 M8 12l2 2 5-5 M8 17h8' },
  { path: '/settings', label: '设置', icon: 'M4 7h16 M4 17h16 M8 4v6 M16 14v6' }
]
const availableRoute = computed(() => !!snapshot.value || /^\/(settings|jobs)/.test(route.path))
function focusMain() {
  document.getElementById('main')?.focus()
}
onMounted(() => {
  void refresh()
  startChanges()
})
onBeforeUnmount(stopChanges)
useEventListener(window, 'focus', checkChanges)
</script>
<template>
  <a class="skip-link" href="#main" @click.prevent="focusMain">跳到主内容</a>
  <header class="app-header">
    <RouterLink to="/workbench" class="brand">
      <span class="brand-mark">Y</span>
      YSS
      <span class="brand-sub">本机工作台</span>
    </RouterLink>
    <span class="local-label">
      <span aria-hidden="true">●</span>
      个人 · 本机
    </span>
    <Button variant="outline" :disabled="loading || expired" @click="refresh">
      {{ loading ? '刷新中…' : '刷新' }}
    </Button>
  </header>
  <aside class="sidebar">
    <nav aria-label="主导航">
      <RouterLink v-for="item in navigation" :key="item.path" :to="item.path">
        <svg
          width="17"
          height="17"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path :d="item.icon" />
        </svg>
        <span>{{ item.label }}</span>
      </RouterLink>
    </nav>
    <div class="nav-caption recent-caption">最近更新工程</div>
    <nav aria-label="最近项目">
      <RouterLink
        v-for="project in recentProjects"
        :key="project.record.id"
        :to="`/projects/${project.record.id}`"
      >
        <span class="project-dot">{{ project.record.name.slice(0, 1).toUpperCase() }}</span>
        <span class="ellipsis">{{ project.record.name }}</span>
      </RouterLink>
    </nav>
    <div class="sidebar-foot">
      数据保存在本机
      <br />
      Agent 使用隔离工作区
    </div>
  </aside>
  <main id="main" class="main" tabindex="-1">
    <div v-if="expired" role="alert" class="notice error">
      <strong>会话已失效</strong>
      <p>请从终端重新打开完整工作台链接。当前输入仍保留。</p>
    </div>
    <div v-else-if="error" role="alert" class="notice error">
      {{ error }}
      <p v-if="snapshot">显示上次快照 {{ dateLabel(snapshot.readAt) }}</p>
      <Button variant="outline" :disabled="loading" @click="refresh">重试读取</Button>
    </div>
    <div v-if="loading && !snapshot" class="empty" role="status">正在读取本机工程…</div>
    <RouterView v-if="availableRoute" />
    <div v-else-if="!loading && !error && !expired" class="empty">尚未读取工作台</div>
    <footer v-if="snapshot" class="page-footer">最近读取 {{ dateLabel(snapshot.readAt) }}</footer>
  </main>
</template>
