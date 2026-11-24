<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Card } from '@/components/ui/card'
import { Alert } from '@/components/ui/alert'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { shallowRef, ref, computed, onBeforeUnmount, onMounted, useId } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Checkbox } from '@/components/ui/checkbox'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { Field, FieldLabel } from '@/components/ui/field'
import { discoverSpecs, errorMessage, type DiscoverResult } from '@/services/workbench'
import { useWorkbench } from '@/hooks/useWorkbench'
const props = defineProps<{ projectId: string; directories: string[] }>()
const { submit, saving, snapshot, refresh } = useWorkbench()
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
const unregistered = computed(
  () => result.value?.candidates.filter((c) => !c.alreadyRegistered).map((c) => c.sourcePath) ?? []
)
let controller: AbortController | undefined
onBeforeUnmount(() => controller?.abort())
onMounted(() => void scan())
async function scan(target = directory.value) {
  if (busy.value && !scanning.value) return
  controller?.abort()
  controller = new AbortController()
  const current = controller
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
      if (selected.value.length) {
        const available = new Set(unregistered.value)
        selected.value = selected.value.filter((path) => available.has(path))
      }
    }
  } catch (cause) {
    if (!current.signal.aborted && controller === current) scanError.value = errorMessage(cause)
  } finally {
    if (controller === current) action.value = undefined
  }
}
function toggleCandidate(path: string, checked: boolean | 'indeterminate') {
  if (busy.value || !resultsCurrent.value || !unregistered.value.includes(path)) return
  selected.value = checked === true
    ? [...new Set([...selected.value, path])]
    : selected.value.filter((item) => item !== path)
}
async function add(paths: string[], kind: 'batch' | 'manual') {
  if (busy.value || saving.value || (kind === 'batch' && !resultsCurrent.value)) return
  const projectId = props.projectId
  action.value = kind
  errors.value = []
  let count = 0
  try {
    await refresh()
    for (const path of paths) {
      outcomes.value[path] = { status: 'pending', message: '正在关联…' }
      try {
        const existing = snapshot.value?.specs.some(
          (spec) => spec.record.projectId === projectId && spec.record.sourcePath === path
        )
        if (!existing)
          await submit({ kind: 'addSpec', projectId, sourcePath: path })
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
    const summary = `已关联 ${count} 个 Spec`
    message.value = !errors.value.length
      ? `${summary}。`
      : kind === 'batch'
        ? `${summary}，失败项保留选中，可核对后继续关联。`
        : `${summary}，请核对路径后重试。`
    if (paths.length === 1 && count === 1 && paths[0] === manual.value) manual.value = ''
  } catch (cause) {
    errors.value.push(errorMessage(cause))
  } finally {
    action.value = undefined
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
            :disabled="busy || saving || !resultsCurrent || !unregistered.length"
            @click="selected = [...unregistered]"
          >
            选择当前未关联项（{{ unregistered.length }}）
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
                    :model-value="selected.includes(candidate.sourcePath)"
                    :disabled="candidate.alreadyRegistered || busy || saving || !resultsCurrent"
                    :aria-label="`选择 ${candidate.title}`"
                    @update:model-value="toggleCandidate(candidate.sourcePath, $event)"
                  />
                </TableCell>
                <TableHead scope="row">
                  {{ candidate.title
                  }}<span class="row-sub path-text">{{ candidate.sourcePath }}</span>
                </TableHead>
                <TableCell>
                  <Badge v-if="candidate.alreadyRegistered" variant="secondary">已关联（含停用）</Badge>
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
          :disabled="!selected.length || busy || saving || !resultsCurrent"
          @click="add([...selected], 'batch')"
        >
          <LoadingIndicator :active="action === 'batch'" />
          {{ action === 'batch' ? '关联中…' : `关联选中项（${selected.length}）` }}
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
              <TableCell :class="outcome.status === 'failed' ? 'warning-text' : 'muted'">
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
