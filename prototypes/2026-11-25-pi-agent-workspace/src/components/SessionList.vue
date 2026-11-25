<script setup lang="ts">
import { ArrowUpLeft, ArrowUpRight, ChevronRight, CircleAlert, Clock3, Folder, History, LoaderCircle, Plus, Square } from '@lucide/vue'
import { ref, useId, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { runLabels, type RunState, type Session } from '../demo-agent'

const props = defineProps<{ sessions: Session[], activeId: string | null }>()
defineEmits<{ select: [id: string | null], source: [], history: [] }>()

const expanded = ref(true)
const statusPrefix = useId()
watch(() => props.activeId, (id) => {
  if (id) expanded.value = true
})

const statusIcons = {
  running: LoaderCircle, stopping: LoaderCircle, waiting: Clock3, interrupted: CircleAlert, stopped: Square,
}
function status(session: Session): RunState {
  return session.turns.at(-1)?.status ?? 'done'
}
function statusIcon(state: RunState) {
  return state === 'done' ? undefined : statusIcons[state]
}
</script>

<template>
  <nav class="sessions" aria-label="Pi Agent 会话">
    <Button class="new-session" variant="ghost" type="button" @click="$emit('select', null)"><Plus /> 新建会话</Button>
    <div class="side-heading">Spec 会话</div>
    <Collapsible v-model:open="expanded" class="spec-group">
      <CollapsibleTrigger as-child>
        <Button class="spec-group-trigger" variant="ghost" type="button">
          <ChevronRight :size="14" class="spec-group-caret" aria-hidden="true" />
          <Folder :size="15" aria-hidden="true" />
          <span class="spec-group-name">工作台加载体验</span>
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent class="spec-group-items">
        <div class="session-items">
          <Button
            v-for="session in sessions" :key="session.id" variant="ghost" type="button" class="session-item"
            :data-current="activeId === session.id" :aria-current="activeId === session.id ? 'page' : undefined"
            :aria-label="session.title" :aria-describedby="`${statusPrefix}-${session.id}`" :title="`${session.title} · ${runLabels[status(session)]} · ${session.time}`"
            @click="$emit('select', session.id)"
          >
            <span class="session-copy">{{ session.title }}</span>
            <component
              :is="statusIcon(status(session))" v-if="status(session) !== 'done'" class="session-state"
              :class="{ spin: ['running', 'stopping'].includes(status(session)) }" :data-state="status(session)"
              aria-hidden="true"
            />
            <span :id="`${statusPrefix}-${session.id}`" class="sr-only">{{ runLabels[status(session)] }} · {{ session.time }}</span>
          </Button>
        </div>
      </CollapsibleContent>
    </Collapsible>
    <div class="session-bottom">
      <Button class="source-link" variant="ghost" type="button" @click="$emit('source')"><ArrowUpLeft /> 返回 Spec</Button>
      <Button variant="ghost" type="button" class="history-link" @click="$emit('history')"><History /> 执行记录 <ArrowUpRight class="outgoing" /></Button>
    </div>
  </nav>
</template>
