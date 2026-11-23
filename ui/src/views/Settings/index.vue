<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import EngineeringPanel from '@/components/EngineeringPanel.vue'
import ExternalLinks from '@/components/ExternalLinks.vue'
import BackupForms from './components/BackupForms.vue'
import { useSystemInfo } from './hooks/useSystemInfo'
import { byteLabel } from './schemas/system'
import { restartWorkbench, readableValue } from '@/services/workbench-platform'
const { info, loading, error, refresh } = useSystemInfo()
const route = useRoute()
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
const toolRoot = shallowRef('')
const restarting = shallowRef(false)
const restartNotice = shallowRef('')
async function restart() {
  if (restarting.value) return
  restarting.value = true
  try {
    restartNotice.value = (await restartWorkbench(toolRoot.value)).message
  } catch (cause) {
    restartNotice.value = cause instanceof Error ? cause.message : String(cause)
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
    <Button variant="outline" :disabled="loading" @click="refresh">{{
      loading ? '诊断中…' : '重新诊断'
    }}</Button>
  </div>
  <nav class="settings-navigation" aria-label="设置分组">
    <RouterLink
      v-for="item in groups"
      :key="item.value"
      :to="{ query: { ...route.query, section: item.value } }"
      :aria-current="group === item.value ? 'page' : undefined"
      >{{ item.label }}</RouterLink
    >
  </nav>
  <p v-if="error" role="alert" class="notice error">{{ error }}</p>
  <section v-show="group === 'general'" aria-label="本机与诊断">
    <section v-if="info" class="platform-section">
      <div class="section-heading">
        <h2>本机工作台</h2>
        <span class="muted">YSS {{ info.version.version }}</span>
      </div>
      <div class="table-scroll" tabindex="0" role="region" aria-label="本机运行信息">
        <table class="spec-table settings-facts">
          <caption class="sr-only">
            本机工作台运行与存储信息
          </caption>
          <tbody>
            <tr>
              <th scope="row">运行平台</th>
              <td>{{ info.version.target }}</td>
            </tr>
            <tr>
              <th scope="row">数据目录</th>
              <td class="path-text">{{ info.dataDir }}</td>
            </tr>
            <tr>
              <th scope="row">运行程序</th>
              <td class="path-text">{{ info.executable }}</td>
            </tr>
            <tr>
              <th scope="row">数据库</th>
              <td>{{ byteLabel(info.databaseBytes) }}</td>
            </tr>
            <tr v-for="[key, value] in measurements" :key="key">
              <th scope="row">{{ storageLabels[key] ?? key }}</th>
              <td>
                {{
                  typeof value === 'number' && /bytes|size/i.test(key)
                    ? byteLabel(value)
                    : readableValue(value)
                }}
              </td>
            </tr>
            <tr>
              <th scope="row">模板 Bundle</th>
              <td class="path-text">{{ info.version.bundleDigest }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <h3 class="tools-heading">本机工具</h3>
      <div class="table-scroll" tabindex="0" role="region" aria-label="本机工具状态">
        <table class="spec-table tools-table">
          <caption class="sr-only">
            Git、Pi Agent 与远端平台工具
          </caption>
          <thead>
            <tr>
              <th scope="col">工具</th>
              <th scope="col">状态</th>
              <th scope="col">版本或处理提示</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="info.pi">
              <th scope="row">Pi Agent</th>
              <td :class="info.pi.available ? 'muted' : 'warning-text'">
                {{ info.pi.available ? '本机检查通过' : '运行不可用' }}
              </td>
              <td>
                {{ [
                  info.pi.version,
                  info.pi.model && `${info.pi.model.provider} / ${info.pi.model.id}`,
                  info.pi.sandboxReady && '命令沙箱就绪',
                  info.pi.message
                ].filter(Boolean).join(' · ') }}
              </td>
            </tr>
            <tr v-for="tool in info.tools" :key="tool.name">
              <th scope="row">{{ tool.name }}</th>
              <td :class="tool.available ? 'muted' : 'warning-text'">
                {{ tool.available ? '可用' : '不可用' }}
              </td>
              <td>{{ tool.version || tool.message || '请核对本机安装与认证' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <details class="advanced-settings">
        <summary>程序重启</summary>
        <form class="form-stack" aria-label="重启工作台" @submit.prevent="restart">
          <label
            >新版工具目录（可选）<input
              v-model="toolRoot"
              name="toolRoot"
              spellcheck="false"
              autocorrect="off"
              autocapitalize="none"
              placeholder="沿用当前运行程序"
              :disabled="restarting"
          /></label>
          <p class="muted">活动作业结束后使用新会话重新打开，已保存的编辑草稿可恢复。</p>
          <p v-if="restartNotice" role="status" class="notice">{{ restartNotice }}</p>
          <div class="form-actions">
            <Button variant="outline" :disabled="restarting">{{
              restarting ? '正在安排重启…' : '重启工作台'
            }}</Button>
          </div>
        </form>
      </details>
      <details>
        <summary>原始诊断数据</summary>
        <pre>{{ readableValue(info) }}</pre>
      </details>
    </section>
    <p v-else-if="loading" role="status" class="empty">正在读取本机信息…</p>
  </section>
  <section v-show="group === 'backup'" aria-label="备份与恢复"><BackupForms /></section>
  <section v-show="group === 'engineering'" aria-label="工程维护"><EngineeringPanel /></section>
  <section v-show="group === 'external'" aria-label="研发关联"><ExternalLinks /></section>
</template>
<style scoped>
.settings-navigation {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
  margin-bottom: 24px;
  border-bottom: 1px solid var(--yss-colors-border);
}
.settings-navigation a {
  padding: 10px 0;
  border-bottom: 2px solid transparent;
  color: var(--brand-color-text-secondary);
}
.settings-navigation a[aria-current='page'] {
  color: var(--yss-color-primary-control);
  border-bottom-color: var(--yss-color-primary-control);
  font-weight: 600;
}
.settings-facts th {
  width: 150px;
  white-space: nowrap;
  font-weight: 500;
}
.settings-facts td {
  white-space: normal;
  overflow-wrap: anywhere;
}
.settings-facts {
  min-width: 360px;
}
.tools-heading {
  margin: 24px 0 10px;
}
.tools-table {
  min-width: 430px;
}
.tools-table td:last-child {
  white-space: normal;
}
.advanced-settings {
  margin-top: 20px;
  border-top: 1px solid var(--yss-colors-border);
  padding-top: 8px;
}
</style>
