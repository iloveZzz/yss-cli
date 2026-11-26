<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { useRoute, type RouteLocationRaw } from 'vue-router'
import { Check, ChevronsUpDown, CircleUserRound, FileText, FolderClosed, FolderOpen, LayoutDashboard, Monitor, MonitorCog, RefreshCw, Settings, Sparkles } from '@lucide/vue'
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
  projectsLoading: boolean
  projectsError: boolean
  agentTarget: RouteLocationRaw
  showRefresh: boolean
  refreshBusy: boolean
  refreshDisabled: boolean
}>()
const emit = defineEmits<{ refresh: []; retryProjects: [] }>()
const route = useRoute()
const desktopSettingsUrl = (window as Window & { __YSS_DESKTOP_SETTINGS_URL__?: string }).__YSS_DESKTOP_SETTINGS_URL__
const navigation = [
  { path: '/workbench', label: '工作台', short: '工作台', icon: LayoutDashboard },
  { path: '/projects', label: '产品与工程', short: '工程', icon: FolderClosed },
  { path: '/specs', label: '规格管理', short: '规格', icon: FileText },
  { path: '/agent', label: 'AI Agent', short: 'Agent', icon: Sparkles },
] as const
const projectScopeName = computed(() => props.currentProject?.name || '全部工程')

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
      <DropdownMenu :modal="false" @update:open="recordOpening('project', $event)">
        <DropdownMenuTrigger as-child>
          <Button
            ref="projectTrigger"
            variant="ghost"
            class="rail-link rail-switcher"
            :aria-label="`切换工程 · ${projectScopeName}`"
            :title="currentProject?.name"
          >
            <FolderOpen class="size-[17px]" :stroke-width="1.7" aria-hidden="true" />
            <span class="rail-label-wide scope-name">{{ projectScopeName }}</span>
            <span class="rail-label-compact">切换工程</span>
            <ChevronsUpDown class="scope-chevron size-[14px]" :stroke-width="1.7" aria-hidden="true" />
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
          <DropdownMenuLabel v-if="recentProjects.length" class="rail-menu-caption">最近工程</DropdownMenuLabel>
          <DropdownMenuLabel v-else-if="projectsLoading" class="rail-menu-caption empty-projects">正在读取工程…</DropdownMenuLabel>
          <DropdownMenuItem v-else-if="projectsError" class="rail-menu-item" @select="emit('retryProjects')">
            <RefreshCw aria-hidden="true" />重试读取工程
          </DropdownMenuItem>
          <DropdownMenuLabel v-else class="rail-menu-caption empty-projects">暂无最近工程</DropdownMenuLabel>
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
            :aria-label="item.label"
            :aria-current="indicateCurrent(item.path)"
          >
            <component :is="item.icon" class="size-[17px]" :stroke-width="1.7" aria-hidden="true" />
            <span class="rail-label-wide">{{ item.label }}</span>
            <span class="rail-label-compact">{{ item.short }}</span>
          </RouterLink>
        </Button>
      </ControlTooltip>
    </nav>

    <div class="rail-actions">
      <ControlTooltip v-if="desktopSettingsUrl" label="桌面设置" side="right">
        <Button variant="ghost" as-child>
          <a :href="desktopSettingsUrl" class="rail-link" draggable="false">
            <MonitorCog class="size-[17px]" :stroke-width="1.7" aria-hidden="true" />
            <span>桌面设置</span>
          </a>
        </Button>
      </ControlTooltip>
      <ControlTooltip label="本机设置" side="right">
        <Button variant="ghost" as-child>
          <RouterLink to="/settings" class="rail-link" :aria-current="indicateCurrent('/settings')">
            <Settings class="size-[17px]" :stroke-width="1.7" aria-hidden="true" />
            <span>本机设置</span>
          </RouterLink>
        </Button>
      </ControlTooltip>
      <Separator class="rail-divider rail-divider-user" decorative />
      <DropdownMenu :modal="false" @update:open="recordOpening('user', $event)">
        <DropdownMenuTrigger as-child>
          <Button ref="userTrigger" variant="ghost" class="rail-link" aria-label="本机用户菜单">
            <CircleUserRound class="size-[17px]" :stroke-width="1.7" aria-hidden="true" />
            <span>本机用户</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          class="workbench-rail-menu workbench-rail-user-menu"
          side="right"
          align="end"
          :side-offset="8"
          :collision-padding="8"
          @close-auto-focus="restoreMenuFocus('user', $event)"
        >
          <DropdownMenuLabel class="user-summary"><CircleUserRound class="size-4" aria-hidden="true" />本机用户</DropdownMenuLabel>
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
    </div>
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
  padding: 12px 8px;
  overflow-y: auto;
  overflow-x: hidden;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  border-inline-end: 1px solid var(--yss-colors-border);
  background: var(--workbench-glass-chrome);
  -webkit-backdrop-filter: var(--workbench-glass-blur);
  backdrop-filter: var(--workbench-glass-blur);
}
.rail-top, .rail-nav, .rail-actions {
  display: flex;
  flex-direction: column;
  gap: 4px;
  flex: none;
}
.rail-divider[data-slot='separator'] {
  flex: none;
  align-self: center;
  inline-size: calc(100% - 16px);
  margin-block: 8px;
}
.rail-divider-user[data-slot='separator'] { margin-block: 4px; }
.workbench-rail .rail-link {
  display: flex;
  justify-content: flex-start;
  align-items: center;
  gap: 8px;
  inline-size: 100%;
  min-block-size: 44px;
  padding: 4px 8px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  line-height: 1.2;
  text-decoration: none;
}
.workbench-rail .rail-switcher {
  position: relative;
  gap: 6px;
  color: var(--brand-color-text);
  font-weight: 650;
  background: var(--workbench-glass-pane);
  border: 1px solid var(--yss-colors-border);
}
.scope-name { min-inline-size: 0; overflow: hidden; text-overflow: ellipsis; }
.scope-chevron { margin-inline-start: auto; color: var(--brand-color-text-secondary); }
.rail-label-compact { display: none; }
.workbench-rail .rail-link:hover,
.workbench-rail .rail-link[aria-expanded='true'] {
  color: var(--brand-color-text);
  background: var(--brand-color-fill-secondary);
}
.workbench-rail .rail-link:focus-visible {
  outline: 2px solid var(--yss-color-primary-control);
  outline-offset: -2px;
}
.workbench-rail .rail-link[aria-current] {
  color: var(--yss-color-primary-control-active);
  background: var(--yss-colors-primary-bg);
  font-weight: 650;
}
.rail-actions {
  margin-block-start: auto;
  padding-block-start: 16px;
}
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
.project-name { min-inline-size: 0; overflow-wrap: anywhere; }
.rail-menu-item .current-project-check { margin-inline-start: auto; color: #315dcf; }

@media (max-width: 879px) {
  .workbench-rail { padding-inline: 4px; }
  .workbench-rail .rail-link {
    flex-direction: column;
    justify-content: center;
    gap: 3px;
    padding-inline: 2px;
    font-size: 11px;
  }
  .rail-label-wide { display: none; }
  .rail-label-compact { display: block; }
  .workbench-rail .rail-switcher { gap: 3px; padding-block: 5px; }
  .scope-chevron {
    position: absolute;
    inset-block-start: 8px;
    inset-inline-end: 3px;
    inline-size: 12px;
    block-size: 12px;
  }
}
@media (width < 720px) {
  .rail-menu-item { min-block-size: 44px; }
}
@media (forced-colors: active) {
  .workbench-rail { border-inline-end-color: CanvasText; }
  .rail-link[aria-current], .rail-menu-item[data-highlighted] {
    outline: 2px solid Highlight;
    outline-offset: -2px;
  }
  .workbench-rail .rail-link:focus-visible,
  .workbench-rail .rail-link[aria-expanded='true'] { outline: 2px solid Highlight; outline-offset: -2px; }
}
</style>
