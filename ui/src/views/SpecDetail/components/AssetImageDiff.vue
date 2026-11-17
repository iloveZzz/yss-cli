<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { ApiError, errorMessage } from '@/services/workbench'
import { currentSpecFile, historicalFile, type ArtifactChange } from '@/services/workbench-platform'
const props = defineProps<{ specId: string; confirmationId: string; file: ArtifactChange }>()
type Side = 'before' | 'after'
interface ImageState {
  url: string
  loading: boolean
  error: string
}
const before = shallowRef<ImageState>({ url: '', loading: false, error: '' })
const after = shallowRef<ImageState>({ url: '', loading: false, error: '' })
const visible = shallowRef(false)
const loading = shallowRef(false)
const id = useId()
let controller: AbortController | undefined
let generation = 0
const sides = computed(() => {
  const entries: { key: Side; label: string; state: ImageState; size: number }[] = []
  if (props.file.before)
    entries.push({
      key: 'before',
      label: '确认版本',
      state: before.value,
      size: props.file.before.size
    })
  if (props.file.after)
    entries.push({
      key: 'after',
      label: '当前版本',
      state: after.value,
      size: props.file.after.size
    })
  return entries
})
function reset() {
  generation++
  controller?.abort()
  controller = undefined
  for (const state of [before, after]) {
    if (state.value.url) URL.revokeObjectURL(state.value.url)
    state.value = { url: '', loading: false, error: '' }
  }
  visible.value = false
  loading.value = false
}
watch(
  () => [
    props.specId,
    props.confirmationId,
    props.file.path,
    props.file.before?.sha256,
    props.file.after?.sha256
  ],
  reset,
  { flush: 'sync' }
)
async function load() {
  reset()
  const own = generation
  const connection = new AbortController()
  controller = connection
  const specId = props.specId,
    confirmationId = props.confirmationId,
    path = props.file.path
  const previous = props.file.before,
    current = props.file.after
  visible.value = true
  loading.value = true
  async function read(side: Side) {
    const state = side === 'before' ? before : after
    const file = side === 'before' ? previous : current
    if (!file) return
    state.value = { url: '', loading: true, error: '' }
    try {
      const blob =
        side === 'before'
          ? await historicalFile(confirmationId, path, connection.signal)
          : await currentSpecFile(specId, path, file.sha256, connection.signal)
      if (own !== generation || connection.signal.aborted) return
      state.value = { url: URL.createObjectURL(blob), loading: false, error: '' }
    } catch (cause) {
      if (own !== generation || connection.signal.aborted) return
      state.value = {
        url: '',
        loading: false,
        error:
          cause instanceof ApiError && cause.status === 409
            ? '当前图片已变化，请重新比较文件后加载。'
            : errorMessage(cause)
      }
    }
  }
  await Promise.all([read('before'), read('after')])
  if (own === generation) loading.value = false
}
function imageFailed(side: Side, url: string) {
  const state = side === 'before' ? before : after
  if (state.value.url !== url) return
  URL.revokeObjectURL(url)
  state.value = { url: '', loading: false, error: '图片无法解码，可下载该版本文件核对。' }
}
onBeforeUnmount(reset)
</script>
<template>
  <section class="asset-preview" aria-label="图片版本对照">
    <div class="inline-actions">
      <Button
        type="button"
        variant="outline"
        :disabled="loading"
        :aria-expanded="visible"
        :aria-controls="id"
        @click="load"
      >
        {{ loading ? '正在加载图片…' : visible ? '重新加载图片' : '加载图片预览' }}
      </Button>
      <Button v-if="visible" type="button" variant="ghost" @click="reset">收起预览</Button>
    </div>
    <div v-if="visible" :id="id" class="asset-image-grid">
      <figure v-for="side in sides" :key="side.key">
        <figcaption>
          <strong>{{ side.label }}</strong>
          <span class="muted">{{ side.size }} 字节</span>
        </figcaption>
        <div class="asset-image-frame">
          <p v-if="side.state.loading" role="status" class="muted">正在读取图片…</p>
          <p v-else-if="side.state.error" role="alert" class="warning-text">
            {{ side.state.error }}
          </p>
          <img
            v-else-if="side.state.url"
            :key="side.state.url"
            :src="side.state.url"
            :alt="`${side.label} · ${file.path}`"
            decoding="async"
            @error="imageFailed(side.key, side.state.url)"
          />
        </div>
      </figure>
    </div>
  </section>
</template>
<style scoped>
.asset-preview {
  margin: 16px 0;
}
.asset-image-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 16px;
  margin-top: 16px;
}
.asset-image-grid figure {
  min-width: 0;
  margin: 0;
}
.asset-image-grid figcaption {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
  font-size: 12px;
}
.asset-image-frame {
  display: grid;
  place-items: center;
  min-height: 220px;
  padding: 16px;
  background: #f7f8fa;
  border: 1px solid var(--yss-colors-border);
  border-radius: 8px;
  overflow: hidden;
}
.asset-image-frame img {
  display: block;
  max-width: 100%;
  max-height: 420px;
  object-fit: contain;
}
.asset-image-frame p {
  overflow-wrap: anywhere;
  font-size: 12px;
}
@media (width<720px) {
  .asset-image-grid {
    grid-template-columns: 1fr;
  }
  .asset-image-frame {
    min-height: 180px;
  }
}
</style>
