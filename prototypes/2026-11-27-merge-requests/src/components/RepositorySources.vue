<script setup lang="ts">
import { useTemplateRef, computed, watch, ref } from 'vue'
import { Check, CircleAlert, GitBranch, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { defaultEnabled, repositoryFor, sources, type Source } from '@/demo'
import PlatformMark from './PlatformMark.vue'

const props = defineProps<{
  open: boolean
  connected: boolean
  enabled: string[]
  readStates: Record<string, { label: string; detail?: string; tone: 'neutral' | 'success' | 'warning' }>
}>()
const emit = defineEmits<{ close: []; save: [ids: string[]]; restoreFocus: [event: Event] }>()
const selected = ref<string[]>([])
const changed = computed(() => selected.value.length !== props.enabled.length || selected.value.some(id => !props.enabled.includes(id)))

const repositorySources = sources.map(source => ({
  ...source,
  repositories: source.repositories.map(item => ({
    ...item,
    repository: repositoryFor(item.id),
    pinned: defaultEnabled.includes(item.id),
  })),
}))
const pinnedSources = repositorySources.filter(source => source.repositories.some(item => item.pinned))
const sourceGroups = [
  { id: 'defaults', title: '默认纳入', entries: pinnedSources },
  { id: 'other', title: '其他已登记工程', entries: repositorySources.filter(source => !pinnedSources.includes(source)) },
]

watch(() => props.open, (open) => {
  if (open) selected.value = [...props.enabled]
})

function toggle(source: Source, id: string, checked: boolean) {
  if (!checked) {
    selected.value = selected.value.filter(item => item !== id)
    return
  }

  if (source.chooseOne) {
    const rivals = source.repositories.map(repo => repo.id)
    selected.value = selected.value.filter(item => !rivals.includes(item))
  }
  selected.value = [...selected.value, id]
}

function unresolved(source: Source) {
  return !source.repositories.length || (source.chooseOne && !source.repositories.some(repo => selected.value.includes(repo.id)))
}

const heading = useTemplateRef<InstanceType<typeof DialogTitle>>('heading')

function focusHeading(event: Event) {
  const target = heading.value?.$el
  if (!(target instanceof HTMLElement)) return
  event.preventDefault()
  target.focus({ preventScroll: true })
}
</script>

<template>
  <Dialog :open="open" @update:open="value => { if (!value) emit('close') }">
    <DialogContent
      class="sources-dialog" :show-close-button="false"
      @open-auto-focus="focusHeading"
      @close-auto-focus="event => emit('restoreFocus', event)"
    >
      <header class="sources-header">
        <div>
          <DialogTitle ref="heading" tabindex="-1">仓库来源</DialogTitle>
          <DialogDescription>从工作台已登记并启用的工程读取。此处管理哪些仓库纳入合并请求列表。</DialogDescription>
        </div>
        <DialogClose as-child>
          <Button class="source-close" variant="ghost" size="icon" aria-label="关闭仓库来源"><X aria-hidden="true" /></Button>
        </DialogClose>
      </header>
      <div v-if="connected" class="sources-body">
        <section v-for="group in sourceGroups" :key="group.id" class="source-group" :aria-labelledby="`source-group-${group.id}`">
          <h3 :id="`source-group-${group.id}`" class="source-group-title">{{ group.title }}</h3>
          <section v-for="source in group.entries" :key="source.id" class="source-entry" :aria-labelledby="`source-name-${source.id}`">
            <header>
              <GitBranch aria-hidden="true" />
              <h4 :id="`source-name-${source.id}`">{{ source.project }}</h4>
              <span v-if="unresolved(source)" class="source-warning"><CircleAlert aria-hidden="true" />{{ source.chooseOne ? '待确认来源' : '无可用远端' }}</span>
            </header>
            <p :id="`source-hint-${source.id}`">{{ source.hint }}</p>
            <div
              v-if="source.repositories.length"
              class="repository-rows"
              :role="source.chooseOne ? 'group' : undefined"
              :aria-labelledby="source.chooseOne ? `source-name-${source.id}` : undefined"
              :aria-describedby="source.chooseOne ? `source-hint-${source.id}` : undefined"
            >
              <component
                :is="item.pinned ? 'div' : 'label'"
                v-for="item in source.repositories"
                :key="item.id"
                class="repository-row"
                :class="{ 'repository-choice': !item.pinned }"
                :for="item.pinned ? undefined : `include-${item.id}`"
                :data-selected="!item.pinned && selected.includes(item.id) ? '' : undefined"
              >
                <Check v-if="item.pinned" class="included-default" aria-hidden="true" />
                <Checkbox v-else
                  :id="`include-${item.id}`"
                  :aria-labelledby="`repository-name-${item.id}`"
                  :aria-describedby="`repository-meta-${item.id} repository-read-${item.id}`"
                  :model-value="selected.includes(item.id)"
                  @update:model-value="value => toggle(source, item.id, value === true)"
                />
                <span class="repository-summary">
                  <span :id="`repository-name-${item.id}`" class="repository-name">{{ item.repository.slug }}</span>
                  <span :id="`repository-meta-${item.id}`" class="repository-meta">
                    <span>{{ item.binding }}</span>
                    <span class="repository-remote">
                      <PlatformMark :platform="item.repository.platform" />
                      <span class="repository-host">{{ item.repository.host }}</span>
                    </span>
                  </span>
                  <span :id="`repository-read-${item.id}`" class="repository-read" :data-tone="readStates[item.id].tone">
                    <span class="repository-read-label">{{ readStates[item.id].label }}</span>
                    <span v-if="readStates[item.id].detail" class="repository-read-detail">{{ readStates[item.id].detail }}</span>
                  </span>
                </span>
              </component>
            </div>
          </section>
        </section>
      </div>
      <div v-else class="sources-body sources-empty">
        <h3>尚无已登记工程</h3>
        <p class="muted">通过「产品与工程」登记并启用工程后，这里会显示可识别的仓库来源。</p>
      </div>
      <footer class="sources-footer">
        <span class="muted" aria-live="polite" aria-atomic="true">已选 {{ selected.length }} 个仓库</span>
        <div class="footer-actions">
          <DialogClose as-child><Button class="source-cancel" variant="outline">取消</Button></DialogClose>
          <Button class="source-save action-primary" :disabled="!changed" @click="emit('save', [...selected])">保存来源</Button>
        </div>
      </footer>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
.sources-body {
  container: repository-sources / inline-size;
  --sources-inset: 19px;
}

.source-group {
  padding-block: 14px 2px;
}

.source-group + .source-group {
  border-block-start: 1px solid var(--divider);
}

.source-group-title {
  margin-inline: var(--sources-inset);
  color: var(--muted);
  font-size: 12px;
}

.source-entry h4 {
  color: var(--title);
  font-size: 13px;
  font-weight: 650;
}

.repository-rows {
  display: grid;
  gap: 6px;
  margin-block-start: 8px;
}

.repository-row {
  display: grid;
  grid-template-columns: 16px minmax(0, 1fr);
  align-items: start;
  gap: 8px;
  padding: 2px 9px;
}

.included-default {
  width: 15px;
  height: 15px;
  margin-block-start: 2px;
  color: var(--open);
}

.repository-choice {
  min-height: 44px;
  padding-block: 6px;
  border: 1px solid var(--divider);
  border-radius: 8px;
  background: var(--workbench-glass-card);
  cursor: pointer;
}

.repository-choice:hover,
.repository-choice[data-selected] {
  border-color: #b9cbed;
  background: #eef4ff;
}

.repository-choice :deep([data-slot='checkbox']) {
  align-self: center;
}

.repository-choice:has(:focus-visible) {
  outline: 2px solid var(--yss-color-primary-control);
  outline-offset: 1px;
}

.repository-summary {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.repository-name {
  color: var(--title);
  font-weight: 600;
  overflow-wrap: anywhere;
}

.repository-meta,
.repository-remote {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 2px 12px;
  color: var(--muted);
  font-size: 11px;
}

.repository-remote {
  gap: 2px 6px;
}

.repository-host {
  overflow-wrap: anywhere;
}

.repository-read {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  min-width: 0;
  gap: 2px 6px;
  margin-block-start: 2px;
  color: var(--muted);
  font-size: 11px;
}

.repository-read-label {
  padding-inline: 5px;
  border-radius: 4px;
  color: #515c6e;
  background: #edf0f6;
  font-weight: 650;
}

.repository-read[data-tone='success'] .repository-read-label {
  color: var(--open);
  background: var(--opened-bg);
}

.repository-read[data-tone='warning'] .repository-read-label {
  color: #825419;
  background: #fbf1de;
}

.repository-read-detail {
  overflow-wrap: anywhere;
}

.sources-footer {
  flex-wrap: wrap;
}

.footer-actions {
  margin-inline-start: auto;
}

.source-close,
.source-cancel {
  min-block-size: 34px;
  border-radius: 8px;
}

.source-close {
  inline-size: 34px;
  color: #404c61;
}

.source-close svg {
  width: 15px;
  height: 15px;
  stroke-width: 1.8;
}

.source-cancel {
  border-color: #d1d7e3;
  background: var(--workbench-glass-card);
  box-shadow: none;
  font-size: 13px;
}

.source-close:focus-visible,
.source-cancel:focus-visible {
  outline: 2px solid var(--yss-color-primary-control);
  outline-offset: 2px;
}

@container repository-sources (width >= 530px) {
  .repository-row {
    align-items: center;
  }

  .included-default {
    margin-block-start: 0;
  }

  .repository-summary {
    grid-template-columns: minmax(0, .9fr) minmax(0, 1fr);
    align-items: baseline;
    column-gap: 12px;
  }

  .repository-read {
    grid-column: 2;
    margin-block-start: 0;
  }
}

@media (width < 740px) {
  .sources-body {
    --sources-inset: 12px;
  }
}

@media (width < 740px), (pointer: coarse) {
  .source-close,
  .source-cancel,
  .source-save {
    min-inline-size: 44px;
    min-block-size: 44px;
  }
}

@media (forced-colors: active) {
  .repository-choice {
    border-color: CanvasText;
  }

  .repository-choice[data-selected],
  .repository-choice:has(:focus-visible) {
    border-color: Highlight;
  }
}
</style>
