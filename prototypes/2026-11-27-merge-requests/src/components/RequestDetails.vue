<script setup lang="ts">
import { computed, nextTick, onScopeDispose, reactive, useTemplateRef, watch } from 'vue'
import { ArrowRight, CalendarDays, ExternalLink, ListChecks, LoaderCircle, ShieldCheck, UserRound } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { performanceDetail, repositoryFor, requestNumber, type MergeRequest } from '@/demo'
import PlatformMark from './PlatformMark.vue'
import RequestMarkdown from './RequestMarkdown.vue'
import RequestState from './RequestState.vue'

const props = defineProps<{
  open: boolean
  request: MergeRequest | null
  embedded: boolean
  progressive?: boolean
  showRepositoryHost?: boolean
}>()
const emit = defineEmits<{ close: []; restoreFocus: [event: Event] }>()
const repository = computed(() => props.request ? repositoryFor(props.request.repo) : null)
const isMarkdownSample = computed(() => props.progressive && props.request?.id === performanceDetail.id)
const historyEntry = computed(() => isMarkdownSample.value ? performanceDetail.history : props.request?.historicalCi)
type DetailArea = 'base' | 'approval' | 'ci' | 'history'
type ReadState = 'idle' | 'loading' | 'failed' | 'ready'
const readings = reactive<Record<DetailArea, ReadState>>({ base: 'ready', approval: 'ready', ci: 'ready', history: 'idle' })
const timers = new Map<DetailArea, number>()
const retries = reactive(new Set<DetailArea>())

function cancelReads() {
  timers.forEach(timer => window.clearTimeout(timer))
  timers.clear()
  retries.clear()
}

function simulateRead(area: DetailArea, delay: number, state: ReadState = 'ready', trigger?: HTMLElement) {
  readings[area] = 'loading'
  const requestId = props.request?.id
  const timer = window.setTimeout(async () => {
    timers.delete(area)
    if (!props.open || props.request?.id !== requestId) return
    const restoreFocus = document.activeElement === trigger
    readings[area] = state
    retries.delete(area)
    if (restoreFocus) {
      await nextTick()
      if (!trigger?.isConnected && document.activeElement === document.body) {
        document.getElementById(`detail-${area}-state`)?.focus({ preventScroll: true })
      }
    }
  }, delay)
  timers.set(area, timer)
}

watch(() => [props.open, props.request?.id, props.progressive], () => {
  cancelReads()
  readings.base = 'ready'
  readings.approval = 'ready'
  readings.ci = 'ready'
  readings.history = 'idle'
  if (!props.open || !props.request || !props.progressive) return
  simulateRead('base', 450)
  simulateRead('approval', 1400, isMarkdownSample.value ? 'failed' : 'ready')
  simulateRead('ci', 2000)
}, { immediate: true })
onScopeDispose(cancelReads)

function retryCheck(area: 'approval' | 'ci', event: MouseEvent) {
  if (readings[area] !== 'failed') return
  retries.add(area)
  simulateRead(area, 750, 'ready', event.currentTarget as HTMLElement)
}

function loadHistory(event: MouseEvent) {
  if (readings.history !== 'idle') return
  simulateRead('history', 850, 'ready', event.currentTarget as HTMLElement)
}

const checks = computed(() => {
  const request = props.request
  if (!request) return []
  return [
    { id: 'approval', label: '评审与审批', icon: ShieldCheck, summary: request.approval },
    { id: 'ci', label: props.progressive ? '当前 CI' : 'CI', icon: ListChecks,
      summary: isMarkdownSample.value ? { ...request.ci, evidence: performanceDetail.current } : request.ci },
  ] as const
})

const heading = useTemplateRef<InstanceType<typeof SheetTitle>>('heading')

function focusHeading(event: Event) {
  const target = heading.value?.$el
  if (!(target instanceof HTMLElement)) return
  event.preventDefault()
  target.focus({ preventScroll: true })
}
</script>

<template>
  <Sheet :open="open" @update:open="open => { if (!open) emit('close') }">
    <SheetContent
      class="request-drawer"
      @open-auto-focus="focusHeading"
      @close-auto-focus="event => emit('restoreFocus', event)"
    >
      <template v-if="request && repository">
        <SheetHeader class="drawer-header">
          <div class="drawer-record">
            <PlatformMark :platform="repository.platform" />
            <span>
              {{ repository.slug }}
              <span v-if="showRepositoryHost"> · {{ repository.host }}</span>
            </span>
            <span class="request-number">{{ requestNumber(request) }}</span>
          </div>
          <SheetTitle ref="heading" tabindex="-1">{{ request.title }}</SheetTitle>
          <SheetDescription class="sr-only">合并请求详情</SheetDescription>
          <div class="drawer-summary">
            <RequestState :status="request.status" :draft="request.draft" />
            <span v-if="progressive" class="drawer-byline">{{ request.author }}<span aria-hidden="true">·</span>更新于 <time :datetime="request.updated.replace(' ', 'T')">{{ request.updated }}</time></span>
          </div>
        </SheetHeader>

        <div class="drawer-body" role="region" aria-label="合并请求详情正文" tabindex="0">
          <section class="drawer-section" aria-labelledby="description-title">
            <h3 id="description-title">描述</h3>
            <p v-if="readings.base === 'loading'" class="detail-loading" role="status"><LoaderCircle class="spinning" aria-hidden="true" />正在读取描述与分支…</p>
            <RequestMarkdown v-else-if="isMarkdownSample" :source="performanceDetail.description" />
            <p v-else-if="request.descriptionUnavailable" class="muted">不可用。{{ request.descriptionUnavailable }}</p>
            <p v-else-if="request.description" class="request-description">{{ request.description }}</p>
            <p v-else class="muted">无描述</p>
          </section>
          <section v-if="readings.base === 'ready'" class="drawer-section" aria-labelledby="branches-title">
            <h3 id="branches-title">分支</h3>
            <dl class="branch-route">
              <div class="branch-endpoint">
                <dt>源分支</dt>
                <dd>
                  <span v-if="request.fromRepository" class="branch-repository">{{ request.fromRepository }}</span>
                  <code>{{ request.from }}</code>
                </dd>
              </div>
              <div class="branch-endpoint">
                <dt>目标分支</dt>
                <dd>
                  <ArrowRight class="branch-direction" aria-hidden="true" />
                  <span v-if="request.fromRepository" class="branch-repository">{{ repository.slug }}</span>
                  <code>{{ request.to }}</code>
                </dd>
              </div>
            </dl>
          </section>
          <section v-if="readings.base === 'ready'" class="drawer-section" aria-labelledby="attributes-title">
            <h3 id="attributes-title">概要</h3>
            <dl class="request-attributes">
              <div v-if="!progressive"><dt><UserRound aria-hidden="true" />作者</dt><dd>{{ request.author }}</dd></div>
              <div><dt><CalendarDays aria-hidden="true" />创建时间</dt><dd><time :datetime="request.created.replace(' ', 'T')">{{ request.created }}</time></dd></div>
              <div v-if="!progressive"><dt><CalendarDays aria-hidden="true" />更新时间</dt><dd><time :datetime="request.updated.replace(' ', 'T')">{{ request.updated }}</time></dd></div>
              <div v-if="request.ended"><dt><CalendarDays aria-hidden="true" />{{ request.status === 'merged' ? '合并时间' : '关闭时间' }}</dt><dd><time :datetime="request.ended.replace(' ', 'T')">{{ request.ended }}</time></dd></div>
            </dl>
          </section>
          <section class="drawer-section" aria-labelledby="check-title">
            <h3 id="check-title">审阅与检查</h3>
            <dl class="check-strip">
              <div v-for="{ id, label, icon, summary } in checks" :key="id" class="check-reading">
                <dt><component :is="icon" aria-hidden="true" />{{ label }}</dt>
                <dd
                  :id="`detail-${id}-state`" class="check-status" :data-result="readings[id] === 'ready' ? summary.status : undefined"
                  :aria-live="progressive ? 'polite' : undefined" :tabindex="progressive ? -1 : undefined"
                >
                  <span v-if="readings[id] === 'loading'" class="detail-loading"><LoaderCircle class="spinning" aria-hidden="true" />读取中</span>
                  <span v-else-if="readings[id] === 'failed'" class="check-error">读取失败</span>
                  <template v-else>{{ summary.status }}</template>
                </dd>
                <dd v-if="readings[id] === 'failed' || retries.has(id)" class="check-context">
                  <p v-if="readings[id] === 'failed'">暂时无法读取{{ label }}。</p>
                  <Button variant="outline" class="check-action" :aria-disabled="retries.has(id) || undefined" @click="retryCheck(id, $event)">
                    {{ retries.has(id) ? '重试中' : `重试${label}` }}
                  </Button>
                </dd>
                <dd v-if="readings[id] === 'ready' && (summary.evidence || summary.detail)" class="check-context">
                  <template v-if="summary.evidence">
                    <span>适用提交 <code>{{ summary.evidence.commit }}</code></span>
                    <time :datetime="summary.evidence.recordedAt.replace(' ', 'T')">{{ summary.evidence.recordedAt }}</time>
                  </template>
                  <p v-if="summary.detail">{{ summary.detail }}</p>
                </dd>
                <dd v-if="id === 'ci' && progressive" class="check-context check-history">
                  <Button v-if="readings.history !== 'ready'" variant="outline" class="check-action" :aria-disabled="readings.history === 'loading' || undefined" @click="loadHistory">
                    {{ readings.history === 'loading' ? '历史 CI 读取中' : '读取历史 CI' }}
                  </Button>
                  <div id="detail-history-state" class="history-summary" aria-live="polite" tabindex="-1">
                    <template v-if="readings.history === 'ready'">
                      <strong>历史 CI · {{ historyEntry?.status ?? '暂无记录' }}</strong>
                      <template v-if="historyEntry">
                        <span>旧提交 <code>{{ historyEntry.commit }}</code></span>
                        <time :datetime="historyEntry.recordedAt.replace(' ', 'T')">{{ historyEntry.recordedAt }}</time>
                      </template>
                    </template>
                  </div>
                </dd>
                <dd v-else-if="id === 'ci' && historyEntry" class="check-context check-history">
                  <strong>历史 CI · {{ historyEntry.status }}</strong>
                  <span>旧提交 <code>{{ historyEntry.commit }}</code></span>
                  <time :datetime="historyEntry.recordedAt.replace(' ', 'T')">{{ historyEntry.recordedAt }}</time>
                </dd>
              </div>
            </dl>
          </section>
        </div>
        <footer class="drawer-footer">
          <span class="muted">{{ embedded ? '打开模拟原站，可返回详情' : '在新标签页打开模拟原站' }}</span>
          <Button as-child class="action-primary">
            <a :href="`#source/${request.id}`" :target="embedded ? undefined : '_blank'" rel="noopener">
              <ExternalLink aria-hidden="true" />在原站打开
            </a>
          </Button>
        </footer>
      </template>
    </SheetContent>
  </Sheet>
</template>

<style scoped>
.drawer-body {
  container: request-details / inline-size;
}

.drawer-body:focus-visible {
  outline-offset: -2px;
}

.drawer-summary,
.drawer-byline,
.detail-loading {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 7px;
}

.drawer-byline,
.detail-loading {
  color: var(--muted);
  font-size: 12px;
  font-weight: 400;
}

.detail-loading svg {
  width: 14px;
  height: 14px;
  flex: none;
}

.branch-route {
  display: grid;
  grid-template-columns: minmax(0, 1.7fr) minmax(0, 1fr);
  gap: 38px;
}

.branch-endpoint {
  display: grid;
  align-content: start;
  gap: 5px;
  min-width: 0;
}

.branch-endpoint dt,
.check-reading dt {
  color: var(--muted);
  font-size: 12px;
}

.branch-endpoint dd {
  position: relative;
  min-width: 0;
  margin: 0;
}

.branch-repository {
  display: block;
  margin-bottom: 4px;
  color: var(--muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}

.branch-endpoint code {
  display: inline-block;
  max-width: 100%;
  padding: 3px 7px;
  border-radius: 5px;
  background: var(--yss-colors-primary-bg);
  color: #32476e;
  font-family: var(--mono);
  font-size: 12px;
  overflow-wrap: anywhere;
}

.branch-direction {
  position: absolute;
  inset-inline-start: -26px;
  inset-block-start: 5px;
  color: var(--muted);
}

.branch-direction,
.check-reading svg {
  width: 15px;
  height: 15px;
  flex: none;
  stroke-width: 1.7;
}

.check-strip {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  padding-block: 10px;
  border: 1px solid var(--divider);
  border-radius: 8px;
  background: var(--workbench-glass-card);
}

.check-reading {
  display: flex;
  flex-wrap: wrap;
  align-content: flex-start;
  align-items: baseline;
  justify-content: space-between;
  gap: 4px 10px;
  padding-inline: 11px;
}

.check-reading + .check-reading {
  border-inline-start: 1px solid var(--divider);
}

.check-reading dt {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.check-reading dd {
  margin: 0;
}

.check-status {
  color: var(--brand-color-text);
  font-weight: 650;
}

.check-status[data-result='已批准'],
.check-status[data-result='已通过'] {
  color: var(--open);
}

.check-status[data-result='等待审阅'],
.check-status[data-result='运行中'],
.check-status[data-result='不可用'] {
  color: #775117;
}

.check-status[data-result='未通过'] {
  color: var(--closed);
}

.check-context,
.history-summary {
  display: grid;
  gap: 3px;
  font-size: 12px;
  color: var(--muted);
  overflow-wrap: anywhere;
}

.check-context {
  flex: 0 0 100%;
}

.check-context code {
  font-family: var(--mono);
}

.check-reading .check-history {
  margin-top: 4px;
  padding-top: 7px;
  border-top: 1px solid var(--divider);
}

.check-error { color: #775117; }
.check-action { justify-self: start; margin-block-start: 3px; font-size: 12px; min-height: 32px; }

.check-history strong {
  color: var(--brand-color-text);
  font-weight: 650;
}

.drawer-footer .action-primary {
  flex: none;
}

@container request-details (width < 400px) {
  .check-action { min-height: 44px; }
  .branch-route {
    grid-template-columns: minmax(0, 1fr);
    gap: 7px;
  }

  .branch-endpoint {
    grid-template-columns: 4em minmax(0, 1fr);
  }

  .branch-endpoint dt {
    padding-block-start: 4px;
  }

  .branch-direction {
    display: none;
  }

  .check-strip {
    grid-template-columns: minmax(0, 1fr);
  }

  .check-reading + .check-reading {
    margin-block-start: 10px;
    padding-block-start: 10px;
    border-inline-start: 0;
    border-block-start: 1px solid var(--divider);
  }
}

@media (pointer: coarse) {
  :global(.request-drawer [data-slot='sheet-close']),
  .drawer-footer .action-primary {
    min-inline-size: 44px;
    min-block-size: 44px;
  }
}

@media (forced-colors: active) {
  .check-strip,
  .check-reading + .check-reading,
  .check-history {
    border-color: CanvasText;
  }

  .branch-endpoint code {
    outline: 1px solid CanvasText;
    outline-offset: -1px;
  }
}
</style>
