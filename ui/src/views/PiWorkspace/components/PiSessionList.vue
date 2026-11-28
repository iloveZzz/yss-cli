<script setup lang="ts">
import { computed, nextTick, shallowRef, useId } from 'vue'
import { Folder, ListFilter, Plus, RotateCw, Search, Trash2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import ControlTooltip from '@/components/ControlTooltip.vue'
import SingleSelect from '@/components/SingleSelect.vue'
import { dateLabel } from '@/services/workbench'
import LoadingSkeleton from '@/components/LoadingSkeleton.vue'
import { agentHostLabels, jobStatusLabels } from '@/services/workbench-platform'
import { type AgentAttention, type AgentSessionPage, type AgentSummary } from '@/services/workbench-agent'

const props = defineProps<{
  context: AgentSessionPage
  activeSession: string
  error: string
  notice: string
  busy: boolean
  current: boolean
  scopeOptions?: readonly { value: string; label: string }[]
}>()
const emit = defineEmits<{
  new: []
  choose: [session: AgentSummary, item?: AgentAttention]
  refresh: []
  'load-more': []
  remove: [session: AgentSummary, trigger: EventTarget | null]
  'select-scope': [specId: string]
}>()
const filter = defineModel<string>('search', { required: true })
const pending = defineModel<boolean>('attention-only', { required: true })
const searching = shallowRef(false)
const searchId = useId()
const searchTrigger = searchId + '-toggle'
const canSwitchScope = computed(() => props.scopeOptions?.some((option) => option.value !== props.context.spec.id))
const labels: Record<AgentAttention['kind'], string> = {
  recovery: '需恢复', question: '待答复', answerFailed: '答复保存失败',
  interrupted: '中断待核对', failed: '运行失败', review: '待审阅'
}
async function toggleSearch() {
  searching.value = !searching.value
  if (!searching.value) filter.value = ''
  await nextTick()
  document.getElementById(searching.value ? searchId : searchTrigger)?.focus({ preventScroll: true })
}
</script>

<template>
  <section class="pi-history" aria-label="Agent 会话列表" :aria-busy="busy">
    <div class="pi-history-heading">
      <h2>会话</h2>
      <ControlTooltip label="搜索会话">
        <Button :id="searchTrigger" type="button" variant="ghost" size="icon" :aria-expanded="searching"
          :aria-controls="searching ? searchId : undefined" aria-label="搜索会话" @click="toggleSearch">
          <Search :size="16" aria-hidden="true" />
        </Button>
      </ControlTooltip>
      <ControlTooltip label="刷新会话">
        <Button type="button" variant="ghost" size="icon" aria-label="刷新会话" :disabled="busy" @click="emit('refresh')">
          <RotateCw :size="15" aria-hidden="true" />
        </Button>
      </ControlTooltip>
    </div>
    <Transition name="workbench-feedback">
      <Input v-if="searching" :id="searchId" v-model="filter" type="search" placeholder="搜索会话"
        aria-label="搜索会话标题" class="pi-history-search" @keydown.esc.stop.prevent="toggleSearch" />
    </Transition>
    <Button type="button" variant="ghost" class="pi-history-new" :disabled="!context.spec.active || !!context.sourceError"
      @click="emit('new')"><Plus :size="17" />新会话</Button>
    <Button type="button" variant="ghost" class="pi-history-filter" :aria-pressed="pending"
      @click="pending = !pending"><ListFilter :size="16" />待处理 <span>{{ context.attentionTotal }}</span></Button>
    <p v-if="error" class="pi-history-note" role="alert">{{ error }} · 请刷新<template v-if="!current && context.sessions.length">，暂显示上次结果</template></p>
    <p v-if="notice" class="pi-history-note" role="status">{{ notice }}</p>
    <p v-if="context.sessions.some((session) => session.reviewError)" class="pi-history-note">部分成果尚待核实，刷新后查看最新状态。</p>
    <div class="pi-history-spec" :title="context.spec.title">
      <Folder :size="16" aria-hidden="true" />
      <SingleSelect v-if="canSwitchScope && scopeOptions" class="w-full min-w-0" aria-label="切换规格"
        :model-value="context.spec.id" :options="scopeOptions" :placeholder="context.spec.title"
        @update:model-value="emit('select-scope', $event)" />
      <span v-else>{{ context.spec.title }}</span>
    </div>
    <LoadingSkeleton v-if="!current && busy" variant="text" :rows="3" label="正在筛选会话" />
    <TransitionGroup v-else-if="context.sessions.length" tag="ul" name="workbench-list" class="pi-history-list">
      <li v-for="session in context.sessions" :key="session.id" :data-selected="session.id === activeSession">
        <button type="button" class="pi-session-title" :title="session.title"
          :aria-current="session.id === activeSession ? 'true' : undefined" @click="emit('choose', session)">
          <span>{{ session.title }}</span>
          <small>
            {{ agentHostLabels[session.host ?? 'pi'] }} · {{ jobStatusLabels[session.status] ?? session.status }}<template v-if="session.workspaceMode !== 'direct'"> · 历史隔离运行</template>
            · {{ dateLabel(session.updatedAt) }}
          </small>
        </button>
        <button v-if="session.attention[0]" type="button" class="pi-session-attention"
          :title="labels[session.attention[0].kind]" @click="emit('choose', session, session.attention[0])">
          {{ session.attention[0].status === 'saving' ? '答复保存中' : labels[session.attention[0].kind] }}
        </button>
        <Button type="button" variant="ghost" size="icon-sm" class="pi-session-delete"
          :aria-label="'删除会话 ' + session.title" :title="session.deleteIssue || '删除会话'"
          :aria-disabled="!!session.deleteIssue" @click="!session.deleteIssue && emit('remove', session, $event.currentTarget)">
          <Trash2 :size="15" aria-hidden="true" />
        </Button>
      </li>
    </TransitionGroup>
    <p v-else class="pi-history-note" data-workbench-reveal>{{ pending ? '暂无待处理会话' : filter ? '没有匹配的会话' : '暂无会话' }}</p>
    <Button v-if="current && context.nextCursor" type="button" variant="ghost" size="sm" :disabled="busy"
      @click="emit('load-more')">{{ busy ? '正在读取…' : '加载更多' }}（{{ context.sessions.length }} / {{ context.total }}）</Button>
  </section>
</template>
