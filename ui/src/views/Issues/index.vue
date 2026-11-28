<script setup lang="ts">
import { computed, nextTick, shallowRef, useTemplateRef } from 'vue'
import { ChevronLeft, ChevronRight, Clock3, FolderOpen, RefreshCw, Search, TriangleAlert, X } from '@lucide/vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import WorkbenchBreadcrumb from '@/components/WorkbenchBreadcrumb.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { registerWorkbenchRouteReads } from '@/hooks/useWorkbenchReads'
import IssueReader from './components/IssueReader.vue'
import IssueSourcePicker from './components/IssueSourcePicker.vue'
import IssueTable from './components/IssueTable.vue'
import { useIssueBrowser } from './hooks/useIssueBrowser'
import { issueStates, problemTitle, retryReadLabel } from './schemas/issues'

const {
  sources, sourceId, source, sourcesLoading, sourcesProblem, state, query, page, visitedPages,
  pageData, pageProblem, pageLoading, rows, withheldCount, reader, busy, readAt, refreshSources,
  selectSource, selectState, search, previousPage, nextPage, openReader, closeReader, retryPage, retryDetail
} = useIssueBrowser()
registerWorkbenchRouteReads({ busy, readAt, refresh: refreshSources })

const draft = shallowRef<string | number>('')
const composing = shallowRef(false)
const heading = useTemplateRef<InstanceType<typeof WorkbenchBreadcrumb>>('heading')
const searchField = useTemplateRef<InstanceType<typeof Input>>('search-field')
const results = useTemplateRef<HTMLElement>('results')
let trigger: HTMLElement | null = null

const label = '搜索标题、正文或 #编号'
const hasNextPage = computed(() => {
  const next = pageData.value?.nextPage
  return typeof next === 'number' && next > page.value
})
const emptyQueryTitle = computed(() => {
  if (withheldCount.value || hasNextPage.value) return '此页没有可显示的 Issue'
  return pageData.value?.incomplete ? '已读范围内没有匹配的 Issue' : '没有匹配的 Issue'
})
const footerText = computed(() => {
  if (!pageData.value) return ''
  const term = query.value ? `“${query.value}” · ` : ''
  return term + (pageData.value.incomplete ? '本页已读 ' : '本页 ') + rows.value.length + ' 条'
})
const sourceOptions = computed(() => sources.value?.sources ?? [])
const warnings = computed(() => sources.value?.warnings ?? [])

function changeState(value: string | number) {
  const selected = issueStates.find(entry => entry.value === value)
  if (selected) selectState(selected.value)
}

function preventCompositionSubmit(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229 || composing.value) event.preventDefault()
}

function submitSearch() {
  if (composing.value) return
  const field = searchField.value?.$el
  search(field instanceof HTMLInputElement ? field.value : String(draft.value))
}

function focusSearch(preventScroll = false) {
  const field = searchField.value?.$el
  if (!(field instanceof HTMLInputElement) || !field.isConnected) return false
  field.focus({ preventScroll })
  return true
}

async function clearSearch() {
  draft.value = ''
  search('')
  await nextTick()
  focusSearch()
}

function showDetail(number: number, button: HTMLElement | null) {
  trigger = button
  void openReader(number)
}

async function restoreFocus(event: Event) {
  event.preventDefault()
  const previous = trigger
  trigger = null
  await nextTick()
  if (reader.value) return
  if (previous?.isConnected) previous.focus({ preventScroll: true })
  else if (!focusSearch()) heading.value?.focus()
}

function retryResults() {
  results.value?.focus({ preventScroll: true })
  void retryPage()
}

function changePage(direction: 'back' | 'forward') {
  if (sourcesLoading.value || pageLoading.value || (direction === 'back' ? visitedPages.value.length < 2 : !hasNextPage.value)) return
  results.value?.scrollIntoView({ block: 'nearest' })
  if (direction === 'back') previousPage()
  else nextPage()
}
</script>

<template>
  <header class="page-heading issues-heading">
    <WorkbenchBreadcrumb ref="heading" title="Issues" />
  </header>

  <section class="panel issues-panel" aria-label="仓库 Issues">
    <IssueSourcePicker :source="source" :options="sourceOptions" :disabled="sourcesLoading" @select="selectSource" />

    <Alert v-if="warnings.length" class="notice warning source-warnings">
      <TriangleAlert aria-hidden="true" />
      <AlertTitle>部分工程的仓库来源需核对</AlertTitle>
      <AlertDescription>
        <ul>
          <li v-for="warning in warnings" :key="warning.projectId"><strong>{{ warning.projectName }}</strong> · {{ warning.message }}</li>
        </ul>
      </AlertDescription>
    </Alert>

    <Tabs v-if="source" :model-value="state" activation-mode="manual" class="issues-tabs" @update:model-value="changeState">
      <TabsList class="issue-tabbar" aria-label="Issue 状态">
        <TabsTrigger v-for="tab in issueStates" :key="tab.value" :value="tab.value">{{ tab.label }}</TabsTrigger>
      </TabsList>

      <TabsContent :value="state" :force-mount="true">
        <div class="issues-toolbar">
          <form class="issue-search" role="search" aria-label="搜索当前仓库的 Issues" @submit.prevent="submitSearch">
            <div class="search-field">
              <Search class="search-icon" aria-hidden="true" />
              <label class="sr-only" for="issue-search">{{ label }}</label>
              <Input
                id="issue-search"
                ref="search-field"
                v-model="draft"
                type="text"
                autocomplete="off"
                :placeholder="label"
                @keydown.enter="preventCompositionSubmit"
                @compositionstart="composing = true"
                @compositionend="composing = false"
              />
              <Button v-if="draft || query" type="button" variant="ghost" class="clear-search" aria-label="清除搜索" @click="clearSearch">
                <X aria-hidden="true" />
              </Button>
            </div>
            <Button type="submit" variant="outline">搜索</Button>
          </form>
          <span class="issue-order"><Clock3 aria-hidden="true" />最近更新</span>
        </div>

        <div ref="results" class="issue-results" role="region" aria-label="Issues 查询结果" :aria-busy="pageLoading || sourcesLoading" tabindex="-1">
          <Alert v-if="pageData?.incomplete" class="notice warning results-notice">
            <TriangleAlert aria-hidden="true" />
            <AlertTitle>仓库只返回了部分结果</AlertTitle>
            <AlertDescription>可收窄搜索条件，或重新读取当前仓库。</AlertDescription>
            <Button type="button" variant="outline" class="justify-self-start" @click="retryResults">重试读取</Button>
          </Alert>
          <Alert v-if="withheldCount" class="notice warning results-notice">
            <TriangleAlert aria-hidden="true" />
            <AlertTitle>此页有 {{ withheldCount }} 条 Issue 已收起</AlertTitle>
            <AlertDescription>条目可能已移除，或当前账号无法访问。</AlertDescription>
            <Button type="button" variant="outline" class="justify-self-start" @click="retryResults">重新检查不可用条目</Button>
          </Alert>
          <LoadingSkeleton v-if="pageLoading || sourcesLoading" variant="table" :rows="5" label="正在读取 Issues" />
          <IssueTable v-else-if="rows.length" :rows="rows" :source-id="sourceId" @open="showDetail" />
          <Empty v-else class="issues-empty">
            <EmptyHeader>
              <EmptyMedia variant="icon"><TriangleAlert v-if="pageProblem" aria-hidden="true" /><Search v-else aria-hidden="true" /></EmptyMedia>
              <EmptyTitle>{{ pageProblem ? problemTitle(pageProblem, '未能读取仓库') : emptyQueryTitle }}</EmptyTitle>
              <EmptyDescription v-if="pageProblem">{{ pageProblem.message }}</EmptyDescription>
              <EmptyDescription v-else-if="hasNextPage">可继续查看下一页。</EmptyDescription>
              <EmptyDescription v-else-if="pageData?.incomplete">其他 Issue 仍可能匹配当前条件。</EmptyDescription>
              <EmptyDescription v-else-if="query">可以修改搜索内容，或切换状态再试。</EmptyDescription>
              <EmptyDescription v-else>可以切换状态查看其他 Issue。</EmptyDescription>
            </EmptyHeader>
            <EmptyContent v-if="pageProblem || query" class="empty-actions">
              <Button v-if="pageProblem" type="button" variant="outline" @click="retryResults">
                <RefreshCw aria-hidden="true" />{{ retryReadLabel(pageProblem) }}
              </Button>
              <Button v-if="query" type="button" variant="outline" @click="clearSearch">清除搜索条件</Button>
            </EmptyContent>
          </Empty>
        </div>

        <footer v-if="pageData || page > 1" class="issue-footer">
          <span role="status">{{ footerText }}</span>
          <nav v-if="visitedPages.length > 1 || hasNextPage" aria-label="Issues 分页" class="issue-pagination">
            <Button type="button" variant="outline" size="icon" :aria-disabled="sourcesLoading || pageLoading || visitedPages.length < 2" aria-label="上一页" @click="changePage('back')">
              <ChevronLeft aria-hidden="true" />
            </Button>
            <span aria-live="polite">第 {{ page }} 页</span>
            <Button type="button" variant="outline" size="icon" :aria-disabled="sourcesLoading || pageLoading || !hasNextPage" aria-label="下一页" @click="changePage('forward')">
              <ChevronRight aria-hidden="true" />
            </Button>
          </nav>
        </footer>
      </TabsContent>
    </Tabs>

    <LoadingSkeleton v-else-if="sourcesLoading" variant="table" :rows="5" label="正在读取仓库来源" />
    <Empty v-else class="issues-empty">
      <EmptyHeader>
        <EmptyMedia variant="icon"><TriangleAlert v-if="sourcesProblem || pageProblem" aria-hidden="true" /><FolderOpen v-else aria-hidden="true" /></EmptyMedia>
        <EmptyTitle>{{ sourcesProblem ? '未能读取仓库来源' : pageProblem ? '原仓库来源已不可用' : '暂无可用的仓库来源' }}</EmptyTitle>
        <EmptyDescription>
          {{ sourcesProblem?.message || pageProblem?.message || '在「产品与工程」核对已启用工程的 Issue 跟踪仓库或远端地址。' }}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent class="empty-actions">
        <Button type="button" variant="outline" @click="refreshSources"><RefreshCw aria-hidden="true" />重新读取来源</Button>
        <Button as-child variant="ghost"><RouterLink to="/projects">产品与工程</RouterLink></Button>
      </EmptyContent>
    </Empty>
  </section>

  <IssueReader :source="source" :reader="reader" @close="closeReader" @restore-focus="restoreFocus" @retry="retryDetail" />
</template>

<style scoped>
.issues-heading { margin-block-end: 14px; }
.issues-panel { container: issue-panel / inline-size; min-width: 0; background-color: var(--workbench-glass-card); }
.issues-tabs { gap: 0; }
.issue-tabbar[data-slot='tabs-list'] { display: flex; justify-content: flex-start; align-items: stretch; width: auto; height: auto; min-height: 42px; gap: 20px; margin-inline: 20px; padding: 0; border-radius: 0; border-bottom: 1px solid var(--yss-colors-border); background: transparent; }
.issue-tabbar :deep([data-slot='tabs-trigger']) { flex: none; min-height: 42px; height: auto; padding-inline: 2px; border: 0; border-radius: 0; border-bottom: 2px solid transparent; background: transparent; color: var(--brand-color-text-secondary); box-shadow: none; font-size: 13px; }
.issue-tabbar :deep([data-state='active']) { border-bottom-color: var(--yss-color-primary-control); color: var(--yss-color-primary-control-active); font-weight: 650; }
.issues-toolbar, .issue-search, .issue-order, .issue-pagination { display: flex; align-items: center; }
.issues-toolbar { justify-content: space-between; flex-wrap: wrap; gap: 8px 18px; padding: 13px 20px; }
.issue-search { flex: 1; gap: 8px; max-width: 444px; min-width: 0; }
.search-field { flex: 1; position: relative; min-width: 0; }
.search-field :deep(input) { padding-inline: 33px 36px; background: var(--workbench-glass-card); }
.search-icon { position: absolute; inset: 50% auto auto 11px; width: 14px; height: 14px; color: var(--brand-color-text-secondary); transform: translateY(-50%); pointer-events: none; }
.clear-search[data-slot='button'] { position: absolute; inset: 0 0 auto auto; width: 34px; padding: 0; }
.clear-search svg { width: 14px; height: 14px; }
.issue-order { flex: none; gap: 5px; color: var(--brand-color-text-secondary); font-size: 11px; }
.issue-order svg { width: 13px; height: 13px; }
.issue-results:focus { outline-offset: -3px; }
.issues-empty { min-height: 230px; gap: 12px; padding: 24px 14px; border: 0; }
.empty-actions { flex-flow: row wrap; justify-content: center; }
.source-warnings { margin: 0 20px 10px; }
.results-notice { margin: 0 20px 13px; }
.source-warnings li + li { margin-block-start: 3px; }
.issue-footer { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; min-height: 55px; gap: 8px; padding: 9px 20px; color: var(--brand-color-text-secondary); font-size: 12px; }
.issue-footer > [role='status'] { min-width: 0; overflow-wrap: anywhere; }
.issue-pagination { gap: 9px; font-variant-numeric: tabular-nums; white-space: nowrap; }
.issue-pagination [data-slot='button'] { width: 34px; padding: 0; }
.issue-pagination [aria-disabled='true'] { opacity: .5; cursor: default; }
@container issue-panel (width < 680px) {
  .issue-order { display: none; }
  .issue-search { max-width: none; }
}
@container issue-panel (width < 440px) {
  .issue-tabbar[data-slot='tabs-list'] { gap: 13px; margin-inline: 12px; }
  .issues-toolbar, .issue-footer { padding-inline: 12px; }
  .source-warnings, .results-notice { margin-inline: 12px; }
  .issue-search { gap: 5px; }
  .issue-search > button { padding-inline: 9px; }
  .search-field :deep(input) { padding-inline-start: 8px; }
  .search-icon { display: none; }
}
@media (width < 720px) {
  .issues-heading { margin-block-end: 8px; }
  .issue-tabbar :deep([data-slot='tabs-trigger']) { min-height: 44px; }
  .issue-pagination [data-slot='button'], .clear-search[data-slot='button'] { width: 44px; }
  .search-field:has(.clear-search) :deep(input) { padding-inline-end: 44px; }
  .search-field:not(:has(.clear-search)) :deep(input) { padding-inline-end: 8px; }
}
@media (forced-colors: active) {
  .issue-tabbar :deep([data-state='active']) { outline: 2px solid Highlight; outline-offset: -2px; }
}
</style>
