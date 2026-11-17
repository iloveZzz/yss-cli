<script setup lang="ts">
import { shallowRef } from 'vue'
import { Button } from '@/components/ui/button'
import { createBackup, restoreBackup } from '@/services/workbench-platform'
import { errorMessage } from '@/services/workbench'
const task = shallowRef<'backup' | 'restore'>('backup')
const archiveTarget = shallowRef('')
const archivePath = shallowRef('')
const restoreTarget = shallowRef('')
const busy = shallowRef(false)
const error = shallowRef('')
const jobId = shallowRef('')
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
  <section class="platform-section">
    <h2>备份与恢复</h2>
    <div class="inline-actions backup-actions" aria-label="备份操作">
      <Button
        type="button"
        :variant="task === 'backup' ? 'default' : 'outline'"
        :aria-pressed="task === 'backup'"
        :disabled="busy"
        @click="task = 'backup'"
        >创建备份</Button
      ><Button
        type="button"
        :variant="task === 'restore' ? 'default' : 'outline'"
        :aria-pressed="task === 'restore'"
        :disabled="busy"
        @click="task = 'restore'"
        >从归档恢复</Button
      >
    </div>
    <p v-if="error" role="alert" class="notice error">{{ error }}</p>
    <p v-if="jobId" role="status" class="notice">
      操作已开始，<RouterLink :to="`/jobs/${jobId}`">查看执行结果</RouterLink>。
    </p>
    <form
      v-show="task === 'backup'"
      class="form-stack"
      aria-label="创建备份"
      @submit.prevent="backup"
    >
      <p class="muted">导出当前个人数据的一致归档，完成后从执行记录核对保存位置。</p>
      <fieldset :disabled="busy" class="plain-fieldset form-stack">
        <legend class="sr-only">备份归档位置</legend>
        <label
          >备份归档绝对路径（必填）<input
            v-model="archiveTarget"
            name="targetPath"
            required
            spellcheck="false"
            autocorrect="off"
            autocapitalize="none"
            placeholder="/absolute/path/workbench-backup.tar.gz"
        /></label>
        <div class="form-actions">
          <Button :disabled="busy">{{ busy ? '正在安排…' : '创建一致备份' }}</Button>
        </div>
      </fieldset>
    </form>
    <form
      v-show="task === 'restore'"
      class="form-stack"
      aria-label="恢复备份"
      @submit.prevent="restore"
    >
      <p class="muted">恢复到新的或空的个人数据目录，完成后按执行结果打开并核对工程路径。</p>
      <fieldset :disabled="busy" class="plain-fieldset form-stack">
        <legend class="sr-only">恢复来源与目标</legend>
        <label
          >备份归档绝对路径（必填）<input
            v-model="archivePath"
            name="archivePath"
            required
            spellcheck="false"
            autocorrect="off"
            autocapitalize="none"
            placeholder="/absolute/path/workbench-backup.tar.gz" /></label
        ><label
          >新的或空的数据目录（必填）<input
            v-model="restoreTarget"
            name="targetPath"
            required
            spellcheck="false"
            autocorrect="off"
            autocapitalize="none"
            placeholder="/absolute/path/restored-workbench"
        /></label>
        <div class="form-actions">
          <Button :disabled="busy">{{ busy ? '正在安排…' : '校验并恢复备份' }}</Button>
        </div>
      </fieldset>
    </form>
  </section>
</template>
<style scoped>
.backup-actions {
  margin: 18px 0;
}
</style>
