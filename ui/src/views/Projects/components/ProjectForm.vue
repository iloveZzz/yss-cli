<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { Alert } from '@/components/ui/alert'
import { reactive, shallowRef, useId, watch } from 'vue'
import { useRouter } from 'vue-router'
import ModalDialog from '@/components/ModalDialog.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { useWorkbench } from '@/hooks/useWorkbench'
import { errorMessage, type ProjectRecord } from '@/services/workbench'
const props = defineProps<{ project?: ProjectRecord }>()
const open = defineModel<boolean>({ required: true })
const router = useRouter()
const id = useId()
const { snapshot, submit, saving } = useWorkbench()
const form = reactive({ name: '', rootPath: '', active: true })
const error = shallowRef('')
const submitting = shallowRef(false)
let revision = 0
watch(open, (value) => {
  if (value) {
    Object.assign(form, {
      name: props.project?.name ?? '',
      rootPath: props.project?.rootPath ?? '',
      active: props.project?.active ?? true
    })
    revision = snapshot.value?.revision ?? 0
    error.value = ''
  }
})
async function save() {
  if (submitting.value || saving.value) return
  submitting.value = true
  error.value = ''
  try {
    const result = await submit(
      props.project
        ? { kind: 'updateProject', projectId: props.project.id, ...form }
        : {
            kind: 'addProject',
            rootPath: form.rootPath,
            ...(form.name.trim() ? { name: form.name } : {})
          },
      revision
    )
    open.value = false
    if (!props.project && result.projectId) await router.push(`/projects/${result.projectId}`)
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    submitting.value = false
  }
}
</script>
<template>
  <ModalDialog v-model="open" :title="project ? '编辑项目' : '登记本机项目'" :busy="submitting || saving">
    <form class="form-stack" @submit.prevent="save">
      <fieldset :disabled="submitting || saving" class="form-stack plain-fieldset">
        <legend class="sr-only">项目信息</legend>
        <Field class="gap-1.5">
          <FieldLabel :for="`${id}-name`">项目名称</FieldLabel>
          <Input
            :id="`${id}-name`"
            v-model="form.name"
            :required="!!project"
            placeholder="留空使用目录名称"
            autofocus
          />
        </Field>
        <Field class="gap-1.5">
          <FieldLabel :for="`${id}-path`">本机绝对路径</FieldLabel>
          <Input
            :id="`${id}-path`"
            v-model="form.rootPath"
            required
            placeholder="/Users/you/Projects/my-project"
            spellcheck="false"
            autocorrect="off"
            :aria-describedby="`${id}-path-help`"
          />
          <FieldDescription :id="`${id}-path-help`">
            登记本机目录后可关联 Spec、维护工程或参与隔离运行。目录移动后在这里重新定位。
          </FieldDescription>
        </Field>
        <FieldLabel v-if="project" :for="`${id}-active`" class="check-label has-data-[state=checked]:bg-transparent has-data-[state=checked]:border-transparent">
          <Checkbox :id="`${id}-active`" v-model="form.active" :disabled="submitting || saving" />
          启用项目（停用后保留全部个人记录）
        </FieldLabel>
        <Alert v-if="error" class="notice error" variant="destructive">{{ error }}</Alert>
        <div class="form-actions">
          <Button type="button" variant="outline" :disabled="submitting || saving" @click="open = false">取消</Button>
          <Button type="submit" :disabled="submitting || saving">
            <LoadingIndicator :active="submitting" />
            {{ submitting ? '保存中…' : '保存项目' }}
          </Button>
        </div>
      </fieldset>
    </form>
  </ModalDialog>
</template>
