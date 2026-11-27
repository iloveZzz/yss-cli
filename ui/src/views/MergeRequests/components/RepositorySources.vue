<script setup lang="ts">
import { computed, shallowRef, useId, useTemplateRef, watch } from 'vue'
import { ExternalLink } from '@lucide/vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import ModalDialog from '@/components/ModalDialog.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  type MergeRequestIssue, type MergeRequestSources, type MergeRequestSourceSelection
} from '@/services/workbench-merge-requests'
import { issueLabel, repositoryLabel, safeHttpUrl } from '../schemas/requests'

const open = defineModel<boolean>({ required: true })
const props = defineProps<{
  sources?: MergeRequestSources
  loading: boolean
  saving: boolean
  loadError: string
  saveError: string
  repositoryReads: ReadonlyMap<string, { issue?: MergeRequestIssue; readAt?: string; pending: boolean }>
}>()
const emit = defineEmits<{ save: [selected: MergeRequestSourceSelection[]]; retry: []; dismissError: [] }>()

const headingId = useId()
const sourceList = useTemplateRef<HTMLElement>('source-list')
const basisLabels = { home: '产品 Home 默认来源', origin: 'origin 默认来源', manual: '额外来源' }
const repositoryById = computed(() => new Map(props.sources?.repositories.map((repository) => [repository.id, repository])))
const keyFor = (projectId: string, repositoryId: string) => JSON.stringify([projectId, repositoryId])
const initialSelection = computed(() => new Set(props.sources?.projects.flatMap((project) =>
  project.candidates.filter((candidate) => candidate.selected)
    .map((candidate) => keyFor(project.projectId, candidate.repositoryId)))))
const checked = shallowRef(new Set<string>())
const changed = computed(() => checked.value.size !== initialSelection.value.size ||
  [...checked.value].some((key) => !initialSelection.value.has(key)))

watch([open, initialSelection], ([value]) => {
  if (value) checked.value = new Set(initialSelection.value)
})

function update(projectId: string, repositoryId: string, value: boolean | 'indeterminate') {
  if (props.saving || props.loading) return
  const key = keyFor(projectId, repositoryId)
  const next = new Set(checked.value)
  if (value === true) next.add(key)
  else next.delete(key)
  checked.value = next
  if (props.saveError) emit('dismissError')
}

function submit() {
  if (!changed.value || !props.sources || props.saving || props.loading) return
  emit('save', props.sources.projects.flatMap((project) =>
    project.candidates
      .filter((candidate) => checked.value.has(keyFor(project.projectId, candidate.repositoryId)))
      .map((candidate) => ({ projectId: project.projectId, repositoryId: candidate.repositoryId }))))
}

function repositoryStatus(id: string) {
  const read = props.repositoryReads.get(id)
  if (read?.issue) return issueLabel(read.issue)
  if (read?.pending) return read.readAt ? '已读取部分分页' : '等待读取'
  if (read?.readAt) return '已读取'
  return repositoryById.value.get(id)?.included ? '当前未读取' : undefined
}

function retrySources() {
  sourceList.value?.focus()
  emit('retry')
}
</script>

<template>
  <ModalDialog v-model="open" title="仓库来源" :busy="saving" :style="{ width: 'min(620px, calc(100% - 24px))' }">
    <p :id="headingId" class="muted source-intro">默认来源随工程配置。可按工程纳入或撤回额外远端。</p>
    <div ref="source-list" class="source-list" role="region" :aria-labelledby="headingId" :aria-busy="loading || saving" tabindex="0">
      <LoadingSkeleton v-if="loading && !sources" label="正在读取仓库来源" variant="detail" :rows="3" />
      <Alert v-else-if="loadError && !sources" class="notice warning">
        <AlertDescription>{{ loadError }}</AlertDescription>
        <Button type="button" variant="outline" class="justify-self-start" @click="retrySources">重试读取</Button>
      </Alert>
      <template v-else-if="sources?.projects.length">
        <fieldset v-for="(project, projectIndex) in sources.projects" :key="project.projectId" class="source-project">
          <legend>{{ project.name }}</legend>
          <p v-if="project.issue" class="project-issue">{{ project.issue }}</p>
          <p v-if="!project.candidates.length && !project.issue" class="muted">暂无可核对的远端。</p>
          <template v-for="(candidate, candidateIndex) in project.candidates" :key="candidate.repositoryId">
            <div v-if="repositoryById.get(candidate.repositoryId)" class="source-option">
              <label :for="headingId + '-' + projectIndex + '-' + candidateIndex" class="source-choice">
                <Checkbox
                  :id="headingId + '-' + projectIndex + '-' + candidateIndex"
                  :model-value="candidate.default || checked.has(keyFor(project.projectId, candidate.repositoryId))"
                  :disabled="saving || loading || candidate.default"
                  @update:model-value="update(project.projectId, candidate.repositoryId, $event)"
                />
                <span>
                  <strong>{{ repositoryLabel(repositoryById.get(candidate.repositoryId)!) }}</strong>
                  <small>{{ candidate.remote }} · {{ candidate.default ? basisLabels[candidate.basis] : '额外来源' }}<template v-if="repositoryStatus(candidate.repositoryId)"> · {{ repositoryStatus(candidate.repositoryId) }}</template></small>
                </span>
              </label>
              <Button
                v-if="candidate.default && candidate.selected"
                type="button" variant="ghost" :disabled="saving || loading"
                @click="update(project.projectId, candidate.repositoryId, !checked.has(keyFor(project.projectId, candidate.repositoryId)))"
              >{{ checked.has(keyFor(project.projectId, candidate.repositoryId)) ? '撤回额外纳入' : '保留额外纳入' }}</Button>
              <Button
                v-if="safeHttpUrl(repositoryById.get(candidate.repositoryId)?.url)"
                as-child variant="ghost" size="icon"
              >
                <a :href="safeHttpUrl(repositoryById.get(candidate.repositoryId)?.url)" target="_blank" rel="noopener noreferrer" :aria-label="'打开仓库 ' + repositoryLabel(repositoryById.get(candidate.repositoryId)!)">
                  <ExternalLink aria-hidden="true" />
                </a>
              </Button>
            </div>
          </template>
        </fieldset>
      </template>
      <div v-else class="source-empty">
        <p>暂无可浏览仓库</p>
        <p class="muted">先在「产品与工程」登记并启用工程。</p>
        <Button as-child variant="outline"><RouterLink to="/projects">前往产品与工程</RouterLink></Button>
      </div>
    </div>
    <p v-if="saveError" role="alert" class="source-error">{{ saveError }}</p>
    <footer class="source-actions">
      <LoadingIndicator :active="saving" label="正在保存仓库来源" />
      <Button type="button" variant="outline" :disabled="saving" @click="open = false">{{ changed ? '取消' : '关闭' }}</Button>
      <Button type="button" :disabled="!changed || loading || saving" @click="submit">保存来源</Button>
    </footer>
  </ModalDialog>
</template>

<style scoped>
.source-intro { margin-block-end: 10px; }
.source-list { display: grid; gap: 11px; min-height: 100px; max-height: min(51dvh, 520px); overflow: auto; padding: 5px 3px 8px; }
.source-project { min-width: 0; padding: 4px 10px 8px; border: 1px solid var(--yss-colors-border); border-radius: var(--brand-border-radius); }
.source-project legend { max-width: 100%; padding: 0 5px; font-weight: 600; overflow-wrap: anywhere; }
.project-issue { margin: 4px 3px 5px; color: var(--brand-color-text-secondary); overflow-wrap: anywhere; }
.source-option { display: flex; flex-wrap: wrap; align-items: center; gap: 5px; padding-inline: 4px; }
.source-option + .source-option { border-top: 1px solid var(--yss-colors-border); }
.source-choice { display: flex; flex: 1 1 150px; align-items: center; gap: 8px; min-width: 0; min-height: 44px; padding-block: 6px; }
.source-choice strong, .source-choice small { display: block; overflow-wrap: anywhere; }
.source-choice strong { font-weight: 500; }
.source-choice small { color: var(--brand-color-text-secondary); }
.source-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; align-items: center; gap: 8px; margin-block-start: 10px; }
.source-error { color: var(--yss-colors-error-text); }
.source-empty { display: grid; justify-items: start; align-content: center; gap: 8px; padding: 5px 2px; }
</style>
