<script setup lang="ts">
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { useId } from 'vue'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { healthLabels, type SpecView } from '@/services/workbench'
import { isWebLink } from '../schemas/markdown'
defineProps<{ resources: SpecView['resources'] }>()
const emit = defineEmits<{ open: [path: string]; copy: [path: string] }>()
const id = useId()
const kinds = { supplement: '规格补充', prototype: '原型', interaction: '交互说明', other: '其他' }
</script>
<template>
  <div class="table-scroll resource-table-wrap" tabindex="0" role="region" :aria-labelledby="id">
    <Table class="spec-table resource-table">
      <TableCaption :id="id" class="sr-only">关联资源、来源与可访问状态</TableCaption>
      <TableHeader>
        <TableRow>
          <TableHead scope="col">资源</TableHead>
          <TableHead scope="col">位置</TableHead>
          <TableHead scope="col">来源状态</TableHead>
          <TableHead scope="col">操作</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(resource, index) in resources" :key="`${resource.record.target}:${index}`">
          <TableHead scope="row">
            <strong>{{ resource.record.label }}</strong>
            <span class="row-sub">{{ kinds[resource.record.kind] }}</span>
          </TableHead>
          <TableCell class="resource-path">
            <code>{{ resource.record.target }}</code>
          </TableCell>
          <TableCell class="resource-health">
            <span
              :class="
                ['available', 'external'].includes(resource.health) ? 'muted' : 'warning-text'
              "
            >
              {{ healthLabels[resource.health] }}
            </span>
            <p v-if="resource.message" class="warning-text">{{ resource.message }}</p>
          </TableCell>
          <TableCell class="resource-actions">
            <a
              v-if="resource.record.targetType === 'web' && isWebLink(resource.record.target)"
              :href="resource.record.target"
              target="_blank"
              rel="noopener noreferrer"
            >
              打开链接 ↗
            </a>
            <span v-else-if="resource.record.targetType === 'web'" class="warning-text">
              链接格式无效
            </span>
            <div v-else class="inline-actions">
              <Button type="button" variant="outline" @click="emit('open', resource.record.target)">
                查看内容
              </Button>
              <Button type="button" variant="ghost" @click="emit('copy', resource.record.target)">
                复制路径
              </Button>
            </div>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </div>
  <Empty v-if="!resources.length">
    <EmptyDescription>暂无关联资源，可同步主规格关联或添加个人资源。</EmptyDescription>
  </Empty>
</template>
<style scoped>
.resource-table-wrap {
  max-width: 100%;
}
.resource-table {
  min-width: 660px;
  border-collapse: separate;
  border-spacing: 0;
}
.resource-table tbody th {
  position: sticky;
  inset-inline-start: 0;
  z-index: 1;
  min-width: 150px;
  max-width: 230px;
  padding: 12px 14px;
  background: #fff;
  font-size: 12px;
  font-weight: 400;
  text-align: left;
  vertical-align: top;
}
.resource-table strong {
  font-weight: 550;
}
.resource-table td {
  vertical-align: top;
  font-size: 12px;
}
.resource-path {
  min-width: 180px;
  max-width: 360px;
  overflow-wrap: anywhere;
  white-space: normal;
}
.resource-path code {
  font-size: 11px;
}
.resource-health {
  min-width: 135px;
  max-width: 220px;
}
.resource-health p {
  font-size: 11px;
  margin-top: 6px;
  overflow-wrap: anywhere;
}
.resource-actions {
  min-width: 170px;
}
.resource-actions a {
  color: #315dcf;
  white-space: nowrap;
}
.resource-actions .inline-actions {
  gap: 4px;
}
</style>
