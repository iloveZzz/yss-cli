<script setup lang="ts">
import { computed, ref, useTemplateRef, watch } from 'vue'
import MarkdownIt from 'markdown-it'
import { BookOpen, CircleCheck, CircleDot, CodeXml, ExternalLink, FileText } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type { Issue, IssueSource } from './fixtures'

const props = defineProps<{
  open: boolean
  issue: Issue | null
  source: IssueSource | undefined
  problem?: { title: string; text: string; retryLabel?: string }
  retrying?: boolean
}>()
const emit = defineEmits<{ close: []; restoreFocus: []; retry: [] }>()
const mode = ref('preview')
const heading = useTemplateRef<InstanceType<typeof SheetTitle>>('heading')
const entry = computed(() => props.problem ? null : props.issue)
const dateFormat = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' })
function webAddress(value: string | null) {
  if (!value) return
  try {
    const url = new URL(value)
    if (['http:', 'https:'].includes(url.protocol) && !url.username && !url.password) return url
  } catch { return }
}
const original = computed(() => {
  if (!entry.value || !props.source) return
  const url = webAddress(entry.value.url)
  const origin = props.source.provider === 'GitHub' ? 'https://github.com' : 'https://gitlab.com'
  return url?.origin === origin ? url.href : undefined
})

const markdown = new MarkdownIt({ html: false, linkify: false })
markdown.core.ruler.after('inline', 'readonly_task_list', state => {
  for (let index = 2; index < state.tokens.length; index++) {
    const item = state.tokens[index - 2]
    const inline = state.tokens[index]
    if (item?.type !== 'list_item_open' || inline?.type !== 'inline') continue
    const first = inline.children?.[0]
    const match = first?.type === 'text' && /^\[([ xX])\]\s+/.exec(first.content)
    if (!first || !match) continue
    const checkbox = new state.Token('readonly_checkbox', '', 0)
    checkbox.markup = match[1] === ' ' ? '' : 'checked'
    checkbox.content = inline.content.slice(match[0].length)
    first.content = first.content.slice(match[0].length)
    inline.children!.unshift(checkbox)
    item.attrJoin('class', 'reader-task')
  }
})
markdown.renderer.rules.readonly_checkbox = (tokens, index) => {
  const token = tokens[index]!
  return `<input type="checkbox" disabled ${token.markup} aria-label="${markdown.utils.escapeHtml(token.content)}"> `
}
markdown.renderer.rules.image = (tokens, index) => {
  const alternative = markdown.utils.escapeHtml(tokens[index]?.content || '未提供替代文字')
  return `<span class="reader-image">图片 · ${alternative}（在原平台查看）</span>`
}
markdown.renderer.rules.link_open = (tokens, index, options, env, renderer) => {
  const token = tokens[index]!
  const url = webAddress(token.attrGet('href'))?.href
  token.attrs = []
  const destination = url || env.original
  if (destination) {
    token.attrSet('href', destination)
    token.attrSet('target', '_blank')
    token.attrSet('rel', 'noopener noreferrer')
  }
  const opening = renderer.renderToken(tokens, index, options)
  return !url && env.original ? opening + '<span class="reader-reference">在原平台查看</span> ' : opening
}
markdown.renderer.rules.table_open = () => '<div class="reader-table" role="region" aria-label="正文表格" tabindex="0"><table>'
markdown.renderer.rules.table_close = () => '</table></div>\n'
const preview = computed(() => entry.value ? markdown.render(entry.value.body, { original: original.value }) : '')

watch(() => [props.open, props.source?.id, entry.value?.number], () => {
  if (props.open) mode.value = 'preview'
})

function focusHeading(event?: Event) {
  const target = heading.value?.$el
  if (!(target instanceof HTMLElement)) return
  event?.preventDefault()
  target.focus({ preventScroll: true })
}

function restoreFocus(event: Event) {
  event.preventDefault()
  emit('restoreFocus')
}

function retryDetail() {
  focusHeading()
  emit('retry')
}
</script>

<template>
  <Sheet :open="open" @update:open="nextOpen => { if (!nextOpen) emit('close') }">
    <SheetContent
      class="issue-reader"
      @open-auto-focus="focusHeading"
      @close-auto-focus="restoreFocus"
    >
      <SheetHeader class="reader-heading">
        <div class="reader-heading-row">
          <div class="reader-identity">
            <p v-if="entry && source" class="reader-location">{{ source.provider }} · {{ source.repository }} <span>#{{ entry.number }}</span></p>
            <SheetTitle ref="heading" tabindex="-1" class="reader-title">{{ entry?.title || 'Issue 详情' }}</SheetTitle>
            <SheetDescription class="sr-only">Issue 的状态与 Markdown 正文</SheetDescription>
          </div>
          <slot name="scenario" />
        </div>
        <template v-if="entry && source">
          <div class="reader-summary">
            <Badge variant="outline" class="reader-state" :data-issue-state="entry.state">
              <component :is="entry.state === 'open' ? CircleDot : CircleCheck" aria-hidden="true" />
              {{ entry.state === 'open' ? '打开' : '已关闭' }}
            </Badge>
            <Badge v-for="label in entry.labels" :key="label" variant="secondary" class="reader-label">{{ label }}</Badge>
          </div>
          <div class="reader-meta">
            <span>来源工程 <strong>{{ source.project }}</strong></span>
            <span>更新于 <time :datetime="entry.updatedAt">{{ dateFormat.format(new Date(entry.updatedAt)) }}</time></span>
          </div>
        </template>
      </SheetHeader>

      <div class="reader-scroll" role="region" aria-label="Issue 详情正文" :aria-busy="retrying" tabindex="0">
        <Alert v-if="problem" class="reader-problem">
          <AlertTitle>{{ problem.title }}</AlertTitle>
          <AlertDescription>{{ problem.text }}</AlertDescription>
          <Button v-if="problem.retryLabel" type="button" variant="outline" :disabled="retrying" @click="retryDetail">
            {{ retrying ? '正在读取…' : problem.retryLabel }}
          </Button>
        </Alert>
        <section v-else-if="entry" class="reader-section" aria-labelledby="issue-body-heading">
          <Tabs v-if="entry.body.trim()" v-model="mode" activation-mode="manual" class="reader-tabs">
            <div class="reader-body-bar">
              <h3 id="issue-body-heading">正文</h3>
              <TabsList aria-label="正文显示方式">
                <TabsTrigger value="preview"><BookOpen aria-hidden="true" />预览</TabsTrigger>
                <TabsTrigger value="source"><CodeXml aria-hidden="true" />原文</TabsTrigger>
              </TabsList>
            </div>
            <TabsContent value="preview" class="reader-panel">
              <!-- markdown-it escapes raw HTML; only static checkbox, text and safe external anchors are added. -->
              <div class="reader-markdown" v-html="preview" />
            </TabsContent>
            <TabsContent value="source" class="reader-panel">
              <pre class="reader-source">{{ entry.body }}</pre>
            </TabsContent>
          </Tabs>
          <template v-else>
            <div class="reader-body-bar"><h3 id="issue-body-heading">正文</h3></div>
            <Empty class="reader-empty"><FileText aria-hidden="true" /><EmptyDescription>此 Issue 没有正文。</EmptyDescription></Empty>
          </template>
        </section>
      </div>

      <footer v-if="entry && source" class="reader-footer">
        <Button v-if="original" as-child>
          <a :href="original" target="_blank" rel="noopener noreferrer"><ExternalLink aria-hidden="true" />在 {{ source.provider }} 打开</a>
        </Button>
        <span v-else class="reader-meta">原站链接不可用</span>
      </footer>
    </SheetContent>
  </Sheet>
</template>

<style scoped>
:global(.issue-reader[data-slot='sheet-content']) {
  gap: 0;
  width: min(525px, 100%);
  max-width: none;
  min-width: 0;
  height: 100dvh;
  background-color: var(--brand-color-bg-elevated);
  color: var(--yss-colors-text-title);
}
:global(.issue-reader[data-slot='sheet-content'] > button) {
  top: 8px;
  right: 8px;
  display: grid;
  width: 32px;
  height: 32px;
  place-items: center;
  border-radius: 8px;
}
.reader-heading {
  flex: none;
  gap: 8px;
  padding: 21px 50px 16px 20px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.reader-heading-row { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: start; gap: 4px 12px; }
.reader-identity { flex: 1 1 200px; min-width: 0; display: grid; gap: 6px; }
.reader-location, .reader-meta { margin: 0; color: var(--brand-color-text-secondary, #62666c); font-size: 12px; overflow-wrap: anywhere; }
.reader-location span { margin-inline-start: 4px; font-variant-numeric: tabular-nums; }
.reader-title { margin: 0; font-size: 20px; line-height: 1.36; overflow-wrap: anywhere; }
.reader-title:focus { outline: none; }
.reader-summary, .reader-meta { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; }
.reader-meta { gap: 4px 16px; }
.reader-meta strong, .reader-meta time { font-weight: 500; color: var(--brand-color-text-default, #25272b); }
.reader-state { white-space: normal; }
.reader-state[data-issue-state='open'] { background: #e7f4ec; border-color: #b5d9c1; color: #18613e; }
.reader-state[data-issue-state='closed'] { background: #f1edf8; border-color: #d8c9ee; color: #684395; }
.reader-label { max-width: 100%; overflow-wrap: anywhere; white-space: normal; }
.reader-scroll { flex: 1; min-width: 0; min-height: 0; overflow: auto; padding: 17px 20px 20px; overscroll-behavior: contain; }
.reader-footer { display: flex; flex: none; justify-content: flex-end; padding: 12px 20px max(12px, env(safe-area-inset-bottom)); border-top: 1px solid var(--yss-colors-border); }
.reader-problem { display: grid; gap: 6px; white-space: normal; }
.reader-problem :deep([data-slot='button']) { justify-self: start; margin-top: 4px; }
.reader-tabs { gap: 0; }
.reader-body-bar { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; margin-bottom: 14px; }
.reader-body-bar h3 { margin: 0; font-size: 14px; font-weight: 650; }
.reader-panel { min-width: 0; }
.reader-scroll:focus-visible, .reader-panel:focus-visible, .reader-markdown :deep(.reader-table:focus-visible) { outline: 2px solid var(--brand-color-primary, #3e6ae1); outline-offset: -3px; }
.reader-markdown, .reader-source { max-width: 78ch; margin: 0; font-size: 14px; line-height: 1.75; overflow-wrap: anywhere; color: var(--brand-color-text-default, #25272b); }
.reader-source, .reader-markdown :deep(code) { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.reader-source { white-space: pre-wrap; font-size: 13px; }
.reader-markdown :deep(p) { margin: 0 0 12px; }
.reader-markdown :deep(h1), .reader-markdown :deep(h2), .reader-markdown :deep(h3), .reader-markdown :deep(h4) { margin: 20px 0 7px; font-weight: 650; line-height: 1.4; }
.reader-markdown :deep(h1) { font-size: 20px; }
.reader-markdown :deep(h2) { font-size: 17px; }
.reader-markdown :deep(h3), .reader-markdown :deep(h4) { font-size: 15px; }
.reader-markdown :deep(ol), .reader-markdown :deep(ul) { margin: 0 0 12px; padding-inline-start: 1.6em; }
.reader-markdown :deep(ol) { list-style: decimal; }
.reader-markdown :deep(ul) { list-style: disc; }
.reader-markdown :deep(li + li) { margin-top: 3px; }
.reader-markdown :deep(.reader-task) { list-style: none; margin-inline-start: -1.45em; }
.reader-markdown :deep(input[type='checkbox']) { margin-inline-end: 3px; accent-color: var(--brand-color-primary, #3e6ae1); }
.reader-markdown :deep(blockquote) { margin: 12px 0; padding: 3px 0 3px 12px; border-inline-start: 3px solid #b9c7e7; color: var(--brand-color-text-secondary, #62666c); }
.reader-markdown :deep(blockquote p), .reader-markdown :deep(> :last-child) { margin-bottom: 0; }
.reader-markdown :deep(a) { color: var(--brand-color-primary, #3e6ae1); text-decoration: underline; text-underline-offset: 2px; }
.reader-markdown :deep(.reader-reference), .reader-markdown :deep(.reader-image) { font-size: 12px; }
.reader-markdown :deep(.reader-image) { color: var(--brand-color-text-secondary, #62666c); }
.reader-markdown :deep(code) { padding: 2px 4px; border-radius: 4px; background: #f0f2f6; font-size: .92em; }
.reader-markdown :deep(pre) { margin: 10px 0 14px; padding: 10px 12px; border-radius: 7px; background: #f0f2f6; white-space: pre-wrap; }
.reader-markdown :deep(pre code) { padding: 0; }
.reader-markdown :deep(.reader-table) { max-width: 100%; margin: 8px 0 14px; overflow-x: auto; border-radius: 5px; }
.reader-markdown :deep(table) { border-collapse: collapse; font-size: 13px; width: max-content; max-width: 100%; }
.reader-markdown :deep(th), .reader-markdown :deep(td) { padding: 6px 10px; border: 1px solid var(--yss-colors-border, #e4e6eb); text-align: start; }
.reader-markdown :deep(th) { background: #f4f5f8; font-weight: 600; }
.reader-empty { min-height: 160px; gap: 8px; }
.reader-empty > svg { width: 21px; height: 21px; color: var(--brand-color-text-secondary, #62666c); }
@media (max-width: 719px) {
  :global(.issue-reader[data-slot='sheet-content'] > button) { width: 44px; height: 44px; }
  .reader-footer :deep([data-slot='button']), .reader-body-bar :deep([role='tab']) { min-height: 44px; }
  .reader-body-bar :deep([role='tablist']) { height: auto; }
}
@media (width < 460px) {
  .reader-heading, .reader-scroll, .reader-footer { padding-inline-start: 15px; }
  .reader-scroll, .reader-footer { padding-inline-end: 15px; }
}
</style>
