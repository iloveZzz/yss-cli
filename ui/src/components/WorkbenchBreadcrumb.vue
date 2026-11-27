<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import {
  Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator
} from '@/components/ui/breadcrumb'

interface BreadcrumbAncestor {
  label: string
  to: string
}

const props = withDefaults(defineProps<{
  title: string
  ancestors?: BreadcrumbAncestor[]
  home?: string | false
}>(), {
  ancestors: () => [],
  home: '/workbench'
})
const items = computed(() => props.home
  ? [{ label: '工作台', to: props.home }, ...props.ancestors]
  : props.ancestors
)
const heading = useTemplateRef<HTMLHeadingElement>('heading')

defineExpose({
  focus: (options?: FocusOptions) => heading.value?.focus(options)
})
</script>

<template>
  <Breadcrumb aria-label="面包屑导航" class="min-w-0">
    <BreadcrumbList class="breadcrumb-list">
      <template v-for="item in items" :key="item.to">
        <BreadcrumbItem class="min-w-0">
          <BreadcrumbLink as-child class="hover:underline focus-visible:underline">
            <RouterLink :to="item.to">{{ item.label }}</RouterLink>
          </BreadcrumbLink>
        </BreadcrumbItem>
        <BreadcrumbSeparator class="breadcrumb-separator">
          <span class="breadcrumb-slash" />
        </BreadcrumbSeparator>
      </template>
      <BreadcrumbItem class="min-w-0">
        <h1 ref="heading" tabindex="-1" class="breadcrumb-heading">
          <BreadcrumbPage class="breadcrumb-current">{{ title }}</BreadcrumbPage>
        </h1>
      </BreadcrumbItem>
    </BreadcrumbList>
  </Breadcrumb>
</template>

<style scoped>
.breadcrumb-list {
  gap: 3px 10px;
  font-size: var(--workbench-nav-font-size);
  line-height: 1.4;
  overflow-wrap: anywhere;
}
.breadcrumb-separator { color: var(--yss-colors-border-control); }
.breadcrumb-slash::before { content: '/' / ''; }
.breadcrumb-heading { font: inherit; }
.breadcrumb-current {
  color: var(--yss-colors-text-title);
  font-weight: 650;
}
</style>
