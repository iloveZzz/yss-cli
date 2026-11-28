<script setup lang="ts">
import { ArrowRight, TriangleAlert } from '@lucide/vue'
import { computed, nextTick, ref, shallowRef, useTemplateRef, watch } from 'vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { assistantA2uiText, parseAssistantA2ui, type AssistantA2uiSelection } from '../schemas/assistant-a2ui'
import AssistantA2uiNode from './AssistantA2uiNode.vue'

const props = defineProps<{
  content: string
  textOnly?: boolean
  interactive?: boolean
  append?: (text: string) => Promise<boolean> | boolean
  selectionKey?: string
}>()

type Surface = NonNullable<ReturnType<typeof parseAssistantA2ui>['surface']>
const storagePrefix = 'yss:a2ui-selection:'
const block = useTemplateRef<HTMLElement>('block')
const surface = shallowRef<Surface | null>(null)
const selection = shallowRef<AssistantA2uiSelection>()
const invalidUpdate = ref(false)
const appending = ref(false)
const appendFailed = ref(false)
const interactive = computed(() => Boolean(!props.textOnly && props.interactive && props.append))
const readableText = computed(() => props.textOnly ? assistantA2uiText(props.content) : '')

function restoreSelection(key: string | undefined, source: Surface) {
  if (!key) return undefined
  try {
    const stored = sessionStorage.getItem(storagePrefix + key)
    const saved: unknown = stored ? JSON.parse(stored) : null
    if (!Array.isArray(saved) || saved.length !== 4 || saved[0] !== source.id || typeof saved[1] !== 'string') return undefined
    const option = source.options.get(saved[1])
    if (option && option.optionLabel === saved[2] && option.text === saved[3]) return option
  } catch {
    // Unavailable storage should not affect the card.
  }
  return undefined
}

watch([() => props.content, () => props.textOnly, () => props.selectionKey], ([content, textOnly, key], previous) => {
  if (textOnly) return
  if (key !== previous?.[2]) {
    surface.value = null
    selection.value = undefined
    appendFailed.value = false
  }
  const next = parseAssistantA2ui(content)
  invalidUpdate.value = next.invalidUpdate
  if (!next.surface) return
  const current = next.surface.id === surface.value?.id ? selection.value : undefined
  const updated = current && next.surface.options.get(current.id)
  if (!current || !updated || updated.text !== current.text || updated.optionLabel !== current.optionLabel) {
    selection.value = restoreSelection(key, next.surface)
    appendFailed.value = false
  }
  surface.value = next.surface
}, { immediate: true })

watch([interactive, surface], async ([enabled]) => {
  if (!enabled) appendFailed.value = false
  const active = document.activeElement
  if (!(active instanceof HTMLButtonElement) || !block.value?.contains(active)) return
  await nextTick()
  if ((!active.isConnected || active.disabled) &&
    (document.activeElement === active || document.activeElement === document.body)) {
    block.value?.focus()
  }
})

function select(option: AssistantA2uiSelection) {
  if (!interactive.value || appending.value || !surface.value) return
  selection.value = option
  appendFailed.value = false
  if (!props.selectionKey) return
  try {
    sessionStorage.setItem(storagePrefix + props.selectionKey, JSON.stringify([
      surface.value.id, option.id, option.optionLabel, option.text,
    ]))
  } catch {
    // The choice remains usable while this card is mounted.
  }
}

async function appendSelection() {
  const selected = selection.value
  if (!interactive.value || !props.append || !selected || appending.value) return
  appending.value = true
  appendFailed.value = false
  try {
    appendFailed.value = !await props.append(selected.text) && interactive.value && selection.value === selected
  } catch {
    appendFailed.value = interactive.value && selection.value === selected
  } finally {
    appending.value = false
  }
}
</script>

<template>
  <p v-if="textOnly" class="a2ui-plain" dir="auto">
    {{ readableText || '此回复未提供可阅读的界面内容。可继续请 Agent 用文字说明。' }}
  </p>
  <article v-else ref="block" class="assistant-a2ui" aria-label="助手回复卡片" tabindex="-1">
    <p v-if="invalidUpdate" class="a2ui-notice" role="status">
      <TriangleAlert aria-hidden="true" class="size-4" />
      <span v-if="surface">这次卡片更新无法显示，仍可查看上次有效的内容和依据。</span>
      <span v-else>收到的卡片格式无法显示，可以继续阅读本轮文字。</span>
    </p>
    <Card v-if="surface" :key="`${selectionKey ?? ''}:${surface.id}`" class="a2ui-surface">
      <AssistantA2uiNode
        :node="surface.root"
        :interactive="interactive"
        :selection-busy="appending"
        :selected-id="selection?.id"
        heading="h3"
        outer
        @select="select"
      />
      <div v-if="(interactive && surface.options.size > 0) || selection" class="a2ui-footer">
        <span class="a2ui-selection" role="status">
          <template v-if="selection">已选「<bdi>{{ selection.optionLabel }}</bdi>」</template>
        </span>
        <Button
          v-if="interactive"
          type="button"
          class="a2ui-continue"
          size="sm"
          :disabled="!selection || appending"
          :aria-busy="appending"
          @click="appendSelection"
        >
          继续讨论
          <LoadingIndicator v-if="appending" :active="appending" />
          <ArrowRight v-else aria-hidden="true" />
        </Button>
      </div>
      <p v-if="appendFailed" class="a2ui-notice" role="status">
        <TriangleAlert aria-hidden="true" class="size-4" />
        <span>暂时无法加入草稿，请核对当前会话后再试。</span>
      </p>
    </Card>
  </article>
</template>

<style scoped>
.a2ui-plain {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.assistant-a2ui {
  --a2ui-foreground: var(--foreground, var(--brand-color-text));
  --a2ui-muted: var(--muted-foreground, var(--brand-color-text-secondary));
  --a2ui-border: var(--border, var(--yss-colors-border));
  --a2ui-input-border: var(--input, var(--yss-colors-border-control));
  --a2ui-focus: var(--ring, var(--yss-color-primary-control));
  --a2ui-accent: var(--accent, var(--yss-colors-primary-bg));
  container: assistant-card / inline-size;
  width: 100%;
  min-width: 0;
}
.assistant-a2ui:focus-visible,
.a2ui-continue:focus-visible {
  outline: 2px solid var(--a2ui-focus);
  outline-offset: 2px;
}
.a2ui-surface {
  gap: 12px;
  min-width: 0;
  padding: 14px 16px;
  border: 1px solid var(--a2ui-border);
  border-radius: var(--brand-border-radius-lg);
  background: var(--workbench-glass-card);
  color: var(--a2ui-foreground);
  box-shadow: none;
}
.a2ui-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 12px;
  padding-block-start: 8px;
  border-block-start: 1px solid var(--a2ui-border);
}
.a2ui-selection {
  min-width: 0;
  margin-inline-end: auto;
  color: var(--a2ui-muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}
.a2ui-continue {
  min-height: 34px;
  border-radius: var(--brand-border-radius);
  background: var(--primary, var(--yss-color-primary-control));
  color: var(--primary-foreground, var(--yss-color-on-primary));
  font-size: 13px;
}
.a2ui-continue:hover:not(:disabled) {
  background: var(--brand-color-primary-hover, var(--yss-color-primary-control-hover));
}
.a2ui-notice {
  display: flex;
  align-items: baseline;
  gap: 6px;
  color: var(--destructive, var(--yss-colors-error-text));
  font-size: 12px;
  overflow-wrap: anywhere;
}
.a2ui-notice svg {
  flex: none;
  align-self: start;
}
.a2ui-notice + .a2ui-surface {
  margin-block-start: 8px;
}
@media (max-width: 720px), (any-pointer: coarse) {
  .a2ui-continue {
    min-width: 44px;
    min-height: 44px;
  }
}
@media (max-width: 720px) {
  .a2ui-surface {
    padding-inline: 12px;
  }
}
@media (forced-colors: active) {
  .assistant-a2ui:focus-visible,
  .a2ui-continue:focus-visible {
    outline-color: Highlight;
  }
}
</style>
