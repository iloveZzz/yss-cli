<script setup lang="ts">
import { Alert } from '@/components/ui/alert'
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { errorMessage } from '@/services/workbench'
import { prepareHistoricalPreview } from '@/services/workbench-platform'
const props = defineProps<{ confirmationId: string }>()
const target = defineModel<string | null>({ required: true })
const open = computed({
  get: () => target.value !== null,
  set: (value) => {
    if (!value) target.value = null
  }
})
const url = shallowRef('')
const error = shallowRef('')
const loading = shallowRef(false)
const frameLoading = shallowRef(false)
const previewLoading = computed(() => loading.value || frameLoading.value)
const maximized = shallowRef(true)
const retry = shallowRef(0)
watch(
  () => [props.confirmationId, target.value, retry.value],
  async (_value, _previous, cleanup) => {
    url.value = ''
    frameLoading.value = false
    error.value = ''
    loading.value = false
    if (!target.value || !props.confirmationId) return
    const controller = new AbortController()
    cleanup(() => controller.abort())
    loading.value = true
    try {
      const result = await prepareHistoricalPreview(
        props.confirmationId,
        target.value,
        controller.signal
      )
      const parsed = new URL(result.url)
      if (
        parsed.protocol !== 'http:' ||
        parsed.hostname !== '127.0.0.1' ||
        parsed.origin === location.origin ||
        parsed.username ||
        parsed.password
      )
        throw new Error('历史预览地址未通过本机隔离校验，请重新读取。')
      if (!controller.signal.aborted) {
        frameLoading.value = true
        url.value = parsed.href
      }
    } catch (cause) {
      if (!controller.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (!controller.signal.aborted) loading.value = false
    }
  }
)

function frameLoaded(event: Event) {
  const frame = event.currentTarget as HTMLIFrameElement
  if (frame.src === url.value) frameLoading.value = false
}
</script>
<template>
  <ModalDialog
    v-model="open"
    :title="`历史原型 · ${target ?? ''}`"
    class="historical-dialog"
    :class="{ maximized }"
  >
    <template #actions>
      <Button type="button" variant="ghost" @click="maximized = !maximized">
        {{ maximized ? '还原窗口' : '最大化窗口' }}
      </Button>
    </template>
    <div class="historical-body">
      <LoadingSkeleton v-if="previewLoading" variant="detail" :rows="4" label="正在读取确认版本的静态原型…" class="preview-message" />
      <div v-if="error" class="preview-message">
        <Alert role="alert" class="notice warning">{{ error }}</Alert>
        <Button type="button" variant="outline" @click="retry++">重新读取</Button>
      </div>
      <iframe
        v-if="url"
        :key="url"
        :src="url"
        :class="{ 'frame-loading': frameLoading }"
        :aria-hidden="frameLoading ? true : undefined"
        :tabindex="frameLoading ? -1 : undefined"
        @load="frameLoaded"
        :title="`确认版本原型 ${target}`"
        sandbox="allow-scripts allow-same-origin allow-forms allow-modals"
        referrerpolicy="no-referrer"
      />
    </div>
  </ModalDialog>
</template>
<style scoped>
:global(.workbench-dialog.historical-dialog) {
  width: min(1280px, calc(100vw - 48px));
  height: min(860px, calc(100dvh - 48px));
  max-width: none;
  max-height: none;
  padding: 0;
  gap: 0;
  overflow: hidden;
  scrollbar-gutter: auto;
}
:global(.workbench-dialog.historical-dialog[data-state="open"]) {
  display: flex;
  flex-direction: column;
}
:global(.workbench-dialog.historical-dialog.maximized) {
  width: 100vw;
  height: 100dvh;
  margin: 0;
  border: 0;
  border-radius: 0;
}
:global(.workbench-dialog.historical-dialog .dialog-header) {
  flex-shrink: 0;
  gap: 12px;
  margin: 0;
  padding: 10px 16px;
  border-bottom: 1px solid var(--yss-colors-border);
}
:global(.workbench-dialog.historical-dialog .dialog-header h2) {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15px;
}
.historical-body {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
  background: #fff;
}
.historical-body iframe {
  display: block;
  flex: 1;
  min-height: 0;
  width: 100%;
  height: 100%;
  border: 0;
  background: #fff;
}
.preview-message {
  padding: 24px;
}
.frame-loading {
  position: absolute;
  visibility: hidden;
  pointer-events: none;
}
</style>
