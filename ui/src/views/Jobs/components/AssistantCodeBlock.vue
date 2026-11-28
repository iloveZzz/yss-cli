<script lang="ts">
import type { HighlighterCore, ThemedToken } from 'shiki/core'

const grammars = {
  bash: () => import('shiki/langs/bash.mjs'),
  css: () => import('shiki/langs/css.mjs'),
  diff: () => import('shiki/langs/diff.mjs'),
  go: () => import('shiki/langs/go.mjs'),
  html: () => import('shiki/langs/html.mjs'),
  java: () => import('shiki/langs/java.mjs'),
  javascript: () => import('shiki/langs/javascript.mjs'),
  json: () => import('shiki/langs/json.mjs'),
  jsx: () => import('shiki/langs/jsx.mjs'),
  python: () => import('shiki/langs/python.mjs'),
  rust: () => import('shiki/langs/rust.mjs'),
  sql: () => import('shiki/langs/sql.mjs'),
  toml: () => import('shiki/langs/toml.mjs'),
  tsx: () => import('shiki/langs/tsx.mjs'),
  typescript: () => import('shiki/langs/typescript.mjs'),
  xml: () => import('shiki/langs/xml.mjs'),
  yaml: () => import('shiki/langs/yaml.mjs')
}
type CodeLanguage = keyof typeof grammars
const aliases = new Map<string, CodeLanguage>([
  ['js', 'javascript'], ['py', 'python'], ['rs', 'rust'], ['sh', 'bash'],
  ['shell', 'bash'], ['ts', 'typescript'], ['yml', 'yaml'], ['zsh', 'bash']
])
const theme = 'github-light'
let highlighter: Promise<HighlighterCore> | undefined
const loadedGrammars = new Map<CodeLanguage, Promise<HighlighterCore>>()

function loadGrammar(language: CodeLanguage) {
  highlighter ??= Promise.all([import('shiki/core'), import('shiki/engine/javascript')])
    .then(([shiki, engine]) => shiki.createHighlighterCore({
      engine: engine.createJavaScriptRegexEngine(),
      langs: [],
      themes: [import('shiki/themes/github-light.mjs')]
    }))

  const loaded = loadedGrammars.get(language)
  if (loaded) return loaded
  const pending = Promise.all([highlighter, grammars[language]()]).then(async ([instance, grammar]) => {
    await instance.loadLanguage(grammar.default)
    return instance
  })
  loadedGrammars.set(language, pending)
  return pending
}
</script>

<script setup lang="ts">
import { Check, Code, Copy, Workflow } from '@lucide/vue'
import { computed, onBeforeUnmount, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { renderMermaid } from '@/lib/renderMermaid'

const props = defineProps<{ code: string; language?: string; diagram?: boolean }>()
const syntax = computed(() => {
  const name = props.language?.toLowerCase() ?? ''
  return Object.hasOwn(grammars, name) ? name as CodeLanguage : aliases.get(name)
})
const label = computed(() => props.diagram ? 'Mermaid' : props.language || '纯文本')
const sourceId = useId()
const diagramId = useId()
const source = useTemplateRef<HTMLPreElement>('source')
const highlighted = shallowRef<ThemedToken[][]>()
const svg = shallowRef('')
const diagramFailed = shallowRef(false)
const sourceExpanded = shallowRef(false)
const copyStatus = shallowRef<'idle' | 'copied' | 'failed'>('idle')
const sourceVisible = computed(() => !svg.value || sourceExpanded.value)
const copyLabel = computed(() => {
  if (copyStatus.value === 'copied') return '已复制'
  if (copyStatus.value === 'failed') return '重试复制'
  return '复制'
})
let copyAttempt = 0
let copyTimeout: ReturnType<typeof setTimeout> | undefined

function resetCopy() {
  copyAttempt++
  clearTimeout(copyTimeout)
  copyStatus.value = 'idle'
}

onBeforeUnmount(resetCopy)
watch(() => props.code, resetCopy)

async function copySource() {
  resetCopy()
  const attempt = copyAttempt
  try {
    await navigator.clipboard.writeText(props.code)
    if (attempt !== copyAttempt) return
    copyStatus.value = 'copied'
    copyTimeout = setTimeout(resetCopy, 2400)
  } catch {
    if (attempt !== copyAttempt) return
    if (props.diagram) sourceExpanded.value = true
    copyStatus.value = 'failed'
  }
}

watch(
  [() => props.code, syntax, () => props.diagram],
  async ([code, language, diagram], _previous, cleanup) => {
    highlighted.value = undefined
    svg.value = ''
    diagramFailed.value = false
    sourceExpanded.value = false
    const controller = new AbortController()
    cleanup(() => controller.abort())
    if (diagram) {
      try {
        const rendered = await renderMermaid(code, controller.signal, { allowEmbeddedResources: false })
        if (controller.signal.aborted) return
        const element = source.value
        if (element) {
          const selection = element.ownerDocument.getSelection()
          if (element.contains(element.ownerDocument.activeElement)
              || (selection && !selection.isCollapsed
                && (element.contains(selection.anchorNode) || element.contains(selection.focusNode)))) {
            sourceExpanded.value = true
          }
        }
        svg.value = rendered
      } catch {
        if (!controller.signal.aborted) diagramFailed.value = true
      }
      return
    }
    if (!language || !code || code.length > 32 * 1024) return
    try {
      const instance = await loadGrammar(language)
      if (controller.signal.aborted) return
      highlighted.value = instance.codeToTokensBase(code, {
        lang: language,
        theme,
        tokenizeMaxLineLength: 1000,
        tokenizeTimeLimit: 25
      })
    } catch {
      // Keep the original source visible if the grammar or engine is unavailable.
    }
  },
  { immediate: true }
)
</script>

<template>
  <div class="assistant-code-block">
    <div class="code-toolbar">
      <div class="code-meta">
        <span v-if="copyStatus === 'failed'" class="code-recovery">
          {{ sourceVisible ? '选中文本后手动复制' : '查看源码后手动复制' }}
        </span>
        <span v-else class="code-language" :title="label">{{ label }}</span>
      </div>
      <div class="code-actions">
        <Button
          v-if="svg"
          type="button"
          size="sm"
          variant="ghost"
          :aria-controls="diagramId + ' ' + sourceId"
          @click="sourceExpanded = !sourceExpanded"
        >
          <Workflow v-if="sourceExpanded" aria-hidden="true" />
          <Code v-else aria-hidden="true" />
          {{ sourceExpanded ? '查看图表' : '查看源码' }}
        </Button>
        <Button
          class="code-copy"
          type="button"
          size="sm"
          variant="ghost"
          :aria-label="copyStatus === 'failed' ? '重试复制源码' : '复制源码'"
          :data-copy-status="copyStatus"
          :disabled="!code"
          @click="copySource"
        >
          <Check v-if="copyStatus === 'copied'" aria-hidden="true" />
          <Copy v-else aria-hidden="true" />
          {{ copyLabel }}
        </Button>
      </div>
    </div>
    <span class="sr-only" role="status">{{
      copyStatus === 'copied' ? '已复制源码' : copyStatus === 'failed' ? '复制失败，请选中源码文本后手动复制。' : ''
    }}</span>
    <p v-if="diagramFailed" class="code-notice" role="alert">图表无法预览，已显示源码。</p>
    <p v-else-if="diagram && !svg" class="code-notice" role="status">正在生成图表…</p>
    <!-- The shared strict Mermaid renderer sanitizes SVG and strips embedded media and navigation. -->
    <div v-if="svg" v-show="!sourceVisible" :id="diagramId" class="diagram-viewport" role="region"
      aria-label="Mermaid 图表" tabindex="0" v-html="svg"
    />
    <pre v-show="sourceVisible" :id="sourceId" ref="source"
      class="code-viewport" role="region" aria-label="源码" tabindex="0"
    ><code v-if="highlighted"><template v-for="(line, index) in highlighted" :key="index">{{ index > 0 ? '\n' : '' }}<span
      v-for="token in line" :key="token.offset" :style="{ color: token.color }"
    >{{ token.content }}</span></template></code><code v-else>{{ code }}</code></pre>
  </div>
</template>

<style scoped>
.assistant-code-block {
  min-inline-size: 0;
  max-inline-size: 100%;
  container-type: inline-size;
  overflow: clip;
  border-radius: var(--brand-border-radius);
  background: var(--brand-color-bg-layout);
}
.code-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0 8px;
  min-block-size: 32px;
  padding-inline: 12px 5px;
  border-block-end: 1px solid var(--yss-colors-border);
}
.code-meta {
  flex: 1 1 4.5rem;
  min-inline-size: 0;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  font-weight: 600;
  line-height: 1.4;
}
.code-language {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.code-recovery {
  font-weight: 500;
  overflow-wrap: anywhere;
}
.code-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  max-inline-size: 100%;
  margin-inline-start: auto;
  gap: 2px;
}
.code-actions button {
  min-block-size: 30px;
  padding-inline: 7px;
  font-size: 12px;
  color: var(--brand-color-text-secondary);
  transition: none;
}
.code-actions button:is(:hover, :focus-visible) {
  color: var(--brand-color-text);
}
.code-actions .code-copy {
  min-inline-size: 5.75rem;
  justify-content: flex-start;
}
.code-actions .code-copy[data-copy-status='copied'] {
  color: var(--brand-color-text);
}
.code-actions button:focus-visible,
.diagram-viewport:focus-visible,
.assistant-code-block .code-viewport:focus-visible {
  outline: 2px solid var(--yss-color-primary-control);
  outline-offset: -2px;
}
.code-actions button:focus-visible { box-shadow: none; }
.assistant-code-block .code-viewport,
.diagram-viewport {
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
}
.assistant-code-block .code-viewport {
  background: transparent;
  color: var(--brand-color-text);
  font: 12px/1.7 ui-monospace, SFMono-Regular, Consolas, monospace;
  overflow-wrap: normal;
  tab-size: 4;
  white-space: pre;
}
.diagram-viewport {
  max-block-size: 28rem;
  background: var(--brand-color-bg-container);
}
.diagram-viewport :deep(svg) {
  display: block;
  height: auto;
  margin-inline: auto;
}
.code-notice {
  padding: 8px 12px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
}
.code-notice + .code-viewport { padding-block-start: 2px; }
@container (width < 480px) {
  .assistant-code-block .code-viewport {
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
  .code-actions button { min-block-size: 44px; }
}
@media (width < 720px), (pointer: coarse) {
  .code-actions button { min-block-size: 44px; }
}
@media (forced-colors: active) {
  .assistant-code-block { border: 1px solid CanvasText; }
  .code-actions button:focus-visible,
  .diagram-viewport:focus-visible,
  .assistant-code-block .code-viewport:focus-visible { outline-color: Highlight; }
}
</style>
