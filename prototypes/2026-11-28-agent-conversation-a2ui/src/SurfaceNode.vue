<script setup lang="ts">
import { computed, useId } from 'vue'
import { ArrowRight, Check, ChevronDown, Layers2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import type { ViewNode } from './surface'

const props = defineProps<{
  node: ViewNode
  selected?: string
  expanded?: readonly string[]
  readonly?: boolean
  prefix?: string
}>()
const localId = useId()
const prefix = computed(() => props.prefix ?? localId)
const state = computed(() => ({
  selected: props.selected, expanded: props.expanded, readonly: props.readonly, prefix: prefix.value,
}))
const id = computed(() => props.node.id.replaceAll('_', '-'))
const textTag = computed(() => id.value === 'title' ? 'h2'
  : /^(option|question)-title-/.test(id.value) ? 'h3' : 'p')
const evidence = computed(() => id.value.startsWith('evidence-') ? id.value.slice(9) : undefined)
const isExpanded = computed(() => evidence.value ? !!props.expanded?.includes(evidence.value) : undefined)
const isPicked = computed(() => id.value.startsWith('pick-') ? props.selected === id.value.slice(5) : undefined)
const groupTitle = computed(() => {
  if (id.value.startsWith('option-static-')) return 'option-title-' + id.value.slice(14)
  if (id.value === 'question-scope' || id.value === 'question-owner') return 'question-title-' + id.value.slice(9)
  return undefined
})
const highlighted = computed(() => id.value.startsWith('option-static-') && props.selected === id.value.slice(14))
</script>

<template>
  <component :is="node.kind === 'Card' ? Card : 'div'" v-if="node.kind === 'Card' || node.kind === 'Column'"
    :id="prefix + '-' + id" :data-node="id" :data-picked="highlighted || undefined"
    :role="node.kind === 'Card' ? 'region' : groupTitle ? 'group' : undefined"
    :aria-labelledby="node.kind === 'Card' ? prefix + '-title' : groupTitle ? prefix + '-' + groupTitle : undefined">
    <SurfaceNode v-for="child in node.children" :key="child.id" :node="child" v-bind="state" />
  </component>
  <component :is="textTag" v-else-if="node.kind === 'Text'" :id="prefix + '-' + id" :data-node="id"
    :class="{ 'node-caption': node.caption }" :tabindex="id === 'title' ? -1 : undefined">
    <Layers2 v-if="id === 'eyebrow'" aria-hidden="true" />{{ node.text }}
  </component>
  <Button v-else-if="node.kind === 'Button'" type="button" :id="prefix + '-' + id" :data-node="id" :variant="node.variant"
    :disabled="props.readonly && !evidence" :aria-expanded="isExpanded" :aria-pressed="isPicked"
    :aria-controls="isExpanded ? prefix + '-reason-' + evidence : undefined" @click="node.activate()">
    <Check v-if="isPicked" aria-hidden="true" />
    <span>{{ node.label }}</span>
    <ChevronDown v-if="evidence" aria-hidden="true" />
    <ArrowRight v-else-if="id === 'continue' || id === 'compare'" aria-hidden="true" />
  </Button>
  <component :is="node.view" v-else-if="node.kind === 'custom' && (node.children.length || node.slotText !== undefined)" v-bind="node.props">
    <template v-if="node.slotText !== undefined">{{ node.slotText }}</template>
    <SurfaceNode v-for="child in node.children" :key="child.id" :node="child" v-bind="state" />
  </component>
  <component :is="node.view" v-else-if="node.kind === 'custom'" v-bind="node.props" />
</template>
