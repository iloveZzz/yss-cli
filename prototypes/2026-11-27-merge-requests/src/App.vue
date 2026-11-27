<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, useTemplateRef, watch } from 'vue'
import {
  ArrowLeft, ArrowUpDown, ChevronLeft, ChevronRight, CircleAlert, CircleUserRound,
  FileText, FolderClosed, FolderOpen, GitPullRequest, LayoutDashboard, RefreshCw, Search,
  Settings, Sparkles, X,
} from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  defaultEnabled, issues, repositories, repoById, repositoryFor, requestNumber, requests,
  scenes, sourceUrl, tabs, type Issue, type MergeRequest, type Platform, type Repository, type Scene, type Tab,
} from '@/demo'
import PlatformMark from './components/PlatformMark.vue'
import RepositorySources from './components/RepositorySources.vue'
import RequestDetails from './components/RequestDetails.vue'
import RequestState from './components/RequestState.vue'

type Order = 'updated' | 'created'
type SimulatedSource = { kind: 'request'; request: MergeRequest } | { kind: 'repository'; repo: Repository }
type ReadState = { label: string; detail?: string; tone: 'neutral' | 'success' | 'warning' }
const pageSize = 8
const embedded = window.self !== window.top
const scene = ref<Scene>('normal')
const sceneError = ref(false)
const simulatedSource = ref<SimulatedSource | null>(null)
const enabled = ref<string[]>([...defaultEnabled])
const recoveredRepositories = ref(new Set<string>())
const busy = ref<'refresh' | 'retry' | null>(null)
const readingRepositories = ref<string[]>([])
const refreshed = ref(false)
const activeTab = ref<Tab>('open')
const platformFilter = ref<'all' | Platform>('all')
const repositoryFilter = ref('all')
const query = ref('')
const sort = ref<Order>('updated')
const page = ref(1)
const selected = ref<MergeRequest | null>(null)
const showDetails = ref(false)
const showSources = ref(false)
const showConnection = ref(false)
const connectionRepo = ref<Repository | null>(null)
const listMain = useTemplateRef<HTMLElement>('listMain')
const searchField = useTemplateRef<HTMLElement>('searchField')
const requestList = useTemplateRef<HTMLElement>('requestList')
const emptyResult = useTemplateRef<HTMLElement>('emptyResult')
const simulatorHeading = useTemplateRef<HTMLElement>('simulatorHeading')
let detailTrigger: HTMLElement | null = null
let sourcesTrigger: HTMLElement | null = null
let connectionTrigger: HTMLElement | null = null
let repositorySimulatorTrigger: HTMLElement | null = null
let authorizationConfirmed = false
let loadTimer: number | undefined

const collapsedItems = [
  { label: '工作台', icon: LayoutDashboard },
  { label: '产品与工程', icon: FolderClosed },
  { label: '规格管理', icon: FileText },
  { label: '合并请求', icon: GitPullRequest, active: true },
  { label: 'AI Agent', icon: Sparkles },
]

const connected = computed(() => scene.value !== 'empty')
const eligibleRepositories = computed(() => repositories.filter(repo => enabled.value.includes(repo.id)))
const filterRepos = computed(() => eligibleRepositories.value.filter(repo => platformFilter.value === 'all' || platformFilter.value === repo.platform))
const scopeRepos = computed(() => filterRepos.value.filter(repo => repositoryFilter.value === 'all' || repositoryFilter.value === repo.id))
const selectedRepository = computed(() => filterRepos.value.find(repo => repo.id === repositoryFilter.value))
const repositoryNamesakes = computed(() => {
  const slugs = new Set<string>()
  const duplicates = new Set<string>()
  for (const repo of eligibleRepositories.value) {
    if (slugs.has(repo.slug)) duplicates.add(repo.slug)
    slugs.add(repo.slug)
  }
  return duplicates
})

function repositoryLabel(repo: Repository) {
  return repositoryNamesakes.value.has(repo.slug) ? `${repo.slug} · ${repo.platform} · ${repo.host}` : repo.slug
}

const compactRepositoryName = computed(() => {
  const repo = selectedRepository.value
  if (!repo) return ''
  const name = repo.slug.split('/').at(-1)
  return name && eligibleRepositories.value.filter(item => item.slug.split('/').at(-1) === name).length === 1 ? name : ''
})

function issueFor(repo: string): Issue | undefined {
  if (recoveredRepositories.value.has(repo)) return
  if (scene.value === 'partial' && repo === 'web') return 'unavailable'
  if (scene.value === 'auth' && repo === 'core') return 'unauthorized'
  if (scene.value === 'rate' && repo === 'web') return 'rateLimited'
}

const scopeIssues = computed(() => scopeRepos.value.flatMap(repo => {
  const issue = issueFor(repo.id)
  return issue ? [{ repo, issue }] : []
}))
const loadedRepositories = computed(() => scopeRepos.value.filter(repo => !issueFor(repo.id)))
const matchingRequests = computed(() => {
  const q = query.value.trim().toLowerCase()
  const exactNumber = /^[!#]?\d+$/.test(q) ? Number(q.replace(/^[!#]/, '')) : null
  const requestedPlatform = q.startsWith('!') ? 'GitLab' : q.startsWith('#') ? 'GitHub' : undefined
  return requests.filter(request => {
    const repo = loadedRepositories.value.find(item => item.id === request.repo)
    if (!repo) return false
    if (exactNumber !== null) return request.number === exactNumber && (!requestedPlatform || repo.platform === requestedPlatform)
    return request.title.toLowerCase().includes(q)
  })
})
const countByTab = computed(() => ({
  open: matchingRequests.value.filter(item => item.status === 'open').length,
  merged: matchingRequests.value.filter(item => item.status === 'merged').length,
  closed: matchingRequests.value.filter(item => item.status === 'closed').length,
  all: matchingRequests.value.length,
}))
const countsKnown = computed(() => scopeRepos.value.length > 0 && scopeIssues.value.length === 0)
const hasFilters = computed(() => Boolean(query.value) || platformFilter.value !== 'all' || repositoryFilter.value !== 'all')
const filteredRequests = computed(() => matchingRequests.value
  .filter(request => activeTab.value === 'all' || request.status === activeTab.value)
  .sort((first, second) => second[sort.value].localeCompare(first[sort.value]) || first.id.localeCompare(second.id)))
const pages = computed(() => Math.max(1, Math.ceil(filteredRequests.value.length / pageSize)))
const shownPage = computed(() => Math.min(page.value, pages.value))
const firstRow = computed(() => (shownPage.value - 1) * pageSize)
const visibleRequests = computed(() => filteredRequests.value.slice(firstRow.value, firstRow.value + pageSize))
const lastRead = computed(() => refreshed.value ? '2026-11-27 10:46' : '2026-11-27 10:45')
const readStates = computed<Record<string, ReadState>>(() => Object.fromEntries(
  repositories.map(repo => [repo.id, repositoryReadState(repo)]),
))
const resultStatus = computed(() => {
  if (busy.value) return busy.value === 'retry' ? '正在重试失败仓库…' : '正在刷新当前范围…'
  if (!connected.value) return '尚无已登记工程'
  if (!eligibleRepositories.value.length) return '尚未纳入仓库'
  if (!scopeRepos.value.length) return '当前范围没有仓库'
  if (scopeIssues.value.length) return `已读取 ${loadedRepositories.value.length} / ${scopeRepos.value.length} 个仓库，列表不完整`
  return `已读取 ${loadedRepositories.value.length} 个仓库，当前条件共 ${filteredRequests.value.length} 条`
})

function repositoryReadState(repo: Repository): ReadState {
  if (!enabled.value.includes(repo.id)) return { label: '未纳入', tone: 'neutral' }
  if (!scopeRepos.value.includes(repo)) return { label: '当前范围未读取', tone: 'neutral' }
  if (readingRepositories.value.includes(repo.id)) return { label: '读取中', tone: 'neutral' }
  const issue = issueFor(repo.id)
  if (issue === 'unauthorized') return { label: issues[issue].label, detail: '检查连接后可重新读取。', tone: 'warning' }
  if (issue) return { ...issues[issue], tone: 'warning' }
  return { label: '读取成功', tone: 'success' }
}

const simulatorRepository = computed(() => {
  const source = simulatedSource.value
  return source?.kind === 'request' ? repositoryFor(source.request.repo) : source?.repo
})
const simulatorRequests = computed(() => simulatedSource.value?.kind === 'repository'
  ? requests.filter(request => request.repo === simulatorRepository.value?.id).sort((first, second) => second.updated.localeCompare(first.updated))
  : [])
const simulatorAddress = computed(() => {
  const source = simulatedSource.value
  if (!source) return ''
  if (source.kind === 'request') return sourceUrl(source.request)
  const { platform, host, slug } = source.repo
  return `https://${host}/${slug}${platform === 'GitHub' ? '/pulls' : '/-/merge_requests'}`
})

watch([activeTab, platformFilter, repositoryFilter, query, sort], () => { page.value = 1 })
watch(platformFilter, () => {
  if (!filterRepos.value.some(repo => repo.id === repositoryFilter.value)) repositoryFilter.value = 'all'
})
watch(pages, () => { page.value = shownPage.value })

function clearConditions() {
  platformFilter.value = 'all'
  repositoryFilter.value = 'all'
  query.value = ''
  page.value = 1
}

function reset(nextScene: Scene) {
  window.clearTimeout(loadTimer)
  scene.value = nextScene
  sceneError.value = false
  enabled.value = nextScene === 'empty' ? [] : [...defaultEnabled]
  activeTab.value = 'open'
  sort.value = 'updated'
  clearConditions()
  recoveredRepositories.value.clear()
  readingRepositories.value = []
  refreshed.value = false
  busy.value = null
  selected.value = null
  showDetails.value = false
  showSources.value = false
  showConnection.value = false
  authorizationConfirmed = false
}

function syncHash() {
  const hash = window.location.hash.slice(1)
  if (hash.startsWith('source/')) {
    const request = requests.find(item => item.id === hash.slice('source/'.length))
    simulatedSource.value = request ? { kind: 'request', request } : null
    sceneError.value = !request
    return
  }
  if (hash.startsWith('repository/')) {
    const repo = repoById.get(hash.slice('repository/'.length))
    simulatedSource.value = repo ? { kind: 'repository', repo } : null
    sceneError.value = !repo
    return
  }
  if (simulatedSource.value) {
    simulatedSource.value = null
    if (!hash || hash === scene.value) return
  }
  const nextScene = scenes.find(item => item.id === (hash || 'normal'))
  if (nextScene) {
    if (scene.value !== nextScene.id || sceneError.value) reset(nextScene.id)
  } else {
    sceneError.value = true
  }
}

syncHash()
onMounted(() => {
  window.addEventListener('hashchange', syncHash)
  if (simulatedSource.value) simulatorHeading.value?.focus({ preventScroll: true })
})
watch(simulatedSource, async (source, previous) => {
  await nextTick()
  if (source) {
    simulatorHeading.value?.focus()
  } else if (previous?.kind === 'repository') {
    if (repositorySimulatorTrigger?.isConnected) repositorySimulatorTrigger.focus()
    else focusReadResults()
    repositorySimulatorTrigger = null
  }
})
onUnmounted(() => {
  window.removeEventListener('hashchange', syncHash)
  window.clearTimeout(loadTimer)
})

function chooseScene(value: unknown) {
  const nextScene = scenes.find(item => item.id === value)
  if (!nextScene) return
  reset(nextScene.id)
  window.location.hash = nextScene.id
}
function setTab(value: unknown) {
  const tab = tabs.find(item => item.id === value)
  if (tab) activeTab.value = tab.id
}
function setPlatform(value: unknown) {
  if (value === 'GitHub' || value === 'GitLab' || value === 'all') platformFilter.value = value
}
function setRepository(value: unknown) {
  if (typeof value === 'string') repositoryFilter.value = value
}
function setOrder(value: unknown) {
  if (value === 'updated' || value === 'created') sort.value = value
}

function focusReadResults() {
  const result = requestList.value ?? emptyResult.value ?? listMain.value
  result?.focus({ preventScroll: true })
  const scrollTarget = requestList.value?.querySelector('.request-title') ?? emptyResult.value?.querySelector('h2') ?? result
  scrollTarget?.scrollIntoView({ block: 'nearest' })
}

function refresh(kind: 'refresh' | 'retry', trigger?: EventTarget | null, authorizedRepo?: string) {
  if (busy.value) return
  const recovering = kind === 'retry' ? scopeIssues.value.filter(({ repo, issue }) =>
    authorizedRepo ? repo.id === authorizedRepo && issue === 'unauthorized' : issue !== 'unauthorized',
  ).map(({ repo }) => repo.id) : []
  readingRepositories.value = kind === 'retry' ? recovering : scopeRepos.value.map(repo => repo.id)
  busy.value = kind
  loadTimer = window.setTimeout(async () => {
    const returnFocus = recovering.length > 0 && document.activeElement === trigger
    for (const id of recovering) recoveredRepositories.value.add(id)
    refreshed.value = true
    readingRepositories.value = []
    busy.value = null
    if (returnFocus) {
      await nextTick()
      if (document.activeElement === document.body || document.activeElement === trigger) focusReadResults()
    }
  }, 380)
}

function openConnection(repo: Repository, event: MouseEvent) {
  if (busy.value) return
  connectionTrigger = event.currentTarget as HTMLElement
  connectionRepo.value = repo
  showConnection.value = true
}

function recoverAuthorization() {
  authorizationConfirmed = true
  showConnection.value = false
}

function closeConnection(event: Event) {
  restoreFocus(event, connectionTrigger)
  if (authorizationConfirmed && connectionRepo.value) {
    refresh('retry', connectionTrigger, connectionRepo.value.id)
  }
  authorizationConfirmed = false
}

function rememberRepositorySimulatorTrigger(event: MouseEvent) {
  if (embedded && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) {
    repositorySimulatorTrigger = event.currentTarget as HTMLElement
  }
}

function openRequest(request: MergeRequest, event: MouseEvent) {
  detailTrigger = event.currentTarget as HTMLElement
  selected.value = request
  showDetails.value = true
}
function openSources(event: MouseEvent) {
  sourcesTrigger = event.currentTarget as HTMLElement
  showSources.value = true
}
function restoreFocus(event: Event, trigger: HTMLElement | null) {
  if (simulatedSource.value || !trigger?.isConnected) return
  event.preventDefault()
  trigger.focus({ preventScroll: true })
}
function saveSources(ids: string[]) {
  enabled.value = ids
  if (!ids.includes(repositoryFilter.value)) repositoryFilter.value = 'all'
  page.value = 1
  showSources.value = false
  refresh('refresh')
}
function focusSearch() {
  searchField.value?.querySelector('input')?.focus({ preventScroll: true })
}
function clearSearch() {
  query.value = ''
  focusSearch()
}
function clearFilters() {
  clearConditions()
  focusSearch()
}
async function changePage(direction: -1 | 1) {
  page.value = shownPage.value + direction
  await nextTick()
  const list = requestList.value
  if (!list) return
  const firstRequest = list.querySelector<HTMLButtonElement>('.request-title')
  firstRequest?.focus({ preventScroll: true })
  if (list.getBoundingClientRect().top < 12) list.scrollIntoView({ block: 'start' })
}
function exitSimulator() {
  window.location.hash = scene.value
}
</script>

<template>
  <main v-if="simulatedSource && simulatorRepository" class="simulator">
    <Button variant="outline" @click="exitSimulator"><ArrowLeft aria-hidden="true" />{{ simulatedSource.kind === 'repository' ? '返回合并请求列表' : embedded ? '返回合并请求详情' : '查看合并请求列表' }}</Button>
    <section class="glass-panel simulator-panel" aria-labelledby="simulator-heading">
      <p class="simulator-label"><PlatformMark :platform="simulatorRepository.platform" /> 模拟原站</p>
      <h1 id="simulator-heading" ref="simulatorHeading" tabindex="-1">{{ simulatedSource.kind === 'request' ? simulatedSource.request.title : '合并请求列表' }}</h1>
      <p class="muted">{{ simulatorRepository.slug }}<template v-if="simulatedSource.kind === 'request'"> · {{ requestNumber(simulatedSource.request) }}</template></p>
      <p class="simulator-address"><span>原站地址示例</span><code>{{ simulatorAddress }}</code></p>
      <ul v-if="simulatedSource.kind === 'repository'" class="simulator-requests" role="list">
        <li v-for="request in simulatorRequests" :key="request.id">
          <span><strong>{{ request.title }}</strong><small class="request-number">{{ requestNumber(request) }}</small></span>
          <RequestState :status="request.status" :draft="request.draft" />
        </li>
      </ul>
    </section>
  </main>

  <div v-show="!simulatedSource" class="app-shell">
    <a href="#list-main" class="skip-link" @click.prevent="listMain?.focus()">跳转到合并请求</a>
    <aside class="workbench-rail" aria-label="工作台侧栏">
      <div class="rail-scope" title="全部工程" aria-label="全部工程">
        <span class="scope-icon"><FolderOpen aria-hidden="true" /></span><span class="rail-label">全部工程</span>
      </div>
      <div class="rail-divider"></div>
      <nav aria-label="主导航" class="rail-nav">
        <span v-for="item in collapsedItems" :key="item.label" class="rail-link" :title="item.label" :aria-current="item.active ? 'page' : undefined">
          <component :is="item.icon" aria-hidden="true" />
          <span class="rail-label">{{ item.label }}</span>
        </span>
      </nav>
      <div class="rail-bottom">
        <span class="rail-link" title="本机设置"><Settings aria-hidden="true" /><span class="rail-label">本机设置</span></span>
        <div class="rail-divider"></div>
        <span class="rail-link" title="本机用户"><CircleUserRound aria-hidden="true" /><span class="rail-label">本机用户</span></span>
      </div>
    </aside>

    <main id="list-main" ref="listMain" class="workbench-main" tabindex="-1">
      <header class="page-heading">
        <div>
          <h1>合并请求</h1>
          <p class="page-scope">{{ connected ? `已纳入 ${eligibleRepositories.length} 个仓库` : '尚无已登记工程' }}<span v-if="loadedRepositories.length" class="read-time"><span aria-hidden="true">·</span> 读取于 <time :datetime="lastRead.replace(' ', 'T')">{{ lastRead }}</time></span></p>
        </div>
        <div class="heading-actions">
          <Button variant="outline" class="action-outline" @click="openSources"><FolderOpen aria-hidden="true" />仓库来源</Button>
          <Button variant="outline" class="refresh-button" :disabled="!scopeRepos.length" :aria-disabled="busy ? true : undefined" @click="refresh('refresh')"><RefreshCw aria-hidden="true" :class="{ spinning: busy === 'refresh' }" />{{ busy === 'refresh' ? '刷新中' : '刷新' }}</Button>
        </div>
      </header>

      <div v-if="sceneError" class="glass-panel empty-state">
        <CircleAlert aria-hidden="true" />
        <h2>无法打开该原型场景</h2>
        <p>请选择已提供的原型场景。</p>
        <Button variant="outline" @click="chooseScene('normal')">返回正常读取</Button>
      </div>
      <Tabs v-else :model-value="activeTab" activation-mode="manual" class="glass-panel request-panel" @update:model-value="setTab">
        <div class="scope-controls">
          <TabsList class="page-tabs" aria-label="合并请求状态">
            <TabsTrigger v-for="tab in tabs" :key="tab.id" :value="tab.id">
              {{ tab.label }}
              <span v-if="countsKnown" class="tab-count">{{ countByTab[tab.id] }}<span class="sr-only"> 条</span></span>
            </TabsTrigger>
          </TabsList>
          <div class="filter-controls" role="search" aria-label="筛选合并请求">
            <div ref="searchField" class="search-field">
              <label for="request-search" class="sr-only">搜索标题或编号</label>
              <Search class="search-icon" aria-hidden="true" />
              <Input id="request-search" v-model="query" type="text" autocomplete="off" placeholder="搜索标题或编号" />
              <Button v-if="query" class="clear-search" variant="ghost" size="icon" aria-label="清除搜索" @click="clearSearch"><X aria-hidden="true" /></Button>
            </div>
            <Select :model-value="platformFilter" @update:model-value="setPlatform">
              <SelectTrigger class="filter-select platform-select" :data-filtered="platformFilter !== 'all' || undefined" aria-label="平台"><SelectValue /></SelectTrigger>
              <SelectContent class="select-popover" align="start" :side-offset="4">
                <SelectItem value="all">全部平台</SelectItem>
                <SelectItem value="GitLab">GitLab</SelectItem>
                <SelectItem value="GitHub">GitHub</SelectItem>
              </SelectContent>
            </Select>
            <Select :model-value="repositoryFilter" @update:model-value="setRepository">
              <SelectTrigger class="filter-select repo-select" :data-filtered="repositoryFilter !== 'all' || undefined" aria-label="仓库">
                <SelectValue>
                  <template v-if="selectedRepository">
                    <span :class="{ 'repo-label-full': compactRepositoryName }">{{ repositoryLabel(selectedRepository) }}</span>
                    <span v-if="compactRepositoryName" class="repo-label-compact" aria-hidden="true">{{ compactRepositoryName }}</span>
                  </template>
                  <template v-else>全部仓库</template>
                </SelectValue>
              </SelectTrigger>
              <SelectContent class="select-popover" align="start" :side-offset="4">
                <SelectItem value="all">全部仓库</SelectItem>
                <SelectItem v-for="repo in filterRepos" :key="repo.id" :value="repo.id">{{ repositoryLabel(repo) }}</SelectItem>
              </SelectContent>
            </Select>
            <Select :model-value="sort" @update:model-value="setOrder">
              <SelectTrigger class="filter-select order-select" aria-label="排序">
                <ArrowUpDown aria-hidden="true" class="order-icon" /><SelectValue />
              </SelectTrigger>
              <SelectContent class="select-popover" align="start" :side-offset="4">
                <SelectItem value="updated">最近更新</SelectItem>
                <SelectItem value="created">最近创建</SelectItem>
              </SelectContent>
            </Select>
            <Button v-if="hasFilters" class="reset-filters" variant="ghost" @click="clearFilters">清除筛选</Button>
          </div>
        </div>

        <TabsContent :value="activeTab" class="tab-content">
          <Alert v-if="scopeIssues.length" class="issue-alert" role="group" aria-labelledby="read-issue-title">
            <CircleAlert aria-hidden="true" />
            <div class="issue-copy">
              <AlertTitle id="read-issue-title">{{ loadedRepositories.length ? '部分仓库读取失败' : '当前范围读取失败' }}</AlertTitle>
              <AlertDescription>
                <ul role="list">
                  <li v-for="{ repo, issue } in scopeIssues" :key="repo.id">
                    <div :id="`read-issue-${repo.id}`">
                      <strong>{{ repo.platform }} · {{ repo.host }} · {{ repo.slug }}</strong>
                      <span> · {{ issues[issue].label }}。{{ issue === 'unauthorized' ? '检查连接后可重新读取。' : issues[issue].detail }}</span>
                    </div>
                    <Button
                      v-if="issue === 'unauthorized'" variant="outline" class="issue-action" aria-haspopup="dialog"
                      :aria-describedby="`read-issue-${repo.id}`" :aria-disabled="busy ? true : undefined"
                      @click="openConnection(repo, $event)"
                    >检查连接</Button>
                    <Button
                      v-if="issue === 'rateLimited'" as="a" variant="outline" class="issue-action"
                      :href="`#repository/${repo.id}`" :target="embedded ? undefined : '_blank'" rel="noopener"
                      :aria-describedby="`read-issue-${repo.id}`" @click="rememberRepositorySimulatorTrigger"
                    >在原站继续查找</Button>
                  </li>
                </ul>
              </AlertDescription>
            </div>
            <Button v-if="scopeIssues.some(({ issue }) => issue !== 'unauthorized')" variant="outline" class="retry-button" :aria-disabled="busy ? true : undefined" @click="refresh('retry', $event.currentTarget)"><RefreshCw aria-hidden="true" :class="{ spinning: busy === 'retry' }" />{{ busy === 'retry' ? '重试中' : '重试' }}</Button>
          </Alert>
          <div v-if="visibleRequests.length" ref="requestList" class="request-list" role="region" aria-label="合并请求列表" tabindex="-1">
            <div class="list-columns" aria-hidden="true">
              <span>标题</span><span>来源仓库</span><span>作者</span><span>更新时间</span><span>创建时间</span><span>状态</span>
            </div>
            <ul class="request-rows" role="list">
              <li v-for="request in visibleRequests" :key="request.id" class="request-row">
                <div class="request-cell title-cell">
                  <Button
                    variant="ghost" class="request-title" aria-haspopup="dialog"
                    :aria-label="request.title" :aria-describedby="`number-${request.id} repository-${request.id}`"
                    @click="openRequest(request, $event)"
                  >
                    <span class="request-title-copy">
                      <span class="request-name">{{ request.title }}</span>
                      <span :id="`number-${request.id}`" class="request-number">{{ requestNumber(request) }}</span>
                    </span>
                    <ChevronRight class="row-chevron" aria-hidden="true" />
                  </Button>
                </div>
                <div :id="`repository-${request.id}`" class="request-cell repo-cell">
                  <span class="repo-slug">{{ repositoryFor(request.repo).slug }}</span>
                  <PlatformMark :platform="repositoryFor(request.repo).platform" />
                  <small v-if="repositoryNamesakes.has(repositoryFor(request.repo).slug)" class="repo-instance">{{ repositoryFor(request.repo).host }}</small>
                </div>
                <div class="request-meta">
                  <div class="request-cell author-cell"><span class="field-label">作者</span>{{ request.author }}</div>
                  <div class="request-cell time-cell updated-cell"><span class="field-label">更新</span><time :datetime="request.updated.replace(' ', 'T')">{{ request.updated }}</time></div>
                  <div class="request-cell time-cell created-cell"><span class="field-label">创建</span><time :datetime="request.created.replace(' ', 'T')">{{ request.created }}</time></div>
                </div>
                <div class="request-cell status-cell"><RequestState :status="request.status" :draft="request.draft" /></div>
              </li>
            </ul>
          </div>
          <div v-else ref="emptyResult" class="empty-state" role="group" aria-labelledby="empty-result-heading" tabindex="-1">
            <FolderOpen v-if="!eligibleRepositories.length" aria-hidden="true" />
            <CircleAlert v-else-if="scopeIssues.length" aria-hidden="true" />
            <Search v-else aria-hidden="true" />
            <template v-if="!connected">
              <h2 id="empty-result-heading">尚未登记工程</h2>
              <p>在「产品与工程」登记并启用工程后，合并请求会显示在这里。</p>
              <Button variant="outline" @click="openSources">查看仓库来源</Button>
            </template>
            <template v-else-if="!eligibleRepositories.length">
              <h2 id="empty-result-heading">尚未纳入仓库</h2>
              <p>选择要读取的仓库来源。</p>
              <Button variant="outline" @click="openSources">配置仓库来源</Button>
            </template>
            <template v-else-if="scopeIssues.length">
              <h2 id="empty-result-heading">目前没有可显示的结果</h2>
              <p>受影响的仓库恢复读取后，结果可能增加。</p>
            </template>
            <template v-else>
              <h2 id="empty-result-heading">{{ scopeRepos.length ? '未找到符合条件的合并请求' : '当前范围没有仓库' }}</h2>
              <p v-if="hasFilters">调整搜索词或清除上方筛选条件。</p>
              <p v-else>可以切换状态查看其他合并请求。</p>
            </template>
          </div>
          <footer class="list-footer">
            <div role="status" aria-live="polite" aria-atomic="true" class="result-status">{{ resultStatus }}<span v-if="!busy && visibleRequests.length" class="sr-only">，第 {{ shownPage }} 页</span></div>
            <nav v-if="filteredRequests.length > pageSize || page > 1" class="pagination" aria-label="列表分页">
              <Button variant="outline" size="icon" :disabled="shownPage === 1" aria-label="上一页" @click="changePage(-1)"><ChevronLeft aria-hidden="true" /></Button>
              <span class="page-range" aria-hidden="true">{{ shownPage }}<template v-if="countsKnown"> / {{ pages }}</template> 页</span>
              <Button variant="outline" size="icon" :disabled="shownPage >= pages" aria-label="下一页" @click="changePage(1)"><ChevronRight aria-hidden="true" /></Button>
            </nav>
          </footer>
        </TabsContent>
      </Tabs>

      <footer class="prototype-bar" aria-label="原型演示工具">
        <label for="prototype-scene">原型场景</label>
        <Select :model-value="scene" @update:model-value="chooseScene">
          <SelectTrigger id="prototype-scene" class="filter-select scene-select"><SelectValue /></SelectTrigger>
          <SelectContent class="select-popover" align="start" :side-offset="4">
            <SelectItem v-for="item in scenes" :key="item.id" :value="item.id">{{ item.label }}</SelectItem>
          </SelectContent>
        </Select>
        <Button variant="ghost" class="reset-scene" @click="chooseScene(scene)">重置场景</Button>
      </footer>
    </main>
  </div>
  <RequestDetails
    :open="showDetails && !simulatedSource" :request="selected" :embedded="embedded"
    :show-repository-host="!!selected && repositoryNamesakes.has(repositoryFor(selected.repo).slug)"
    @close="showDetails = false" @restore-focus="event => restoreFocus(event, detailTrigger)"
  />
  <RepositorySources
    :open="showSources" :enabled="enabled" :connected="connected" :read-states="readStates"
    @close="showSources = false" @save="saveSources" @restore-focus="event => restoreFocus(event, sourcesTrigger)"
  />
  <Dialog v-model:open="showConnection">
    <DialogContent class="connection-dialog" :show-close-button="false" @close-auto-focus="closeConnection">
      <DialogTitle>检查连接</DialogTitle>
      <DialogDescription>此仓库的连接授权已失效。恢复授权后将重新读取该仓库。</DialogDescription>
      <div v-if="connectionRepo" class="connection-remote">
        <span><PlatformMark :platform="connectionRepo.platform" />{{ connectionRepo.host }}</span>
        <strong>{{ connectionRepo.slug }}</strong>
      </div>
      <div class="footer-actions">
        <DialogClose as-child><Button variant="outline">取消</Button></DialogClose>
        <Button class="action-primary" @click="recoverAuthorization">模拟恢复授权</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
