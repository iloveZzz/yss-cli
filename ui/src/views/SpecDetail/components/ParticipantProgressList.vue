<script setup lang="ts">
import { Button } from '@/components/ui/button'
import {
  roleLabels,
  progressLabels,
  validityLabels,
  type ParticipantProgress,
  type Progress
} from '@/services/workbench'
import ProgressEvidence from './ProgressEvidence.vue'
defineProps<{
  participants: ParticipantProgress[]
  busy: boolean
  canCorrect: boolean
}>()
const emit = defineEmits<{
  correct: [projectId: string, progress: Progress]
  clear: [projectId: string]
}>()
</script>
<template>
  <section
    v-if="participants.length"
    class="participant-section"
    aria-label="跨仓进展"
  >
    <h3>跨仓进展</h3>
    <details
      v-for="participant in participants"
      :key="participant.role + ':' + participant.projectId"
      class="participant-detail"
    >
      <summary>
        <span class="participant-title">
          <strong>
            {{ roleLabels[participant.role] }} · {{ participant.label }}
          </strong>
          <span>{{ participant.summary || '进展待核实' }}</span>
        </span>
        <span class="participant-state">
          {{
            participant.validity === 'current'
              ? progressLabels[participant.progress]
              : validityLabels[participant.validity]
          }}
        </span>
      </summary>
      <div class="participant-body">
        <p class="path-text">工程 {{ participant.projectId }}</p>
        <p v-if="participant.blocker" class="warning-text">
          {{ participant.blocker }}
        </p>
        <p v-if="participant.nextAction">
          下一步 · {{ participant.nextAction }}
        </p>
        <ProgressEvidence
          :evidence="participant.evidence"
          :versions="participant.versions"
          :participants="[participant]"
        />
        <p v-if="participant.currentVersion" class="current-version">
          当前工程版本
          <code>{{ participant.currentVersion.commit || '提交未知' }}</code>
          <code v-if="participant.currentVersion.workingTreeDigest">
            工作树 {{ participant.currentVersion.workingTreeDigest }}
          </code>
        </p>
        <p v-if="participant.overrideRecord" class="override-reason">
          人工更正原因 · {{ participant.overrideRecord.reason }}
        </p>
        <ul v-if="participant.conflicts.length" class="notice warning">
          <li v-for="conflict in participant.conflicts" :key="conflict">
            {{ conflict }}
          </li>
        </ul>
        <div class="inline-actions participant-actions">
          <Button
            type="button"
            variant="outline"
            :disabled="busy || !canCorrect"
            @click="
              emit('correct', participant.projectId, participant.progress)
            "
          >
            更正此工程
          </Button>
          <Button
            v-if="participant.hasOverride"
            type="button"
            variant="ghost"
            :disabled="busy"
            @click="emit('clear', participant.projectId)"
          >
            清除更正
          </Button>
        </div>
      </div>
    </details>
  </section>
</template>
<style scoped>
.participant-section {
  margin-top: 24px;
}
h3 {
  font-size: 14px;
  font-weight: 600;
  margin-bottom: 12px;
}
.participant-detail {
  border-top: 1px solid var(--yss-colors-border);
}
.participant-detail > summary {
  display: flex;
  align-items: center;
  gap: 16px;
  justify-content: space-between;
  padding: 14px 0;
  cursor: pointer;
}
.participant-title {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.participant-title strong {
  font-weight: 550;
}
.participant-title > span,
.participant-state {
  font-size: 11px;
  color: #717780;
}
.participant-state {
  flex-shrink: 0;
}
.participant-body {
  padding-bottom: 16px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.participant-actions {
  margin-top: 14px;
}
.current-version {
  margin-top: 12px;
  color: #717780;
  font-size: 11px;
}
.current-version code {
  display: block;
  overflow-wrap: anywhere;
}
.override-reason {
  margin-top: 12px;
  color: #9b5a0e;
  font-size: 12px;
  overflow-wrap: anywhere;
}
@media (width < 600px) {
  .participant-detail > summary {
    flex-wrap: wrap;
    gap: 8px;
  }
}
</style>
