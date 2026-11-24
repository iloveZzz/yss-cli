<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { computed, nextTick, onBeforeUnmount, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldLabel } from '@/components/ui/field'
import { ApiError, errorMessage } from '@/services/workbench'
import {
  getAgentBindingPreview,
  rebindAgent,
  type AgentBindingPreview
} from '@/services/workbench-agent'
const props = defineProps<{ runId: string; busy?: boolean }>()
const emit = defineEmits<{ rebound: []; busy: [value: boolean] }>()
const preview = shallowRef<AgentBindingPreview>()
const confirmed = shallowRef(false)
const phase = shallowRef<'preview' | 'rebind' | null>(null)
const error = shallowRef('')
const notice = shallowRef('')
const id = useId()
const pending = computed(() => phase.value !== null)
let generation = 0
function reset() {
  generation++
  preview.value = undefined
  confirmed.value = false
  phase.value = null
  error.value = ''
  notice.value = ''
  emit('busy', false)
}
function cancelReview() {
  reset()
  void nextTick(() => document.getElementById(`${id}-trigger`)?.focus())
}
watch(() => props.runId, reset, { flush: 'sync' })
onBeforeUnmount(reset)
async function review() {
  if (pending.value || props.busy) return
  const own = ++generation
  const runId = props.runId
  preview.value = undefined
  confirmed.value = false
  error.value = ''
  notice.value = ''
  phase.value = 'preview'
  emit('busy', true)
  try {
    const candidate = await getAgentBindingPreview(runId)
    if (own === generation) preview.value = candidate
  } catch (cause) {
    if (own === generation) error.value = errorMessage(cause)
  } finally {
    if (own === generation) {
      phase.value = null
      emit('busy', false)
    }
  }
}
async function apply() {
  const candidate = preview.value
  if (pending.value || props.busy || !candidate?.bindings.length) return
  if (!confirmed.value) {
    error.value = '请勾选确认已核对工程路径与 Git 基线。'
    return
  }
  const own = ++generation
  phase.value = 'rebind'
  error.value = ''
  emit('busy', true)
  try {
    await rebindAgent(props.runId, candidate.previewToken)
    if (own !== generation) return
    preview.value = undefined
    confirmed.value = false
    notice.value = '工程绑定已更新，请重新审阅差异并选择要接收的文件。'
    emit('rebound')
  } catch (cause) {
    if (own !== generation) return
    if (cause instanceof ApiError && cause.status === 409) {
      preview.value = undefined
      confirmed.value = false
      error.value = `工程登记或 Git 状态已变化，请重新核对工程绑定。${errorMessage(cause)}`
    } else error.value = errorMessage(cause)
  } finally {
    if (own === generation) {
      phase.value = null
      emit('busy', false)
    }
  }
}
</script>
<template>
  <section class="rebind-panel" aria-label="运行工程绑定核对">
    <Button
      :id="`${id}-trigger`"
      type="button"
      variant="outline"
      :disabled="busy || pending"
      :aria-expanded="!!preview"
      :aria-controls="preview ? id : undefined"
      @click="review"
    >
      {{ phase === 'preview' ? '正在核对工程绑定…' : '核对工程绑定' }}
    </Button>
    <Alert v-if="error" variant="destructive" class="notice error">
      <AlertDescription class="text-current">{{ error }}</AlertDescription>
    </Alert>
    <Alert v-if="notice" role="status" class="notice">
      <AlertDescription class="text-current">{{ notice }}</AlertDescription>
    </Alert>
    <LoadingSkeleton v-if="phase === 'preview'" variant="form" :rows="3" label="正在读取工程绑定预览" />
    <Card v-else-if="preview" class="rebind-review-card gap-0 p-0 shadow-none">
      <form
        :id="id"
        :action="`/api/agent/${encodeURIComponent(runId)}/rebind`"
        method="post"
        class="binding-review"
        :aria-labelledby="`${id}-title`"
        :aria-busy="phase === 'rebind'"
        @submit.prevent="apply"
      >
        <h3 :id="`${id}-title`">核对本机工程位置</h3>
        <p class="muted">恢复到新目录或机器后，可按当前工程登记更新这次运行的接收目标。</p>
        <article
          v-for="binding in preview.bindings"
          :key="binding.previousRoot"
          class="binding-mapping"
        >
          <dl>
            <div>
              <dt>原工程路径</dt>
              <dd class="path-text">{{ binding.previousRoot }}</dd>
            </div>
            <div>
              <dt>当前登记路径</dt>
              <dd class="path-text">{{ binding.root }}</dd>
            </div>
            <div>
              <dt>当前分支</dt>
              <dd>{{ binding.gitBaseline.branch?.replace(/^refs\/heads\//, '') ?? '分离 HEAD' }}</dd>
            </div>
            <div>
              <dt>HEAD</dt>
              <dd class="path-text">{{ binding.gitBaseline.head }}</dd>
            </div>
          </dl>
          <ul class="binding-projects">
            <li v-for="projectId in binding.projectIds" :key="projectId">
              工程 {{ projectId }} · 相对路径 {{ binding.projectPrefixes[projectId] || '.' }}
            </li>
          </ul>
        </article>
        <template v-if="preview.bindings.length">
          <Field orientation="horizontal" class="binding-confirmation">
            <Checkbox
              :id="`${id}-confirmed`"
              :model-value="confirmed"
              name="confirmed"
              required
              :disabled="busy || pending"
              @update:model-value="confirmed = $event === true"
            />
            <FieldLabel :for="`${id}-confirmed`">
              我已核对以上工程路径、分支与 HEAD，确认更新本次运行的工程绑定（必选）
            </FieldLabel>
          </Field>
          <div class="inline-actions">
            <Button type="submit" :disabled="busy || pending">
              <LoadingIndicator :active="phase === 'rebind'" />
              {{ phase === 'rebind' ? '正在更新工程绑定…' : '确认更新绑定' }}
            </Button>
            <Button type="button" variant="ghost" :disabled="pending" @click="cancelReview">取消</Button>
          </div>
        </template>
        <p v-else class="muted">没有可更新的工程绑定。</p>
      </form>
    </Card>
  </section>
</template>
<style scoped>
.rebind-panel {
  margin: 16px 0;
}
.rebind-panel > .notice {
  margin-top: 12px;
}
.rebind-review-card {
  margin-top: 16px;
}
.binding-review {
  padding: 16px;
}
.binding-review > p {
  margin: 8px 0 16px;
}
.binding-mapping {
  padding: 12px 0;
  border-bottom: 1px solid var(--yss-colors-border);
}
.binding-mapping dl {
  display: grid;
  gap: 10px;
  margin: 0;
}
.binding-mapping dl > div {
  display: grid;
  grid-template-columns: 100px minmax(0, 1fr);
  gap: 12px;
}
.binding-mapping dt {
  color: #62666c;
  font-size: 12px;
}
.binding-mapping dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.binding-projects {
  margin: 12px 0 0;
  padding-left: 16px;
  color: #717780;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.binding-confirmation {
  min-height: 44px;
  margin: 12px 0;
}
.binding-confirmation label {
  min-height: inherit;
  align-items: center;
  cursor: pointer;
}
@media (width<720px) {
  .binding-mapping dl > div {
    grid-template-columns: 1fr;
    gap: 4px;
  }
}
</style>
