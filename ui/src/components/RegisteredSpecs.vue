<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { Card } from '@/components/ui/card'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from '@/components/ui/table'
import type { ProjectRecord } from '@/services/workbench'
import type { CatalogSpec } from '@/services/workbench-read'

const props = defineProps<{
  projects: readonly ProjectRecord[]
  specs: readonly CatalogSpec[]
  failed?: boolean
  limit?: number
}>()
const route = useRoute()
const projects = computed(() => new Map(props.projects.map((project) => [project.id, project.name])))
const visible = computed(() => props.specs.slice(0, props.limit ?? 10))
</script>

<template>
  <Card v-if="visible.length" class="panel block gap-0 py-0 shadow-none">
    <p class="muted px-4 pt-4 pb-2" role="status">
      {{ failed ? '已登记 · 进度尚待核对' : '已登记 · 正在核对进度' }}
      <template v-if="visible.length < specs.length">（显示前 {{ visible.length }} 项）</template>
    </p>
    <Table class="spec-table">
      <TableHeader>
        <TableRow>
          <TableHead scope="col">Spec / 工程</TableHead>
          <TableHead scope="col">来源</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="item in visible" :key="item.id">
          <TableHead scope="row">
            <RouterLink :to="{ path: '/specs/' + item.id, query: { return: route.fullPath } }">
              {{ item.title }}
            </RouterLink>
            <span class="row-sub">{{ projects.get(item.projectId) }}</span>
          </TableHead>
          <TableCell class="row-sub">{{ item.sourcePath }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </Card>
</template>
