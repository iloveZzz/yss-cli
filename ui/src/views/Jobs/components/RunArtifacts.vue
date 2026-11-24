<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { getAgentSpecView } from '@/services/workbench-agent'
import { errorMessage, contentStatuses, type SpecView } from '@/services/workbench'
import SourcePreview from '@/views/SpecDetail/components/SourcePreview.vue'
import ResourceViewer from '@/views/SpecDetail/components/ResourceViewer.vue'
import { currentQuestionResponse, questionHistory, questionStateLabel, responseText, responseUse } from '@/services/workbench-content'
import QuestionHistory from '@/views/SpecDetail/components/QuestionHistory.vue'
const props = defineProps<{ runId: string; specId: string; running: boolean }>()
const view = shallowRef<SpecView>()
const error = shallowRef('')
const loading = shallowRef(false)
const refreshing = shallowRef(false)
const resource = shallowRef<{ path: string; fragment: string } | null>(null)
const version = computed(
  () => `${view.value?.sourceModifiedAt}:${view.value?.content.artifactDigest}`
)
const questions = computed(() => (view.value?.content.questions ?? []).map((item) => ({
  item, response: currentQuestionResponse(item), history: questionHistory(item)
})))
let generation = 0
onBeforeUnmount(() => {
  generation++
})
async function refresh(explicit = false) {
  if (explicit) refreshing.value = true
  if (loading.value) return
  const own = ++generation
  loading.value = true
  try {
    const result = await getAgentSpecView(props.runId, props.specId)
    if (own === generation) {
      view.value = result
      error.value = ''
    }
  } catch (cause) {
    if (own === generation) error.value = errorMessage(cause)
  } finally {
    if (own === generation) {
      loading.value = false
      refreshing.value = false
    }
  }
}
watch(() => [props.runId, props.specId], () => {
  generation++
  view.value = undefined
  resource.value = null
  error.value = ''
  loading.value = false
  refreshing.value = false
  void refresh()
}, { immediate: true })
useIntervalFn(() => {
  if (props.running && !loading.value && !document.hidden) void refresh()
}, 5000)
function navigate(path: string, fragment = '') {
  resource.value = { path, fragment }
}
function safeWeb(value: string) {
  try {
    const url = new URL(value)
    return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password
  } catch {
    return false
  }
}
</script>
<template>
  <section class="platform-section">
    <div class="section-heading">
      <h2>隔离工作区产物</h2>
      <Button type="button" variant="outline" :aria-disabled="refreshing" @click="!refreshing && refresh(true)">
        <LoadingIndicator :active="refreshing && !!view" />
        {{ refreshing ? '正在读取…' : '重新读取' }}
      </Button>
    </div>
    <Alert v-if="error" class="notice warning">
      <AlertDescription class="text-current">{{ error }}</AlertDescription>
    </Alert>
    <LoadingSkeleton v-if="loading && !view" variant="detail" label="正在读取本次产物" />
    <template v-if="view">
      <div class="artifact-status">
        <Badge variant="secondary">{{ contentStatuses[view.content.status] }}</Badge>
        <span class="muted">{{ view.content.scope }}</span>
      </div>
      <div v-if="view.resources.length" class="inline-actions resource-shortcuts">
        <template v-for="(item, index) in view.resources" :key="index">
          <Button v-if="item.record.targetType === 'web' && safeWeb(item.record.target)" variant="outline" class="artifact-link" as-child>
            <a :href="item.record.target" target="_blank" rel="noopener noreferrer">
              {{ item.record.label }} <span aria-hidden="true">↗</span>
            </a>
          </Button>
          <Button v-else type="button" variant="outline" class="artifact-link" @click="navigate(item.record.target)">
            {{ item.record.label }}
          </Button>
        </template>
      </div>
      <SourcePreview
        :run-id="runId"
        :spec-id="specId"
        :source-path="view.record.sourcePath"
        :refresh-key="version"
        embedded
        @navigate="navigate"
      />
      <div v-if="view.content.questions.length" class="run-questions">
        <h3>本次运行的问题</h3>
        <Card
          v-for="{ item: question, response, history } in questions"
          :key="question.question.id"
          class="run-question gap-2 py-3 shadow-none"
        >
          <CardHeader class="px-4">
            <CardTitle class="text-sm leading-snug">{{ question.question.id }} · {{ question.question.title }}</CardTitle>
            <Badge variant="outline">{{ questionStateLabel(question) }}</Badge>
          </CardHeader>
          <CardContent class="grid gap-2 px-4">
            <p v-if="response" class="preserve-lines">
              {{ responseUse(question, response) }} · {{ response.id }}<br />
              {{ responseText(response) }}
            </p>
            <p v-if="question.question.resolution" class="preserve-lines">
              {{ question.state === 'resolved' || question.state === 'deferred' ? '已回写结论' : '来源中的既有记录，供复查' }} · {{ question.question.resolution }}
            </p>
            <QuestionHistory :entries="history" />
          </CardContent>
        </Card>
      </div>
      <ResourceViewer
        v-model="resource"
        :run-id="runId"
        :spec-id="specId"
        :source-path="view.record.sourcePath"
        :refresh-key="version"
      />
    </template>
  </section>
</template>

<style scoped>
.section-heading button[aria-disabled='true'] {
  opacity: 0.65;
}
.artifact-status {
  display: flex;
  gap: 8px;
  align-items: baseline;
  flex-wrap: wrap;
  margin-bottom: 12px;
  overflow-wrap: anywhere;
}
.artifact-link {
  max-width: 100%;
  height: auto;
  white-space: normal;
  text-align: start;
  overflow-wrap: anywhere;
}
.run-questions {
  margin-top: 16px;
  display: grid;
  gap: 10px;
}
.run-question {
  overflow-wrap: anywhere;
}
</style>
