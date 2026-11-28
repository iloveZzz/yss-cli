<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  BookMarked, Check, ChevronDown, ChevronLeft, ChevronRight, CircleCheck,
  CircleDot, CircleUserRound, Clock3, CloudOff, FileText, FolderClosed,
  FolderOpen, GitFork, GitPullRequest, LayoutDashboard, LockKeyhole, PanelLeftClose,
  PanelLeftOpen, RefreshCw, Search, Settings, Sparkles, TriangleAlert, X,
} from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { issues, sources } from './fixtures'
import IssueReader from './IssueReader.vue'
import IssueScenarios, { type Scenario, type ReadState } from './IssueScenarios.vue'

type Issue = (typeof issues)[number]
type IssueState = 'open' | 'closed' | 'all'

const sections = [
  { name: '工作台', icon: LayoutDashboard }, { name: '产品与工程', icon: FolderClosed },
  { name: '规格管理', icon: FileText }, { name: '合并请求', icon: GitPullRequest },
  { name: 'Issues', icon: CircleDot }, { name: 'AI Agent', icon: Sparkles },
]
const tabs: { value: IssueState; label: string }[] = [
  { value: 'open', label: '打开' }, { value: 'closed', label: '已关闭' }, { value: 'all', label: '全部' },
]
const projects = [...new Set(sources.map(source => source.project))]
const project = ref('')
const sourceId = ref(sources[0]?.id ?? '')
const scopedSources = computed(() => sources.filter(source => !project.value || source.project === project.value))
const currentSource = computed(() => scopedSources.value.find(source => source.id === sourceId.value))
const noSources = ref(false)
const repositoryReads = reactive<Record<string, ReadState>>({})
const scenario = computed<Scenario>(() => noSources.value ? 'none' : repositoryReads[sourceId.value] ?? 'normal')
const readable = computed(() => !!currentSource.value && ['normal', 'partial', 'unavailable'].includes(scenario.value))
const unavailableNumbers = reactive<Record<string, number | undefined>>({})
const state = ref<IssueState>('open')
const draft = ref('')
const term = ref('')
const page = ref(1)
const pageSize = 5
const selectedNumber = ref<number | null>(null)
const drawerOpen = ref(false)
const detailUnavailable = computed(() => selectedNumber.value !== null && scenario.value === 'unavailable'
  && selectedNumber.value === unavailableNumbers[sourceId.value])
const retrying = ref(false)
let retryTimer: ReturnType<typeof setTimeout> | undefined
let triggerBeforeDetail = ''
const mainHeading = ref<HTMLElement>()
const compact = ref(false)
const manualCollapse = ref<boolean>()
const narrow = ref(false)
const collapsed = computed(() => narrow.value || (manualCollapse.value ?? compact.value))
const announcement = ref('')

const available = computed(() => {
  if (!readable.value) return []
  const sorted = issues.filter(issue => issue.sourceId === sourceId.value && issue.number !== unavailableNumbers[sourceId.value])
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt) || b.number - a.number)
  return scenario.value === 'partial' ? sorted.slice(0, 4) : sorted
})
const selectedIssue = computed(() => readable.value && !detailUnavailable.value
  ? issues.find(issue => issue.sourceId === sourceId.value && issue.number === selectedNumber.value)
  : undefined)
const matches = computed(() => {
  const value = term.value.toLocaleLowerCase()
  if (!value) return available.value
  if (/^#\d+$/u.test(value) && Number(value.slice(1)) > 0) return available.value.filter(issue => issue.number === Number(value.slice(1)))
  return available.value.filter(issue => issue.title.toLocaleLowerCase().includes(value) || issue.body.toLocaleLowerCase().includes(value))
})
const tabCount = (tab: IssueState) => tab === 'all' ? matches.value.length : matches.value.filter(issue => issue.state === tab).length
const filtered = computed(() => matches.value.filter(issue => state.value === 'all' || issue.state === state.value))
const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const rows = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))
const statusText = computed(() => {
  if (!readable.value) return ''
  const prefix = scenario.value === 'partial' ? '已读范围内' : ''
  const search = term.value ? `“${term.value}” · ` : ''
  return `${search}${prefix}共 ${filtered.value.length} 条`
})
const blocked = computed(() => {
  if (scenario.value === 'none' || !currentSource.value) return {
    title: '暂无可用的仓库来源', text: '请在「产品与工程」核对已启用工程的 Issue 跟踪仓库或远端地址。', icon: FolderOpen,
  }
  if (scenario.value === 'denied') return {
    title: '没有权限读取此仓库', text: '请核对当前平台账号的仓库访问权限。也可以切换到其他来源。', icon: LockKeyhole,
  }
  return { title: '未能读取仓库', text: '网络连接中断。恢复连接后，可按当前条件重新读取。', icon: CloudOff }
})
const detailProblem = computed(() => {
  if (detailUnavailable.value) return {
    title: '此 Issue 已不可用',
    text: '条目可能已移除，或当前账号无法访问。可关闭详情后核对来源，或重新读取。',
    retryLabel: '重试读取',
  }
  if (readable.value) return undefined
  const { title, text } = blocked.value
  return {
    title, text,
    retryLabel: !currentSource.value || noSources.value ? undefined
      : scenario.value === 'denied' ? '重新检查权限' : '重试读取',
  }
})
const dateFormat = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone: 'Asia/Shanghai' })

function dateLabel(date: string) {
  return dateFormat.format(new Date(date))
}
function rowId(number: number) {
  return `issue-trigger-${sourceId.value}-${number}`
}
function reportViewport() {
  compact.value = window.innerWidth < 880
  narrow.value = window.innerWidth < 720
}
function cancelRetry() {
  clearTimeout(retryTimer)
  retrying.value = false
}
function switchSource(id: string) {
  cancelRetry()
  sourceId.value = id
  page.value = 1
  draft.value = term.value
  announcement.value = ''
}
function switchProject(name: string) {
  project.value = name
  const next = scopedSources.value.find(source => source.id === sourceId.value) ?? scopedSources.value[0]
  switchSource(next?.id ?? '')
}
function chooseScenario(value: Scenario) {
  cancelRetry()
  noSources.value = value === 'none'
  if (value !== 'none') {
    repositoryReads[sourceId.value] = value
    unavailableNumbers[sourceId.value] = value === 'unavailable' && drawerOpen.value ? selectedNumber.value ?? undefined : undefined
  }
  if (readable.value) page.value = Math.min(page.value, pages.value)
  announcement.value = ''
}
function retry() {
  cancelRetry()
  retrying.value = true
  const id = sourceId.value
  retryTimer = setTimeout(() => {
    repositoryReads[id] = 'normal'
    unavailableNumbers[id] = undefined
    retrying.value = false
    announcement.value = '仓库已重新读取'
  }, 280)
}
function changeTab(value: string | number) {
  if (tabs.some(tab => tab.value === value)) {
    state.value = value as IssueState
    page.value = 1
  }
}
function submitSearch() {
  term.value = draft.value.trim()
  draft.value = term.value
  page.value = 1
}
async function clearSearch() {
  draft.value = ''
  submitSearch()
  await nextTick()
  document.getElementById('issue-search')?.focus()
}
function openIssue(issue: Issue) {
  triggerBeforeDetail = rowId(issue.number)
  selectedNumber.value = issue.number
  if (scenario.value === 'unavailable' && unavailableNumbers[sourceId.value] === undefined) {
    unavailableNumbers[sourceId.value] = issue.number
  }
  drawerOpen.value = true
}

function closeIssue() {
  drawerOpen.value = false
  if (readable.value) page.value = Math.min(page.value, pages.value)
}

async function restoreIssueFocus() {
  await nextTick()
  if (drawerOpen.value) return
  selectedNumber.value = null
  const trigger = document.getElementById(triggerBeforeDetail)
  if (trigger) {
    trigger.focus({ preventScroll: true })
  } else {
    const fallback = document.getElementById('issue-search') ?? mainHeading.value
    fallback?.focus()
  }
}
onMounted(() => {
  reportViewport()
  window.addEventListener('resize', reportViewport)
})
onBeforeUnmount(() => {
  cancelRetry()
  window.removeEventListener('resize', reportViewport)
})
</script>

<template>
  <div class="issues-app" :data-collapsed="collapsed">
    <a class="skip-link" href="#issue-main">跳至主要内容</a>
    <aside class="issues-rail" aria-label="工作台侧栏">
      <DropdownMenu :modal="false">
        <DropdownMenuTrigger as-child>
          <Button variant="ghost" class="rail-project" :aria-label="'切换工程 · ' + (project || '全部工程')">
            <span class="rail-project-mark"><FolderOpen aria-hidden="true" /></span>
            <span class="rail-text rail-scope-name">{{ project || '全部工程' }}</span><ChevronDown class="rail-text rail-chevron" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent :side="collapsed ? 'right' : 'bottom'" align="start" :side-offset="6" class="issues-menu">
          <DropdownMenuLabel>工程范围</DropdownMenuLabel>
          <DropdownMenuItem @select="switchProject('')">全部工程 <Check v-if="!project" class="menu-check" aria-hidden="true" /></DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem v-for="name in projects" :key="name" @select="switchProject(name)">
            {{ name }} <Check v-if="project === name" class="menu-check" aria-hidden="true" />
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <div class="rail-rule"></div>
      <nav class="rail-items" aria-label="主导航">
        <template v-for="item in sections" :key="item.name">
          <Button v-if="item.name === 'Issues'" variant="ghost" class="rail-item" aria-current="page" aria-label="Issues" title="Issues">
            <component :is="item.icon" aria-hidden="true" /><span class="rail-text">{{ item.name }}</span>
          </Button>
          <span v-else class="rail-item" :aria-label="item.name" :title="item.name">
            <component :is="item.icon" aria-hidden="true" /><span class="rail-text">{{ item.name }}</span>
          </span>
        </template>
      </nav>
      <div class="rail-foot">
        <span class="rail-item" title="本机设置" aria-label="本机设置"><Settings aria-hidden="true" /><span class="rail-text">本机设置</span></span>
        <div class="rail-rule"></div>
        <span class="rail-item" title="本机用户" aria-label="本机用户"><CircleUserRound aria-hidden="true" /><span class="rail-text">本机用户</span></span>
        <Button v-if="!narrow" class="rail-item rail-toggle" variant="ghost" :aria-label="collapsed ? '展开侧栏' : '收起侧栏'" :aria-expanded="!collapsed" @click="manualCollapse = !collapsed">
          <component :is="collapsed ? PanelLeftOpen : PanelLeftClose" aria-hidden="true" /><span class="rail-text">收起侧栏</span>
        </Button>
      </div>
    </aside>

    <main id="issue-main" class="issues-main" tabindex="-1">
      <header class="issues-top">
        <nav aria-label="面包屑导航" class="issue-crumb">
          <span>工作台</span><span aria-hidden="true">/</span>
          <h1 ref="mainHeading" tabindex="-1">Issues</h1>
        </nav>
        <IssueScenarios :scenario="scenario" @select="chooseScenario" />
      </header>

      <section class="issues-panel panel" aria-label="仓库 Issues">
        <div class="repository-heading">
          <span class="repository-mark"><BookMarked aria-hidden="true" /></span>
          <div class="repository-identity">
            <h2>{{ scenario === 'none' ? '仓库 Issues' : currentSource?.repository || '仓库 Issues' }}</h2>
            <p v-if="currentSource && scenario !== 'none'"><span>{{ currentSource.provider }}</span><span aria-hidden="true">·</span><span>{{ currentSource.project }}</span></p>
          </div>
          <DropdownMenu v-if="scenario !== 'none'" :modal="false">
            <DropdownMenuTrigger as-child>
              <Button variant="outline" class="source-trigger" aria-label="切换来源"><GitFork aria-hidden="true" /><span>切换来源</span><ChevronDown aria-hidden="true" /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" :side-offset="4" :collision-padding="8" class="issues-menu source-menu">
              <DropdownMenuLabel>当前工程范围的仓库</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuItem v-for="source in scopedSources" :key="source.id" @select="switchSource(source.id)">
                <span class="source-option">
                  <strong>{{ source.repository }}</strong>
                  <small>{{ source.provider }} · {{ source.project }}</small>
                  <small>{{ source.sourceBasis }}</small>
                  <small v-if="source.codeRepository !== source.repository">代码仓 · {{ source.codeRepository }}</small>
                </span>
                <Check v-if="source.id === sourceId" class="menu-check" aria-hidden="true" />
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <Tabs v-if="scenario !== 'none' && currentSource" :model-value="state" class="issues-tabs" activation-mode="manual" @update:model-value="changeTab">
          <TabsList class="issue-tabbar" aria-label="Issue 状态">
            <TabsTrigger v-for="tab in tabs" :key="tab.value" :value="tab.value">{{ tab.label }}<span v-if="readable && scenario !== 'partial'" class="issue-tab-count">{{ tabCount(tab.value) }}</span></TabsTrigger>
          </TabsList>
          <TabsContent :value="state" class="issue-results">
            <div class="issues-toolbar">
              <form class="issue-search-form" role="search" aria-label="搜索当前仓库的 Issues" @submit.prevent="submitSearch">
                <div class="issue-search-box">
                  <Search aria-hidden="true" />
                  <Input id="issue-search" v-model="draft" type="text" autocomplete="off" placeholder="搜索标题、正文或 #编号" aria-label="搜索标题、正文或 #编号" />
                  <Button v-if="draft || term" type="button" variant="ghost" class="issue-clear" aria-label="清除搜索" @click="clearSearch"><X aria-hidden="true" /></Button>
                </div>
                <Button type="submit" variant="outline">搜索</Button>
              </form>
              <span v-if="readable" class="issue-order"><Clock3 aria-hidden="true" />最近更新</span>
            </div>
            <Alert v-if="scenario === 'partial'" class="partial-read">
              <TriangleAlert aria-hidden="true" />
              <AlertTitle>仓库只返回了部分结果</AlertTitle>
              <AlertDescription>可收窄搜索条件，或重新读取当前仓库。<Button type="button" variant="outline" :disabled="retrying" @click="retry">{{ retrying ? '正在重试…' : '重试读取' }}</Button></AlertDescription>
            </Alert>
            <template v-if="readable">
              <Table v-if="rows.length" class="issue-table" role="table" aria-label="Issues 列表">
                <TableHeader role="rowgroup"><TableRow role="row"><TableHead scope="col">Issue</TableHead><TableHead scope="col">标签</TableHead><TableHead scope="col">状态</TableHead><TableHead scope="col">更新时间</TableHead></TableRow></TableHeader>
                <TableBody role="rowgroup">
                  <TableRow v-for="issue in rows" :key="issue.number" role="row" class="issue-row" @click="openIssue(issue)">
                    <TableCell class="issue-title-cell">
                      <Button :id="rowId(issue.number)" type="button" variant="ghost" class="issue-title" aria-haspopup="dialog" :aria-label="'#' + issue.number + ' ' + issue.title" @click.stop="openIssue(issue)">
                        <span>{{ issue.title }}</span><small>#{{ issue.number }}</small>
                      </Button>
                    </TableCell>
                    <TableCell class="issue-label-cell"><span class="issue-labels"><Badge v-for="label in issue.labels" :key="label" variant="outline">{{ label }}</Badge><span v-if="!issue.labels.length" aria-label="无标签">—</span></span></TableCell>
                    <TableCell class="issue-state-cell"><span class="issue-state" :data-state="issue.state"><component :is="issue.state === 'open' ? CircleDot : CircleCheck" aria-hidden="true" />{{ issue.state === 'open' ? '打开' : '已关闭' }}</span></TableCell>
                    <TableCell class="issue-updated"><time :datetime="issue.updatedAt">{{ dateLabel(issue.updatedAt) }}</time><ChevronRight aria-hidden="true" /></TableCell>
                  </TableRow>
                </TableBody>
              </Table>
              <Empty v-else class="issues-empty">
                <EmptyHeader><EmptyMedia variant="icon"><Search aria-hidden="true" /></EmptyMedia>
                  <EmptyTitle>{{ scenario === 'partial' ? '已读范围内没有匹配的 Issue' : '没有匹配的 Issue' }}</EmptyTitle>
                  <EmptyDescription>{{ scenario === 'partial' ? '其他 Issue 仍可能匹配当前条件。' : term ? '可以修改搜索内容，或切换状态再试。' : '可以切换状态查看其他 Issue。' }}</EmptyDescription>
                </EmptyHeader>
                <EmptyContent v-if="term"><Button variant="outline" @click="clearSearch">清除搜索条件</Button></EmptyContent>
              </Empty>
              <footer class="issue-footer">
                <span role="status">{{ statusText }}</span>
                <nav v-if="pages > 1" aria-label="Issues 分页" class="issue-pagination">
                  <Button variant="outline" size="icon" aria-label="上一页" :disabled="page === 1" @click="page--"><ChevronLeft aria-hidden="true" /></Button>
                  <span aria-live="polite">第 {{ page }} / {{ pages }} 页</span>
                  <Button variant="outline" size="icon" aria-label="下一页" :disabled="page === pages" @click="page++"><ChevronRight aria-hidden="true" /></Button>
                </nav>
              </footer>
            </template>
          </TabsContent>
        </Tabs>
        <Empty v-if="!readable" class="issues-empty">
          <EmptyHeader><EmptyMedia variant="icon"><component :is="blocked.icon" aria-hidden="true" /></EmptyMedia>
            <EmptyTitle>{{ blocked.title }}</EmptyTitle><EmptyDescription>{{ blocked.text }}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent class="empty-actions">
            <Button v-if="scenario !== 'none' && currentSource" variant="outline" :disabled="retrying" @click="retry"><RefreshCw aria-hidden="true" />{{ retrying ? '正在读取…' : scenario === 'denied' ? '重新检查权限' : '重试读取' }}</Button>
          </EmptyContent>
        </Empty>
      </section>
      <span class="sr-only" role="status">{{ announcement }}</span>
    </main>
    <IssueReader
      :open="drawerOpen"
      :issue="selectedIssue ?? null"
      :source="currentSource"
      :problem="detailProblem"
      :retrying="retrying"
      @close="closeIssue"
      @restore-focus="restoreIssueFocus"
      @retry="retry"
    >
      <template #scenario>
        <IssueScenarios :scenario="scenario" compact @select="chooseScenario" />
      </template>
    </IssueReader>
  </div>
</template>
