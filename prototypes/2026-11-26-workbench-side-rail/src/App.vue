<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import {
  ArrowRight, Check, ChevronDown, FileClock, FileText, FolderClosed,
  History, Layers3, LayoutDashboard, Monitor, RefreshCw, Settings, Sparkles,
} from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/card'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Separator } from '@/components/ui/separator'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

const navigation = [
  { id: 'workbench', label: '工作台', short: '工作台', icon: LayoutDashboard },
  { id: 'projects', label: '产品与工程', short: '工程', icon: FolderClosed },
  { id: 'specs', label: '规格管理', short: '规格', icon: FileText },
  { id: 'agent', label: 'AI Agent', short: 'Agent', icon: Sparkles },
  { id: 'settings', label: '设置', short: '设置', icon: Settings },
] as const

const projects = [
  { id: 'customer', name: '客户经营平台', code: 'customer-platform', mark: '客' },
  { id: 'data', name: '数据集成服务', code: 'data-integration', mark: '数' },
] as const

const specs = [
  { id: 'assignment', title: '线索分配与回收', project: 'customer', stage: '产品规格', progress: '待确认与核实', state: '待确认', next: '确认转派后的归属与回收规则', time: '今天 10:32' },
  { id: 'contacts', title: '联系人标签治理', project: 'customer', stage: '需求分析', progress: '进行中', state: '待补充', next: '补齐历史标签兼容口径', time: '今天 09:54' },
  { id: 'import', title: '同步失败重试策略', project: 'data', stage: '产品规格', progress: '需要解除阻塞', state: '待回写', next: '将重试上限答复回写到规格', time: '昨天 17:26' },
] as const

const reminderTabs = ['待你决定', '待补充', '需要复查', '待回写', '可确认'] as const
const progressTabs = ['待确认与核实', '需要解除阻塞', '进行中', '来源异常'] as const
const tasks = [
  { title: '跨团队转派的归属范围', spec: 'assignment', group: '待你决定', blocking: true, note: '确认转派后由原团队还是接收团队跟进。' },
  { title: '回收后是否保留原负责人', spec: 'assignment', group: '待你决定', blocking: false, note: '确认回收后负责人字段的展示和分配规则。' },
  { title: '重试耗尽后何时通知维护人', spec: 'import', group: '待你决定', blocking: false, note: '确认自动重试结束后的通知时机和接收人。' },
  { title: '补充历史标签的兼容口径', spec: 'contacts', group: '待补充', blocking: false, note: '补充历史标签在编辑时的保留规则。' },
  { title: '将重试上限答复回写到规格', spec: 'import', group: '待回写', blocking: true, note: '核对已保存的答复并回写到规格正文。' },
] as const

const runs = [
  { title: '梳理线索回收规则', context: '客户经营平台 · Pi Agent · 只读分析', state: '运行中', time: '今天 10:38', action: '查看运行', note: '已读取规格文件，正在核对交互与待确认问题。' },
  { title: '检查工程目录绑定', context: '数据集成服务 · 工程维护', state: '需要恢复', time: '今天 09:41', action: '核对恢复', note: '上次执行已中断。恢复前需核对当前工程路径。' },
] as const

const activities = [
  { spec: 'assignment', action: '更新待确认事项', time: '今天 10:32' },
  { spec: 'contacts', action: '读取规格正文', time: '今天 09:54' },
  { spec: 'import', action: '保存业务答复', time: '昨天 17:26' },
] as const

const route = ref(readRoute())
const selectedProject = computed(() => projects.find((project) => route.value === 'projects/' + project.id))
const section = computed(() => route.value.split('/')[0])
const navigationSection = computed(() => section.value === 'runs' ? 'workbench' : section.value)
const pageTitle = computed(() => selectedProject.value?.name ?? (
  section.value === 'runs' ? '执行记录' : navigation.find((item) => item.id === section.value)?.label ?? '工作台'
))
const reminder = ref('待你决定')
const progress = ref('待确认与核实')
const projectSpecs = computed(() => specs.filter((spec) => spec.project === selectedProject.value?.id))
const main = ref<HTMLElement>()
const lastRead = ref('10:42')
const refreshMessage = ref('')
const detail = ref<{ title: string; context: string; note: string } | null>(null)
let detailTrigger: HTMLElement | null = null

function readRoute() {
  const hash = window.location.hash.slice(1)
  return ['workbench', 'projects', 'specs', 'agent', 'settings', 'runs'].includes(hash) ||
    projects.some((project) => hash === 'projects/' + project.id) ? hash : 'workbench'
}

function updateRoute() {
  const next = readRoute()
  if (route.value === next) return
  route.value = next
  window.scrollTo({ top: 0, behavior: 'instant' })
}

onMounted(() => window.addEventListener('hashchange', updateRoute))
onUnmounted(() => window.removeEventListener('hashchange', updateRoute))

function projectName(id: string) {
  return projects.find((project) => project.id === id)?.name ?? ''
}

function specFor(id: string) {
  return specs.find((spec) => spec.id === id)!
}

function tasksFor(group: string) {
  return tasks.filter((task) => task.group === group)
}

function specsFor(group: string) {
  return specs.filter((spec) => spec.progress === group)
}

function refresh() {
  lastRead.value = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date())
  refreshMessage.value = '已刷新样例 · ' + lastRead.value
}

function showDetail(event: MouseEvent, title: string, context: string, note: string) {
  detailTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  detail.value = { title, context, note }
}

function showSpec(event: MouseEvent, id: string) {
  const spec = specFor(id)
  showDetail(event, spec.title, projectName(spec.project) + ' · ' + spec.stage, spec.next)
}

function restoreDetailFocus(event: Event) {
  if (!detailTrigger) return
  event.preventDefault()
  void nextTick(() => detailTrigger?.focus())
}
</script>

<template>
  <a href="#workspace-main" class="skip-link" @click.prevent="main?.focus()">跳到操作区</a>
  <aside class="rail" aria-label="工作台侧栏">
    <div class="rail-top">
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button variant="ghost" class="rail-link identity-link" aria-label="本机用户菜单">
            <span class="avatar" aria-hidden="true">本</span>
            <span>用户</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent side="right" align="start" :side-offset="8" class="flyout user-flyout">
          <DropdownMenuLabel class="flyout-label">
            <span class="avatar" aria-hidden="true">本</span>
            <span><strong>本机用户</strong><small>个人 · 本机</small></span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem as-child class="flyout-option">
            <a href="#settings"><Monitor aria-hidden="true" />本机设置</a>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button variant="ghost" class="rail-link identity-link" :aria-label="selectedProject ? '打开工程菜单，当前 ' + selectedProject.name : '打开工程菜单'">
            <span class="switcher-mark" aria-hidden="true">
              <template v-if="selectedProject">{{ selectedProject.mark }}</template>
              <Layers3 v-else :size="16" />
            </span>
            <span>切换<ChevronDown class="size-[10px]" aria-hidden="true" /></span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent side="right" align="start" :side-offset="8" class="flyout">
          <DropdownMenuLabel class="flyout-caption">最近工程</DropdownMenuLabel>
          <DropdownMenuItem v-for="project in projects" :key="project.id" as-child class="flyout-option">
            <a :href="'#projects/' + project.id" :aria-current="selectedProject?.id === project.id ? 'page' : undefined">
              <span class="project-mark" aria-hidden="true">{{ project.mark }}</span>
              <span><strong>{{ project.name }}</strong><small>{{ project.code }}</small></span>
              <Check v-if="selectedProject?.id === project.id" class="project-selected" aria-hidden="true" />
            </a>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem as-child class="flyout-option">
            <a href="#projects"><FolderClosed aria-hidden="true" />查看全部工程</a>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
    <Separator class="rail-divider" decorative />
    <nav class="rail-nav" aria-label="主导航">
      <Button v-for="item in navigation" :key="item.id" variant="ghost" as-child>
        <a :href="'#' + item.id" class="rail-link" :class="{ 'rail-settings': item.id === 'settings' }"
          :aria-label="item.label"
          :aria-current="navigationSection === item.id ? (route === item.id ? 'page' : 'location') : undefined">
          <component :is="item.icon" class="size-[17px]" :stroke-width="1.7" aria-hidden="true" />
          <span>{{ item.short }}</span>
        </a>
      </Button>
    </nav>
    <span class="rail-local"><span class="local-dot" aria-hidden="true"></span>本机</span>
  </aside>

  <main id="workspace-main" ref="main" class="workspace" tabindex="-1">
    <div class="content">
      <header class="page-heading">
        <div>
          <div class="page-context"><span class="brand-mark" aria-hidden="true">Y</span><span>YSS</span><span class="context-divider" aria-hidden="true"></span><span>{{ selectedProject ? '产品与工程' : '本机工作台' }}</span></div>
          <h1>{{ pageTitle }}</h1>
          <p v-if="section === 'workbench'" class="page-meta">全部工程 <span aria-hidden="true">·</span> 最近读取 {{ lastRead }}</p>
          <p v-else-if="selectedProject" class="page-meta">{{ selectedProject.code }}</p>
        </div>
        <div v-if="section === 'workbench'" class="page-actions">
          <Button variant="outline" @click="refresh"><RefreshCw aria-hidden="true" />刷新</Button>
          <Button as-child variant="outline"><a href="#projects">工程管理</a></Button>
          <Button as-child variant="ghost"><a href="#specs">全部 Spec <ArrowRight aria-hidden="true" /></a></Button>
        </div>
        <Button v-else-if="selectedProject" as-child variant="outline"><a href="#projects">全部工程</a></Button>
      </header>
      <p v-if="refreshMessage && section === 'workbench'" role="status" class="refresh-status"><Check :size="14" aria-hidden="true" />{{ refreshMessage }}</p>

      <div v-if="section === 'workbench'" class="home-layout">
        <div class="home-priority">
          <section aria-labelledby="attention-title">
            <div class="section-heading">
              <h2 id="attention-title">需要你处理</h2>
              <a class="text-link" href="#specs">查看规格列表 <ArrowRight :size="13" aria-hidden="true" /></a>
            </div>
            <Tabs v-model="reminder" activation-mode="manual" class="panel home-tabs-panel">
              <TabsList class="home-tabs attention-tabs" aria-label="内容待办">
                <TabsTrigger v-for="group in reminderTabs" :key="group" :value="group">
                  {{ group }}<Badge variant="secondary" class="tab-count">{{ tasksFor(group).length }}</Badge>
                </TabsTrigger>
              </TabsList>
              <TabsContent v-for="group in reminderTabs" :key="group" :value="group">
                <Table v-if="tasksFor(group).length" scrollable :scroll-label="group + '事项'" class="spec-table attention-table">
                  <TableCaption class="sr-only">{{ group }}事项</TableCaption>
                  <TableHeader><TableRow>
                    <TableHead scope="col">Spec / 工程</TableHead>
                    <TableHead scope="col">待处理事项</TableHead>
                    <TableHead scope="col">操作</TableHead>
                  </TableRow></TableHeader>
                  <TableBody>
                    <TableRow v-for="task in tasksFor(group)" :key="task.title">
                      <TableHead scope="row">
                        <Button variant="ghost" class="row-link" @click="showSpec($event, task.spec)">{{ specFor(task.spec).title }}</Button>
                        <span class="row-sub">{{ projectName(specFor(task.spec).project) }}</span>
                      </TableHead>
                      <TableCell>
                        {{ task.title }}
                        <Badge v-if="task.blocking" variant="outline" class="blocking-label">阻塞</Badge>
                      </TableCell>
                      <TableCell>
                        <Button variant="link" class="row-action" @click="showDetail($event, task.title, specFor(task.spec).title, task.note)">
                          处理事项<ArrowRight aria-hidden="true" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  </TableBody>
                </Table>
                <p v-else class="quiet-empty">当前没有{{ group }}的事项</p>
              </TabsContent>
            </Tabs>
          </section>

          <section aria-labelledby="execution-title">
            <div class="section-heading">
              <h2 id="execution-title">执行与待处理</h2>
              <a class="text-link" href="#runs">全部执行记录 <ArrowRight :size="13" aria-hidden="true" /></a>
            </div>
            <Card class="panel table-card">
              <Table scrollable scroll-label="执行与待处理" class="spec-table execution-table">
                <TableCaption class="sr-only">执行与待处理记录</TableCaption>
                <TableHeader><TableRow>
                  <TableHead scope="col">执行</TableHead><TableHead scope="col">状态</TableHead>
                  <TableHead scope="col">最近更新</TableHead><TableHead scope="col">操作</TableHead>
                </TableRow></TableHeader>
                <TableBody>
                  <TableRow v-for="run in runs" :key="run.title">
                    <TableHead scope="row">{{ run.title }}<span class="row-sub">{{ run.context }}</span></TableHead>
                    <TableCell><Badge :variant="run.state === '需要恢复' ? 'outline' : 'secondary'" :class="{ 'blocking-label': run.state === '需要恢复' }">{{ run.state }}</Badge></TableCell>
                    <TableCell class="muted">{{ run.time }}</TableCell>
                    <TableCell><Button variant="link" class="row-action" @click="showDetail($event, run.title, run.context, run.note)">{{ run.action }}<ArrowRight aria-hidden="true" /></Button></TableCell>
                  </TableRow>
                </TableBody>
              </Table>
            </Card>
          </section>

          <section aria-labelledby="progress-title">
            <div class="section-heading">
              <h2 id="progress-title">Spec 推进</h2>
              <a class="text-link" href="#specs">查看规格列表 <ArrowRight :size="13" aria-hidden="true" /></a>
            </div>
            <Tabs v-model="progress" activation-mode="manual" class="panel home-tabs-panel">
              <TabsList class="home-tabs progress-tabs" aria-label="Spec 推进范围">
                <TabsTrigger v-for="group in progressTabs" :key="group" :value="group">
                  {{ group }}<Badge variant="secondary" class="tab-count">{{ specsFor(group).length }}</Badge>
                </TabsTrigger>
              </TabsList>
              <TabsContent v-for="group in progressTabs" :key="group" :value="group">
                <Table v-if="specsFor(group).length" scrollable :scroll-label="group + ' Spec'" class="spec-table progress-table">
                  <TableCaption class="sr-only">{{ group }} Spec</TableCaption>
                  <TableHeader><TableRow>
                    <TableHead scope="col">Spec / 工程</TableHead><TableHead scope="col">自动进度</TableHead>
                    <TableHead scope="col">内容与待办</TableHead><TableHead scope="col">下一步与阻塞</TableHead>
                    <TableHead scope="col">最近更新</TableHead>
                  </TableRow></TableHeader>
                  <TableBody>
                    <TableRow v-for="spec in specsFor(group)" :key="spec.id">
                      <TableHead scope="row">
                        <Button variant="ghost" class="row-link" @click="showSpec($event, spec.id)">{{ spec.title }}</Button>
                        <span class="row-sub">{{ projectName(spec.project) }}</span>
                      </TableHead>
                      <TableCell><span class="stage-status"><i aria-hidden="true"></i>{{ spec.stage }}</span></TableCell>
                      <TableCell><Badge variant="secondary">{{ spec.state }}</Badge></TableCell>
                      <TableCell>{{ spec.next }}</TableCell>
                      <TableCell class="muted">{{ spec.time }}</TableCell>
                    </TableRow>
                  </TableBody>
                </Table>
                <p v-else class="quiet-empty">当前没有此类规格</p>
              </TabsContent>
            </Tabs>
          </section>
        </div>

        <div class="recent-panels">
          <Card class="panel recent-panel">
            <CardHeader class="recent-heading"><FileClock :size="15" aria-hidden="true" /><h2>最近更新的规格</h2></CardHeader>
            <ul class="compact-records">
              <li v-for="spec in specs" :key="spec.id">
                <Button variant="ghost" class="row-link" @click="showSpec($event, spec.id)">{{ spec.title }}</Button>
                <time>{{ spec.time }}</time>
              </li>
            </ul>
          </Card>
          <Card class="panel recent-panel">
            <CardHeader class="recent-heading"><History :size="15" aria-hidden="true" /><h2>最近操作</h2></CardHeader>
            <ul class="compact-records">
              <li v-for="activity in activities" :key="activity.spec">
                <div>
                  <Button variant="ghost" class="row-link" @click="showSpec($event, activity.spec)">{{ specFor(activity.spec).title }}</Button>
                  <p class="row-sub">{{ activity.action }}</p>
                </div>
                <time>{{ activity.time }}</time>
              </li>
            </ul>
          </Card>
        </div>
      </div>

      <Card v-else class="panel listing">
        <CardHeader class="recent-heading"><h2>{{
          selectedProject ? 'Spec 与文件' :
            ({ projects: '已登记工程', specs: '全部规格', agent: 'Pi 会话', runs: '最近执行', settings: '本机工作台' }[section] ?? '')
        }}</h2></CardHeader>
        <ul v-if="section === 'projects' && !selectedProject" class="compact-records listing-records">
          <li v-for="project in projects" :key="project.id">
            <Button variant="ghost" as-child class="listing-link">
              <a :href="'#projects/' + project.id"><span class="project-mark" aria-hidden="true">{{ project.mark }}</span><span>{{ project.name }}<small>{{ project.code }}</small></span><ArrowRight aria-hidden="true" /></a>
            </Button>
          </li>
        </ul>
        <ul v-else-if="section === 'specs' || selectedProject" class="compact-records listing-records">
          <li v-for="spec in selectedProject ? projectSpecs : specs" :key="spec.id">
            <Button variant="ghost" class="listing-link" @click="showSpec($event, spec.id)">
              <span>{{ spec.title }}<small>{{ projectName(spec.project) }}</small></span>
              <Badge variant="secondary">{{ spec.state }}</Badge><ArrowRight aria-hidden="true" />
            </Button>
          </li>
        </ul>
        <ul v-else-if="section === 'agent' || section === 'runs'" class="compact-records listing-records">
          <li v-for="run in section === 'agent' ? runs.slice(0, 1) : runs" :key="run.title">
            <Button variant="ghost" class="listing-link" @click="showDetail($event, run.title, run.context, run.note)">
              <span>{{ run.title }}<small>{{ run.context }}</small></span>
              <Badge variant="secondary">{{ run.state }}</Badge><ArrowRight aria-hidden="true" />
            </Button>
          </li>
        </ul>
        <dl v-else class="local-settings"><div><dt>使用方式</dt><dd>个人 · 本机</dd></div><div><dt>数据来源</dt><dd>内置样例</dd></div></dl>
      </Card>

      <footer class="page-footer">样例数据</footer>
    </div>
  </main>
  <Dialog :open="detail !== null" @update:open="!$event && (detail = null)">
    <DialogContent class="detail-dialog" :show-close-button="false" @close-auto-focus="restoreDetailFocus">
      <DialogHeader v-if="detail">
        <DialogTitle>{{ detail.title }}</DialogTitle>
        <DialogDescription>{{ detail.context }}</DialogDescription>
        <p class="detail-note">{{ detail.note }}</p>
      </DialogHeader>
      <DialogClose as-child>
        <Button variant="outline" class="detail-close">关闭</Button>
      </DialogClose>
    </DialogContent>
  </Dialog>
</template>
