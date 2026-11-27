<script setup lang="ts">
import { computed, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { contentEqual, errorMessage } from '@/services/workbench'
import {
  previewAgentCiJob, type AgentCiMode, type AgentProjectCandidate
} from '@/services/workbench-agent'
import { getExternalLinks, type ExternalLink } from '@/services/workbench-engineering'
import { objectValue } from '@/services/workbench-platform'
import type { PiCiAttachment, PiCiRecovery } from '../usePiDrafts'
import { projectCheckout, type PiCheckout } from '../usePiProjects'
import { useWorkbench } from '@/hooks/useWorkbench'

const props = defineProps<{
  specId: string
  projects: AgentProjectCandidate[]
  registry: AgentProjectCandidate[]
  busy: boolean
  recovery?: PiCiRecovery
}>()
const attachment = defineModel<PiCiAttachment | undefined>()
const id = useId()
const projectId = shallowRef(attachment.value?.job.projectId ?? props.recovery?.projectId ?? '')
const linkId = shallowRef(attachment.value?.job.linkId ?? props.recovery?.linkId ?? '')
const jobRef = shallowRef(String(attachment.value?.job.jobId ?? props.recovery?.jobId ?? ''))
const mode = shallowRef<AgentCiMode>(attachment.value?.mode ?? props.recovery?.mode ?? 'reproduce')
const preview = shallowRef<Omit<PiCiAttachment, 'mode'> | undefined>(attachment.value)
const links = shallowRef<ExternalLink[]>([])
const linksBusy = shallowRef(false)
const linksLoaded = shallowRef(false)
const linksError = shallowRef('')
const previewBusy = shallowRef(false)
const previewError = shallowRef('')
const linksGeneration = shallowRef(0)
const previewRequest = shallowRef<{ projectId: string; linkId: string; jobRef: string; checkout: PiCheckout }>()
const project = computed(() => props.projects.find((item) => item.id === projectId.value))
const checkout = computed(() => projectCheckout(project.value))
const checkoutKey = computed(() => JSON.stringify(checkout.value))
const { externalVersion } = useWorkbench()
function repositoryGroupKey(project?: AgentProjectCandidate) {
  return project?.repositoryGroupKey === undefined ? project?.repositoryKey : project.repositoryGroupKey
}
const repositoryIds = computed(() => {
  const key = repositoryGroupKey(project.value)
  const ids = key ? props.registry.filter((item) => item.active && repositoryGroupKey(item) === key).map((item) => item.id) : []
  return new Set([projectId.value, ...ids])
})
const pipelines = computed(() => links.value.filter((item) =>
  (!item.specId || item.specId === props.specId) && item.projectId && repositoryIds.value.has(item.projectId) &&
  item.provider === 'gitlab' && item.kind === 'pipeline'))
const pipeline = computed(() => pipelines.value.find((item) => item.id === linkId.value))
const jobs = computed(() => {
  const items = objectValue(pipeline.value?.data?.jobs).items
  if (!Array.isArray(items)) return []
  return items.map(objectValue).filter((item) => Number.isSafeInteger(item.id))
    .map((item) => ({ id: String(item.id), name: String(item.name ?? ''), status: String(item.status ?? '') }))
})
const currentPreview = computed(() => {
  const result = preview.value
  return result && result.job.projectId === projectId.value && result.job.linkId === pipeline.value?.id &&
    contentEqual(result.checkout, checkout.value) ? result : undefined
})
const attached = computed(() => {
  const current = attachment.value
  return !!current && current.job.projectId === currentPreview.value?.job.projectId &&
    current.job.linkId === currentPreview.value?.job.linkId &&
    current.job.jobId === currentPreview.value?.job.jobId &&
    current.job.sha === currentPreview.value?.job.sha && current.mode === mode.value &&
    contentEqual(current.checkout, currentPreview.value?.checkout)
})
const matches = computed(() => !!currentPreview.value && currentPreview.value.job.sha === currentPreview.value.checkout.head)
const previewUrl = computed(() => {
  const value = currentPreview.value?.job.url
  if (!value) return undefined
  try {
    const url = new URL(value)
    if (['http:', 'https:'].includes(url.protocol) && !url.username && !url.password) return url.href
  } catch {
    return undefined
  }
  return undefined
})
watch([() => props.specId, linksGeneration, externalVersion], async ([specId], _, onCleanup) => {
  const request = new AbortController()
  onCleanup(() => request.abort())
  linksBusy.value = true
  linksError.value = ''
  try {
    const result = await getExternalLinks(request.signal)
    if (!request.signal.aborted && specId === props.specId) {
      links.value = result
      linksLoaded.value = true
      if (!pipeline.value) linkId.value = ''
    }
  } catch (cause) {
    if (!request.signal.aborted) linksError.value = errorMessage(cause)
  } finally {
    if (!request.signal.aborted) linksBusy.value = false
  }
}, { immediate: true })
watch([projectId, linkId, jobRef, checkoutKey], () => {
  previewRequest.value = undefined
  preview.value = undefined
  previewError.value = ''
}, { flush: 'sync' })
watch(previewRequest, async (target, _, onCleanup) => {
  previewBusy.value = false
  if (!target) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  previewBusy.value = true
  try {
    const result = await previewAgentCiJob(props.specId, {
      projectId: target.projectId, linkId: target.linkId, jobRef: target.jobRef
    }, controller.signal)
    if (!controller.signal.aborted && previewRequest.value === target)
      preview.value = { job: result, checkout: target.checkout }
  } catch (cause) {
    if (!controller.signal.aborted && previewRequest.value === target) previewError.value = errorMessage(cause)
  } finally {
    if (!controller.signal.aborted && previewRequest.value === target) previewBusy.value = false
  }
})
function checkJob() {
  if (!checkout.value || !pipeline.value || !jobRef.value.trim() || previewBusy.value) return
  preview.value = undefined
  previewError.value = ''
  previewRequest.value = { projectId: projectId.value, linkId: linkId.value, jobRef: jobRef.value.trim(), checkout: checkout.value }
}
function attach() {
  const result = currentPreview.value
  if (result && (mode.value === 'compare' || matches.value))
    attachment.value = { ...result, mode: mode.value }
}
</script>

<template>
  <fieldset :disabled="busy" class="pi-ci" :aria-describedby="id + '-hint'">
    <legend>GitLab CI 作业</legend>
    <p :id="id + '-hint'">从当前 Spec 或所选工程已关联的 GitLab 流水线选择一个作业。</p>
    <p v-if="linksBusy">正在读取研发关联…</p>
    <p v-if="linksError" role="alert">
      {{ linksError }} <Button variant="outline" size="sm" type="button" @click="linksGeneration++">重试读取</Button>
    </p>
    <div class="pi-ci-field">
      <label :for="id + '-project'">CI 所属工程</label>
      <select :id="id + '-project'" v-model="projectId" name="ciProject" :disabled="linksBusy || !linksLoaded" @change="linkId = ''; jobRef = ''">
        <option value="">选择本轮参与工程</option>
        <option v-for="item in projects" :key="item.id" :value="item.id">{{ item.name }} · {{ item.rootPath }}</option>
      </select>
    </div>
    <div v-if="project && linksLoaded" class="pi-ci-field">
      <label :for="id + '-pipeline'">关联流水线</label>
      <select :id="id + '-pipeline'" v-model="linkId" name="ciPipeline" :disabled="!pipelines.length" @change="jobRef = ''">
        <option value="">{{ pipelines.length ? '选择流水线' : '此工程尚无关联的 GitLab 流水线' }}</option>
        <option v-for="item in pipelines" :key="item.id" :value="item.id">#{{ item.number }} · {{ item.host }}/{{ item.project }}</option>
      </select>
      <RouterLink v-if="!pipelines.length" :to="{ path: '/specs/' + specId, query: { tab: 'development' } }">查看 Spec 的研发关联</RouterLink>
    </div>
    <template v-if="pipeline">
      <div v-if="jobs.length" class="pi-ci-field">
        <label :for="id + '-job-option'">已读取的作业</label>
        <select :id="id + '-job-option'" name="ciListedJob" :value="jobs.some((item) => item.id === jobRef) ? jobRef : ''"
          @change="jobRef = ($event.target as HTMLSelectElement).value">
          <option value="">选择作业或填写作业 ID</option>
          <option v-for="job in jobs" :key="job.id" :value="job.id">#{{ job.id }} · {{ job.name }} · {{ job.status }}</option>
        </select>
      </div>
      <div class="pi-ci-field">
        <label :for="id + '-job'">作业 ID 或链接</label>
        <input :id="id + '-job'" v-model="jobRef" name="ciJobRef" type="text" autocomplete="off" autocapitalize="none"
          spellcheck="false" :aria-invalid="!!previewError || undefined" :aria-describedby="previewError ? id + '-job-error' : undefined"
          @keydown.enter.prevent="checkJob" />
        <p v-if="previewError" :id="id + '-job-error'" role="alert">{{ previewError }}</p>
      </div>
      <Button variant="outline" size="sm" type="button" :disabled="!jobRef.trim() || previewBusy" @click="checkJob">
        <LoadingIndicator :active="previewBusy" />{{ previewBusy ? '正在读取作业…' : '核对作业和版本' }}
      </Button>
    </template>
    <div v-if="currentPreview" class="pi-ci-preview">
      <strong>{{ currentPreview.job.name }} · #{{ currentPreview.job.jobId }} · {{ currentPreview.job.status }}</strong>
      <a v-if="previewUrl" :href="previewUrl" target="_blank" rel="noopener noreferrer">查看 GitLab 作业</a>
      <p>CI 提交 <code>{{ currentPreview.job.sha }}</code></p>
      <p>本机 HEAD <code>{{ currentPreview.checkout.head }}</code></p>
      <fieldset class="pi-ci-mode" :aria-describedby="id + '-sha'">
        <legend>使用方式</legend>
        <div>
          <input :id="id + '-reproduce'" v-model="mode" :name="id + '-ciMode'" value="reproduce" type="radio" :disabled="!matches" />
          <label :for="id + '-reproduce'">同提交复现</label>
        </div>
        <div>
          <input :id="id + '-compare'" v-model="mode" :name="id + '-ciMode'" value="compare" type="radio" />
          <label :for="id + '-compare'">差异对照</label>
        </div>
        <p :id="id + '-sha'">{{ matches ? 'CI 提交与本机 HEAD 一致。本轮也会读取所选工程的未提交文件。' : 'CI 提交与本机 HEAD 不一致，只能差异对照。' }}</p>
      </fieldset>
      <Button variant="outline" size="sm" type="button" :disabled="attached || (!matches && mode === 'reproduce')" @click="attach">
        {{ attached ? '已附加到本轮' : '附加作业到本轮' }}
      </Button>
    </div>
    <div v-if="attachment" class="pi-ci-attached">
      <p role="status">本轮已附加 #{{ attachment.job.jobId }} · {{ attachment.mode === 'reproduce' ? '同提交复现' : '差异对照' }}</p>
      <Button type="button" variant="ghost" size="sm" @click="attachment = undefined">移除作业</Button>
    </div>
  </fieldset>
</template>

<style scoped>
.pi-ci, .pi-ci-field, .pi-ci-preview, .pi-ci-mode { display: grid; gap: 6px; min-width: 0; }
.pi-ci { gap: 10px; }
.pi-ci legend { margin-bottom: 6px; font-weight: 600; }
.pi-ci label, .pi-ci-preview strong { font-size: 13px; }
.pi-ci p { color: var(--brand-color-text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.pi-ci select, .pi-ci input[type="text"] { min-height: 36px; min-width: 0; padding-inline: 8px; border: 1px solid var(--input); border-radius: var(--radius-md); background: var(--background); color: var(--foreground); font: inherit; text-overflow: ellipsis; }
.pi-ci-preview { border: 1px solid var(--border); border-radius: var(--radius-md); padding: 10px; }
.pi-ci-mode label { margin-inline-start: 6px; }
.pi-ci a { text-decoration: underline; }
.pi-ci-attached { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 6px; }
</style>
