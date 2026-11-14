<script setup lang="ts">
import { statuses, type Status } from '@/services/workbench'
defineProps<{ status: Status }>()
</script>
<template><span class="status-badge" :data-status="status"><span aria-hidden="true" class="status-dot" />{{ statuses[status] }}</span></template>
