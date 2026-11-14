import { createRouter, createWebHashHistory } from 'vue-router'
import Workbench from '@/views/Workbench/index.vue'
import Projects from '@/views/Projects/index.vue'
import ProjectDetail from '@/views/ProjectDetail/index.vue'
import Specs from '@/views/Specs/index.vue'
import SpecDetail from '@/views/SpecDetail/index.vue'
const positions = new Map<string, number>()
export const router = createRouter({ history: createWebHashHistory(), routes: [
  { path: '/', redirect: '/workbench' }, { path: '/workbench', component: Workbench },
  { path: '/projects', component: Projects }, { path: '/projects/:projectId', component: ProjectDetail },
  { path: '/specs', component: Specs }, { path: '/specs/:specId', component: SpecDetail },
  { path: '/:pathMatch(.*)*', redirect: '/workbench' },
], scrollBehavior(to, _from, saved) { return saved ?? { top: positions.get(to.fullPath) ?? 0 } } })

router.beforeEach((_to, from) => { positions.set(from.fullPath, window.scrollY) })
