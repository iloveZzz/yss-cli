<script setup lang="ts">
import { BookMarked, Check, ChevronDown, GitFork } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger
} from '@/components/ui/dropdown-menu'
import type { IssueSource } from '@/services/workbench-issues'
import { platformLabels, sourceHost } from '../schemas/issues'

defineProps<{ source?: IssueSource; options: IssueSource[]; disabled: boolean }>()
const emit = defineEmits<{ select: [id: string] }>()
</script>

<template>
  <header class="source-heading">
    <span class="source-mark"><BookMarked aria-hidden="true" /></span>
    <div class="source-identity">
      <h2>{{ source?.slug || '仓库 Issues' }}</h2>
      <p v-if="source">{{ platformLabels[source.platform] }} <span aria-hidden="true">·</span> {{ sourceHost(source) }} <span aria-hidden="true">·</span> {{ source.projectName }}</p>
    </div>
    <DropdownMenu v-if="options.length" :modal="false">
      <DropdownMenuTrigger as-child>
        <Button type="button" variant="outline" class="source-trigger" :disabled="disabled">
          <GitFork aria-hidden="true" /><span>切换来源</span><ChevronDown aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" :side-offset="4" :collision-padding="8" class="source-menu">
        <DropdownMenuLabel>已登记工程的仓库</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem v-for="option in options" :key="option.id" class="source-item" @select="emit('select', option.id)">
          <span class="source-option">
            <strong>{{ option.slug }}</strong>
            <small>{{ platformLabels[option.platform] }} · {{ sourceHost(option) }} · {{ option.projectName }}</small>
            <small>{{ option.basis === 'tracker' ? 'Issue 跟踪仓库' : '工程远端仓库' }}</small>
            <span v-if="option.id === source?.id" class="sr-only">当前来源</span>
          </span>
          <Check v-if="option.id === source?.id" class="source-check" aria-hidden="true" />
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </header>
</template>

<style scoped>
.source-heading { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; padding: 20px 20px 12px; }
.source-mark { display: grid; flex: none; place-items: center; width: 40px; height: 40px; border: 1px solid var(--yss-colors-border); border-radius: 10px; background: var(--yss-colors-primary-bg); color: var(--yss-color-primary-control-active); }
.source-mark svg { width: 20px; height: 20px; stroke-width: 1.7; }
.source-identity { flex: 1; min-width: 0; }
.source-identity h2 { color: var(--yss-colors-text-title); font-size: 19px; font-weight: 650; line-height: 1.35; letter-spacing: -.25px; overflow-wrap: anywhere; }
.source-identity p { margin-block-start: 3px; color: var(--brand-color-text-secondary); font-size: 12px; overflow-wrap: anywhere; }
.source-identity p > span { margin-inline: 2px; }
.source-trigger { flex: none; gap: 6px; }
.source-trigger svg:last-child { width: 12px; }
.source-menu { width: min(276px, var(--reka-dropdown-menu-content-available-width)); max-height: var(--reka-dropdown-menu-content-available-height); overflow-y: auto; }
.source-item { justify-content: space-between; min-height: 44px; }
.source-option { display: grid; gap: 2px; min-width: 0; overflow-wrap: anywhere; }
.source-option strong { font-size: 13px; font-weight: 550; }
.source-option small { color: var(--brand-color-text-secondary); font-size: 11px; }
.source-check { flex: none; color: var(--yss-color-primary-control-active); }
@container issue-panel (width < 440px) {
  .source-heading { gap: 7px 9px; padding: 14px 12px 8px; }
  .source-mark { width: 32px; height: 32px; }
  .source-mark svg { width: 16px; height: 16px; }
  .source-identity h2 { font-size: 16px; }
  .source-trigger { padding-inline: 7px; }
  .source-trigger svg { display: none; }
}
</style>
