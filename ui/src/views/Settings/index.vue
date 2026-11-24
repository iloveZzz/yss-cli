<script setup lang="ts">
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { Alert } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Field, FieldLabel } from '@/components/ui/field'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import EngineeringPanel from '@/components/EngineeringPanel.vue'
import ExternalLinks from '@/components/ExternalLinks.vue'
import BackupForms from './components/BackupForms.vue'
import { useSystemInfo } from './hooks/useSystemInfo'
import { byteLabel } from './schemas/system'
import { restartWorkbench, readableValue } from '@/services/workbench-platform'
const { info, loading, error, refresh } = useSystemInfo()
const route = useRoute()
const router = useRouter()
const groups = [
  { value: 'general', label: '本机与诊断' },
  { value: 'backup', label: '备份与恢复' },
  { value: 'engineering', label: '工程维护' },
  { value: 'external', label: '研发关联' }
]
const group = computed(() =>
  groups.some((item) => item.value === route.query.section)
    ? String(route.query.section)
    : route.query.operation
      ? 'engineering'
      : 'general'
)
function selectGroup(value: string | number) {
  if (typeof value === 'string' && value !== route.query.section)
    void router.push({ query: { ...route.query, section: value } })
}
const toolRoot = shallowRef('')
const restarting = shallowRef(false)
const restartAccepted = shallowRef(false)
const restartError = shallowRef('')
const restartNotice = shallowRef('')
async function restart() {
  if (restarting.value || restartAccepted.value) return
  restarting.value = true
  restartError.value = ''
  try {
    restartNotice.value = (await restartWorkbench(toolRoot.value)).message
    restartAccepted.value = true
  } catch (cause) {
    restartError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    restarting.value = false
  }
}
const storageLabels: Record<string, string> = {
  schemaVersion: '数据结构版本',
  blobBytes: '内容快照',
  recordBytes: '草稿与运行记录',
  eventBytes: '执行日志'
}
const measurements = computed(() => Object.entries(info.value?.storage ?? {}))
</script>
<template>
  <div class="page-heading">
    <h1>设置</h1>
    <Button variant="outline" :disabled="loading" @click="refresh">
      <LoadingIndicator :active="loading && !!info" />
      {{ loading ? '诊断中…' : '重新诊断' }}
    </Button>
  </div>
  <Tabs :model-value="group" :unmount-on-hide="false" activation-mode="manual" @update:model-value="selectGroup">
    <TabsList class="settings-navigation" aria-label="设置分组">
      <TabsTrigger v-for="item in groups" :key="item.value" :value="item.value">{{ item.label }}</TabsTrigger>
    </TabsList>
    <Alert v-if="error" class="notice error" role="alert" variant="destructive">{{ error }}</Alert>
    <TabsContent value="general" class="min-w-0">
      <Card v-if="info" class="platform-section block gap-0 py-0 shadow-none" :aria-busy="loading">
        <div class="section-heading">
          <h2>本机工作台</h2>
          <Badge variant="outline">YSS {{ info.version.version }}</Badge>
        </div>
        <div class="table-scroll" tabindex="0" role="region" aria-label="本机运行信息">
          <Table class="spec-table settings-facts">
            <TableCaption class="sr-only">
              本机工作台运行与存储信息
            </TableCaption>
            <TableBody>
              <TableRow>
                <TableHead scope="row">运行平台</TableHead>
                <TableCell>{{ info.version.target }}</TableCell>
              </TableRow>
              <TableRow>
                <TableHead scope="row">数据目录</TableHead>
                <TableCell class="path-text">{{ info.dataDir }}</TableCell>
              </TableRow>
              <TableRow>
                <TableHead scope="row">运行程序</TableHead>
                <TableCell class="path-text">{{ info.executable }}</TableCell>
              </TableRow>
              <TableRow>
                <TableHead scope="row">数据库</TableHead>
                <TableCell>{{ byteLabel(info.databaseBytes) }}</TableCell>
              </TableRow>
              <TableRow v-for="[key, value] in measurements" :key="key">
                <TableHead scope="row">{{ storageLabels[key] ?? key }}</TableHead>
                <TableCell>
                  {{
                    typeof value === 'number' && /bytes|size/i.test(key)
                      ? byteLabel(value)
                      : readableValue(value)
                  }}
                </TableCell>
              </TableRow>
              <TableRow>
                <TableHead scope="row">模板 Bundle</TableHead>
                <TableCell class="path-text">{{ info.version.bundleDigest }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
        <h3 class="tools-heading">本机工具</h3>
        <div class="table-scroll" tabindex="0" role="region" aria-label="本机工具状态">
          <Table class="spec-table tools-table">
            <TableCaption class="sr-only">
              Git、Pi Agent 与远端平台工具
            </TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">工具</TableHead>
                <TableHead scope="col">状态</TableHead>
                <TableHead scope="col">版本或处理提示</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-if="info.pi">
                <TableHead scope="row">Pi Agent</TableHead>
                <TableCell>
                  <Badge variant="outline" :class="{ 'warning-text': !info.pi.available }">
                    {{ info.pi.available ? '本机检查通过' : '运行不可用' }}
                  </Badge>
                </TableCell>
                <TableCell>
                  {{ [
                    info.pi.version,
                    info.pi.model && `${info.pi.model.provider} / ${info.pi.model.id}`,
                    info.pi.sandboxReady && '命令沙箱就绪',
                    info.pi.message
                  ].filter(Boolean).join(' · ') }}
                </TableCell>
              </TableRow>
              <TableRow v-for="tool in info.tools" :key="tool.name">
                <TableHead scope="row">{{ tool.name }}</TableHead>
                <TableCell>
                  <Badge variant="outline" :class="{ 'warning-text': !tool.available }">
                    {{ tool.available ? '可用' : '不可用' }}
                  </Badge>
                </TableCell>
                <TableCell>{{ tool.version || tool.message || '请核对本机安装与认证' }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
        <Collapsible class="advanced-settings" :unmount-on-hide="false">
          <CollapsibleTrigger>程序重启</CollapsibleTrigger>
          <CollapsibleContent>
            <form class="form-stack" aria-label="重启工作台" @submit.prevent="restart">
              <Field class="gap-1.5">
                <FieldLabel for="tool-root">新版工具目录（可选）</FieldLabel>
                <Input
                  id="tool-root"
                  v-model="toolRoot"
                  name="toolRoot"
                  spellcheck="false"
                  autocorrect="off"
                  autocapitalize="none"
                  placeholder="沿用当前运行程序"
                  :disabled="restarting || restartAccepted"
                />
              </Field>
              <p class="muted">活动作业结束后使用新会话重新打开，已保存的编辑草稿可恢复。</p>
              <Alert v-if="restartError" variant="destructive" class="notice error">{{ restartError }}</Alert>
              <Alert v-else-if="restartNotice" role="status" class="notice">{{ restartNotice }}</Alert>
              <div class="form-actions">
                <Button type="submit" variant="outline" :disabled="restarting || restartAccepted">
                  <LoadingIndicator :active="restarting" />
                  {{ restarting ? '正在安排重启…' : restartAccepted ? '已安排重启' : '重启工作台' }}
                </Button>
              </div>
            </form>
          </CollapsibleContent>
        </Collapsible>
        <Collapsible>
          <CollapsibleTrigger>原始诊断数据</CollapsibleTrigger>
          <CollapsibleContent>
            <pre class="diagnostic-raw">{{ readableValue(info) }}</pre>
          </CollapsibleContent>
        </Collapsible>
      </Card>
      <LoadingSkeleton
        v-else-if="loading"
        class="platform-section"
        variant="table"
        :rows="6"
        label="正在读取本机信息"
      />
    </TabsContent>
    <TabsContent value="backup" class="min-w-0"><BackupForms /></TabsContent>
    <TabsContent value="engineering" class="min-w-0"><EngineeringPanel /></TabsContent>
    <TabsContent value="external" class="min-w-0"><ExternalLinks /></TabsContent>
  </Tabs>
</template>
<style scoped>
.settings-navigation {
  display: flex;
  justify-content: flex-start;
  width: 100%;
  height: auto;
  min-width: 0;
  padding: 4px;
  gap: 4px;
  overflow-x: auto;
  overscroll-behavior-x: contain;
  margin-bottom: 12px;
}
.settings-navigation :deep([data-slot='tabs-trigger']) {
  flex: 0 0 auto;
  min-height: var(--yss-control-min-height);
  padding-inline: 12px;
}
.settings-facts :deep(th) {
  width: 150px;
  white-space: nowrap;
  font-weight: 500;
}
.settings-facts :deep(td) {
  white-space: normal;
  overflow-wrap: anywhere;
}
@media (width < 720px) {
  .settings-facts :deep(th) {
    width: 105px;
    white-space: normal;
  }
}
.tools-heading {
  margin: 24px 0 10px;
}
.tools-table {
  min-width: 430px;
}
.tools-table :deep(td:last-child) {
  white-space: normal;
}
.advanced-settings {
  margin-top: 20px;
  border-top: 1px solid var(--yss-colors-border);
  padding-top: 8px;
}
.diagnostic-raw {
  padding: 14px;
  background: var(--brand-color-bg-layout);
  border-radius: 8px;
  max-height: 36rem;
  overflow: auto;
  overscroll-behavior: contain;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
