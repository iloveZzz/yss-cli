<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import WorkbenchErrorAlert from '@/components/WorkbenchErrorAlert.vue'
import { Card } from '@/components/ui/card'
import { Alert } from '@/components/ui/alert'
import { Textarea } from '@/components/ui/textarea'
import { Input } from '@/components/ui/input'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, onMounted, reactive, shallowRef, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { FieldLabel } from '@/components/ui/field'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import SingleSelect from '@/components/SingleSelect.vue'
import { errorFeedback, type ErrorFeedback } from '@/services/workbench'
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
const id = useId()
const route = useRoute()
const catalog = shallowRef<EngineeringCatalog>()
const catalogLoading = shallowRef(true)
const catalogError = shallowRef<ErrorFeedback>()
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
const action = shallowRef<'inspect' | 'prepare' | 'execute'>()
const busy = computed(() => !!action.value)
const error = shallowRef<ErrorFeedback>()
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
async function loadCatalog() {
  catalogLoading.value = true
  catalogError.value = undefined
  try {
    catalog.value = await getEngineeringCatalog()
    if (
      typeof route.query.operation === 'string' &&
      catalog.value.operations.some((item) => item.id === route.query.operation)
    )
      operation.value = route.query.operation
    recipe.value = catalog.value.scaffold.defaultRecipeId ?? recipes.value[0]?.id ?? ''
  } catch (cause) {
    catalogError.value = errorFeedback(cause)
  } finally {
    catalogLoading.value = false
  }
}
onMounted(loadCatalog)
async function inspect() {
  if (busy.value) return
  action.value = 'inspect'
  error.value = undefined
  try {
    inspection.value = await inspectEngineering(values.root ?? '')
  } catch (cause) {
    error.value = errorFeedback(cause)
  } finally {
    action.value = undefined
  }
}
async function prepare() {
  if (busy.value) return
  previewSource.value = ''
  changeLimit.value = 20
  action.value = 'prepare'
  error.value = undefined
  try {
    const encoded = JSON.stringify(request.value)
    preview.value = await previewEngineering(request.value)
    previewSource.value = encoded
  } catch (cause) {
    error.value = errorFeedback(cause)
  } finally {
    action.value = undefined
  }
}
async function execute() {
  if (!preview.value || !previewCurrent.value || busy.value) return
  action.value = 'execute'
  error.value = undefined
  try {
    const job = await executeEngineering(preview.value.id)
    await router.push(`/jobs/${job.id}`)
  } catch (cause) {
    error.value = errorFeedback(cause)
  } finally {
    action.value = undefined
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
  <Card class="block gap-0 py-0 shadow-none platform-section engineering-panel">
    <div class="section-heading">
      <div>
        <h2>工程维护</h2>
        <p v-if="catalog" class="muted">
          YSS {{ catalog.processVersion }} · Bundle {{ catalog.bundleDigest.slice(0, 12) }}
        </p>
      </div>
      <Button type="button" variant="outline" :disabled="busy || !values.root" @click="inspect">
        <LoadingIndicator :active="action === 'inspect'" />
        {{ action === 'inspect' ? '读取中…' : '读取工程状态' }}
      </Button>
    </div>
    <WorkbenchErrorAlert v-if="error" :feedback="error" />
    <WorkbenchErrorAlert v-if="catalogError" :feedback="catalogError">
      <Button class="self-start" type="button" variant="outline" :disabled="catalogLoading" @click="loadCatalog">
        重试读取可用操作
      </Button>
    </WorkbenchErrorAlert>
    <LoadingSkeleton
      v-if="catalogLoading && !catalog"
      variant="form"
      :rows="2"
      label="正在读取工程维护操作"
    />
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
            <Textarea
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
            <Input
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
          <Collapsible v-else-if="field.type === 'object'" class="parameter-details">
            <CollapsibleTrigger>项目资料变量（可选）</CollapsibleTrigger>
            <CollapsibleContent>
              <div class="form-grid">
                <label v-for="name in options(field.options)" :key="name"
                  >{{ variableLabels[name] ?? name }}<Input v-model="variables[name]" :name="name"
                /></label>
              </div>
            </CollapsibleContent>
          </Collapsible>
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
                >{{ item.label }}（必填）<Input
                  :model-value="String(backend[item.name] ?? '')"
                  :name="item.name"
                  required
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none"
                  @update:model-value="backend[item.name] = String($event)"
              /></label>
            </div>
            <details :open="!recipe">
              <summary>核对或自定义技术坐标</summary>
              <div class="form-grid">
                <label v-for="item in backendLabels.slice(4)" :key="item.name"
                  >{{ item.label
                  }}<Input
                    :model-value="String(backend[item.name] ?? '')"
                    :name="item.name"
                    :type="item.name === 'javaVersion' ? 'number' : 'text'"
                    required
                    spellcheck="false"
                    autocorrect="off"
                    autocapitalize="none"
                    @update:model-value="
                      backend[item.name] = item.name === 'javaVersion' ? Number($event) : String($event)
                    "
                /></label>
              </div>
            </details>
          </fieldset>
          <fieldset v-else-if="field.type === 'upgrade'" class="plain-fieldset form-stack">
            <legend :id="`${id}-upgrade-legend`">升级来源</legend>
            <RadioGroup
              v-model="upgradeMode"
              name="upgradeMode"
              class="flex flex-wrap gap-5"
              :aria-labelledby="`${id}-upgrade-legend`"
              :disabled="busy"
            >
              <FieldLabel :for="`${id}-upgrade-online`" class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent">
                <RadioGroupItem :id="`${id}-upgrade-online`" value="online" />在线发行
              </FieldLabel>
              <FieldLabel :for="`${id}-upgrade-offline`" class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent">
                <RadioGroupItem :id="`${id}-upgrade-offline`" value="offline" />离线归档
              </FieldLabel>
            </RadioGroup>
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
                >离线归档绝对路径（必填）<Input
                  v-model="upgrade.artifact"
                  name="artifact"
                  required
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none" /></label
              ><label
                >归档 SHA-256（必填）<Input
                  v-model="upgrade.sha256"
                  name="sha256"
                  required
                  pattern="[a-fA-F0-9]{64}"
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none" /></label
            ></template>
            <label
              >目标版本<Input
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
          <Button type="submit" :disabled="busy">
            <LoadingIndicator :active="action === 'prepare'" />
            {{ action === 'prepare' ? '正在核对…' : '查看执行内容' }}
          </Button>
        </div>
      </fieldset>
    </form>
    <section v-if="inspection" class="result-details inspection" aria-label="工程状态" :aria-busy="action === 'inspect'">
      <div class="section-heading">
        <h3>工程状态</h3>
        <span class="path-text">{{ inspection.root }}</span>
      </div>
      <Table class="spec-table facts-table">
        <TableCaption class="sr-only">
          安装身份与文件诊断摘要
        </TableCaption>
        <TableBody>
          <TableRow>
            <TableHead scope="row">工程安装</TableHead>
            <TableCell>
              {{
                installation.profile
                  ? labels[String(installation.profile)]
                  : toolBuild.version
                    ? '独立工具目录'
                    : errorText(inspection.doctorError) || '尚未接入'
              }}
            </TableCell>
          </TableRow>
          <TableRow v-if="installation.profile">
            <TableHead scope="row">受管内容</TableHead>
            <TableCell>
              {{
                inspectionDifferences.length
                  ? `${inspectionDifferences.length} 处本地差异`
                  : '与安装记录一致'
              }}
            </TableCell>
          </TableRow>
          <TableRow v-if="Array.isArray(pendingTransactions)">
            <TableHead scope="row">待恢复事务</TableHead>
            <TableCell :class="pendingTransactions.length ? 'warning-text' : ''">
              {{ pendingTransactions.length }} 项
            </TableCell>
          </TableRow>
          <TableRow v-if="toolBuild.version">
            <TableHead scope="row">磁盘工具版本</TableHead>
            <TableCell>{{ toolBuild.version }}</TableCell>
          </TableRow>
          <TableRow>
            <TableHead scope="row">Issue tracker</TableHead>
            <TableCell>
              <span v-for="[key, value] in trackerFields" :key="key" class="tracker-field"
                >{{ key }} · {{ value }}</span
              ><span v-if="!trackerFields.length" class="muted">未配置</span>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <Collapsible v-if="skillRows.length">
        <CollapsibleTrigger>技能安装状态（{{ skillRows.length }}）</CollapsibleTrigger>
        <CollapsibleContent>
          <div class="table-scroll" tabindex="0" role="region" aria-label="技能安装状态">
            <Table class="spec-table">
              <TableHeader>
                <TableRow>
                  <TableHead scope="col">技能</TableHead>
                  <TableHead scope="col">状态</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="[name, status] in skillRows" :key="name">
                  <TableHead scope="row">{{ name }}</TableHead>
                  <TableCell>{{ skillStatus[String(status)] ?? status }}</TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </CollapsibleContent>
      </Collapsible>
      <Collapsible v-if="inspectionDifferences.length">
        <CollapsibleTrigger>查看本地文件差异</CollapsibleTrigger>
        <CollapsibleContent>
          <ul class="readable-list">
            <li v-for="item in inspectionDifferences" :key="String(item.path)">
              <span class="path-text">{{ item.path }}</span><span>{{
                actionLabels[String(item.action)] ?? item.action
              }}</span>
            </li>
          </ul>
        </CollapsibleContent>
      </Collapsible>
      <Collapsible>
        <CollapsibleTrigger>完整诊断数据</CollapsibleTrigger>
        <CollapsibleContent>
          <p v-if="inspection.skillsError" class="warning-text">
            {{ errorText(inspection.skillsError) }}
          </p>
          <p v-if="inspection.toolError" class="warning-text">
            {{ errorText(inspection.toolError) }}
          </p>
          <pre>{{ readableValue(inspection) }}</pre>
        </CollapsibleContent>
      </Collapsible>
    </section>
    <div v-if="preview" class="relative">
      <section class="result-details execution-preview" aria-label="执行预览" :aria-busy="action === 'prepare'">
        <div class="section-heading">
          <h3>
            {{
              catalog?.operations.find((item) => item.id === preview?.request.operation)?.label
            }}预览
          </h3>
          <span class="muted">{{ selected?.mutating ? '核对后执行' : '只读操作' }}</span>
        </div>
        <Table class="spec-table facts-table">
          <TableCaption class="sr-only">
            操作目标与版本
          </TableCaption>
          <TableBody>
            <TableRow v-for="fact in facts" :key="fact.label">
              <TableHead scope="row">{{ fact.label }}</TableHead>
              <TableCell class="path-text">{{ fact.value }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
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
          <Table class="spec-table">
            <TableCaption class="sr-only">
              逐项计划文件变化
            </TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">文件</TableHead>
                <TableHead scope="col">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="item in visibleChanges" :key="item.path">
                <TableCell class="path-text">{{ item.path }}</TableCell>
                <TableCell>{{ actionLabels[item.action] ?? item.action }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
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
          <Table class="spec-table">
            <TableHeader>
              <TableRow>
                <TableHead scope="col">技能</TableHead>
                <TableHead scope="col">状态</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="[name, status] in resultSkillRows" :key="name">
                <TableHead scope="row">{{ name }}</TableHead>
                <TableCell>{{ skillStatus[String(status)] ?? status }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
        <Collapsible v-if="preserved.length">
          <CollapsibleTrigger>保留的本地内容</CollapsibleTrigger>
          <CollapsibleContent>
            <ul class="readable-list">
              <li v-for="path in preserved" :key="String(path)" class="path-text">{{ path }}</li>
            </ul>
          </CollapsibleContent>
        </Collapsible>
        <Collapsible>
          <CollapsibleTrigger>原始报告与来源</CollapsibleTrigger>
          <CollapsibleContent>
            <pre>{{ readableValue(preview.report) }}</pre>
          </CollapsibleContent>
        </Collapsible>
        <Alert v-if="!previewCurrent && action !== 'prepare'" class="notice warning">参数已变化，请重新查看执行内容。</Alert>
        <div class="form-actions">
          <Button type="button" :disabled="busy || !previewCurrent" @click="execute">
            <LoadingIndicator :active="action === 'execute'" />
            {{ action === 'execute' ? '正在发起…' : selected?.mutating ? '执行此操作' : '执行读取' }}
          </Button>
        </div>
      </section>
      <LoadingIndicator
        :active="action === 'prepare'"
        placement="overlay"
        label="正在核对，暂显示上次预览"
      />
    </div>
    <LoadingSkeleton
      v-else-if="action === 'prepare'"
      variant="table"
      :rows="3"
      label="正在读取执行预览"
    />
  </Card>
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
.facts-table :deep(th) {
  width: 150px;
  white-space: nowrap;
  font-weight: 500;
}
.facts-table :deep(td) {
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
.change-table :deep(table) {
  min-width: 440px;
}
.change-table :deep(td:first-child) {
  white-space: normal;
  overflow-wrap: anywhere;
}
.change-table :deep(td:last-child) {
  white-space: nowrap;
  width: 100px;
}
.change-table :deep(thead) {
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
  .facts-table :deep(th) {
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
