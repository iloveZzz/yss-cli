<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { getAgentSpecView } from '@/services/workbench-agent'
import { errorMessage, contentStatuses, type SpecView } from '@/services/workbench'
import SourcePreview from '@/views/SpecDetail/components/SourcePreview.vue'
import ResourceViewer from '@/views/SpecDetail/components/ResourceViewer.vue'
import { questionStateLabel } from '@/services/workbench-content'
const props = defineProps<{ runId: string; specId: string; running: boolean }>()
const view = shallowRef<SpecView>()
const error = shallowRef('')
const loading = shallowRef(false)
const open = shallowRef(false)
const resource = shallowRef<{ path: string; fragment: string } | null>(null)
const version = computed(
  () => `${view.value?.sourceModifiedAt}:${view.value?.content.artifactDigest}`
)
let generation = 0
onBeforeUnmount(() => {
  generation++
})
async function refresh() {
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
    if (own === generation) loading.value = false
  }
}
watch(
  () => [props.runId, props.specId, open.value],
  () => {
    if (open.value) void refresh()
  }
)
useIntervalFn(() => {
  if (open.value && props.running && !document.hidden) void refresh()
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
      <h2>规格、原型与问题</h2>
      <div class="inline-actions">
        <Button type="button" variant="outline" :aria-expanded="open" @click="open = !open">
          {{ open ? '收起产物' : '阅读运行产物' }}
        </Button>
        <Button v-if="open" type="button" variant="ghost" :disabled="loading" @click="refresh">
          重新读取
        </Button>
      </div>
    </div>
    <p v-if="error" role="alert" class="notice warning">{{ error }}</p>
    <template v-if="open && view">
      <p class="muted">{{ contentStatuses[view.content.status] }} · {{ view.content.scope }}</p>
      <div class="inline-actions resource-shortcuts">
        <template v-for="(item, index) in view.resources" :key="index">
          <a
            v-if="item.record.targetType === 'web' && safeWeb(item.record.target)"
            :href="item.record.target"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ item.record.label }} ↗
          </a>
          <Button v-else type="button" variant="outline" @click="navigate(item.record.target)">
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
        <article
          v-for="question in view.content.questions"
          :key="question.question.id"
          class="diff-file"
        >
          <strong>{{ question.question.id }} · {{ question.question.title }}</strong>
          <p class="muted">{{ questionStateLabel(question) }}</p>
          <p v-if="question.response" class="preserve-lines">
            {{ question.response.answer || question.response.reason }}
          </p>
          <p v-if="question.question.resolution" class="preserve-lines">
            {{ question.question.resolution }}
          </p>
        </article>
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
