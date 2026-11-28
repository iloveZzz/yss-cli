<script setup lang="ts">
import { computed, useId, useTemplateRef, watch } from 'vue'
import { ArrowRight, CalendarDays, ExternalLink, GitBranch, GitFork, ListChecks, ShieldCheck } from '@lucide/vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import type { MergeRequestDetailSection } from '@/services/workbench-merge-requests'
import type { MergeRequestDetailRead, MergeRequestSectionRead } from '../hooks/useMergeRequestDetail'
import {
  dateLabel, issueLabel, losesAccess, needsConnection, platformLabels, repositoryHost, requestNumber, safeHttpUrl
} from '../schemas/requests'
import MergeRequestMarkdown from './MergeRequestMarkdown.vue'
import MergeRequestState from './MergeRequestState.vue'

const props = defineProps<{ canRetry: boolean; detail?: MergeRequestDetailRead }>()
const emit = defineEmits<{
  close: []
  restoreFocus: [event: Event]
  retry: [section: MergeRequestDetailSection]
}>()
const heading = useTemplateRef<InstanceType<typeof SheetTitle>>('heading')
const body = useTemplateRef<HTMLElement>('body')
const sectionId = useId()
const result = computed(() => props.detail?.result)
const selected = computed(() => result.value ?? props.detail?.selected)
const pending = computed(() => !!props.detail && !result.value && !props.detail.issue)
const requestUrl = computed(() => safeHttpUrl(result.value?.url, props.detail?.repository.url))
const history = computed(() => props.detail?.signals.history)
const checkSections = [
  { section: 'review', label: '评审与审批', icon: ShieldCheck },
  { section: 'ci', label: '当前 CI', icon: ListChecks }
] as const
const checks = computed(() => checkSections.map(group => ({ ...group, read: props.detail?.signals[group.section] })))
const times = computed(() => {
  const request = result.value
  if (!request) return []
  return [
    { label: '创建时间', at: request.createdAt },
    ...(request.mergedAt ? [{ label: '合并时间', at: request.mergedAt }] : []),
    ...(request.closedAt ? [{ label: '关闭时间', at: request.closedAt }] : [])
  ]
})
const signalLabels: Record<string, string> = {
  approved: '已批准', changesRequested: '有变更请求', reviewRequired: '等待评审',
  passing: '通过', failing: '失败', pending: '等待结果', noRecords: '暂无记录',
  unavailable: '不可用', skipped: '已跳过', cancelled: '已取消', canceled: '已取消',
  neutral: '中性结果', readFailed: '读取失败'
}

function signalLabel(status: string) {
  return signalLabels[status] ?? signalLabels.unavailable
}

function readLabel(read?: MergeRequestSectionRead) {
  if (!read || read.state === 'loading') return '读取中'
  if (read.state === 'failed') return issueLabel(read.issue)
  return signalLabel(read.signal.status)
}

function canRetryRead(read?: MergeRequestSectionRead) {
  if (read?.state === 'failed') return !losesAccess(read.issue) && read.issue.code !== 'notFound'
  return read?.state === 'ready' && read.signal.status === 'readFailed'
}

function statusId(section: MergeRequestDetailSection) {
  return `${sectionId}-${section}-status`
}

function focusHeading(event?: Event) {
  const target = heading.value?.$el
  if (!(target instanceof HTMLElement)) return
  event?.preventDefault()
  target.focus({ preventScroll: true })
}

function retry(section: MergeRequestDetailSection) {
  if (section === 'base') focusHeading()
  else document.getElementById(statusId(section))?.focus({ preventScroll: true })
  emit('retry', section)
}

watch(result, (next, previous) => {
  if (next || !previous || !props.detail) return
  if (body.value?.contains(document.activeElement)) focusHeading()
  body.value?.scrollTo({ top: 0 })
})
</script>

<template>
  <Sheet :open="!!detail" @update:open="open => { if (!open) emit('close') }">
    <SheetContent
      class="merge-request-sheet [&>button]:top-2 [&>button]:right-2 [&>button]:grid [&>button]:size-11 [&>button]:place-items-center [&>button]:rounded-lg sm:[&>button]:top-4 sm:[&>button]:right-3.5 sm:[&>button]:size-8"
      :style="{ width: 'min(525px, 100%)', maxWidth: 'none', height: '100dvh', gap: '0', backgroundColor: 'var(--brand-color-bg-elevated)' }"
      @open-auto-focus="focusHeading"
      @close-auto-focus="emit('restoreFocus', $event)"
    >
      <template v-if="detail">
        <SheetHeader class="detail-header">
          <div v-if="selected" class="detail-repository">
            <span class="detail-platform">
              <component :is="detail.repository.platform === 'github' ? GitBranch : GitFork" aria-hidden="true" />
              {{ platformLabels[detail.repository.platform] }}
            </span>
            <span>{{ detail.repository.slug }} · {{ repositoryHost(detail.repository) }}</span>
            <code>{{ requestNumber(detail.repository.platform, detail.number) }}</code>
          </div>
          <SheetTitle ref="heading" class="detail-title" tabindex="-1">
            {{ selected ? selected.title || '标题不可用' : '合并请求详情' }}
          </SheetTitle>
          <SheetDescription class="sr-only">合并请求的描述、分支、评审与 CI</SheetDescription>
          <div v-if="selected" class="detail-summary">
            <MergeRequestState :status="selected.status" :draft="selected.draft" />
            <span class="detail-byline">
              <span>{{ selected.author || '作者不可用' }}</span>
              <span aria-hidden="true">·</span>
              <span>更新于 <time v-if="selected.updatedAt" :datetime="selected.updatedAt">{{ dateLabel(selected.updatedAt) }}</time><template v-else>不可用</template></span>
            </span>
          </div>
        </SheetHeader>

        <span class="sr-only" role="status">{{ pending ? '正在读取描述与分支' : '' }}</span>
        <div ref="body" class="detail-body" role="region" aria-label="合并请求详情正文" :aria-busy="pending" tabindex="0">
          <Alert v-if="detail.issue" class="notice warning">
            <AlertTitle>{{ issueLabel(detail.issue) }}</AlertTitle>
            <AlertDescription>{{ detail.issue.message }}</AlertDescription>
            <Button v-if="needsConnection(detail.issue)" as-child variant="outline" class="justify-self-start">
              <RouterLink to="/settings">检查连接</RouterLink>
            </Button>
            <Button v-else-if="canRetry && !losesAccess(detail.issue)" type="button" variant="outline" class="justify-self-start" @click="retry('base')">
              重试详情
            </Button>
          </Alert>
          <section v-else-if="pending" class="detail-section" :aria-labelledby="`${sectionId}-description`">
            <h3 :id="`${sectionId}-description`">描述</h3>
            <LoadingSkeleton variant="text" :rows="4" />
          </section>
          <template v-else-if="result">
            <section class="detail-section" :aria-labelledby="`${sectionId}-description`">
              <h3 :id="`${sectionId}-description`">描述</h3>
              <p v-if="!result.descriptionAvailable" class="muted">描述不可用。平台未提供可读取的描述。</p>
              <MergeRequestMarkdown v-else-if="result.description?.trim()" :source="result.description" />
              <p v-else class="muted">无描述</p>
            </section>

            <section class="detail-section" :aria-labelledby="`${sectionId}-branches`">
              <h3 :id="`${sectionId}-branches`">分支</h3>
              <dl class="branch-route">
                <div>
                  <dt>源分支</dt>
                  <dd><span v-if="result.sourceRepository">{{ result.sourceRepository }}</span><code>{{ result.sourceBranch || '不可用' }}</code></dd>
                </div>
                <div>
                  <dt>目标分支</dt>
                  <dd>
                    <ArrowRight aria-hidden="true" class="branch-arrow" />
                    <span v-if="result.targetRepository">{{ result.targetRepository }}</span>
                    <code>{{ result.targetBranch || '不可用' }}</code>
                  </dd>
                </div>
              </dl>
            </section>

            <section class="detail-section" :aria-labelledby="`${sectionId}-facts`">
              <h3 :id="`${sectionId}-facts`">概要</h3>
              <dl class="request-facts">
                <div v-for="{ label, at } in times" :key="label">
                  <dt><CalendarDays aria-hidden="true" />{{ label }}</dt>
                  <dd><time v-if="at" :datetime="at">{{ dateLabel(at) }}</time><template v-else>不可用</template></dd>
                </div>
              </dl>
            </section>

            <section class="detail-section" :aria-labelledby="`${sectionId}-checks`">
              <h3 :id="`${sectionId}-checks`">审阅与检查</h3>
              <dl class="check-strip">
                <div v-for="{ section, label, icon, read } in checks" :key="section" class="check-reading">
                  <dt><component :is="icon" aria-hidden="true" />{{ label }}</dt>
                  <dd
                    :id="statusId(section)" class="check-status" tabindex="-1" aria-live="polite"
                    :data-status="read?.state === 'ready' ? read.signal.status : undefined"
                  >{{ readLabel(read) }}</dd>
                  <dd v-if="read?.state === 'failed'" class="signal-context">
                    <p>{{ read.issue.message }}</p>
                  </dd>
                  <dd v-else-if="read?.state === 'ready'" class="signal-context">
                    <span v-if="read.signal.commit">适用提交 <code>{{ read.signal.commit }}</code></span>
                    <time v-if="read.signal.at" :datetime="read.signal.at">{{ dateLabel(read.signal.at) }}</time>
                    <p v-if="read.signal.message">{{ read.signal.message }}</p>
                    <p v-else-if="read.signal.status === 'unavailable' && requestUrl">可在原站核对。</p>
                  </dd>
                  <dd v-if="canRetryRead(read)" class="signal-context">
                    <Button type="button" variant="outline" class="check-action" @click="retry(section)">重试{{ label }}</Button>
                  </dd>
                  <dd v-if="section === 'ci' && result.historyAvailable" class="signal-context signal-history">
                    <div :id="statusId('history')" class="history-status" aria-live="polite" tabindex="-1">
                      <template v-if="history && (history.state !== 'ready' || history.signal.status !== 'neutral')">历史 CI · {{ readLabel(history) }}</template>
                      <strong v-else-if="history">历史 CI</strong>
                    </div>
                    <p v-if="history?.state === 'failed'">{{ history.issue.message }}</p>
                    <p v-else-if="history?.state === 'ready' && history.signal.message">{{ history.signal.message }}</p>
                    <Button
                      v-if="!history || canRetryRead(history)"
                      type="button" variant="outline" class="check-action"
                      :aria-controls="statusId('history')" @click="retry('history')"
                    >{{ history ? '重试历史 CI' : '读取历史 CI' }}</Button>
                    <ul v-if="history?.state === 'ready' && history.historicalCi?.length" class="history-records">
                      <li v-for="(signal, index) in history.historicalCi" :key="index">
                        <strong :data-status="signal.status">{{ signalLabel(signal.status) }}</strong>
                        <span v-if="signal.commit">旧提交 <code>{{ signal.commit }}</code></span>
                        <time v-if="signal.at" :datetime="signal.at">{{ dateLabel(signal.at) }}</time>
                        <p v-if="signal.message">{{ signal.message }}</p>
                      </li>
                    </ul>
                  </dd>
                </div>
              </dl>
            </section>
          </template>
        </div>

        <footer v-if="result" class="detail-footer">
          <Button v-if="requestUrl" as-child>
            <a :href="requestUrl" target="_blank" rel="noopener noreferrer"><ExternalLink aria-hidden="true" />在原站打开</a>
          </Button>
          <span v-else class="muted">原站链接不可用</span>
        </footer>
      </template>
    </SheetContent>
  </Sheet>
</template>

<style scoped>
.detail-header {
  flex: none; text-align: start; gap: 9px; padding: 22px 60px 18px 23px;
  border-block-end: 1px solid var(--yss-colors-border);
}
.detail-title { font-size: 21px; line-height: 1.42; overflow-wrap: anywhere; }
.detail-repository, .detail-summary, .detail-platform, .detail-byline { display: flex; flex-wrap: wrap; align-items: center; gap: 7px; }
.detail-repository, .detail-byline { font-size: 12px; color: var(--brand-color-text-secondary); overflow-wrap: anywhere; }
.detail-repository > :not(:first-child) { border-inline-start: 1px solid var(--yss-colors-border); padding-inline-start: 7px; }
.detail-platform svg { inline-size: 14px; block-size: 14px; flex: none; }
.detail-body { container: merge-request-detail / inline-size; flex: 1; min-height: 0; overflow: auto; padding: 20px 23px; overscroll-behavior: contain; }
.detail-body:focus-visible { outline-offset: -3px; }
.detail-section { min-width: 0; overflow-wrap: anywhere; }
.detail-section + .detail-section { margin-block-start: 20px; padding-block-start: 17px; border-block-start: 1px solid var(--yss-colors-border); }
.detail-section h3 { margin-block-end: 9px; font-size: 14px; }
.branch-route { display: grid; grid-template-columns: minmax(0, 1.7fr) minmax(0, 1fr); gap: 38px; }
.branch-route > div { display: grid; gap: 5px; align-content: start; }
.branch-route dd { position: relative; }
.branch-route dt, .request-facts dt, .branch-route dd span { color: var(--brand-color-text-secondary); font-size: 12px; }
.branch-route dd span { display: block; margin-block-end: 4px; }
.branch-route code { display: inline-block; max-inline-size: 100%; padding: 3px 7px; color: var(--yss-colors-text-title); background: var(--yss-colors-primary-bg); border-radius: 5px; font-size: 12px; }
.branch-arrow { position: absolute; inset-inline-start: -26px; inset-block-start: 5px; width: 15px; height: 15px; color: var(--brand-color-text-secondary); }
.request-facts { display: grid; gap: 8px; }
.request-facts > div { display: grid; grid-template-columns: 90px minmax(0, 1fr); gap: 8px; align-items: baseline; }
.request-facts dt, .check-reading dt { display: flex; align-items: center; gap: 6px; }
.request-facts dt svg, .check-reading dt svg { width: 14px; height: 14px; flex: none; }
.request-facts time, .detail-byline time { font-variant-numeric: tabular-nums; }
.check-strip { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); padding-block: 10px; border: 1px solid var(--yss-colors-border); border-radius: 8px; background: var(--workbench-glass-card); }
.check-reading { display: flex; flex-wrap: wrap; align-content: flex-start; align-items: baseline; justify-content: space-between; gap: 4px 10px; padding-inline: 11px; }
.check-reading + .check-reading { border-inline-start: 1px solid var(--yss-colors-border); }
.check-reading dt, .signal-context { color: var(--brand-color-text-secondary); font-size: 12px; }
.check-status { font-weight: 650; }
.check-reading [data-status='approved'], .check-reading [data-status='passing'] { color: #176141; }
.check-reading [data-status='failing'], .check-reading [data-status='changesRequested'] { color: var(--yss-colors-error-text); }
.check-reading [data-status='unavailable'], .check-reading [data-status='readFailed'] { color: #775117; }
.signal-context { display: grid; gap: 3px; flex: 0 0 100%; }
.signal-history { margin-block-start: 4px; padding-block-start: 7px; border-block-start: 1px solid var(--yss-colors-border); }
.history-status:empty { min-block-size: 0; }
.history-records, .history-records li { display: grid; gap: 3px; }
.history-records { gap: 7px; list-style: none; }
.history-records strong, .history-status strong { color: var(--brand-color-text); font-weight: 650; }
.check-action[data-slot='button'] { justify-self: start; margin-block-start: 2px; font-size: 12px; }
.detail-footer { display: flex; flex: none; justify-content: flex-end; padding: 12px 23px max(12px, env(safe-area-inset-bottom)); border-block-start: 1px solid var(--yss-colors-border); }
@container merge-request-detail (width < 400px) {
  .branch-route { grid-template-columns: minmax(0, 1fr); gap: 7px; }
  .branch-route > div { grid-template-columns: 4em minmax(0, 1fr); }
  .branch-route dt { padding-block-start: 3px; }
  .branch-arrow { display: none; }
  .check-strip { grid-template-columns: minmax(0, 1fr); }
  .check-reading + .check-reading { margin-block-start: 10px; padding-block-start: 10px; border-inline-start: 0; border-block-start: 1px solid var(--yss-colors-border); }
}
@media (width < 640px) {
  .detail-header { padding: 18px 53px 15px 16px; }
  .detail-body { padding: 17px 16px; }
  .detail-footer { padding-inline: 16px; }
}
</style>
