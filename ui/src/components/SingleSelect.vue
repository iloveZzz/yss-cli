<script setup lang="ts">
import { computed, shallowRef, useTemplateRef } from 'vue'
import {
  SelectRoot, SelectTrigger, SelectValue, SelectPortal, SelectContent,
  SelectViewport, SelectItem, SelectItemText, SelectItemIndicator,
} from 'reka-ui'

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
const trigger = useTemplateRef<HTMLButtonElement>('trigger')
const open = shallowRef(false)
const dialog = shallowRef<HTMLDialogElement>()
function setOpen(value: boolean) {
  // 原生 dialog 的 top layer 会遮挡传送到 body 的菜单。
  if (value) dialog.value = trigger.value?.closest('dialog') ?? undefined
  open.value = value
}
function closeOnEscape(event: KeyboardEvent) {
  event.preventDefault()
  open.value = false
}
</script>

<template>
  <SelectRoot
    :model-value="internalValue"
    :open="open"
    :disabled="disabled"
    :name="name"
    :required="required"
    @update:model-value="updateValue"
    @update:open="setOpen"
  >
    <SelectTrigger v-bind="$attrs" as-child class="single-select-trigger">
      <button ref="trigger" type="button" data-slot="select-trigger">
        <span class="single-select-value"><SelectValue :placeholder="emptyLabel" /></span>
        <svg class="single-select-chevron" width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m4 6 4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </button>
    </SelectTrigger>
    <SelectPortal :to="dialog ?? 'body'">
      <SelectContent
        data-slot="select-content"
        class="single-select-content"
        position="popper"
        side="bottom"
        align="start"
        position-strategy="fixed"
        :side-offset="4"
        :collision-padding="8"
        :collision-boundary="dialog"
        @escape-key-down="closeOnEscape"
      >
        <SelectViewport class="single-select-viewport">
          <SelectItem
            v-for="option in options"
            :key="option.value"
            :value="option.value === '' ? null : option.value"
            :text-value="option.label"
            :disabled="option.disabled"
            data-slot="select-item"
            class="single-select-item"
          >
            <SelectItemText class="single-select-item-text">{{ option.label }}</SelectItemText>
            <SelectItemIndicator class="single-select-indicator"><svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m3 8 3 3 7-7" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" /></svg></SelectItemIndicator>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
  <!-- Reka 仅为具名字段生成原生 select，补齐无 name 的必填校验。 -->
  <select
    v-if="required && !name"
    :value="modelValue"
    :disabled="disabled"
    required
    tabindex="-1"
    aria-hidden="true"
    class="single-select-validation"
    @invalid="trigger?.focus()"
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
.single-select-trigger { display:inline-flex; align-items:center; justify-content:space-between; gap:8px; min-width:0; max-width:100%; height:34px; min-height:34px; padding:0 10px; border:1px solid var(--yss-colors-border-control); border-radius:8px; background:var(--brand-color-bg-container); color:inherit; text-align:left; font-size:13px; line-height:1.4; vertical-align:middle; box-shadow:none; }
.single-select-trigger:hover:not(:disabled) { border-color:#a8b4cb; }
.single-select-trigger:focus { border-color:var(--yss-color-primary-control); outline:none; box-shadow:none; }
.single-select-trigger[data-state=open] { border-color:var(--yss-color-primary-control); }
.single-select-trigger:disabled { cursor:not-allowed; opacity:.55; }
.single-select-trigger[aria-invalid=true] { border-color:var(--yss-colors-error-text); }
.single-select-trigger[aria-invalid=true]:focus { border-bottom-width:3px; }
@media (forced-colors:active) {
  .single-select-trigger:focus { outline:2px solid Highlight; outline-offset:2px; }
}
.single-select-value { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.single-select-chevron { flex:none; color:var(--brand-color-text-secondary); }
.single-select-trigger[data-state=open] .single-select-chevron { transform:rotate(180deg); }
/* Reka Popper 将 scope 属性放在定位容器；菜单自身需按专属类名匹配。 */
:global(.single-select-content) { z-index:100; width:var(--reka-select-trigger-width); min-width:160px; max-width:calc(100vw - 16px); max-height:min(320px,var(--reka-select-content-available-height)); overflow:hidden; border:1px solid var(--yss-colors-border); border-radius:8px; background:var(--brand-color-bg-elevated); color:var(--brand-color-text); box-shadow:0 8px 24px #1721361f; font-size:13px; }
/* Viewport 包含额外的样式节点，同样不会将 scope 属性透传到滚动容器。 */
:global(.single-select-content .single-select-viewport) { min-width:0; min-height:0; padding:4px; overscroll-behavior:contain; scrollbar-width:thin; scrollbar-gutter:stable; }
:global(.single-select-content .single-select-viewport::-webkit-scrollbar) { display:block; width:8px; }
:global(.single-select-item) { position:relative; display:flex; align-items:center; min-height:34px; padding:7px 30px 7px 9px; border-radius:5px; line-height:1.45; cursor:default; user-select:none; outline:none; }
.single-select-item[data-state=checked] { color:var(--yss-color-primary-control); background:var(--yss-colors-primary-bg); }
.single-select-item[data-highlighted] { background:var(--brand-color-fill-secondary); }
.single-select-item[data-state=checked][data-highlighted] { background:#e4ecff; }
.single-select-item[data-disabled] { color:var(--brand-color-text-secondary); opacity:.5; pointer-events:none; }
.single-select-item-text { min-width:0; overflow-wrap:anywhere; }
.single-select-indicator { position:absolute; right:9px; display:flex; color:var(--yss-color-primary-control); }
.single-select-validation { position:absolute; width:1px; min-height:0; height:1px; margin:-1px; padding:0; overflow:hidden; clip-path:inset(50%); white-space:nowrap; border:0; }
@media (width < 720px) { .single-select-trigger { min-height:44px; height:44px; font-size:16px; }:global(.single-select-item) { min-height:44px; }:global(.single-select-content) { max-height:min(360px,var(--reka-select-content-available-height)); font-size:16px; } }
</style>
