<script setup lang="ts">
import { ArrowLeftRight } from '@lucide/vue'
import { useResizeObserver } from '@vueuse/core'
import { MarkdownRender } from 'markstream-vue'
import type { TableNode } from 'stream-markdown-parser'
import { computed, shallowRef, useTemplateRef } from 'vue'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { assistantMarkdownScope } from '../schemas/assistant-markdown'

defineProps<{ node: TableNode }>()

const viewport = useTemplateRef<HTMLDivElement>('viewport')
const table = computed(() => viewport.value?.querySelector('table') ?? null)
const overflows = shallowRef(false)
useResizeObserver([viewport, table], () => {
  const element = viewport.value
  overflows.value = !!element && element.scrollWidth > element.clientWidth + 1
})
</script>

<template>
  <div class="assistant-markdown-table">
    <div v-if="overflows" class="assistant-table-hint" aria-hidden="true">
      <ArrowLeftRight :size="13" />
      横向滚动查看更多
    </div>
    <div ref="viewport" data-slot="table-container" class="assistant-table-scroll"
      :class="{ 'assistant-simple-table': node.header.cells.length === 2 }"
      :role="overflows ? 'region' : undefined" :aria-label="overflows ? '可横向滚动的数据表格' : undefined"
      :tabindex="overflows ? 0 : undefined">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead v-for="(cell, column) in node.header.cells" :key="column" scope="col">
              <MarkdownRender :nodes="cell.children" :custom-id="assistantMarkdownScope" html-policy="escape"
                render-as-fragment dom-mode="minimal" :typewriter="false" />
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="(row, index) in node.rows" :key="index">
            <TableCell v-for="(cell, column) in row.cells" :key="column" class="align-top whitespace-normal">
              <MarkdownRender :nodes="cell.children" :custom-id="assistantMarkdownScope" html-policy="escape"
                render-as-fragment dom-mode="minimal" :typewriter="false" />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  </div>
</template>

<style scoped>
.assistant-markdown-table { min-inline-size: 0; }
.assistant-table-hint {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 5px;
  margin-block-end: 5px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  line-height: 1.4;
}
</style>
