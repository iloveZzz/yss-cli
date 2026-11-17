<script setup lang="ts" generic="T extends object">
import { Button } from '@/components/ui/button'
import { dateLabel } from '@/services/workbench'
import ConflictResolver from '@/components/ConflictResolver.vue'
import { objectValue, type DraftRecord, type JsonObject } from '@/services/workbench-platform'
defineProps<{
  status: string
  error: string
  drafts: DraftRecord<T>[]
  conflict?: { base: T; local: T; current: T }
}>()
const emit = defineEmits<{
  restore: [value: DraftRecord<T>]
  discard: [value: DraftRecord<T>]
  merge: [value: T]
  cancelRecovery: []
}>()
function merge(value: JsonObject) {
  emit('merge', value as T)
}
</script>
<template>
  <div class="draft-notice">
    <ConflictResolver
      v-if="conflict"
      :base="objectValue(conflict.base)"
      :local="objectValue(conflict.local)"
      :current="objectValue(conflict.current)"
      @resolve="merge"
      @close="emit('cancelRecovery')"
    />
    <p v-if="status" role="status" class="muted">{{ status }}</p>
    <p v-if="error" role="alert" class="warning-text">{{ error }}</p>
    <details v-if="drafts.length">
      <summary>可恢复草稿（{{ drafts.length }}）</summary>
      <div v-for="draft in drafts" :key="draft.id" class="draft-choice">
        <span>{{ dateLabel(draft.updatedAt) }}</span>
        <Button type="button" variant="outline" @click="$emit('restore', draft)">
          恢复这份草稿
        </Button>
        <Button type="button" variant="ghost" @click="$emit('discard', draft)">移除</Button>
      </div>
    </details>
  </div>
</template>
<style scoped>
.draft-notice {
  font-size: 12px;
}
.draft-notice p {
  margin: 6px 0;
}
.draft-notice summary {
  cursor: pointer;
  color: #315dcf;
}
.draft-choice {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  padding: 10px 0;
}
</style>
