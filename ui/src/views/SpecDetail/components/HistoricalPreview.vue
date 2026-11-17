<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
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
const maximized = shallowRef(true)
const retry = shallowRef(0)
watch(
  () => [props.confirmationId, target.value, retry.value],
  async (_value, _previous, cleanup) => {
    url.value = ''
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
      if (!controller.signal.aborted) url.value = result.url
    } catch (cause) {
      if (!controller.signal.aborted) error.value = errorMessage(cause)
    } finally {
      if (!controller.signal.aborted) loading.value = false
    }
  }
)
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
      <p v-if="loading" class="preview-message" role="status">正在读取确认版本的静态原型…</p>
      <div v-if="error" class="preview-message">
        <p role="alert" class="notice warning">{{ error }}</p>
        <Button type="button" variant="outline" @click="retry++">重新读取</Button>
      </div>
      <iframe
        v-if="url"
        :src="url"
        :title="`确认版本原型 ${target}`"
        sandbox="allow-scripts allow-same-origin allow-forms allow-modals"
        referrerpolicy="no-referrer"
      />
    </div>
  </ModalDialog>
</template>
<style scoped>
.historical-dialog {
  width: min(1280px, calc(100vw - 48px));
  height: min(860px, calc(100dvh - 48px));
  max-width: none;
  max-height: none;
  padding: 0;
  overflow: hidden;
}
.historical-dialog[open] {
  display: flex;
  flex-direction: column;
}
.historical-dialog.maximized {
  width: 100vw;
  height: 100dvh;
  margin: 0;
  border: 0;
  border-radius: 0;
}
.historical-dialog :deep(.dialog-header) {
  flex-shrink: 0;
  gap: 12px;
  margin: 0;
  padding: 10px 16px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.historical-dialog :deep(.dialog-header h2) {
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
</style>
