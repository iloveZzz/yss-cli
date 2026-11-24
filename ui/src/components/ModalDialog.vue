<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'

defineOptions({ inheritAttrs: false })
const props = defineProps<{ title: string; busy?: boolean; beforeClose?: () => boolean | Promise<boolean> }>()
const open = defineModel<boolean>({ required: true })
const checking = ref(false)
let invoker: HTMLElement | undefined
let openVersion = 0

watch(open, (value) => {
  openVersion++
  if (value) invoker = document.activeElement instanceof HTMLElement ? document.activeElement : undefined
}, { flush: 'sync' })

async function close() {
  if (!open.value || props.busy || checking.value) return
  const version = openVersion
  checking.value = true
  try {
    if ((!props.beforeClose || await props.beforeClose()) && openVersion === version && !props.busy) {
      open.value = false
    }
  } finally {
    checking.value = false
  }
}

function updateOpen(value: boolean) {
  if (!value) void close()
}

function handleEscape(event: KeyboardEvent) {
  event.preventDefault()
  void close()
}

function setInitialFocus(event: Event) {
  if (!(event.target instanceof HTMLElement)) return
  const field = event.target.querySelector<HTMLElement>('[autofocus]:not(:disabled)')
  if (!field) return
  event.preventDefault()
  field.focus()
}

async function restoreFocus(event: Event) {
  event.preventDefault()
  const trigger = invoker
  const version = openVersion
  await nextTick()
  if (!open.value && version === openVersion && trigger?.isConnected) trigger.focus({ preventScroll: true })
}
</script>

<template>
  <Dialog :open="open" @update:open="updateOpen">
    <DialogContent
      v-bind="$attrs"
      class="workbench-dialog"
      :show-close-button="false"
      :aria-describedby="undefined"
      @escape-key-down="handleEscape"
      @interact-outside.prevent
      @open-auto-focus="setInitialFocus"
      @close-auto-focus="restoreFocus"
    >
      <DialogHeader class="dialog-header">
        <DialogTitle as="h2">{{ title }}</DialogTitle>
        <div class="dialog-actions">
          <slot name="actions" />
          <Button type="button" variant="ghost" :disabled="busy || checking" aria-label="关闭对话框" @click="close">
            <X :size="16" aria-hidden="true" />
          </Button>
        </div>
      </DialogHeader>
      <slot />
    </DialogContent>
  </Dialog>
</template>

<style scoped>
.dialog-actions { display: flex; align-items: center; gap: 4px; flex-shrink: 0; }
</style>
