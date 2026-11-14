use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;

pub const MAX_STATE: u64 = 16 * 1024 * 1024;
pub const MAX_SOURCE: u64 = 2 * 1024 * 1024;
#[derive(
    Debug,
    Clone,
    Copy,
    Default,
    PartialEq,
    Eq,
    PartialOrd,
    Ord,
    Serialize,
    Deserialize,
    clap::ValueEnum,
)]
#[serde(rename_all = "camelCase")]
pub enum Stage {
    #[default]
    Unmarked,
    Research,
    Analysis,
    Spec,
    Prototype,
    Handoff,
    Implementation,
    Verification,
    Delivery,
}
impl Stage {
    pub const LIFECYCLE: [Self; 8] = [
        Self::Research,
        Self::Analysis,
        Self::Spec,
        Self::Prototype,
        Self::Handoff,
        Self::Implementation,
        Self::Verification,
        Self::Delivery,
    ];
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum Status {
    #[default]
    Unmarked,
    Todo,
    Doing,
    Blocked,
    Done,
    Canceled,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProjectRecord {
    pub id: String,
    pub name: String,
    pub root_path: String,
    pub active: bool,
    pub created_at: String,
    pub updated_at: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum ResourceKind {
    Supplement,
    Prototype,
    Interaction,
    Other,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum TargetType {
    Local,
    Web,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ResourceRef {
    pub kind: ResourceKind,
    pub label: String,
    pub target_type: TargetType,
    pub target: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SpecRecord {
    pub id: String,
    pub project_id: String,
    pub source_path: String,
    pub title: String,
    pub stage: Stage,
    pub status: Status,
    pub next_action: String,
    pub blocker: String,
    pub notes: String,
    pub resources: Vec<ResourceRef>,
    pub active: bool,
    pub created_at: String,
    pub updated_at: String,
    pub last_visited_at: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct FieldChange {
    pub before: Value,
    pub after: Value,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ChangeEvent {
    pub id: String,
    pub project_id: String,
    pub spec_id: Option<String>,
    pub task_id: Option<String>,
    pub lifecycle_stage: Option<Stage>,
    pub at: String,
    pub changes: BTreeMap<String, FieldChange>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct WorkbenchState {
    pub schema_version: u32,
    pub revision: u64,
    pub projects: Vec<ProjectRecord>,
    pub specs: Vec<SpecRecord>,
    pub history: Vec<ChangeEvent>,
    pub tasks: Vec<TaskRecord>,
    pub stage_records: Vec<StageRecord>,
    pub saved_views: Vec<SavedView>,
}
impl Default for WorkbenchState {
    fn default() -> Self {
        Self {
            schema_version: 2,
            revision: 0,
            projects: vec![],
            specs: vec![],
            history: vec![],
            tasks: vec![],
            stage_records: vec![],
            saved_views: vec![],
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct TaskRecord {
    pub id: String,
    pub spec_id: String,
    pub title: String,
    pub stage: Stage,
    pub status: Status,
    pub blocker: String,
    pub notes: String,
    pub resources: Vec<ResourceRef>,
    pub active: bool,
    pub created_at: String,
    pub updated_at: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct TaskDraft {
    pub title: String,
    pub stage: Stage,
    pub resources: Vec<ResourceRef>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct TaskEdit {
    pub title: String,
    pub stage: Stage,
    pub status: Status,
    pub blocker: String,
    pub notes: String,
    pub resources: Vec<ResourceRef>,
    pub active: bool,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum StageProgress {
    Unmarked,
    Doing,
    Blocked,
    Done,
    Skipped,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct StageRecord {
    pub spec_id: String,
    pub stage: Stage,
    pub progress: StageProgress,
    pub note: String,
    pub resources: Vec<ResourceRef>,
    pub updated_at: String,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum WorkEntity {
    #[default]
    Spec,
    Task,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum ActivityScope {
    #[default]
    Active,
    Inactive,
    All,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
#[value(rename_all = "camelCase")]
pub enum WorkSort {
    #[default]
    UpdatedDesc,
    TitleAsc,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum Layout {
    List,
    Board,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum GroupBy {
    Status,
    Stage,
}
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct WorkQuery {
    pub entity: WorkEntity,
    pub q: String,
    pub project_ids: Vec<String>,
    pub spec_ids: Vec<String>,
    pub stages: Vec<Stage>,
    pub statuses: Vec<Status>,
    pub activity: ActivityScope,
    pub include_closed: bool,
    pub source_problems_only: bool,
    pub sort: WorkSort,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SavedView {
    pub id: String,
    pub name: String,
    pub query: WorkQuery,
    pub layout: Layout,
    pub group_by: GroupBy,
    pub created_at: String,
    pub updated_at: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct WorkItem {
    pub entity: WorkEntity,
    pub id: String,
    pub project_id: String,
    pub project_name: String,
    pub spec_id: String,
    pub spec_title: String,
    pub source_path: String,
    pub title: String,
    pub stage: Stage,
    pub status: Status,
    pub next_action: String,
    pub blocker: String,
    pub notes: String,
    pub active: bool,
    pub updated_at: String,
    pub has_source_problem: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct QueryResult {
    pub revision: u64,
    pub read_at: String,
    pub total: usize,
    pub items: Vec<WorkItem>,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum SourceHealth {
    Available,
    Missing,
    Unreadable,
    Unsupported,
    Oversized,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum ResourceHealth {
    Available,
    Missing,
    Unreadable,
    Unsupported,
    External,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ResourceView {
    pub record: ResourceRef,
    pub health: ResourceHealth,
    pub message: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProjectView {
    pub record: ProjectRecord,
    pub source_directories: Vec<String>,
    pub available: bool,
    pub message: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SpecView {
    pub record: SpecRecord,
    pub source_health: SourceHealth,
    pub source_message: Option<String>,
    pub source_modified_at: Option<String>,
    pub resources: Vec<ResourceView>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Snapshot {
    pub revision: u64,
    pub read_at: String,
    pub projects: Vec<ProjectView>,
    pub specs: Vec<SpecView>,
    pub history: Vec<ChangeEvent>,
    pub tasks: Vec<TaskView>,
    pub stage_records: Vec<StageRecord>,
    pub saved_views: Vec<SavedView>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TaskView {
    pub record: TaskRecord,
    pub resources: Vec<ResourceView>,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TaskCounts {
    pub total: usize,
    pub unmarked: usize,
    pub todo: usize,
    pub doing: usize,
    pub blocked: usize,
    pub done: usize,
    pub canceled: usize,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct LifecycleStageView {
    pub stage: Stage,
    pub record: Option<StageRecord>,
    pub task_counts: TaskCounts,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct LifecycleView {
    pub revision: u64,
    pub spec: SpecView,
    pub stages: Vec<LifecycleStageView>,
    pub tasks: Vec<TaskView>,
    pub events: Vec<ChangeEvent>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DiscoverRequest {
    pub project_id: String,
    pub directory: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Candidate {
    pub source_path: String,
    pub title: String,
    pub modified_at: Option<String>,
    pub already_registered: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DiscoverResult {
    pub candidates: Vec<Candidate>,
    pub warnings: Vec<String>,
    pub complete: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SourceDocument {
    pub spec_id: String,
    pub text: String,
    pub digest: String,
    pub read_at: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct MutationRequest {
    pub expected_revision: u64,
    pub action: Action,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MutationResult {
    pub revision: u64,
    pub project_id: Option<String>,
    pub spec_id: Option<String>,
    pub task_ids: Vec<String>,
    pub view_id: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(
    tag = "kind",
    rename_all = "camelCase",
    rename_all_fields = "camelCase",
    deny_unknown_fields
)]
pub enum Action {
    SaveView {
        view_id: Option<String>,
        name: String,
        query: WorkQuery,
        layout: Layout,
        group_by: GroupBy,
    },
    RemoveView {
        view_id: String,
    },
    AddTasks {
        spec_id: String,
        drafts: Vec<TaskDraft>,
    },
    EditTask {
        task_id: String,
        edit: TaskEdit,
    },
    SetStageRecord {
        spec_id: String,
        stage: Stage,
        progress: StageProgress,
        note: String,
        resources: Vec<ResourceRef>,
    },
    SetWorkStatus {
        entity: WorkEntity,
        id: String,
        status: Status,
        blocker: String,
    },
    SetWorkStage {
        entity: WorkEntity,
        id: String,
        stage: Stage,
    },
    AddProject {
        root_path: String,
        name: Option<String>,
    },
    UpdateProject {
        project_id: String,
        name: String,
        root_path: String,
        active: bool,
    },
    AddSpec {
        project_id: String,
        source_path: String,
    },
    EditSpec {
        spec_id: String,
        source_path: String,
        title: String,
        stage: Stage,
        status: Status,
        next_action: String,
        blocker: String,
        notes: String,
        resources: Vec<ResourceRef>,
        active: bool,
    },
    VisitSpec {
        spec_id: String,
    },
}
