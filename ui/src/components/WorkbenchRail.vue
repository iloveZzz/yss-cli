<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { useRoute, type RouteLocationRaw } from 'vue-router'
import { Check, ChevronDown, FileText, FolderClosed, Layers3, LayoutDashboard, Monitor, RefreshCw, Settings, Sparkles } from '@lucide/vue'
import ControlTooltip from '@/components/ControlTooltip.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Separator } from '@/components/ui/separator'

interface RailProject {
  id: string
  name: string
}

const props = defineProps<{
  recentProjects: readonly RailProject[]
  currentProject?: RailProject
  agentTarget: RouteLocationRaw
  showRefresh: boolean
  refreshBusy: boolean
  refreshDisabled: boolean
}>()
const emit = defineEmits<{ refresh: [] }>()
const route = useRoute()
const navigation = [
  { path: '/workbench', label: '工作台', short: '工作台', icon: LayoutDashboard },
  { path: '/projects', label: '产品与工程', short: '工程', icon: FolderClosed },
  { path: '/specs', label: '规格管理', short: '规格', icon: FileText },
  { path: '/agent', label: 'AI Agent', short: 'Agent', icon: Sparkles },
  { path: '/settings', label: '设置', short: '设置', icon: Settings },
] as const
const projectMenuLabel = computed(() => props.currentProject
  ? '打开工程菜单，当前工程是' + props.currentProject.name
  : '打开工程菜单')

type MenuName = 'user' | 'project'
const menuTriggers = {
  user: useTemplateRef<InstanceType<typeof Button>>('userTrigger'),
  project: useTemplateRef<InstanceType<typeof Button>>('projectTrigger'),
}
const openedMenus: Partial<Record<MenuName, { path: string; linkSelected: boolean }>> = {}

function indicateCurrent(path: string) {
  if (route.path === path) return 'page'
  if (route.path.startsWith(path + '/') || (path === '/workbench' && /^\/jobs(?:\/|$)/.test(route.path))) return 'location'
}

function projectInitial(name: string) {
  return Array.from(name.trim())[0]?.toUpperCase() || '工'
}

function projectLocation(project: RailProject) {
  return '/projects/' + encodeURIComponent(project.id)
}

function recordOpening(menu: MenuName, open: boolean) {
  if (open) openedMenus[menu] = { path: route.path, linkSelected: false }
}

function recordLinkSelection(menu: MenuName) {
  const opened = openedMenus[menu]
  if (opened) opened.linkSelected = true
}

function restoreMenuFocus(menu: MenuName, event: Event) {
  const opened = openedMenus[menu]
  if (!opened) return
  if (opened.path !== route.path) {
    event.preventDefault()
    return
  }
  if (!opened.linkSelected) return
  event.preventDefault()
  // A link may commit its route before Reka's deferred focus restoration runs.
  setTimeout(() => {
    if (openedMenus[menu] !== opened || opened.path !== route.path || document.activeElement !== document.body) return
    const trigger: unknown = menuTriggers[menu].value?.$el
    if (trigger instanceof HTMLElement) trigger.focus({ preventScroll: true })
  }, 0)
}
</script>

<template>
  <aside class="workbench-rail" aria-label="工作台侧栏">
    <div class="rail-top">
      <DropdownMenu :modal="false" @update:open="recordOpening('user', $event)">
        <DropdownMenuTrigger as-child>
          <Button ref="userTrigger" variant="ghost" class="rail-link identity-link" aria-label="本机用户菜单">
            <span class="rail-mark user-mark" aria-hidden="true">本</span>
            <span>用户</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          class="workbench-rail-menu workbench-rail-user-menu"
          side="right"
          align="start"
          :side-offset="8"
          :collision-padding="8"
          @close-auto-focus="restoreMenuFocus('user', $event)"
        >
          <DropdownMenuLabel class="user-summary">
            <span class="rail-mark user-mark" aria-hidden="true">本</span>
            <span>本机用户<small>个人 · 本机</small></span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem as-child class="rail-menu-item" @select="recordLinkSelection('user')">
            <RouterLink to="/settings"><Monitor aria-hidden="true" />本机设置</RouterLink>
          </DropdownMenuItem>
          <DropdownMenuItem
            v-if="showRefresh"
            class="rail-menu-item"
            :disabled="refreshDisabled || refreshBusy"
            @select="emit('refresh')"
          >
            <LoadingIndicator v-if="refreshBusy" active />
            <RefreshCw v-else aria-hidden="true" />
            {{ refreshBusy ? '正在刷新当前页面…' : '刷新当前页面' }}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <DropdownMenu :modal="false" @update:open="recordOpening('project', $event)">
        <DropdownMenuTrigger as-child>
          <Button
            ref="projectTrigger"
            variant="ghost"
            class="rail-link identity-link"
            :aria-label="projectMenuLabel"
            :title="currentProject?.name ?? '切换工程'"
          >
            <span class="rail-mark switcher-mark" aria-hidden="true">
              <template v-if="currentProject">{{ projectInitial(currentProject.name) }}</template>
              <Layers3 v-else class="size-4" />
            </span>
            <span class="switcher-label">切换<ChevronDown class="size-[10px]" aria-hidden="true" /></span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          class="workbench-rail-menu workbench-rail-project-menu"
          side="right"
          align="start"
          :side-offset="8"
          :collision-padding="8"
          @close-auto-focus="restoreMenuFocus('project', $event)"
        >
          <DropdownMenuLabel class="rail-menu-caption">最近工程</DropdownMenuLabel>
          <DropdownMenuLabel v-if="!recentProjects.length" class="rail-menu-caption empty-projects">暂无最近工程</DropdownMenuLabel>
          <DropdownMenuItem v-for="project in recentProjects" :key="project.id" as-child class="rail-menu-item" @select="recordLinkSelection('project')">
            <RouterLink :to="projectLocation(project)" :aria-current="currentProject?.id === project.id ? 'page' : undefined">
              <span class="rail-mark project-mark" aria-hidden="true">{{ projectInitial(project.name) }}</span>
              <span class="project-name">{{ project.name }}</span>
              <Check v-if="currentProject?.id === project.id" class="current-project-check size-4" aria-hidden="true" />
            </RouterLink>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem as-child class="rail-menu-item" @select="recordLinkSelection('project')">
            <RouterLink to="/projects"><FolderClosed aria-hidden="true" />查看全部工程</RouterLink>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>

    <Separator class="rail-divider" decorative />

    <nav class="rail-nav" aria-label="主导航">
      <ControlTooltip v-for="item in navigation" :key="item.path" :label="item.label" side="right">
        <Button variant="ghost" as-child>
          <RouterLink
            :to="item.path === '/agent' ? agentTarget : item.path"
            class="rail-link"
            :class="{ 'rail-settings': item.path === '/settings' }"
            :aria-label="item.label"
            :aria-current="indicateCurrent(item.path)"
          >
            <component :is="item.icon" class="size-[17px]" :stroke-width="1.7" aria-hidden="true" />
            <span>{{ item.short }}</span>
          </RouterLink>
        </Button>
      </ControlTooltip>
    </nav>
    <span class="rail-local"><span aria-hidden="true"></span>本机</span>
  </aside>
</template>

<style scoped>
.workbench-rail {
  position: fixed;
  z-index: 20;
  inset-block: 0;
  inset-inline-start: 0;
  inline-size: var(--workbench-rail-width);
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-block: 11px 12px;
  overflow-y: auto;
  overflow-x: hidden;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  border-inline-end: 1px solid var(--yss-colors-border);
  background: var(--workbench-glass-chrome);
  -webkit-backdrop-filter: var(--workbench-glass-blur);
  backdrop-filter: var(--workbench-glass-blur);
}
.rail-top, .rail-nav {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  inline-size: 100%;
}
.rail-top, .rail-divider, .rail-local { flex: none; }
.rail-divider[data-slot='separator'] {
  inline-size: 30px;
  margin-block: 9px;
}
.rail-nav { flex: 1 0 auto; }
.workbench-rail .rail-link {
  position: relative;
  display: grid;
  place-content: center;
  justify-items: center;
  gap: 3px;
  inline-size: 46px;
  block-size: 46px;
  padding: 2px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  line-height: 1.2;
  text-decoration: none;
}
.workbench-rail .identity-link { block-size: 49px; }
.workbench-rail .rail-link:hover, .rail-link[aria-expanded='true'] {
  color: var(--brand-color-text);
  background: var(--brand-color-fill-secondary);
}
.workbench-rail .rail-link[aria-current] {
  color: #315dcf;
  background: var(--yss-colors-primary-bg);
  font-weight: 650;
}
.rail-link[aria-current]::before {
  content: '';
  position: absolute;
  inset-inline-start: -7px;
  inset-block: 11px;
  inline-size: 2px;
  border-radius: 0 3px 3px 0;
  background: var(--yss-color-primary-control);
}
.rail-settings { margin-block-start: auto; }
.rail-mark {
  display: grid;
  flex: none;
  place-items: center;
  inline-size: 27px;
  block-size: 27px;
  overflow: hidden;
  border: 1px solid var(--yss-colors-border);
  border-radius: var(--brand-border-radius-sm);
  color: var(--brand-color-text-secondary);
  background: var(--brand-color-fill-secondary);
  font-size: 12px;
  font-weight: 650;
}
.user-mark {
  color: var(--yss-colors-text-title);
  background: var(--brand-color-bg-container);
}
.switcher-mark {
  color: #315dcf;
  background: var(--yss-colors-primary-bg);
}
.switcher-label, .rail-local {
  display: flex;
  align-items: center;
  gap: 2px;
}
.rail-local {
  gap: 4px;
  margin-block-start: 8px;
  color: var(--brand-color-text-secondary);
  font-size: 10px;
}
.rail-local > span {
  inline-size: 6px;
  block-size: 6px;
  border-radius: 50%;
  background: #238264;
}
:global(.workbench-rail-menu) {
  min-inline-size: 0;
  max-inline-size: min(
    var(--reka-dropdown-menu-content-available-width, 100vw),
    calc(100vw - var(--workbench-rail-width) - 16px)
  );
  overscroll-behavior: contain;
}
:global(.workbench-rail-project-menu) { inline-size: 248px; }
:global(.workbench-rail-user-menu) { inline-size: 186px; }
.rail-menu-caption { color: var(--brand-color-text-secondary); font-size: 11px; }
.empty-projects { font-weight: 400; }
.rail-menu-item { min-block-size: 34px; }
.rail-menu-item[aria-current='page'] { font-weight: 650; }
.user-summary { display: flex; align-items: center; gap: 8px; }
.user-summary small { display: block; color: var(--brand-color-text-secondary); font-weight: 400; }
.project-name { min-inline-size: 0; overflow-wrap: anywhere; }
.rail-menu-item .current-project-check { margin-inline-start: auto; color: #315dcf; }

@media (width < 720px) {
  .rail-menu-item { min-block-size: 44px; }
}
@media (forced-colors: active) {
  .workbench-rail { border-inline-end-color: CanvasText; }
  .rail-link[aria-current], .rail-menu-item[data-highlighted] {
    outline: 2px solid Highlight;
    outline-offset: -2px;
  }
  .rail-link:focus-visible { outline-color: Highlight; }
}
</style>
