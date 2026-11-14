<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
import ModalDialog from '@/components/ModalDialog.vue'
import SourcePreview from './SourcePreview.vue'
import { errorMessage, preparePreview } from '@/services/workbench'
const props = defineProps<{ specId: string; sourcePath: string }>()
const target = defineModel<{ path: string; fragment: string } | null>({ required: true })
const open = computed({ get: () => !!target.value, set: value => { if (!value) target.value = null } })
const markdown = computed(() => /\.(md|markdown)$/i.test(target.value?.path ?? ''))
const url = shallowRef(''); const message = shallowRef(''); const error = shallowRef(''); const loading = shallowRef(false); const retry = shallowRef(0)
watch(() => [props.specId, target.value?.path, retry.value], async (_next, _old, cleanup) => {
  url.value = ''; error.value = ''; message.value = ''
  if (!target.value || markdown.value) return
  const controller = new AbortController(); cleanup(() => controller.abort()); loading.value = true
  try {
    const result = await preparePreview(props.specId, target.value.path, controller.signal)
    if (!controller.signal.aborted) {
      const parsed = new URL(result.url)
      if (parsed.protocol !== 'http:' || parsed.hostname !== '127.0.0.1' || parsed.origin === location.origin || parsed.username || parsed.password) throw new Error('预览地址没有与工作台隔离，已阻止加载。')
      url.value = result.url; message.value = result.message
    }
  } catch (e) { if (!controller.signal.aborted) error.value = errorMessage(e) }
  finally { if (!controller.signal.aborted) loading.value = false }
})
</script>
<template>
  <ModalDialog v-model="open" title="关联内容" class="resource-dialog">
    <template v-if="target">
      <SourcePreview v-if="markdown" :spec-id="specId" :source-path="sourcePath" :document-path="target.path" :fragment="target.fragment" @navigate="(path, fragment) => target = { path, fragment }" />
      <template v-else>
        <p class="path-text">{{ target.path }}</p><p v-if="loading" class="muted">正在准备隔离预览…</p>
        <p v-if="error" role="alert" class="notice warning">{{ error }}<Button variant="outline" :disabled="loading" @click="retry++">重新读取</Button></p>
        <p v-if="message" class="notice">{{ message }}</p>
        <iframe v-if="url" :src="url" :title="`原型预览：${target.path}`" sandbox="allow-scripts allow-same-origin allow-forms allow-modals" referrerpolicy="no-referrer" class="prototype-frame" />
        <p class="preview-note">原型在独立地址运行，不接收工作台会话凭据；不会自动启动开发服务或执行构建。</p>
      </template>
    </template>
  </ModalDialog>
</template>
<style scoped>
.resource-dialog { width: min(1180px, calc(100vw - 32px)); }
.prototype-frame { width: 100%; height: 65vh; min-height: 320px; border: 1px solid var(--yss-colors-border); border-radius: 8px; background: white; }
</style>
