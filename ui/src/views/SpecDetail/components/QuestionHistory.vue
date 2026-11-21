<script setup lang="ts">
import type { QuestionHistoryEntry } from '@/services/workbench'
import { responseText } from '@/services/workbench-content'

withDefaults(defineProps<{ entries: QuestionHistoryEntry[]; title?: string; showQuestionId?: boolean }>(), {
  title: '历史记录'
})
</script>
<template>
  <section v-if="entries.length" class="question-history" :aria-label="title">
    <header><strong>{{ title }}</strong><span>仅供复查</span></header>
    <article v-for="(entry, index) in entries" :key="entry.response.id || index">
      <p class="history-meta">
        <template v-if="showQuestionId">{{ entry.response.questionId || '问题 ID 未知' }} · </template>
        {{ entry.response.kind === 'defer' ? '延期记录' : '答复记录' }} ·
        {{ entry.response.id || 'ID 未知' }} · {{ entry.reason }}
      </p>
      <p class="history-meta">原问题摘要 · {{ entry.response.questionDigest || '未知' }}</p>
      <p class="preserve-lines">{{ responseText(entry.response) }}</p>
      <p v-if="entry.sourceResolution" class="preserve-lines">原规格回写记录 · {{ entry.sourceResolution }}</p>
    </article>
  </section>
</template>
<style scoped>
.question-history {
  min-width: 0;
  padding: 12px 14px;
  border: 1px solid #ecddbe;
  border-radius: 8px;
  background: #fff9ed;
  overflow-wrap: anywhere;
}
.question-history header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}
.question-history header span { color: #80540f; font-size: 12px; }
.question-history article + article { border-top: 1px solid #ecddbe; margin-top: 9px; padding-top: 9px; }
.question-history .history-meta { font-size: 12px; color: #62666c; margin: 0; }
.question-history .preserve-lines { margin: 5px 0 0; }
</style>
