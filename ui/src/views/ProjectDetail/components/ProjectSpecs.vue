<script setup lang="ts">
import { computed, shallowRef, toRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import WorkItemRows from '@/components/WorkItemRows.vue'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import { FieldLabel } from '@/components/ui/field'
import { useDelayedBusy } from '@/hooks/useDelayedBusy'
import { defaultWorkQuery, type ProjectView } from '@/services/workbench'
import { useSpecWorkItems } from '@/views/Specs/hooks/useSpecWorkItems'
import { encodeWorkQuery } from '@/views/Specs/schemas/filter'
import DiscoverSpecs from './DiscoverSpecs.vue'

const props = defineProps<{
  project: ProjectView
  directoryKnown: boolean
  registeredCount?: number
  active: boolean
  refresh: () => Promise<boolean>
  refreshing: boolean
  refreshError: string
  reloadProject: (minimumRevision?: number) => Promise<boolean>
}>()
const route = useRoute()
const router = useRouter()
const showInactive = computed({
  get: () => route.query.activity === 'all',
  set: (value) => {
    void router.replace({ query: { ...route.query, activity: value ? 'all' : undefined } })
  }
})
const showClosed = computed({
  get: () => route.query.showClosed === 'true',
  set: (value) => {
    void router.replace({ query: { ...route.query, showClosed: value ? 'true' : undefined } })
  }
})
const query = computed(() => ({
  ...defaultWorkQuery(),
  projectIds: [props.project.record.id],
  activity: showInactive.value ? ('all' as const) : ('active' as const),
  includeClosed: showClosed.value
}))
const {
  result, resultsCurrent, loading, loadingMore, verification, verificationError, writable, error, reload, loadMore
} = useSpecWorkItems(query, toRef(() => props.active))

// Pending responses have no verified rows or coverage, even when their counts are zero.
const page = computed(() => result.value?.projectionVersion ? result.value : undefined)
const coverage = computed(() => page.value?.coverage)
const listBusy = computed(() => props.refreshing || (loading.value && !loadingMore.value))
const delayedRefresh = useDelayedBusy(() => listBusy.value && !!page.value?.items.length, () => 180)
const refreshLabel = computed(() => {
  if (props.refreshing) return '正在核验工程 Spec'
  return resultsCurrent.value ? '正在刷新工程 Spec' : '正在筛选，暂显示上次结果'
})
const listError = computed(() => props.refreshError || error.value ||
  (verification.value === 'failed' ? verificationError.value || 'Spec 进度核对失败。' : ''))
const awaitingFirstPage = computed(() => !page.value && !listError.value &&
  (listBusy.value || verification.value === 'pending' || verification.value === 'stale'))
const allLink = computed(() => ({
  path: '/specs',
  query: encodeWorkQuery({ ...query.value, activity: 'all', includeClosed: true })
}))

function reloadFailed() {
  if (props.active && error.value) return reload()
}
function retry() {
  return props.refreshError || verification.value === 'failed' ? props.refresh() : reload()
}
defineExpose({ reloadFailed })

const discoverOpen = shallowRef(false)
const discoverMounted = shallowRef(false)
watch(discoverOpen, (open) => {
  if (open) discoverMounted.value = true
})
watch(() => props.project.record.rootPath, () => {
  discoverOpen.value = false
  discoverMounted.value = false
})
</script>

<template>
  <div class="section-heading">
    <h2>已纳管 Spec</h2>
    <RouterLink :to="allLink">查看项目全部记录 →</RouterLink>
  </div>
  <div class="toolbar project-spec-toolbar">
    <span v-if="coverage" class="project-spec-count">
      共 {{ coverage.total }} 项 · 启用且已关闭 {{ coverage.closed }} · 停用 {{ coverage.inactive }}
    </span>
    <span v-else-if="registeredCount !== undefined" class="project-spec-count">已登记 {{ registeredCount }} 项</span>
    <FieldLabel for="project-spec-inactive" class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent">
      <Checkbox id="project-spec-inactive" v-model="showInactive" />
      显示停用记录
    </FieldLabel>
    <FieldLabel for="project-spec-closed" class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent">
      <Checkbox id="project-spec-closed" v-model="showClosed" />
      显示已完成／已取消
    </FieldLabel>
  </div>
  <Alert v-if="listError" class="notice error" role="alert" variant="destructive">
    {{ listError }}{{ page ? ' 保留上次读取的记录。' : '' }}
    <Button type="button" variant="outline" :disabled="loading || refreshing" @click="retry">重试</Button>
  </Alert>
  <p v-if="refreshing" class="project-result-status muted" role="status">
    正在核验工程 Spec。{{ page ? ' 保留上次核实结果。' : '' }}
  </p>
  <p v-else-if="page && !resultsCurrent && !listBusy" class="project-result-status muted">
    下方显示上次读取的记录，请重试以应用当前筛选。
  </p>
  <p v-else-if="page && verification === 'stale' && !listBusy" class="project-result-status muted" role="status">
    正在核对变化，当前显示上次核实结果。
  </p>
  <p v-else-if="!page && !listError && (verification === 'pending' || verification === 'stale')" class="project-result-status muted" role="status">
    Spec 进度待核验。
  </p>
  <p class="sr-only" role="status">{{ delayedRefresh && !refreshing ? refreshLabel : '' }}</p>
  <div v-if="page" class="relative">
    <Card class="panel block gap-0 py-0 shadow-none" :aria-busy="loading || refreshing">
      <div :inert="!resultsCurrent">
        <WorkItemRows :items="page.items" :refreshing="listBusy" :refresh-label="refreshLabel">
          <template #empty>
            <Empty>
              <EmptyDescription>
                {{ resultsCurrent ? '当前筛选没有匹配记录，可调整条件或查看全部记录。' : '上次筛选没有匹配记录。' }}
              </EmptyDescription>
            </Empty>
          </template>
        </WorkItemRows>
      </div>
      <div v-if="page.total > page.items.length" class="load-more">
        <Button type="button" variant="outline" :disabled="loading || refreshing || !writable" @click="loadMore()">
          <LoadingIndicator :active="loadingMore" />
          {{ loadingMore ? '正在加载更多…' : '加载更多' }}（已显示 {{ page.items.length }} / {{ page.total }}）
        </Button>
      </div>
    </Card>
    <LoadingIndicator v-if="!page.items.length" :active="listBusy" placement="overlay" :label="refreshLabel" />
  </div>
  <LoadingSkeleton v-else-if="awaitingFirstPage" variant="table" :rows="4" label="正在读取工程 Spec" />
  <p class="view-note muted">
    包含自动纳管和手动关联的 Spec。其他候选文件可在下方选择关联；不扫描外部平台。
  </p>
  <Collapsible
    v-if="directoryKnown && project.available && project.record.active"
    v-model:open="discoverOpen"
    :key="project.record.rootPath"
    :unmount-on-hide="false"
    class="discover-disclosure"
  >
    <CollapsibleTrigger>自动发现与关联 Spec 文件</CollapsibleTrigger>
    <CollapsibleContent>
      <DiscoverSpecs
        v-if="discoverMounted"
        :project-id="project.record.id"
        :root-path="project.record.rootPath"
        :directories="project.sourceDirectories"
        :visible="active && discoverOpen"
        :reload-project="reloadProject"
      />
    </CollapsibleContent>
  </Collapsible>
</template>

<style scoped>
.project-result-status {
  margin: 0 0 8px;
  font-size: 12px;
}
.load-more {
  display: flex;
  justify-content: center;
  padding-top: 12px;
}
@media (width < 720px) {
  .project-spec-toolbar {
    column-gap: 12px;
    row-gap: 0;
  }
  .project-spec-count {
    flex-basis: 100%;
  }
}
.discover-disclosure {
  margin-top: 22px;
  border-top: 1px solid var(--yss-colors-border);
}
.discover-disclosure :deep(.discover-panel) {
  margin-top: 0;
}
</style>
