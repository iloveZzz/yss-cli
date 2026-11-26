<script setup lang="ts">
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Alert } from '@/components/ui/alert'
import { computed, shallowRef, watch, onBeforeUnmount } from 'vue'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  dateLabel,
  decisionLabel,
  errorMessage,
  loadLifecycle,
  stages,
  progressLabels,
  validityLabels,
  roleLabels,
  type GoalRecord,
  type ReportRecord,
  type OverrideRecord,
  type DecisionRecord,
  type ProgressHistory
} from '@/services/workbench'
import ProgressEvidence from './ProgressEvidence.vue'
import { sorted } from '@/lib/collections'
const props = defineProps<{ specId: string; goalVersion: string | null }>()
const { snapshot } = useWorkbench()
const expanded = shallowRef(false)
const history = shallowRef<ProgressHistory>()
const limit = shallowRef(50)
const error = shallowRef('')
const loading = shallowRef(false)
let controller: AbortController | undefined
async function read() {
  controller?.abort()
  if (!expanded.value) {
    loading.value = false
    return
  }
  const own = new AbortController()
  controller = own
  loading.value = true
  try {
    const result = await loadLifecycle(props.specId, own.signal)
    if (!own.signal.aborted) {
      history.value = result.progressHistory
      error.value = ''
    }
  } catch (cause) {
    if (!own.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (controller === own) loading.value = false
  }
}
watch(
  [
    expanded,
    () => props.specId,
    () => snapshot.value?.revision,
    () => snapshot.value?.progress?.[props.specId]?.evidenceDigest
  ],
  () => void read()
)
onBeforeUnmount(() => controller?.abort())
type HistoryEntry =
  | { kind: 'goal'; at: string; id: string; record: GoalRecord }
  | { kind: 'report'; at: string; id: string; record: ReportRecord }
  | { kind: 'override'; at: string; id: string; record: OverrideRecord }
  | { kind: 'decision'; at: string; id: string; record: DecisionRecord }
const entries = computed<HistoryEntry[]>(() => {
  const value = history.value
  if (!value) return []
  const result: HistoryEntry[] = [
    ...value.goals.map((record) => ({
      kind: 'goal' as const,
      at: record.createdAt,
      id: record.goalVersion,
      record
    })),
    ...value.reports.map((record) => ({
      kind: 'report' as const,
      at: record.recordedAt,
      id: record.id,
      record
    })),
    ...value.overrides.map((record) => ({
      kind: 'override' as const,
      at: record.clearedAt ?? record.recordedAt,
      id: record.id,
      record
    })),
    ...value.decisions.map((record) => ({
      kind: 'decision' as const,
      at: record.recordedAt,
      id: record.id,
      record
    }))
  ]
  return sorted(result, (left, right) => right.at.localeCompare(left.at))
})
const labels = {
  goal: '交付目标',
  report: '阶段上报',
  override: '人工更正'
}
function projectName(id: string | null | undefined) {
  return (
    snapshot.value?.projects.find((project) => project.record.id === id)?.record
      .name ??
    id ??
    '整个阶段'
  )
}
function participantsFor(goalVersion: string) {
  return (
    history.value?.goals.find((goal) => goal.goalVersion === goalVersion)
      ?.participants ?? []
  )
}
</script>
<template>
  <Collapsible
    class="progress-history"
    v-model:open="expanded"
  >
    <CollapsibleTrigger>版本与进展历史</CollapsibleTrigger>
    <CollapsibleContent>
      <div v-if="expanded" class="history-body">
        <LoadingSkeleton v-if="loading && !history" variant="text" :rows="4" label="正在读取历史…" />
        <LoadingIndicator v-else :active="loading" label="正在更新历史…" />
        <Alert v-if="error" role="alert" class="notice error">
          {{ error }}
          <Button type="button" variant="outline" @click="read">重试</Button>
        </Alert>
        <p v-if="!loading && !entries.length" class="muted">
          暂无进展记录。
        </p>
        <ol v-if="entries.length" class="history-entries">
          <li
            v-for="entry in entries.slice(0, limit)"
            :key="entry.kind + ':' + entry.id"
          >
            <Collapsible class="history-entry">
              <CollapsibleTrigger>
                <span>
                  <strong>{{ entry.kind === 'decision' ? decisionLabel(entry.record) : labels[entry.kind] }}</strong>
                  <span v-if="entry.kind !== 'goal'">
                    · {{ stages[entry.record.stage] }}
                  </span>
                  <span class="version-state">
                    {{
                      entry.record.goalVersion === goalVersion
                        ? '当前目标版本'
                        : '历史目标版本'
                    }}
                  </span>
                </span>
                <time :datetime="entry.at">{{ dateLabel(entry.at) }}</time>
              </CollapsibleTrigger>
            <CollapsibleContent>
              <div class="entry-detail">
                <code class="version-code">
                  目标 {{ entry.record.goalVersion }}
                </code>
                <template v-if="entry.kind === 'goal'">
                  <p>{{ entry.record.scope }}</p>
                  <p>
                    交付位置 · {{ entry.record.target.label }}
                    <code>{{ entry.record.target.reference }}</code>
                  </p>
                  <code>规格摘要 {{ entry.record.specDigest }}</code>
                  <ul class="scope-history">
                    <li v-for="scope in entry.record.stages" :key="scope.stage">
                      {{ stages[scope.stage] }} ·
                      {{ scope.applicable ? '适用' : '不适用' }}
                      <span v-if="scope.reason">· {{ scope.reason }}</span>
                    </li>
                  </ul>
                  <p v-if="entry.record.participants.length">
                    参与工程 ·
                    {{
                      entry.record.participants
                        .map(
                          (participant) =>
                            roleLabels[participant.role] +
                            ' · ' +
                            participant.label
                        )
                        .join('，')
                    }}
                  </p>
                  <ul>
                    <li
                      v-for="reference in entry.record.acceptanceRefs"
                      :key="reference"
                    >
                      {{ reference }}
                    </li>
                  </ul>
                </template>
                <template v-else-if="entry.kind === 'report'">
                  <p>
                    {{ roleLabels[entry.record.role] }} ·
                    {{ projectName(entry.record.projectId) }} ·
                    {{ progressLabels[entry.record.progress] }} ·
                    {{ validityLabels[entry.record.validity] }}
                  </p>
                  <p class="preserve-lines">{{ entry.record.summary }}</p>
                  <p v-if="entry.record.blocker" class="warning-text">
                    {{ entry.record.blocker }}
                  </p>
                  <p v-if="entry.record.nextAction">
                    下一步 · {{ entry.record.nextAction }}
                  </p>
                  <code>上报 {{ entry.record.id }}</code>
                  <code>规格摘要 {{ entry.record.specDigest }}</code>
                  <ProgressEvidence
                    :evidence="entry.record.evidence"
                    :versions="entry.record.versions"
                    :participants="participantsFor(entry.record.goalVersion)"
                  />
                </template>
                <template v-else-if="entry.kind === 'override'">
                  <p>
                    {{ projectName(entry.record.projectId) }} ·
                    {{ progressLabels[entry.record.progress] }} ·
                    {{ entry.record.clearedAt ? '已清除' : '更正记录' }}
                  </p>
                  <p class="preserve-lines">原因 · {{ entry.record.reason }}</p>
                  <p>记录时间 · {{ dateLabel(entry.record.recordedAt) }}</p>
                  <p v-if="entry.record.clearedAt">
                    清除时间 · {{ dateLabel(entry.record.clearedAt) }}
                  </p>
                </template>
                <template v-else>
                  <p>{{ entry.record.source }}</p>
                  <p class="preserve-lines">{{ entry.record.scope }}</p>
                  <code>凭证摘要 {{ entry.record.evidenceDigest }}</code>
                  <code v-if="entry.record.stageEvidenceDigest">
                    阶段凭证摘要 {{ entry.record.stageEvidenceDigest }}
                  </code>
                </template>
              </div>
            </CollapsibleContent>
            </Collapsible>
          </li>
        </ol>
        <Button
          v-if="entries.length > limit"
          type="button"
          variant="outline"
          @click="limit += 50"
        >
          继续显示（{{ Math.min(limit, entries.length) }} / {{ entries.length }}）
        </Button>
      </div>
    </CollapsibleContent>
  </Collapsible>
</template>
<style scoped>
.progress-history {
  margin: 20px 0;
  border-top: 1px solid var(--yss-colors-border);
}
.progress-history > [data-slot='collapsible-trigger'] {
  padding: 16px 0;
  color: #62666c;
  cursor: pointer;
  font-size: 13px;
}
.history-body {
  padding-bottom: 14px;
}
.history-entries {
  list-style: none;
  padding: 0;
  margin: 0 0 16px;
}
.history-entries > li {
  border-bottom: 1px solid var(--yss-colors-border);
}
.history-entry > [data-slot='collapsible-trigger'] {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 0;
  cursor: pointer;
  font-size: 12px;
}
.history-entry strong {
  font-weight: 550;
}
.version-state {
  margin-left: 12px;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
}
time {
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  white-space: nowrap;
}
.entry-detail {
  padding: 0 0 16px;
  font-size: 12px;
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.entry-detail p {
  margin-top: 8px;
}
.entry-detail code {
  display: block;
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.version-code {
  margin-bottom: 12px;
}
.scope-history {
  padding-left: 18px;
}
@media (width < 600px) {
  .history-entry > [data-slot='collapsible-trigger'] {
    flex-direction: column;
    gap: 5px;
  }
  .version-state {
    display: block;
    margin: 4px 0 0;
  }
}
</style>
