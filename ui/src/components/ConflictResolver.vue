<script setup lang="ts">
import { computed, reactive, useId } from 'vue'
import { contentEqual } from '@/services/workbench'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { FieldLabel } from '@/components/ui/field'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { readableValue, type JsonObject } from '@/services/workbench-platform'
const props = defineProps<{ base: JsonObject; local: JsonObject; current: JsonObject }>()
const emit = defineEmits<{ resolve: [value: JsonObject]; close: [] }>()
const id = useId()
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
const options = [
  { key: 'local', label: '当前输入' },
  { key: 'current', label: '最新记录' },
  { key: 'base', label: '开始编辑时' }
] as const
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
  <section class="conflict-panel" :aria-labelledby="id">
    <Alert class="notice warning">
      <AlertTitle :id="id">记录已被其他页面修改</AlertTitle>
      <AlertDescription>逐项选择保留内容，合并后再次保存。</AlertDescription>
    </Alert>
    <fieldset v-for="field in fields.filter((item) => item.conflict)" :key="field.key">
      <legend :id="`${id}-${field.key}`">{{ labels[field.key] ?? field.key }}</legend>
      <RadioGroup
        v-model="choices[field.key]"
        :aria-labelledby="`${id}-${field.key}`"
      >
        <div v-for="option in options" :key="option.key" class="conflict-option">
          <RadioGroupItem :id="`${id}-${field.key}-${option.key}`" :value="option.key" />
          <div class="conflict-value">
            <FieldLabel :for="`${id}-${field.key}-${option.key}`">{{ option.label }}</FieldLabel>
            <pre>{{ readableValue(props[option.key][field.key]) }}</pre>
          </div>
        </div>
      </RadioGroup>
    </fieldset>
    <div class="inline-actions">
      <Button type="button" :disabled="unresolved" @click="resolve">合并选中内容</Button>
      <Button type="button" variant="outline" @click="emit('close')">继续编辑</Button>
    </div>
  </section>
</template>
<style scoped>
.conflict-panel fieldset {
  min-width: 0;
  border: 1px solid #decda9;
  padding: 12px;
  margin: 12px 0;
  border-radius: 8px;
}
.conflict-option {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 0;
}
.conflict-option [data-slot='radio-group-item'] { flex: none; }
.conflict-value { min-width: 0; }
.conflict-value label { cursor: pointer; }
.conflict-option pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font: inherit;
  margin: 5px 0;
}
</style>
