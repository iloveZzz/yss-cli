<script setup lang="ts">
import { useId } from 'vue'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'

defineProps<{ disabled?: boolean }>()
const requested = defineModel<boolean>({ required: true })
const id = useId()
</script>

<template>
  <Field orientation="horizontal" class="command-permission gap-2.5">
    <Checkbox
      :id="id"
      :model-value="requested"
      name="allowCommands"
      :aria-describedby="id + '-help'"
      :disabled="disabled"
      @update:model-value="requested = $event === true"
    />
    <div>
      <FieldLabel :for="id">允许本轮执行离线命令</FieldLabel>
      <FieldDescription :id="id + '-help'" class="mt-1 text-xs">
        可在选定工程内离线构建和测试。命令结束或停止后，已写入的文件会保留。
      </FieldDescription>
    </div>
  </Field>
</template>

<style scoped>
.command-permission {
  min-height: 44px;
  align-items: flex-start;
}
.command-permission > [data-slot='checkbox'] {
  margin-top: 3px;
}
.command-permission label {
  cursor: pointer;
}
</style>
