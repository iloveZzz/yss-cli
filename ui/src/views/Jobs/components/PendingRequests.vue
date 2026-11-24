<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { computed, onMounted, shallowRef } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import ModalDialog from '@/components/ModalDialog.vue'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Empty, EmptyDescription } from '@/components/ui/empty'
import JobDisclosure from './JobDisclosure.vue'
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import {
  dateLabel,
  pendingRequests,
  reconcilePendingMutation,
  resolvePendingRequest,
  acknowledgeRequest,
  errorMessage
} from '@/services/workbench'
import {
  getRequests,
  getReceipt,
  jobStatusLabels,
  readableValue,
  type RequestRecord
} from '@/services/workbench-platform'
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ count: [total: number] }>()
const { refresh: refreshSnapshot } = useWorkbench()
const records = shallowRef<RequestRecord[]>([])
const total = shallowRef(0)
const dated = computed(() => records.value.some((record) => record.updatedAt || record.createdAt))
const error = shallowRef('')
const result = shallowRef('')
const receiptDetails = shallowRef('')
const phase = shallowRef<{ kind: 'refresh' } | { kind: 'inspect'; requestId: string }>()
const loading = computed(() => !!phase.value)
const historyLoaded = shallowRef(false)

async function readReceipts() {
  const page = await getRequests()
  records.value = page.items.sort((a, b) =>
    (b.updatedAt ?? b.createdAt ?? '').localeCompare(a.updatedAt ?? a.createdAt ?? '')
  )
  total.value = page.total ?? records.value.length
  historyLoaded.value = true
  error.value = ''
  emit('count', total.value)
}
async function refresh() {
  if (loading.value) return
  phase.value = { kind: 'refresh' }
  try {
    await readReceipts()
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    phase.value = undefined
  }
}
async function inspect(id: string) {
  if (loading.value) return
  phase.value = { kind: 'inspect', requestId: id }
  error.value = ''
  try {
    const pending = pendingRequests.value.find((intent) => intent.requestId === id)
    if (pending?.path === '/api/commands') {
      const committed = await reconcilePendingMutation(id)
      result.value = '已使用原请求内容核实，此次保存已提交。'
      receiptDetails.value = readableValue(committed)
      await Promise.all([refreshSnapshot(), readReceipts()])
      return
    }
    const receipt = await getReceipt(id)
    result.value = receipt.found
      ? '已找到已提交回执，此次请求已核对。'
      : '尚未找到已提交回执，可在原操作入口重试同一次操作。'
    receiptDetails.value = receipt.found ? readableValue(receipt.result) : ''
    if (receipt.found) {
      resolvePendingRequest(id)
      await acknowledgeRequest(id)
      await readReceipts()
    }
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    phase.value = undefined
  }
}
onMounted(refresh)
</script>
<template>
  <section v-if="pendingRequests.length || (!open && (error || result))" class="platform-section">
    <div v-if="pendingRequests.length || error" class="section-heading">
      <h2>需要核对的请求</h2>
      <Button type="button" variant="outline" :aria-disabled="loading" @click="refresh">
        <LoadingIndicator :active="phase?.kind === 'refresh' && !open" />
        刷新回执
      </Button>
    </div>
    <Alert v-if="error" class="notice warning">
      <AlertDescription class="text-current">{{ error }}</AlertDescription>
    </Alert>
    <article v-for="intent in pendingRequests" :key="intent.requestId" class="external-row">
      <div>
        <Badge variant="outline">{{ intent.status === 'uncertain' ? '结果待核对' : '正在提交' }}</Badge>
        <p class="path-text">{{ intent.path }}</p>
        <p v-if="intent.path === '/api/commands'">将使用原请求内容核实并完成此次保存。</p>
      </div>
      <Button
        type="button"
        variant="outline"
        :aria-disabled="loading"
        @click="inspect(intent.requestId)"
      >
        <LoadingIndicator :active="phase?.kind === 'inspect' && phase.requestId === intent.requestId && !open" />
        {{ intent.path === '/api/commands' ? '用原请求核实' : '核对回执' }}
      </Button>
    </article>
    <Alert v-if="result" role="status" class="notice">
      <AlertDescription class="text-current">{{ result }}</AlertDescription>
    </Alert>
    <Button v-if="receiptDetails" variant="ghost" @click="open = true">查看完整回执</Button>
  </section>
  <ModalDialog v-model="open" title="请求历史">
    <div v-if="open">
      <div class="section-heading">
        <span class="muted"
          >{{ total }} 条请求<span v-if="total > records.length">
            · 已列出 {{ records.length }} 条</span
          ></span
        >
        <Button type="button" variant="outline" :aria-disabled="loading" @click="refresh">刷新历史</Button>
      </div>
      <Alert v-if="error" class="notice warning">
        <AlertDescription class="text-current">{{ error }}</AlertDescription>
      </Alert>
      <Alert v-if="result" role="status" class="notice">
        <AlertDescription class="text-current">{{ result }}</AlertDescription>
      </Alert>
      <JobDisclosure v-if="receiptDetails">
        <template #label>查看完整回执</template>
        <pre>{{ receiptDetails }}</pre>
      </JobDisclosure>
      <LoadingSkeleton v-if="!historyLoaded && loading" variant="table" :rows="3" label="正在读取请求记录" />
      <div v-else-if="historyLoaded" class="receipt-result">
        <div class="table-scroll receipts" tabindex="0" role="region" aria-label="请求回执列表" :aria-busy="loading">
          <Table class="spec-table">
            <TableCaption class="sr-only">
              本机请求及保存回执
            </TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">请求</TableHead>
                <TableHead scope="col">状态</TableHead>
                <TableHead v-if="dated" scope="col">最近更新</TableHead>
                <TableHead scope="col">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="record in records" :key="record.requestId">
                <TableHead scope="row">
                  {{ record.kind === 'mutation' ? '保存操作' : (record.kind ?? '本机操作') }}
                  <small class="path-text">{{ record.requestId }}</small>
                </TableHead>
                <TableCell><Badge variant="secondary">{{ jobStatusLabels[record.status ?? ''] ?? record.status ?? '已记录' }}</Badge></TableCell>
                <TableCell v-if="dated">{{ dateLabel(record.updatedAt ?? record.createdAt ?? null) }}</TableCell>
                <TableCell>
                  <Button
                    type="button"
                    variant="ghost"
                    :aria-disabled="loading"
                    @click="inspect(record.requestId)"
                  >
                    <LoadingIndicator :active="phase?.kind === 'inspect' && phase.requestId === record.requestId" />
                    核对回执
                  </Button>
                  <RouterLink v-if="record.jobId" :to="`/jobs/${record.jobId}`">查看执行</RouterLink>
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
        <Empty v-if="!records.length && !error" class="p-4">
          <EmptyDescription>暂无请求记录</EmptyDescription>
        </Empty>
        <LoadingIndicator :active="phase?.kind === 'refresh'" placement="overlay" label="正在刷新请求历史" />
      </div>
    </div>
  </ModalDialog>
</template>
<style scoped>
button[aria-disabled='true'] {
  opacity: 0.65;
}
.receipts {
  max-height: 60vh;
  overflow: auto;
  margin-top: 12px;
}
.receipt-result {
  position: relative;
}
.receipts th small {
  display: block;
  font-size: 11px;
  font-weight: 400;
  color: var(--brand-color-text-secondary);
}
</style>
