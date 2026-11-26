<script lang="ts">
import { shallowReactive } from 'vue'

// Index the workbench session's pending requests so a remounted discovery panel can recover them by file.
const associationRequestIds = shallowReactive(new Map<string, string>())
</script>

<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Card } from '@/components/ui/card'
import { Alert } from '@/components/ui/alert'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { shallowRef, ref, computed, onBeforeUnmount, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { Field, FieldLabel } from '@/components/ui/field'
import { ApiError, discoverSpecs, errorMessage, pendingRequests, type DiscoverResult } from '@/services/workbench'
import { useWorkbench } from '@/hooks/useWorkbench'
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
const props = defineProps<{
  projectId: string
  rootPath: string
  directories: string[]
  visible: boolean
  reloadProject: (minimumRevision?: number) => Promise<boolean>
}>()
const { snapshot, submit, saving } = useWorkbench()
const { catalog, catalogError, refreshCatalog } = useWorkbenchCatalog()
const id = useId()
const directory = shallowRef('')
const manual = shallowRef('')
const result = shallowRef<DiscoverResult>()
const resultDirectory = shallowRef<string>()
const requestedDirectory = shallowRef('')
const scanError = shallowRef('')
const resultsCurrent = computed(() => resultDirectory.value === requestedDirectory.value)
const selected = ref<string[]>([])
const outcomes = ref<Record<string, { status: string; message: string }>>({})
const action = shallowRef<'scan' | 'batch' | 'manual'>()
const busy = computed(() => !!action.value)
const scanning = computed(() => action.value === 'scan')
const message = shallowRef('')
const errors = ref<string[]>([])
const pendingIds = computed(() => new Set(pendingRequests.value.map(({ requestId }) => requestId)))
const associationPaths = computed(() => result.value?.candidates
  .filter((candidate) => !candidate.alreadyRegistered || awaitingReceipt(candidate.sourcePath))
  .map((candidate) => candidate.sourcePath) ?? [])
const selectablePaths = computed(() => new Set(associationPaths.value))
const selectedPaths = computed(() => selected.value.filter((path) => selectablePaths.value.has(path)))
const hasAwaitingReceipt = computed(() => associationPaths.value.some(awaitingReceipt))
let controller: AbortController | undefined
let resumeScan = true
let disposed = false
function associationKey(path: string) {
  return JSON.stringify([props.projectId, path])
}
function awaitingReceipt(path: string) {
  const requestId = associationRequestIds.get(associationKey(path))
  return requestId !== undefined && pendingIds.value.has(requestId)
}
onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
})
watch(() => props.visible, (visible) => {
  if (visible && resumeScan) void scan(requestedDirectory.value)
  if (!visible && scanning.value) {
    resumeScan = true
    controller?.abort()
    controller = undefined
    action.value = undefined
  }
}, { immediate: true })
async function scan(target = directory.value) {
  if (!props.visible || (busy.value && !scanning.value)) return
  controller?.abort()
  controller = new AbortController()
  const current = controller
  resumeScan = false
  action.value = 'scan'
  requestedDirectory.value = target
  scanError.value = ''
  errors.value = []
  message.value = ''
  try {
    const next = await discoverSpecs(
      { projectId: props.projectId, directory: target || undefined },
      current.signal
    )
    if (!current.signal.aborted && controller === current) {
      if (resultDirectory.value !== target) selected.value = []
      resultDirectory.value = target
      result.value = next
      if (selected.value.length) selected.value = selectedPaths.value
    }
  } catch (cause) {
    if (!current.signal.aborted && controller === current) scanError.value = errorMessage(cause)
  } finally {
    if (controller === current) {
      controller = undefined
      action.value = undefined
    }
  }
}
function toggleCandidate(path: string, checked: boolean | 'indeterminate') {
  if (!props.visible || busy.value || !resultsCurrent.value || !selectablePaths.value.has(path)) return
  selected.value = checked === true
    ? [...new Set([...selected.value, path])]
    : selected.value.filter((item) => item !== path)
}
async function add(paths: string[], kind: 'batch' | 'manual') {
  if (!props.visible || busy.value || saving.value || (kind === 'batch' && !resultsCurrent.value)) return
  const projectId = props.projectId
  action.value = kind
  errors.value = []
  message.value = ''
  let count = 0
  let latestReceipt: number | undefined
  let needsRecheck = false
  try {
    const [projectReady, catalogReady] = await Promise.all([props.reloadProject(), refreshCatalog()])
    if (disposed) return
    if (!projectReady) throw new Error('无法核对当前工程，请重试。')
    if (!catalogReady || !catalog.value) throw new Error(catalogError.value || '无法读取已登记的 Spec。')
    const revisionAfterRead = snapshot.value?.revision
    if (revisionAfterRead !== undefined && catalog.value.revision < revisionAfterRead) {
      const updated = await refreshCatalog(revisionAfterRead)
      if (disposed) return
      if (!updated) throw new Error(catalogError.value || '无法核对最新的登记记录。')
    }
    const currentProject = catalog.value.projects.find((entry) => entry.id === projectId)
    if (!currentProject?.active || currentProject.rootPath !== props.rootPath)
      throw new Error('工程登记信息已变化，请核对后重新关联。')
    let revision = catalog.value.revision
    const registeredPaths = new Set(
      catalog.value.specs.filter((spec) => spec.projectId === projectId).map((spec) => spec.sourcePath)
    )
    for (const path of paths) {
      if (disposed) return
      const key = associationKey(path)
      const recovering = awaitingReceipt(path)
      outcomes.value[path] = { status: 'pending', message: '正在关联…' }
      try {
        const existing = registeredPaths.has(path)
        if (!existing || recovering) {
          const receipt = await submit(
            { kind: 'addSpec', projectId, sourcePath: path },
            revision,
            undefined,
            // Reconcile lightweight reads once after the batch. Each receipt advances the version.
            async () => {}
          )
          associationRequestIds.delete(key)
          if (disposed) return
          if (receipt.revision >= revision) registeredPaths.add(path)
          revision = Math.max(revision, receipt.revision)
          latestReceipt = revision
        }
        count++
        outcomes.value[path] = {
          status: 'success',
          message: recovering ? '已确认原关联结果' : existing ? '已有关联，已保留原记录' : '关联成功'
        }
        selected.value = selected.value.filter((item) => item !== path)
        if (result.value && registeredPaths.has(path))
          result.value = {
            ...result.value,
            candidates: result.value.candidates.map((candidate) =>
              candidate.sourcePath === path ? { ...candidate, alreadyRegistered: true } : candidate
            )
          }
      } catch (cause) {
        const uncertain = cause instanceof ApiError && cause.context.uncertain === true
        const requestId = cause instanceof ApiError ? cause.context.requestId : undefined
        if (uncertain && typeof requestId === 'string') {
          for (const [pathKey, id] of associationRequestIds) {
            if (!pendingIds.value.has(id)) associationRequestIds.delete(pathKey)
          }
          associationRequestIds.set(key, requestId)
        } else if (!awaitingReceipt(path)) {
          associationRequestIds.delete(key)
        }
        if (disposed) return
        const detail = errorMessage(cause)
        outcomes.value[path] = { status: uncertain ? 'uncertain' : 'failed', message: detail }
        errors.value.push(`${path}：${detail}`)
        if (uncertain || (cause instanceof ApiError && cause.status === 409 && 'revision' in cause.context)) {
          needsRecheck = true
          break
        }
      }
    }
    const summary = `已关联 ${count} 个 Spec`
    message.value = !errors.value.length
      ? `${summary}。`
      : kind === 'batch'
        ? `${summary}，未确认关联的文件保留选中，可核对后继续关联。`
        : `${summary}，请核对路径后重试。`
    if (paths.length === 1 && count === 1 && paths[0] === manual.value) manual.value = ''
  } catch (cause) {
    if (!disposed) errors.value.push(errorMessage(cause))
  } finally {
    if (!disposed && (latestReceipt !== undefined || needsRecheck)) {
      await Promise.all([props.reloadProject(latestReceipt), refreshCatalog(latestReceipt)])
    }
    if (!disposed) action.value = undefined
  }
}
</script>
<template>
  <Card class="block gap-0 py-0 shadow-none panel discover-panel">
    <h2>关联 Spec</h2>
    <p class="muted">工作台启动和登记项目时自动纳管可识别的主规格。其他 Markdown 可按目录查找后手动关联。</p>
    <form class="inline-form" aria-label="发现工程 Spec 文件" @submit.prevent="scan()">
      <Field class="grow gap-1.5">
        <FieldLabel :for="`${id}-directory`">相对目录</FieldLabel>
        <Input
          :id="`${id}-directory`"
          v-model="directory"
          name="directory"
          :list="`${id}-directories`"
          placeholder="留空使用常用规格目录"
        />
      </Field>
      <datalist :id="`${id}-directories`">
        <option v-for="dir in directories" :key="dir" :value="dir" />
        <option value="." />
      </datalist>
      <Button type="submit" variant="outline" :disabled="busy || saving">
        {{ scanning ? '发现中…' : '发现文件' }}
      </Button>
    </form>
    <Alert v-if="scanError" class="notice error" variant="destructive">
      {{ scanError }}
      <Button type="button" variant="outline" :disabled="busy || saving" @click="scan(requestedDirectory)">
        重试发现文件
      </Button>
    </Alert>
    <p v-if="result && !resultsCurrent && !scanning" class="muted result-warning">
      下方显示上次发现结果，请重试当前目录后再关联候选。
    </p>
    <LoadingSkeleton v-if="scanning && !result" variant="table" :rows="3" label="正在发现 Spec 文件" />
    <div v-if="result" class="relative">
      <Alert class="notice" role="status" :class="{ warning: !result.complete }">
        <div>
        {{
          result.complete
            ? `发现 ${result.candidates.length} 个候选文件`
            : '发现结果不完整，请查看提示或缩小目录范围。'
        }}
        <ul v-if="result.warnings.length">
          <li v-for="warning in result.warnings" :key="warning">{{ warning }}</li>
        </ul>
        </div>
      </Alert>
      <template v-if="result.candidates.length">
        <div class="toolbar">
          <Button
            variant="outline"
            :disabled="busy || saving || !resultsCurrent || !associationPaths.length"
            @click="selected = [...associationPaths]"
          >
            {{ hasAwaitingReceipt ? '选择未关联与待核对项' : '选择当前未关联项' }}（{{ associationPaths.length }}）
          </Button>
          <Button variant="ghost" :disabled="busy || saving || !resultsCurrent || !selected.length" @click="selected = []">
            清空选择
          </Button>
          <span class="muted">仅选择当前发现结果，不代表整个项目。</span>
        </div>
        <div
          class="candidate-list table-scroll"
          :aria-busy="scanning"
          tabindex="0"
          role="region"
          aria-label="发现的 Spec 文件"
        >
          <Table class="spec-table candidate-table">
            <TableCaption class="sr-only">
              发现候选与关联状态
            </TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">选择</TableHead>
                <TableHead scope="col">Spec 文件</TableHead>
                <TableHead scope="col">关联状态</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="candidate in result.candidates" :key="candidate.sourcePath">
                <TableCell>
                  <Checkbox
                    :model-value="selectedPaths.includes(candidate.sourcePath)"
                    :disabled="!selectablePaths.has(candidate.sourcePath) || busy || saving || !resultsCurrent"
                    :aria-label="`选择 ${candidate.title}`"
                    @update:model-value="toggleCandidate(candidate.sourcePath, $event)"
                  />
                </TableCell>
                <TableHead scope="row">
                  {{ candidate.title
                  }}<span class="row-sub path-text">{{ candidate.sourcePath }}</span>
                </TableHead>
                <TableCell>
                  <Badge v-if="awaitingReceipt(candidate.sourcePath)" variant="outline">关联结果待核对</Badge>
                  <Badge v-else-if="candidate.alreadyRegistered" variant="secondary">已关联（含停用）</Badge>
                  <span
                    v-else-if="outcomes[candidate.sourcePath]"
                    :class="outcomes[candidate.sourcePath]?.status === 'failed' ? 'warning-text' : 'muted'"
                  >
                    {{ outcomes[candidate.sourcePath]?.message }}
                  </span>
                  <Badge v-else variant="outline">待关联</Badge>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
        <Button
          type="button"
          :disabled="!selectedPaths.length || busy || saving || !resultsCurrent"
          @click="add([...selectedPaths], 'batch')"
        >
          <LoadingIndicator :active="action === 'batch'" />
          {{ action === 'batch' ? '关联中…' : `关联选中项（${selectedPaths.length}）` }}
        </Button>
      </template>
      <Empty v-else class="border-0 py-5">
        <EmptyDescription>当前目录内没有可显示的候选，可调整目录或手工关联文件。</EmptyDescription>
      </Empty>
      <LoadingIndicator
        :active="scanning"
        placement="overlay"
        :label="resultsCurrent ? '正在重新发现 Spec 文件' : '正在发现文件，暂显示上次结果'"
      />
    </div>
    <Collapsible class="manual-disclosure" :unmount-on-hide="false">
      <CollapsibleTrigger>手工关联文件</CollapsibleTrigger>
      <CollapsibleContent>
        <form class="inline-form" aria-label="手工关联 Spec 文件" @submit.prevent="add([manual], 'manual')">
          <Field class="grow gap-1.5">
            <FieldLabel :for="`${id}-manual`">主规格相对路径</FieldLabel>
            <Input :id="`${id}-manual`" v-model="manual" name="sourcePath" required placeholder="docs/specs/my-spec.md" />
          </Field>
          <Button type="submit" variant="outline" :disabled="busy || saving">
            <LoadingIndicator :active="action === 'manual'" />
            {{ action === 'manual' ? '关联中…' : '关联' }}
          </Button>
        </form>
      </CollapsibleContent>
    </Collapsible>
    <div v-if="Object.keys(outcomes).length" class="association-results">
      <h3>逐项关联结果</h3>
      <div class="table-scroll" tabindex="0" role="region" aria-label="逐项关联结果">
        <Table class="spec-table candidate-table">
          <TableHeader>
            <TableRow>
              <TableHead scope="col">文件</TableHead>
              <TableHead scope="col">结果</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="(outcome, path) in outcomes" :key="path">
              <TableHead scope="row" class="path-text">{{ path }}</TableHead>
              <TableCell :class="outcome.status === 'failed' || outcome.status === 'uncertain' ? 'warning-text' : 'muted'">
                {{ outcome.message }}
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>
    <Alert v-if="message" role="status" class="notice">{{ message }}</Alert>
    <Alert v-for="error in errors" :key="error" role="alert" class="notice error" variant="destructive">{{ error }}</Alert>
  </Card>
</template>

<style scoped>
.candidate-table {
  min-width: 560px;
}
.candidate-table :deep(th[scope='row']) {
  text-align: left;
  font-weight: 500;
  white-space: normal;
  overflow-wrap: anywhere;
}
.candidate-table :deep(td) {
  white-space: normal;
}
.candidate-table :deep(td:first-child) {
  width: 60px;
}
.candidate-table :deep(thead) {
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
.result-warning {
  margin-bottom: 8px;
}
.manual-disclosure {
  margin-top: 20px;
  border-top: 1px solid var(--yss-colors-border);
}
@media (width < 540px) {
  .discover-panel {
    padding: 14px;
  }
  .inline-form {
    flex-wrap: wrap;
  }
  .inline-form > .grow {
    flex-basis: 100%;
  }
}
</style>
