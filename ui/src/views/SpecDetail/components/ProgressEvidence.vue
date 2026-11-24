<script setup lang="ts">
import { computed } from 'vue'
import {
  roleLabels,
  type Evidence,
  type Participant,
  type ProjectVersion
} from '@/services/workbench'
const props = defineProps<{
  evidence: Evidence[]
  versions: ProjectVersion[]
  currentVersions?: ProjectVersion[]
  participants: Participant[]
}>()
function projectLabel(projectId: string) {
  const participants = props.participants.filter(
    (entry) => entry.projectId === projectId
  )
  return participants.length
    ? participants
        .map(
          (participant) =>
            `${roleLabels[participant.role]} · ${participant.label}`
        )
        .join(' / ')
    : projectId
}
const versionRows = computed(() => {
  const uniqueVersions = new Map<string, ProjectVersion>()
  for (const version of props.versions) {
    const key = JSON.stringify([
      version.projectId,
      version.commit,
      version.workingTreeDigest
    ])
    uniqueVersions.set(key, version)
  }
  return Array.from(uniqueVersions.values()).map((version) => ({
    ...version,
    label: projectLabel(version.projectId)
  }))
})
const currentVersionRows = computed(() =>
  (props.currentVersions ?? []).map((version) => ({
    ...version,
    label: projectLabel(version.projectId)
  }))
)
</script>
<template>
  <dl class="evidence-facts">
    <div>
      <dt>依据</dt>
      <dd>
        <ul v-if="evidence.length">
          <li v-for="(item, index) in evidence" :key="index">
            <span>{{ item.label }}</span>
            <span v-if="item.outcome" class="outcome">
              {{
                { passed: '通过', failed: '失败', observed: '已观察' }[
                  item.outcome
                ]
              }}
            </span>
            <a
              v-if="/^https?:\/\//i.test(item.reference)"
              :href="item.reference"
              target="_blank"
              rel="noopener noreferrer"
            >
              {{ item.reference }} ↗
            </a>
            <code v-else>{{ item.reference }}</code>
          </li>
        </ul>
        <span v-else>尚无可核实凭证</span>
      </dd>
    </div>
    <div>
      <dt>来源版本</dt>
      <dd>
        <ul v-if="versionRows.length">
          <li v-for="(version, index) in versionRows" :key="index">
            <strong>{{ version.label }}</strong>
            <code>{{ version.commit || '提交版本未知' }}</code>
            <code v-if="version.workingTreeDigest">
              工作树 {{ version.workingTreeDigest }}
            </code>
          </li>
        </ul>
        <span v-else>版本待核实</span>
      </dd>
    </div>
    <div v-if="currentVersionRows.length">
      <dt>当前工程版本</dt>
      <dd>
        <ul>
          <li v-for="(version, index) in currentVersionRows" :key="index">
            <strong>{{ version.label }}</strong>
            <code>{{ version.commit || '提交版本未知' }}</code>
            <code v-if="version.workingTreeDigest">
              工作树 {{ version.workingTreeDigest }}
            </code>
          </li>
        </ul>
      </dd>
    </div>
  </dl>
</template>
<style scoped>
.evidence-facts {
  margin: 14px 0 0;
}
.evidence-facts > div {
  display: grid;
  grid-template-columns: 76px minmax(0, 1fr);
  gap: 12px;
  margin-top: 12px;
}
dt {
  font-size: 12px;
  color: var(--brand-color-text-secondary);
}
dd {
  margin: 0;
  min-width: 0;
  color: #62666c;
  font-size: 12px;
  line-height: 1.7;
}
ul {
  list-style: none;
  margin: 0;
  padding: 0;
}
li + li {
  margin-top: 10px;
}
code,
a {
  display: block;
  font-size: 11px;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
strong {
  font-weight: 550;
}
.outcome {
  margin-left: 8px;
  color: var(--brand-color-text-secondary);
}
@media (width < 600px) {
  .evidence-facts > div {
    grid-template-columns: minmax(0, 1fr);
    gap: 4px;
  }
}
</style>
