<script setup lang="ts">
import { computed } from 'vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { Badge } from '@/components/ui/badge'
import { grokRuntimeLabel, runtimeIdentityLabel, type GrokRuntimeStatus } from '@/services/workbench-platform'

const props = defineProps<{ runtime?: GrokRuntimeStatus; checking?: boolean; error?: string; review?: string }>()
const label = computed(() => props.checking ? '正在检查连接' : props.review ? '运行配置待核对' : grokRuntimeLabel(props.runtime))
const details = computed(() => {
  const state = props.runtime
  return [state?.version, state?.executable, state?.model?.id, runtimeIdentityLabel(state?.identity)].filter(Boolean).join(' · ')
})
</script>

<template>
  <div class="grid gap-1 text-sm" role="status">
    <div><Badge variant="outline" :class="{ 'warning-text': !checking && (review || !runtime?.available) }">
      <LoadingIndicator :active="checking" />{{ label }}
    </Badge></div>
    <p v-if="details" class="break-all text-muted-foreground">{{ details }}</p>
    <p v-if="runtime?.message || error || review" class="break-words">{{ runtime?.message || error || review }}</p>
    <p v-if="runtime?.issue === 'resumeIdentityChanged'">切回原会话账号后重新检查连接。</p>
    <p v-if="runtime?.loginCommand" class="break-all">
      在本机终端完成登录后重新检查。<code class="block whitespace-pre-wrap">{{ runtime.loginCommand }}</code>
    </p>
  </div>
</template>
