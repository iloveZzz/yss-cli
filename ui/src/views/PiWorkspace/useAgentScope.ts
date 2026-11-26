import { computed } from 'vue'
import { useLocalStorage } from '@vueuse/core'
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'

const { catalog } = useWorkbenchCatalog()
const scopeOptions = computed(() => {
  const projects = new Map(catalog.value?.projects.filter((project) => project.active)
    .map((project) => [project.id, project.name]))
  return (catalog.value?.specs ?? [])
    .filter((spec) => spec.active && projects.has(spec.projectId))
    .toSorted((left, right) =>
      right.updatedAt.localeCompare(left.updatedAt) || left.title.localeCompare(right.title, 'zh-CN'))
    .map((record) => ({ value: record.id, label: `${record.title} · ${projects.get(record.projectId)}` }))
})

export function useAgentScope() {
  const lastAgentSpec = useLocalStorage('yss:agent-last-spec', '', { writeDefaults: false })
  const preferredSpecId = computed(() =>
    scopeOptions.value.find((option) => option.value === lastAgentSpec.value)?.value ?? scopeOptions.value[0]?.value
  )
  return { scopeOptions, preferredSpecId, lastAgentSpec }
}
