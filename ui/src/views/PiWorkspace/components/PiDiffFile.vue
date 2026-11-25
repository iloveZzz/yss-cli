<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import type { AgentFile, AgentDiff, ReviewExcerpt } from '@/services/workbench-agent'

const props = defineProps<{
  file: AgentFile
  diff: AgentDiff
  editable: boolean
  selected: boolean
  disabled: boolean
}>()
const emit = defineEmits<{ select: [selected: boolean]; cite: [excerpt: ReviewExcerpt] }>()
const side = shallowRef('result')
const conflictConfirmed = shallowRef(props.selected && props.file.conflict)
const selecting = shallowRef(false)
const start = shallowRef<string | number>(1)
const end = shallowRef<string | number>(1)
const note = shallowRef('')
const sides = computed(() => {
  const file = props.file
  return [
    { id: 'initial', label: '运行起点', desc: file.initial, text: file.initialText },
    { id: 'result', label: '隔离结果', desc: file.result, text: file.resultText },
    ...(!props.diff.historical ? [{ id: 'current', label: '原工程当前', desc: file.current, text: file.currentText }] : []),
    ...(file.reviewedBeforeKnown ? [{ id: 'before', label: '接收前原工程', desc: file.reviewedBeforeDescriptor, text: file.reviewedBefore }] : [])
  ]
})
const activeSide = computed(() => sides.value.find((item) => item.id === side.value) ?? sides.value[1]!)
const sourceLines = computed(() => {
  const text = activeSide.value.text
  if (!text) return []
  return text.replace(/\r?\n$/, '').split(/\r?\n/)
})
const range = computed(() => {
  const from = Number(start.value)
  const to = Number(end.value)
  const valid = Number.isInteger(from) && Number.isInteger(to) && from >= 1 &&
    from <= to && to <= sourceLines.value.length && to - from < 200
  return valid ? { from, to, snippet: sourceLines.value.slice(from - 1, to).join('\n') } : undefined
})
const mayCite = computed(() => props.editable && !props.disabled && !props.file.binary &&
  ['initial', 'result'].includes(side.value) && sourceLines.value.length > 0)
const encoder = new TextEncoder()
const citationIssue = computed(() => {
  if (range.value && encoder.encode(range.value.snippet).length > 24 * 1024) return '所选文本超过 24 KiB，请缩小行范围。'
  if (encoder.encode(note.value.trim()).length > 8 * 1024) return '调整意见须在 8 KiB 以内。'
  return ''
})
function cite() {
  const selection = range.value
  if (!mayCite.value || !selection || !note.value.trim() || citationIssue.value) return
  emit('cite', {
    projectId: props.file.projectId, path: props.file.path, reviewToken: props.diff.reviewToken,
    side: side.value === 'initial' ? 'initial' : 'result', startLine: selection.from, endLine: selection.to,
    note: note.value.trim(), snippet: selection.snippet, jobId: props.diff.jobId
  })
}
function useSelection(event: MouseEvent) {
  const code = event.currentTarget
  const selection = window.getSelection()
  if (!mayCite.value || !(code instanceof HTMLElement) || !selection?.rangeCount || selection.isCollapsed) return
  const selectedRange = selection.getRangeAt(0)
  if (!code.contains(selectedRange.startContainer) || !code.contains(selectedRange.endContainer)) return
  const before = document.createRange()
  before.selectNodeContents(code)
  before.setEnd(selectedRange.startContainer, selectedRange.startOffset)
  const prefix = before.toString()
  const from = prefix.split('\n').length
  const text = selectedRange.toString().replace(/\r?\n$/, '')
  start.value = from
  end.value = from + text.split('\n').length - 1
  selecting.value = true
}

const operation = computed(() => {
  if (props.file.accepted) return '已接收'
  if (props.file.contentMatches) return '内容一致 · 无需写入'
  if (!props.file.baseline && props.file.result) return '新增'
  if (!props.file.result) return '删除'
  return '修改'
})
</script>

<template>
  <div class="pi-file-body">
    <p class="pi-file-basis">
      {{ operation }} · 比较基准 {{ file.baseline?.sha256.slice(0, 10) ?? '文件不存在' }}
      <template v-if="file.acceptedReviewToken"> · 已核对版本 {{ file.acceptedReviewToken.slice(0, 10) }}</template>
    </p>
    <Tabs :model-value="side" @update:model-value="side = String($event); selecting = false">
      <TabsList aria-label="文件版本">
        <TabsTrigger v-for="item in sides" :key="item.id" :value="item.id">{{ item.label }}</TabsTrigger>
      </TabsList>
      <TabsContent v-for="item in sides" :key="item.id" :value="item.id" :tabindex="0">
        <pre v-if="item.text != null && !file.binary" class="pi-file-code" :aria-label="item.label + '内容'"
          @mouseup="useSelection">{{ item.text }}</pre>
        <p v-else-if="item.desc" class="pi-file-basis">
          {{ file.binary ? '二进制文件' : '文本超出预览范围' }} · {{ item.desc.size }} 字节 · {{ item.desc.sha256 }}
        </p>
        <p v-else class="pi-file-basis">该版本中没有此文件。</p>
      </TabsContent>
    </Tabs>
    <template v-if="mayCite">
      <Button v-if="!selecting" type="button" size="sm" variant="outline" @click="selecting = true">选行返工</Button>
      <form v-else class="pi-cite-form" @submit.prevent="cite">
        <div class="pi-cite-range">
          <label>起始行<Input v-model="start" type="number" min="1" :max="sourceLines.length" required /></label>
          <label>结束行<Input v-model="end" type="number" min="1" :max="sourceLines.length" required /></label>
        </div>
        <p class="pi-file-basis">{{ range ? '已选 ' + range.from + '–' + range.to + ' 行' : '请选 200 行以内的连续文本。' }}</p>
        <pre v-if="range" class="pi-file-code pi-file-excerpt">{{ range.snippet }}</pre>
        <label>调整意见<Textarea v-model="note" rows="2" maxlength="8000" required /></label>
        <p v-if="citationIssue" class="pi-issue" role="status">{{ citationIssue }}</p>
        <div class="pi-review-actions">
          <Button :disabled="!range || !note.trim() || !!citationIssue" type="submit" size="sm">交给 Pi 调整</Button>
          <Button type="button" variant="ghost" size="sm" @click="selecting = false">取消</Button>
        </div>
      </form>
    </template>
    <label v-if="editable && file.pending && file.conflict" class="pi-review-check pi-review-conflict">
      <Checkbox :model-value="conflictConfirmed" :disabled="disabled"
        @update:model-value="conflictConfirmed = $event === true; if (!conflictConfirmed) emit('select', false)" />
      确认采用隔离结果，覆盖原工程当前版本
    </label>
    <label v-if="editable && file.pending" class="pi-review-check">
      <Checkbox :model-value="selected" :disabled="disabled || (file.conflict && !conflictConfirmed)"
        @update:model-value="emit('select', $event === true)" />
      接收此文件
    </label>
  </div>
</template>
