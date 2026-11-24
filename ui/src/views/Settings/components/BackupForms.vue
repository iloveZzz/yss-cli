<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import { Card } from '@/components/ui/card'
import { Alert } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { shallowRef } from 'vue'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { createBackup, restoreBackup } from '@/services/workbench-platform'
import { errorMessage } from '@/services/workbench'
const task = shallowRef<'backup' | 'restore'>('backup')
const archiveTarget = shallowRef('')
const archivePath = shallowRef('')
const restoreTarget = shallowRef('')
const busy = shallowRef(false)
const error = shallowRef('')
const jobId = shallowRef('')
function selectTask(value: string | number) {
  if (!busy.value && (value === 'backup' || value === 'restore')) task.value = value
}
async function backup() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    jobId.value = (await createBackup(archiveTarget.value)).id
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
async function restore() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    jobId.value = (await restoreBackup(archivePath.value, restoreTarget.value)).id
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <Card class="platform-section block gap-0 py-0 shadow-none">
    <h2>备份与恢复</h2>
    <Tabs :model-value="task" :unmount-on-hide="false" activation-mode="manual" @update:model-value="selectTask">
      <TabsList class="backup-actions" aria-label="备份操作">
        <TabsTrigger value="backup" :disabled="busy">创建备份</TabsTrigger>
        <TabsTrigger value="restore" :disabled="busy">从归档恢复</TabsTrigger>
      </TabsList>
      <Alert v-if="error" class="notice error" variant="destructive">{{ error }}</Alert>
      <Alert v-if="jobId" role="status" class="notice">
        <div>操作已开始，<RouterLink :to="`/jobs/${jobId}`" class="text-link">查看执行结果</RouterLink>。</div>
      </Alert>
      <TabsContent value="backup">
        <form class="form-stack" aria-label="创建备份" @submit.prevent="backup">
          <fieldset :disabled="busy" class="plain-fieldset form-stack">
            <legend class="sr-only">备份归档位置</legend>
            <Field class="gap-1.5">
              <FieldLabel for="backup-target">备份归档绝对路径（必填）</FieldLabel>
              <Input
                id="backup-target"
                v-model="archiveTarget"
                name="targetPath"
                required
                spellcheck="false"
                autocorrect="off"
                autocapitalize="none"
                placeholder="/absolute/path/workbench-backup.tar.gz"
                aria-describedby="backup-target-help"
              />
              <FieldDescription id="backup-target-help">
                导出当前个人数据的一致归档，完成后从执行记录核对保存位置。
              </FieldDescription>
            </Field>
            <div class="form-actions">
              <Button type="submit" :disabled="busy">
                <LoadingIndicator :active="busy && task === 'backup'" />
                {{ busy ? '正在安排…' : '创建一致备份' }}
              </Button>
            </div>
          </fieldset>
        </form>
      </TabsContent>
      <TabsContent value="restore">
        <form class="form-stack" aria-label="恢复备份" @submit.prevent="restore">
          <fieldset :disabled="busy" class="plain-fieldset form-stack">
            <legend class="sr-only">恢复来源与目标</legend>
            <Field class="gap-1.5">
              <FieldLabel for="restore-archive">备份归档绝对路径（必填）</FieldLabel>
              <Input
                id="restore-archive"
                v-model="archivePath"
                name="archivePath"
                required
                spellcheck="false"
                autocorrect="off"
                autocapitalize="none"
                placeholder="/absolute/path/workbench-backup.tar.gz"
              />
            </Field>
            <Field class="gap-1.5">
              <FieldLabel for="restore-target">新的或空的数据目录（必填）</FieldLabel>
              <Input
                id="restore-target"
                v-model="restoreTarget"
                name="targetPath"
                required
                spellcheck="false"
                autocorrect="off"
                autocapitalize="none"
                placeholder="/absolute/path/restored-workbench"
                aria-describedby="restore-target-help"
              />
              <FieldDescription id="restore-target-help">
                完成后按执行结果打开并核对工程路径。
              </FieldDescription>
            </Field>
            <div class="form-actions">
              <Button type="submit" :disabled="busy">
                <LoadingIndicator :active="busy && task === 'restore'" />
                {{ busy ? '正在安排…' : '校验并恢复备份' }}
              </Button>
            </div>
          </fieldset>
        </form>
      </TabsContent>
    </Tabs>
  </Card>
</template>
<style scoped>
.backup-actions {
  margin-top: 4px;
  height: auto;
}
.backup-actions :deep([data-slot='tabs-trigger']) {
  min-height: var(--yss-control-min-height);
  padding-inline: 12px;
}
</style>
