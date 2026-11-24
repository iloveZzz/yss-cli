<script setup lang="ts">
import { statuses, type Status } from '@/services/workbench'
import { Badge } from '@/components/ui/badge'
defineProps<{ status: Status }>()
</script>
<template><Badge variant="outline" class="status-badge" :data-status="status"><span aria-hidden="true" class="status-dot" />{{ statuses[status] }}</Badge></template>
