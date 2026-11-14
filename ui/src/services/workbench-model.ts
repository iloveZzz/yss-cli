export const stages = { unmarked: '未标记', research: '调研', analysis: '分析', spec: '规格', prototype: '原型', handoff: '交接', implementation: '实施', verification: '验证', delivery: '交付' } as const
export const statuses = { unmarked: '未标记', todo: '待处理', doing: '进行中', blocked: '阻塞', done: '已完成', canceled: '已取消' } as const
export type Stage = keyof typeof stages
export type Status = keyof typeof statuses
export type Health = 'available' | 'missing' | 'unreadable' | 'unsupported' | 'oversized'
export const healthLabels = { available: '可访问', missing: '缺失', unreadable: '不可读', unsupported: '不支持', oversized: '超过读取上限', external: '外部链接，未探测' }
export interface ProjectRecord { id: string; name: string; rootPath: string; active: boolean; createdAt: string; updatedAt: string }
export interface ResourceRef { kind: 'supplement' | 'prototype' | 'interaction' | 'other'; label: string; targetType: 'local' | 'web'; target: string }
export interface SpecRecord { id: string; projectId: string; sourcePath: string; title: string; stage: Stage; status: Status; nextAction: string; blocker: string; notes: string; resources: ResourceRef[]; active: boolean; createdAt: string; updatedAt: string; lastVisitedAt: string | null }
export interface ProjectView { sourceDirectories: string[]; record: ProjectRecord; available: boolean; message: string | null }
export interface SpecView { content: ContentView; record: SpecRecord; sourceHealth: Health; sourceMessage: string | null; sourceModifiedAt: string | null; resources: { record: ResourceRef; health: Health | 'external'; message: string | null }[] }
export interface ChangeEvent { id: string; projectId: string; specId: string | null; taskId: string | null; lifecycleStage: Stage | null; at: string; changes: Record<string, { before: unknown; after: unknown }> }
export interface Snapshot { revision: number; readAt: string; projects: ProjectView[]; specs: SpecView[]; history: ChangeEvent[]; tasks: TaskView[]; stageRecords: StageRecord[]; savedViews: SavedView[] }
export interface Candidate { sourcePath: string; title: string; modifiedAt: string | null; alreadyRegistered: boolean }
export interface DiscoverResult { candidates: Candidate[]; warnings: string[]; complete: boolean }
export interface SourceDocument { specId: string; text: string; digest: string; readAt: string }
export type EditSpec = Pick<SpecRecord, 'sourcePath' | 'title' | 'stage' | 'status' | 'nextAction' | 'blocker' | 'notes' | 'resources' | 'active'>
export type Action = RespondQuestionAction | { kind: 'refreshSpec'; specId: string } | { kind: 'confirmContent'; specId: string; scope: string; artifactDigest: string } | { kind: 'addProject'; rootPath: string; name?: string } | { kind: 'updateProject'; projectId: string; name: string; rootPath: string; active: boolean } | { kind: 'addSpec'; projectId: string; sourcePath: string } | ({ kind: 'editSpec'; specId: string } & EditSpec) | { kind: 'visitSpec'; specId: string } | {kind:'addTasks'; specId:string; drafts:TaskDraft[]} | {kind:'editTask';taskId:string;edit:TaskEdit} | ({kind:'setStageRecord'} & Omit<StageRecord,'updatedAt'>) | {kind:'setWorkStatus';entity:WorkEntity;id:string;status:Status;blocker:string} | {kind:'setWorkStage';entity:WorkEntity;id:string;stage:Stage} | {kind:'saveView';viewId:string|null;name:string;query:WorkQuery;layout:Layout;groupBy:GroupBy} | {kind:'removeView';viewId:string}
export interface MutationRequest { expectedRevision: number; action: Action }
export interface MutationResult { revision: number; projectId: string | null; specId: string | null; taskIds: string[]; viewId: string | null }

export type WorkEntity = 'spec' | 'task'
export type Layout = 'list' | 'board'
export type GroupBy = 'status' | 'stage'
export type StageProgress = 'unmarked' | 'doing' | 'blocked' | 'done' | 'skipped'
export const stageProgressLabels: Record<StageProgress,string> = {unmarked:'未记录',doing:'进行中',blocked:'受阻',done:'已完成',skipped:'已跳过'}
export const lifecycleStages = (Object.keys(stages) as Stage[]).filter(s => s !== 'unmarked')
export interface TaskRecord {id:string;specId:string;title:string;stage:Stage;status:Status;blocker:string;notes:string;resources:ResourceRef[];active:boolean;createdAt:string;updatedAt:string}
export type TaskEdit = Pick<TaskRecord,'title'|'stage'|'status'|'blocker'|'notes'|'resources'|'active'>
export interface TaskDraft {title:string;stage:Stage;resources:ResourceRef[]}
export interface TaskView {record:TaskRecord;resources:SpecView['resources']}
export interface StageRecord {specId:string;stage:Stage;progress:StageProgress;note:string;resources:ResourceRef[];updatedAt:string}
export interface WorkQuery {entity:WorkEntity;q:string;projectIds:string[];specIds:string[];stages:Stage[];statuses:Status[];activity:'active'|'inactive'|'all';includeClosed:boolean;sourceProblemsOnly:boolean;sort:'updatedDesc'|'titleAsc'}
export interface SavedView {id:string;name:string;query:WorkQuery;layout:Layout;groupBy:GroupBy;createdAt:string;updatedAt:string}
export interface WorkItem {entity:WorkEntity;id:string;projectId:string;projectName:string;specId:string;specTitle:string;sourcePath:string;title:string;stage:Stage;status:Status;nextAction:string;blocker:string;notes:string;active:boolean;updatedAt:string;hasSourceProblem:boolean}
export interface QueryResult {revision:number;readAt:string;total:number;items:WorkItem[]}
export type TaskCounts = Record<Status | 'total',number>
export interface LifecycleView {revision:number;spec:SpecView;stages:{stage:Stage;record:StageRecord|null;taskCounts:TaskCounts}[];tasks:TaskView[];events:ChangeEvent[]}
export function defaultWorkQuery():WorkQuery {return {entity:'spec',q:'',projectIds:[],specIds:[],stages:[],statuses:[],activity:'active',includeClosed:false,sourceProblemsOnly:false,sort:'updatedDesc'}}

export const contentStatuses = { unmarked: '未标记', draft: '草稿', ready: '可确认', confirmed: '已确认' } as const
export type ContentStatus = keyof typeof contentStatuses
export interface ContentQuestion { id: string; title: string; kind: 'research' | 'decision'; impact: string; recommendation: string; blocking: boolean; affectedScope: string; status: 'open' | 'resolved' | 'deferred'; resolution: string; appliedDecisionId: string | null }
export interface QuestionResponse { id: string; specId: string; questionId: string; kind: 'answer' | 'defer'; answer: string; reason: string; impact: string; excludedScope: string; reviewAt: string; reviewUtcOffsetMinutes: number; reviewWhen: string; questionDigest: string; createdAt: string }
export interface QuestionView { question: ContentQuestion; digest: string; state: 'open' | 'pendingWriteback' | 'resolved' | 'deferred' | 'overdue' | 'needsReview'; response: QuestionResponse | null }
export interface ConfirmationRecord { id: string; specId: string; scope: string; artifactDigest: string; createdAt: string }
export interface ContentView { status: ContentStatus; scope: string; artifactDigest: string | null; metadataError: string | null; questions: QuestionView[]; confirmations: ConfirmationRecord[]; canConfirm: boolean; blockers: string[]; needsReconfirmation: boolean }
export interface RespondQuestionAction { kind: 'respondQuestion'; specId: string; questionId: string; questionDigest: string; responseKind: 'answer' | 'defer'; answer: string; reason: string; impact: string; excludedScope: string; reviewAt: string; reviewUtcOffsetMinutes: number; reviewWhen: string }
export interface ProjectDocument extends SourceDocument { sourcePath: string }
export interface AgentRequest { specId: string; text: string; readAt: string }
