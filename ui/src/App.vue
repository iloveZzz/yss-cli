<script setup lang="ts">
import { computed, nextTick, onMounted, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useEventListener } from '@vueuse/core'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Empty, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Separator } from '@/components/ui/separator'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useQuestionReviewDeadline } from '@/hooks/useQuestionReviewDeadline'
import { failedRoute, pendingRoute, retryFailedRoute } from '@/router'
import { dateLabel } from '@/services/workbench'
const { snapshot, loading, error, expired, refresh, startChanges, stopChanges, checkChanges } =
  useWorkbench()
useQuestionReviewDeadline(() => {
  const value = snapshot.value
  if (!value || expired.value) return
  return { readAt: value.readAt, questions: value.specs.flatMap((spec) => spec.content.questions) }
}, refresh)
const route = useRoute()
const refreshAction = shallowRef<'header' | 'retry'>()
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
const routeReady = computed(() => route.matched.length > 0)
const availableRoute = computed(() =>
  routeReady.value && (!!snapshot.value || /^\/(settings|jobs)(\/|$)/.test(route.path))
)
const initialLoading = computed(() =>
  !availableRoute.value &&
  !expired.value &&
  !error.value &&
  !failedRoute.value &&
  (!routeReady.value || loading.value)
)
const initialLayout = computed(() => {
  const path = pendingRoute.value?.path ?? route.path
  if (/^\/(projects|specs|jobs)\//.test(path)) return 'detail'
  return path === '/' || path === '/workbench' ? 'cards' : 'table'
})
function focusMain() {
  document.getElementById('main')?.focus({ preventScroll: true })
}
async function refreshFrom(action: 'header' | 'retry', event?: MouseEvent) {
  if (loading.value || refreshAction.value || expired.value) return
  const returnFocus = document.activeElement === event?.currentTarget
  const currentPath = route.path
  refreshAction.value = action
  try {
    const recovered = await refresh()
    if (action === 'retry' && recovered && returnFocus) {
      await nextTick()
      if (route.path === currentPath && document.activeElement === document.body) focusMain()
    }
  } finally {
    refreshAction.value = undefined
  }
}
const isCurrent = (path: string) => route.path === path || route.path.startsWith(path + '/')
watch(() => route.path, (_path, previous) => {
  if (previous !== '/') focusMain()
}, { flush: 'post' })
onMounted(() => {
  if (!expired.value) {
    void refresh()
    startChanges()
  }
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
    <Badge class="local-label" variant="outline">
      <span aria-hidden="true">●</span>
      个人 · 本机
    </Badge>
    <Button class="refresh-action" variant="outline" :disabled="loading || expired" @click="refreshFrom('header')">
      <LoadingIndicator :active="refreshAction === 'header'" />
      {{ refreshAction === 'header' ? '刷新中…' : '刷新' }}
    </Button>
    <LoadingIndicator
      class="route-activity"
      :active="!!pendingRoute && availableRoute"
      label="正在打开页面…"
    />
  </header>
  <aside class="sidebar">
    <nav aria-label="主导航">
      <Button v-for="item in navigation" :key="item.path" as-child variant="ghost">
        <RouterLink :to="item.path" :class="{ 'section-active': isCurrent(item.path) }">
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
      </Button>
    </nav>
    <template v-if="recentProjects.length">
      <div class="nav-caption recent-caption">最近更新工程</div>
      <ScrollArea class="recent-projects">
        <nav aria-label="最近项目">
          <Button v-for="project in recentProjects" :key="project.record.id" as-child variant="ghost">
            <RouterLink :to="`/projects/${project.record.id}`">
              <span class="project-dot" aria-hidden="true">{{ project.record.name.slice(0, 1).toUpperCase() }}</span>
              <span class="ellipsis">{{ project.record.name }}</span>
            </RouterLink>
          </Button>
        </nav>
      </ScrollArea>
    </template>
    <div class="sidebar-foot">
      <Separator class="sidebar-separator" />
      数据保存在本机
      <br />
      Agent 使用隔离工作区
    </div>
  </aside>
  <main id="main" class="main" tabindex="-1">
    <Alert v-if="expired" variant="destructive" class="notice error">
      <AlertTitle>会话已失效</AlertTitle>
      <AlertDescription>请从终端重新打开完整工作台链接。当前输入仍保留。</AlertDescription>
    </Alert>
    <Alert v-else-if="error" variant="destructive" class="notice error">
      <AlertTitle>{{ error }}</AlertTitle>
      <AlertDescription v-if="snapshot">显示上次快照 {{ dateLabel(snapshot.readAt) }}</AlertDescription>
      <Button
        class="justify-self-start"
        variant="outline"
        :disabled="loading"
        @click="refreshFrom('retry', $event)"
      >
        <LoadingIndicator :active="refreshAction === 'retry'" />
        {{ refreshAction === 'retry' ? '重试中…' : '重试读取' }}
      </Button>
    </Alert>
    <Alert v-if="!expired && failedRoute" variant="destructive" class="notice error">
      <AlertTitle>页面加载失败</AlertTitle>
      <AlertDescription>可重新加载目标页面。</AlertDescription>
      <Button class="justify-self-start" variant="outline" @click="retryFailedRoute">重试打开页面</Button>
    </Alert>
    <div v-if="initialLoading" class="app-loading">
      <LoadingSkeleton :variant="initialLayout" :rows="3" label="正在读取本机工作台…" />
    </div>
    <RouterView v-if="availableRoute" />
    <Empty v-else-if="!initialLoading && !loading && !error && !expired && !failedRoute">
      <EmptyHeader><EmptyTitle>尚未读取工作台</EmptyTitle></EmptyHeader>
    </Empty>
    <footer v-if="snapshot" class="page-footer">最近读取 {{ dateLabel(snapshot.readAt) }}</footer>
  </main>
</template>

<style scoped>
.sidebar nav > a[data-slot='button'] { justify-content: flex-start; }
.sidebar nav > a.section-active {
  color: #315dcf;
  background: var(--yss-colors-primary-bg);
  font-weight: 600;
}
.sidebar-foot { border-top: 0; }
.sidebar-separator { margin-bottom: 12px; }
.recent-projects { min-height: 0; margin-bottom: 12px; }
.refresh-action { min-width: 84px; }
.app-loading { min-height: 14rem; padding-block: 10px 20px; }
.route-activity {
  position: absolute;
  top: calc(100% + 8px);
  right: 16px;
  padding: 6px 10px;
  border-radius: var(--brand-border-radius);
  pointer-events: none;
}
.route-activity[data-visible='true'] {
  background: var(--brand-color-bg-container);
  box-shadow: 0 0 0 1px var(--yss-colors-border), 0 3px 14px #17213617;
}
@media (width < 720px) {
  .recent-projects { display: none; }
  .local-label > span { display: none; }
}
</style>
