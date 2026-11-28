<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, useId, watch } from 'vue'
import { Check, Search } from '@lucide/vue'
import { ListboxContent, ListboxFilter, ListboxItem, ListboxItemIndicator, ListboxRoot } from 'reka-ui'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getPiModels, piModelKey } from '@/services/workbench-agent'
import { ApiError, errorMessage } from '@/services/workbench'
import { agentHostLabels, grokRuntimeLabel, isWorkspaceAgent, piThinkingLabels, type AgentHost, type GrokRuntimeStatus, type PiModel, type PiThinkingLevel } from '@/services/workbench-platform'

const props = withDefaults(defineProps<{
  selectedModel?: PiModel
  currentModel?: PiModel
  defaultLabel?: string
  disabled?: boolean
  showThinking?: boolean
  thinkingLevels?: PiThinkingLevel[]
  thinkingLevel?: PiThinkingLevel
  checkingThinking?: boolean
  cli?: AgentHost
  lockedCli?: AgentHost
  selectionLocked?: boolean
  issue?: string
}>(), { defaultLabel: '使用 Pi 默认模型' })
const emit = defineEmits<{ select: [model?: PiModel]; thinking: [level: PiThinkingLevel]; target: [host: AgentHost, model?: PiModel]; newSession: [] }>()

const open = shallowRef(false)
const awaitingLevels = shallowRef(false)
const loading = shallowRef(false)
const query = shallowRef('')
const provider = shallowRef('')
const browsedCli = shallowRef<AgentHost>(props.cli ?? 'pi')
const directories = shallowRef<Partial<Record<AgentHost, PiModel[]>>>({})
const errors = shallowRef<Partial<Record<AgentHost, string>>>({})
const grokStatus = shallowRef<GrokRuntimeStatus>()
const feedback = computed(() => {
  if (browsedCli.value !== 'grok' || !grokStatus.value) return error.value
  return [grokRuntimeLabel(grokStatus.value), grokStatus.value.message].filter(Boolean).join(' · ')
})
const models = computed(() => directories.value[browsedCli.value] ?? [])
const error = computed(() => errors.value[browsedCli.value] ?? '')
const selectedCli = computed(() => props.cli ?? 'pi')
const browsingSelection = computed(() => browsedCli.value === selectedCli.value)
const selectionDisabled = computed(() => props.selectionLocked || (props.lockedCli != null && props.lockedCli !== browsedCli.value))
const cliLabel = computed(() => agentHostLabels[browsedCli.value])
const defaultTargetLabel = computed(() => props.cli
  ? props.lockedCli && browsingSelection.value ? '沿用会话模型' : '使用 ' + cliLabel.value + ' 默认模型'
  : props.defaultLabel)
const listId = useId()
const providers = computed(() => [...new Set(models.value.map((model) => model.provider))])
const showLevels = computed(() => props.showThinking && browsingSelection.value && (props.checkingThinking || !!props.thinkingLevels?.length))
const selectedKey = computed(() => {
  const model = browsingSelection.value ? props.selectedModel ?? props.currentModel : undefined
  return model ? piModelKey(model) : ''
})
const filteredModels = computed(() => {
  const terms = query.value.toLowerCase().split(/[\s/]+/u).filter(Boolean)
  return models.value.filter((model) => {
    const text = `${model.provider}/${model.id}`.toLowerCase()
    return (!provider.value || model.provider === provider.value) && terms.every((term) => text.includes(term))
  })
})
let request: AbortController | undefined

function cancelRead() {
  request?.abort()
  request = undefined
  loading.value = false
}

async function readModels(refresh = false) {
  const host = browsedCli.value
  if (request || (!refresh && directories.value[host])) return
  const current = new AbortController()
  request = current
  loading.value = true
  errors.value = { ...errors.value, [host]: '' }
  try {
    const result = await getPiModels(current.signal, host)
    if (current.signal.aborted) return
    directories.value = { ...directories.value, [host]: result.models }
    if (host === 'grok') grokStatus.value = undefined
  } catch (cause) {
    if (current.signal.aborted) return
    const message = errorMessage(cause)
    errors.value = { ...errors.value, [host]: message }
    if (host === 'grok') grokStatus.value = {
      host, available: false, message,
      issue: cause instanceof ApiError && typeof cause.context.grokIssue === 'string' ? cause.context.grokIssue : undefined
    }
  } finally {
    if (request === current) {
      request = undefined
      loading.value = false
    }
  }
}

function browse(host: unknown) {
  if (!isWorkspaceAgent(host) || browsedCli.value === host) return
  cancelRead()
  awaitingLevels.value = false
  browsedCli.value = host
  query.value = ''
  provider.value = ''
  void readModels()
}

function choose(model?: PiModel) {
  if (props.disabled || selectionDisabled.value || (model && (error.value || loading.value))) return
  const sameModel = browsingSelection.value && !!model && piModelKey(model) === selectedKey.value
  awaitingLevels.value = !!(props.showThinking && model && !sameModel)
  if (!awaitingLevels.value) open.value = false
  if (props.cli) emit('target', browsedCli.value, model)
  else emit('select', model)
}

function openNewSession() {
  open.value = false
  emit('newSession')
}

function chooseThinking(level: PiThinkingLevel) {
  emit('thinking', level)
  open.value = false
}

watch(open, (isOpen) => {
  if (isOpen) {
    browsedCli.value = selectedCli.value
    query.value = ''
    provider.value = ''
    void readModels()
  } else {
    awaitingLevels.value = false
    cancelRead()
  }
}, { flush: 'sync' })
watch([() => props.checkingThinking, () => props.thinkingLevels], ([checking, levels]) => {
  if (!awaitingLevels.value || checking) return
  awaitingLevels.value = false
  if (!props.issue && (levels?.length ?? 0) < 2) open.value = false
})
watch(() => props.disabled, (disabled) => { if (disabled) open.value = false })
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <slot name="trigger">
        <Button type="button" variant="outline" size="sm" :disabled="disabled">选择本次模型</Button>
      </slot>
    </PopoverTrigger>
    <PopoverContent side="top" align="end" :side-offset="8" :collision-padding="12" class="pi-model-picker"
      :aria-label="cli ? '选择 CLI 和模型' : '选择本次模型'" :data-thinking="showThinking ? '' : undefined">
      <h2 v-if="cli" class="px-3 pt-3 text-sm font-semibold">选择 CLI 和模型</h2>
      <Tabs :model-value="browsedCli" activation-mode="manual" class="model-picker-tabs" @update:model-value="browse">
        <TabsList v-if="cli" aria-label="浏览 Agent CLI" class="model-cli-tabs mx-3 mt-2 grid w-auto grid-cols-2">
          <TabsTrigger value="pi">Pi</TabsTrigger>
          <TabsTrigger value="grok">Grok Build</TabsTrigger>
        </TabsList>
        <TabsContent :value="browsedCli" class="model-picker-panel">
          <div v-if="cli && selectionDisabled" class="mx-3 flex flex-wrap items-center justify-between gap-1 rounded-md bg-muted px-2 py-1 text-xs text-muted-foreground">
            <template v-if="lockedCli && lockedCli !== browsedCli">
              <span>本会话使用 {{ agentHostLabels[lockedCli] }}。可通过新会话或交接使用其他 CLI。</span>
              <Button type="button" variant="ghost" size="xs" @click="openNewSession">新会话</Button>
            </template>
            <span v-else>当前轮次或待核对请求已固定发送目标。</span>
          </div>
          <p v-if="browsingSelection && issue" class="px-3 pt-2 text-xs text-destructive" role="status">{{ issue }}</p>
          <div class="model-picker-body" :data-levels="showLevels ? '' : undefined">
            <ListboxRoot :model-value="selectedKey" highlight-on-hover class="model-picker-content"
              :data-providers="providers.length > 1 ? '' : undefined">
              <div class="model-search">
                <Search :size="16" aria-hidden="true" />
                <ListboxFilter v-model="query" role="combobox" :aria-controls="listId" :aria-expanded="open"
                  aria-autocomplete="list" aria-label="搜索模型" placeholder="搜索模型或供应商…" autocomplete="off" />
              </div>
              <div v-if="providers.length > 1" class="model-providers" role="group" aria-label="模型供应商">
                <Button type="button" variant="ghost" size="xs" :aria-pressed="!provider" @click="provider = ''">全部</Button>
                <Button v-for="name in providers" :key="name" type="button" variant="ghost" size="xs"
                  :aria-pressed="provider === name" @click="provider = name">{{ name }}</Button>
              </div>
              <div class="model-list">
                <LoadingSkeleton v-if="loading" :rows="3" :label="'正在读取本机 ' + cliLabel + ' 模型'" />
                <div v-if="error" class="model-list-feedback">
                  <p role="alert">{{ models.length ? '目录待核对，以下为上次成功列表。' : '' }} {{ feedback }}</p>
                </div>
                <p v-else-if="!loading && !filteredModels.length" role="status" class="model-list-feedback">
                  {{ models.length ? '没有匹配的模型。' : `本机 ${cliLabel} 没有可选模型，请检查模型和凭据配置。` }}
                </p>
                <ListboxContent :id="listId" :aria-label="'本机 ' + cliLabel + ' 模型'">
                  <ListboxItem v-for="model in filteredModels" :key="piModelKey(model)" :value="piModelKey(model)" :disabled="selectionDisabled || loading || !!error"
                    :aria-disabled="selectionDisabled || loading || !!error ? true : undefined" class="model-option" @select="choose(model)">
                    <span class="model-id">{{ model.id }} <small>{{ model.provider }}</small></span>
                    <ListboxItemIndicator><Check :size="16" aria-hidden="true" /></ListboxItemIndicator>
                  </ListboxItem>
                </ListboxContent>
              </div>
            </ListboxRoot>
            <div v-if="showLevels" class="model-thinking">
              <div class="model-thinking-label">推理级别</div>
              <LoadingSkeleton v-if="checkingThinking" :rows="1" label="正在读取模型推理级别" />
              <p v-else-if="thinkingLevels?.length === 1 && thinkingLevels[0] === 'off'" class="model-thinking-note">此模型不提供可调推理级别</p>
              <div v-else role="group" aria-label="推理级别" class="model-thinking-options">
                <Button v-for="level in thinkingLevels" :key="level" type="button" variant="ghost" size="xs"
                  :aria-pressed="level === thinkingLevel" :disabled="selectionDisabled" @click="chooseThinking(level)">{{ piThinkingLabels[level] ?? level }}</Button>
              </div>
            </div>
          </div>
          <div class="model-picker-actions">
            <Button type="button" variant="ghost" size="sm" :disabled="selectionDisabled || (browsingSelection && !selectedModel)" @click="choose()">{{ defaultTargetLabel }}</Button>
            <Button type="button" variant="ghost" size="sm" :disabled="loading" @click="readModels(true)">刷新目录</Button>
          </div>
        </TabsContent>
      </Tabs>
    </PopoverContent>
  </Popover>
</template>

<style scoped>
:global(.pi-model-picker[data-slot='popover-content']) {
  display: flex; flex-direction: column; width: 28rem; padding: 0; overflow: hidden; border-radius: 12px;
  max-width: min(calc(100vw - 24px), var(--reka-popover-content-available-width, 100vw));
  max-height: min(29rem, var(--reka-popover-content-available-height, calc(100dvh - 24px)));
  color: var(--brand-color-text-primary, #222);
}
.model-picker-tabs, .model-picker-panel { display: flex; flex-direction: column; min-height: 0; }
.model-cli-tabs [role='tab'][data-state='active'] { border-color: var(--yss-colors-border); box-shadow: 0 1px 2px rgb(0 0 0 / 12%); }
.model-picker-content { display: flex; flex-direction: column; min-height: 0; }
.model-picker-body { min-height: 0; display: flex; flex-direction: column; }
.model-search {
  display: flex; align-items: center; gap: 9px; margin-inline: 12px; padding-inline: 4px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.model-search:focus-within { border-bottom-color: var(--yss-color-primary-control); }
.model-search svg { flex: none; color: var(--muted-foreground); }
.model-search input { width: 100%; min-width: 0; height: 42px; border: 0; outline: 0; background: transparent; font-size: 14px; }
.model-search:has(input:focus-visible) { box-shadow: inset 0 -1px var(--yss-color-primary-control); }
.model-providers { display: flex; flex: none; gap: 4px; padding: 6px 12px; overflow-x: auto; scrollbar-width: thin; }
.model-providers [aria-pressed='true'] { background: var(--accent); }
.model-list { min-height: 0; overflow: auto; overscroll-behavior: contain; padding: 5px 6px; }
.model-option { display: flex; align-items: center; justify-content: space-between; gap: 12px; min-height: 34px; padding: 6px 9px; border-radius: 6px; font-size: 13px; cursor: pointer; }
.model-option[data-highlighted] { background: var(--accent); }
.model-id { min-width: 0; overflow-wrap: anywhere; }
.model-id small { margin-inline-start: 5px; font-size: 11px; color: var(--muted-foreground); }
.model-list-feedback { display: grid; gap: 8px; padding: 8px 10px; justify-items: start; color: var(--muted-foreground); font-size: 13px; }
.model-picker-actions { display: flex; flex: none; justify-content: flex-end; padding: 4px 7px; border-top: 1px solid var(--yss-colors-border); }
.model-thinking { flex: none; padding: 8px 12px; border-top: 1px solid var(--yss-colors-border); }
.model-thinking-label, .model-thinking-note { font-size: 12px; color: var(--muted-foreground); }
.model-thinking-options { display: flex; flex-wrap: wrap; gap: 2px; margin-top: 4px; }
.model-thinking-options [aria-pressed='true'] { background: var(--accent); }
@media (width >= 700px) {
  :global(.pi-model-picker[data-slot='popover-content'][data-thinking]) { width: 42rem; }
  .model-picker-body[data-levels] { display: grid; grid-template-columns: minmax(0, 1fr) minmax(126px, 24%); }
  .model-picker-content[data-providers] {
    display: grid; grid-template-columns: minmax(96px, 28%) minmax(0, 1fr);
    grid-template-rows: auto minmax(0, 1fr);
  }
  .model-picker-content[data-providers] .model-search, .model-picker-content[data-providers] .model-list { grid-column: 2; }
  .model-picker-content[data-providers] .model-list { grid-row: 2; }
  .model-providers {
    grid-area: 1 / 1 / 3; flex-direction: column; padding: 7px; overflow: auto;
    border-inline-end: 1px solid var(--yss-colors-border); overscroll-behavior: contain;
  }
  .model-providers button { justify-content: flex-start; min-height: 28px; height: auto; white-space: normal; overflow-wrap: anywhere; }
  .model-thinking { border-inline-start: 1px solid var(--yss-colors-border); border-block-start: 0; padding-block: 11px; overflow: auto; }
  .model-thinking-options { flex-direction: column; align-items: stretch; }
  .model-thinking-options button { justify-content: flex-start; min-height: 30px; }
}
@media (width < 600px) { .model-search input { font-size: 16px; } .model-option { min-height: 42px; } }
@media (forced-colors: active) {
  .model-search input:focus-visible { outline: 2px solid Highlight; outline-offset: -2px; }
}
</style>
