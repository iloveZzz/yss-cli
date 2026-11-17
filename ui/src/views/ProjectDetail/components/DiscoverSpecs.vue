<script setup lang="ts">
import { shallowRef, ref, computed, onBeforeUnmount } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { discoverSpecs, errorMessage, type DiscoverResult } from '@/services/workbench'
import { useWorkbench } from '@/hooks/useWorkbench'
const props = defineProps<{ projectId: string; directories: string[] }>()
const { submit, saving, snapshot, refresh } = useWorkbench()
const directory = shallowRef(props.directories[0] ?? '.')
const manual = shallowRef('')
const result = shallowRef<DiscoverResult>()
const selected = ref<string[]>([])
const outcomes = ref<Record<string, { status: string; message: string }>>({})
const busy = shallowRef(false)
const message = shallowRef('')
const errors = ref<string[]>([])
const unregistered = computed(
  () => result.value?.candidates.filter((c) => !c.alreadyRegistered).map((c) => c.sourcePath) ?? []
)
let controller: AbortController | undefined
onBeforeUnmount(() => controller?.abort())
async function scan() {
  controller?.abort()
  controller = new AbortController()
  const current = controller
  busy.value = true
  errors.value = []
  message.value = ''
  selected.value = []
  result.value = undefined
  try {
    const next = await discoverSpecs(
      { projectId: props.projectId, directory: directory.value },
      current.signal
    )
    if (!current.signal.aborted) result.value = next
  } catch (e) {
    if (!current.signal.aborted) errors.value = [errorMessage(e)]
  } finally {
    if (controller === current) busy.value = false
  }
}
async function add(paths: string[]) {
  if (busy.value || saving.value) return
  busy.value = true
  errors.value = []
  let count = 0
  try {
    await refresh()
    for (const path of paths) {
      outcomes.value[path] = { status: 'pending', message: '正在关联…' }
      try {
        const existing = snapshot.value?.specs.some(
          (spec) => spec.record.projectId === props.projectId && spec.record.sourcePath === path
        )
        if (!existing)
          await submit({ kind: 'addSpec', projectId: props.projectId, sourcePath: path })
        count++
        outcomes.value[path] = {
          status: 'success',
          message: existing ? '已有关联，已保留原记录' : '关联成功'
        }
        selected.value = selected.value.filter((item) => item !== path)
        if (result.value)
          result.value = {
            ...result.value,
            candidates: result.value.candidates.map((candidate) =>
              candidate.sourcePath === path ? { ...candidate, alreadyRegistered: true } : candidate
            )
          }
      } catch (cause) {
        const detail = errorMessage(cause)
        outcomes.value[path] = { status: 'failed', message: detail }
        errors.value.push(`${path}：${detail}`)
      }
    }
    message.value = `已关联 ${count} 个 Spec${errors.value.length ? '，失败项保留选中，可核对后继续关联。' : '。'}`
    if (paths.length === 1 && count === 1 && paths[0] === manual.value) manual.value = ''
  } catch (cause) {
    errors.value.push(errorMessage(cause))
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <section class="panel discover-panel">
    <h2>关联 Spec</h2>
    <p class="muted">选择项目内目录查找 Markdown，勾选后才会关联。</p>
    <form class="inline-form" aria-label="发现工程 Spec 文件" @submit.prevent="scan">
      <label class="grow">
        相对目录
        <Input
          v-model="directory"
          name="directory"
          list="spec-directories"
          required
          placeholder="例如 docs/specs"
        />
      </label>
      <datalist id="spec-directories">
        <option v-for="dir in directories" :key="dir" :value="dir" />
        <option value="." />
      </datalist>
      <Button variant="outline" :disabled="busy || saving">
        {{ busy ? '处理中…' : '发现文件' }}
      </Button>
    </form>
    <template v-if="result">
      <div class="notice" :class="{ warning: !result.complete }">
        {{
          result.complete
            ? `发现 ${result.candidates.length} 个候选文件`
            : '发现结果不完整，请查看提示或缩小目录范围。'
        }}
        <ul v-if="result.warnings.length">
          <li v-for="warning in result.warnings" :key="warning">{{ warning }}</li>
        </ul>
      </div>
      <div class="toolbar">
        <Button
          variant="outline"
          :disabled="busy || !unregistered.length"
          @click="selected = [...unregistered]"
        >
          选择当前未关联项（{{ unregistered.length }}）
        </Button>
        <Button variant="ghost" :disabled="busy || !selected.length" @click="selected = []">
          清空选择
        </Button>
        <span class="muted">仅选择当前发现结果，不代表整个项目。</span>
      </div>
      <div
        class="candidate-list table-scroll"
        tabindex="0"
        role="region"
        aria-label="发现的 Spec 文件"
      >
        <table class="spec-table candidate-table">
          <caption class="sr-only">
            发现候选与关联状态
          </caption>
          <thead>
            <tr>
              <th scope="col">选择</th>
              <th scope="col">Spec 文件</th>
              <th scope="col">关联状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="candidate in result.candidates" :key="candidate.sourcePath">
              <td>
                <input
                  v-model="selected"
                  type="checkbox"
                  :value="candidate.sourcePath"
                  :disabled="candidate.alreadyRegistered || busy"
                  :aria-label="`选择 ${candidate.title}`"
                />
              </td>
              <th scope="row">
                {{ candidate.title
                }}<span class="row-sub path-text">{{ candidate.sourcePath }}</span>
              </th>
              <td>
                <span v-if="candidate.alreadyRegistered" class="muted">已关联（含停用）</span
                ><span
                  v-else-if="outcomes[candidate.sourcePath]"
                  :class="
                    outcomes[candidate.sourcePath]?.status === 'failed' ? 'warning-text' : 'muted'
                  "
                  >{{ outcomes[candidate.sourcePath]?.message }}</span
                ><span v-else class="muted">待关联</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Button :disabled="!selected.length || busy || saving" @click="add([...selected])">
        关联选中项（{{ selected.length }}）
      </Button>
    </template>
    <details>
      <summary>手工关联文件</summary>
      <form class="inline-form" aria-label="手工关联 Spec 文件" @submit.prevent="add([manual])">
        <label class="grow">
          主规格相对路径
          <Input v-model="manual" name="sourcePath" required placeholder="docs/specs/my-spec.md" />
        </label>
        <Button variant="outline" :disabled="busy || saving">关联</Button>
      </form>
    </details>
    <div v-if="Object.keys(outcomes).length" class="association-results">
      <h3>逐项关联结果</h3>
      <div class="table-scroll" tabindex="0" role="region" aria-label="逐项关联结果">
        <table class="spec-table candidate-table">
          <thead>
            <tr>
              <th scope="col">文件</th>
              <th scope="col">结果</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(outcome, path) in outcomes" :key="path">
              <th scope="row" class="path-text">{{ path }}</th>
              <td :class="outcome.status === 'failed' ? 'warning-text' : 'muted'">
                {{ outcome.message }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <p v-if="message" role="status" class="notice">{{ message }}</p>
    <p v-for="error in errors" :key="error" role="alert" class="notice error">{{ error }}</p>
  </section>
</template>

<style scoped>
.candidate-table {
  min-width: 560px;
}
.candidate-table th[scope='row'] {
  text-align: left;
  font-weight: 500;
  white-space: normal;
  overflow-wrap: anywhere;
}
.candidate-table td {
  white-space: normal;
}
.candidate-table td:first-child {
  width: 60px;
}
.candidate-table thead {
  position: sticky;
  top: 0;
  z-index: 1;
}
.association-results {
  margin-top: 18px;
}
.association-results h3 {
  margin-bottom: 10px;
}
</style>
