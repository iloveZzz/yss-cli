<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import {
  getConfirmations,
  getConfirmationDiff,
  getConfirmationArtifacts,
  captureConfirmationSnapshot,
  historicalFile,
  currentSpecFile,
  type ConfirmationSnapshot,
  type ConfirmationDiff,
  type ConfirmationArtifacts,
  type ComparisonBasis,
  type ArtifactChange
} from '@/services/workbench-platform'
import { errorMessage, dateLabel } from '@/services/workbench'
import HistoricalPreview from './HistoricalPreview.vue'
import AssetImageDiff from './AssetImageDiff.vue'
import { imageAsset } from '../schemas/assets'
const props = defineProps<{ specId: string }>()
const id = useId()
const records = shallowRef<ConfirmationSnapshot[]>([])
const selected = shallowRef('')
const basis = shallowRef<ComparisonBasis>('candidate')
const basisLabel = computed(() => basis.value === 'candidate' ? '本轮确认材料' : '当前来源')
const loadedConfirmationId = shallowRef('')
const diff = shallowRef<ConfirmationDiff>()
const artifacts = shallowRef<ConfirmationArtifacts>()
const selectedFile = shallowRef<ArtifactChange>()
const selectedPrototype = shallowRef('')
const previewTarget = shallowRef<string | null>(null)
const open = shallowRef(false)
const loading = shallowRef(false)
const downloading = shallowRef(false)
const capturing = shallowRef(false)
const error = shallowRef('')
const notice = shallowRef('')
const labels = { added: '新增', removed: '删除', modified: '修改' }
const recordOptions = computed(() =>
  records.value.map((record) => ({
    value: record.id,
    label: `${dateLabel(record.createdAt)} · ${record.artifactDigest.slice(0, 12)}${record.baselineAvailable === false ? ' · 无历史基线' : ''}`
  }))
)
const files = computed(
  () =>
    artifacts.value?.snapshot?.files.toSorted((left, right) =>
      left.path.localeCompare(right.path)
    ) ?? []
)
const htmlEntries = computed(() =>
  files.value.filter((file) => file.kind === 'prototype' && file.mime === 'text/html')
)
const downloads = new Set<string>()
let downloadController: AbortController | undefined
let generation = 0
watch(
  [selected, basis],
  () => {
    generation++
    loadedConfirmationId.value = ''
    diff.value = undefined
    artifacts.value = undefined
    selectedFile.value = undefined
    previewTarget.value = null
    notice.value = ''
  },
  { flush: 'sync' }
)
async function load() {
  open.value = !open.value
  if (!open.value) return
  basis.value = 'candidate'
  loading.value = true
  error.value = ''
  try {
    records.value = await getConfirmations(props.specId)
    selected.value = records.value.at(-1)?.id ?? ''
    if (selected.value) await compare()
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}
async function compare() {
  if (!selected.value) return
  const confirmationId = selected.value
  const own = ++generation
  loading.value = true
  error.value = ''
  selectedFile.value = undefined
  previewTarget.value = null
  try {
    const [changes, manifest] = await Promise.all([
      getConfirmationDiff(props.specId, confirmationId, undefined, basis.value),
      getConfirmationArtifacts(confirmationId)
    ])
    if (own !== generation) return
    loadedConfirmationId.value = confirmationId
    diff.value = changes
    artifacts.value = manifest
    selectedPrototype.value =
      htmlEntries.value.find((file) => /(^|\/)index\.html$/i.test(file.path))?.path ??
      htmlEntries.value[0]?.path ??
      ''
  } catch (cause) {
    if (own === generation) error.value = errorMessage(cause)
  } finally {
    if (own === generation) loading.value = false
  }
}
async function readFile(path: string) {
  if (!loadedConfirmationId.value) return
  const confirmationId = loadedConfirmationId.value
  const own = ++generation
  loading.value = true
  error.value = ''
  try {
    const result = await getConfirmationDiff(props.specId, confirmationId, path, basis.value)
    if (own === generation) selectedFile.value = result.files.find((file) => file.path === path)
  } catch (cause) {
    if (own === generation) error.value = errorMessage(cause)
  } finally {
    if (own === generation) loading.value = false
  }
}
async function captureSnapshot() {
  const confirmationId = loadedConfirmationId.value
  if (!confirmationId || capturing.value) return
  capturing.value = true
  error.value = ''
  notice.value = ''
  try {
    await captureConfirmationSnapshot(confirmationId)
    if (selected.value !== confirmationId) return
    const confirmations = await getConfirmations(props.specId)
    if (selected.value !== confirmationId) return
    records.value = confirmations
    await compare()
    if (selected.value === confirmationId) notice.value = '已补存此确认记录的产物快照。'
  } catch (cause) {
    if (selected.value === confirmationId) error.value = errorMessage(cause)
  } finally {
    capturing.value = false
  }
}
async function download(path: string, version: 'before' | 'after' = 'before') {
  if (!loadedConfirmationId.value || downloading.value) return
  const confirmationId = loadedConfirmationId.value
  const current = selectedFile.value?.path === path ? selectedFile.value.after : null
  if (version === 'after' && !current) return
  downloadController?.abort()
  const controller = new AbortController()
  downloadController = controller
  downloading.value = true
  error.value = ''
  notice.value = ''
  try {
    const blob =
      version === 'after' && current
        ? await currentSpecFile(props.specId, path, current.sha256, controller.signal, basis.value)
        : await historicalFile(confirmationId, path, controller.signal)
    if (controller.signal.aborted) return
    const url = URL.createObjectURL(blob)
    downloads.add(url)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = path.split('/').at(-1) ?? 'historical-file'
    document.body.append(anchor)
    anchor.click()
    anchor.remove()
    setTimeout(() => {
      URL.revokeObjectURL(url)
      downloads.delete(url)
    }, 1500)
    notice.value = `已下载${version === 'after' ? basisLabel.value : '确认版本'}中的 ${path}`
  } catch (cause) {
    if (!controller.signal.aborted) error.value = errorMessage(cause)
  } finally {
    if (downloadController === controller) downloading.value = false
  }
}
onBeforeUnmount(() => {
  generation++
  downloadController?.abort()
  for (const url of downloads) URL.revokeObjectURL(url)
})
</script>
<template>
  <div class="confirmation-diff">
    <Button variant="outline" type="button" :aria-expanded="open" @click="load">
      {{ open ? '收起版本比较' : '与确认版本比较' }}
    </Button>
    <section v-if="open" class="version-comparison">
      <div class="inline-form">
        <label class="grow" :for="id + '-baseline'">
          对比基线
          <SingleSelect
            :id="id + '-baseline'"
            v-model="selected"
            :options="recordOptions"
            :disabled="loading || capturing"
            name="confirmationId"
            placeholder="选择确认版本"
          />
        </label>
        <label class="grow" :for="id + '-basis'">
          对比对象
          <SingleSelect
            :id="id + '-basis'"
            :model-value="basis"
            :options="[
              { value: 'candidate', label: '本轮确认材料' },
              { value: 'latest', label: '当前来源' }
            ]"
            :disabled="loading || capturing || downloading"
            name="comparisonBasis"
            @update:model-value="basis = $event as ComparisonBasis"
          />
        </label>
        <Button variant="outline" :disabled="loading || !selected" @click="compare">比较</Button>
      </div>
      <p v-if="error" role="alert" class="notice error">{{ error }}</p>
      <p v-if="notice" role="status" class="notice">{{ notice }}</p>
      <p v-if="!records.length && !loading" class="muted">尚无可比较的确认版本</p>
      <p v-if="diff?.message" class="notice">{{ diff.message }}</p>
      <div v-if="diff?.baselineAvailable === false" class="snapshot-backfill">
        <p class="muted">当前全部产物与原确认摘要一致时，可补存用于后续比较的快照。</p>
        <Button
          type="button"
          variant="outline"
          :disabled="capturing || loading"
          @click="captureSnapshot"
        >
          {{ capturing ? '正在核对并补存…' : '从当前内容补存快照' }}
        </Button>
      </div>
      <p v-if="artifacts?.snapshot" class="muted snapshot-captured">
        快照保存于 {{ dateLabel(artifacts.snapshot.capturedAt) }}
      </p>
      <section v-if="htmlEntries.length" class="historical-entry">
        <h3>确认版本的原型</h3>
        <div class="inline-form">
          <label class="grow" :for="id + '-prototype'">
            HTML 入口
            <SingleSelect
              :id="id + '-prototype'"
              v-model="selectedPrototype"
              :options="htmlEntries.map((file) => ({ value: file.path, label: file.path }))"
              name="prototypeEntry"
            />
          </label>
          <Button
            type="button"
            :disabled="!selectedPrototype || loading"
            @click="previewTarget = selectedPrototype"
          >
            预览历史原型
          </Button>
          <Button
            type="button"
            variant="outline"
            :disabled="!selectedPrototype || downloading"
            @click="download(selectedPrototype)"
          >
            下载历史 HTML
          </Button>
        </div>
      </section>
      <ul class="changed-file-list">
        <li v-for="file in diff?.files ?? []" :key="file.path">
          <Button type="button" variant="ghost" :disabled="loading" @click="readFile(file.path)">
            {{ labels[file.status] }} · {{ file.path }}
          </Button>
          <span class="muted">{{ file.before?.size ?? 0 }} → {{ file.after?.size ?? 0 }} 字节</span>
        </li>
      </ul>
      <section v-if="selectedFile" class="diff-file">
        <div class="section-heading">
          <h3>{{ selectedFile.path }}</h3>
          <div class="inline-actions">
            <Button
              v-if="selectedFile.before"
              type="button"
              variant="outline"
              :disabled="downloading"
              @click="download(selectedFile.path)"
            >
              下载确认版本
            </Button>
            <Button
              v-if="selectedFile.after"
              type="button"
              variant="outline"
              :disabled="downloading"
              @click="download(selectedFile.path, 'after')"
            >
              下载{{ basisLabel }}
            </Button>
          </div>
        </div>
        <AssetImageDiff
          v-if="imageAsset(selectedFile)"
          :key="`${loadedConfirmationId}:${selectedFile.path}`"
          :spec-id="specId"
          :confirmation-id="loadedConfirmationId"
        :basis="basis"
          :file="selectedFile"
        />
        <p v-if="selectedFile.binary && !imageAsset(selectedFile)" class="muted">
          二进制资源，可核对文件大小与摘要。
        </p>
        <p v-if="selectedFile.binary || imageAsset(selectedFile)" class="path-text">
          {{ selectedFile.before?.sha256 ?? '不存在' }} →
          {{ selectedFile.after?.sha256 ?? '不存在' }}
        </p>
        <pre
          v-else-if="selectedFile.lines?.length"
          class="line-diff"
        ><span v-for="(line,index) in selectedFile.lines" :key="index" :data-kind="line.kind">{{line.kind==='add'?'+':line.kind==='remove'?'-':' '}} {{line.text}}
</span></pre>
        <div v-else class="three-way">
          <section>
            <h4>确认版本</h4>
            <pre>{{ selectedFile.beforeText ?? '文件不存在' }}</pre>
          </section>
          <section>
            <h4>当前版本</h4>
            <pre>{{ selectedFile.afterText ?? '文件不存在' }}</pre>
          </section>
        </div>
      </section>
      <p v-if="diff?.baselineAvailable && !diff.files.length" class="muted">
        当前版本与所选确认版本一致
      </p>
      <details v-if="files.length" class="historical-files">
        <summary>下载确认版本的文件（{{ files.length }}）</summary>
        <ul class="changed-file-list">
          <li v-for="file in files" :key="file.path">
            <span class="path-text">{{ file.path }} · {{ file.size }} 字节</span>
            <Button
              type="button"
              variant="ghost"
              :disabled="downloading"
              @click="download(file.path)"
            >
              下载历史文件
            </Button>
          </li>
        </ul>
      </details>
      <HistoricalPreview v-model="previewTarget" :confirmation-id="loadedConfirmationId" />
    </section>
  </div>
</template>
<style scoped>
.historical-entry {
  margin: 20px 0;
  padding-top: 16px;
  border-top: 1px solid var(--yss-colors-border);
}
.version-comparison .grow {
  min-width: 0;
}
.version-comparison :deep([data-slot='select-trigger']) {
  width: 100%;
  min-width: 0;
}
.historical-entry .inline-form {
  flex-wrap: wrap;
}
.historical-entry label {
  min-width: 200px;
}
.historical-files {
  margin: 16px 0;
}
.historical-files > summary {
  cursor: pointer;
  font-size: 12px;
  color: #315dcf;
  padding: 8px 0;
}
.snapshot-backfill {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin: 16px 0;
}
.snapshot-captured {
  margin: 12px 0;
  font-size: 12px;
}
</style>
