<script setup lang="ts">
import { computed, useTemplateRef, ref, watch } from 'vue'
import { BookOpen, CodeXml, ExternalLink, FileText, TriangleAlert } from '@lucide/vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type { IssueDetail, IssueProblem, IssueSource } from '@/services/workbench-issues'
import { issueDate, issueUrl, platformLabels, problemTitle, retryReadLabel, sourceHost } from '../schemas/issues'
import { renderIssueMarkdown } from '../schemas/markdown'
import IssueState from './IssueState.vue'

const props = defineProps<{
  reader?: { number: number; result?: IssueDetail; problem?: IssueProblem }
  source?: IssueSource
}>()
const emit = defineEmits<{ close: []; restoreFocus: [event: Event]; retry: [] }>()

const heading = useTemplateRef<InstanceType<typeof SheetTitle>>('heading')
const mode = ref('preview')
const result = computed(() => props.reader?.result)
const pending = computed(() => !!props.reader && !result.value && !props.reader.problem)
const original = computed(() => {
  if (!props.source || !result.value) return undefined
  return issueUrl(result.value.url, props.source, result.value.number)
})
const body = computed(() => result.value?.body ?? '')
const preview = computed(() => body.value.trim() ? renderIssueMarkdown(body.value, original.value) : '')

watch(() => props.reader?.number, () => { mode.value = 'preview' })

function focusHeading(event?: Event) {
  const target = heading.value?.$el
  if (!(target instanceof HTMLElement)) return
  event?.preventDefault()
  target.focus({ preventScroll: true })
}

function retry() {
  focusHeading()
  emit('retry')
}
</script>

<template>
  <Sheet :open="!!reader" @update:open="open => { if (!open) emit('close') }">
    <SheetContent
      class="issue-reader"
      :style="{ width: 'min(525px, 100%)', maxWidth: 'none', height: '100dvh', gap: 0, backgroundColor: 'var(--brand-color-bg-elevated)' }"
      @open-auto-focus="focusHeading"
      @close-auto-focus="emit('restoreFocus', $event)"
    >
      <SheetHeader class="reader-heading">
        <p v-if="source && result" class="reader-location">
          {{ platformLabels[source.platform] }} · {{ sourceHost(source) }} · {{ source.slug }}
          <span>#{{ result.number }}</span>
        </p>
        <SheetTitle ref="heading" class="reader-title" tabindex="-1">
          {{ result ? result.title || '标题不可用' : 'Issue 详情' }}
        </SheetTitle>
        <SheetDescription class="sr-only">Issue 的状态与 Markdown 正文</SheetDescription>
        <template v-if="source && result">
          <div class="reader-summary">
            <IssueState :status="result.status" chip />
            <Badge v-for="label in result.labels" :key="label" variant="secondary" class="reader-label">{{ label }}</Badge>
          </div>
          <div class="reader-meta">
            <span>来源工程 <strong>{{ source.projectName }}</strong></span>
            <span>更新于 <time v-if="result.updatedAt" :datetime="result.updatedAt">{{ issueDate(result.updatedAt) }}</time><template v-else>时间不可用</template></span>
          </div>
        </template>
      </SheetHeader>

      <div class="reader-scroll" role="region" aria-label="Issue 详情正文" :aria-busy="pending" tabindex="0">
        <LoadingSkeleton v-if="pending" variant="detail" :rows="5" label="正在读取 Issue 详情" />
        <Alert v-else-if="reader?.problem" class="notice warning reader-problem">
          <TriangleAlert aria-hidden="true" />
          <AlertTitle>{{ problemTitle(reader.problem, '未能读取 Issue 详情') }}</AlertTitle>
          <AlertDescription>{{ reader.problem.message }}</AlertDescription>
          <Button type="button" variant="outline" class="justify-self-start" @click="retry">
            {{ retryReadLabel(reader.problem) }}
          </Button>
        </Alert>
        <section v-else-if="result" aria-labelledby="issue-body-heading">
          <Tabs v-if="body.trim()" v-model="mode" class="gap-0" activation-mode="manual">
            <div class="reader-body-bar">
              <h3 id="issue-body-heading">正文</h3>
              <TabsList aria-label="正文显示方式">
                <TabsTrigger value="preview"><BookOpen aria-hidden="true" />预览</TabsTrigger>
                <TabsTrigger value="source"><CodeXml aria-hidden="true" />原文</TabsTrigger>
              </TabsList>
            </div>
            <TabsContent value="preview" class="min-w-0">
              <!-- The renderer escapes HTML, replaces images and validates outgoing links. -->
              <div class="reader-markdown" v-html="preview" />
            </TabsContent>
            <TabsContent value="source" class="min-w-0">
              <pre class="reader-source">{{ body }}</pre>
            </TabsContent>
          </Tabs>
          <template v-else>
            <div class="reader-body-bar"><h3 id="issue-body-heading">正文</h3></div>
            <Empty class="reader-empty">
              <FileText aria-hidden="true" />
              <EmptyDescription>此 Issue 没有正文。</EmptyDescription>
            </Empty>
          </template>
        </section>
      </div>

      <footer v-if="source && result" class="reader-footer">
        <Button v-if="original" as-child>
          <a :href="original" target="_blank" rel="noopener noreferrer">
            <ExternalLink aria-hidden="true" />在 {{ platformLabels[source.platform] }} 打开
          </a>
        </Button>
        <span v-else class="muted">原站链接不可用</span>
      </footer>
    </SheetContent>
  </Sheet>
</template>

<style scoped>
:global(.issue-reader[data-slot='sheet-content'] > button) { inset: 8px 8px auto auto; display: grid; place-items: center; width: 32px; height: 32px; padding: 0; border-radius: 8px; }
.reader-heading { flex: none; gap: 8px; max-height: 42dvh; overflow-y: auto; overscroll-behavior: contain; padding: 21px 56px 16px 20px; border-bottom: 1px solid var(--yss-colors-border); }
.reader-title { font-size: 20px; line-height: 1.36; overflow-wrap: anywhere; }
.reader-location, .reader-meta { font-size: 12px; color: var(--brand-color-text-secondary); overflow-wrap: anywhere; }
.reader-location span { margin-inline-start: 4px; font-variant-numeric: tabular-nums; }
.reader-summary, .reader-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 6px; }
.reader-meta { gap: 4px 16px; }
.reader-meta :is(strong, time) { color: var(--brand-color-text); font-weight: 500; }
.reader-label { max-width: 100%; white-space: normal; overflow-wrap: anywhere; }
.reader-scroll { flex: 1; min-width: 0; min-height: 0; overflow: auto; overscroll-behavior: contain; padding: 17px 20px 20px; }
.reader-scroll:focus-visible, .reader-markdown :deep(.issue-markdown-table:focus-visible) { outline-offset: -3px; }
.reader-body-bar { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; margin-block-end: 14px; }
.reader-body-bar h3 { font-size: 14px; font-weight: 650; }
.reader-footer { display: flex; flex: none; justify-content: flex-end; padding: 12px 20px max(12px, env(safe-area-inset-bottom)); border-top: 1px solid var(--yss-colors-border); }
.reader-problem { white-space: normal; }
.reader-problem :deep([data-slot='button']) { margin-block-start: 4px; }
.reader-empty { min-height: 160px; gap: 8px; color: var(--brand-color-text-secondary); }
.reader-empty > svg { width: 21px; height: 21px; }
.reader-markdown, .reader-source { overflow-wrap: anywhere; font-size: 14px; line-height: 1.75; }
.reader-source, .reader-markdown :deep(code) { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.reader-source { white-space: pre-wrap; font-size: 13px; }
.reader-markdown :deep(p) { margin-block-end: 12px; }
.reader-markdown :deep(:is(h1, h2, h3, h4, h5, h6)) { margin-block: 20px 7px; font-size: 15px; font-weight: 650; line-height: 1.4; }
.reader-markdown :deep(h1) { font-size: 20px; }
.reader-markdown :deep(h2) { font-size: 17px; }
.reader-markdown :deep(:is(ol, ul)) { margin-block-end: 12px; padding-inline-start: 1.6em; }
.reader-markdown :deep(ol) { list-style: decimal; }
.reader-markdown :deep(ul) { list-style: disc; }
.reader-markdown :deep(li + li) { margin-block-start: 3px; }
.reader-markdown :deep(.issue-task) { margin-inline-start: -1.4em; list-style: none; }
.reader-markdown :deep(input[type='checkbox']) { margin-inline-end: 3px; accent-color: var(--yss-color-primary-control); }
.reader-markdown :deep(blockquote) { margin-block: 12px; padding: 3px 0 3px 12px; border-inline-start: 3px solid var(--yss-colors-border-control); color: var(--brand-color-text-secondary); }
.reader-markdown :deep(blockquote p:last-child) { margin-block-end: 0; }
.reader-markdown :deep(a[href]) { color: var(--yss-color-primary-control-active); text-decoration: underline; text-underline-offset: 2px; }
.reader-markdown :deep(:is(.issue-reference, .issue-image)) { color: var(--brand-color-text-secondary); font-size: 12px; }
.reader-markdown :deep(code) { padding: 2px 4px; border-radius: 4px; background: var(--brand-color-bg-layout); font-size: .92em; }
.reader-markdown :deep(pre) { margin-block: 10px 14px; padding: 10px 12px; border-radius: 7px; background: var(--brand-color-bg-layout); white-space: pre-wrap; }
.reader-markdown :deep(pre code) { padding: 0; }
.reader-markdown :deep(.issue-markdown-table) { max-width: 100%; margin-block: 8px 14px; overflow-x: auto; }
.reader-markdown :deep(table) { width: max-content; max-width: 100%; border-collapse: collapse; font-size: 13px; }
.reader-markdown :deep(:is(th, td)) { padding: 6px 10px; border: 1px solid var(--yss-colors-border); text-align: start; }
.reader-markdown :deep(th) { background: var(--brand-color-bg-layout); font-weight: 600; }
@media (width < 720px) {
  :global(.issue-reader[data-slot='sheet-content'] > button) { width: 44px; height: 44px; }
  .reader-body-bar :deep([role='tablist']) { height: auto; }
  .reader-body-bar :deep([role='tab']) { min-height: 44px; }
}
@media (width < 460px) {
  .reader-heading, .reader-scroll, .reader-footer { padding-inline-start: 15px; }
  .reader-scroll, .reader-footer { padding-inline-end: 15px; }
}
</style>
