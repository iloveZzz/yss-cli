import type { Stage, StageRecord, Status } from './workbench-model'

export type Progress =
  | 'unmarked'
  | 'todo'
  | 'doing'
  | 'blocked'
  | 'done'
  | 'skipped'
export type Validity = 'current' | 'unverified' | 'needsSync' | 'needsReview'
export type EffectiveGoalState = 'unverified' | 'missing' | 'active' | 'completed' | 'canceled'
export type ParticipantRole = 'spec' | 'backend' | 'frontend' | 'integration'
export type ProgressSource =
  | 'unmarked'
  | 'evidence'
  | 'content'
  | 'manual'
  | 'goal'
  | 'historical'
export const progressLabels: Record<Progress, string> = {
  unmarked: '未记录',
  todo: '待开始',
  doing: '进行中',
  blocked: '受阻',
  done: '已完成',
  skipped: '已跳过'
}
export const validityLabels: Record<Validity, string> = {
  current: '有效',
  unverified: '待核实',
  needsSync: '待更新',
  needsReview: '需复核'
}
export const roleLabels: Record<ParticipantRole, string> = {
  spec: 'Spec',
  backend: 'Backend',
  frontend: 'Frontend',
  integration: '联调'
}
export const sourceLabels: Record<ProgressSource, string> = {
  unmarked: '尚无依据',
  evidence: '自动依据',
  content: '内容确认',
  manual: '人工更正',
  goal: '交付目标',
  historical: '历史凭证'
}
export const attentionLabels: Record<string, string> = {
  awaitingConfirmation: '待自动核实',
  confirmationRequired: '待核实',
  goalMissing: '未设置本轮目标',
  manualOverride: '人工更正',
  blocked: '受阻',
  unverified: '未核实',
  needsSync: '证据待更新',
  needsReview: '需复核',
  conflict: '有冲突'
}
export interface DeliveryTarget {
  kind: 'local' | 'merged' | 'environment'
  label: string
  reference?: string | null
}
export function targetLabel(target: DeliveryTarget) {
  const kinds = { local: '本地产物', merged: '合并代码', environment: '指定环境' }
  return [...new Set([kinds[target.kind], target.label, target.reference].filter(Boolean))].join(' · ')
}
export interface StageScope {
  stage: Stage
  applicable: boolean
  reason: string
}
export interface Participant {
  projectId: string
  role: ParticipantRole
  label: string
}
export interface LifecycleGoalInput {
  specId: string
  scope: string
  target: DeliveryTarget
  stages: StageScope[]
  participants: Participant[]
  acceptanceRefs: string[]
  specDigest: string
}
export interface GoalRecord extends LifecycleGoalInput {
  schemaVersion: number
  goalVersion: string
  createdAt: string
}
export interface ProjectVersion {
  projectId: string
  commit?: string | null
  workingTreeDigest?: string | null
}
export interface Evidence {
  kind:
    | 'document'
    | 'artifact'
    | 'command'
    | 'test'
    | 'merge'
    | 'environment'
    | 'acceptance'
    | 'other'
  label: string
  reference: string
  outcome?: 'passed' | 'failed' | 'observed' | null
}
export interface ProgressReportInput {
  id: string
  specId: string
  goalVersion: string
  specDigest: string
  stage: Stage
  projectId?: string | null
  role: ParticipantRole
  progress: Progress
  validity: Validity
  summary: string
  blocker: string
  nextAction: string
  scopeRefs: string[]
  versions: ProjectVersion[]
  evidence: Evidence[]
  inputPaths?: { projectId: string; paths: string[]; observedDigest?: string }[]
}
export interface ReportRecord extends ProgressReportInput {
  schemaVersion: number
  recordedAt: string
}
export interface ProgressHistory {
  goals: GoalRecord[]
  roundIds?: Record<string, string>
  reports: ReportRecord[]
  overrides: OverrideRecord[]
  decisions: DecisionRecord[]
  cancellations?: GoalCancellation[]
  reopens?: { specId: string; goalVersion: string; previousGoalVersion: string; recordedAt: string }[]
  lateReports?: { specId: string; goalVersion: string; reportId: string; inputLimitations: string[] }[]
}
export interface GoalCancellation {
  specId: string
  goalVersion: string
  reason: string
  recordedAt: string
  source?: string
}
export interface ConfirmationInput {
  specId: string
  goalVersion: string
  stage: Stage
  scope: string
  evidenceDigest: string
  source: string
  decision: 'confirm' | 'reject'
}
export interface DecisionRecord extends ConfirmationInput {
  origin?: 'automatic' | 'manual'
  stageEvidenceDigest?: string | null
  schemaVersion: number
  id: string
  recordedAt: string
}
export interface OverrideInput {
  specId: string
  goalVersion: string
  stage: Stage
  progress: Progress
  reason: string
  projectId?: string | null
}
export interface ClearOverrideInput {
  specId: string
  goalVersion: string
  stage: Stage
  projectId?: string | null
}
export interface OverrideRecord extends OverrideInput {
  schemaVersion: number
  id: string
  recordedAt: string
  clearedAt: string | null
}
export interface ParticipantProgress {
  projectId: string
  role: ParticipantRole
  label: string
  progress: Progress
  validity: Validity
  source: ProgressSource
  summary: string
  blocker: string
  nextAction: string
  evidence: Evidence[]
  reportId: string | null
  lastVerifiedAt: string | null
  hasOverride: boolean
  conflicts: string[]
  versions: ProjectVersion[]
  currentVersion: ProjectVersion | null
  overrideRecord: OverrideRecord | null
}
export interface StageSummary {
  stage: Stage
  applicable: boolean
  scopeReason: string
  progress: Progress
  validity: Validity
  source: ProgressSource
  summary: string
  blocker: string
  nextAction: string
  evidence: Evidence[]
  participants: ParticipantProgress[]
  confirmationRequired: boolean
  confirmation: DecisionRecord | null
  lastVerifiedAt: string | null
  hasOverride: boolean
  conflicts: string[]
  legacy: StageRecord | null
  versions: ProjectVersion[]
  overrideRecord: OverrideRecord | null
}
export interface EvidenceSync {
  stage: Stage
  projectId?: string
  projectName?: string
  role?: ParticipantRole
  reportId?: string
  recordedAt?: string
  reason: string
  paths: string[]
  nextAction: string
}

export function evidenceSyncKey(evidence: EvidenceSync) {
  return [evidence.stage, evidence.projectId, evidence.role, evidence.reportId].join(':')
}

export interface ProgressSummary {
  roundId?: string | null
  autoConfirmableStages?: Stage[]
  currentVersions?: ProjectVersion[]
  lastDelivery?: DecisionRecord
  materialReview?: boolean
  completion?: { completed: number; total: number }
  syncEvidence?: EvidenceSync[]
  cancellation?: GoalCancellation
  goal: GoalRecord | null
  goalVersion: string | null
  stages: StageSummary[]
  participants: Participant[]
  activeStages: Stage[]
  primaryStage: Stage
  status: Status
  validity: Validity
  attention: string[]
  summary: string
  blocker: string
  nextAction: string
  lastVerifiedAt: string | null
  evidenceDigest: string
  hasOverride: boolean
  conflicts: string[]
}
export type LifecycleGoalChange =
  | { kind: 'cancelLifecycleGoal'; input: { specId: string; goalVersion: string; reason: string } }
  | { kind: 'reopenLifecycleGoal'; input: { specId: string; goalVersion: string; goal: LifecycleGoalInput } }

export type ProgressAction = LifecycleGoalChange
  | { kind: 'setLifecycleGoal'; input: LifecycleGoalInput }
  | { kind: 'reportProgress'; input: ProgressReportInput }
  | { kind: 'confirmProgress'; input: ConfirmationInput }
  | { kind: 'setProgressOverride'; input: OverrideInput }
  | { kind: 'clearProgressOverride'; input: ClearOverrideInput }
  | { kind: 'reconcileProgress'; specId: string }

export function decisionLabel(record: DecisionRecord) {
  if (record.decision === 'reject') return '用户已退回'
  return record.origin === 'automatic' ? '已自动核实' : '用户已确认'
}

export function stageState(stage: StageSummary, autoConfirmableStages?: Stage[]) {
  if (
    !stage.applicable &&
    !(stage.stage === 'delivery' && stage.confirmationRequired)
  )
    return '不适用'
  if (stage.confirmation?.decision === 'reject') return '已退回'
  if (stage.stage === 'delivery' && stage.confirmation?.decision === 'confirm' && stage.source === 'historical') {
    return stage.confirmation.origin === 'automatic' ? '该版本已自动核实' : '该版本已验收'
  }
  if (stage.validity !== 'current') return validityLabels[stage.validity]
  if (stage.progress === 'done' || stage.progress === 'skipped') {
    if (stage.confirmationRequired && !stage.confirmation) {
      return autoConfirmableStages?.includes(stage.stage) ? '待自动核实' : '待补证据'
    }
    if (stage.confirmation?.origin === 'automatic') return '已自动核实'
  }
  return progressLabels[stage.progress]
}
