<script setup lang="ts">
import { useId } from 'vue'
import { Button } from '@/components/ui/button'
import SingleSelect from '@/components/SingleSelect.vue'
import { Input } from '@/components/ui/input'
import { Field, FieldDescription, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import type { ResourceRef } from '@/services/workbench'
const resources = defineModel<ResourceRef[]>({ required: true })
const id = useId()
</script>
<template>
  <div class="section-heading">
    <h3>关联资源</h3>
    <Button
      type="button"
      variant="outline"
      @click="
        resources = [...resources, { kind: 'other', label: '', targetType: 'local', target: '' }]
      "
    >
      ＋ 添加
    </Button>
  </div>
  <FieldSet v-for="(resource, i) in resources" :key="i" class="resource-editor">
    <FieldLegend class="text-xs">资源 {{ i + 1 }}</FieldLegend>
    <div class="form-grid">
      <Field class="gap-1.5">
        <FieldLabel :for="`${id}-${i}-kind`">类型</FieldLabel>
        <SingleSelect
          :id="`${id}-${i}-kind`"
          :model-value="resource.kind"
          :options="[
            { value: 'supplement', label: '规格补充' },
            { value: 'prototype', label: '原型' },
            { value: 'interaction', label: '交互说明' },
            { value: 'other', label: '其他' }
          ]"
          :aria-label="`资源 ${i + 1} 类型`"
          @update:model-value="resource.kind = $event as ResourceRef['kind']"
        />
      </Field>
      <Field class="gap-1.5">
        <FieldLabel :for="`${id}-${i}-target-type`">位置</FieldLabel>
        <SingleSelect
          :id="`${id}-${i}-target-type`"
          :model-value="resource.targetType"
          :options="[
            { value: 'local', label: '项目内文件' },
            { value: 'web', label: 'HTTP(S) 链接' }
          ]"
          :aria-label="`资源 ${i + 1} 位置`"
          @update:model-value="resource.targetType = $event as ResourceRef['targetType']"
        />
      </Field>
    </div>
    <Field class="gap-1.5">
      <FieldLabel :for="`${id}-${i}-label`">资源名称</FieldLabel>
      <Input :id="`${id}-${i}-label`" v-model="resource.label" required />
    </Field>
    <Field class="gap-1.5">
      <FieldLabel :for="`${id}-${i}-target`">{{ resource.targetType === 'local' ? '项目内相对路径' : '链接地址' }}</FieldLabel>
      <Input :id="`${id}-${i}-target`" v-model="resource.target" required spellcheck="false" />
    </Field>
    <Button
      type="button"
      variant="ghost"
      @click="resources = resources.filter((_, index) => index !== i)"
    >
      移除资源 {{ i + 1 }}
    </Button>
  </FieldSet>
  <FieldDescription>项目内文件使用相对路径。外部链接由你点击打开，工作台不主动探测。</FieldDescription>
</template>
