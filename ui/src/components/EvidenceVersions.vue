<script setup lang="ts">
import { computed, useId } from 'vue'

type EvidenceExcerpt = {
  digest: string
  startLine: number
  endLine: number
  snippet: string
}

const props = defineProps<{
  retained: EvidenceExcerpt & { snippetTruncated?: boolean }
  retainedVersionLabel?: string
  current?:
    | { status: 'loading' }
    | { status: 'unavailable' }
    | { status: 'unlocated', digest: string }
    | ({ status: 'current' } & EvidenceExcerpt)
}>()

const headingId = useId()
const retainedHeadingId = `${headingId}-retained`
const currentHeadingId = `${headingId}-current`
const statusText = computed(() => {
  const { retained, current } = props
  if (!current) return ''
  if (current.status === 'loading') return '正在读取当前来源。'
  if (current.status === 'unavailable') return '当前来源不可用，仍可查看留存片段。'
  if (current.status === 'unlocated') return '当前来源无法按留存行号定位，原文位置可能已移动。'
  if (current.digest === retained.digest) return '当前文件摘要与留存版本一致。'

  const positionNote = '原文位置可能已移动。'
  if (current.startLine !== retained.startLine || current.endLine !== retained.endLine) {
    return `文件摘要已变更，当前行号范围与留存范围不同。${positionNote}`
  }
  if (retained.snippetTruncated && current.snippet.startsWith(retained.snippet)) {
    return `文件摘要已变更，按原行号对照，已留存的前缀一致。${positionNote}`
  }
  if (current.snippet === retained.snippet) {
    return `文件摘要已变更，按原行号对照，片段内容未变更。${positionNote}`
  }
  return `文件摘要已变更，按原行号对照，片段内容也已变更。${positionNote}`
})

function lineLabel({ startLine, endLine }: EvidenceExcerpt) {
  return startLine === endLine ? `第 ${startLine} 行` : `第 ${startLine}–${endLine} 行`
}
</script>

<template>
  <section class="min-w-0" :aria-labelledby="headingId">
    <h3 :id="headingId" class="font-medium">原文证据</h3>
    <p class="text-muted-foreground" aria-live="polite" aria-atomic="true">{{ statusText }}</p>

    <div class="evidence-versions">
      <div class="evidence-version">
        <h4 :id="retainedHeadingId" class="font-medium">引用时留存</h4>
        <p class="evidence-meta">
          {{ lineLabel(retained) }} · {{ retainedVersionLabel ?? '文件摘要' }}
          <code :title="retained.digest">{{ retained.digest.slice(0, 10) }}</code>
        </p>
        <pre class="evidence-snippet" role="region" :aria-labelledby="retainedHeadingId" tabindex="0">{{ retained.snippet }}</pre>
        <p v-if="retained.snippetTruncated" class="text-muted-foreground">留存片段已截断。</p>
      </div>

      <div v-if="current?.status === 'current' || current?.status === 'unlocated'" class="evidence-version">
        <h4 :id="currentHeadingId" class="font-medium">当前来源</h4>
        <p class="evidence-meta">
          <template v-if="current.status === 'current'">{{ lineLabel(current) }} · </template>
          文件摘要 <code :title="current.digest">{{ current.digest.slice(0, 10) }}</code>
        </p>
        <pre v-if="current.status === 'current'" class="evidence-snippet" role="region"
          :aria-labelledby="currentHeadingId" tabindex="0">{{ current.snippet }}</pre>
      </div>
    </div>
  </section>
</template>

<style scoped>
.evidence-versions {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 18rem), 1fr));
  gap: 12px;
  margin-block-start: 8px;
}

.evidence-version {
  display: grid;
  align-content: start;
  gap: 4px;
  min-width: 0;
}

.evidence-meta {
  color: var(--muted-foreground);
  overflow-wrap: anywhere;
}

.evidence-snippet {
  max-block-size: 14rem;
  overflow: auto;
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--muted);
  font-size: 12px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
