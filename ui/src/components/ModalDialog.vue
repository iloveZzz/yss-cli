<script setup lang="ts">
import { useId, useTemplateRef, watch } from 'vue'
import { Button } from '@/components/ui/button'
const props = defineProps<{ title: string; busy?: boolean; beforeClose?: () => boolean | Promise<boolean> }>()
const open = defineModel<boolean>({ required: true })
const dialog = useTemplateRef<HTMLDialogElement>('dialog')
const id = useId()
watch([open, dialog], () => { if (open.value && !dialog.value?.open) dialog.value?.showModal(); else if (!open.value) dialog.value?.close() })
async function close() { if (!props.busy && (!props.beforeClose || await props.beforeClose())) open.value = false }
</script>
<template><dialog ref="dialog" :aria-labelledby="id" @close="open = false" @cancel.prevent="close"><header class="dialog-header"><h2 :id="id">{{ title }}</h2><Button type="button" variant="ghost" :disabled="busy" aria-label="关闭对话框" @click="close">×</Button></header><slot /></dialog></template>
