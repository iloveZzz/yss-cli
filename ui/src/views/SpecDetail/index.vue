<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ModalDialog from '@/components/ModalDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import HistoryList from '@/components/HistoryList.vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { stages, statuses, healthLabels, dateLabel, isClosed, contentStatuses, errorMessage } from '@/services/workbench'
import { useSpecEditor } from './hooks/useSpecEditor'
import SourcePreview from './components/SourcePreview.vue'
import ResourceEditor from './components/ResourceEditor.vue'
import TaskList from './components/TaskList.vue'
import LifecyclePanel from './components/LifecyclePanel.vue'
import ContentPanel from './components/ContentPanel.vue'
import ResourceViewer from './components/ResourceViewer.vue'
import { contentReminders } from '@/services/workbench-content'
import { isWebLink } from './schemas/markdown'
const route = useRoute(); const { snapshot, submit } = useWorkbench()
const taskDirty=shallowRef(false);const stageDirty=shallowRef(false);const questionDirty=shallowRef(false);const externalDirty=computed(()=>taskDirty.value || stageDirty.value || questionDirty.value)
const { spec, draft, dirty, error, notice, initializing, saving, save, reload, confirmOpen, decide, allowLeave, reset } = useSpecEditor(externalDirty)
const tab = shallowRef('overview');
const tabs=[{id:'overview',label:'规格与关联内容'},{id:'content',label:'待确认事项'},{id:'tasks',label:'任务'},{id:'lifecycle',label:'生命周期'},{id:'resources',label:'关联资源'},{id:'history',label:'变更历史'}]
watch(()=>route.query.taskId,id=>{if(id)tab.value='tasks'},{immediate:true})
watch(() => route.query.tab, value => { if (value === 'content') tab.value = 'content' }, { immediate: true })
const resourceTarget = shallowRef<{ path: string; fragment: string } | null>(null)
const reminders = computed(() => contentReminders(spec.value?.content))
function openResource(path: string, fragment = '') { resourceTarget.value = { path, fragment } }
function childSaved(){if(!dirty.value)reset()}
 const copyNotice = shallowRef('')
const syncError = shallowRef('')
async function syncResources() {
  if (!spec.value || !await allowLeave()) return
  syncError.value = ''
  try { await submit({ kind: 'refreshSpec', specId: spec.value.record.id }); reset(); copyNotice.value = '已同步主规格关联，旧记录与历史已保留。' }
  catch (e) { syncError.value = errorMessage(e) }
}
function moveTab(event: KeyboardEvent) {
  const ids = tabs.map(t=>t.id); const index = ids.indexOf(tab.value)
  const next = event.key === 'ArrowRight' ? (index + 1) % ids.length : event.key === 'ArrowLeft' ? (index + ids.length - 1) % ids.length : event.key === 'Home' ? 0 : event.key === 'End' ? ids.length-1 : -1
  if (next >= 0) { event.preventDefault(); tab.value = ids[next] ?? 'overview'; document.getElementById(`tab-${tab.value}`)?.focus() }
}
const project = computed(() => snapshot.value?.projects.find(p => p.record.id === spec.value?.record.projectId))
const unfinishedTasks = computed(() => snapshot.value?.tasks.filter(t => t.record.specId === spec.value?.record.id && t.record.active && !isClosed(t.record.status)).length ?? 0)
const events = computed(() => [...(snapshot.value?.history.filter(e => e.specId === spec.value?.record.id) ?? [])].reverse())
const back = computed(() => typeof route.query.return === 'string' && /^\/(specs|projects|workbench)(\?|\/|$)/.test(route.query.return) ? route.query.return : '/specs')
async function copyPath(path: string) { try { await navigator.clipboard.writeText(`${project.value?.record.rootPath}/${path}`); copyNotice.value = '路径已复制' } catch { copyNotice.value = '复制失败，请手工选择路径复制' } }
</script>
<template><template v-if="spec"><RouterLink :to="back" class="back-link">← 返回</RouterLink><div class="page-heading"><div><div class="eyebrow">{{ project?.record.name }} / SPEC</div><h1>{{ spec.record.title }}</h1><div class="inline-meta"><StatusBadge :status="spec.record.status" /><span>{{ stages[spec.record.stage] }}</span><span v-if="spec.content">内容：{{ contentStatuses[spec.content.status] }}</span><span v-if="!spec.record.active">已停用</span><span v-if="dirty" class="warning-text">有未保存修改</span></div></div><Button v-if="draft" :disabled="saving || !dirty" @click="save">{{ saving ? '保存中…' : '保存个人标记' }}</Button></div><div class="tabs" role="tablist" aria-label="Spec 内容" @keydown="moveTab"><button v-for="item in tabs" :id="`tab-${item.id}`" :key="item.id" role="tab" :tabindex="tab === item.id ? 0 : -1" :aria-selected="tab === item.id" :aria-controls="`panel-${item.id}`" @click="tab = item.id">{{ item.label }}</button></div><div v-if="spec.content" class="content-reminder-links"><Button v-for="item in reminders" :key="item.id" variant="ghost" @click="tab = 'content'">{{ item.label }} {{ item.count }}</Button><span v-if="spec.content.needsReconfirmation" class="warning-text">来源已更新，须重新确认</span></div><section v-show="tab === 'overview'" id="panel-overview" role="tabpanel" aria-labelledby="tab-overview"><div class="notice source-strip" :class="{ warning: spec.sourceHealth !== 'available' }"><span>{{ healthLabels[spec.sourceHealth] }} · {{ spec.record.sourcePath }}</span><span>文件修改：{{ dateLabel(spec.sourceModifiedAt) }}</span></div><div v-if="spec.resources.length" class="resource-shortcuts panel"><strong>一并阅读</strong><template v-for="(resource, index) in spec.resources" :key="index"><a v-if="resource.record.targetType === 'web' && isWebLink(resource.record.target)" :href="resource.record.target" target="_blank" rel="noopener noreferrer">{{ resource.record.label }} ↗</a><Button v-else variant="outline" @click="openResource(resource.record.target)">{{ resource.record.label }}</Button></template></div><div class="detail-grid"><SourcePreview :spec-id="spec.record.id" :source-path="spec.record.sourcePath" @navigate="openResource" /><aside class="panel editor"><h2>个人标记</h2><p class="muted">阶段与状态由你维护，文件变化不会自动推进。</p><p v-if="initializing" role="status">正在读取标记…</p><form v-else-if="draft" class="form-stack" @submit.prevent="save"><label>标题<Input v-model="draft.title" required /></label><div class="form-grid"><label>当前关注阶段<select v-model="draft.stage"><option v-for="(label, value) in stages" :key="value" :value="value">{{ label }}</option></select></label><label>个人状态<select v-model="draft.status"><option v-for="(label, value) in statuses" :key="value" :value="value">{{ label }}</option></select></label></div><p v-if="draft.status === 'done'" class="muted">“已完成”仅代表个人完成，不代表研发验收。</p><p v-if="draft.status === 'done' && unfinishedTasks" role="status" class="notice warning">仍有 {{unfinishedTasks}} 项未完成任务；标记 Spec 完成不会关闭这些任务。</p><label>下一步<Input v-model="draft.nextAction" placeholder="接下来要做什么" /></label><label>阻塞说明 <span v-if="draft.status === 'blocked'">（必填）</span><textarea v-model="draft.blocker" :required="draft.status === 'blocked'" rows="3" /></label><label>待决备注<textarea v-model="draft.notes" rows="4" /></label><label>主规格相对路径<Input v-model="draft.sourcePath" required spellcheck="false" /></label><p class="muted">文件移动后可重新关联，保留当前 Spec 身份与历史。</p><label class="check-label"><input v-model="draft.active" type="checkbox">启用此 Spec</label><p v-if="error" role="alert" class="notice error">{{ error }}<Button type="button" variant="outline" @click="reload">载入最新标记</Button></p><p v-if="notice" role="status" class="notice">{{ notice }}</p><div class="form-actions"><Button type="button" variant="outline" :disabled="saving" @click="reload">重新载入</Button><Button :disabled="saving || !dirty">保存</Button></div></form></aside></div></section><section v-show="tab === 'content'" id="panel-content" role="tabpanel" aria-labelledby="tab-content"><ContentPanel v-if="spec.content && !initializing" :key="spec.record.id" :spec-id="spec.record.id" :source-path="spec.record.sourcePath" :content="spec.content" :confirm-leave="allowLeave" @dirty="questionDirty=$event" @saved="childSaved" /></section><section v-show="tab === 'tasks'" id="panel-tasks" role="tabpanel" aria-labelledby="tab-tasks" class="panel"><TaskList v-if="!initializing" :spec-id="spec.record.id" :confirm-leave="allowLeave" @dirty="taskDirty=$event" @saved="childSaved" /></section><section v-show="tab === 'lifecycle'" id="panel-lifecycle" role="tabpanel" aria-labelledby="tab-lifecycle"><LifecyclePanel v-if="!initializing" :spec-id="spec.record.id" :confirm-leave="allowLeave" @dirty="stageDirty=$event" @saved="childSaved" /></section><section v-show="tab === 'resources'" id="panel-resources" role="tabpanel" aria-labelledby="tab-resources" class="resource-grid"><div class="panel"><div class="section-heading"><h2>关联资源</h2><Button variant="outline" :disabled="saving" @click="syncResources">同步主规格关联</Button></div><p class="muted">同步主规格中的资源；被归入主规格的旧独立记录会停用，个人备注与历史保留。</p><p v-if="syncError" role="alert" class="notice error">{{ syncError }}</p><article v-for="(resource, index) in spec.resources" :key="index" class="resource-item"><h3>{{ resource.record.label }}</h3><p class="path-text">{{ resource.record.target }}</p><p :class="['available','external'].includes(resource.health) ? 'muted' : 'warning-text'">{{ healthLabels[resource.health] }}</p><a v-if="resource.record.targetType === 'web' && isWebLink(resource.record.target)" :href="resource.record.target" target="_blank" rel="noopener noreferrer">打开外部链接 ↗</a><template v-else><Button variant="outline" @click="openResource(resource.record.target)">查看内容</Button><Button variant="ghost" @click="copyPath(resource.record.target)">复制路径</Button></template><p v-if="resource.message" class="warning-text">{{ resource.message }}</p></article><p v-if="!spec.resources.length" class="empty">暂无关联资源</p><p role="status">{{ copyNotice }}</p></div><form v-if="draft" class="panel form-stack" @submit.prevent="save"><ResourceEditor v-model="draft.resources" /><p v-if="error" role="alert" class="notice error">{{ error }}</p><p v-if="notice" role="status" class="notice">{{ notice }}</p><Button :disabled="saving || !dirty">保存个人标记</Button></form></section><section v-show="tab === 'history'" id="panel-history" role="tabpanel" aria-labelledby="tab-history" class="panel"><HistoryList :events="events" /></section><ResourceViewer v-model="resourceTarget" :spec-id="spec.record.id" :source-path="spec.record.sourcePath" /><ModalDialog v-model="confirmOpen" title="放弃未保存修改？"><p>继续操作将放弃当前尚未保存的编辑。</p><div class="form-actions"><Button variant="outline" @click="decide(false)">继续编辑</Button><Button @click="decide(true)">放弃修改并继续</Button></div></ModalDialog></template><p v-else class="empty">Spec 不存在。<RouterLink to="/specs">返回全部 Spec</RouterLink></p></template>

<style scoped>
.content-reminder-links { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin: -6px 0 18px; }
.resource-shortcuts { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; padding: 14px 18px; margin-bottom: 20px; }
.resource-shortcuts strong { font-size: 12px; margin-right: 4px; }
.resource-shortcuts a { color: var(--yss-color-primary-control); }
</style>
