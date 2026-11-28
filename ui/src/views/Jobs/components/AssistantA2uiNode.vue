<script setup lang="ts">
import { computed } from 'vue'
import { Check, ChevronDown } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import type { AssistantA2uiNode, AssistantA2uiSelection } from '../schemas/assistant-a2ui'

type Container = Extract<AssistantA2uiNode, { children: AssistantA2uiNode[] }>
type OptionLayout = 'row' | 'comparison'

const props = defineProps<{
  node: AssistantA2uiNode
  interactive: boolean
  selectionBusy: boolean
  selectedId?: string
  heading?: 'h3' | 'h4'
  outer?: boolean
  optionLayout?: OptionLayout
  representedOption?: boolean
}>()

const emit = defineEmits<{ select: [option: AssistantA2uiSelection] }>()

function isContainer(node: AssistantA2uiNode): node is Container {
  return node.kind === 'Card' || node.kind === 'Column'
}

function content(node: Container): AssistantA2uiNode[] {
  const child = node.children[0]
  return node.kind === 'Card' && child?.kind === 'Column' ? child.children : node.children
}

function choice(node: AssistantA2uiNode) {
  if (!isContainer(node)) return undefined
  const options = content(node).filter(child => child.kind === 'select')
  return options.length === 1 ? options[0] : undefined
}

const container = computed(() => isContainer(props.node) ? props.node : undefined)
const headingId = computed(() => {
  if (!props.heading || !container.value) return undefined
  return container.value.children.find(child => child.kind === 'Text' && !child.caption && child.text.trim())?.id
})
const headingWrapper = computed(() => container.value?.children.length === 1 &&
  container.value.children[0]?.kind === 'Column')
const optionGroups = computed<OptionLayout | undefined>(() => {
  const children = container.value?.children
  if (!children || children.length < 2 || !children.every(isContainer) || !children.every(choice)) return undefined
  // Comparison options can each supply evidence along with their named dimensions.
  const comparable = children.every(child => {
    const items = content(child)
    return items.filter(item => item.kind === 'Text').length >= 2 || !items.some(item => item.kind === 'details')
  })
  return comparable ? 'comparison' : 'row'
})
const option = computed(() => props.optionLayout ? choice(props.node) : undefined)
const visibleText = computed(() => new Set(
  container.value?.children.flatMap(child => child.kind === 'Text' ? [child.text.trim()] : []) ?? [],
))
const selected = computed(() => Boolean(props.selectedId &&
  (props.selectedId === props.node.id || props.selectedId === option.value?.id)))

function childHeading(child: AssistantA2uiNode) {
  if (child.id === headingId.value || headingWrapper.value) return props.heading
  if (optionGroups.value) return 'h4'
  return undefined
}
</script>

<template>
  <component
    :is="node.kind === 'Card' && !outer && !optionLayout ? Card : 'div'"
    v-if="container"
    class="a2ui-group"
    :data-option-layout="optionLayout"
    :data-group-layout="optionGroups"
    :data-selected="option && selected ? true : undefined"
  >
    <AssistantA2uiNode
      v-for="child in container.children"
      :key="child.id"
      :node="child"
      :interactive="interactive"
      :selection-busy="selectionBusy"
      :selected-id="selectedId"
      :heading="childHeading(child)"
      :outer="outer && headingWrapper"
      :option-layout="optionGroups"
      :represented-option="child.kind === 'select' && visibleText.has(child.optionLabel.trim())"
      @select="emit('select', $event)"
    />
  </component>
  <component
    :is="heading || 'p'"
    v-else-if="node.kind === 'Text' && node.text.trim()"
    class="a2ui-text"
    :class="{ 'a2ui-caption': node.caption, 'a2ui-heading': heading }"
    dir="auto"
  >{{ node.text }}</component>
  <Collapsible v-else-if="node.kind === 'details'" as-child>
    <div class="a2ui-evidence">
      <CollapsibleTrigger as-child>
        <Button type="button" variant="ghost" size="sm" class="a2ui-action a2ui-evidence-toggle">
          <span dir="auto">{{ node.buttonLabel }}</span>
          <ChevronDown aria-hidden="true" />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent class="a2ui-evidence-content">
        <p class="a2ui-text" dir="auto">{{ node.text }}</p>
      </CollapsibleContent>
    </div>
  </Collapsible>
  <template v-else-if="node.kind === 'select'">
    <Button
      v-if="interactive"
      type="button"
      :variant="node.variant === 'borderless' ? 'ghost' : 'outline'"
      size="sm"
      class="a2ui-action a2ui-choice"
      :aria-label="node.buttonLabel.includes(node.optionLabel) ? undefined : `${node.buttonLabel} · ${node.optionLabel}`"
      :aria-pressed="selected"
      :disabled="selectionBusy"
      @click="emit('select', node)"
    >
      <Check v-if="selected" aria-hidden="true" />
      <span dir="auto">{{ node.buttonLabel }}</span>
    </Button>
    <p v-else-if="!representedOption" class="a2ui-text" dir="auto">{{ node.optionLabel }}</p>
  </template>
</template>

<style scoped>
.a2ui-group {
  display: grid;
  min-width: 0;
  align-content: start;
  gap: 8px;
}
.a2ui-group[data-slot='card'] {
  gap: 8px;
  padding: 12px;
  border-radius: var(--brand-border-radius-lg);
  background: var(--workbench-glass-pane);
  box-shadow: none;
}
.a2ui-group[data-option-layout='comparison'],
.a2ui-group[data-option-layout='row'] {
  padding: 12px;
  border: 1px solid var(--a2ui-border);
  border-radius: var(--brand-border-radius);
  background: var(--workbench-glass-pane);
}
.a2ui-group[data-option-layout][data-selected='true'] {
  border-color: var(--a2ui-focus);
  background: var(--a2ui-accent);
}
.a2ui-group[data-group-layout='row'] {
  gap: 6px;
}
.a2ui-text {
  min-width: 0;
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.a2ui-text.a2ui-caption {
  color: var(--a2ui-muted);
  font-size: 12px;
}
.a2ui-text.a2ui-heading {
  color: var(--a2ui-foreground);
  font-size: 1.14em;
  line-height: 1.4;
  font-weight: 650;
}
h4.a2ui-text.a2ui-heading {
  font-size: 1em;
}
.a2ui-evidence {
  min-width: 0;
}
.a2ui-action {
  justify-self: start;
  max-width: 100%;
  min-width: 34px;
  min-height: 34px;
  height: auto;
  padding-block: 6px;
  border-radius: var(--brand-border-radius);
  font-size: 13px;
  line-height: 1.45;
  text-align: start;
  white-space: normal;
  overflow-wrap: anywhere;
}
.a2ui-action span {
  min-width: 0;
}
.a2ui-action:focus-visible {
  outline: 2px solid var(--a2ui-focus);
  outline-offset: 2px;
}
.a2ui-evidence-toggle {
  color: var(--a2ui-muted);
}
.a2ui-evidence-toggle[aria-expanded='true'] svg {
  rotate: 180deg;
}
.a2ui-evidence-content {
  margin-block-start: 4px;
  padding: 6px 10px;
  border-inline-start: 2px solid var(--a2ui-input-border);
  color: var(--a2ui-muted);
}
.a2ui-choice[aria-pressed='true'] {
  background: var(--a2ui-accent);
  border-color: var(--a2ui-focus);
  color: var(--a2ui-foreground);
}
@container assistant-card (min-width: 30rem) {
  .a2ui-group[data-group-layout='comparison'] {
    grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  }
}
@media (max-width: 720px), (any-pointer: coarse) {
  .a2ui-action {
    min-width: 44px;
    min-height: 44px;
  }
}
@media (forced-colors: active) {
  .a2ui-group[data-option-layout][data-selected='true'],
  .a2ui-choice[aria-pressed='true'] {
    border-color: Highlight;
  }
  .a2ui-action:focus-visible {
    outline-color: Highlight;
  }
}
</style>
