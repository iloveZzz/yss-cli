import { computed } from 'vue'
import { useLocalStorage } from '@vueuse/core'
import { useWorkbench } from '@/hooks/useWorkbench'

const { snapshot, activeSpecs } = useWorkbench()
const scopeOptions = computed(() => {
  const projects = new Map(snapshot.value?.projects.filter((project) => project.available)
    .map((project) => [project.record.id, project.record.name]))
  return activeSpecs.value
    .filter((spec) => spec.sourceHealth === 'available' && projects.has(spec.record.projectId))
    .toSorted(({ record: left }, { record: right }) =>
      (right.lastVisitedAt ?? '').localeCompare(left.lastVisitedAt ?? '') ||
      right.updatedAt.localeCompare(left.updatedAt) || left.title.localeCompare(right.title, 'zh-CN'))
    .map(({ record }) => ({ value: record.id, label: `${record.title} · ${projects.get(record.projectId)}` }))
})

export function useAgentScope() {
  const lastAgentSpec = useLocalStorage('yss:agent-last-spec', '', { writeDefaults: false })
  const preferredSpecId = computed(() =>
    scopeOptions.value.find((option) => option.value === lastAgentSpec.value)?.value ?? scopeOptions.value[0]?.value
  )
  return { scopeOptions, preferredSpecId, lastAgentSpec }
}
