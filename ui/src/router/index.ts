import { shallowReadonly, shallowRef } from 'vue'
import { createRouter, createWebHashHistory, type RouteLocationNormalized } from 'vue-router'

const Workbench = () => import('@/views/Workbench/index.vue')
const Projects = () => import('@/views/Projects/index.vue')
const ProjectDetail = () => import('@/views/ProjectDetail/index.vue')
const Specs = () => import('@/views/Specs/index.vue')
const SpecDetail = () => import('@/views/SpecDetail/index.vue')
const PiWorkspace = () => import('@/views/PiWorkspace/index.vue')
const AgentEntry = () => import('@/views/PiWorkspace/AgentEntry.vue')
const Jobs = () => import('@/views/Jobs/index.vue')
const Settings = () => import('@/views/Settings/index.vue')

const pending = shallowRef<RouteLocationNormalized>()
const failure = shallowRef<RouteLocationNormalized>()
export const pendingRoute = shallowReadonly(pending)
export const failedRoute = shallowReadonly(failure)

const retrySearchKey = 'reload-workbench-view'
const startUrl = new URL(window.location.href)
if (startUrl.searchParams.has(retrySearchKey)) {
  startUrl.searchParams.delete(retrySearchKey)
  window.history.replaceState(window.history.state, '', startUrl)
}

const positions = new Map<string, number>()
export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/workbench' },
    { path: '/workbench', component: Workbench, meta: { lightweight: true } },
    { path: '/projects', component: Projects, meta: { lightweight: true } },
    { path: '/projects/:projectId', component: ProjectDetail, meta: { lightweight: true, projectDetail: true } },
    { path: '/specs', component: Specs, meta: { lightweight: true } },
    { path: '/specs/:specId', component: SpecDetail, meta: { lightweight: true, specDetail: true } },
    { path: '/specs/:specId/agent', name: 'pi-workspace', component: PiWorkspace, props: true, meta: { piWorkspace: true, lightweight: true } },
    { path: '/agent', component: AgentEntry, meta: { piWorkspace: true, lightweight: true } },
    { path: '/agent/:specId', name: 'agent-workbench', component: PiWorkspace, props: true, meta: { piWorkspace: true, lightweight: true } },
    { path: '/jobs', component: Jobs },
    { path: '/jobs/:jobId', component: Jobs },
    { path: '/settings', component: Settings },
    { path: '/:pathMatch(.*)*', redirect: '/workbench' }
  ],
  scrollBehavior(to, from, saved) {
    if (saved) return saved
    // In-page query updates include reading progress and component-managed anchors.
    if (to.path === from.path) return false
    return { top: positions.get(to.fullPath) ?? 0 }
  }
})

router.beforeEach((to, from) => {
  positions.set(from.fullPath, window.scrollY)
  const query = { ...to.query }
  let changed = false
  if ('entity' in query && query.entity !== 'spec') {
    query.entity = 'spec'
    changed = true
  }
  if ('taskId' in query) {
    delete query.taskId
    changed = true
  }
  if ((Array.isArray(query.tab) ? query.tab : [query.tab]).includes('tasks')) {
    delete query.tab
    changed = true
  }
  if (changed) return { path: to.path, query, hash: to.hash, replace: true }
  failure.value = undefined
  pending.value = to.path !== from.path ? to : undefined
})

router.afterEach((to) => {
  if (pending.value === to) pending.value = undefined
})

router.onError((_cause, to) => {
  if (pending.value !== to) return
  pending.value = undefined
  failure.value = to
})

export function retryFailedRoute() {
  if (!failure.value) return
  const destination = new URL(router.resolve(failure.value.fullPath).href, window.location.href)
  if (destination.href === window.location.href) {
    window.location.reload()
    return
  }
  // A failed dynamic import can stay cached for the lifetime of the current document.
  destination.searchParams.set(retrySearchKey, '1')
  window.location.assign(destination)
}
