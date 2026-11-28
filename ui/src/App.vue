<script setup lang="ts">
import { computed, nextTick, onMounted, onBeforeUnmount, shallowRef, useTemplateRef, watch } from 'vue'
import { useEventListener, useMediaQuery, usePreferredReducedMotion } from '@vueuse/core'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import WorkbenchRail from '@/components/WorkbenchRail.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Empty, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Progress } from '@/components/ui/progress'
import { TooltipProvider } from '@/components/ui/tooltip'
import { useDelayedBusy } from '@/hooks/useDelayedBusy'
import { useWorkbench } from '@/hooks/useWorkbench'
import { provideWorkbenchRefreshAction, provideWorkbenchRouteReads, useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import { useQuestionReviewDeadline } from '@/hooks/useQuestionReviewDeadline'
import { failedRoute, pendingRoute, retryFailedRoute } from '@/router'
import { dateLabel } from '@/services/workbench'
import { requestWorkbenchReadRefresh } from '@/services/workbench-read'
import { desktopDraftError, flushDesktopDrafts } from '@/services/desktop-storage'
import { initialTerminalLayout, reportTerminalRailWidth, terminalPanelOpenUrl, workbenchTerminalLayout } from '@/services/terminal-panel'
import { sorted } from '@/lib/collections'
import { withTimeout } from '@/lib/abort'
import { useAgentScope } from '@/views/PiWorkspace/useAgentScope'
const { snapshot, loading, error, expired, refresh, startChanges, stopChanges, checkChanges, requireSnapshot, sourceState, changesError } =
  useWorkbench()
const routeReads = provideWorkbenchRouteReads()
const { catalog, catalogLoading, catalogError, refreshCatalog } = useWorkbenchCatalog()
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
const terminalOpen = terminalPanelOpenUrl()
const nativeLayout = shallowRef(terminalOpen ? initialTerminalLayout() : undefined)
const terminalMode = computed(() => nativeLayout.value?.mode ?? 'hidden')
const terminalMaximized = computed(() => terminalMode.value === 'maximized')
const canExpandBrowserRail = useMediaQuery('(min-width: 720px)')
const wideBrowserRail = useMediaQuery('(min-width: 880px)')
const canExpandRail = computed(() => nativeLayout.value?.railViewport?.canExpand ?? canExpandBrowserRail.value)
const defaultRailCollapsed = computed(() => nativeLayout.value?.railViewport?.defaultCollapsed ?? !wideBrowserRail.value)
const railPreference = shallowRef<boolean>()
const railCollapsed = computed(() => !canExpandRail.value || (railPreference.value ?? defaultRailCollapsed.value))
const isHome = computed(() => route.path === '/workbench')
const preferredMotion = usePreferredReducedMotion()
// Resolving the Agent entry to a Spec keeps the same animated surface.
const pageMotionKey = computed(() => piWorkspace.value ? 'pi-workspace' : route.path)
const surface = useTemplateRef<HTMLElement>('surface')
let pageMotion: Animation | undefined
let railObserver: ResizeObserver | undefined
let railReportFrame = 0
let reportedRailWidth: number | undefined
const resumeRefresh = new AbortController()
let lastResumeRefresh = -Infinity
const refreshAction = shallowRef<{ type: 'manual' | 'retry' }>()
const projects = computed(() => {
  if (expired.value) return []
  if (catalog.value && (lightweight.value || catalog.value.revision >= (snapshot.value?.revision ?? 0)))
    return catalog.value.projects
  const records = snapshot.value?.projects.map(({ record }) => record)
  return records ?? []
})
const currentProject = computed(() => projects.value.find((project) => project.id === route.params.projectId))
const recentProjects = computed(() =>
  sorted(projects.value.filter((project) => project.active), (a, b) => b.updatedAt.localeCompare(a.updatedAt))
    .slice(0, 6)
)
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
const refreshLoading = computed(() => !!routeReads.value?.busy.value || (!lightweight.value && loading.value))
const refreshDisabled = computed(() => refreshLoading.value || !!refreshAction.value || expired.value)
const manualRefreshBusy = computed(() => refreshAction.value?.type === 'manual')
provideWorkbenchRefreshAction({ busy: manualRefreshBusy, disabled: refreshDisabled, refresh: refreshCurrentPage })
const readAt = computed(() => {
  if (routeReads.value || lightweight.value) return routeReads.value?.readAt.value
  return snapshot.value?.readAt
})
const navigationBusy = useDelayedBusy(() => !!pendingRoute.value && availableRoute.value, () => 160)
const showPageRefresh = useDelayedBusy(() => manualRefreshBusy.value && !expired.value && !piWorkspace.value && !isHome.value, () => 180)
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
  if (!terminalMaximized.value) document.getElementById('main')?.focus({ preventScroll: true })
}
function acceptTerminalLayout(layout: ReturnType<typeof workbenchTerminalLayout>) {
  const current = nativeLayout.value?.revision
  if (!layout || (current !== undefined && (layout.revision ?? 0) <= current)) return
  nativeLayout.value = layout
}
function reportRail() {
  if (railReportFrame || !terminalOpen) return
  railReportFrame = window.requestAnimationFrame(() => {
    railReportFrame = 0
    const rail = document.getElementById('workbench-rail')
    if (!rail && !piWorkspace.value) return
    const width = Math.ceil(rail?.getBoundingClientRect().width ?? 0)
    if (reportedRailWidth !== width && reportTerminalRailWidth(width)) reportedRailWidth = width
  })
}
function observeRail() {
  if (!railObserver) return
  const rail = document.getElementById('workbench-rail')
  railObserver.disconnect()
  if (rail) railObserver.observe(rail)
  reportRail()
}
function refreshCurrentPage() {
  return refreshFrom('manual')
}
async function refreshFrom(action: 'manual' | 'retry', event?: MouseEvent) {
  if (refreshDisabled.value) return
  const returnFocus = document.activeElement === event?.currentTarget
  const owner = { type: action }
  refreshAction.value = owner
  try {
    if (lightweight.value) {
      if (specDetail.value || route.meta.projectDetail === true) await routeReads.value?.refresh()
      else await Promise.all([routeReads.value?.refresh(), refreshCatalog()])
      return
    }
    if (action === 'manual') {
      await Promise.all([refresh(), routeReads.value?.refresh()])
      return
    }
    const recovered = await refresh()
    if (recovered && returnFocus) {
      await nextTick()
      if (refreshAction.value === owner && document.activeElement === document.body) focusMain()
    }
  } finally {
    if (refreshAction.value === owner) refreshAction.value = undefined
  }
}
watch(() => route.path, (_path, previous) => {
  refreshAction.value = undefined
  if (previous !== '/') focusMain()
}, { flush: 'post' })
watch([pageMotionKey, surface, preferredMotion], ([key, element, preference], [previousKey, previousElement]) => {
  pageMotion?.cancel()
  pageMotion = undefined
  // Only animate navigation within the mounted surface. First paint and loaded data show immediately.
  if (!element?.animate || element !== previousElement || preference !== 'no-preference' || key === previousKey) return
  const enteringAgent = piWorkspace.value
  const keyframes = enteringAgent
    ? [{ opacity: .25, scale: .99 }, { opacity: 1, scale: 1 }]
    : [{ opacity: .9 }, { opacity: 1 }]
  pageMotion = element.animate(keyframes, {
    duration: enteringAgent ? 220 : 145,
    easing: 'cubic-bezier(.2, .72, .24, 1)'
  })
}, { flush: 'post' })
onMounted(() => {
  if (!expired.value) startChanges()
  if (terminalOpen) {
    acceptTerminalLayout(initialTerminalLayout())
    railObserver = new ResizeObserver(reportRail)
    observeRail()
  }
})
watch(piWorkspace, observeRail, { flush: 'post' })
watch([routeReady, lightweight], ([ready, compact]) => {
  requireSnapshot(ready && !compact)
}, { immediate: true })
watch([routeReady, () => sourceState.value?.revision], ([ready, revision]) => {
  if (ready && (!catalog.value || (revision !== undefined && revision > catalog.value.revision)))
    void refreshCatalog(revision)
}, { immediate: true })
onBeforeUnmount(() => {
  pageMotion?.cancel()
  railObserver?.disconnect()
  if (railReportFrame) window.cancelAnimationFrame(railReportFrame)
  resumeRefresh.abort()
  stopChanges()
})
function onWorkbenchResume() {
  if (document.visibilityState !== 'visible' || expired.value) return
  void checkChanges()
  if (!routeReady.value || (!isHome.value && route.path !== '/specs' && !specDetail.value) ||
    performance.now() - lastResumeRefresh < 5_000) return
  lastResumeRefresh = performance.now()
  const request = withTimeout(resumeRefresh.signal, 5_000)
  void requestWorkbenchReadRefresh(request.signal)
    .catch(() => { /* The regular background observation also checks external changes. */ })
    .finally(request.dispose)
}
useEventListener(window, 'focus', onWorkbenchResume)
useEventListener(document, 'visibilitychange', onWorkbenchResume)
useEventListener(window, 'yss:terminal-layout', (event) => {
  if (!terminalOpen || !(event instanceof CustomEvent)) return
  acceptTerminalLayout(workbenchTerminalLayout(event.detail))
})
</script>
<template>
  <TooltipProvider :delay-duration="220">
    <div class="app-shell" :data-rail-collapsed="railCollapsed" :data-terminal-mode="terminalMode">
      <a v-show="!terminalMaximized" class="skip-link" href="#main" @click.prevent="focusMain">跳到主内容</a>
      <WorkbenchRail
        v-if="!piWorkspace"
        :collapsed="railCollapsed"
        :can-toggle="canExpandRail"
        :recent-projects="recentProjects"
        :current-project="currentProject"
        :projects-loading="!expired && (catalogLoading || (!catalog && !snapshot && !catalogError))"
        :projects-error="!expired && !!catalogError && !catalog && !snapshot"
        :agent-target="agentTarget"
        :show-refresh="!isHome"
        :refresh-busy="manualRefreshBusy"
        :refresh-disabled="refreshDisabled"
        :terminal-url="terminalOpen"
        :terminal-visible="terminalMode !== 'hidden'"
        @refresh="refreshCurrentPage"
        @retry-projects="refreshCatalog()"
        @toggle-collapse="railPreference = !railCollapsed"
      />
      <main
        id="main"
        :class="piWorkspace ? 'pi-main' : 'main'"
        :inert="terminalMaximized"
        :aria-hidden="terminalMaximized ? 'true' : undefined"
        tabindex="-1"
      >
        <Alert v-if="desktopDraftError" variant="destructive" class="notice error" role="alert">
          <AlertTitle>{{ desktopDraftError }}</AlertTitle>
          <Button type="button" variant="outline" @click="flushDesktopDrafts">重试保存</Button>
        </Alert>
        <Transition name="workbench-feedback">
          <Progress v-if="navigationBusy" :model-value="null" aria-label="正在打开页面" class="workbench-route-progress" />
        </Transition>
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
            :disabled="refreshDisabled"
            @click="refreshFrom('retry', $event)"
          >
            <LoadingIndicator :active="refreshAction?.type === 'retry'" />
            {{ refreshAction?.type === 'retry' ? '重试中…' : '重试读取' }}
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
        <div v-if="showPageRefresh" class="page-refresh-feedback" role="status">
          <LoadingIndicator :active="true" :delay="0" />
          正在刷新当前页面…
        </div>
        <div v-if="initialLoading" class="app-loading">
          <LoadingSkeleton :variant="initialLayout" :rows="3" label="正在读取本机工作台…" />
        </div>
        <div v-if="availableRoute && (!piWorkspace || !expired)" ref="surface" class="workbench-view">
          <RouterView />
        </div>
        <Empty v-else-if="!initialLoading && !loading && !error && !expired && !failedRoute">
          <EmptyHeader><EmptyTitle>尚未读取工作台</EmptyTitle></EmptyHeader>
        </Empty>
        <footer v-if="readAt && !piWorkspace && !isHome" class="page-footer">最近读取 {{ dateLabel(readAt) }}</footer>
      </main>
    </div>
  </TooltipProvider>
</template>

<style scoped>
.app-shell[data-rail-collapsed='true'] { --workbench-rail-width: 62px; }
.app-shell[data-terminal-mode='maximized'] > main { visibility: hidden; }
@media (prefers-reduced-motion: no-preference) {
  .app-shell > .main {
    transition: margin-inline-start var(--workbench-motion-panel) var(--workbench-motion-ease);
  }
  .main .workbench-route-progress {
    transition: inset-inline-start var(--workbench-motion-panel) var(--workbench-motion-ease);
  }
}
.app-loading { min-height: 14rem; padding-block: 10px 20px; }
.workbench-view { min-width: 0; }
.page-refresh-feedback {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-block-end: 10px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.workbench-route-progress[data-slot='progress'] {
  position: fixed;
  inset: 0 0 auto;
  z-index: 45;
  height: 3px;
  border-radius: 0;
  pointer-events: none;
}
.main .workbench-route-progress { inset-inline-start: var(--workbench-rail-width); }
.workbench-route-progress :deep([data-slot='progress-indicator']) {
  width: 29%;
  opacity: .85;
}
</style>
