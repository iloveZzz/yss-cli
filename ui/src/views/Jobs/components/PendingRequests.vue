<script setup lang="ts">
import { onMounted, shallowRef } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import { Button } from '@/components/ui/button'
import {
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
const { refresh: refreshSnapshot } = useWorkbench()
const records = shallowRef<RequestRecord[]>([])
const error = shallowRef('')
const result = shallowRef('')
const receiptDetails = shallowRef('')
const loading = shallowRef(false)
async function refresh() {
  loading.value = true
  try {
    records.value = (await getRequests()).items
    error.value = ''
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}
async function inspect(id: string) {
  loading.value = true
  try {
    const pending = pendingRequests.value.find((intent) => intent.requestId === id)
    if (pending?.path === '/api/commands') {
      const committed = await reconcilePendingMutation(id)
      result.value = '已使用原请求内容核实，此次保存已提交。'
      receiptDetails.value = readableValue(committed)
      await Promise.all([refreshSnapshot(), refresh()])
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
      await refresh()
    }
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}
onMounted(refresh)
</script>
<template>
  <section
    v-if="pendingRequests.length || records.length || error"
    :class="pendingRequests.length || error ? 'platform-section' : 'request-history-only'"
  >
    <div v-if="pendingRequests.length || error" class="section-heading">
      <h2>需要核对的请求</h2>
      <Button type="button" variant="outline" :disabled="loading" @click="refresh">刷新回执</Button>
    </div>
    <p v-if="error" role="alert" class="notice warning">{{ error }}</p>
    <article v-for="intent in pendingRequests" :key="intent.requestId" class="external-row">
      <div>
        <strong>{{ intent.status === 'uncertain' ? '结果待核对' : '正在提交' }}</strong>
        <p class="path-text">{{ intent.path }}</p>
        <p v-if="intent.path === '/api/commands'">将使用原请求内容核实并完成此次保存。</p>
      </div>
      <Button
        type="button"
        variant="outline"
        :disabled="loading"
        @click="inspect(intent.requestId)"
      >
        {{ intent.path === '/api/commands' ? '用原请求核实' : '核对回执' }}
      </Button>
    </article>
    <details v-if="records.length" class="request-history">
      <summary>请求历史（{{ records.length }}）</summary>
      <div class="history-actions">
        <Button type="button" variant="ghost" :disabled="loading" @click="refresh">刷新历史</Button>
      </div>
      <article v-for="record in records" :key="record.requestId" class="external-row">
        <div>
          <strong>{{ record.kind === 'mutation' ? '保存操作' : (record.kind ?? '本机操作') }} · {{ jobStatusLabels[record.status ?? ''] ?? record.status ?? '已记录' }}</strong>
          <p class="path-text">{{ record.requestId }}</p>
          <RouterLink v-if="record.jobId" :to="`/jobs/${record.jobId}`">查看执行记录</RouterLink>
        </div>
        <Button
          type="button"
          variant="ghost"
          :disabled="loading"
          @click="inspect(record.requestId)"
        >
          核对回执
        </Button>
      </article>
    </details>
    <p v-if="result" role="status" class="notice">{{ result }}</p>
    <details v-if="receiptDetails">
      <summary>查看完整回执</summary>
      <pre>{{ receiptDetails }}</pre>
    </details>
  </section>
</template>
<style scoped>
.request-history-only { margin: -8px 0 12px; color: var(--brand-color-text-secondary); font-size: 12px; }
.request-history-only > .request-history > summary { width: fit-content; margin-left: auto; min-height: 28px; cursor: pointer; padding: 4px 0; }
.request-history[open] { padding: 12px 16px; border: 1px solid var(--yss-colors-border); border-radius: 8px; background: var(--brand-color-bg-container); color: var(--brand-color-text); }
.request-history[open] > summary { margin-left: 0; }
.history-actions { display: flex; justify-content: flex-end; margin-top: 8px; }
</style>
