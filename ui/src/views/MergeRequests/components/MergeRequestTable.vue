<script setup lang="ts">
import { ChevronRight, GitBranch, GitFork } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import MergeRequestState from './MergeRequestState.vue'
import {
  dateLabel, platformLabels, repositoryHost, requestKey, requestNumber, type MergeRequestRow
} from '../schemas/requests'

defineProps<{ rows: MergeRequestRow[]; namesakes: ReadonlySet<string> }>()
const emit = defineEmits<{ open: [row: MergeRequestRow, event: MouseEvent] }>()
</script>

<template>
  <div class="requests-table-wrap">
    <Table class="requests-table" role="table">
      <TableCaption class="sr-only">合并请求列表</TableCaption>
      <TableHeader role="rowgroup" class="requests-table-head">
        <TableRow role="row">
          <TableHead scope="col" role="columnheader" class="heading-title">标题</TableHead>
          <TableHead scope="col" role="columnheader" class="heading-repo">来源仓库</TableHead>
          <TableHead scope="col" role="columnheader" class="heading-author">作者</TableHead>
          <TableHead scope="col" role="columnheader" class="heading-date">更新时间</TableHead>
          <TableHead scope="col" role="columnheader" class="heading-date">创建时间</TableHead>
          <TableHead scope="col" role="columnheader" class="heading-status">状态</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody role="rowgroup">
        <TableRow v-for="row in rows" :key="requestKey(row)" role="row" class="request-row">
          <TableCell role="cell" class="title-cell">
            <Button
              type="button"
              variant="ghost"
              class="request-title"
              aria-haspopup="dialog"
              :aria-describedby="'mr-source-' + requestKey(row)"
              @click="emit('open', row, $event)"
            >
              <span class="title-copy">
                <span>{{ row.request.title || '标题不可用' }}</span>
                <small>{{ requestNumber(row.repository.platform, row.request.number) }}</small>
              </span>
              <ChevronRight class="row-chevron" aria-hidden="true" />
            </Button>
          </TableCell>
          <TableCell :id="'mr-source-' + requestKey(row)" role="cell" class="source-cell">
            <span class="repo-slug">{{ row.repository.slug }}</span>
            <small><component :is="row.repository.platform === 'github' ? GitBranch : GitFork" class="platform-icon" aria-hidden="true" />{{ platformLabels[row.repository.platform] }}<template v-if="namesakes.has(row.repository.slug)"> · {{ repositoryHost(row.repository) }}</template></small>
          </TableCell>
          <TableCell role="cell" class="author-cell">
            <span class="mobile-label" aria-hidden="true">作者</span>{{ row.request.author || '不可用' }}
          </TableCell>
          <TableCell role="cell" class="date-cell updated-cell">
            <span class="mobile-label" aria-hidden="true">更新</span>
            <time v-if="row.request.updatedAt" :datetime="row.request.updatedAt">{{ dateLabel(row.request.updatedAt) }}</time>
            <template v-else>不可用</template>
          </TableCell>
          <TableCell role="cell" class="date-cell created-cell">
            <span class="mobile-label" aria-hidden="true">创建</span>
            <time v-if="row.request.createdAt" :datetime="row.request.createdAt">{{ dateLabel(row.request.createdAt) }}</time>
            <template v-else>不可用</template>
          </TableCell>
          <TableCell role="cell" class="status-cell">
            <MergeRequestState :status="row.request.status" :draft="row.request.draft" />
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </div>
</template>

<style scoped>
.requests-table-wrap { container: merge-request-table / inline-size; }
.requests-table { table-layout: fixed; font-size: 13px; }
.requests-table-head { background: var(--workbench-glass-card); }
.requests-table-head th {
  height: 36px;
  color: var(--brand-color-text-secondary);
  font-size: 12px;
  font-weight: 500;
}
.heading-title { width: 32%; padding-inline-start: 20px; }
.heading-repo { width: 19%; }
.heading-date { width: 12%; }
.heading-status { width: 14%; }
.request-row { border-color: var(--yss-colors-border); }
.request-row td { padding: 7px 8px; white-space: normal; overflow-wrap: anywhere; }
.request-row .title-cell { padding-inline-start: 12px; }
.request-row .status-cell { padding-inline-end: 12px; }
.request-title[data-slot='button'] {
  width: 100%;
  min-height: 42px;
  height: auto;
  padding: 4px 8px;
  gap: 5px;
  justify-content: space-between;
  color: var(--brand-color-text);
  text-align: start;
  white-space: normal;
}
.title-copy { display: grid; min-width: 0; gap: 2px; color: var(--yss-colors-text-title); }
.title-copy small, .source-cell small, .date-cell, .mobile-label {
  color: var(--brand-color-text-secondary);
  font-size: 11px;
  font-weight: 400;
}
.source-cell small { display: block; margin-block-start: 2px; }
.platform-icon { display: inline-block; width: 12px; height: 12px; margin-inline-end: 3px; vertical-align: -2px; }
.row-chevron { flex: none; color: var(--brand-color-text-secondary); }
.date-cell { font-variant-numeric: tabular-nums; }
.mobile-label { display: none; }
@container merge-request-table (width < 770px) {
  .requests-table, .requests-table :deep(tbody) { display: block; }
  .requests-table-head {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  .request-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(100px, auto) minmax(100px, auto);
    grid-template-areas: 'title source status' 'author updated created';
    align-items: center;
    column-gap: 8px;
    padding: 7px 12px;
  }
  .request-row td { min-width: 0; padding: 2px 0; }
  .request-row :is(.title-cell, .status-cell) { padding-inline: 0; }
  .request-title[data-slot='button'] { min-height: 44px; padding-inline: 4px; }
  .title-cell { grid-area: title; }
  .source-cell { grid-area: source; }
  .status-cell { grid-area: status; }
  .author-cell { grid-area: author; padding-inline-start: 4px; }
  .updated-cell { grid-area: updated; }
  .created-cell { grid-area: created; }
  .mobile-label { display: inline; margin-inline-end: 3px; }
}
@container merge-request-table (width < 460px) {
  .request-row {
    grid-template-columns: minmax(0, 1fr) minmax(0, .95fr);
    grid-template-areas: 'title title' 'source status' 'author author' 'updated created';
    column-gap: 6px;
  }
  .source-cell, .date-cell { padding-inline-start: 4px; }
  .status-cell { justify-self: end; }
}
</style>
