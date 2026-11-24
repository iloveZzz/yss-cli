<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import {
  Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
} from '@/components/ui/select'

defineOptions({ inheritAttrs: false })
const props = withDefaults(defineProps<{
  modelValue?: string
  options: readonly { value: string; label: string; disabled?: boolean }[]
  placeholder?: string
  disabled?: boolean
  name?: string
  required?: boolean
}>(), { modelValue: '', placeholder: '请选择' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
// Reka 的空值通过 null 表示，确保隐藏表单控件仍提交空字符串并执行 required 校验。
const internalValue = computed(() => props.modelValue === '' ? null : props.modelValue)
const emptyLabel = computed(() => props.options.find((option) => option.value === '')?.label ?? props.placeholder)
function updateValue(value: unknown) {
  if (value === null || typeof value === 'string') emit('update:modelValue', value ?? '')
}
const trigger = useTemplateRef<InstanceType<typeof SelectTrigger>>('trigger')
function focusTrigger() {
  trigger.value?.$el?.focus()
}
</script>

<template>
  <Select
    :model-value="internalValue"
    :disabled="disabled"
    :name="name"
    :required="required"
    @update:model-value="updateValue"
  >
    <SelectTrigger ref="trigger" v-bind="$attrs" class="single-select-trigger">
      <SelectValue class="single-select-value" :placeholder="emptyLabel" />
    </SelectTrigger>
    <SelectContent
      class="single-select-content"
      position="popper"
      side="bottom"
      align="start"
      position-strategy="fixed"
      :side-offset="4"
      :collision-padding="8"
    >
      <SelectItem
        v-for="option in options"
        :key="option.value"
        :value="option.value === '' ? null : option.value"
        :text-value="option.label"
        :disabled="option.disabled"
      >
        <span class="single-select-option">{{ option.label }}</span>
      </SelectItem>
    </SelectContent>
  </Select>
  <!-- Reka 仅为具名字段生成原生 select，补齐无 name 的必填校验。 -->
  <select
    v-if="required && !name"
    :value="modelValue"
    :disabled="disabled"
    required
    tabindex="-1"
    aria-hidden="true"
    class="single-select-validation"
    @invalid="focusTrigger"
    @change="updateValue(($event.target as HTMLSelectElement).value)"
  >
    <option value="">{{ emptyLabel }}</option>
    <option
      v-for="option in options.filter(option => option.value !== '')"
      :key="option.value"
      :value="option.value"
      :disabled="option.disabled"
    >{{ option.label }}</option>
  </select>
</template>

<style scoped>
.single-select-validation {
  position: absolute;
  width: 1px;
  min-height: 0;
  height: 1px;
  margin: -1px;
  padding: 0;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
  border: 0;
}
</style>
