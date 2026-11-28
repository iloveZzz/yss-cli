<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import PiModelPicker from '@/components/PiModelPicker.vue'
import { computed, onBeforeUnmount, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { getPiRuntime, piModelKey, testPiConnection } from '@/services/workbench-agent'
import { ApiError, errorMessage } from '@/services/workbench'
import { piCapabilityLabels, piRuntimeAvailable, type PiModel, type PiRuntimeStatus } from '@/services/workbench-platform'

const props = withDefaults(defineProps<{ active?: boolean; busy?: boolean; resumeRunId?: string; allowModelSwitch?: boolean }>(), {
  active: true
})
const emit = defineEmits<{ recheck: [] }>()
const readyModel = defineModel<PiModel>('readyModel')
const selectedModel = defineModel<PiModel>('selectedModel')
const blockedReason = defineModel<string>('blockedReason')
const modelRestrictions = defineModel<Record<string, string>>('modelRestrictions', { default: () => ({}) })
const testDescription = useId()
const status = defineModel<PiRuntimeStatus>('runtimeStatus')
const operation = shallowRef<'initial' | 'check' | 'selection' | 'test'>()
const checkError = shallowRef('')
const testResult = shallowRef<{ message: string; connected: boolean; issue?: string }>()
const model = computed(() => status.value && piRuntimeAvailable(status.value) ? status.value.model : undefined)
const canChooseModel = computed(() => props.allowModelSwitch ?? !props.resumeRunId)
const chosen = computed(() => selectedModel.value)
const visibleModel = computed(() => chosen.value ?? status.value?.model)
const modelLabel = computed(() => chosen.value ? (canChooseModel.value ? '本次选择' : '来源轮次模型')
  : props.resumeRunId ? '会话模型' : 'Pi 默认模型')
const capabilityLabel = computed(() => {
  const runtime = status.value
  if (!runtime) return ''
  return piCapabilityLabels[runtime.capabilities?.pi.state ?? (runtime.available ? 'available' : 'blocked')]
})
const statusMessage = computed(() => {
  switch (operation.value) {
    case 'initial': return ''
    case 'check': return '正在重新检测…'
    case 'selection': return '正在检测本次模型…'
    case 'test': return '正在请求模型…'
    default:
      return testResult.value?.message || checkError.value || blockedReason.value || status.value?.capabilities?.pi.reason || status.value?.message ||
        (status.value?.credentialsReady ? '已找到本机凭证。' : '')
  }
})
const showSetup = computed(() => {
  const issue = testResult.value?.issue ?? status.value?.capabilities?.pi.reasonCode ?? status.value?.issue
  return !operation.value && ['configuration', 'credentials', 'modelUnavailable', 'modelAccess'].includes(issue ?? '')
})
const piCommand = computed(() => {
  const configDir = status.value?.configDir
  const executable = status.value?.executable
  if (!configDir || !executable) return undefined
  const node = status.value?.nodeExecutable
  if (/^[a-z]:/i.test(executable) || executable.startsWith('\\\\')) {
    const quote = (value: string) => `'${value.replaceAll("'", "''")}'`
    return `$env:PI_CODING_AGENT_DIR=${quote(configDir)}; & ${node ? quote(node) + ' ' : ''}${quote(executable)} --no-extensions --no-approve`
  }
  const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`
  return `PI_CODING_AGENT_DIR=${quote(configDir)} ${node ? quote(node) + ' ' : ''}${quote(executable)} --no-extensions --no-approve`
})
let request: AbortController | undefined

function recheckRuntime() {
  if (props.busy || request) return
  emit('recheck')
  void checkRuntime()
}

async function checkRuntime(kind: 'initial' | 'check' | 'selection' = 'check') {
  if (request) return
  const current = new AbortController()
  request = current
  operation.value = kind
  checkError.value = ''
  testResult.value = undefined
  readyModel.value = undefined
  blockedReason.value = ''
  try {
    const result = await getPiRuntime({ resumeRunId: props.resumeRunId, selectedModel: chosen.value }, current.signal)
    if (current.signal.aborted) return
    status.value = result
    if (piRuntimeAvailable(result) && result.model) {
      const restriction = modelRestrictions.value[piModelKey(result.model)] ?? ''
      blockedReason.value = restriction
      if (!restriction) readyModel.value = result.model
    } else blockedReason.value = result.capabilities?.pi.reason || result.message || 'Pi 模型未就绪'
  } catch (cause) {
    if (current.signal.aborted) return
    status.value = undefined
    checkError.value = errorMessage(cause)
    blockedReason.value = checkError.value
  } finally {
    if (request === current) {
      request = undefined
      operation.value = undefined
    }
  }
}

async function testConnection() {
  const target = model.value
  if (!target || request || props.busy) return
  const current = new AbortController()
  request = current
  operation.value = 'test'
  testResult.value = undefined
  readyModel.value = undefined
  try {
    await testPiConnection(target, { resumeRunId: props.resumeRunId, selectedModel: chosen.value }, current.signal)
    if (current.signal.aborted) return
    testResult.value = { connected: true, message: '模型连接成功。' }
    const remaining = { ...modelRestrictions.value }
    delete remaining[piModelKey(target)]
    modelRestrictions.value = remaining
    readyModel.value = target
    blockedReason.value = ''
  } catch (cause) {
    if (!current.signal.aborted) {
      const value = cause instanceof ApiError ? cause.context.piIssue : undefined
      const issue = typeof value === 'string' ? value : undefined
      testResult.value = { connected: false, message: errorMessage(cause), issue }
      const key = piModelKey(target)
      const hardFailure = ['subscription', 'configuration', 'credentials', 'modelAccess', 'modelUnavailable', 'modelChanged'].includes(issue ?? '')
      const restriction = hardFailure ? testResult.value.message : modelRestrictions.value[key] ?? ''
      if (hardFailure) {
        modelRestrictions.value = { ...modelRestrictions.value, [key]: restriction }
      }
      blockedReason.value = restriction
      if (!restriction) readyModel.value = target
    }
  } finally {
    if (request === current) {
      request = undefined
      operation.value = undefined
    }
  }
}

function chooseModel(next?: PiModel) {
  if (props.busy || !canChooseModel.value || operation.value === 'test') return
  if (next?.provider === chosen.value?.provider && next?.id === chosen.value?.id) return
  readyModel.value = undefined
  selectedModel.value = next
}

watch([() => props.active, () => props.resumeRunId, chosen], ([active, resumeRunId], previous) => {
  request?.abort()
  request = undefined
  operation.value = undefined
  status.value = undefined
  readyModel.value = undefined
  if (active) void checkRuntime(previous?.[0] && previous[1] === resumeRunId ? 'selection' : 'initial')
}, { immediate: true })
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <Alert v-if="active" role="group" aria-label="Pi 运行检查" class="notice pi-readiness grid-cols-1" :class="{ warning: checkError || (status && !piRuntimeAvailable(status)) || testResult?.connected === false }">
    <p class="model-label">
      <Badge variant="outline">{{ status?.version ? `Pi ${status.version}` : 'Pi' }}</Badge>
      <span v-if="capabilityLabel">基础作业{{ capabilityLabel }}</span>
      <span v-if="visibleModel">
        {{ modelLabel }} · {{ visibleModel.provider }} / {{ visibleModel.id }}
      </span>
    </p>
    <p v-if="status?.configDir" class="config-directory">本机 Pi 配置目录 <code>{{ status.configDir }}</code></p>
    <LoadingSkeleton v-if="operation === 'initial'" variant="text" :rows="2" label="正在检查 Pi 版本、模型和本机凭证" />
    <AlertDescription role="status" aria-atomic="true" class="col-start-1 text-current">
      {{ statusMessage }}
    </AlertDescription>
    <div v-if="showSetup" class="model-setup">
      <p class="font-medium">{{ !canChooseModel && chosen ? '如何恢复来源轮次模型' : resumeRunId ? '如何恢复原会话模型' : '如何配置模型' }}</p>
      <p v-if="!status?.configDir">
        检查启动工作台时的 <code>PI_CODING_AGENT_DIR</code> 设置。Pi 默认使用 <code>~/.pi/agent</code>。
      </p>
      <ol class="list-decimal space-y-1 pl-5">
        <li>
          在本机 Pi 配置目录的 <code>models.json</code> 中声明自定义供应商、模型和凭证。
          <template v-if="piCommand">
            内置供应商可执行以下命令打开工作台所用的 Pi，再用 <code>/login</code> 登录。
            <code class="model-command">{{ piCommand }}</code>
          </template>
          <template v-else>
            内置供应商可在确认工作台使用的 Pi 程序和配置目录后，通过 <code>/login</code> 登录。
          </template>
        </li>
        <li v-if="!canChooseModel && chosen">确认本机 Pi 配置目录提供来源轮次使用的供应商和模型。</li>
        <li v-else-if="resumeRunId">确认本机 Pi 可用的供应商和模型。<template v-if="canChooseModel">可在会话的输入区为下一轮选择其他模型。</template></li>
        <li v-else>
          用 <code>/model</code> 找到模型并按 <kbd>Ctrl+S</kbd> 保存为默认，或在 <code>settings.json</code> 中设置 <code>defaultProvider</code> 和 <code>defaultModel</code>。
          如果配置了 <code>enabledModels</code>，请确保启动范围包含所需模型。
        </li>
        <li v-if="piCommand">
          可离线核对可用模型。
          <code class="model-command">{{ piCommand }} --offline --list-models</code>
        </li>
      </ol>
      <p>配置保存后点击「重新检测」。修改 <code>PI_CODING_AGENT_DIR</code> 或凭证环境变量后，请从设有新值的终端重新启动工作台。</p>
    </div>
    <p v-if="model" :id="testDescription" class="muted">连接测试会发送一条简短请求，可能使用模型额度。</p>
    <div class="model-actions">
      <PiModelPicker v-if="canChooseModel" :selected-model="chosen" :current-model="visibleModel ?? undefined"
        :default-label="resumeRunId ? '沿用会话模型' : undefined" :disabled="busy || operation === 'test'" @select="chooseModel" />
      <Button type="button" variant="outline" size="sm" :aria-disabled="busy || !!operation" @click="recheckRuntime">
        <LoadingIndicator :active="operation === 'check' || operation === 'selection'" />
        重新检测
      </Button>
      <Button
        v-if="model"
        type="button"
        variant="outline"
        size="sm"
        :aria-describedby="testDescription"
        :aria-disabled="busy || !!operation"
        @click="testConnection"
      >
        <LoadingIndicator :active="operation === 'test'" />
        测试模型连接
      </Button>
    </div>
  </Alert>
</template>

<style scoped>
.pi-readiness { display: grid; gap: 6px; }
.pi-readiness p { margin: 0; }
.model-label, .model-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 6px 12px; }
.model-label span, .config-directory, .model-setup { min-width: 0; overflow-wrap: anywhere; }
.model-setup { display: grid; gap: 4px; }
.model-command { display: block; margin-block: 2px; white-space: pre-wrap; }
.model-actions { margin-top: 4px; }
.model-actions button[aria-disabled='true'] { opacity: 0.65; }
</style>
