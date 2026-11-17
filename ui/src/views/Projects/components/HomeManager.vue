<script setup lang="ts">
import { reactive, shallowRef, computed, onMounted, ref, useId } from 'vue'
import { Button } from '@/components/ui/button'
import { useWorkbench } from '@/hooks/useWorkbench'
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
const preview = shallowRef<HomeImport>()
const manualRows = shallowRef('')
const manualEntries = ref<HomeRow[]>([])
const formId = useId()
const sourceFingerprint = shallowRef('')
const root = shallowRef('')
const sourcePath = shallowRef('')
const open = shallowRef(false)
const mapping = reactive<Record<string, string>>({})
const bindings = reactive<Record<string, string>>({})
const error = shallowRef('')
const busy = shallowRef(false)
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
async function load() {
  try {
    homes.value = (await getHomes()).homes
  } catch (cause) {
    error.value = errorMessage(cause)
  }
}
onMounted(load)
async function inspect() {
  if (busy.value) return
  busy.value = true
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
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
function edit(home: HomeImport) {
  preview.value = home
  setEntries(home)
  root.value = home.root
  sourcePath.value = home.sourcePath
  sourceFingerprint.value = sourceKey.value
  open.value = true
  for (const row of home.rows)
    bindings[row.id] = home.bindings?.find((item) => item.entryId === row.id)?.rootPath ?? ''
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
  open.value = !open.value
}
async function reviewRows() {
  if (!preview.value || busy.value) return
  if (!sourceCurrent.value) {
    error.value = '地图来源已变化，请重新读取后继续。'
    return
  }
  busy.value = true
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
    busy.value = false
  }
}
async function save() {
  if (!preview.value || busy.value) return
  if (!sourceCurrent.value) {
    error.value = '地图来源已变化，请重新读取后继续。'
    return
  }
  busy.value = true
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
    busy.value = false
  }
}
</script>
<template>
  <section class="platform-section home-manager" aria-labelledby="home-heading">
    <div class="section-heading">
      <div>
        <h2 id="home-heading">产品 Home</h2>
        <p class="muted">沿用项目仓库地图，绑定本机工程目录。</p>
      </div>
      <Button
        type="button"
        variant="outline"
        :aria-expanded="open"
        :aria-controls="formId"
        :disabled="busy"
        @click="beginImport"
        >{{ open ? '收起导入' : '导入 Home 地图' }}</Button
      >
    </div>
    <p v-if="notice" role="status" class="notice">{{ notice }}</p>
    <p v-if="error" role="alert" class="notice error">{{ error }}</p>
    <div v-if="homes.length" class="home-list">
      <article v-for="home in homes" :key="home.id" class="home-record">
        <div class="home-record-heading">
          <div class="home-identity">
            <strong>{{ home.root.split('/').at(-1) }}</strong><span class="row-sub path-text">{{
              home.root
            }}</span>
          </div>
          <span class="muted home-count"
            >{{ home.bindings?.length ?? 0 }} / {{ home.rows.length }} 已绑定</span
          ><span :class="home.stale ? 'warning-text' : 'muted'">{{
            home.stale ? '地图已变化' : home.requiresReview ? '待核对' : '已读取'
          }}</span
          ><Button type="button" variant="ghost" :disabled="busy" @click="edit(home)">{{
            home.stale ? '重新核对' : '管理绑定'
          }}</Button>
        </div>
        <details v-if="home.bindings?.length">
          <summary>查看已绑定工程</summary>
          <ul class="binding-list">
            <li v-for="binding in home.bindings" :key="binding.entryId">
              <RouterLink v-if="binding.projectId" :to="`/projects/${binding.projectId}`">{{
                binding.entryId
              }}</RouterLink><strong v-else>{{ binding.entryId }}</strong
              ><span class="path-text">{{ binding.rootPath }}</span>
            </li>
          </ul>
        </details>
      </article>
    </div>
    <div v-if="open" :id="formId" class="home-task">
      <details class="source-step" :open="!preview">
        <summary>{{ preview ? `地图来源 · ${preview.sourcePath}` : '选择地图来源' }}</summary>
        <form class="form-stack" aria-label="导入 Home" @submit.prevent="inspect">
          <fieldset :disabled="busy" class="plain-fieldset form-stack">
            <legend class="sr-only">地图来源</legend>
            <div class="form-grid">
              <label
                >Home 根目录（必填）<input
                  v-model="root"
                  name="root"
                  required
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none"
                  placeholder="本机绝对路径" /></label
              ><label
                >地图相对路径<input
                  v-model="sourcePath"
                  name="sourcePath"
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none"
                  placeholder="REPOSITORIES.md 或已有 README"
              /></label>
            </div>
            <details>
              <summary>设置非标准表头</summary>
              <div class="form-grid">
                <label v-for="column in columns" :key="column.key"
                  >{{ column.label }}<input v-model="mapping[column.key]" :name="column.key"
                /></label>
              </div>
            </details>
            <div class="form-actions">
              <Button :disabled="busy">{{ busy ? '读取中…' : '读取地图' }}</Button>
            </div>
          </fieldset>
        </form>
      </details>
      <section v-if="preview" class="home-preview" aria-label="核对工程并绑定目录">
        <div class="section-heading">
          <h3>{{ preview.requiresReview ? '核对地图条目' : '绑定本机工程' }}</h3>
          <span class="muted">{{ preview.rows.length }} 个条目</span>
        </div>
        <p v-if="!sourceCurrent" class="notice warning">地图来源已变化，请重新读取后继续绑定。</p>
        <ul v-if="preview.warnings.length" class="notice warning">
          <li v-for="warning in preview.warnings" :key="warning">{{ warning }}</li>
        </ul>
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
              <table class="spec-table">
                <caption class="sr-only">
                  可编辑的 Home 工程条目
                </caption>
                <thead>
                  <tr>
                    <th scope="col">工程身份</th>
                    <th scope="col">职责与资料</th>
                    <th scope="col">操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(row, index) in manualEntries" :key="index">
                    <td>
                      <label
                        >工程标识<input v-model="row.id" :name="`id-${index}`" required /></label
                      ><label
                        >Git 身份与子路径<input
                          v-model="row.git"
                          :name="`git-${index}`"
                          spellcheck="false"
                          autocorrect="off"
                      /></label>
                    </td>
                    <td>
                      <label
                        >职责<input
                          v-model="row.responsibility"
                          :name="`responsibility-${index}`" /></label
                      ><label
                        >资料入口<input v-model="row.documents" :name="`documents-${index}`"
                      /></label>
                      <details>
                        <summary>关联与定位提示</summary>
                        <label
                          >关联<input v-model="row.relations" :name="`relations-${index}`" /></label
                        ><label
                          >查找提示<input v-model="row.hints" :name="`hints-${index}`"
                        /></label>
                      </details>
                    </td>
                    <td>
                      <Button
                        type="button"
                        variant="ghost"
                        :disabled="busy"
                        :aria-label="`移除第 ${index + 1} 个条目`"
                        @click="manualEntries.splice(index, 1)"
                        >移除</Button
                      >
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="inline-actions">
              <Button type="button" variant="outline" :disabled="busy" @click="addEntry"
                >新增条目</Button
              ><Button :disabled="busy || !sourceCurrent">采用核对后的条目</Button>
            </div>
            <details>
              <summary>使用 JSON 编辑条目</summary>
              <label
                >条目 JSON<textarea
                  v-model="manualRows"
                  name="rows"
                  rows="8"
                  spellcheck="false"
                  autocorrect="off"
                /></label
              ><Button type="button" variant="outline" :disabled="busy" @click="applyJson"
                >将 JSON 填入表格</Button
              >
            </details>
          </fieldset>
        </form>
        <template v-else>
          <div
            class="table-scroll home-bindings"
            tabindex="0"
            role="region"
            aria-label="Home 本机目录绑定"
          >
            <table class="spec-table">
              <caption class="sr-only">
                地图工程与本机目录
              </caption>
              <thead>
                <tr>
                  <th scope="col">工程</th>
                  <th scope="col">职责与资料</th>
                  <th scope="col">本机绝对路径</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in preview.rows" :key="row.id">
                  <th scope="row">
                    {{ row.id }}<span class="row-sub path-text">{{ row.git }}</span>
                  </th>
                  <td>
                    {{ row.responsibility }}
                    <details v-if="row.documents || row.relations || row.hints">
                      <summary>资料与关联</summary>
                      <p class="preserve-lines">{{ row.documents }}</p>
                      <p class="muted">{{ row.relations }}</p>
                      <p class="muted">{{ row.hints }}</p>
                    </details>
                  </td>
                  <td>
                    <label :for="`${formId}-${row.id}`" class="sr-only"
                      >{{ row.id }} 的本机目录</label
                    ><input
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
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <datalist :id="`${formId}-roots`">
            <option v-for="project in projectRoots" :key="project.root" :value="project.root">
              {{ project.name }}
            </option>
          </datalist>
          <div class="form-actions">
            <Button type="button" variant="outline" :disabled="busy" @click="open = false"
              >稍后绑定</Button
            ><Button type="button" :disabled="busy || !sourceCurrent" @click="save">{{
              busy ? '保存中…' : '保存工程绑定'
            }}</Button>
          </div>
        </template>
      </section>
    </div>
    <p v-if="!homes.length && !open" class="muted">从现有仓库地图导入产品，集中找到关联工程。</p>
  </section>
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
.home-record details {
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
.home-bindings table,
.home-review table {
  min-width: 680px;
}
.home-bindings td,
.home-bindings th {
  white-space: normal;
  overflow-wrap: anywhere;
  min-width: 180px;
}
.home-bindings td:last-child {
  width: 42%;
  min-width: 280px;
}
.home-review td {
  min-width: 250px;
  vertical-align: top;
}
.home-review td:last-child {
  min-width: 70px;
}
.home-review label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 10px;
}
.source-step > summary {
  font-weight: 550;
}
@media (width < 720px) {
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
