<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
import SourcePreview from './SourcePreview.vue'
import { prepareAgentPreview } from '@/services/workbench-agent'
import { errorMessage, preparePreview } from '@/services/workbench'
const props = defineProps<{
  specId: string
  sourcePath: string
  label?: string
  runId?: string
  refreshKey?: string | number | null
}>()
const target = defineModel<{ path: string; fragment: string } | null>({ required: true })
const open = computed({
  get: () => !!target.value,
  set: (value) => {
    if (!value) target.value = null
  }
})
const markdown = computed(() => /\.(md|markdown)$/i.test(target.value?.path ?? ''))
const title = computed(() => props.label || target.value?.path.split('/').at(-1) || '关联内容')
const maximized = shallowRef(true)
const windowAction = computed(() => (maximized.value ? '还原窗口' : '最大化窗口'))
watch(open, (value) => {
  if (value) maximized.value = true
})
const url = shallowRef('')
const error = shallowRef('')
const loading = shallowRef(false)
const retry = shallowRef(0)
watch(
  () => [props.specId, props.runId, props.refreshKey, target.value?.path, retry.value],
  async (_next, _old, cleanup) => {
    url.value = ''
    error.value = ''
    loading.value = false
    if (!target.value || markdown.value) return
    const controller = new AbortController()
    cleanup(() => controller.abort())
    loading.value = true
    try {
      const result = props.runId
        ? await prepareAgentPreview(props.runId, props.specId, target.value.path, controller.signal)
        : await preparePreview(props.specId, target.value.path, controller.signal)
      if (!controller.signal.aborted) {
        const parsed = new URL(result.url)
        if (
          parsed.protocol !== 'http:' ||
          parsed.hostname !== '127.0.0.1' ||
          parsed.origin === location.origin ||
          parsed.username ||
          parsed.password
        )
          throw new Error('预览地址没有与工作台隔离，已阻止加载。')
        url.value = result.url
      }
    } catch (e) {
      if (!controller.signal.aborted) error.value = errorMessage(e)
    } finally {
      if (!controller.signal.aborted) loading.value = false
    }
  }
)
</script>
<template>
  <ModalDialog v-model="open" :title="title" class="resource-dialog" :class="{ maximized }">
    <template #actions>
      <Button
        type="button"
        variant="ghost"
        :aria-label="windowAction"
        :title="windowAction"
        @click="maximized = !maximized"
      >
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          aria-hidden="true"
        >
          <template v-if="maximized">
            <path d="M8 8V4h12v12h-4" />
            <rect x="4" y="8" width="12" height="12" rx="1" />
          </template>
          <rect v-else x="4" y="4" width="16" height="16" rx="1" />
        </svg>
      </Button>
    </template>
    <div v-if="target" class="resource-body" :class="{ 'prototype-body': !markdown && !!url }">
      <SourcePreview
        v-if="markdown"
        embedded
        :spec-id="specId"
        :source-path="sourcePath"
        :run-id="runId"
        :refresh-key="refreshKey"
        :document-path="target.path"
        :fragment="target.fragment"
        @navigate="(path, fragment) => (target = { path, fragment })"
        @position="
          (fragment) => {
            if (target) target = { ...target, fragment }
          }
        "
      />
      <template v-else>
        <p v-if="loading" class="preview-status muted" role="status">正在加载原型…</p>
        <div v-if="error" class="preview-status">
          <p role="alert" class="notice warning">
            {{ error }}
            <Button variant="outline" :disabled="loading" @click="retry++">重新读取</Button>
          </p>
        </div>
        <iframe
          v-if="url"
          :src="url"
          :title="`原型预览：${target.path}`"
          sandbox="allow-scripts allow-same-origin allow-forms allow-modals"
          referrerpolicy="no-referrer"
          class="prototype-frame"
        />
      </template>
    </div>
  </ModalDialog>
</template>
<style scoped>
.resource-dialog {
  width: min(1280px, calc(100vw - 64px));
  height: min(860px, calc(100dvh - 64px));
  max-width: none;
  max-height: none;
  padding: 0;
  overflow: hidden;
}
.resource-dialog[open] {
  display: flex;
  flex-direction: column;
}
.resource-dialog.maximized {
  width: 100vw;
  height: 100dvh;
  margin: 0;
  border: 0;
  border-radius: 0;
}
.resource-dialog:modal {
  overscroll-behavior: contain;
}
.resource-dialog::backdrop {
  overflow: hidden;
  overscroll-behavior: contain;
}
.resource-dialog :deep(.dialog-header) {
  flex-shrink: 0;
  gap: 12px;
  margin: 0;
  padding: 10px 16px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.resource-dialog :deep(.dialog-header h2) {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15px;
}
.resource-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
  background: white;
}
.prototype-body {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.preview-status {
  padding: 24px;
}
.prototype-frame {
  display: block;
  flex: 1;
  width: 100%;
  height: 100%;
  min-height: 0;
  border: 0;
  background: white;
}
@media (width < 720px) {
  .resource-dialog:not(.maximized) {
    width: calc(100vw - 24px);
    height: calc(100dvh - 32px);
  }
}
</style>
