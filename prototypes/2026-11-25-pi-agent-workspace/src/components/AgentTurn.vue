<script setup lang="ts">
import { CircleAlert, CircleCheck, FileText, FolderSearch, LoaderCircle, MessageCircle, Terminal, Wrench } from '@lucide/vue'
import { computed, ref } from 'vue'
import { Bubble, BubbleContent } from '@/components/ui/bubble'
import { Button } from '@/components/ui/button'
import { CodeBlock, CodeBlockContent } from '@/components/ui/code-block'
import { Message, MessageContent } from '@/components/ui/message'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Textarea } from '@/components/ui/textarea'
import { Trace, TraceCollapsible, TraceCollapsibleContent, TraceCollapsibleTrigger, TraceIcon, TraceLine, TraceTitle } from '@/components/ui/trace'
import { runLabels, type Turn } from '../demo-agent'

const props = defineProps<{ turn: Turn, latest: boolean }>()
defineEmits<{ answer: [value: string], result: [], logs: [turn: Turn] }>()

const reply = ref('retain')
const custom = ref('')
const answerOptions = {
  retain: '保留现有预览，刷新失败时提供重试。',
  clear: '刷新时清除现有预览，失败后展示空状态及重试。',
}
const sharesDemoSteps = computed(() => props.turn.demoDetails || props.turn.id.startsWith('run-live-'))
const activity = computed(() => {
  const { turn } = props
  if (turn.demoDetails && turn.step >= 2 && !turn.answer) return {
    reading: '现有的加载组件可以复用。规格详情会在刷新时同时更新元数据和预览内容，需要分别处理首次进入与后台刷新。',
    updating: '我把加载状态放进原型预览区。已有内容时继续显示，重试也留在当前位置。',
    summary: turn.text,
  }
  if (sharesDemoSteps.value) {
    const [reading, updating, ...rest] = turn.text.split(/(?<=。)\s+/u)
    return { reading, updating, summary: rest.join(' ') }
  }
  return { summary: turn.text }
})
const output = computed(() => {
  const command = '$ pnpm --dir ui typecheck'
  switch (props.turn.status) {
    case 'done': return `${command}\n> vue-tsc --noEmit\n退出码 0`
    case 'stopped': return `${command}\n本轮已停止，未收到退出码。`
    case 'interrupted': return `${command}\n本轮中断，未收到退出码。`
    case 'stopping': return `${command}\n正在停止。`
    default: return `${command}\n等待退出状态…`
  }
})
function response() {
  return reply.value === 'custom' ? custom.value : answerOptions[reply.value as keyof typeof answerOptions]
}
</script>

<template>
  <article class="turn" :aria-label="`运行 ${turn.time}`">
    <Message align="end" class="user-message" aria-label="你的任务">
      <MessageContent class="message-body">
        <div class="sr-only">你 <time>{{ turn.time }}</time></div>
        <Bubble align="end" variant="muted" class="user-bubble"><BubbleContent>{{ turn.prompt }}</BubbleContent></Bubble>
      </MessageContent>
    </Message>

    <Message class="agent-message" aria-label="Pi Agent 的回复">
      <MessageContent class="message-body assistant-content">
        <span class="sr-only">Pi Agent · {{ runLabels[turn.status] }}</span>
        <p v-if="turn.demoDetails && turn.status === 'done' && !turn.answer" class="progress-note">我先核对规格详情里的原型预览和共享加载组件，再补齐首次打开、刷新和失败后的反馈。</p>
        <p v-if="!turn.step && turn.status === 'running'" class="working"><LoaderCircle :size="14" class="spin" /> 正在读取本轮任务…</p>
        <div v-if="turn.step && sharesDemoSteps" class="turn-activity" aria-label="本轮活动">
          <Trace class="tool-trace">
            <TraceCollapsible>
              <TraceCollapsibleTrigger class="tool-trigger">
                <TraceLine><TraceIcon><FolderSearch :size="15" /></TraceIcon><TraceTitle>检查规格详情与共享加载组件</TraceTitle></TraceLine>
                <span class="tool-meta">读取 3 个文件</span>
              </TraceCollapsibleTrigger>
              <TraceCollapsibleContent class="tool-content">
                <ul class="trace-files"><li><FileText /> docs/spec/workbench-loading/workbench-loading-spec.md</li><li><FileText /> ui/src/views/SpecDetail/index.vue</li><li><FileText /> ui/src/components/LoadingIndicator.vue</li></ul>
              </TraceCollapsibleContent>
            </TraceCollapsible>
          </Trace>
          <p v-if="activity.reading && !turn.answer" class="progress-note">{{ activity.reading }}</p>
          <div v-if="turn.status === 'waiting' && latest" class="question-card">
            <div class="question-title"><MessageCircle :size="16" /><strong>需要你确认</strong></div>
            <p>如果刷新原型时失败，页面是否继续保留已经显示的预览？</p>
            <form class="question-form" @submit.prevent="$emit('answer', response())">
              <RadioGroup v-model="reply" aria-label="刷新失败时的预览行为">
                <label class="radio-label"><RadioGroupItem value="retain" /> 保留预览并提供重试 <span class="recommended">建议</span></label>
                <label class="radio-label"><RadioGroupItem value="clear" /> 清除预览并提供重试</label>
                <label class="radio-label"><RadioGroupItem value="custom" /> 填写其他处理方式</label>
              </RadioGroup>
              <Textarea v-if="reply === 'custom'" v-model="custom" aria-label="其他处理方式" placeholder="说明刷新失败后的行为" />
              <Button type="submit" :disabled="!response().trim()">提交答复</Button>
            </form>
          </div>
          <div v-else-if="turn.answer" class="saved-answer"><CircleCheck :size="15" /><span><strong>已提交答复</strong> · {{ turn.answer }}</span></div>
          <p v-if="activity.reading && turn.answer" class="progress-note">{{ activity.reading }}</p>
          <Trace v-if="turn.step >= 2" class="tool-trace">
            <TraceCollapsible>
              <TraceCollapsibleTrigger class="tool-trigger">
                <TraceLine><TraceIcon><Wrench :size="15" /></TraceIcon><TraceTitle>调整原型预览的加载反馈</TraceTitle></TraceLine>
                <span class="tool-meta">更新 2 个文件</span>
              </TraceCollapsibleTrigger>
              <TraceCollapsibleContent class="tool-content">复用 Skeleton 并补充原位重试。改动保存在本轮隔离工作区。</TraceCollapsibleContent>
            </TraceCollapsible>
          </Trace>
          <p v-if="activity.updating" class="progress-note">{{ activity.updating }}</p>
          <Trace v-if="turn.commands && turn.step >= 2" class="tool-trace">
            <TraceCollapsible>
              <TraceCollapsibleTrigger class="tool-trigger">
                <TraceLine :status="turn.status === 'running' ? 'running' : undefined"><TraceIcon><Terminal :size="15" /></TraceIcon><TraceTitle class="command-text">pnpm --dir ui typecheck</TraceTitle></TraceLine>
                <span class="tool-meta">{{ turn.status === 'done' ? '通过' : runLabels[turn.status] }}</span>
              </TraceCollapsibleTrigger>
              <TraceCollapsibleContent class="tool-content"><CodeBlock size="sm" class="terminal-output"><CodeBlockContent>{{ output }}</CodeBlockContent></CodeBlock></TraceCollapsibleContent>
            </TraceCollapsible>
          </Trace>
        </div>
        <p v-if="activity.summary" class="agent-reply">{{ activity.summary }}</p>
        <div v-if="turn.status === 'stopped' || turn.status === 'interrupted'" class="run-feedback"><CircleAlert :size="15" /> {{ turn.status === 'stopped' ? '本轮已停止，已收到的输出仍可查看。' : '工作台退出后中断。可从本会话继续处理。' }}</div>
        <div v-if="turn.status === 'done' || turn.status === 'stopped' || turn.status === 'interrupted'" class="turn-actions">
          <Button v-if="turn.status === 'done' && turn.step === 3 && latest" variant="ghost" size="sm" class="inline-action" @click="$emit('result')"><FileText /> 查看本次成果</Button>
          <Button variant="ghost" size="sm" class="inline-action" @click="$emit('logs', turn)"><Terminal /> 执行日志</Button>
        </div>
      </MessageContent>
    </Message>
  </article>
</template>
