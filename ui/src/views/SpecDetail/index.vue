<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import { Input } from '@/components/ui/input'
import ModalDialog from '@/components/ModalDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import ProgressSummary from '@/components/ProgressSummary.vue'
import LifecycleOverview from './components/LifecycleOverview.vue'
import DraftNotice from '@/components/DraftNotice.vue'
import ConflictResolver from '@/components/ConflictResolver.vue'
import ExternalLinks from '@/components/ExternalLinks.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import {
  stages,
  statuses,
  healthLabels,
  dateLabel,
  contentStatuses,
  errorMessage,
  type Stage,
  type Status
} from '@/services/workbench'
import { useSpecEditor } from './hooks/useSpecEditor'
import SourcePreview from './components/SourcePreview.vue'
import ResourceEditor from './components/ResourceEditor.vue'
import ResourceList from './components/ResourceList.vue'
import LifecyclePanel from './components/LifecyclePanel.vue'
import ContentPanel from './components/ContentPanel.vue'
import ResourceViewer from './components/ResourceViewer.vue'
import PagedHistory from './components/PagedHistory.vue'
import AgentLauncher from './components/AgentLauncher.vue'
import { contentReminders } from '@/services/workbench-content'
import { isWebLink } from './schemas/markdown'
const route = useRoute()
const router = useRouter()
const { snapshot, submit } = useWorkbench()
const stageDirty = shallowRef(false)
const questionDirty = shallowRef(false)
const lifecycleDirty = shallowRef(false)
const editor = useSpecEditor(
  computed(() => stageDirty.value || questionDirty.value || lifecycleDirty.value)
)
const {
  spec,
  draft,
  dirty,
  error,
  notice,
  initializing,
  saving,
  save,
  reload,
  confirmOpen,
  decide,
  allowLeave,
  reset,
  drafts,
  conflict,
  resolveConflict
} = editor
const tabs = [
  { id: 'overview', label: '规格' },
  { id: 'content', label: '待确认事项' },
  { id: 'lifecycle', label: '生命周期' },
  { id: 'resources', label: '关联资源' },
  { id: 'development', label: '研发关联' },
  { id: 'run', label: '运行 Pi Agent' },
  { id: 'history', label: '变更历史' },
  { id: 'settings', label: '设置' }
]
function setQuery(patch: Record<string, string | undefined>) {
  void router.replace({ query: { ...route.query, ...patch } })
}
const tab = computed({
  get: () =>
    tabs.some((item) => item.id === route.query.tab) ? String(route.query.tab) : 'overview',
  set: (value) => setQuery({ tab: value === 'overview' ? undefined : value })
})
const resourceTarget = computed({
  get: () =>
    typeof route.query.doc === 'string'
      ? {
          path: route.query.doc,
          fragment: typeof route.query.fragment === 'string' ? route.query.fragment : ''
        }
      : null,
  set: (value) => setQuery({ doc: value?.path, fragment: value?.fragment || undefined })
})
const selectedStage = computed(() =>
  typeof route.query.stage === 'string' && Object.hasOwn(stages, route.query.stage)
    ? (route.query.stage as Stage)
    : undefined
)
const sourceSection = computed(() =>
  typeof route.query.section === 'string' ? route.query.section : ''
)
const progress = computed(
  () => snapshot.value?.progress?.[spec.value?.record.id ?? ''] ?? spec.value?.progress
)
const reminders = computed(() => contentReminders(spec.value?.content))
const project = computed(() =>
  snapshot.value?.projects.find((item) => item.record.id === spec.value?.record.projectId)
)
const back = computed(() =>
  typeof route.query.return === 'string' &&
  /^\/(specs|projects|workbench)(\?|\/|$)/.test(route.query.return)
    ? route.query.return
    : '/specs'
)
const editOpen = shallowRef(false)
const resourceEdit = shallowRef(false)
const copyNotice = shallowRef('')
const syncError = shallowRef('')
watch(
  () => spec.value?.record.id,
  () => {
    editOpen.value = false
    resourceEdit.value = false
    copyNotice.value = ''
    syncError.value = ''
    stageDirty.value = false
    lifecycleDirty.value = false
    questionDirty.value = false
  },
  { flush: 'sync' }
)
function openResource(path: string, fragment = '') {
  resourceTarget.value = { path, fragment }
}
function childSaved() {
  if (!dirty.value) void reset()
}
async function syncResources() {
  if (!spec.value || !(await allowLeave())) return
  syncError.value = ''
  try {
    await submit({ kind: 'refreshSpec', specId: spec.value.record.id })
    await reset()
    copyNotice.value = '已同步主规格关联。'
  } catch (cause) {
    syncError.value = errorMessage(cause)
  }
}
async function copyPath(path: string) {
  try {
    await navigator.clipboard.writeText(`${project.value?.record.rootPath}/${path}`)
    copyNotice.value = '路径已复制'
  } catch {
    copyNotice.value = '复制失败，请手工选择路径复制'
  }
}
function moveTab(event: KeyboardEvent) {
  const index = tabs.findIndex((item) => item.id === tab.value)
  const next =
    event.key === 'ArrowRight'
      ? (index + 1) % tabs.length
      : event.key === 'ArrowLeft'
        ? (index + tabs.length - 1) % tabs.length
        : event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? tabs.length - 1
            : -1
  if (next >= 0) {
    event.preventDefault()
    tab.value = tabs[next]?.id ?? 'overview'
    document.getElementById(`tab-${tab.value}`)?.focus()
  }
}
</script>
<template>
  <template v-if="spec">
    <RouterLink :to="back" class="back-link">← 返回</RouterLink>
    <div class="page-heading spec-detail-heading">
      <div>
        <h1>{{ spec.record.title }}</h1>
        <div class="inline-meta">
          <span class="muted">个人标记</span>
          <StatusBadge :status="spec.record.status" />
          <span>{{ stages[spec.record.stage] }}</span>
          <span>{{ contentStatuses[spec.content.status] }}</span>
          <span v-if="!spec.record.active">已停用</span>
          <span v-if="dirty" class="warning-text">有未保存修改</span>
        </div>
        <p class="spec-origin">
          <RouterLink :to="`/projects/${spec.record.projectId}`">
            {{ project?.record.name ?? '未知工程' }}
          </RouterLink>
          <code>{{ spec.record.sourcePath }}</code>
          <Button
            type="button"
            variant="ghost"
            aria-label="复制主规格完整路径"
            @click="copyPath(spec.record.sourcePath)"
          >
            复制路径
          </Button>
        </p>
      </div>
      <div class="inline-actions">
        <Button variant="outline" @click="tab = 'run'">运行 Pi Agent</Button>
        <Button
          v-if="
            draft &&
            (dirty ||
              (editOpen && tab === 'overview') ||
              (resourceEdit && tab === 'resources') ||
              tab === 'settings')
          "
          :disabled="saving || !dirty"
          @click="save"
        >
          {{ saving ? '保存中…' : '保存修改' }}
        </Button>
      </div>
    </div>
    <ProgressSummary :progress="progress" />
    <div class="tabs" role="tablist" aria-label="Spec 内容" @keydown="moveTab">
      <button
        v-for="item in tabs"
        :id="`tab-${item.id}`"
        :key="item.id"
        role="tab"
        :tabindex="tab === item.id ? 0 : -1"
        :aria-selected="tab === item.id"
        :aria-controls="`panel-${item.id}`"
        @click="tab = item.id"
      >
        {{ item.label }}
      </button>
    </div>
    <p v-if="copyNotice" role="status" class="copy-path-notice">{{ copyNotice }}</p>
    <div
      v-if="reminders.some((item) => item.count)"
      class="content-reminder-links"
    >
      <Button
        v-for="item in reminders.filter((item) => item.count)"
        :key="item.id"
        variant="ghost"
        @click="setQuery({ tab: 'content', category: item.id, questionId: undefined })"
      >
        {{ item.label }}{{ item.id === 'ready' ? '' : ` ${item.count}` }}
      </Button>
    </div>
    <DraftNotice
      :status="drafts.status.value"
      :error="drafts.error.value"
      :drafts="drafts.recoverable.value"
      :conflict="drafts.recoveryConflict.value"
      @merge="drafts.mergeRecovered"
      @cancel-recovery="drafts.cancelRecovery"
      @restore="drafts.restore"
      @discard="drafts.discard"
    />
    <ConflictResolver
      v-if="conflict"
      :base="conflict.base"
      :local="conflict.local"
      :current="conflict.current"
      @resolve="resolveConflict"
      @close="conflict = undefined"
    />
    <p v-if="error" role="alert" class="notice error">{{ error }}</p>
    <p v-if="notice" role="status" class="notice">{{ notice }}</p>
    <section
      v-show="tab === 'overview'"
      id="panel-overview"
      role="tabpanel"
      aria-labelledby="tab-overview"
    >
      <section class="focus-summary">
        <div>
          <h2>下一步</h2>
          <p class="preserve-lines">{{ spec.record.nextAction || '尚未填写下一步' }}</p>
          <p v-if="spec.record.blocker" class="warning-text preserve-lines">
            阻塞说明 · {{ spec.record.blocker }}
          </p>
        </div>
        <Button
          type="button"
          variant="outline"
          :aria-expanded="editOpen"
          @click="editOpen = !editOpen"
        >
          {{ editOpen ? '收起编辑' : '编辑个人标记' }}
        </Button>
      </section>
      <form
        v-if="editOpen && draft && !initializing"
        class="platform-section form-stack"
        aria-label="个人标记"
        @submit.prevent="save"
      >
        <div class="form-grid">
          <label>
            当前关注阶段
            <SingleSelect
              :model-value="draft.stage"
              :options="Object.entries(stages).map(([value, label]) => ({ value, label }))"
              aria-label="当前关注阶段"
              @update:model-value="draft.stage = $event as Stage"
            />
          </label>
          <label>
            个人状态
            <SingleSelect
              :model-value="draft.status"
              :options="Object.entries(statuses).map(([value, label]) => ({ value, label }))"
              aria-label="个人状态"
              @update:model-value="draft.status = $event as Status"
            />
          </label>
        </div>
        <label>
          下一步
          <Input v-model="draft.nextAction" name="nextAction" />
        </label>
        <label>
          阻塞说明{{ draft.status === 'blocked' ? '（必填）' : '' }}
          <textarea
            v-model="draft.blocker"
            name="blocker"
            :required="draft.status === 'blocked'"
            rows="3"
          />
        </label>
        <label>
          待决备注
          <textarea v-model="draft.notes" name="notes" rows="3" />
        </label>
        <div class="form-actions">
          <Button type="button" variant="outline" :disabled="saving" @click="reload">
            重新载入
          </Button>
          <Button :disabled="saving || !dirty">保存标记</Button>
        </div>
      </form>
      <div class="notice source-strip" :class="{ warning: spec.sourceHealth !== 'available' }">
        <span>{{ healthLabels[spec.sourceHealth] }}</span>
        <span>文件修改 {{ dateLabel(spec.sourceModifiedAt) }}</span>
      </div>
      <div v-if="spec.resources.length" class="resource-shortcuts">
        <strong>一并阅读</strong>
        <template v-for="(resource, index) in spec.resources" :key="index">
          <a
            v-if="resource.record.targetType === 'web' && isWebLink(resource.record.target)"
            :href="resource.record.target"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ resource.record.label }} ↗
          </a>
          <Button v-else variant="outline" @click="openResource(resource.record.target)">
            {{ resource.record.label }}
          </Button>
        </template>
      </div>
      <SourcePreview
        :spec-id="spec.record.id"
        :source-path="spec.record.sourcePath"
        :fragment="sourceSection"
        :active="tab === 'overview'"
        @navigate="openResource"
        @position="setQuery({ section: $event })"
      />
    </section>
    <section
      v-show="tab === 'content'"
      id="panel-content"
      role="tabpanel"
      aria-labelledby="tab-content"
    >
      <ContentPanel
        v-if="!initializing"
        :key="spec.record.id"
        :spec-id="spec.record.id"
        :source-path="spec.record.sourcePath"
        :project-root="project?.record.rootPath"
        :content="spec.content"
        :confirm-leave="allowLeave"
        @dirty="questionDirty = $event"
        @saved="childSaved"
      />
    </section>
    <section
      v-show="tab === 'lifecycle'"
      id="panel-lifecycle"
      role="tabpanel"
      aria-labelledby="tab-lifecycle"
    >
      <LifecycleOverview
        :key="spec.record.id"
        :spec-id="spec.record.id"
        :project-id="spec.record.projectId"
        :spec-digest="spec.content.artifactDigest"
        :progress="progress"
        :initial-stage="selectedStage"
        :confirm-leave="allowLeave"
        @select="setQuery({ stage: $event })"
        @dirty="lifecycleDirty = $event"
        @saved="childSaved"
        @confirm-content="tab = 'content'"
      />
      <details class="personal-lifecycle">
        <summary>个人阶段记录</summary>
        <LifecyclePanel
          v-if="!initializing"
          :key="spec.record.id"
          :spec-id="spec.record.id"
          :initial-stage="selectedStage"
          :confirm-leave="allowLeave"
          @select="setQuery({ stage: $event })"
          @dirty="stageDirty = $event"
          @saved="childSaved"
        />
      </details>
    </section>
    <section
      v-show="tab === 'resources'"
      id="panel-resources"
      role="tabpanel"
      aria-labelledby="tab-resources"
    >
      <div class="platform-section">
        <div class="section-heading">
          <h2>关联资源</h2>
          <Button variant="outline" :disabled="saving" @click="syncResources">
            同步主规格关联
          </Button>
        </div>
        <p v-if="syncError" role="alert" class="notice error">{{ syncError }}</p>
        <ResourceList :resources="spec.resources" @open="openResource" @copy="copyPath" />
        <div class="resource-edit-toggle">
          <Button
            type="button"
            variant="outline"
            :aria-expanded="resourceEdit"
            @click="resourceEdit = !resourceEdit"
          >
            {{ resourceEdit ? '收起个人资源编辑' : '编辑个人资源' }}
          </Button>
          <span class="muted">个人补充与主规格关联一同展示。</span>
        </div>
      </div>
      <form
        v-if="resourceEdit && draft"
        class="platform-section form-stack"
        aria-label="编辑个人资源"
        @submit.prevent="save"
      >
        <ResourceEditor v-model="draft.resources" />
        <div class="form-actions"><Button :disabled="saving || !dirty">保存个人资源</Button></div>
      </form>
    </section>
    <section
      v-if="tab === 'development'"
      id="panel-development"
      role="tabpanel"
      aria-labelledby="tab-development"
    >
      <ExternalLinks :project-id="spec.record.projectId" :spec-id="spec.record.id" />
    </section>
    <section v-show="tab === 'run'" id="panel-run" role="tabpanel" aria-labelledby="tab-run">
      <AgentLauncher
        :key="spec.record.id"
        :active="tab === 'run'"
        :spec-id="spec.record.id"
        :project-id="spec.record.projectId"
        :lifecycle-stage="selectedStage"
      />
    </section>
    <section
      v-if="tab === 'history'"
      id="panel-history"
      role="tabpanel"
      aria-labelledby="tab-history"
    >
      <PagedHistory :spec-id="spec.record.id" />
    </section>
    <section
      v-show="tab === 'settings'"
      id="panel-settings"
      role="tabpanel"
      aria-labelledby="tab-settings"
    >
      <form
        v-if="draft"
        class="platform-section form-stack"
        aria-label="Spec 关联设置"
        @submit.prevent="save"
      >
        <h2>关联设置</h2>
        <label>
          标题
          <Input v-model="draft.title" name="title" required />
        </label>
        <label>
          主规格相对路径
          <Input v-model="draft.sourcePath" name="sourcePath" required spellcheck="false" />
        </label>
        <details class="settings-help">
          <summary>主规格移动后如何处理</summary>
          <p class="muted">文件移动后在这里更新路径，保留当前 Spec 身份与历史。</p>
        </details>
        <label class="check-label">
          <input v-model="draft.active" type="checkbox" name="active" />
          启用此 Spec
        </label>
        <div class="form-actions"><Button :disabled="saving || !dirty">保存设置</Button></div>
      </form>
    </section>
    <ResourceViewer
      v-model="resourceTarget"
      :refresh-key="spec.content.artifactDigest"
      :spec-id="spec.record.id"
      :source-path="spec.record.sourcePath"
      :label="
        spec.resources.find((resource) => resource.record.target === resourceTarget?.path)?.record
          .label
      "
    />
    <ModalDialog v-model="confirmOpen" title="离开当前编辑？">
      <p>当前编辑包含未保存输入，继续操作将离开编辑。</p>
      <div class="form-actions">
        <Button variant="outline" @click="decide(false)">继续编辑</Button>
        <Button @click="decide(true)">离开并继续</Button>
      </div>
    </ModalDialog>
  </template>
  <p v-else class="empty">
    Spec 不存在。
    <RouterLink to="/specs">返回全部 Spec</RouterLink>
  </p>
</template>
<style scoped>
.spec-detail-heading {
  flex-wrap: wrap;
}
.spec-detail-heading > div:first-child {
  min-width: 0;
}
.spec-origin {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin-top: 10px;
  color: #858d99;
  font-size: 11px;
}
.spec-origin > a {
  color: #62666c;
}
.spec-origin code {
  min-width: 0;
  overflow-wrap: anywhere;
}
.spec-origin > button {
  height: 24px;
  min-height: 24px;
  padding: 0 6px;
  font-size: 11px;
  color: #858d99;
}
.copy-path-notice {
  margin: 0 0 12px;
  color: #278064;
  font-size: 12px;
}
.resource-edit-toggle {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  padding-top: 16px;
}
.resource-edit-toggle > span {
  font-size: 11px;
}
.settings-help summary {
  cursor: pointer;
  font-size: 12px;
  color: #717780;
}
.settings-help p {
  margin-top: 8px;
  font-size: 12px;
}
.content-reminder-links {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
  margin: -6px 0 16px;
}
.focus-summary {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin: 0 0 20px;
  padding: 0 0 20px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.focus-summary p {
  margin-top: 7px;
}
.resource-shortcuts {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin: 18px 0;
}
.resource-shortcuts strong {
  font-size: 12px;
}
.resource-shortcuts a {
  color: #315dcf;
}
</style>
