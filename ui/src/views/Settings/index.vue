<script setup lang="ts">
import GrokReadiness from '@/components/GrokReadiness.vue'
import LoadingIndicator from '@/components/LoadingIndicator.vue'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import WorkbenchBreadcrumb from '@/components/WorkbenchBreadcrumb.vue'
import { Alert } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell, TableCaption } from '@/components/ui/table'
import { computed, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Field, FieldLabel } from '@/components/ui/field'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import EngineeringPanel from '@/components/EngineeringPanel.vue'
import ExternalLinks from '@/components/ExternalLinks.vue'
import { registerWorkbenchRouteReads } from '@/hooks/useWorkbenchReads'
import BackupForms from './components/BackupForms.vue'
import { useSystemInfo } from './hooks/useSystemInfo'
import { byteLabel } from './schemas/system'
import { getGrokRuntime } from '@/services/workbench-agent'
import { errorMessage } from '@/services/workbench'
import { piCapabilityLabels, piCommandCapability, piRuntimeAvailable, restartWorkbench, readableValue, type GrokRuntimeStatus } from '@/services/workbench-platform'
const { info, readAt, loading, error, refresh } = useSystemInfo()
registerWorkbenchRouteReads({ busy: loading, readAt, refresh })
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
const grokRuntime = shallowRef<GrokRuntimeStatus>()
const grokChecking = shallowRef(false)
const grokError = shallowRef('')
const grokRecheck = shallowRef(0)
watch([group, grokRecheck], async ([current], _, onCleanup) => {
  if (current !== 'general') return
  const request = new AbortController()
  onCleanup(() => request.abort())
  grokChecking.value = true
  grokError.value = ''
  try {
    const result = await getGrokRuntime({}, request.signal)
    if (!request.signal.aborted) grokRuntime.value = result
  } catch (cause) {
    if (!request.signal.aborted) {
      grokRuntime.value = undefined
      grokError.value = errorMessage(cause)
    }
  } finally {
    if (!request.signal.aborted) grokChecking.value = false
  }
}, { immediate: true })
function refreshTools() {
  grokRecheck.value++
  void refresh()
}
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
const piCapabilities = computed(() => {
  const runtime = info.value?.pi
  return runtime ? [
    { label: '沙箱离线命令', ...piCommandCapability(runtime, 'sandbox') },
    { label: '本机命令', ...piCommandCapability(runtime, 'host') }
  ] : []
})
const desktopSettingsUrl = (window as Window & { __YSS_DESKTOP_SETTINGS_URL__?: string }).__YSS_DESKTOP_SETTINGS_URL__
function installUrl(tool: 'git' | 'gh' | 'glab' | 'pi' | 'missing') {
  if (!desktopSettingsUrl) return undefined
  const url = new URL(desktopSettingsUrl)
  url.searchParams.set('tools', tool)
  return url.href
}
function installableTool(name: string): 'git' | 'gh' | 'glab' | undefined {
  const id = name.toLowerCase()
  return id === 'git' || id === 'gh' || id === 'glab' ? id : undefined
}
</script>
<template>
  <div class="page-heading">
    <WorkbenchBreadcrumb title="本机设置" />
    <Button variant="outline" :disabled="loading" @click="refreshTools">
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
        <div class="tools-heading">
          <h3>本机工具</h3>
          <Button v-if="desktopSettingsUrl" as-child variant="outline" size="sm">
            <a :href="installUrl('missing')">安装缺失工具</a>
          </Button>
        </div>
        <p v-if="!desktopSettingsUrl" class="muted tools-help">请在本机安装工具后重新诊断；桌面版可在本机设置中安装。</p>
        <div class="table-scroll" tabindex="0" role="region" aria-label="本机工具状态">
          <Table class="spec-table tools-table">
            <TableCaption class="sr-only">
              Git、Pi、Grok Build 与远端平台工具
            </TableCaption>
            <TableHeader>
              <TableRow>
                <TableHead scope="col">工具</TableHead>
                <TableHead scope="col">状态</TableHead>
                <TableHead scope="col">版本或处理提示</TableHead>
                <TableHead v-if="desktopSettingsUrl" scope="col">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-if="info.pi">
                <TableHead scope="row">Pi Agent</TableHead>
                <TableCell>
                  <Badge variant="outline" :class="{ 'warning-text': !piRuntimeAvailable(info.pi) }">
                    基础作业{{ piCapabilityLabels[info.pi.capabilities?.pi.state ?? (info.pi.available ? 'available' : 'blocked')] }}
                  </Badge>
                </TableCell>
                <TableCell>
                  <p v-if="info.pi.version || info.pi.executable || info.pi.model" class="path-text">
                    {{ [info.pi.version ?? (info.pi.executable ? '版本未知' : undefined), info.pi.executable, info.pi.model && `${info.pi.model.provider} / ${info.pi.model.id}`].filter(Boolean).join(' · ') }}
                  </p>
                  <p v-if="info.pi.capabilities?.pi.reason || info.pi.message">{{ info.pi.capabilities?.pi.reason || info.pi.message }}</p>
                  <dl v-if="piCapabilities.length" class="grid gap-1">
                    <div v-for="capability in piCapabilities" :key="capability.label">
                      <dt class="inline">{{ capability.label }}</dt>
                      <dd class="ml-2 inline" :class="{ 'warning-text': capability.state === 'blocked' }">
                        {{ piCapabilityLabels[capability.state] }}<template v-if="capability.reason"> · {{ capability.reason }}</template>
                      </dd>
                    </div>
                  </dl>
                </TableCell>
                <TableCell v-if="desktopSettingsUrl">
                  <Button v-if="!info.pi.executable" as-child variant="outline" size="sm">
                    <a :href="installUrl('pi')">安装</a>
                  </Button>
                </TableCell>
              </TableRow>
              <TableRow>
                <TableHead scope="row">Grok Build</TableHead>
                <TableCell :colspan="desktopSettingsUrl ? 3 : 2">
                  <GrokReadiness :runtime="grokRuntime" :checking="grokChecking" :error="grokError" />
                  <Button type="button" variant="outline" size="sm" class="mt-1.5"
                    :disabled="grokChecking" @click="grokRecheck++">重新检查 Grok Build</Button>
                </TableCell>
              </TableRow>
              <TableRow v-for="tool in info.tools" :key="tool.name">
                <TableHead scope="row">{{ tool.name }}</TableHead>
                <TableCell>
                  <Badge variant="outline" :class="{ 'warning-text': !tool.available }">
                    {{ tool.available ? '可用' : '不可用' }}
                  </Badge>
                </TableCell>
                <TableCell>{{ [tool.version || tool.message || '请核对本机安装', tool.path].filter(Boolean).join(' · ') }}</TableCell>
                <TableCell v-if="desktopSettingsUrl">
                  <Button v-if="!tool.available && !tool.path && installableTool(tool.name)" as-child variant="outline" size="sm">
                    <a :href="installUrl(installableTool(tool.name)!)">安装</a>
                  </Button>
                </TableCell>
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
    <TabsContent value="engineering" class="min-w-0"><EngineeringPanel :root-path="typeof route.query.root === 'string' ? route.query.root : undefined" /></TabsContent>
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
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}
.tools-help { margin: 0 0 10px; }
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
