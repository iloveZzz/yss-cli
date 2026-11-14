<script setup lang="ts">
import { shallowRef, useTemplateRef } from 'vue'
import {
  SelectRoot, SelectTrigger, SelectValue, SelectPortal, SelectContent,
  SelectViewport, SelectItem, SelectItemText, SelectItemIndicator,
  SelectScrollUpButton, SelectScrollDownButton,
} from 'reka-ui'

defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{
  modelValue?: string
  options: readonly { value: string; label: string; disabled?: boolean }[]
  placeholder?: string
  disabled?: boolean
  name?: string
  required?: boolean
}>(), { modelValue: '', placeholder: '请选择' })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
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
  <SelectRoot :model-value="modelValue" :open="open" :disabled="disabled" :name="name" :required="required" @update:model-value="emit('update:modelValue', $event)" @update:open="setOpen">
    <SelectTrigger v-bind="$attrs" as-child class="single-select-trigger">
      <button ref="trigger" type="button" data-slot="select-trigger">
        <SelectValue class="single-select-value" :placeholder="placeholder" />
        <svg class="single-select-chevron" width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m4 6 4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </button>
    </SelectTrigger>
    <SelectPortal :to="dialog ?? 'body'">
      <SelectContent data-slot="select-content" class="single-select-content" position="popper" align="start" :side-offset="6" :collision-padding="12" :collision-boundary="dialog" @escape-key-down="closeOnEscape">
        <SelectScrollUpButton class="single-select-scroll"><svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m4 10 4-4 4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg></SelectScrollUpButton>
        <SelectViewport class="single-select-viewport">
          <SelectItem v-for="option in options" :key="option.value" :value="option.value" :text-value="option.label" :disabled="option.disabled" data-slot="select-item" class="single-select-item">
            <SelectItemText class="single-select-item-text">{{ option.label }}</SelectItemText>
            <SelectItemIndicator class="single-select-indicator"><svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m3 8 3 3 7-7" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" /></svg></SelectItemIndicator>
          </SelectItem>
        </SelectViewport>
        <SelectScrollDownButton class="single-select-scroll"><svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m4 6 4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg></SelectScrollDownButton>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>

<style scoped>
.single-select-trigger { display:inline-flex; align-items:center; justify-content:space-between; gap:8px; min-width:0; max-width:100%; height:28px; padding:3px 9px; border:1px solid var(--yss-colors-border-control); border-radius:8px; background:var(--brand-color-bg-container); color:inherit; text-align:left; font-size:13px; line-height:1.5; box-shadow:none; }
.single-select-trigger:hover:not(:disabled) { border-color:#a8b4cb; }
.single-select-trigger:focus-visible { border-color:var(--yss-color-primary-control); outline:none; box-shadow:0 0 0 3px var(--yss-colors-primary-bg); }
.single-select-trigger[data-state=open] { border-color:var(--yss-color-primary-control); }
.single-select-trigger:disabled { cursor:not-allowed; opacity:.55; }
.single-select-trigger[aria-invalid=true] { border-color:var(--yss-colors-error-text); }
.single-select-value { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.single-select-chevron { flex:none; color:var(--brand-color-text-secondary); }
.single-select-trigger[data-state=open] .single-select-chevron { transform:rotate(180deg); }
/* Reka Popper 将 scope 属性放在定位容器；菜单自身需按专属类名匹配。 */
:global(.single-select-content) { z-index:100; min-width:var(--reka-select-trigger-width); max-width:calc(100vw - 24px); max-height:min(320px,var(--reka-select-content-available-height)); overflow:hidden; border:1px solid var(--yss-colors-border); border-radius:10px; background:var(--brand-color-bg-elevated); color:var(--brand-color-text); box-shadow:0 8px 24px #1721361f; font-size:13px; }
/* Viewport 包含额外的样式节点，同样不会将 scope 属性透传到滚动容器。 */
:global(.single-select-viewport) { min-width:0; padding:4px; overscroll-behavior:contain; }
.single-select-item { position:relative; display:flex; align-items:center; min-height:32px; padding:6px 30px 6px 9px; border-radius:6px; line-height:1.5; cursor:default; user-select:none; outline:none; }
.single-select-item[data-state=checked] { color:var(--yss-color-primary-control); background:var(--yss-colors-primary-bg); }
.single-select-item[data-highlighted] { background:var(--brand-color-fill-secondary); }
.single-select-item[data-state=checked][data-highlighted] { background:#e4ecff; }
.single-select-item[data-disabled] { color:var(--brand-color-text-secondary); opacity:.5; pointer-events:none; }
.single-select-item-text { min-width:0; overflow-wrap:anywhere; }
.single-select-indicator { position:absolute; right:9px; display:flex; color:var(--yss-color-primary-control); }
.single-select-scroll { display:flex; align-items:center; justify-content:center; min-height:22px; color:var(--brand-color-text-secondary); }
@media (width < 720px) { .single-select-trigger { min-height:40px; }.single-select-item { min-height:40px; }:global(.single-select-content) { max-height:min(360px,var(--reka-select-content-available-height)); } }
</style>
