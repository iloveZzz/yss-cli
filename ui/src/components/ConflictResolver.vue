<script setup lang="ts">
import { computed, reactive } from 'vue'
import { contentEqual } from '@/services/workbench'
import { Button } from '@/components/ui/button'
import { readableValue, type JsonObject } from '@/services/workbench-platform'
const props = defineProps<{ base: JsonObject; local: JsonObject; current: JsonObject }>()
const emit = defineEmits<{ resolve: [value: JsonObject]; close: [] }>()
const labels: Record<string, string> = {
  sourcePath: '主规格路径',
  title: '标题',
  stage: '阶段',
  status: '状态',
  nextAction: '下一步',
  blocker: '阻塞说明',
  notes: '备注',
  resources: '关联资源',
  active: '启用状态',
  progress: '阶段进度',
  note: '阶段说明',
  responseKind: '处理方式',
  answer: '答复',
  reason: '延期原因',
  impact: '影响范围',
  excludedScope: '排除范围',
  reviewAt: '复查日期',
  reviewWhen: '复查条件',
  reviewUtcOffsetMinutes: '复查时区'
}
const choices = reactive<Record<string, 'local' | 'current' | 'base'>>({})
const fields = computed(() =>
  [
    ...new Set([
      ...Object.keys(props.base),
      ...Object.keys(props.local),
      ...Object.keys(props.current)
    ])
  ].map((key) => {
    const same = contentEqual
    const conflict =
      !same(props.local[key], props.base[key]) &&
      !same(props.current[key], props.base[key]) &&
      !same(props.local[key], props.current[key])
    return {
      key,
      conflict,
      automatic: same(props.local[key], props.base[key]) ? props.current[key] : props.local[key]
    }
  })
)
const unresolved = computed(() =>
  fields.value.some((field) => field.conflict && !choices[field.key])
)
function resolve() {
  const value: JsonObject = {}
  for (const field of fields.value)
    value[field.key] = field.conflict
      ? props[choices[field.key] ?? 'local'][field.key]
      : field.automatic
  emit('resolve', value)
}
</script>
<template>
  <section class="notice warning conflict-panel" aria-label="合并并发修改">
    <h3>记录已被其他页面修改</h3>
    <p>逐项选择保留内容，合并后再次保存。</p>
    <fieldset v-for="field in fields.filter((item) => item.conflict)" :key="field.key">
      <legend>{{ labels[field.key] ?? field.key }}</legend>
      <label
        v-for="option in [
          { key: 'local', label: '当前输入' },
          { key: 'current', label: '最新记录' },
          { key: 'base', label: '开始编辑时' }
        ] as const"
        :key="option.key"
        class="conflict-option"
      >
        <input
          v-model="choices[field.key]"
          type="radio"
          :name="`conflict-${field.key}`"
          :value="option.key"
        />
        <span>
          <strong>{{ option.label }}</strong>
          <pre>{{ readableValue(props[option.key][field.key]) }}</pre>
        </span>
      </label>
    </fieldset>
    <div class="inline-actions">
      <Button type="button" :disabled="unresolved" @click="resolve">合并选中内容</Button>
      <Button type="button" variant="outline" @click="emit('close')">继续编辑</Button>
    </div>
  </section>
</template>
<style scoped>
.conflict-panel fieldset {
  border: 1px solid #decda9;
  padding: 12px;
  margin: 12px 0;
  border-radius: 8px;
}
.conflict-option {
  display: flex !important;
  flex-direction: row !important;
  align-items: flex-start;
  gap: 10px !important;
  padding: 8px 0;
}
.conflict-option input {
  margin-top: 4px;
}
.conflict-option pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: inherit;
  margin: 5px 0;
}
</style>
