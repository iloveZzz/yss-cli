<script setup lang="ts">
import { computed, onMounted, reactive, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import { errorMessage } from '@/services/workbench'
import { objectValue, readableValue, type JsonObject } from '@/services/workbench-platform'
import {
  getEngineeringCatalog,
  inspectEngineering,
  previewEngineering,
  executeEngineering,
  type EngineeringCatalog,
  type EngineeringPreview,
  type EngineeringInspection,
  type EngineeringRequest
} from '@/services/workbench-engineering'
const props = defineProps<{ rootPath?: string }>()
const router = useRouter()
const route = useRoute()
const catalog = shallowRef<EngineeringCatalog>()
const operation = shallowRef('doctor')
const values = reactive<Record<string, string>>({
  root: props.rootPath ?? '',
  profile: 'spec',
  installation: 'project',
  projectName: ''
})
const backend = reactive<JsonObject>({
  projectName: '',
  basePackage: '',
  groupId: '',
  projectVersion: '1.0.0-SNAPSHOT'
})
const variables = reactive<Record<string, string>>({
  projectName: '',
  businessDomain: '',
  teamSize: ''
})
const upgrade = reactive({ source: 'github', to: '', artifact: '', sha256: '' })
const upgradeMode = shallowRef('online')
const changeLimit = shallowRef(20)
const skills = shallowRef('')
const recipe = shallowRef('')
const previewSource = shallowRef('')
const preview = shallowRef<EngineeringPreview>()
const inspection = shallowRef<EngineeringInspection>()
const busy = shallowRef(false)
const error = shallowRef('')
const selected = computed(() =>
  catalog.value?.operations.find((item) => item.id === operation.value)
)
const recipes = computed(
  () =>
    catalog.value?.scaffold.platforms.flatMap((platform) =>
      platform.recipes.map((item) => ({ ...item, platform }))
    ) ?? []
)
const request = computed<EngineeringRequest>(() => ({
  operation: operation.value,
  root: values.root ?? '',
  profile: selected.value?.fields.some((field) => field.name === 'profile')
    ? values.profile
    : undefined,
  installation: selected.value?.fields.some((field) => field.name === 'installation')
    ? values.installation
    : undefined,
  variables: selected.value?.fields.some((field) => field.name === 'variables')
    ? Object.fromEntries(Object.entries(variables).filter(([, value]) => value.trim()))
    : {},
  skills: skills.value
    .split(/[,\n]/)
    .map((item) => item.trim())
    .filter(Boolean),
  projectName: operation.value === 'scaffoldFrontend' ? values.projectName : undefined,
  backend:
    operation.value === 'scaffoldDdd' || operation.value === 'scaffoldMvc'
      ? { ...backend }
      : undefined,
  upgrade:
    operation.value === 'upgrade'
      ? Object.fromEntries(
          Object.entries(upgrade).filter(
            ([key, value]) =>
              value.trim() &&
              (upgradeMode.value === 'offline'
                ? key !== 'source'
                : key !== 'artifact' && key !== 'sha256')
          )
        )
      : undefined
}))
const previewCurrent = computed(
  () => preview.value && previewSource.value === JSON.stringify(request.value)
)
watch(
  () => props.rootPath,
  (value) => {
    if (value) values.root = value
  }
)
watch(recipe, (id) => {
  const choice = recipes.value.find((item) => item.id === id)
  if (!choice) return
  Object.assign(backend, {
    platformProfile: choice.platform.id,
    springBootVersion: choice.platform.springBootVersion,
    javaVersion: choice.platform.javaVersion,
    parentGroupId: choice.parent.group_id,
    parentArtifactId: choice.parent.artifact_id,
    parentVersion: choice.parent.version,
    yssComponentsVersion: choice.bom.version
  })
})
onMounted(async () => {
  try {
    catalog.value = await getEngineeringCatalog()
    if (
      typeof route.query.operation === 'string' &&
      catalog.value.operations.some((item) => item.id === route.query.operation)
    )
      operation.value = route.query.operation
    recipe.value = catalog.value.scaffold.defaultRecipeId ?? recipes.value[0]?.id ?? ''
  } catch (cause) {
    error.value = errorMessage(cause)
  }
})
async function inspect() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    inspection.value = await inspectEngineering(values.root ?? '')
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
async function prepare() {
  if (busy.value) return
  previewSource.value = ''
  changeLimit.value = 20
  busy.value = true
  error.value = ''
  try {
    const encoded = JSON.stringify(request.value)
    preview.value = await previewEngineering(request.value)
    previewSource.value = encoded
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
async function execute() {
  if (!preview.value || !previewCurrent.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const job = await executeEngineering(preview.value.id)
    await router.push(`/jobs/${job.id}`)
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
function options(value: unknown): string[] {
  return Array.isArray(value)
    ? value.map((item) =>
        typeof item === 'string' ? item : String((item as { value?: unknown }).value ?? '')
      )
    : []
}
const backendLabels = [
  { name: 'projectName', label: '工程名' },
  { name: 'basePackage', label: 'Java 根包' },
  { name: 'groupId', label: 'Group ID' },
  { name: 'projectVersion', label: '工程版本' },
  { name: 'platformProfile', label: '平台 Profile' },
  { name: 'springBootVersion', label: 'Spring Boot 版本' },
  { name: 'javaVersion', label: 'Java 版本' },
  { name: 'parentGroupId', label: 'Parent Group ID' },
  { name: 'parentArtifactId', label: 'Parent Artifact ID' },
  { name: 'parentVersion', label: 'Parent 版本' },
  { name: 'yssComponentsVersion', label: 'YSS 组件版本' }
]

const operationNotes: Record<string, string> = {
  doctor: '读取安装身份、文件差异和未完成事务。',
  skillsList: '查看当前工程可用、已安装与明确选择的技能。',
  skillsEnsure: '补装指定技能及其依赖，沿用工程安装身份。',
  init: '在不存在或空的目录初始化，父目录需已存在。',
  attach: '为已有工程安装资料与技能，保留本地定制并报告冲突。',
  sync: '按当前 Bundle 更新受管内容，沿用原 Profile。',
  scaffoldDdd: '在输出父目录下创建全新的 DDD Java 工程。',
  scaffoldMvc: '在输出父目录下创建全新的 MVC Java 工程。',
  scaffoldFrontend: '在输出父目录下创建全新的 Vue 前端工程。',
  recover: '核对并恢复未完成事务，或重试已提交事务的安全清理。',
  upgrade: '更新独立工具目录，结果分别记录当前进程与磁盘版本。'
}
const labels: Record<string, string> = {
  spec: 'Spec / 产品 Home',
  backend: 'Java 后端',
  frontend: 'Vue 前端',
  project: '项目资料与技能',
  skills: '技能与必要资源'
}
const variableLabels: Record<string, string> = {
  projectName: '项目显示名称',
  businessDomain: '业务领域',
  teamSize: '团队规模'
}
const actionLabels: Record<string, string> = {
  create: '新增',
  replace: '更新',
  delete: '删除',
  record: '更新收据',
  modified: '本地已修改',
  deleted: '本地已删除'
}
const rootLabel = computed(() =>
  operation.value.startsWith('scaffold')
    ? '输出父目录'
    : operation.value === 'upgrade'
      ? '独立工具目录'
      : '工程根目录'
)
const operationOptions = computed(
  () => catalog.value?.operations.map((item) => ({ value: item.id, label: item.label })) ?? []
)
const recipeOptions = computed(() =>
  recipes.value.map((item) => ({ value: item.id, label: `${item.id} · ${item.evidenceStatus}` }))
)
const currentRecipe = computed(() => recipes.value.find((item) => item.id === recipe.value))
const report = computed(() => objectValue(preview.value?.report))
const changes = computed(() => {
  const values = report.value.changes ?? report.value.differences
  return Array.isArray(values)
    ? values
        .map((item) => objectValue(item))
        .map((item) => ({ path: String(item.path ?? ''), action: String(item.action ?? '') }))
    : []
})
const visibleChanges = computed(() => changes.value.slice(0, changeLimit.value))
const preserved = computed(() =>
  Array.isArray(report.value.preserved) ? report.value.preserved : []
)
const warnings = computed(() =>
  Array.isArray(report.value.warnings) ? report.value.warnings.map(String) : []
)
const facts = computed(() => {
  const data = report.value
  const values = [
    { label: '目标目录', value: String(data.root ?? preview.value?.request.root ?? '') }
  ]
  if (data.profile)
    values.push({ label: '工程类型', value: labels[String(data.profile)] ?? String(data.profile) })
  const project = objectValue(data.installation)
  if (project.profile)
    values.push({
      label: '安装身份',
      value: labels[String(project.profile)] ?? String(project.profile)
    })
  const pending = objectValue(data.transactions).pending
  if (Array.isArray(pending)) values.push({ label: '待恢复事务', value: `${pending.length} 项` })
  if (typeof data.operations === 'number')
    values.push({ label: '恢复操作', value: `${data.operations} 项` })
  if (data.installation && typeof data.installation === 'string')
    values.push({ label: '安装范围', value: labels[data.installation] ?? data.installation })
  if (typeof data.files === 'number') values.push({ label: '生成文件', value: `${data.files} 项` })
  if (Array.isArray(data.modules))
    values.push({ label: '模块', value: data.modules.map(String).join('、') })
  if (data.currentVersion)
    values.push({ label: '磁盘当前版本', value: String(data.currentVersion) })
  if (data.targetVersion) values.push({ label: '目标版本', value: String(data.targetVersion) })
  if (data.archiveSha256) values.push({ label: '归档 SHA-256', value: String(data.archiveSha256) })
  return values
})
const installation = computed(() => objectValue(objectValue(inspection.value?.doctor).installation))
const toolBuild = computed(() =>
  objectValue(objectValue(objectValue(inspection.value?.tool).installation).build)
)
const inspectionDifferences = computed(() => {
  const differences = objectValue(inspection.value?.doctor).differences
  return Array.isArray(differences) ? differences.map(objectValue) : []
})
const pendingTransactions = computed(
  () => objectValue(objectValue(inspection.value?.doctor).transactions).pending
)
const inspectionSkills = computed(() => objectValue(inspection.value?.skills))
const skillRows = computed(() => Object.entries(objectValue(inspectionSkills.value.installed)))
const resultSkillRows = computed(() => Object.entries(objectValue(report.value.installed)))
const skillStatus: Record<string, string> = {
  installed: '已安装',
  'not-installed': '未安装',
  missing: '文件缺失',
  modified: '本地定制',
  unmanaged: '工程自行维护'
}
const trackerFields = computed(() =>
  Object.entries(objectValue(objectValue(inspection.value?.tracker).fields))
)
function revealInvalid(event: Event) {
  if (event.target instanceof HTMLElement) {
    const details = event.target.closest('details')
    if (details) details.open = true
  }
}
function errorText(value: unknown): string {
  return typeof value === 'string' ? value : String(objectValue(value).message ?? '')
}
function fieldOptions(value: unknown) {
  return options(value).map((value) => ({ value, label: labels[value] ?? value }))
}
</script>
<template>
  <section class="platform-section engineering-panel">
    <div class="section-heading">
      <div>
        <h2>工程维护</h2>
        <p v-if="catalog" class="muted">
          YSS {{ catalog.processVersion }} · Bundle {{ catalog.bundleDigest.slice(0, 12) }}
        </p>
      </div>
      <Button type="button" variant="outline" :disabled="busy || !values.root" @click="inspect"
        >读取工程状态</Button
      >
    </div>
    <p v-if="error" role="alert" class="notice error">{{ error }}</p>
    <p v-if="!catalog && !error" role="status" class="muted">正在读取可用操作…</p>
    <form
      v-if="catalog"
      class="form-stack"
      aria-label="工程维护"
      @submit.prevent="prepare"
      @invalid.capture="revealInvalid"
    >
      <fieldset :disabled="busy" class="plain-fieldset form-stack">
        <legend class="sr-only">工程操作参数</legend>
        <label
          >操作<SingleSelect
            v-model="operation"
            name="operation"
            aria-label="工程操作"
            :options="operationOptions"
            :disabled="busy"
        /></label>
        <p class="operation-note muted">{{ operationNotes[operation] }}</p>
        <template v-for="field in selected?.fields ?? []" :key="field.name">
          <label v-if="['path', 'text', 'select', 'skills'].includes(field.type)">
            {{ field.name === 'root' ? rootLabel : field.label
            }}{{ field.required ? '（必填）' : '' }}
            <SingleSelect
              v-if="field.type === 'select'"
              v-model="values[field.name]"
              :name="field.name"
              :aria-label="field.label"
              :options="fieldOptions(field.options)"
              :required="field.required"
              :disabled="busy"
            />
            <textarea
              v-else-if="field.type === 'skills'"
              v-model="skills"
              :name="field.name"
              rows="3"
              :required="field.required"
              spellcheck="false"
              autocorrect="off"
              autocapitalize="none"
              placeholder="技能名，每行一个或以逗号分隔"
            />
            <input
              v-else
              v-model="values[field.name]"
              :name="field.name"
              :required="field.required"
              spellcheck="false"
              autocorrect="off"
              autocapitalize="none"
              :placeholder="
                field.name === 'root'
                  ? '本机绝对路径'
                  : field.name === 'projectName'
                    ? '例如 customer-web'
                    : ''
              "
            />
          </label>
          <details v-else-if="field.type === 'object'" class="parameter-details">
            <summary>项目资料变量（可选）</summary>
            <div class="form-grid">
              <label v-for="name in options(field.options)" :key="name"
                >{{ variableLabels[name] ?? name }}<input v-model="variables[name]" :name="name"
              /></label>
            </div>
          </details>
          <fieldset v-else-if="field.type === 'backend'" class="plain-fieldset form-stack">
            <legend>后端工程配方</legend>
            <label
              >配方<SingleSelect
                v-model="recipe"
                name="recipe"
                aria-label="后端工程配方"
                :options="recipeOptions"
                :disabled="busy"
            /></label>
            <p v-if="currentRecipe" class="muted">
              Spring Boot {{ currentRecipe.platform.springBootVersion }} · Java
              {{ currentRecipe.platform.javaVersion }} · YSS {{ currentRecipe.bom.version }}
            </p>
            <p v-for="blocker in currentRecipe?.blockers ?? []" :key="blocker" class="warning-text">
              {{ blocker }}
            </p>
            <div class="form-grid">
              <label v-for="item in backendLabels.slice(0, 4)" :key="item.name"
                >{{ item.label }}（必填）<input
                  :value="String(backend[item.name] ?? '')"
                  :name="item.name"
                  required
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none"
                  @input="backend[item.name] = ($event.target as HTMLInputElement).value"
              /></label>
            </div>
            <details :open="!recipe">
              <summary>核对或自定义技术坐标</summary>
              <div class="form-grid">
                <label v-for="item in backendLabels.slice(4)" :key="item.name"
                  >{{ item.label
                  }}<input
                    :value="String(backend[item.name] ?? '')"
                    :name="item.name"
                    :type="item.name === 'javaVersion' ? 'number' : 'text'"
                    required
                    spellcheck="false"
                    autocorrect="off"
                    autocapitalize="none"
                    @input="
                      backend[item.name] =
                        item.name === 'javaVersion'
                          ? Number(($event.target as HTMLInputElement).value)
                          : ($event.target as HTMLInputElement).value
                    "
                /></label>
              </div>
            </details>
          </fieldset>
          <fieldset v-else-if="field.type === 'upgrade'" class="plain-fieldset form-stack">
            <legend>升级来源</legend>
            <div class="inline-actions">
              <label class="check-label"
                ><input
                  v-model="upgradeMode"
                  type="radio"
                  name="upgradeMode"
                  value="online"
                />在线发行</label
              ><label class="check-label"
                ><input
                  v-model="upgradeMode"
                  type="radio"
                  name="upgradeMode"
                  value="offline"
                />离线归档</label
              >
            </div>
            <label v-if="upgradeMode === 'online'"
              >发行平台<SingleSelect
                v-model="upgrade.source"
                name="source"
                aria-label="发行平台"
                :options="[
                  { value: 'github', label: 'GitHub' },
                  { value: 'gitlab', label: 'GitLab' }
                ]"
                :disabled="busy"
            /></label>
            <template v-else
              ><label
                >离线归档绝对路径（必填）<input
                  v-model="upgrade.artifact"
                  name="artifact"
                  required
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none" /></label
              ><label
                >归档 SHA-256（必填）<input
                  v-model="upgrade.sha256"
                  name="sha256"
                  required
                  pattern="[a-fA-F0-9]{64}"
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none" /></label
            ></template>
            <label
              >目标版本<input
                v-model="upgrade.to"
                name="to"
                spellcheck="false"
                autocorrect="off"
                autocapitalize="none"
                :placeholder="
                  upgradeMode === 'online' ? '留空使用最新正式版本' : '留空按归档中的版本核对'
                "
            /></label>
          </fieldset>
        </template>
        <div class="form-actions">
          <Button :disabled="busy">{{ busy ? '正在核对…' : '查看执行内容' }}</Button>
        </div>
      </fieldset>
    </form>
    <section v-if="inspection" class="result-details inspection" aria-label="工程状态">
      <div class="section-heading">
        <h3>工程状态</h3>
        <span class="muted">{{ inspection.root }}</span>
      </div>
      <table class="spec-table facts-table">
        <caption class="sr-only">
          安装身份与文件诊断摘要
        </caption>
        <tbody>
          <tr>
            <th scope="row">工程安装</th>
            <td>
              {{
                installation.profile
                  ? labels[String(installation.profile)]
                  : toolBuild.version
                    ? '独立工具目录'
                    : errorText(inspection.doctorError) || '尚未接入'
              }}
            </td>
          </tr>
          <tr v-if="installation.profile">
            <th scope="row">受管内容</th>
            <td>
              {{
                inspectionDifferences.length
                  ? `${inspectionDifferences.length} 处本地差异`
                  : '与安装记录一致'
              }}
            </td>
          </tr>
          <tr v-if="Array.isArray(pendingTransactions)">
            <th scope="row">待恢复事务</th>
            <td :class="pendingTransactions.length ? 'warning-text' : ''">
              {{ pendingTransactions.length }} 项
            </td>
          </tr>
          <tr v-if="toolBuild.version">
            <th scope="row">磁盘工具版本</th>
            <td>{{ toolBuild.version }}</td>
          </tr>
          <tr>
            <th scope="row">Issue tracker</th>
            <td>
              <span v-for="[key, value] in trackerFields" :key="key" class="tracker-field"
                >{{ key }} · {{ value }}</span
              ><span v-if="!trackerFields.length" class="muted">未配置</span>
            </td>
          </tr>
        </tbody>
      </table>
      <details v-if="skillRows.length">
        <summary>技能安装状态（{{ skillRows.length }}）</summary>
        <div class="table-scroll" tabindex="0" role="region" aria-label="技能安装状态">
          <table class="spec-table">
            <thead>
              <tr>
                <th scope="col">技能</th>
                <th scope="col">状态</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="[name, status] in skillRows" :key="name">
                <th scope="row">{{ name }}</th>
                <td>{{ skillStatus[String(status)] ?? status }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </details>
      <details v-if="inspectionDifferences.length">
        <summary>查看本地文件差异</summary>
        <ul class="readable-list">
          <li v-for="item in inspectionDifferences" :key="String(item.path)">
            <span class="path-text">{{ item.path }}</span><span>{{
              actionLabels[String(item.action)] ?? item.action
            }}</span>
          </li>
        </ul>
      </details>
      <details>
        <summary>完整诊断数据</summary>
        <p v-if="inspection.skillsError" class="warning-text">
          {{ errorText(inspection.skillsError) }}
        </p>
        <p v-if="inspection.toolError" class="warning-text">
          {{ errorText(inspection.toolError) }}
        </p>
        <pre>{{ readableValue(inspection) }}</pre>
      </details>
    </section>
    <section v-if="preview" class="result-details execution-preview" aria-label="执行预览">
      <div class="section-heading">
        <h3>
          {{
            catalog?.operations.find((item) => item.id === preview?.request.operation)?.label
          }}预览
        </h3>
        <span class="muted">{{ selected?.mutating ? '核对后执行' : '只读操作' }}</span>
      </div>
      <table class="spec-table facts-table">
        <caption class="sr-only">
          操作目标与版本
        </caption>
        <tbody>
          <tr v-for="fact in facts" :key="fact.label">
            <th scope="row">{{ fact.label }}</th>
            <td class="path-text">{{ fact.value }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="changes.length || preserved.length" class="change-summary">
        {{ changes.length }} 项文件变更<span v-if="preserved.length">
          · 保留 {{ preserved.length }} 项本地内容</span
        >
      </p>
      <p v-for="warning in warnings" :key="warning" class="warning-text">{{ warning }}</p>
      <div
        v-if="changes.length"
        class="table-scroll change-table"
        tabindex="0"
        role="region"
        aria-label="计划文件变化"
      >
        <table class="spec-table">
          <caption class="sr-only">
            逐项计划文件变化
          </caption>
          <thead>
            <tr>
              <th scope="col">文件</th>
              <th scope="col">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in visibleChanges" :key="item.path">
              <td class="path-text">{{ item.path }}</td>
              <td>{{ actionLabels[item.action] ?? item.action }}</td>
            </tr>
          </tbody>
        </table>
        <Button
          v-if="changes.length > changeLimit"
          type="button"
          variant="ghost"
          @click="changeLimit += 50"
          >继续显示（还有 {{ changes.length - changeLimit }} 项）</Button
        >
      </div>
      <div
        v-if="resultSkillRows.length"
        class="table-scroll"
        tabindex="0"
        role="region"
        aria-label="可读取的技能状态"
      >
        <table class="spec-table">
          <thead>
            <tr>
              <th scope="col">技能</th>
              <th scope="col">状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="[name, status] in resultSkillRows" :key="name">
              <th scope="row">{{ name }}</th>
              <td>{{ skillStatus[String(status)] ?? status }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <details v-if="preserved.length">
        <summary>保留的本地内容</summary>
        <ul class="readable-list">
          <li v-for="path in preserved" :key="String(path)" class="path-text">{{ path }}</li>
        </ul>
      </details>
      <details>
        <summary>原始报告与来源</summary>
        <pre>{{ readableValue(preview.report) }}</pre>
      </details>
      <p v-if="!previewCurrent" class="notice warning">参数已变化，请重新查看执行内容。</p>
      <div class="form-actions">
        <Button type="button" :disabled="busy || !previewCurrent" @click="execute">{{
          selected?.mutating ? '执行此操作' : '执行读取'
        }}</Button>
      </div>
    </section>
  </section>
</template>
<style scoped>
.engineering-panel label {
  min-width: 0;
}
.operation-note {
  margin: 0 !important;
}
.parameter-details .form-grid {
  margin-top: 12px;
}
.facts-table th {
  width: 150px;
  white-space: nowrap;
  font-weight: 500;
}
.facts-table td {
  white-space: normal;
  overflow-wrap: anywhere;
}
.facts-table {
  min-width: 0;
}
.inspection .section-heading > span {
  max-width: 70%;
  overflow-wrap: anywhere;
}
.tracker-field {
  display: block;
}
.change-summary {
  margin: 14px 0;
  font-variant-numeric: tabular-nums;
}
.change-table {
  max-height: 420px;
}
.change-table table {
  min-width: 440px;
}
.change-table td:first-child {
  white-space: normal;
  overflow-wrap: anywhere;
}
.change-table td:last-child {
  white-space: nowrap;
  width: 100px;
}
.change-table thead {
  position: sticky;
  top: 0;
  z-index: 1;
}
.readable-list {
  list-style: none;
  padding: 0;
  margin: 0;
}
.readable-list li {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 8px 0;
  border-bottom: 1px solid var(--yss-colors-border);
  overflow-wrap: anywhere;
}
.execution-preview {
  padding-top: 18px;
  border-top: 1px solid var(--yss-colors-border);
}
@media (width < 720px) {
  .facts-table th {
    width: 100px;
  }
  .inspection .section-heading {
    align-items: flex-start;
    flex-direction: column;
  }
  .inspection .section-heading > span {
    max-width: 100%;
  }
}
</style>
