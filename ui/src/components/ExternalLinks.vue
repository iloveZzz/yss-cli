<script setup lang="ts">
import { computed, reactive, shallowRef, watch, onBeforeUnmount, useId } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { useWorkbench } from '@/hooks/useWorkbench'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import { errorMessage, dateLabel, roleLabels } from '@/services/workbench'
import { getJob, terminalJob, objectValue, readableValue } from '@/services/workbench-platform'
import {
  getExternalLinks,
  addExternalLink,
  refreshExternalLink,
  type ExternalLink
} from '@/services/workbench-engineering'
const props = defineProps<{ projectId?: string; specId?: string }>()
const { snapshot, externalVersion } = useWorkbench()
const links = shallowRef<ExternalLink[]>([])
const selectedProjectId = shallowRef(props.projectId ?? '')
const sourceProjectId = computed(() => props.projectId ?? snapshot.value?.specs.find((spec) => spec.record.id === props.specId)?.record.projectId)
const projectOptions = computed(() => {
  const participants = props.specId ? snapshot.value?.progress?.[props.specId]?.goal?.participants ?? [] : []
  const ids = [...new Set([sourceProjectId.value, ...participants.map((participant) => participant.projectId)].filter((id): id is string => !!id))]
  return ids.map((id) => {
    const project = snapshot.value?.projects.find((entry) => entry.record.id === id)
    const roles = participants.filter((entry) => entry.projectId === id).map((entry) => roleLabels[entry.role])
    return { value: id, label: [project?.record.name ?? id, roles.join(' / ')].filter(Boolean).join(' · ') }
  })
})
watch(projectOptions, (options) => {
  if (!options.some((option) => option.value === selectedProjectId.value))
    selectedProjectId.value = sourceProjectId.value ?? options[0]?.value ?? ''
}, { immediate: true })
const formId = useId()
const form = reactive({
  provider: 'github' as ExternalLink['provider'],
  host: 'github.com',
  project: '',
  kind: 'issue' as ExternalLink['kind'],
  number: 1
})
const error = shallowRef('')
const busy = shallowRef(false)
const jobId = shallowRef('')
const pendingJobs = shallowRef<Record<string, string>>({})
const expanded = shallowRef(false)
const loading = shallowRef(false)
let generation = 0
let active = true
let polling = false
const providerOptions = [
  { value: 'github', label: 'GitHub' },
  { value: 'gitlab', label: 'GitLab' }
]
const kindOptions = [
  { value: 'issue', label: 'Issue' },
  { value: 'pullRequest', label: 'PR / MR' },
  { value: 'pipeline', label: 'CI 运行' }
]
const labels = { issue: 'Issue', pullRequest: 'PR / MR', pipeline: 'CI' }
const readLabels: Record<string, string> = {
  unread: '尚未读取',
  refreshing: '读取中',
  current: '已更新',
  stale: '保留上次结果',
  canceled: '已停止'
}
const stateLabels: Record<string, string> = {
  open: '打开',
  opened: '打开',
  closed: '已关闭',
  merged: '已合并',
  success: '成功',
  succeeded: '成功',
  failure: '失败',
  failed: '失败',
  pending: '等待中',
  queued: '排队中',
  running: '运行中',
  in_progress: '运行中',
  completed: '已完成',
  canceled: '已取消',
  cancelled: '已取消',
  skipped: '已跳过',
  neutral: '中性结果',
  timed_out: '超时',
  action_required: '需要处理',
  manual: '等待手动操作',
  created: '已创建'
}
function state(value: unknown): string {
  return typeof value === 'string' ? (stateLabels[value] ?? value) : '尚无状态'
}
function linkUrl(link: ExternalLink): string | undefined {
  const value = link.data?.webUrl ?? link.data?.html_url ?? link.data?.url
  if (typeof value !== 'string') return
  try {
    const url = new URL(value)
    if (['http:', 'https:'].includes(url.protocol) && !url.username && !url.password)
      return url.href
  } catch {
    return
  }
}
const entries = computed(() =>
  links.value
    .filter(
      (link) =>
        props.specId
          ? link.specId === props.specId
          : !props.projectId || link.projectId === props.projectId
    )
    .map((link) => {
      const checks = ['checks', 'pipelines', 'jobs'].flatMap((key) => {
        const result = objectValue(link.data?.[key])
        const items = Array.isArray(result.items) ? result.items : []
        return items.map((item, index) => {
          const data = objectValue(item)
          return {
            id: `${key}-${index}`,
            name: String(data.name ?? data.ref ?? data.id ?? `运行 ${index + 1}`),
            status: state(data.conclusion ?? data.status ?? data.state)
          }
        })
      })
      const truncated = ['checks', 'pipelines', 'jobs'].some(
        (key) => objectValue(link.data?.[key]).truncated === true
      )
      return {
        link,
        projectName: snapshot.value?.projects.find((project) => project.record.id === link.projectId)?.record.name ?? link.projectId ?? '未绑定工程',
        url: linkUrl(link),
        title: link.data?.title ?? `${labels[link.kind]} #${link.number}`,
        state: link.data
          ? state(link.data.conclusion ?? link.data.state ?? link.data.status)
          : '尚无远端数据',
        checks,
        truncated
      }
    })
)
watch(
  () => form.provider,
  (provider) => {
    if (['github.com', 'gitlab.com'].includes(form.host))
      form.host = provider === 'github' ? 'github.com' : 'gitlab.com'
  }
)
async function refresh() {
  const current = ++generation
  loading.value = true
  try {
    const result = await getExternalLinks()
    if (active && current === generation) {
      links.value = result
      error.value = ''
    }
  } catch (cause) {
    if (active && current === generation) error.value = errorMessage(cause)
  } finally {
    if (active && current === generation) loading.value = false
  }
}
watch(
  [() => props.projectId, () => props.specId, () => externalVersion.value, () => snapshot.value?.revision, () => props.specId ? snapshot.value?.progress?.[props.specId]?.evidenceDigest : Object.values(snapshot.value?.progress ?? {}).map((progress) => progress.evidenceDigest).join('|')],
  refresh,
  { immediate: true }
)
onBeforeUnmount(() => {
  active = false
  generation++
})
async function add() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await addExternalLink({ ...form, projectId: props.specId ? selectedProjectId.value : props.projectId, specId: props.specId })
    await refresh()
    expanded.value = false
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
async function update(id: string) {
  if (busy.value || pendingJobs.value[id]) return
  busy.value = true
  error.value = ''
  try {
    const job = await refreshExternalLink(id)
    jobId.value = job.id
    pendingJobs.value = { ...pendingJobs.value, [id]: job.id }
    await refresh()
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
useIntervalFn(async () => {
  if (polling || !Object.keys(pendingJobs.value).length) return
  polling = true
  let changed = false
  try {
    for (const [id, job] of Object.entries(pendingJobs.value)) {
      const result = await getJob(job)
      if (!active) return
      if (terminalJob(result.status)) {
        const next = { ...pendingJobs.value }
        delete next[id]
        pendingJobs.value = next
        changed = true
      }
    }
    if (changed) await refresh()
  } catch (cause) {
    if (active) error.value = errorMessage(cause)
  } finally {
    polling = false
  }
}, 2500)
</script>
<template>
  <section class="platform-section external-links">
    <div class="section-heading">
      <div>
        <h2>研发关联</h2>
        <p class="muted">读取 Issue、PR / MR 与 CI 状态，保留最近成功结果。</p>
      </div>
      <div class="inline-actions">
        <Button type="button" variant="outline" :disabled="loading" @click="refresh">{{
          loading ? '读取中…' : '重新读取'
        }}</Button><Button
          type="button"
          :aria-expanded="expanded"
          :aria-controls="formId"
          :disabled="busy"
          @click="expanded = !expanded"
          >{{ expanded ? '收起表单' : '关联外部事项' }}</Button
        >
      </div>
    </div>
    <p v-if="error" role="alert" class="notice error">{{ error }}</p>
    <p v-if="jobId" role="status" class="notice">
      已开始读取，<RouterLink :to="`/jobs/${jobId}`">查看执行进度</RouterLink>。
    </p>
    <form
      v-if="expanded"
      :id="formId"
      class="form-stack inset-form"
      aria-label="关联外部事项"
      @submit.prevent="add"
    >
      <fieldset :disabled="busy" class="plain-fieldset form-stack">
        <legend class="sr-only">远端事项信息</legend>
        <label v-if="specId && projectOptions.length > 1" :for="formId + '-project'">
          关联工程
          <SingleSelect :id="formId + '-project'" v-model="selectedProjectId" :options="projectOptions" :disabled="busy" />
        </label>
        <div class="form-grid">
          <label
            >平台<SingleSelect
              v-model="form.provider"
              name="provider"
              aria-label="远端平台"
              :options="providerOptions"
              :disabled="busy"
          /></label>
          <label
            >对象类型<SingleSelect
              v-model="form.kind"
              name="kind"
              aria-label="关联对象类型"
              :options="kindOptions"
              :disabled="busy"
          /></label>
          <label
            >实例地址（必填）<input
              v-model="form.host"
              name="host"
              required
              spellcheck="false"
              autocorrect="off"
              autocapitalize="none"
              placeholder="https://gitlab.example.com:9443"
            /><span class="field-hint">支持自建实例的协议与端口</span></label
          >
          <label
            >远端项目（必填）<input
              v-model="form.project"
              name="project"
              required
              spellcheck="false"
              autocorrect="off"
              autocapitalize="none"
              placeholder="owner/repository"
          /></label>
          <label
            >{{ form.kind === 'pipeline' ? '运行 ID' : '事项编号' }}（必填）<input
              v-model.number="form.number"
              name="number"
              type="number"
              min="1"
              step="1"
              required
          /></label>
        </div>
        <p class="muted">使用本机 gh / glab 已有认证读取所选实例与项目。</p>
        <div class="form-actions">
          <Button :disabled="busy">{{ busy ? '保存中…' : '保存关联' }}</Button>
        </div>
      </fieldset>
    </form>
    <div
      v-if="entries.length"
      class="table-scroll external-table-wrap"
      tabindex="0"
      role="region"
      aria-label="研发关联事项与读取状态"
    >
      <table class="spec-table external-table">
        <caption class="sr-only">
          研发关联事项、远端状态及最近读取时间
        </caption>
        <thead>
          <tr>
            <th scope="col">关联事项</th>
            <th scope="col">远端状态</th>
            <th scope="col">读取状态</th>
            <th scope="col">最近成功</th>
            <th scope="col">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="entry in entries" :key="entry.link.id">
            <th scope="row" class="external-title">
              <a v-if="entry.url" :href="entry.url" target="_blank" rel="noopener noreferrer">{{
                entry.title
              }}</a><strong v-else>{{ entry.title }}</strong
              ><span class="row-sub"
                >{{ labels[entry.link.kind] }} #{{ entry.link.number }} ·
                {{ entry.link.project }}</span
              >
              <span v-if="specId" class="row-sub">{{ entry.projectName }}</span>
              <details class="external-detail">
                <summary>
                  关联信息<span v-if="entry.checks.length">
                    · {{ entry.checks.length }} 项检查</span
                  >
                </summary>
                <p class="path-text">{{ entry.link.provider }} · {{ entry.link.host }}</p>
                <ul v-if="entry.checks.length" class="check-results">
                  <li v-for="check in entry.checks" :key="check.id">
                    <span>{{ check.name }}</span><span>{{ check.status }}</span>
                  </li>
                </ul>
                <p v-if="entry.truncated" class="warning-text">
                  检查列表达到分页上限，可打开远端继续查看。
                </p>
                <details v-if="entry.link.data">
                  <summary>原始读取数据</summary>
                  <pre>{{ readableValue(entry.link.data) }}</pre>
                </details>
              </details>
            </th>
            <td>{{ entry.state }}</td>
            <td>
              <span :class="entry.link.lastError ? 'warning-text' : 'muted'">{{
                pendingJobs[entry.link.id]
                  ? '读取中'
                  : (readLabels[entry.link.status] ?? entry.link.status)
              }}</span><span v-if="entry.link.lastError" class="row-sub warning-text">{{
                entry.link.lastError
              }}</span>
            </td>
            <td class="external-date">{{ dateLabel(entry.link.lastSuccessAt ?? null) }}</td>
            <td>
              <Button
                type="button"
                variant="outline"
                :disabled="busy || !!pendingJobs[entry.link.id]"
                @click="update(entry.link.id)"
                >{{ pendingJobs[entry.link.id] ? '读取中…' : '更新状态' }}</Button
              >
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else-if="!expanded" class="empty">{{ loading ? '正在读取关联…' : '尚未关联外部事项' }}</p>
  </section>
</template>
<style scoped>
.external-table {
  min-width: 760px;
}
.external-title {
  width: 40%;
  min-width: 240px;
  white-space: normal;
  text-align: left;
  font-weight: 500;
}
.external-title > a {
  color: var(--yss-color-primary-control);
}
.external-table td {
  vertical-align: top;
}
.external-table td:nth-child(3) {
  max-width: 220px;
  white-space: normal;
}
.external-date {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.external-detail {
  font-weight: 400;
}
.check-results {
  list-style: none;
  padding: 0;
  margin: 6px 0;
}
.check-results li {
  display: flex;
  justify-content: space-between;
  gap: 18px;
  border-bottom: 1px solid var(--yss-colors-border);
  padding: 6px 0;
}
.field-hint {
  font-size: 11px;
  color: var(--brand-color-text-secondary);
}
.external-table thead {
  position: sticky;
  top: 0;
  z-index: 1;
}
.external-table-wrap {
  max-height: 65vh;
}
</style>
