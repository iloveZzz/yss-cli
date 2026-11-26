<script setup lang="ts">
import { computed, nextTick, onMounted, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { useEventListener, useLocalStorage, useMediaQuery, usePreferredReducedMotion } from '@vueuse/core'
import { useRoute } from 'vue-router'
import { PanelLeftClose, PanelLeftOpen } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Empty, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Progress } from '@/components/ui/progress'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Separator } from '@/components/ui/separator'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { useDelayedBusy } from '@/hooks/useDelayedBusy'
import { useWorkbench } from '@/hooks/useWorkbench'
import { provideWorkbenchRouteReads, useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import { useQuestionReviewDeadline } from '@/hooks/useQuestionReviewDeadline'
import { failedRoute, pendingRoute, retryFailedRoute } from '@/router'
import { dateLabel } from '@/services/workbench'
import { useAgentScope } from '@/views/PiWorkspace/useAgentScope'
const { snapshot, loading, error, expired, refresh, startChanges, stopChanges, checkChanges, requireSnapshot, sourceState, changesError } =
  useWorkbench()
const routeReads = provideWorkbenchRouteReads()
const { catalog, refreshCatalog } = useWorkbenchCatalog()
const route = useRoute()
const lightweight = computed(() => route.meta.lightweight === true)
const specDetail = computed(() => route.meta.specDetail === true)
useQuestionReviewDeadline(() => {
  const value = snapshot.value
  if (!value || expired.value || lightweight.value) return
  return { readAt: value.readAt, questions: value.specs.flatMap((spec) => spec.content.questions) }
}, refresh)
const { scopeOptions, preferredSpecId } = useAgentScope()
const agentTarget = computed(() => {
  if (lightweight.value && !specDetail.value) return '/agent'
  const current = route.params.specId
  const specId = scopeOptions.value.find((option) => option.value === current)?.value ?? preferredSpecId.value
  return specId ? { name: 'agent-workbench', params: { specId } } : '/agent'
})
const piWorkspace = computed(() => route.meta.piWorkspace === true)
const desktop = useMediaQuery('(width >= 720px)')
const preferredMotion = usePreferredReducedMotion()
const collapsed = useLocalStorage('yss:workbench-sidebar-compact', false, { writeDefaults: false })
const compactNavigation = computed(() => desktop.value && collapsed.value)
const surface = useTemplateRef<HTMLElement>('surface')
let pageMotion: Animation | undefined
const refreshAction = shallowRef<'header' | 'retry'>()
const recentProjects = computed(() =>
  (lightweight.value ? catalog.value?.projects ?? [] : snapshot.value?.projects.map(({ record }) => record) ?? [])
    .filter((project) => project.active)
    .toSorted((a, b) => b.updatedAt.localeCompare(a.updatedAt))
    .slice(0, 6)
)
const navigation = [
  {
    path: '/workbench',
    label: '工作台',
    icon: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z'
  },
  { path: '/projects', label: '产品与工程', icon: 'M3 7h7l2 2h9v11H3z M3 7V4h7l2 3' },
  { path: '/specs', label: '规格管理', icon: 'M6 3h9l4 4v14H6z M14 3v5h5 M9 12h7 M9 16h7' },
  { path: '/agent', label: 'AI Agent', icon: 'M12 8V4H8 M6 8h12a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2v-8a2 2 0 0 1 2-2Z M2 14h2 M20 14h2 M9 13v2 M15 13v2' },
  { path: '/settings', label: '设置', icon: 'M4 7h16 M4 17h16 M8 4v6 M16 14v6' }
]
const routeReady = computed(() => route.matched.length > 0)
const snapshotTargetReady = computed(() => {
  const value = snapshot.value
  if (!value || !loading.value) return !!value
  const { projectId, specId } = route.params
  return (!projectId || value.projects.some((project) => project.record.id === projectId)) &&
    (!specId || value.specs.some((spec) => spec.record.id === specId))
})
const availableRoute = computed(() =>
  routeReady.value && (lightweight.value || snapshotTargetReady.value || piWorkspace.value || /^\/(settings|jobs)(\/|$)/.test(route.path))
)
const refreshLoading = computed(() => lightweight.value ? !!routeReads.value?.busy.value : loading.value)
const readAt = computed(() => lightweight.value ? routeReads.value?.readAt.value : snapshot.value?.readAt)
const navigationBusy = useDelayedBusy(() => !!pendingRoute.value && availableRoute.value, () => 160)
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
  if (refreshLoading.value || refreshAction.value || expired.value) return
  const returnFocus = document.activeElement === event?.currentTarget
  const currentPath = route.path
  refreshAction.value = action
  try {
    if (lightweight.value) {
      if (specDetail.value) await routeReads.value?.refresh()
      else await Promise.all([routeReads.value?.refresh(), refreshCatalog()])
      return
    }
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
watch([() => route.path, surface, preferredMotion], ([path, element, preference], [previousPath, previousElement]) => {
  pageMotion?.cancel()
  if (!element?.animate || preference === 'reduce' || (path === previousPath && element === previousElement)) return
  const tokens = getComputedStyle(element)
  pageMotion = element.animate([{ opacity: .58 }, { opacity: 1 }], {
    duration: Number.parseFloat(tokens.getPropertyValue('--workbench-motion-panel')) || 180,
    easing: tokens.getPropertyValue('--workbench-motion-ease').trim() || 'ease-out'
  })
}, { flush: 'post' })
onMounted(() => {
  if (!expired.value) startChanges()
})
watch([routeReady, lightweight], ([ready, compact]) => {
  requireSnapshot(ready && !compact)
}, { immediate: true })
watch([lightweight, () => sourceState.value?.revision], ([compact, revision]) => {
  if (compact && (!catalog.value || (revision !== undefined && revision > catalog.value.revision)))
    void refreshCatalog(revision)
}, { immediate: true })
onBeforeUnmount(() => {
  pageMotion?.cancel()
  stopChanges()
})
useEventListener(window, 'focus', checkChanges)
</script>
<template>
  <TooltipProvider :delay-duration="220">
  <div class="app-shell" :data-navigation-compact="compactNavigation">
  <a class="skip-link" href="#main" @click.prevent="focusMain">跳到主内容</a>
  <Transition name="workbench-feedback">
    <Progress v-if="navigationBusy" :model-value="null" aria-label="正在打开页面" class="workbench-route-progress" />
  </Transition>
  <header v-if="!piWorkspace" class="app-header">
    <Tooltip v-if="desktop">
      <TooltipTrigger as-child>
        <Button type="button" size="icon" variant="ghost" class="navigation-trigger"
          :aria-label="compactNavigation ? '展开主导航' : '收起主导航'" aria-controls="workbench-sidebar"
          :aria-expanded="!compactNavigation" @click="collapsed = !collapsed">
          <PanelLeftOpen v-if="compactNavigation" :size="17" aria-hidden="true" />
          <PanelLeftClose v-else :size="17" aria-hidden="true" />
        </Button>
      </TooltipTrigger>
      <TooltipContent side="bottom">{{ compactNavigation ? '展开主导航' : '收起主导航' }}</TooltipContent>
    </Tooltip>
    <RouterLink to="/workbench" class="brand">
      <span class="brand-mark">Y</span>
      YSS
      <span class="brand-sub">本机工作台</span>
    </RouterLink>
    <Badge class="local-label" variant="outline">
      <span aria-hidden="true">●</span>
      个人 · 本机
    </Badge>
    <Button class="refresh-action" variant="outline" :disabled="refreshLoading || expired" @click="refreshFrom('header')">
      <LoadingIndicator :active="refreshAction === 'header'" />
      {{ refreshAction === 'header' ? '刷新中…' : '刷新' }}
    </Button>
  </header>
  <aside v-if="!piWorkspace" id="workbench-sidebar" class="sidebar">
    <nav aria-label="主导航">
      <Tooltip v-for="item in navigation" :key="item.path">
        <TooltipTrigger as-child>
          <Button as-child variant="ghost">
            <RouterLink :to="item.path === '/agent' ? agentTarget : item.path" class="navigation-link" :class="{ 'section-active': isCurrent(item.path) }"
              :aria-current="isCurrent(item.path) ? (route.path === item.path ? 'page' : 'location') : undefined">
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
              <span class="navigation-label">{{ item.label }}</span>
            </RouterLink>
          </Button>
        </TooltipTrigger>
        <TooltipContent v-if="compactNavigation" side="right">{{ item.label }}</TooltipContent>
      </Tooltip>
    </nav>
    <template v-if="recentProjects.length">
      <div class="nav-caption recent-caption">最近更新工程</div>
      <ScrollArea class="recent-projects">
        <nav aria-label="最近项目">
          <Tooltip v-for="project in recentProjects" :key="project.id">
            <TooltipTrigger as-child>
              <Button as-child variant="ghost">
                <RouterLink :to="`/projects/${project.id}`" class="navigation-link">
                  <span class="project-dot" aria-hidden="true">{{ project.name.slice(0, 1).toUpperCase() }}</span>
                  <span class="ellipsis navigation-label">{{ project.name }}</span>
                </RouterLink>
              </Button>
            </TooltipTrigger>
            <TooltipContent v-if="compactNavigation" side="right">{{ project.name }}</TooltipContent>
          </Tooltip>
        </nav>
      </ScrollArea>
    </template>
    <div class="sidebar-foot">
      <Separator class="sidebar-separator" />
      数据保存在本机
      <br />
      Pi 可在已选原工程运行
    </div>
  </aside>
  <main id="main" :class="piWorkspace ? 'pi-main' : 'main'" tabindex="-1">
    <Alert v-if="expired" variant="destructive" class="notice error">
      <AlertTitle>会话已失效</AlertTitle>
      <AlertDescription>请从终端重新打开完整工作台链接。当前输入仍保留。</AlertDescription>
    </Alert>
    <Alert v-else-if="error && !piWorkspace && !lightweight" variant="destructive" class="notice error">
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
    <Alert v-if="!expired && changesError && !piWorkspace" class="notice warning" role="status">
      {{ changesError }}
    </Alert>
    <Alert v-if="!expired && failedRoute" variant="destructive" class="notice error">
      <AlertTitle>页面加载失败</AlertTitle>
      <AlertDescription>可重新加载目标页面。</AlertDescription>
      <Button class="justify-self-start" variant="outline" @click="retryFailedRoute">重试打开页面</Button>
    </Alert>
    <div v-if="initialLoading" class="app-loading">
      <LoadingSkeleton :variant="initialLayout" :rows="3" label="正在读取本机工作台…" />
    </div>
    <div v-if="availableRoute && (!piWorkspace || !expired)" ref="surface" class="workbench-view">
      <RouterView />
    </div>
    <Empty v-else-if="!initialLoading && !loading && !error && !expired && !failedRoute">
      <EmptyHeader><EmptyTitle>尚未读取工作台</EmptyTitle></EmptyHeader>
    </Empty>
    <footer v-if="readAt && !piWorkspace" class="page-footer">最近读取 {{ dateLabel(readAt) }}</footer>
  </main>
  </div>
  </TooltipProvider>
</template>

<style scoped>
.sidebar .navigation-link { justify-content: flex-start; }
.sidebar nav > a.section-active {
  color: #315dcf;
  background: var(--yss-colors-primary-bg);
  font-weight: 600;
}
.sidebar-foot { border-top: 0; }
.sidebar-separator { margin-bottom: 12px; }
.recent-projects { min-height: 0; margin-bottom: 12px; }
.navigation-trigger { flex: none; width: 34px; padding: 0; }
.refresh-action { min-width: 84px; }
.app-loading { min-height: 14rem; padding-block: 10px 20px; }
.workbench-view { min-width: 0; }
.workbench-route-progress[data-slot='progress'] {
  position: fixed;
  inset: 0 0 auto;
  z-index: 45;
  height: 3px;
  border-radius: 0;
  pointer-events: none;
}
.workbench-route-progress :deep([data-slot='progress-indicator']) {
  width: 29%;
  opacity: .85;
}
@media (width >= 720px) {
  .app-shell[data-navigation-compact='true'] { --sidebar-width: 62px; }
  .sidebar nav > a > svg { flex: none; }
  .navigation-label { max-width: calc(var(--sidebar-width) - 48px); overflow: hidden; white-space: nowrap; }
  .app-shell[data-navigation-compact='true'] .sidebar .navigation-link {
    justify-content: center;
    gap: 0;
    padding-inline: 0;
  }
  .app-shell[data-navigation-compact='true'] :is(.recent-caption, .sidebar-foot) { display: none; }
  .app-shell[data-navigation-compact='true'] .navigation-label { max-width: 0; opacity: 0; }
}
@media (720px <= width < 900px) {
  .app-shell { --sidebar-width: 168px; }
}
@media (width >= 720px) and (prefers-reduced-motion: no-preference) {
  .app-shell .sidebar { transition: width var(--workbench-motion-panel) var(--workbench-motion-ease); }
  .app-shell .main { transition: margin-inline-start var(--workbench-motion-panel) var(--workbench-motion-ease); }
  .navigation-label { transition: opacity var(--workbench-motion-fast), max-width var(--workbench-motion-panel) var(--workbench-motion-ease); }
}
@media (width < 720px) {
  .recent-projects { display: none; }
  .local-label > span { display: none; }
}
</style>
