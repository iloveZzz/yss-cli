<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Card } from '@/components/ui/card'
import { Alert } from '@/components/ui/alert'
import { Textarea } from '@/components/ui/textarea'
import { Input } from '@/components/ui/input'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { reactive, shallowRef, computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { useWorkbench } from '@/hooks/useWorkbench'
import { registerWorkbenchRouteReads } from '@/hooks/useWorkbenchReads'
import { errorMessage } from '@/services/workbench'
import {
  getHomes,
  importHome,
  bindHome,
  type HomeImport,
  type HomeRow
} from '@/services/workbench-engineering'
const { snapshot, refresh } = useWorkbench()
const homes = shallowRef<HomeImport[]>([])
const loading = shallowRef(true)
const readAt = shallowRef<string>()
const loadError = shallowRef('')
const preview = shallowRef<HomeImport>()
const manualRows = shallowRef('')
const manualEntries = ref<HomeRow[]>([])
const formId = useId()
const sourceFingerprint = shallowRef('')
const root = shallowRef('')
const sourcePath = shallowRef('')
const open = shallowRef(false)
const sourceOpen = shallowRef(true)
const previewHeading = shallowRef<HTMLHeadingElement>()
const mapping = reactive<Record<string, string>>({})
const bindings = reactive<Record<string, string>>({})
const error = shallowRef('')
const action = shallowRef<'inspect' | 'review' | 'bind'>()
const busy = computed(() => !!action.value)
const notice = shallowRef('')
const sourceKey = computed(() =>
  JSON.stringify({ root: root.value, sourcePath: sourcePath.value, mapping })
)
const sourceCurrent = computed(
  () => !!preview.value && !preview.value.stale && sourceFingerprint.value === sourceKey.value
)
const columns = [
  { key: 'id', label: '工程标识列' },
  { key: 'responsibility', label: '职责列' },
  { key: 'git', label: 'Git列' },
  { key: 'documents', label: '资料入口列' },
  { key: 'relations', label: '关系列' },
  { key: 'hints', label: '定位提示列' }
]
const projectRoots = computed(
  () =>
    snapshot.value?.projects.map((project) => ({
      name: project.record.name,
      root: project.record.rootPath
    })) ?? []
)
let loadingHomes: AbortController | undefined
async function load() {
  loadingHomes?.abort()
  const controller = new AbortController()
  loadingHomes = controller
  loading.value = true
  try {
    const result = await getHomes(controller.signal)
    if (controller.signal.aborted) return
    homes.value = result.homes
    loadError.value = ''
    readAt.value = new Date().toISOString()
  } catch (cause) {
    if (!controller.signal.aborted) loadError.value = errorMessage(cause)
  } finally {
    if (loadingHomes === controller) {
      loadingHomes = undefined
      loading.value = false
    }
  }
}
registerWorkbenchRouteReads({ busy: computed(() => loading.value || busy.value), readAt, refresh: load })
onMounted(load)
onBeforeUnmount(() => loadingHomes?.abort())
async function inspect(event: SubmitEvent) {
  if (busy.value) return
  const sourceForm = event.currentTarget as HTMLFormElement
  const initialFocus = document.activeElement
  action.value = 'inspect'
  error.value = ''
  notice.value = ''
  sourceFingerprint.value = ''
  const fingerprint = sourceKey.value
  try {
    const previous = preview.value
    const previousBindings = { ...bindings }
    preview.value = await importHome(
      root.value,
      sourcePath.value,
      Object.fromEntries(Object.entries(mapping).filter(([, value]) => value.trim()))
    )
    setEntries(preview.value)
    sourceFingerprint.value = fingerprint
    const known = homes.value.find((home) => home.id === preview.value?.id)
    for (const row of preview.value.rows)
      bindings[row.id] =
        (previous?.root === preview.value.root ? previousBindings[row.id] : '') ||
        known?.bindings?.find((item) => item.entryId === row.id)?.rootPath ||
        ''
    const moveFocus = sourceForm.contains(initialFocus) &&
      (document.activeElement === document.body || sourceForm.contains(document.activeElement))
    sourceOpen.value = false
    if (moveFocus) await nextTick(() => previewHeading.value?.focus())
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    action.value = undefined
  }
}
function edit(home: HomeImport) {
  preview.value = home
  setEntries(home)
  root.value = home.root
  sourcePath.value = home.sourcePath
  sourceFingerprint.value = sourceKey.value
  sourceOpen.value = false
  open.value = true
  for (const row of home.rows)
    bindings[row.id] = home.bindings?.find((item) => item.entryId === row.id)?.rootPath ?? ''
  void nextTick(() => previewHeading.value?.focus())
}
function setEntries(home: HomeImport) {
  manualEntries.value = structuredClone(home.rows)
  manualRows.value = JSON.stringify(home.rows, null, 2)
}
function addEntry() {
  manualEntries.value.push({
    id: '',
    responsibility: '',
    git: '',
    documents: '',
    relations: '',
    hints: ''
  })
}
function applyJson() {
  error.value = ''
  try {
    const rows: unknown = JSON.parse(manualRows.value)
    if (!Array.isArray(rows)) throw new Error('工程条目须为 JSON 数组。')
    if (
      !rows.every(
        (row) =>
          row &&
          typeof row === 'object' &&
          ['id', 'responsibility', 'git', 'documents', 'relations'].every(
            (key) => typeof row[key] === 'string'
          ) &&
          (row.hints === undefined || typeof row.hints === 'string')
      )
    )
      throw new Error(
        '每个条目需填写字符串类型的 id、responsibility、git、documents 和 relations。'
      )
    manualEntries.value = rows.map((row) => ({ ...row, hints: row.hints ?? '' }))
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}
function beginImport() {
  if (!open.value) sourceOpen.value = !preview.value
  open.value = !open.value
}
function focusImportTrigger() {
  document.getElementById(`${formId}-trigger`)?.focus()
}
function dismissImport() {
  open.value = false
  void nextTick(focusImportTrigger)
}
async function reviewRows() {
  if (!preview.value || busy.value) return
  if (!sourceCurrent.value) {
    error.value = '地图来源已变化，请重新读取后继续。'
    return
  }
  action.value = 'review'
  error.value = ''
  try {
    preview.value = await importHome(
      preview.value.root,
      preview.value.sourcePath,
      undefined,
      manualEntries.value
    )
    setEntries(preview.value)
    notice.value = '已保存人工核对的地图条目，可继续绑定工程。'
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    action.value = undefined
  }
}
async function save() {
  if (!preview.value || busy.value) return
  if (!sourceCurrent.value) {
    error.value = '地图来源已变化，请重新读取后继续。'
    return
  }
  action.value = 'bind'
  error.value = ''
  try {
    await bindHome(
      preview.value.id,
      preview.value.sourceDigest,
      preview.value.rows
        .filter((row) => bindings[row.id]?.trim())
        .map((row) => ({ entryId: row.id, rootPath: bindings[row.id]!.trim() }))
    )
    notice.value = 'Home 工程绑定已保存。'
    preview.value = undefined
    open.value = false
    await load()
    await refresh()
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    action.value = undefined
    if (!open.value) await nextTick(focusImportTrigger)
  }
}
</script>
<template>
  <Card class="block gap-0 py-0 shadow-none platform-section home-manager" role="region" aria-labelledby="home-heading">
    <div class="section-heading">
      <div>
        <h2 id="home-heading">产品 Home</h2>
        <p class="muted">沿用项目仓库地图，绑定本机工程目录。</p>
      </div>
      <Button
        :id="`${formId}-trigger`"
        type="button"
        variant="outline"
        :aria-expanded="open"
        :aria-controls="formId"
        :disabled="busy"
        @click="beginImport"
        >{{ open ? '收起导入' : '导入 Home 地图' }}</Button
      >
    </div>
    <Alert v-if="notice" role="status" class="notice">{{ notice }}</Alert>
    <Alert v-if="error" role="alert" class="notice error" variant="destructive">{{ error }}</Alert>
    <Alert v-if="loadError" role="alert" class="notice error" variant="destructive">{{ loadError }}</Alert>
    <div v-if="homes.length" class="home-list" :aria-busy="loading">
      <article v-for="home in homes" :key="home.id" class="home-record">
        <div class="home-record-heading">
          <div class="home-identity">
            <strong>{{ home.root.split('/').at(-1) }}</strong><span class="row-sub path-text">{{
              home.root
            }}</span>
          </div>
          <span class="muted home-count"
            >{{ home.bindings?.length ?? 0 }} / {{ home.rows.length }} 已绑定</span
          ><Badge variant="outline" :class="{ 'warning-text': home.stale || home.requiresReview }">{{
            home.stale ? '地图已变化' : home.requiresReview ? '待核对' : '已读取'
          }}</Badge
          ><Button type="button" variant="ghost" :disabled="busy" @click="edit(home)">{{
            home.stale ? '重新核对' : '管理绑定'
          }}</Button>
        </div>
        <Collapsible v-if="home.bindings?.length">
          <CollapsibleTrigger>查看已绑定工程</CollapsibleTrigger>
          <CollapsibleContent>
            <ul class="binding-list">
              <li v-for="binding in home.bindings" :key="binding.entryId">
                <RouterLink v-if="binding.projectId" :to="`/projects/${binding.projectId}`">{{
                  binding.entryId
                }}</RouterLink><strong v-else>{{ binding.entryId }}</strong
                ><span class="path-text">{{ binding.rootPath }}</span>
              </li>
            </ul>
          </CollapsibleContent>
        </Collapsible>
      </article>
    </div>
    <div v-if="open" :id="formId" class="home-task">
      <Collapsible class="source-step" v-model:open="sourceOpen" :unmount-on-hide="false">
        <CollapsibleTrigger>{{ preview ? `地图来源 · ${preview.sourcePath}` : '选择地图来源' }}</CollapsibleTrigger>
        <CollapsibleContent>
          <form class="form-stack" aria-label="导入 Home" @submit.prevent="inspect">
            <fieldset :disabled="busy" class="plain-fieldset form-stack">
              <legend class="sr-only">地图来源</legend>
              <div class="form-grid">
                <label
                  >Home 根目录（必填）<Input
                    v-model="root"
                    name="root"
                    required
                    spellcheck="false"
                    autocorrect="off"
                    autocapitalize="none"
                    placeholder="本机绝对路径" /></label
                ><label
                  >地图相对路径<Input
                    v-model="sourcePath"
                    name="sourcePath"
                    spellcheck="false"
                    autocorrect="off"
                    autocapitalize="none"
                    placeholder="REPOSITORIES.md 或已有 README"
                /></label>
              </div>
              <Collapsible :unmount-on-hide="false">
                <CollapsibleTrigger>设置非标准表头</CollapsibleTrigger>
                <CollapsibleContent>
                  <div class="form-grid">
                    <label v-for="column in columns" :key="column.key"
                      >{{ column.label }}<Input v-model="mapping[column.key]" :name="column.key"
                    /></label>
                  </div>
                </CollapsibleContent>
              </Collapsible>
              <div class="form-actions">
                <Button type="submit" :disabled="busy">
                  <LoadingIndicator :active="action === 'inspect'" />
                  {{ action === 'inspect' ? '读取中…' : '读取地图' }}
                </Button>
              </div>
            </fieldset>
          </form>
        </CollapsibleContent>
      </Collapsible>
      <section v-if="preview" class="home-preview" aria-label="核对工程并绑定目录">
        <Separator class="mb-4" />
        <div class="section-heading">
          <h3 ref="previewHeading" tabindex="-1">{{ preview.requiresReview ? '核对地图条目' : '绑定本机工程' }}</h3>
          <span class="muted">{{ preview.rows.length }} 个条目</span>
        </div>
        <Alert v-if="!sourceCurrent" class="notice warning">地图来源已变化，请重新读取后继续绑定。</Alert>
        <Alert v-if="preview.warnings.length" class="notice warning">
          <ul>
            <li v-for="warning in preview.warnings" :key="warning">{{ warning }}</li>
          </ul>
        </Alert>
        <form
          v-if="preview.requiresReview"
          class="form-stack"
          aria-label="人工核对地图"
          @submit.prevent="reviewRows"
        >
          <fieldset :disabled="busy" class="plain-fieldset form-stack">
            <legend class="sr-only">待核对的工程条目</legend>
            <p class="muted">补齐实际工程身份与资料后采用条目，原仓库地图继续维护关系。</p>
            <div
              class="table-scroll home-review"
              tabindex="0"
              role="region"
              aria-label="人工核对的地图条目"
            >
              <Table class="spec-table">
                <TableCaption class="sr-only">
                  可编辑的 Home 工程条目
                </TableCaption>
                <TableHeader>
                  <TableRow>
                    <TableHead scope="col">工程身份</TableHead>
                    <TableHead scope="col">职责与资料</TableHead>
                    <TableHead scope="col">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  <TableRow v-for="(row, index) in manualEntries" :key="index">
                    <TableCell>
                      <label
                        >工程标识<Input v-model="row.id" :name="`id-${index}`" required /></label
                      ><label
                        >Git 身份与子路径<Input
                          v-model="row.git"
                          :name="`git-${index}`"
                          spellcheck="false"
                          autocorrect="off"
                      /></label>
                    </TableCell>
                    <TableCell>
                      <label
                        >职责<Input
                          v-model="row.responsibility"
                          :name="`responsibility-${index}`" /></label
                      ><label
                        >资料入口<Input v-model="row.documents" :name="`documents-${index}`"
                      /></label>
                      <Collapsible :unmount-on-hide="false">
                        <CollapsibleTrigger>关联与定位提示</CollapsibleTrigger>
                        <CollapsibleContent>
                          <label
                            >关联<Input v-model="row.relations" :name="`relations-${index}`" /></label
                          ><label
                            >查找提示<Input v-model="row.hints" :name="`hints-${index}`"
                          /></label>
                        </CollapsibleContent>
                      </Collapsible>
                    </TableCell>
                    <TableCell>
                      <Button
                        type="button"
                        variant="ghost"
                        :disabled="busy"
                        :aria-label="`移除第 ${index + 1} 个条目`"
                        @click="manualEntries.splice(index, 1)"
                        >移除</Button
                      >
                    </TableCell>
                  </TableRow>
                </TableBody>
              </Table>
            </div>
            <div class="inline-actions">
              <Button type="button" variant="outline" :disabled="busy" @click="addEntry"
                >新增条目</Button
              ><Button type="submit" :disabled="busy || !sourceCurrent">
                <LoadingIndicator :active="action === 'review'" />
                {{ action === 'review' ? '保存中…' : '采用核对后的条目' }}
              </Button>
            </div>
            <Collapsible :unmount-on-hide="false">
              <CollapsibleTrigger>使用 JSON 编辑条目</CollapsibleTrigger>
              <CollapsibleContent>
                <label
                  >条目 JSON<Textarea
                    v-model="manualRows"
                    name="rows"
                    rows="8"
                    spellcheck="false"
                    autocorrect="off"
                  /></label
                ><Button type="button" variant="outline" :disabled="busy" @click="applyJson"
                  >将 JSON 填入表格</Button
                >
              </CollapsibleContent>
            </Collapsible>
          </fieldset>
        </form>
        <template v-else>
          <div
            class="table-scroll home-bindings"
            tabindex="0"
            role="region"
            aria-label="Home 本机目录绑定"
          >
            <Table class="spec-table">
              <TableCaption class="sr-only">
                地图工程与本机目录
              </TableCaption>
              <TableHeader>
                <TableRow>
                  <TableHead scope="col">工程</TableHead>
                  <TableHead scope="col">职责与资料</TableHead>
                  <TableHead scope="col">本机绝对路径</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="row in preview.rows" :key="row.id">
                  <TableHead scope="row">
                    {{ row.id }}<span class="row-sub path-text">{{ row.git }}</span>
                  </TableHead>
                  <TableCell>
                    {{ row.responsibility }}
                    <Collapsible v-if="row.documents || row.relations || row.hints">
                      <CollapsibleTrigger>资料与关联</CollapsibleTrigger>
                      <CollapsibleContent>
                        <p class="preserve-lines">{{ row.documents }}</p>
                        <p class="muted">{{ row.relations }}</p>
                        <p class="muted">{{ row.hints }}</p>
                      </CollapsibleContent>
                    </Collapsible>
                  </TableCell>
                  <TableCell>
                    <label :for="`${formId}-${row.id}`" class="sr-only"
                      >{{ row.id }} 的本机目录</label
                    ><Input
                      :id="`${formId}-${row.id}`"
                      v-model="bindings[row.id]"
                      :list="`${formId}-roots`"
                      name="binding"
                      :disabled="busy"
                      spellcheck="false"
                      autocorrect="off"
                      autocapitalize="none"
                      placeholder="选择已登记目录或填写路径"
                    />
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
          <datalist :id="`${formId}-roots`">
            <option v-for="project in projectRoots" :key="project.root" :value="project.root">
              {{ project.name }}
            </option>
          </datalist>
          <div class="form-actions">
            <Button type="button" variant="outline" :disabled="busy" @click="dismissImport"
              >稍后绑定</Button
            ><Button type="button" :disabled="busy || !sourceCurrent" @click="save">
              <LoadingIndicator :active="action === 'bind'" />
              {{ action === 'bind' ? '保存中…' : '保存工程绑定' }}
            </Button>
          </div>
        </template>
      </section>
    </div>
    <LoadingSkeleton
      v-if="loading && !homes.length && !open"
      variant="text"
      :rows="2"
      label="正在读取产品 Home"
    />
    <Empty v-else-if="!homes.length && !open && !error && !loadError" class="home-empty">
      <EmptyDescription>尚未导入仓库地图。</EmptyDescription>
    </Empty>
  </Card>
</template>
<style scoped>
.home-manager {
  padding-block: 18px;
}
.home-record {
  border-bottom: 1px solid var(--yss-colors-border);
  padding-block: 12px;
}
.home-record:last-child {
  border-bottom: 0;
}
.home-record-heading {
  display: flex;
  align-items: center;
  gap: 18px;
}
.home-identity {
  flex: 1;
  min-width: 0;
}
.home-count {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}
.home-record :deep([data-slot='collapsible']) {
  margin-top: 4px;
}
.binding-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.binding-list li {
  display: grid;
  grid-template-columns: minmax(100px, 0.5fr) minmax(0, 1fr);
  gap: 18px;
  padding: 6px 0;
}
.home-task {
  margin-top: 16px;
  border-top: 1px solid var(--yss-colors-border);
}
.home-preview {
  margin-top: 18px;
}
.home-bindings :deep(table),
.home-review :deep(table) {
  min-width: 680px;
}
.home-bindings :deep(td),
.home-bindings :deep(th) {
  white-space: normal;
  overflow-wrap: anywhere;
  min-width: 180px;
}
.home-bindings :deep(td:last-child) {
  width: 42%;
  min-width: 280px;
}
.home-review :deep(td) {
  min-width: 250px;
  vertical-align: top;
}
.home-review :deep(td:last-child) {
  min-width: 70px;
}
.home-review :deep(label) {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 10px;
}
.source-step :deep([data-slot='collapsible-trigger']) {
  font-weight: 550;
}
.home-empty {
  align-items: flex-start;
  padding: 4px 0 0;
  border: 0;
  text-align: start;
}
@media (width < 720px) {
  .home-manager .section-heading {
    flex-wrap: wrap;
  }
  .home-record-heading {
    flex-wrap: wrap;
    gap: 8px 16px;
  }
  .home-identity {
    flex-basis: 100%;
  }
  .home-record-heading button {
    margin-left: auto;
  }
  .home-count {
    margin-right: auto;
  }
}
</style>
