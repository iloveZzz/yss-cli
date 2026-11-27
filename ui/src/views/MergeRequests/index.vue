<script setup lang="ts">
import { computed, nextTick, onScopeDispose, shallowRef, useTemplateRef, watch } from 'vue'
import { ChevronLeft, ChevronRight, CircleAlert, ExternalLink, FolderOpen, RefreshCw, Search, X } from '@lucide/vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { registerWorkbenchRouteReads } from '@/hooks/useWorkbenchReads'
import { ApiError, expired } from '@/services/workbench'
import {
  getMergeRequestSources, saveMergeRequestSources,
  type MergeRequestOrder, type MergeRequestSourceSelection, type MergeRequestSources
} from '@/services/workbench-merge-requests'
import MergeRequestDetails from './components/MergeRequestDetails.vue'
import MergeRequestTable from './components/MergeRequestTable.vue'
import RepositorySources from './components/RepositorySources.vue'
import { useMergeRequestBrowser } from './hooks/useMergeRequestBrowser'
import {
  dateLabel, isTemporary, issueLabel, needsConnection, repositoryLabel, repositoryRequestsUrl, tabs,
  type MergeRequestRow, type MergeRequestTab
} from './schemas/requests'

const sources = shallowRef<MergeRequestSources>()
const sourcesLoading = shallowRef(false)
const sourcesError = shallowRef('')
const saveError = shallowRef('')
const saving = shallowRef(false)
const sourcesOpen = shallowRef(false)
let sourceRequest: AbortController | undefined

const activeTab = shallowRef<MergeRequestTab>('open')
const query = shallowRef<string | number>('')
const queryValue = computed(() => String(query.value ?? '').trim().toLocaleLowerCase())
const platform = shallowRef('')
const repositoryId = shallowRef('')
const order = shallowRef<MergeRequestOrder>('updated')
const hasFilters = computed(() => !!(queryValue.value || platform.value || repositoryId.value || order.value === 'created'))

const included = computed(() => (sources.value?.repositories ?? []).filter((repository) => repository.included))
const platformRepositories = computed(() => included.value.filter((repository) =>
  !platform.value || repository.platform === platform.value))
const queryPlatform = computed(() => {
  if (/^#\d+$/u.test(queryValue.value)) return 'github'
  if (/^!\d+$/u.test(queryValue.value)) return 'gitlab'
  return undefined
})
const filterKey = computed(() => JSON.stringify([platform.value, repositoryId.value]))
const scope = computed(() => !saving.value && !sourcesLoading.value && !expired.value
  ? platformRepositories.value.filter((repository) =>
      (!repositoryId.value || repository.id === repositoryId.value) &&
      (!queryPlatform.value || repository.platform === queryPlatform.value))
  : [])
const repositoryOptions = computed(() => [
  { value: '', label: '全部仓库' },
  ...platformRepositories.value.map((repository) => ({ value: repository.id, label: repositoryLabel(repository) }))
])
const namesakes = computed(() => {
  const seen = new Set<string>()
  return new Set(included.value.flatMap(({ slug }) => {
    if (seen.has(slug)) return [slug]
    seen.add(slug)
    return []
  }))
})
const platforms = [
  { value: '', label: '全部平台' }, { value: 'github', label: 'GitHub' }, { value: 'gitlab', label: 'GitLab' }
]
const orders = [
  { value: 'updated', label: '最近更新' }, { value: 'created', label: '最近创建' }
]

function setPlatform(value: string) {
  platform.value = value
  if (!platformRepositories.value.some((repository) => repository.id === repositoryId.value)) repositoryId.value = ''
}

function applySources(next: MergeRequestSources) {
  if (!next.repositories.some((repository) => repository.included && repository.id === repositoryId.value))
    repositoryId.value = ''
  sources.value = next
}

const {
  busy, page, pages, rows, total, counts, complete, issues, readAt, repositoryReads, hasMore, hasKnownMatches,
  loaded, prepareRefresh, clearPreparedRefresh, continueReading, changePage, retry, detail, openDetail, clearDetail
} = useMergeRequestBrowser(scope, activeTab, queryValue, order, filterKey)
const pageBusy = computed(() => sourcesLoading.value || busy.value || saving.value)

const heading = useTemplateRef<HTMLElement>('heading')
const searchField = useTemplateRef<InstanceType<typeof Input>>('search-field')
const results = useTemplateRef<HTMLElement>('results')
let detailTrigger: HTMLElement | undefined

const footerText = computed(() => {
  if (issues.value.length) return total.value ? `已取得 ${total.value} 条，部分仓库读取不完整` : '当前范围读取不完整'
  if (sourcesError.value) return '未能读取仓库来源'
  if (pageBusy.value && !rows.value.length) return '正在读取合并请求'
  if (hasMore.value) {
    return queryValue.value
      ? `已确认 ${total.value} 条，仍有历史分页待搜索`
      : `已确认 ${total.value} 条，可继续读取后续页`
  }
  if (!scope.value.length) return ''
  return counts.value?.[activeTab.value] === undefined ? `已确认 ${total.value} 条` : `共 ${total.value} 条`
})
const emptyTitle = computed(() => {
  if (sourcesError.value) return '未能读取仓库来源'
  if (!included.value.length) return '暂无可浏览仓库'
  if (issues.value.length) return '目前没有可显示的结果'
  if (hasMore.value) return hasKnownMatches.value ? '待汇总列表顺序' : '已读范围暂无匹配'
  if (!scope.value.length && !queryValue.value) return '当前范围没有仓库'
  return '没有匹配的请求'
})
const emptyDescription = computed(() => {
  if (sourcesError.value) return sourcesError.value
  if (!included.value.length) {
    return sources.value?.projects.length
      ? '查看仓库来源，核对已登记工程和远端配置。'
      : '在「产品与工程」登记并启用工程后，即可浏览合并请求。'
  }
  if (issues.value.length) return '受影响的仓库恢复读取后，结果可能增加。'
  if (hasMore.value) {
    return hasKnownMatches.value ? '继续读取其余仓库后可查看已发现的结果。' : '继续读取可查找更早的合并请求。'
  }
  return hasFilters.value ? '可调整搜索词或筛选条件。' : '可切换状态查看其他合并请求。'
})

async function refreshSources() {
  if (expired.value || saving.value || sourcesLoading.value) return
  const request = new AbortController()
  sourceRequest = request
  prepareRefresh()
  sourcesLoading.value = true
  sourcesError.value = ''
  try {
    const next = await getMergeRequestSources(request.signal)
    if (sourceRequest === request && !request.signal.aborted && !expired.value) applySources(next)
  } catch {
    if (sourceRequest === request && !request.signal.aborted && !expired.value) {
      sources.value = undefined
      sourcesError.value = '无法核对本机工程及仓库来源，可以重试。'
    }
  } finally {
    if (sourceRequest === request) {
      sourceRequest = undefined
      sourcesLoading.value = false
      clearPreparedRefresh()
    }
  }
}

function selectedKeys(selected: MergeRequestSourceSelection[]) {
  return new Set(selected.map(({ projectId, repositoryId: repo }) => JSON.stringify([projectId, repo])))
}

function matchesSelection(next: MergeRequestSources, selected: MergeRequestSourceSelection[]) {
  const previous = selectedKeys(selected)
  const current = selectedKeys(next.projects.flatMap((project) =>
    project.candidates.filter((candidate) => candidate.selected).map((candidate) => ({
      projectId: project.projectId, repositoryId: candidate.repositoryId
    }))))
  return previous.size === current.size && [...current].every((key) => previous.has(key))
}

async function saveSources(selected: MergeRequestSourceSelection[]) {
  if (expired.value || saving.value || sourcesLoading.value || !sources.value) return
  const revision = sources.value.revision
  const request = new AbortController()
  sourceRequest = request
  saving.value = true
  saveError.value = ''
  const current = () => sourceRequest === request && !request.signal.aborted && !expired.value
  try {
    const next = await saveMergeRequestSources(revision, selected, request.signal)
    if (current()) {
      applySources(next)
      sourcesOpen.value = false
    }
  } catch (cause) {
    if (!current()) return
    try {
      const next = await getMergeRequestSources(request.signal)
      if (!current()) return
      if (matchesSelection(next, selected)) {
        applySources(next)
        sourcesOpen.value = false
      } else if (next.revision !== revision || (cause instanceof ApiError && cause.status === 409)) {
        applySources(next)
        saveError.value = '仓库来源已变化。请核对当前选项后重新保存。'
      } else {
        saveError.value = '未能保存这次选择。请重试。'
      }
    } catch {
      if (current()) {
        sources.value = undefined
        sourcesError.value = '保存结果待核对，请重新读取当前来源。'
      }
    }
  } finally {
    if (sourceRequest === request) {
      sourceRequest = undefined
      saving.value = false
    }
  }
}

registerWorkbenchRouteReads({ busy: pageBusy, readAt, refresh: refreshSources })
watch(expired, (value) => {
  if (value) {
    sourceRequest?.abort()
    clearPreparedRefresh()
    sources.value = undefined
    sourcesOpen.value = false
  } else void refreshSources()
}, { immediate: true, flush: 'sync' })
onScopeDispose(() => sourceRequest?.abort())

function showSources() {
  saveError.value = ''
  sourcesOpen.value = true
}

function clearSearch() {
  query.value = ''
  searchField.value?.$el?.focus()
}

function clearFilters() {
  query.value = ''
  setPlatform('')
  repositoryId.value = ''
  order.value = 'updated'
  searchField.value?.$el?.focus()
}

function openRequest(row: MergeRequestRow, event: MouseEvent) {
  detailTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  void openDetail(row)
}

const detailRow = computed(() => {
  const target = detail.value
  return target && rows.value.find((entry) =>
    entry.repository.id === target.repository.id && entry.request.number === target.number)
})

function retryDetail() {
  if (detailRow.value) void openDetail(detailRow.value)
}

async function restoreDetailFocus(event: Event) {
  event.preventDefault()
  const trigger = detailTrigger
  detailTrigger = undefined
  await nextTick()
  if (detail.value || expired.value) return
  if (trigger?.isConnected) trigger.focus({ preventScroll: true })
  else (results.value ?? heading.value)?.focus({ preventScroll: true })
}

async function readWithFocus(event: MouseEvent, read: () => Promise<unknown>) {
  const trigger = event.currentTarget
  const focused = trigger === document.activeElement
  await read()
  await nextTick()
  if (focused && trigger instanceof HTMLElement && !trigger.isConnected && document.activeElement === document.body)
    results.value?.focus()
}
</script>

<template>
  <template v-if="!expired">
    <header class="page-heading requests-heading">
      <div>
        <h1 ref="heading" tabindex="-1">合并请求</h1>
        <p v-if="sources" class="muted request-scope">
          <span>已纳入 {{ included.length }} 个仓库</span>
          <span v-if="readAt">读取于 <time :datetime="readAt">{{ dateLabel(readAt) }}</time></span>
        </p>
      </div>
      <div class="heading-actions">
        <Button type="button" variant="outline" @click="showSources"><FolderOpen aria-hidden="true" />仓库来源</Button>
        <Button type="button" variant="outline" :aria-disabled="pageBusy" @click="!pageBusy && refreshSources()">
          <RefreshCw aria-hidden="true" />刷新
        </Button>
      </div>
    </header>

    <Tabs
      :model-value="activeTab"
      activation-mode="manual"
      class="panel requests-panel"
      @update:model-value="value => { if (tabs.some(tab => tab.value === value)) activeTab = value as MergeRequestTab }"
    >
      <div class="request-controls">
        <TabsList aria-label="合并请求状态" class="request-tabs">
          <TabsTrigger v-for="tab in tabs" :key="tab.value" :value="tab.value">
            {{ tab.label }}<span v-if="counts?.[tab.value] !== undefined" class="tab-count">{{ counts[tab.value] }}<span class="sr-only"> 条</span></span>
          </TabsTrigger>
        </TabsList>
        <div class="request-filters" role="search" aria-label="筛选合并请求">
          <div class="search-field">
            <label for="merge-request-search" class="sr-only">搜索标题或编号</label>
            <Search class="search-icon" aria-hidden="true" />
            <Input id="merge-request-search" ref="search-field" v-model="query" autocomplete="off" type="text" placeholder="搜索标题或编号" />
            <Button v-if="query" type="button" variant="ghost" size="icon" class="clear-search" aria-label="清除搜索" @click="clearSearch">
              <X aria-hidden="true" />
            </Button>
          </div>
          <SingleSelect :model-value="platform" :options="platforms" aria-label="平台" :data-filtered="!!platform || undefined" @update:model-value="setPlatform" />
          <SingleSelect v-model="repositoryId" :options="repositoryOptions" aria-label="仓库" class="repository-select" :data-filtered="!!repositoryId || undefined" />
          <SingleSelect :model-value="order" :options="orders" aria-label="排序" class="order-select" @update:model-value="value => { if (value === 'updated' || value === 'created') order = value }" />
          <Button v-if="hasFilters" type="button" variant="ghost" class="clear-filters" @click="clearFilters">清除筛选</Button>
        </div>
      </div>

      <TabsContent :value="activeTab" class="requests-content">
        <Alert v-if="issues.length" class="notice warning repository-issues" role="group" aria-labelledby="repository-issues-title">
          <CircleAlert aria-hidden="true" />
          <AlertTitle id="repository-issues-title">{{ loaded ? '部分仓库读取不完整' : '当前范围读取失败' }}</AlertTitle>
          <AlertDescription>
            <div v-for="{ repository, issue, readAt: lastSuccess } in issues" :key="repository.id" class="repository-issue">
              <div>
                <strong>{{ repositoryLabel(repository) }}</strong>
                <p>{{ issueLabel(issue) }}。{{ issue.message }}</p>
                <p v-if="lastSuccess">上次成功 <time :datetime="lastSuccess">{{ dateLabel(lastSuccess) }}</time>，{{ isTemporary(issue) ? '数据未更新。' : '仓库尚未读完。' }}</p>
              </div>
              <div class="issue-actions">
                <Button v-if="needsConnection(issue)" as-child variant="outline">
                  <RouterLink to="/settings">检查连接</RouterLink>
                </Button>
                <Button v-if="repositoryRequestsUrl(repository)" as-child variant="outline">
                  <a :href="repositoryRequestsUrl(repository)" target="_blank" rel="noopener noreferrer"><ExternalLink aria-hidden="true" />在原站继续查找</a>
                </Button>
                <Button type="button" variant="outline" :disabled="pageBusy" :aria-label="'重试 ' + repositoryLabel(repository)" @click="readWithFocus($event, () => retry(repository.id))">
                  重试
                </Button>
              </div>
            </div>
          </AlertDescription>
        </Alert>

        <div ref="results" class="requests-results" role="region" aria-label="合并请求结果" :aria-busy="pageBusy" tabindex="-1">
          <MergeRequestTable v-if="rows.length" :rows="rows" :namesakes="namesakes" @open="openRequest" />
          <LoadingSkeleton v-else-if="pageBusy" variant="table" :rows="5" />
          <Empty v-else class="requests-empty">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <CircleAlert v-if="sourcesError || issues.length" aria-hidden="true" />
                <FolderOpen v-else-if="!included.length" aria-hidden="true" />
                <Search v-else aria-hidden="true" />
              </EmptyMedia>
              <EmptyTitle>{{ emptyTitle }}</EmptyTitle>
              <EmptyDescription>{{ emptyDescription }}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent v-if="sourcesError || !included.length">
                <Button v-if="sourcesError" type="button" variant="outline" @click="readWithFocus($event, refreshSources)">重试读取</Button>
              <template v-else>
                <Button type="button" variant="outline" @click="showSources">查看仓库来源</Button>
                <Button v-if="sources && !sources.projects.length" as-child variant="ghost"><RouterLink to="/projects">前往产品与工程</RouterLink></Button>
              </template>
            </EmptyContent>
          </Empty>
        </div>

        <footer class="request-footer">
          <div role="status" aria-live="polite" class="footer-status">
            {{ footerText }}<span v-if="rows.length" class="sr-only">，第 {{ page }} 页</span>
            <p v-if="complete && counts && counts.open === undefined">部分请求的状态不可用，分类数量暂未显示。</p>
          </div>
          <div class="footer-actions">
            <LoadingIndicator :active="pageBusy && rows.length > 0" label="正在读取" />
            <Button v-if="hasMore" type="button" variant="outline" :aria-disabled="pageBusy" @click="!pageBusy && readWithFocus($event, continueReading)">
              {{ queryValue ? '继续搜索' : '继续读取' }}
            </Button>
            <nav v-if="pages > 1" class="pagination" aria-label="合并请求分页">
              <Button type="button" variant="outline" size="icon" :aria-disabled="page <= 1" aria-label="上一页" @click="changePage(-1)"><ChevronLeft aria-hidden="true" /></Button>
              <span>{{ page }}<template v-if="counts?.[activeTab] !== undefined"> / {{ pages }}</template> 页</span>
              <Button type="button" variant="outline" size="icon" :aria-disabled="page >= pages" aria-label="下一页" @click="changePage(1)"><ChevronRight aria-hidden="true" /></Button>
            </nav>
          </div>
        </footer>
      </TabsContent>
    </Tabs>

    <RepositorySources
      v-model="sourcesOpen"
      :sources="sources"
      :loading="sourcesLoading"
      :saving="saving"
      :load-error="sourcesError"
      :save-error="saveError"
      :repository-reads="repositoryReads"
      @save="saveSources"
      @retry="refreshSources"
      @dismiss-error="saveError = ''"
    />
    <MergeRequestDetails :detail="detail" :can-retry="!!detailRow" @close="clearDetail" @restore-focus="restoreDetailFocus" @retry="retryDetail" />
  </template>
</template>

<style scoped>
.requests-heading { align-items: flex-start; }
.request-scope { display: flex; flex-wrap: wrap; gap: 4px 8px; font-size: 12px; }
.request-scope > span + span::before { content: '·'; margin-inline-end: 8px; }
.heading-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 8px; }
.requests-panel { container: requests-panel / inline-size; display: block; background-color: var(--workbench-glass-card); }
.request-controls { padding: 0 16px 12px; }
.request-tabs[data-slot='tabs-list'] {
  width: 100%;
  min-height: 44px;
  height: auto;
  justify-content: flex-start;
  align-items: stretch;
  gap: 16px;
  padding: 0;
  margin-block-end: 12px;
  border-radius: 0;
  border-bottom: 1px solid var(--yss-colors-border);
  background: transparent;
}
.request-tabs :deep([data-slot='tabs-trigger']) {
  flex: none;
  min-height: 44px;
  height: auto;
  padding-inline: 4px;
  border: 0;
  border-bottom: 2px solid transparent;
  border-radius: 0;
  color: var(--brand-color-text-secondary);
  background: transparent;
  box-shadow: none;
}
.request-tabs :deep([data-slot='tabs-trigger'][data-state='active']) {
  color: var(--yss-color-primary-control-active);
  border-bottom-color: var(--yss-color-primary-control);
  font-weight: 600;
}
.tab-count {
  min-width: 18px;
  padding: 2px 4px;
  border-radius: 5px;
  background: var(--brand-color-fill-secondary);
  color: var(--brand-color-text-secondary);
  font-size: 10px;
  line-height: 1.15;
  font-variant-numeric: tabular-nums;
}
[data-state='active'] .tab-count { background: var(--yss-colors-primary-bg); color: var(--yss-color-primary-control-active); }
.request-filters { display: flex; flex-wrap: wrap; align-items: stretch; gap: 8px; }
.search-field { position: relative; flex: 1 1 190px; max-width: 360px; min-width: 0; }
.search-field :deep(input) { padding-inline: 30px 36px; }
.search-icon { position: absolute; top: 50%; left: 9px; width: 14px; height: 14px; color: var(--brand-color-text-secondary); transform: translateY(-50%); pointer-events: none; }
.clear-search[data-slot='button'] { position: absolute; top: 0; right: 0; width: 34px; padding: 0; }
.request-filters :deep(.repository-select) { width: min(215px, 36%); min-width: 122px; }
.request-filters :deep(.order-select) { margin-inline-start: auto; }
.request-filters :deep([data-filtered='true']) {
  color: var(--yss-color-primary-control-active);
  background-color: var(--yss-colors-primary-bg);
}
.requests-content { border-top: 1px solid var(--yss-colors-border); }
.repository-issues { margin: 12px 16px; }
.repository-issue { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; width: 100%; }
.repository-issue + .repository-issue { margin-block-start: 9px; padding-block-start: 9px; border-top: 1px solid var(--yss-colors-border); }
.repository-issue p { margin: 2px 0 0; }
.issue-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
.issue-actions :deep([data-slot='button']) { margin: 0; }
.requests-empty { min-height: 238px; gap: 12px; }
.requests-results { outline-offset: -2px; }
.request-footer { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 8px 12px; align-items: center; padding: 10px 16px; border-top: 1px solid var(--yss-colors-border); }
.footer-status { color: var(--brand-color-text-secondary); font-size: 12px; }
.footer-actions, .pagination { display: flex; flex-wrap: wrap; gap: 7px; align-items: center; }
.pagination { white-space: nowrap; color: var(--brand-color-text-secondary); font-size: 12px; }
:is(.heading-actions, .footer-actions) :deep(button[aria-disabled='true']) { opacity: .5; cursor: default; }
@container requests-panel (width < 600px) {
  .request-controls, .request-footer { padding-inline: 10px; }
  .request-filters { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .search-field { grid-column: 1 / -1; max-width: none; }
  .request-filters :deep([data-slot='select-trigger']) { width: 100%; min-width: 0; margin-inline-start: 0; padding-inline: 6px; }
  .request-tabs[data-slot='tabs-list'] { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 0; }
  .request-tabs :deep([data-slot='tabs-trigger']) { padding-inline: 2px; font-size: 12px; gap: 3px; }
  .repository-issues { margin-inline: 10px; }
  .clear-filters { justify-self: start; }
}
@container requests-panel (width < 385px) {
  .request-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (width < 720px) {
  .clear-search[data-slot='button'], .pagination :deep([data-slot='button']) { min-width: 44px; }
  .search-field :deep(input) { padding-inline-end: 45px; }
}
</style>
