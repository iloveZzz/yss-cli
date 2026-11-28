<script setup lang="ts">
import { ref, useTemplateRef, watch } from 'vue'

const props = defineProps<{ expression: string; raw: string; display: boolean }>()

const math = useTemplateRef<HTMLSpanElement>('math')
const rendered = ref(false)
const maxExpressionBytes = 4 * 1024
const encoder = new TextEncoder()

watch([() => props.expression, () => props.display, math], async ([expression, display, target], _, onCleanup) => {
  rendered.value = false
  if (!target) return
  target.replaceChildren()
  if (!expression.trim() || expression.length > maxExpressionBytes
      || encoder.encode(expression).length > maxExpressionBytes) return

  let current = true
  onCleanup(() => { current = false })
  try {
    const [{ default: katex }] = await Promise.all([import('katex'), import('katex/dist/katex.min.css')])
    if (!current) return
    katex.render(expression, target, {
      displayMode: display,
      output: 'htmlAndMathml',
      trust: false,
      throwOnError: true,
      strict: 'ignore',
      maxExpand: 256,
      maxSize: 24,
    })
    rendered.value = true
  } catch {
    if (current) target.replaceChildren()
  }
}, { flush: 'post' })
</script>

<template>
  <component :is="display ? 'div' : 'span'" class="assistant-math" :class="{ 'assistant-math-display': display }"
    :role="display && rendered ? 'region' : undefined" :aria-label="display && rendered ? '公式' : undefined"
    :tabindex="display && rendered ? 0 : undefined"
  >
    <span v-show="rendered" ref="math" />
    <span v-if="!rendered" class="assistant-math-source">{{ raw }}</span>
  </component>
</template>

<style scoped>
.assistant-math-display {
  max-width: 100%;
  overflow-x: auto;
  padding-block: 0.25rem;
}
.assistant-math-display :deep(.katex-display) {
  margin: 0;
  min-width: max-content;
}
.assistant-math-source {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
}
</style>
