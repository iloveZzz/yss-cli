<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { ArrowRight, CalendarDays, ExternalLink, ListChecks, ShieldCheck, UserRound } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { repositoryFor, requestNumber, type MergeRequest } from '@/demo'
import PlatformMark from './PlatformMark.vue'
import RequestState from './RequestState.vue'

const props = defineProps<{
  open: boolean
  request: MergeRequest | null
  embedded: boolean
  showRepositoryHost?: boolean
}>()
const emit = defineEmits<{ close: []; restoreFocus: [event: Event] }>()
const repository = computed(() => props.request ? repositoryFor(props.request.repo) : null)
const checks = computed(() => {
  const request = props.request
  if (!request) return []
  return [
    { label: '评审与审批', icon: ShieldCheck, summary: request.approval },
    { label: 'CI', icon: ListChecks, summary: request.ci, history: request.historicalCi },
  ]
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
          <RequestState :status="request.status" :draft="request.draft" />
        </SheetHeader>

        <div class="drawer-body" role="region" aria-label="合并请求详情正文" tabindex="0">
          <section class="drawer-section" aria-labelledby="description-title">
            <h3 id="description-title">描述</h3>
            <p v-if="request.descriptionUnavailable" class="muted">不可用。{{ request.descriptionUnavailable }}</p>
            <p v-else-if="request.description" class="request-description">{{ request.description }}</p>
            <p v-else class="muted">无描述</p>
          </section>
          <section class="drawer-section" aria-labelledby="branches-title">
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
          <section class="drawer-section" aria-labelledby="attributes-title">
            <h3 id="attributes-title">概要</h3>
            <dl class="request-attributes">
              <div><dt><UserRound aria-hidden="true" />作者</dt><dd>{{ request.author }}</dd></div>
              <div><dt><CalendarDays aria-hidden="true" />创建时间</dt><dd><time :datetime="request.created.replace(' ', 'T')">{{ request.created }}</time></dd></div>
              <div><dt><CalendarDays aria-hidden="true" />更新时间</dt><dd><time :datetime="request.updated.replace(' ', 'T')">{{ request.updated }}</time></dd></div>
              <div v-if="request.ended"><dt><CalendarDays aria-hidden="true" />{{ request.status === 'merged' ? '合并时间' : '关闭时间' }}</dt><dd><time :datetime="request.ended.replace(' ', 'T')">{{ request.ended }}</time></dd></div>
            </dl>
          </section>
          <section class="drawer-section" aria-labelledby="check-title">
            <h3 id="check-title">审阅与检查</h3>
            <dl class="check-strip">
              <div v-for="{ label, icon, summary, history } in checks" :key="label" class="check-reading">
                <dt><component :is="icon" aria-hidden="true" />{{ label }}</dt>
                <dd class="check-status" :data-result="summary.status">{{ summary.status }}</dd>
                <dd v-if="summary.evidence || summary.detail || history" class="check-context">
                  <template v-if="summary.evidence">
                    <span>适用提交 <code>{{ summary.evidence.commit }}</code></span>
                    <time :datetime="summary.evidence.recordedAt.replace(' ', 'T')">{{ summary.evidence.recordedAt }}</time>
                  </template>
                  <p v-if="summary.detail">{{ summary.detail }}</p>
                  <div v-if="history" class="check-history">
                    <strong>历史 CI · {{ history.status }}</strong>
                    <span>旧提交 <code>{{ history.commit }}</code></span>
                    <time :datetime="history.recordedAt.replace(' ', 'T')">{{ history.recordedAt }}</time>
                  </div>
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
.check-history {
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

.check-history {
  margin-top: 4px;
  padding-top: 7px;
  border-top: 1px solid var(--divider);
}

.check-history strong {
  color: var(--brand-color-text);
  font-weight: 650;
}

.drawer-footer .action-primary {
  flex: none;
}

@container request-details (width < 400px) {
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
