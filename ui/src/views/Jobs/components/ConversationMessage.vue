<script setup lang="ts">
import { computed } from 'vue'
import { Message, MessageContent, MessageHeader } from '@/components/ui/message'
import { dateLabel } from '@/services/workbench'
import { renderMarkdown } from '../../SpecDetail/schemas/markdown'

const props = defineProps<{
  text: string
  user?: boolean
  streaming?: boolean
  createdAt?: string
  rowId?: number
}>()

const html = computed(() =>
  props.user ? '' : renderMarkdown(props.text, {
    documentLinks: false,
    hideSpecMetadata: false,
    headingPrefix: `pi-message-${props.rowId}-`
  })
)
</script>

<template>
  <Message as="article" :align="user ? 'end' : 'start'" :aria-busy="streaming || undefined">
    <div class="conversation-message" :class="{ 'conversation-user': user }">
      <MessageHeader class="conversation-message-label">
        <strong>{{ user ? '你' : 'Pi' }}</strong>
        <time v-if="createdAt" :datetime="createdAt" :title="dateLabel(createdAt)">
          {{ dateLabel(createdAt).split(' ').at(-1) }}
        </time>
      </MessageHeader>
      <MessageContent class="conversation-message-body">
        <p v-if="user" class="preserve-lines">{{ text }}</p>
        <!-- MarkdownIt: raw HTML and image loads are disabled. -->
        <div v-else class="source-markdown" v-html="html" />
        <span v-if="streaming" class="muted conversation-streaming">正在生成…</span>
      </MessageContent>
    </div>
  </Message>
</template>

<style scoped>
.conversation-message {
  width: min(100%, 760px);
  min-width: 0;
}
.conversation-user {
  width: fit-content;
  max-width: min(90%, 760px);
}
.conversation-message-label {
  margin-bottom: 5px;
  gap: 8px;
  font-size: 12px;
  color: var(--brand-color-text-secondary);
}
.conversation-user .conversation-message-label {
  justify-content: flex-end;
}
.conversation-message-body {
  padding: 10px 14px;
  border-radius: 12px;
  border: 1px solid var(--yss-colors-border);
  background: var(--brand-color-bg-container);
  font-size: 13px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.conversation-user .conversation-message-body {
  background: var(--yss-colors-primary-bg);
  border-color: transparent;
}
.conversation-message-body :deep(p) {
  margin: 0;
}
.conversation-message-body :deep(.source-markdown) {
  padding: 0;
  max-width: none;
  overflow: auto;
}
.conversation-message-body :deep(.source-markdown > * + *) {
  margin-top: 0.6em;
}
.conversation-message-body :deep(pre) {
  overflow: auto;
}
.conversation-streaming {
  display: inline-block;
  font-size: 12px;
  margin-top: 4px;
}
</style>
