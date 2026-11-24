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
      <FieldLabel :for="id">允许本次运行执行隔离命令</FieldLabel>
      <FieldDescription :id="id + '-help'" class="mt-1 text-xs">
        命令可在选定工作区内离线构建和测试，使用本机已准备的依赖。
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
