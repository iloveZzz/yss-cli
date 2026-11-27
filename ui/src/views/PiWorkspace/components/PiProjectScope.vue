<script setup lang="ts">
import { computed, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import type {
  AgentHomeEntry, AgentProject, AgentProjectCandidate, AgentProjects, ProjectAccessMode
} from '@/services/workbench-agent'

const props = defineProps<{
  mainId: string
  projects: AgentProject[]
  scope?: AgentProjects
  busy: boolean
  loading: boolean
  error: string
  grantIssue: string
  locked: boolean
  participatingIds: string[]
  perProject: boolean
  commandsUnavailable?: boolean
  commandsMessage?: string | null
}>()
const emit = defineEmits<{ refresh: []; clearStaleProjectGrants: [] }>()
const selected = defineModel<string[]>('selected', { required: true })
const modes = defineModel<Record<string, ProjectAccessMode>>('modes', { required: true })
const id = useId()
const homeId = shallowRef('')
const manualProject = shallowRef('')
const homeCheckout = shallowRef<Record<string, string>>({})
const candidates = computed(() => new Map(props.scope?.projects.map((project) => [project.id, project])))
const knownProjects = computed(() => new Map(props.projects.map((project) => [project.id, project])))
const participants = computed(() => [...new Set([props.mainId, ...props.participatingIds])]
  .filter(Boolean)
  .map((projectId) => {
    const detail = candidates.value.get(projectId)
    const project = detail ?? knownProjects.value.get(projectId)
    return {
      id: projectId, name: project?.name ?? projectId, rootPath: project?.rootPath,
      status: detail?.status ?? (props.scope ? 'unavailable' : undefined),
      branch: detail?.branch, head: detail?.head
    }
  }))
const manualChoices = computed(() => (props.scope?.projects ?? [])
  .filter((project) => project.status === 'ready' && project.head && !participants.value.some((item) => item.id === project.id)))
const home = computed(() => props.scope?.homes.find((item) => item.id === homeId.value) ??
  (props.scope?.homes.length === 1 ? props.scope.homes[0] : undefined))
const labels = {
  ready: '可用', inactive: '已停用', unavailable: '本机路径或 Git 不可用', unbound: '未绑定本机工程', stale: '地图待重新导入'
}
const accessChoices: { value: ProjectAccessMode; label: string }[] = [
  { value: 'read', label: '只读' },
  { value: 'edit', label: '编辑' },
  { value: 'execute', label: '编辑并运行离线命令' }
]
watch(manualChoices, (choices) => {
  if (!choices.some((project) => project.id === manualProject.value)) manualProject.value = ''
})
watch(() => props.scope?.homes, (homes) => {
  if (!homes?.some((item) => item.id === homeId.value)) homeId.value = ''
  homeCheckout.value = {}
})
watch(homeId, () => { homeCheckout.value = {} })
function add(projectId: string) {
  const project = candidates.value.get(projectId)
  if (!project || project.status !== 'ready' || !project.head ||
    participants.value.some((item) => item.id === projectId)) return
  selected.value = [...selected.value, projectId]
}
function remove(projectId: string) {
  selected.value = selected.value.filter((item) => item !== projectId)
  const next = { ...modes.value }
  delete next[projectId]
  modes.value = next
}
function addHomeCheckout(entryId: string) {
  const projectId = homeCheckout.value[entryId]
  if (!projectId) return
  add(projectId)
  homeCheckout.value = { ...homeCheckout.value, [entryId]: '' }
}
function options(entry: AgentHomeEntry): AgentProjectCandidate[] {
  return entry.projectIds.flatMap((projectId) => {
    const project = candidates.value.get(projectId)
    return project ? [project] : []
  })
}
</script>

<template>
  <fieldset :disabled="busy" class="pi-scope" :aria-describedby="id + '-help'">
    <legend>参与工程</legend>
    <p :id="id + '-help'">
      {{ locked ? '沿用本会话的工程和本机路径。' : '主工程始终参与。可以按本机检出路径添加其他工程。' }}
      <template v-if="perProject">权限按工程和轮次生效。</template>
    </p>
    <p v-if="commandsUnavailable" role="status">离线命令不可用：{{ commandsMessage || '命令隔离尚未通过检测。' }} 已选离线命令权限需改为只读或编辑后才能运行。</p>
    <div class="pi-scope-actions">
      <Button type="button" variant="outline" size="sm" :disabled="loading" @click="emit('refresh')">
        <LoadingIndicator :active="loading" />{{ loading ? '正在核对工程…' : '重新核对工程' }}
      </Button>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <div v-if="grantIssue" class="pi-scope-actions">
      <p role="status">{{ grantIssue }}</p>
      <Button type="button" variant="outline" size="sm" :disabled="loading" @click="emit('clearStaleProjectGrants')">
        清除过期授权
      </Button>
    </div>
    <div v-for="project in participants" :key="project.id" class="pi-scope-project">
      <div class="pi-scope-project-header">
        <strong>{{ project.name }}<small v-if="project.id === mainId"> · 主工程</small></strong>
        <Button v-if="!locked && project.id !== mainId" type="button" variant="ghost" size="sm"
          :aria-label="'移除工程 ' + project.name" @click="remove(project.id)">移除</Button>
      </div>
      <code v-if="project.rootPath">{{ project.rootPath }}</code>
      <p v-if="project.head" class="pi-scope-git">
        <span>{{ project.branch || '游离 HEAD' }}</span>
        <code>{{ project.head }}</code>
      </p>
      <p v-if="project.status && project.status !== 'ready'">
        {{ labels[project.status] }}
        <RouterLink to="/projects">检查工程登记</RouterLink>
      </p>
      <div v-if="perProject && project.status === 'ready' && project.head" class="pi-scope-access">
        <label :for="id + '-mode-' + project.id">{{ project.name }}的权限</label>
        <select :id="id + '-mode-' + project.id" :name="'projectMode-' + project.id"
          :value="modes[project.id] ?? 'read'" :disabled="loading"
          @change="modes = { ...modes, [project.id]: ($event.target as HTMLSelectElement).value as ProjectAccessMode }">
          <option v-for="choice in accessChoices" :key="choice.value" :value="choice.value"
            :disabled="choice.value === 'execute' && commandsUnavailable">{{ choice.label }}</option>
        </select>
      </div>
    </div>

    <template v-if="!locked && scope">
      <div v-if="scope.homes.length > 1" class="pi-scope-access">
        <label :for="id + '-home'">产品 Home</label>
        <select :id="id + '-home'" v-model="homeId" name="home" :disabled="loading">
          <option value="">选择产品 Home</option>
          <option v-for="item in scope.homes" :key="item.id" :value="item.id">{{ item.root }}</option>
        </select>
      </div>
      <div v-if="home" class="pi-scope-project">
        <strong>产品 Home</strong>
        <code>{{ home.root }} / {{ home.sourcePath }}</code>
        <p v-if="home.stale">地图已变化。<RouterLink :to="{ path: '/projects', hash: '#home-heading' }">重新导入地图</RouterLink></p>
        <div v-for="entry in home.entries" :key="entry.id" class="pi-scope-home-entry">
          <span>{{ entry.name || entry.id }}<small> · {{ labels[entry.status] }}</small></span>
          <code v-if="entry.rootPath">{{ entry.rootPath }}</code>
          <div v-if="!home.stale && options(entry).length" class="pi-scope-access">
            <label :for="id + '-entry-' + entry.id">添加 {{ entry.name || entry.id }} 的本机检出</label>
            <select :id="id + '-entry-' + entry.id" :name="'homeCheckout-' + entry.id"
              :value="homeCheckout[entry.id] ?? ''" :disabled="loading"
              @change="homeCheckout = { ...homeCheckout, [entry.id]: ($event.target as HTMLSelectElement).value }">
              <option value="">请选择实际路径</option>
              <option v-for="project in options(entry)" :key="project.id" :value="project.id"
                :disabled="project.status !== 'ready' || !project.head || participants.some((item) => item.id === project.id)">
                {{ project.rootPath }} · {{ project.head ? (project.branch || '游离 HEAD') + ' · ' + project.head.slice(0, 12) : labels[project.status] }}
              </option>
            </select>
            <div class="pi-scope-actions">
              <Button type="button" variant="outline" size="sm" :disabled="loading || !homeCheckout[entry.id]"
                @click="addHomeCheckout(entry.id)">添加检出</Button>
            </div>
          </div>
          <RouterLink v-if="entry.status !== 'ready' && !home.stale"
            :to="{ path: '/projects', hash: '#home-heading' }">检查 Home 绑定</RouterLink>
        </div>
      </div>
      <div class="pi-scope-access">
        <label :for="id + '-registered'">从已登记工程添加</label>
        <select :id="id + '-registered'" v-model="manualProject" name="registeredProject" :disabled="loading || !manualChoices.length">
          <option value="">{{ manualChoices.length ? '选择本机工程' : '没有其他可用工程' }}</option>
          <option v-for="project in manualChoices" :key="project.id" :value="project.id">
            {{ project.name }} · {{ project.rootPath }} · {{ project.branch || '游离 HEAD' }} · {{ project.head?.slice(0, 12) }}
          </option>
        </select>
        <div class="pi-scope-actions">
          <Button type="button" variant="outline" size="sm" :disabled="loading || !manualProject" @click="add(manualProject)">添加工程</Button>
          <RouterLink to="/projects">管理工程与产品 Home</RouterLink>
        </div>
      </div>
    </template>
  </fieldset>
</template>

<style scoped>
.pi-scope { display: grid; gap: 10px; min-width: 0; }
.pi-scope legend { margin-bottom: 7px; font-weight: 600; }
.pi-scope p, .pi-scope small, .pi-scope code { color: var(--brand-color-text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.pi-scope-project { display: grid; gap: 5px; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-md); min-width: 0; }
.pi-scope-project-header, .pi-scope-actions, .pi-scope-git { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; }
.pi-scope-project-header { justify-content: space-between; font-size: 13px; }
.pi-scope-home-entry { display: grid; gap: 5px; padding-block: 7px; font-size: 13px; }
.pi-scope-home-entry + .pi-scope-home-entry { border-top: 1px solid var(--border); }
.pi-scope-access { display: grid; gap: 5px; min-width: 0; font-size: 13px; }
.pi-scope select { width: 100%; min-height: 36px; min-width: 0; padding-inline: 8px; border: 1px solid var(--input); border-radius: var(--radius-md); background: var(--background); color: var(--foreground); font: inherit; text-overflow: ellipsis; }
.pi-scope a { text-decoration: underline; }
</style>
