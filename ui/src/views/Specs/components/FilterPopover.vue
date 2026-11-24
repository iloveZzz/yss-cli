<script setup lang="ts">
import { nextTick, useId, useTemplateRef } from 'vue'
import { Popover, PopoverTrigger, PopoverContent, PopoverClose } from '@/components/ui/popover'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

defineProps<{ label: string; summary?: string; count?: number; clearable?: boolean }>()
const emit = defineEmits<{ clear: [] }>()
const id = useId()
const footer = useTemplateRef<HTMLDivElement>('footer')
async function clear() {
 emit('clear')
 await nextTick()
 footer.value?.querySelector<HTMLButtonElement>('[data-filter-done]')?.focus()
}
</script>

<template>
 <Popover>
  <PopoverTrigger as-child>
   <Button type="button" variant="outline" class="filter-trigger" :class="{ selected: count }">
    <span>{{ label }}</span>
    <span v-if="summary" class="filter-summary">{{ summary }}</span>
    <Badge v-if="count" variant="secondary" class="filter-count">{{ count }}</Badge>
    <svg class="filter-chevron" width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="m4 6 4 4 4-4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
   </Button>
  </PopoverTrigger>
   <PopoverContent as-child class="p-0" align="start" :side-offset="7" :collision-padding="12">
    <div class="filter-popover" :aria-labelledby="id">
     <div class="popover-heading"><strong :id="id">{{ label }}</strong><span v-if="clearable" class="muted">{{ count ? `已选 ${count} 项` : '全部' }}</span></div>
     <div class="popover-body"><slot /></div>
     <div ref="footer" class="popover-footer">
      <Button v-if="clearable" type="button" variant="ghost" :disabled="!count" @click="clear">清除</Button>
      <PopoverClose as-child><Button type="button" variant="outline" data-filter-done>完成</Button></PopoverClose>
     </div>
    </div>
   </PopoverContent>
 </Popover>
</template>

<style scoped>
.filter-trigger { gap:7px; max-width:100%; min-width:0; height:34px; padding:0 10px; border-color:var(--yss-colors-border-control); }
.filter-trigger.selected { color:#315dcf; border-color:#cbd8fa; background:#f5f8ff; }
.filter-summary { max-width:100px; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--brand-color-text-secondary); font-weight:400; }
.filter-count { display:grid; place-items:center; min-width:18px; height:18px; padding:0 4px; border-radius:5px; background:#e4ecff; color:#315dcf; font-size:11px; }
.filter-chevron { flex:none; color:var(--brand-color-text-secondary); transition:transform .15s; }
.filter-trigger[data-state=open] .filter-chevron { transform:rotate(180deg); }
.filter-popover {
 z-index:30;
 width:280px;
 max-width:min(calc(100vw - 24px), var(--reka-popover-content-available-width, 100vw));
 max-height:var(--reka-popover-content-available-height, calc(100dvh - 24px));
 display:flex;
 flex-direction:column;
 border:1px solid #dce2eb;
 border-radius:10px;
 background:#fff;
 color:var(--brand-color-text);
 box-shadow:0 8px 30px #1721361f;
 overflow:hidden;
}
.popover-heading { display:flex; justify-content:space-between; align-items:center; gap:12px; padding:12px 14px; border-bottom:1px solid #edf0f4; font-size:12px; }
.popover-body { overflow:auto; overscroll-behavior:contain; padding:6px; min-height:0; max-height:320px; }
.popover-footer { display:flex; justify-content:flex-end; gap:8px; padding:8px 10px; border-top:1px solid #edf0f4; }
.popover-footer>:first-child:not(:last-child) { margin-right:auto; }
@media (width < 720px) { .filter-trigger { min-height:44px; }.popover-footer :deep(button) { min-height:44px; } }
</style>
