<script setup lang="ts">
import { watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft } from '@lucide/vue'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { useWorkbenchCatalog } from '@/hooks/useWorkbenchReads'
import { useAgentScope } from './useAgentScope'
import './workspace.css'

const router = useRouter()
const route = useRoute()
const { catalog, catalogLoading, catalogError, refreshCatalog } = useWorkbenchCatalog()
const { preferredSpecId } = useAgentScope()

watch(preferredSpecId, (specId) => {
  if (specId) void router.replace({ name: 'agent-workbench', params: { specId }, query: route.query })
}, { immediate: true })
</script>

<template>
  <div class="pi-workspace" data-history="false">
    <div class="pi-stage">
      <header class="pi-bar">
        <Button variant="ghost" size="icon" as-child>
          <RouterLink to="/workbench" aria-label="返回工作台" title="返回工作台"><ArrowLeft :size="17" /></RouterLink>
        </Button>
        <h1>AI Agent</h1>
      </header>
      <div class="pi-entry-status">
        <LoadingSkeleton v-if="preferredSpecId || (!catalog && !catalogError)" variant="text" :rows="3" label="正在打开 Agent 工作台" />
        <Alert v-else-if="catalogError" class="notice error" role="alert">
          {{ catalogError }}
          <Button variant="outline" :disabled="catalogLoading" @click="refreshCatalog()">重试</Button>
        </Alert>
        <Empty v-else>
          <EmptyHeader>
            <EmptyTitle><h2>暂无可用规格</h2></EmptyTitle>
            <EmptyDescription>Agent 会话需要关联一份已启用的规格。</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button as-child variant="outline"><RouterLink to="/specs">打开规格管理</RouterLink></Button>
          </EmptyContent>
        </Empty>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pi-entry-status {
  width: min(480px, calc(100% - 32px));
  margin: clamp(24px, 10vh, 90px) auto 0;
}
</style>
