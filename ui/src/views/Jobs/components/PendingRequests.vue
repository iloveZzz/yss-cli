<script setup lang="ts">
import { computed, onMounted, shallowRef } from 'vue'
import { useWorkbench } from '@/hooks/useWorkbench'
import ModalDialog from '@/components/ModalDialog.vue'
import { Button } from '@/components/ui/button'
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
const loading = shallowRef(false)
async function refresh() {
  loading.value = true
  try {
    const page = await getRequests()
    records.value = page.items.sort((a, b) =>
      (b.updatedAt ?? b.createdAt ?? '').localeCompare(a.updatedAt ?? a.createdAt ?? '')
    )
    total.value = page.total ?? records.value.length
    emit('count', total.value)
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
  <section v-if="pendingRequests.length || (!open && (error || result))" class="platform-section">
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
    <p v-if="result" role="status" class="notice">{{ result }}</p>
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
        <Button type="button" variant="outline" :disabled="loading" @click="refresh"
          >刷新历史</Button
        >
      </div>
      <p v-if="error" role="alert" class="notice warning">{{ error }}</p>
      <p v-if="result" role="status" class="notice">{{ result }}</p>
      <details v-if="receiptDetails">
        <summary>查看完整回执</summary>
        <pre>{{ receiptDetails }}</pre>
      </details>
      <div class="table-scroll receipts" tabindex="0" role="region" aria-label="请求回执列表">
        <table class="spec-table">
          <caption class="sr-only">
            本机请求及保存回执
          </caption>
          <thead>
            <tr>
              <th scope="col">请求</th>
              <th scope="col">状态</th>
              <th v-if="dated" scope="col">最近更新</th>
              <th scope="col">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="record in records" :key="record.requestId">
              <th scope="row">
                {{ record.kind === 'mutation' ? '保存操作' : (record.kind ?? '本机操作') }}
                <small class="path-text">{{ record.requestId }}</small>
              </th>
              <td>{{ jobStatusLabels[record.status ?? ''] ?? record.status ?? '已记录' }}</td>
              <td v-if="dated">{{ dateLabel(record.updatedAt ?? record.createdAt ?? null) }}</td>
              <td>
                <Button
                  type="button"
                  variant="ghost"
                  :disabled="loading"
                  @click="inspect(record.requestId)"
                  >核对回执</Button
                >
                <RouterLink v-if="record.jobId" :to="`/jobs/${record.jobId}`">查看执行</RouterLink>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-if="!records.length && !loading && !error" class="empty">暂无请求记录</p>
      </div>
    </div>
  </ModalDialog>
</template>
<style scoped>
.receipts {
  max-height: 60vh;
  overflow: auto;
  margin-top: 12px;
}
.receipts th small {
  display: block;
  font-size: 11px;
  font-weight: 400;
  color: var(--brand-color-text-secondary);
}
</style>
