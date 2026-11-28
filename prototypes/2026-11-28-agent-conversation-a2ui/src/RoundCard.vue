<script setup lang="ts">
import { computed, watchEffect } from 'vue'
import { CircleCheck, Radio, TriangleAlert } from '@lucide/vue'
import { Button } from './components/ui/button'
import LoadingSkeleton from './components/LoadingSkeleton.vue'
import { catalogId, usePrototypeSurface } from './surface'
import { sceneMessages } from './scenes'
import type { Option, Question, Round } from './conversation'
import SurfaceNode from './SurfaceNode.vue'

type ReplayStage = NonNullable<Round['replayStage']>
const props = defineProps<{ origin: string; round: Round; latest: boolean; replay?: boolean; awaitingReceipt?: boolean }>()
const emit = defineEmits<{
  advanceReplay: [stage: ReplayStage]
  evidence: [key: Question]; basis: []; pick: [key: Option]; compare: []; continue: [key: Option]
  pickQuestion: [key: Question]; continueQuestion: [key: Question]
}>()

const { root, error, show } = usePrototypeSurface(props.origin, action => {
  if (action.surfaceId !== props.origin || action.context?.origin !== props.origin) return
  const argument = action.context.argument
  if (action.name === 'evidence' && (argument === 'scope' || argument === 'owner')) return emit('evidence', argument)
  if (action.name === 'compareBasis') return emit('basis')
  if (!props.latest || props.awaitingReceipt) return
  if (action.name === 'compare') emit('compare')
  if (argument === 'scope' || argument === 'owner') {
    if (action.name === 'pickQuestion') emit('pickQuestion', argument)
    if (action.name === 'continueQuestion' && props.round.question === argument) emit('continueQuestion', argument)
  }
  if (argument === 'closed' || argument === 'admin') {
    if (action.name === 'pick') emit('pick', argument)
    if (action.name === 'continue' && props.round.selected === argument) emit('continue', argument)
  }
})

const stage = computed<ReplayStage>(() => {
  const round = props.round
  if (round.replayStage !== undefined) return round.replayStage
  // Older local records kept user actions but did not store the received packets.
  if (round.question || round.selected || round.expanded.length || round.basis || round.card === 'compare') return 2
  return 0
})
const hasCompleteCard = computed(() => !props.replay || stage.value >= 2)
const simulating = computed(() => Boolean(props.replay && props.latest))
const expanded = computed(() => props.round.basis ? [...props.round.expanded, 'basis'] : props.round.expanded)
const instructions = [
  '文字已到达，正在接收卡片。',
  '已收到一条不支持的消息。',
  '已收到完整卡片。',
  '收到一条后续更新。',
  '已收到有效更新，卡片已恢复。',
]
const steps = ['接收不支持的消息', '接收完整卡片', '接收无效更新', '接收有效更新', '再次接收无效更新']
function advance() {
  const next: readonly ReplayStage[] = [1, 2, 3, 4, 3]
  if (simulating.value) emit('advanceReplay', next[stage.value])
}

watchEffect(() => {
  if (props.replay && stage.value === 0) return
  if (props.replay && stage.value === 1) {
    show([{ version: '1.0', createSurface: { surfaceId: props.origin, catalogId,
      components: [{ id: 'root', component: 'UnlistedWidget' }] } }])
    return
  }
  const messages = sceneMessages(props.origin, props.round, props.latest, props.awaitingReceipt)
  if (show(messages) && props.replay && stage.value === 3) {
    // Reject a malformed update without replacing the last valid, interactive surface.
    show([...messages, { version: '1.0', updateDataModel: { surfaceId: props.origin, path: 17, value: '' } }])
  }
})
</script>

<template>
  <div v-if="simulating" class="replay-tools" aria-label="模拟收包">
    <span class="packet-status" role="status"><CircleCheck v-if="stage === 2 || stage === 4" :size="14" aria-hidden="true" /><Radio v-else :size="14" aria-hidden="true" />{{ instructions[stage] }}</span>
    <Button type="button" variant="outline" size="sm" @click="advance">模拟收包 · {{ steps[stage] }}</Button>
  </div>
  <LoadingSkeleton v-if="simulating && stage === 0" variant="cards" :rows="1" />
  <p v-if="replay && !latest && stage === 0" class="pi-round-note">本轮未留存完整卡片，可回看已收到的文字。</p>
  <p v-if="error && (!replay || stage !== 0)" class="notice pi-inline-error" role="alert"><TriangleAlert :size="15" aria-hidden="true" />{{ root && hasCompleteCard
    ? '这次卡片更新无法显示，仍可查看上一张卡片的内容和依据。'
    : '收到的卡片格式无法显示，可以继续阅读本轮文字。' }}</p>
  <SurfaceNode v-if="root && hasCompleteCard" :node="root"
    :selected="round.card === 'compare' ? round.selected : round.question" :expanded="expanded" :readonly="!latest || awaitingReceipt" />
</template>

<style scoped>
.packet-status, .notice { display: flex; align-items: center; gap: 6px; }
.notice svg { flex: none; }
.replay-tools { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 14px; padding-block: 7px 11px; color: var(--quiet); font-size: 12px; }
@media (max-width: 719px) { .replay-tools button { min-height: 44px; } }
</style>
