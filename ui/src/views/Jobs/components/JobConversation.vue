<script setup lang="ts">
import { nextTick, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport
} from '@/components/ui/message-scroller'
import type { ConversationPage, JobActivity } from '@/services/workbench-platform'
import ConversationMessage from './ConversationMessage.vue'
import ConversationQuestion from './ConversationQuestion.vue'
import ConversationTool from './ConversationTool.vue'

const props = defineProps<{
  jobId: string
  page?: ConversationPage
  items: JobActivity[]
  terminal: boolean
  status: string
  error: string
  hasOlder: boolean
  loadingHistory: boolean
  historyError: string
  busy: boolean
  submit: (requestId: string, value: unknown) => Promise<string | void>
}>()
const emit = defineEmits<{ retry: []; older: [] }>()
const section = useTemplateRef<HTMLElement>('section')

watch(() => props.hasOlder, async (hasOlder) => {
  if (hasOlder || document.activeElement !== section.value?.querySelector('[data-load-older]')) return
  await nextTick()
  section.value?.querySelector<HTMLElement>('[data-conversation-viewport]')?.focus({ preventScroll: true })
})

async function focusQuestion(id: number) {
  await nextTick()
  const card = section.value?.querySelector<HTMLElement>(`#pi-question-${id}`)
  card?.focus({ preventScroll: true })
  card?.scrollIntoView({ block: 'nearest' })
}

defineExpose({ focusQuestion })
</script>

<template>
  <section ref="section" class="platform-section job-conversation" aria-label="本轮会话">
    <div class="conversation-toolbar">
      <div>
        <span class="muted" role="status">{{ status }}</span>
        <p v-if="page && !page.prompt" class="muted">本轮未留存原始要求</p>
      </div>
      <Button
        v-if="hasOlder"
        data-load-older
        variant="outline"
        type="button"
        :aria-disabled="loadingHistory"
        @click="!loadingHistory && emit('older')"
      >{{ loadingHistory ? '正在读取…' : '加载更早记录' }}</Button>
    </div>
    <details v-if="page?.prompt && hasOlder" class="conversation-requirement">
      <summary>本轮要求</summary>
      <div class="original-prompt">
        <ConversationMessage :text="page.prompt" user />
      </div>
    </details>
    <p v-if="error || historyError" role="alert" class="notice warning">
      {{ error || historyError }}
      <Button v-if="error" variant="ghost" @click="emit('retry')">重新连接</Button>
    </p>
    <MessageScrollerProvider v-if="page" auto-scroll default-scroll-position="end" :scroll-edge-threshold="20">
      <MessageScroller class="conversation-scroller">
        <MessageScrollerViewport
          data-conversation-viewport
          :preserve-scroll-on-prepend="true"
          aria-label="会话记录"
          role="region"
          tabindex="0"
        >
          <MessageScrollerContent class="conversation-scroll-content">
            <MessageScrollerItem v-if="page.prompt && !hasOlder" key="prompt" message-id="prompt" class="conversation-entry">
              <ConversationMessage :text="page.prompt" user />
            </MessageScrollerItem>
            <MessageScrollerItem v-for="row in items" :key="row.id" :message-id="String(row.id)" class="conversation-entry">
              <ConversationMessage
                v-if="row.kind === 'message' || row.kind === 'user'"
                :text="row.text"
                :user="row.kind === 'user'"
                :created-at="row.createdAt"
                :row-id="row.id"
                :streaming="row.kind === 'message' && !row.status && !terminal"
              />
              <ConversationQuestion
                v-else-if="row.question"
                :row="row"
                :question="row.question"
                :terminal="terminal"
                :busy="busy"
                :submit="submit"
              />
              <ConversationTool
                v-else-if="row.kind === 'tool' || row.kind === 'command' || row.kind === 'files'"
                :job-id="jobId"
                :row="row"
                :terminal="terminal"
              />
              <p v-else-if="row.kind === 'error'" class="notice error preserve-lines">{{ row.text }}</p>
            </MessageScrollerItem>
            <p v-if="!items.length" class="conversation-empty muted">
              {{ terminal ? '本轮暂无其他会话记录' : '等待 Pi 开始答复…' }}
            </p>
          </MessageScrollerContent>
        </MessageScrollerViewport>
        <MessageScrollerButton size="sm" variant="outline" behavior="auto" class="conversation-latest">
          回到最新
        </MessageScrollerButton>
      </MessageScroller>
    </MessageScrollerProvider>
    <p v-else class="conversation-empty muted">正在读取本轮会话…</p>
  </section>
</template>

<style scoped>
.job-conversation {
  padding: 0;
  overflow: hidden;
}
.conversation-toolbar {
  min-height: 48px;
  padding: 6px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.conversation-toolbar button[aria-disabled='true'] {
  opacity: 0.65;
}
.conversation-requirement {
  padding: 0 16px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.conversation-requirement summary {
  min-height: var(--yss-control-min-height);
  padding-block: 8px;
  color: var(--brand-color-primary);
}
.original-prompt {
  max-height: min(32vh, 20rem);
  overflow-y: auto;
  padding-block: 4px 12px;
}
.job-conversation > .notice {
  margin: 12px 16px 0;
}
.conversation-scroller {
  height: clamp(250px, 52vh, 650px);
}
.conversation-scroll-content {
  max-width: 1024px;
  margin: 0 auto;
  padding: 16px 20px 52px;
  gap: 14px;
}
.conversation-entry {
  content-visibility: visible;
}
.conversation-empty {
  padding: 12px 0;
  text-align: center;
}
.conversation-latest {
  box-shadow: 0 2px 6px #171a2015;
}
@media (width < 720px) {
  .conversation-scroll-content {
    padding-inline: 12px;
  }
}
</style>
