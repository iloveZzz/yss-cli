<script setup lang="ts" generic="T extends object">
import { Button } from '@/components/ui/button'
import { Alert } from '@/components/ui/alert'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { ChevronDown } from '@lucide/vue'
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
    <Alert v-if="error" variant="destructive" class="notice error">{{ error }}</Alert>
    <Collapsible v-if="drafts.length">
      <CollapsibleTrigger as-child>
        <Button class="draft-trigger" type="button" variant="ghost">
          <ChevronDown :size="14" aria-hidden="true" />
          可恢复草稿（{{ drafts.length }}）
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div v-for="draft in drafts" :key="draft.id" class="draft-choice">
          <span>{{ dateLabel(draft.updatedAt) }}</span>
          <Button type="button" variant="outline" @click="$emit('restore', draft)">
            恢复这份草稿
          </Button>
          <Button type="button" variant="ghost" @click="$emit('discard', draft)">移除</Button>
        </div>
      </CollapsibleContent>
    </Collapsible>
  </div>
</template>
<style scoped>
.draft-notice {
  font-size: 12px;
}
.draft-notice p {
  margin: 6px 0;
}
.draft-trigger { color: #315dcf; }
.draft-trigger[data-state='open'] svg { transform: rotate(180deg); }
.draft-choice {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  padding: 10px 0;
}
</style>
