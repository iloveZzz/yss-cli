import type { Stage, StageRecord, Status } from './workbench-model'

export type Progress =
  | 'unmarked'
  | 'todo'
  | 'doing'
  | 'blocked'
  | 'done'
  | 'skipped'
export type Validity = 'current' | 'unverified' | 'needsSync' | 'needsReview'
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
  needsSync: '待同步',
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
  awaitingConfirmation: '待确认',
  confirmationRequired: '待确认',
  goalMissing: '目标待核实',
  manualOverride: '人工更正',
  blocked: '受阻',
  unverified: '未核实',
  needsSync: '待同步',
  needsReview: '需复核',
  conflict: '有冲突'
}
export interface DeliveryTarget {
  kind: 'local' | 'merged' | 'environment'
  label: string
  reference?: string | null
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
}
export interface ReportRecord extends ProgressReportInput {
  schemaVersion: number
  recordedAt: string
}
export interface ProgressHistory {
  goals: GoalRecord[]
  reports: ReportRecord[]
  overrides: OverrideRecord[]
  decisions: DecisionRecord[]
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
export interface ProgressSummary {
  currentVersions?: ProjectVersion[]
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
export type ProgressAction =
  | { kind: 'setLifecycleGoal'; input: LifecycleGoalInput }
  | { kind: 'reportProgress'; input: ProgressReportInput }
  | { kind: 'confirmProgress'; input: ConfirmationInput }
  | { kind: 'setProgressOverride'; input: OverrideInput }
  | { kind: 'clearProgressOverride'; input: ClearOverrideInput }
  | { kind: 'reconcileProgress'; specId: string }

export function stageState(stage: StageSummary) {
  if (
    !stage.applicable &&
    !(stage.stage === 'delivery' && stage.confirmationRequired)
  )
    return '不适用'
  if (stage.validity !== 'current') return validityLabels[stage.validity]
  if (stage.confirmation?.decision === 'reject') return '已退回'
  if (stage.confirmationRequired && !stage.confirmation) return '待确认'
  return progressLabels[stage.progress]
}
