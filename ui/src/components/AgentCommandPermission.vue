<script setup lang="ts">
import { useId } from 'vue'

defineProps<{ disabled?: boolean }>()
const requested = defineModel<boolean>({ required: true })
const id = useId()
</script>

<template>
  <div>
    <label :for="id" class="check-label command-permission">
      <input
        :id="id"
        v-model="requested"
        type="checkbox"
        name="allowCommands"
        :aria-describedby="id + '-help'"
        :disabled="disabled"
      />
      <span>允许本次运行执行隔离命令</span>
    </label>
    <p :id="id + '-help'" class="muted">
      命令可在选定工作区内离线构建和测试，使用本机已准备的依赖。
    </p>
  </div>
</template>

<style scoped>
.command-permission {
  min-height: 44px;
}
</style>
