<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import ModalDialog from '@/components/ModalDialog.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { getPiModels } from '@/services/workbench-agent'
import { errorMessage } from '@/services/workbench'
import type { PiModel } from '@/services/workbench-platform'

const props = defineProps<{ selectedModel?: PiModel; disabled?: boolean }>()
const emit = defineEmits<{ select: [model?: PiModel] }>()

const open = shallowRef(false)
const loading = shallowRef(false)
const query = shallowRef('')
const models = shallowRef<PiModel[]>([])
const error = shallowRef('')
const filteredModels = computed(() => {
  const terms = query.value.toLowerCase().split(/[\s/]+/u).filter(Boolean)
  return models.value.filter(({ provider, id }) => {
    const text = `${provider}/${id}`.toLowerCase()
    return terms.every((term) => text.includes(term))
  })
})
let request: AbortController | undefined

async function readModels() {
  if (request) return
  const current = new AbortController()
  request = current
  loading.value = true
  error.value = ''
  models.value = []
  try {
    const result = await getPiModels(current.signal)
    if (!current.signal.aborted) models.value = result.models
  } catch (cause) {
    if (!current.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (request === current) {
      request = undefined
      loading.value = false
    }
  }
}

function choose(model?: PiModel) {
  open.value = false
  emit('select', model)
}

watch(open, (isOpen) => {
  if (isOpen) {
    query.value = ''
    void readModels()
  } else {
    request?.abort()
    request = undefined
    loading.value = false
  }
}, { flush: 'sync' })
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <Button type="button" variant="outline" size="sm" aria-haspopup="dialog" :disabled="disabled" @click="open = true">
    选择本次模型
  </Button>

  <ModalDialog v-model="open" title="选择 Pi 模型">
    <div class="model-picker-content">
      <p class="muted">从本机 Pi 加载。选择仅用于本次运行，远端权限可通过「测试模型连接」确认。</p>
      <Input v-model="query" type="search" aria-label="搜索供应商或模型" placeholder="搜索供应商或模型" autofocus />
      <LoadingSkeleton v-if="loading" :rows="3" label="正在读取本机 Pi 模型" />
      <div v-else-if="error" class="model-list-feedback">
        <p role="alert" class="notice warning">{{ error }}</p>
        <Button type="button" variant="outline" size="sm" @click="readModels">重试</Button>
      </div>
      <p v-else-if="!filteredModels.length" role="status" class="muted">
        {{ models.length ? '没有匹配的模型。' : '本机 Pi 没有找到可用模型，请先配置模型和凭证。' }}
      </p>
      <ul v-else class="model-options" aria-label="本机 Pi 模型">
        <li v-for="model in filteredModels" :key="JSON.stringify([model.provider, model.id])">
          <Button
            type="button"
            variant="ghost"
            class="model-option"
            :aria-current="selectedModel?.provider === model.provider && selectedModel.id === model.id ? 'true' : undefined"
            @click="choose(model)"
          >
            <span class="model-id">{{ model.provider }} / {{ model.id }}</span>
            <span v-if="selectedModel?.provider === model.provider && selectedModel.id === model.id" class="muted shrink-0">已选</span>
          </Button>
        </li>
      </ul>
      <div class="model-picker-actions">
        <Button type="button" variant="outline" size="sm" :disabled="!props.selectedModel" @click="choose()">使用 Pi 默认模型</Button>
      </div>
    </div>
  </ModalDialog>
</template>

<style scoped>
.model-picker-content { display: grid; gap: 10px; }
.model-options { display: grid; gap: 2px; min-height: 0; max-height: min(24rem, 48dvh); overflow: auto; overscroll-behavior: contain; }
.model-option { width: 100%; height: auto; justify-content: space-between; padding-block: 8px; text-align: start; white-space: normal; }
.model-option[aria-current='true'] { background: var(--brand-color-bg-layout); }
.model-id { min-width: 0; overflow-wrap: anywhere; }
.model-list-feedback { display: grid; gap: 8px; justify-items: start; }
.model-picker-actions { display: flex; justify-content: flex-end; }
</style>
