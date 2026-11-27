<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { ArrowRight, ExternalLink, ListChecks, ShieldCheck } from '@lucide/vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import type {
  MergeRequestDetail, MergeRequestIssue, MergeRequestRepository
} from '@/services/workbench-merge-requests'
import MergeRequestState from './MergeRequestState.vue'
import {
  dateLabel, issueLabel, losesAccess, needsConnection, platformLabels, repositoryHost, requestNumber, safeHttpUrl
} from '../schemas/requests'

const props = defineProps<{
  canRetry: boolean
  detail?: {
    repository: MergeRequestRepository
    number: number
    result?: MergeRequestDetail
    issue?: MergeRequestIssue
  }
}>()
const emit = defineEmits<{ close: []; restoreFocus: [event: Event]; retry: [] }>()
const heading = useTemplateRef<InstanceType<typeof SheetTitle>>('heading')
const result = computed(() => props.detail?.result)
const pending = computed(() => !!props.detail && !result.value && !props.detail.issue)
const requestUrl = computed(() => safeHttpUrl(result.value?.url, props.detail?.repository.url))
const groups = computed(() => result.value && [
  { label: '评审与审批', icon: ShieldCheck, signal: result.value.review },
  { label: 'CI', icon: ListChecks, signal: result.value.ci }
])
const times = computed(() => {
  const request = result.value
  if (!request) return []
  return [
    { label: '创建时间', at: request.createdAt },
    { label: '更新时间', at: request.updatedAt },
    ...(request.mergedAt ? [{ label: '合并时间', at: request.mergedAt }] : []),
    ...(request.closedAt ? [{ label: '关闭时间', at: request.closedAt }] : [])
  ]
})
const signalLabels: Record<string, string> = {
  approved: '已批准', changesRequested: '有变更请求', reviewRequired: '等待评审',
  passing: '通过', failing: '失败', pending: '等待结果', noRecords: '暂无记录',
  unavailable: '不可用', skipped: '已跳过', cancelled: '已取消', canceled: '已取消', neutral: '中性结果'
}

function focusHeading(event?: Event) {
  const target = heading.value?.$el
  if (!(target instanceof HTMLElement)) return
  event?.preventDefault()
  target.focus({ preventScroll: true })
}

function retryDetail() {
  focusHeading()
  emit('retry')
}
</script>

<template>
  <Sheet :open="!!detail" @update:open="open => { if (!open) emit('close') }">
    <SheetContent
      class="[&>button]:top-2 [&>button]:right-2 [&>button]:grid [&>button]:size-11 [&>button]:place-items-center sm:[&>button]:size-8"
      :style="{ width: 'min(525px, 100%)', maxWidth: 'none', height: '100dvh', gap: '0', backgroundColor: 'var(--brand-color-bg-elevated)' }"
      @open-auto-focus="focusHeading"
      @close-auto-focus="emit('restoreFocus', $event)"
    >
      <template v-if="detail">
        <SheetHeader class="detail-header">
          <div v-if="result" class="detail-repository">
            <span>{{ platformLabels[detail.repository.platform] }}</span>
            <span>{{ repositoryHost(detail.repository) }} · {{ detail.repository.slug }}</span>
            <span>{{ requestNumber(detail.repository.platform, detail.number) }}</span>
          </div>
          <SheetTitle ref="heading" class="detail-title" tabindex="-1">
            {{ result ? result.title || '标题不可用' : '合并请求详情' }}
          </SheetTitle>
          <SheetDescription class="sr-only">合并请求的变更说明、分支、评审与 CI</SheetDescription>
          <MergeRequestState v-if="result" :status="result.status" :draft="result.draft" />
        </SheetHeader>

        <span class="sr-only" role="status">{{ pending ? '正在读取合并请求详情' : '' }}</span>
        <div class="detail-body" role="region" aria-label="合并请求详情正文" :aria-busy="pending" tabindex="0">
          <LoadingSkeleton v-if="pending" variant="detail" :rows="4" />
          <Alert v-if="detail.issue" class="notice warning">
            <AlertTitle>{{ issueLabel(detail.issue) }}</AlertTitle>
            <AlertDescription>{{ detail.issue.message }}</AlertDescription>
            <Button v-if="needsConnection(detail.issue)" as-child variant="outline" class="justify-self-start">
              <RouterLink to="/settings">检查连接</RouterLink>
            </Button>
            <Button v-else-if="canRetry && !losesAccess(detail.issue)" type="button" variant="outline" class="justify-self-start" @click="retryDetail">
              重试详情
            </Button>
          </Alert>
          <template v-else-if="result">
            <section class="detail-section" aria-label="描述">
              <h3>描述</h3>
              <p v-if="!result.descriptionAvailable" class="muted">不可用。平台未提供可读取的描述。</p>
              <p v-else-if="result.description?.trim()" class="request-description">{{ result.description }}</p>
              <p v-else class="muted">无描述</p>
            </section>

            <section class="detail-section" aria-label="分支">
              <h3>分支</h3>
              <dl class="branch-route">
                <div>
                  <dt>源分支</dt>
                  <dd><span v-if="result.sourceRepository">{{ result.sourceRepository }}</span><code>{{ result.sourceBranch || '不可用' }}</code></dd>
                </div>
                <ArrowRight aria-hidden="true" class="branch-arrow" />
                <div>
                  <dt>目标分支</dt>
                  <dd><span v-if="result.targetRepository">{{ result.targetRepository }}</span><code>{{ result.targetBranch || '不可用' }}</code></dd>
                </div>
              </dl>
            </section>

            <section class="detail-section" aria-label="概要">
              <h3>概要</h3>
              <dl class="request-facts">
                <div><dt>作者</dt><dd>{{ result.author || '不可用' }}</dd></div>
                <div v-for="{ label, at } in times" :key="label">
                  <dt>{{ label }}</dt>
                  <dd><time v-if="at" :datetime="at">{{ dateLabel(at) }}</time><template v-else>不可用</template></dd>
                </div>
              </dl>
            </section>

            <section class="detail-section" aria-label="审阅与检查">
              <h3>审阅与检查</h3>
              <div class="check-grid">
                <section v-for="{ label, icon, signal } in groups" :key="label" class="check-summary" :aria-label="label">
                  <h4><component :is="icon" aria-hidden="true" />{{ label }}</h4>
                  <strong :data-status="signal.status">{{ signalLabels[signal.status] || '不可用' }}</strong>
                  <p v-if="signal.commit" class="signal-context">适用提交 <code>{{ signal.commit }}</code></p>
                  <time v-if="signal.at" :datetime="signal.at" class="signal-context">{{ dateLabel(signal.at) }}</time>
                  <p v-if="signal.message" class="signal-context">{{ signal.message }}</p>
                  <ul v-if="label === 'CI' && result.historicalCi.length" class="signal-history">
                    <li v-for="(signal, index) in result.historicalCi" :key="index">
                      <strong>历史 CI · {{ signalLabels[signal.status] || '不可用' }}</strong>
                      <span v-if="signal.commit" class="signal-context">旧提交 <code>{{ signal.commit }}</code></span>
                      <time v-if="signal.at" :datetime="signal.at" class="signal-context">{{ dateLabel(signal.at) }}</time>
                      <p v-if="signal.message" class="signal-context">{{ signal.message }}</p>
                    </li>
                  </ul>
                </section>
              </div>
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
  flex: none;
  gap: 8px;
  padding: 21px 50px 16px 20px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.detail-title { font-size: 20px; line-height: 1.36; overflow-wrap: anywhere; }
.detail-repository { display: flex; flex-wrap: wrap; gap: 4px 9px; font-size: 12px; color: var(--brand-color-text-secondary); overflow-wrap: anywhere; }
.detail-body { container: merge-request-detail / inline-size; flex: 1; min-height: 0; overflow: auto; padding: 17px 20px 20px; overscroll-behavior: contain; }
.detail-body:focus-visible { outline-offset: -3px; }
.detail-section { padding-block-end: 15px; overflow-wrap: anywhere; }
.detail-section + .detail-section { padding-block-start: 14px; border-top: 1px solid var(--yss-colors-border); }
.detail-section h3 { margin-block-end: 7px; font-size: 14px; }
.request-description { white-space: pre-wrap; }
.branch-route { display: grid; grid-template-columns: minmax(0, 1fr) 18px minmax(0, 1fr); gap: 8px; align-items: start; }
.branch-route dt, .request-facts dt { color: var(--brand-color-text-secondary); }
.branch-route dd span { display: block; margin-block: 3px; font-size: 12px; }
.branch-route code, .signal-context code { color: var(--yss-colors-text-title); background: var(--yss-colors-primary-bg); border-radius: 4px; padding: 1px 4px; }
.branch-arrow { align-self: center; width: 16px; color: var(--brand-color-text-secondary); }
.request-facts { display: grid; gap: 4px; }
.request-facts > div { display: grid; grid-template-columns: 76px minmax(0, 1fr); gap: 8px; }
.check-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); border: 1px solid var(--yss-colors-border); border-radius: 8px; }
.check-summary { padding: 10px; font-size: 12px; }
.check-summary + .check-summary { border-inline-start: 1px solid var(--yss-colors-border); }
.check-summary h4 { display: flex; align-items: center; margin-block-end: 5px; gap: 5px; font-size: 12px; }
.check-summary h4 svg { width: 15px; height: 15px; }
.check-summary strong[data-status='approved'], .check-summary strong[data-status='passing'] { color: #176141; }
.check-summary strong[data-status='failing'], .check-summary strong[data-status='changesRequested'] { color: var(--yss-colors-error-text); }
.signal-context { display: block; margin-block-start: 4px; color: var(--brand-color-text-secondary); }
.signal-history { margin-block-start: 8px; padding: 7px 0 0; border-top: 1px solid var(--yss-colors-border); list-style: none; }
.signal-history li + li { margin-block-start: 6px; }
.detail-footer { display: flex; flex: none; justify-content: flex-end; padding: 12px 20px max(12px, env(safe-area-inset-bottom)); border-top: 1px solid var(--yss-colors-border); }
@container merge-request-detail (width < 400px) {
  .branch-route { grid-template-columns: minmax(0, 1fr); }
  .branch-route > div { display: grid; grid-template-columns: 4em minmax(0, 1fr); gap: 6px; }
  .branch-arrow { display: none; }
  .check-grid { grid-template-columns: minmax(0, 1fr); }
  .check-summary { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: baseline; }
  .check-summary h4 { margin: 0; }
  .check-summary > .signal-context, .signal-history { grid-column: 1 / -1; }
  .check-summary + .check-summary { border-inline-start: 0; border-block-start: 1px solid var(--yss-colors-border); }
}
@media (width < 460px) {
  .detail-header, .detail-body, .detail-footer { padding-inline-start: 15px; }
  .detail-body, .detail-footer { padding-inline-end: 15px; }
}
</style>
