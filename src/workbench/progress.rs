//! Versioned lifecycle evidence and deterministic projection.
use super::{
    model::*,
    sources,
    sqlite::{CommitEffects, RecordWrite},
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::Result,
    fs::Root,
    model::{RelPath, Sha256},
};
use serde::{Deserialize, Serialize};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

use time::{OffsetDateTime, format_description::well_known::Rfc3339};

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum Progress {
    #[default]
    Unmarked,
    Todo,
    Doing,
    Blocked,
    Done,
    Skipped,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum ProgressBasis {
    #[default]
    Personal,
    Effective,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
#[value(rename_all = "camelCase")]
pub enum Validity {
    Current,
    #[default]
    Unverified,
    NeedsSync,
    NeedsReview,
}
#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, clap::ValueEnum,
)]
#[serde(rename_all = "camelCase")]
pub enum ParticipantRole {
    Spec,
    Backend,
    Frontend,
    Integration,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum DeliveryTargetKind {
    Local,
    Merged,
    Environment,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DeliveryTarget {
    pub kind: DeliveryTargetKind,
    pub label: String,
    #[serde(default)]
    pub reference: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct StageScope {
    pub stage: Stage,
    pub applicable: bool,
    #[serde(default)]
    pub reason: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Participant {
    pub project_id: String,
    pub role: ParticipantRole,
    pub label: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct LifecycleGoalInput {
    pub spec_id: String,
    pub scope: String,
    pub target: DeliveryTarget,
    #[serde(default)]
    pub stages: Vec<StageScope>,
    #[serde(default)]
    pub participants: Vec<Participant>,
    #[serde(default)]
    pub acceptance_refs: Vec<String>,
    pub spec_digest: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct GoalRecord {
    pub schema_version: u32,
    pub goal_version: String,
    pub created_at: String,
    #[serde(flatten)]
    pub goal: LifecycleGoalInput,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CancelGoalInput {
    pub spec_id: String,
    pub goal_version: String,
    pub reason: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CancellationRecord {
    pub schema_version: u32,
    pub spec_id: String,
    pub goal_version: String,
    pub reason: String,
    pub recorded_at: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReopenGoalInput {
    pub spec_id: String,
    pub goal_version: String,
    pub goal: LifecycleGoalInput,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReopenRecord {
    pub schema_version: u32,
    pub spec_id: String,
    pub goal_version: String,
    pub previous_goal_version: String,
    pub recorded_at: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct CurrentGoalRecord {
    schema_version: u32,
    spec_id: String,
    goal_version: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct GoalRoundRecord {
    schema_version: u32,
    spec_id: String,
    goal_version: String,
    round_id: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProjectVersion {
    pub project_id: String,
    #[serde(default)]
    pub commit: Option<String>,
    #[serde(default)]
    pub working_tree_digest: Option<String>,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum EvidenceKind {
    Document,
    Artifact,
    Command,
    Test,
    Merge,
    Environment,
    Acceptance,
    Other,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum EvidenceOutcome {
    Passed,
    Failed,
    Observed,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Evidence {
    pub kind: EvidenceKind,
    pub label: String,
    pub reference: String,
    #[serde(default)]
    pub outcome: Option<EvidenceOutcome>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProgressReportInput {
    pub id: String,
    pub spec_id: String,
    pub goal_version: String,
    pub spec_digest: String,
    pub stage: Stage,
    #[serde(default)]
    pub project_id: Option<String>,
    pub role: ParticipantRole,
    pub progress: Progress,
    pub validity: Validity,
    pub summary: String,
    #[serde(default)]
    pub blocker: String,
    #[serde(default)]
    pub next_action: String,
    #[serde(default)]
    pub scope_refs: Vec<String>,
    #[serde(default)]
    pub versions: Vec<ProjectVersion>,
    #[serde(default)]
    pub evidence: Vec<Evidence>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub input_paths: Vec<ProjectInputs>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProjectInputs {
    pub project_id: String,
    pub paths: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub observed_digest: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct InputObservation {
    pub inputs: ProjectInputs,
    pub version: ProjectVersion,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct CheckedInputs {
    project_id: String,
    digest: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReportRecord {
    pub schema_version: u32,
    pub recorded_at: String,
    #[serde(flatten)]
    pub report: ProgressReportInput,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    checked_inputs: Vec<CheckedInputs>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct LateReportRecord {
    pub schema_version: u32,
    pub spec_id: String,
    pub goal_version: String,
    pub report_id: String,
    pub input_limitations: Vec<String>,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum ConfirmationDecision {
    Confirm,
    Reject,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum DecisionOrigin {
    #[default]
    Manual,
    Automatic,
}
impl DecisionOrigin {
    fn is_manual(&self) -> bool {
        *self == Self::Manual
    }
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ConfirmationInput {
    pub spec_id: String,
    pub goal_version: String,
    pub stage: Stage,
    pub scope: String,
    pub evidence_digest: String,
    pub source: String,
    pub decision: ConfirmationDecision,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DecisionRecord {
    pub schema_version: u32,
    pub id: String,
    pub recorded_at: String,
    #[serde(default, skip_serializing_if = "DecisionOrigin::is_manual")]
    pub origin: DecisionOrigin,
    #[serde(default)]
    pub stage_evidence_digest: Option<String>,
    #[serde(flatten)]
    pub confirmation: ConfirmationInput,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct OverrideInput {
    pub spec_id: String,
    pub goal_version: String,
    pub stage: Stage,
    pub progress: Progress,
    pub reason: String,
    #[serde(default)]
    pub project_id: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ClearOverrideInput {
    pub spec_id: String,
    pub goal_version: String,
    pub stage: Stage,
    #[serde(default)]
    pub project_id: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct OverrideRecord {
    pub schema_version: u32,
    pub id: String,
    pub recorded_at: String,
    pub cleared_at: Option<String>,
    #[serde(flatten)]
    pub value: OverrideInput,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProgressRecords {
    pub goals: Vec<GoalRecord>,
    pub reports: Vec<ReportRecord>,
    pub overrides: Vec<OverrideRecord>,
    pub decisions: Vec<DecisionRecord>,
    #[serde(default)]
    pub cancellations: Vec<CancellationRecord>,
    #[serde(default)]
    pub reopens: Vec<ReopenRecord>,
    #[serde(default)]
    pub late_reports: Vec<LateReportRecord>,
    #[serde(default)]
    current_goals: Vec<CurrentGoalRecord>,
    #[serde(default)]
    goal_rounds: Vec<GoalRoundRecord>,
    #[serde(skip)]
    pub current_versions: Vec<ProjectVersion>,
    #[serde(skip)]
    checked_inputs: BTreeMap<(String, Vec<String>), Option<String>>,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum ProgressSource {
    Unmarked,
    Evidence,
    Content,
    Manual,
    Goal,
    Historical,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ParticipantProgress {
    pub project_id: String,
    pub role: ParticipantRole,
    pub label: String,
    pub progress: Progress,
    pub validity: Validity,
    pub source: ProgressSource,
    pub summary: String,
    pub blocker: String,
    pub next_action: String,
    pub evidence: Vec<Evidence>,
    pub report_id: Option<String>,
    pub last_verified_at: Option<String>,
    pub has_override: bool,
    pub conflicts: Vec<String>,
    pub versions: Vec<ProjectVersion>,
    pub current_version: Option<ProjectVersion>,
    pub override_record: Option<OverrideRecord>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct StageSummary {
    pub stage: Stage,
    pub applicable: bool,
    pub scope_reason: String,
    pub progress: Progress,
    pub validity: Validity,
    pub source: ProgressSource,
    pub summary: String,
    pub blocker: String,
    pub next_action: String,
    pub evidence: Vec<Evidence>,
    pub participants: Vec<ParticipantProgress>,
    pub confirmation_required: bool,
    pub confirmation: Option<DecisionRecord>,
    pub last_verified_at: Option<String>,
    pub has_override: bool,
    pub conflicts: Vec<String>,
    pub legacy: Option<StageRecord>,
    pub versions: Vec<ProjectVersion>,
    pub override_record: Option<OverrideRecord>,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProgressCompletion {
    pub completed: usize,
    pub total: usize,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProgressSummary {
    pub goal: Option<GoalRecord>,
    pub goal_version: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub round_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cancellation: Option<CancellationRecord>,
    pub stages: Vec<StageSummary>,
    pub participants: Vec<Participant>,
    pub active_stages: Vec<Stage>,
    pub primary_stage: Stage,
    pub status: Status,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub completion: Option<ProgressCompletion>,
    pub validity: Validity,
    pub attention: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub sync_evidence: Vec<EvidenceSync>,
    pub summary: String,
    pub blocker: String,
    pub next_action: String,
    pub last_verified_at: Option<String>,
    pub evidence_digest: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub auto_confirmable_stages: Vec<Stage>,
    pub has_override: bool,
    pub conflicts: Vec<String>,
    #[serde(default)]
    pub current_versions: Vec<ProjectVersion>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_delivery: Option<DecisionRecord>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub material_review: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EvidenceSync {
    pub stage: Stage,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub project_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub project_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub role: Option<ParticipantRole>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub report_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub recorded_at: Option<String>,
    pub reason: String,
    pub paths: Vec<String>,
    pub next_action: String,
}

pub(super) const SYNC_EVIDENCE_NEXT_ACTION: &str = "在现用工程版本核实并补报本轮证据";

const AUTOMATIC_DECISION_KIND: &str = "automaticProgressDecision";
const CANCELLATION_KIND: &str = "lifecycleGoalCancellation";
const REOPEN_KIND: &str = "lifecycleGoalReopen";
const CURRENT_GOAL_KIND: &str = "currentLifecycleGoal";
const LATE_REPORT_KIND: &str = "historicalProgressReport";
const GOAL_ROUND_KIND: &str = "lifecycleGoalRound";

pub const RECORD_KINDS: &[&str] = &[
    "lifecycleGoal",
    "progressEvidence",
    "progressOverride",
    "progressDecision",
    AUTOMATIC_DECISION_KIND,
    CANCELLATION_KIND,
    REOPEN_KIND,
    CURRENT_GOAL_KIND,
    LATE_REPORT_KIND,
    GOAL_ROUND_KIND,
];

impl ProgressRecords {
    pub fn from_raw(raw: &BTreeMap<String, Vec<(String, String)>>) -> Result<Self> {
        fn decode<T: serde::de::DeserializeOwned>(
            raw: &BTreeMap<String, Vec<(String, String)>>,
            kind: &str,
        ) -> Result<Vec<T>> {
            raw.get(kind)
                .into_iter()
                .flatten()
                .map(|(_, text)| {
                    serde_json::from_str(text)
                        .map_err(|_| sources::invalid("生命周期扩展记录格式无效"))
                })
                .collect()
        }
        let mut decisions = decode(raw, RECORD_KINDS[3])?;
        decisions.extend(decode(raw, AUTOMATIC_DECISION_KIND)?);
        let records = Self {
            goals: decode(raw, RECORD_KINDS[0])?,
            reports: decode(raw, RECORD_KINDS[1])?,
            overrides: decode(raw, RECORD_KINDS[2])?,
            decisions,
            cancellations: decode(raw, CANCELLATION_KIND)?,
            reopens: decode(raw, REOPEN_KIND)?,
            current_goals: decode(raw, CURRENT_GOAL_KIND)?,
            goal_rounds: decode(raw, GOAL_ROUND_KIND)?,
            late_reports: decode(raw, LATE_REPORT_KIND)?,
            current_versions: Vec::new(),
            checked_inputs: BTreeMap::new(),
        };
        records.validate_schema()?;
        Ok(records)
    }
    fn validate_schema(&self) -> Result<()> {
        if self.goals.iter().any(|r| r.schema_version != 1)
            || self.reports.iter().any(|r| r.schema_version != 1)
            || self.overrides.iter().any(|r| r.schema_version != 1)
            || self.decisions.iter().any(|r| r.schema_version != 1)
            || self.cancellations.iter().any(|r| r.schema_version != 1)
            || self.reopens.iter().any(|r| r.schema_version != 1)
            || self.current_goals.iter().any(|r| r.schema_version != 1)
            || self.goal_rounds.iter().any(|r| r.schema_version != 1)
            || self.late_reports.iter().any(|r| r.schema_version != 1)
        {
            return Err(sources::invalid("不支持的生命周期记录版本"));
        }
        Ok(())
    }
}

pub fn load(store: &mut Store) -> Result<ProgressRecords> {
    let mut decisions = store.records(RECORD_KINDS[3])?;
    decisions.extend(store.records(AUTOMATIC_DECISION_KIND)?);
    let records = ProgressRecords {
        goals: store.records(RECORD_KINDS[0])?,
        reports: store.records(RECORD_KINDS[1])?,
        overrides: store.records(RECORD_KINDS[2])?,
        decisions,
        cancellations: store.records(CANCELLATION_KIND)?,
        reopens: store.records(REOPEN_KIND)?,
        current_goals: store.records(CURRENT_GOAL_KIND)?,
        goal_rounds: store.records(GOAL_ROUND_KIND)?,
        late_reports: store.records(LATE_REPORT_KIND)?,
        current_versions: Vec::new(),
        checked_inputs: BTreeMap::new(),
    };
    records.validate_schema()?;
    Ok(records)
}

pub(crate) fn include_pending(
    records: &mut ProgressRecords,
    effects: &CommitEffects,
) -> Result<()> {
    fn decode<T: serde::de::DeserializeOwned>(write: &RecordWrite) -> Result<T> {
        let invalid = || sources::invalid("待保存的生命周期记录格式无效");
        let payload = write.payload.as_deref().ok_or_else(invalid)?;
        serde_json::from_str(payload).map_err(|_| invalid())
    }
    for write in &effects.records {
        let kind = write.kind.as_str();
        if kind == RECORD_KINDS[0] {
            let goal: GoalRecord = decode(write)?;
            records.goals.retain(|old| {
                old.goal.spec_id != goal.goal.spec_id || old.goal_version != goal.goal_version
            });
            records.goals.push(goal);
        } else if kind == RECORD_KINDS[1] {
            records.reports.retain(|old| old.report.id != write.id);
            records.reports.push(decode(write)?);
        } else if kind == RECORD_KINDS[2] {
            records.overrides.retain(|old| old.id != write.id);
            records.overrides.push(decode(write)?);
        } else if kind == RECORD_KINDS[3] || kind == AUTOMATIC_DECISION_KIND {
            records.decisions.retain(|old| old.id != write.id);
            records.decisions.push(decode(write)?);
        } else if kind == CANCELLATION_KIND {
            records.cancellations.push(decode(write)?);
        } else if kind == REOPEN_KIND {
            records.reopens.push(decode(write)?);
        } else if kind == CURRENT_GOAL_KIND {
            records.current_goals.retain(|old| old.spec_id != write.id);
            records.current_goals.push(decode(write)?);
        } else if kind == LATE_REPORT_KIND {
            records.late_reports.push(decode(write)?);
        } else if kind == GOAL_ROUND_KIND {
            records.goal_rounds.push(decode(write)?);
        }
    }
    Ok(())
}

/// Read only participating repositories. An unavailable repository stays unknown.
pub fn current_versions(
    state: &WorkbenchState,
    project_ids: &BTreeSet<String>,
) -> Vec<ProjectVersion> {
    current_versions_with_cancel(state, project_ids, &Cancellation::new())
}

pub(super) fn current_versions_with_cancel(
    state: &WorkbenchState,
    project_ids: &BTreeSet<String>,
    cancel: &Cancellation,
) -> Vec<ProjectVersion> {
    let mut versions = BTreeMap::new();
    state
        .projects
        .iter()
        .filter(|p| project_ids.contains(&p.id))
        .map(|project| {
            let cached = versions
                .entry(project.root_path.as_str())
                .or_insert_with(|| {
                    read_project_version(project, cancel).unwrap_or_else(|_| ProjectVersion {
                        project_id: project.id.clone(),
                        commit: None,
                        working_tree_digest: None,
                    })
                });
            ProjectVersion {
                project_id: project.id.clone(),
                ..cached.clone()
            }
        })
        .collect()
}

fn read_project_version(project: &ProjectRecord, cancel: &Cancellation) -> Result<ProjectVersion> {
    use std::time::Duration;
    let root = Root::open(Path::new(&project.root_path))?;
    let git = |args: &[&str]| {
        let mut command = std::process::Command::new("git");
        command
            .arg("--no-optional-locks")
            .arg("-C")
            .arg(&project.root_path)
            .args(args);
        super::process::run_command(command, cancel, Duration::from_secs(5))
    };
    let head = git(&["rev-parse", "--verify", "HEAD"])?;
    let commit = String::from_utf8(head)
        .map_err(|_| sources::invalid("Git 版本不是 UTF-8"))?
        .trim()
        .to_owned();
    if !valid_commit(&commit) {
        return Err(sources::invalid("Git 版本无效"));
    }
    let tracked = git(&[
        "diff",
        "--no-ext-diff",
        "--no-textconv",
        "--binary",
        "HEAD",
        "--",
    ])?;
    let mut bytes = tracked.clone();
    let names = git(&["ls-files", "--others", "--exclude-standard", "-z"])?;
    for name in names.split(|b| *b == 0).filter(|n| !n.is_empty()) {
        cancel.check()?;
        let name =
            std::str::from_utf8(name).map_err(|_| sources::invalid("Git 文件名不是 UTF-8"))?;
        let checked = root.read_checked(&RelPath::new(name)?, 16 * 1024 * 1024)?;
        bytes.extend_from_slice(&(name.len() as u64).to_be_bytes());
        bytes.extend_from_slice(name.as_bytes());
        bytes.extend_from_slice(Sha256::of(&checked.bytes).as_str().as_bytes());
    }
    // Recheck HEAD and tracked/untracked names so a concurrent checkout does not look current.
    if git(&["rev-parse", "--verify", "HEAD"])? != format!("{commit}\n").as_bytes()
        || git(&["ls-files", "--others", "--exclude-standard", "-z"])? != names
        || git(&[
            "diff",
            "--no-ext-diff",
            "--no-textconv",
            "--binary",
            "HEAD",
            "--",
        ])? != tracked
    {
        return Err(sources::invalid("读取期间工程版本变化，请重新核实"));
    }
    root.revalidate()?;
    Ok(ProjectVersion {
        project_id: project.id.clone(),
        commit: Some(commit),
        working_tree_digest: Some(Sha256::of(&bytes).as_str().to_owned()),
    })
}

fn inputs_digest(root: &Root, paths: &[String], cancel: &Cancellation) -> Result<String> {
    let mut pending = paths.iter().map(RelPath::new).collect::<Result<Vec<_>>>()?;
    let mut entries = BTreeMap::<String, (u8, Option<(String, u32)>)>::new();
    let mut budget = 4096;
    let mut total = 0;
    while let Some(path) = pending.pop() {
        cancel.check()?;
        if entries.contains_key(path.as_str()) {
            continue;
        }
        validate_input_path(&path)?;
        let metadata = match root.source_metadata(&path) {
            Ok(metadata) => metadata,
            Err(e) if e.context.get("kind").is_some_and(|kind| kind == "NotFound") => {
                entries.insert(path.to_string(), (0, None));
                continue;
            }
            Err(e) => return Err(e),
        };
        if metadata.is_dir() {
            let (children, complete) = root.source_entries(Some(&path), &mut budget, cancel)?;
            if !complete {
                return Err(sources::invalid("凭证范围内文件过多，无法完整核实"));
            }
            for (name, _) in children {
                pending.push(path.join(&name)?);
            }
            entries.insert(path.to_string(), (1, None));
        } else if metadata.is_file() {
            let file = root.source_read(&path, 16 * 1024 * 1024)?;
            total += file.bytes.len();
            if total > 128 * 1024 * 1024 {
                return Err(sources::invalid("凭证范围内文件超过 128 MiB"));
            }
            let descriptor = file
                .snapshot
                .descriptor()
                .ok_or_else(|| sources::invalid("凭证文件无法核实"))?;
            entries.insert(
                path.to_string(),
                (
                    2,
                    Some((descriptor.sha256.as_str().into(), descriptor.mode)),
                ),
            );
        } else {
            return Err(sources::invalid("凭证范围内存在不可核实的文件类型"));
        }
    }
    root.revalidate()?;
    Ok(digest(&(&root.path, entries)))
}

pub(super) fn refresh_inputs(
    records: &mut ProgressRecords,
    state: &WorkbenchState,
    cancel: &Cancellation,
) {
    let current: BTreeSet<_> = state
        .specs
        .iter()
        .filter_map(|spec| goal(records, &spec.id).map(|goal| (&spec.id, &goal.goal_version)))
        .collect();
    let mut inputs = BTreeMap::new();
    for report in records.reports.iter().filter(|r| {
        current.contains(&(&r.report.spec_id, &r.report.goal_version))
            && !is_late_report(records, r)
    }) {
        for scope in &report.report.input_paths {
            inputs
                .entry((scope.project_id.clone(), scope.paths.clone()))
                .or_insert_with(|| {
                    let project = sources::project(state, &scope.project_id).ok()?;
                    let root = Root::open(Path::new(&project.root_path)).ok()?;
                    inputs_digest(&root, &scope.paths, cancel).ok()
                });
        }
    }
    records.checked_inputs = inputs;
}

pub(super) fn input_versions_digest(records: &ProgressRecords) -> String {
    digest(&records.checked_inputs.iter().collect::<Vec<_>>())
}

fn validate_input_paths(paths: &[String]) -> Result<()> {
    if paths.is_empty()
        || paths.len() > 128
        || paths
            .iter()
            .map(|path| crate::fs::alias_key(path))
            .collect::<BTreeSet<_>>()
            .len()
            != paths.len()
    {
        return Err(sources::invalid(
            "凭证输入须按工程登记 1 至 128 个不同的相对路径",
        ));
    }
    for path in paths {
        validate_input_path(&RelPath::new(path)?)?;
    }
    Ok(())
}

fn validate_input_path(path: &RelPath) -> Result<()> {
    if path
        .as_str()
        .split('/')
        .any(|p| matches!(p, ".git" | ".yss" | "node_modules"))
    {
        return Err(sources::invalid("凭证范围包含版本库或本机工具数据"));
    }
    Ok(())
}

fn verified_input_digest(
    project: &ProjectRecord,
    paths: &[String],
    version: &ProjectVersion,
    cancel: &Cancellation,
) -> Result<String> {
    let root = Root::open(Path::new(&project.root_path))?;
    let hash = inputs_digest(&root, paths, cancel)?;
    if read_project_version(project, cancel)? != *version
        || inputs_digest(&root, paths, cancel)? != hash
    {
        return Err(
            sources::invalid("采集期间凭证输入发生变化，请核实后重试").context("httpStatus", 409)
        );
    }
    Ok(hash)
}

pub fn observe_inputs(
    data_dir: &Path,
    project_id: &str,
    paths: &[String],
    cancel: &Cancellation,
) -> Result<InputObservation> {
    validate_input_paths(paths)?;
    let state = super::store::read_state(data_dir)?;
    let project = sources::project(&state, project_id)?;
    let version = read_project_version(project, cancel)?;
    let hash = verified_input_digest(project, paths, &version, cancel)?;
    Ok(InputObservation {
        inputs: ProjectInputs {
            project_id: project_id.into(),
            paths: paths.to_vec(),
            observed_digest: Some(hash),
        },
        version,
    })
}

fn capture_report_inputs(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: &ProgressReportInput,
    history_only: bool,
) -> Result<(Vec<CheckedInputs>, Vec<String>)> {
    let mut projects = BTreeSet::new();
    let mut checked = Vec::new();
    let mut limitations = Vec::new();
    for scope in &input.input_paths {
        validate_input_paths(&scope.paths)?;
        if !projects.insert(&scope.project_id) {
            return Err(sources::invalid("每个工程只能登记一组凭证输入"));
        }
        let observed = scope
            .observed_digest
            .as_deref()
            .filter(|digest| valid_digest(digest))
            .ok_or_else(|| {
                sources::invalid(
                    "须提供检查时采集的凭证输入摘要，请在执行检查前使用 observe-inputs",
                )
            })?;
        let version = input
            .versions
            .iter()
            .find(|v| v.project_id == scope.project_id)
            .ok_or_else(|| sources::invalid("凭证输入须绑定报告实际采用的工程版本"))?;
        let verify = || {
            let project = snapshot
                .projects
                .iter()
                .find(|p| p.record.id == scope.project_id)
                .ok_or_else(|| sources::invalid("凭证输入工程未登记"))?;
            let before = records
                .current_versions
                .iter()
                .find(|v| v.project_id == scope.project_id)
                .filter(|actual| versions_match(version, actual))
                .ok_or_else(|| {
                    sources::invalid("报告版本与当前可读取的工程不同，无法采集输入范围")
                        .context("httpStatus", 409)
                })?;
            let hash =
                verified_input_digest(&project.record, &scope.paths, before, &Cancellation::new())?;
            if hash != observed {
                return Err(sources::invalid(
                    "当前输入与实际检查时的摘要不同，请对当前版本重新检查",
                )
                .context("httpStatus", 409));
            }
            Ok(CheckedInputs {
                project_id: scope.project_id.clone(),
                digest: hash,
            })
        };
        match verify() {
            Ok(item) => checked.push(item),
            Err(error) if history_only => limitations.push(format!(
                "{} 的原检查输入无法复核：{}",
                scope.project_id, error.message
            )),
            Err(error) => return Err(error),
        }
    }
    if history_only {
        for version in &input.versions {
            if !projects.contains(&version.project_id)
                && !records.current_versions.iter().any(|actual| {
                    actual.project_id == version.project_id && versions_match(version, actual)
                })
            {
                limitations.push(format!("{} 的原工程版本无法复核", version.project_id));
            }
        }
    }
    Ok((checked, limitations))
}

fn digest<T: Serialize>(value: &T) -> String {
    Sha256::of(&serde_json::to_vec(value).expect("lifecycle values serialize"))
        .as_str()
        .to_owned()
}
fn write<T: Serialize>(kind: &str, id: String, value: &T) -> Result<CommitEffects> {
    Ok(CommitEffects {
        records: vec![RecordWrite {
            kind: kind.into(),
            id,
            payload: Some(
                serde_json::to_string(value)
                    .map_err(|_| sources::invalid("生命周期记录编码失败"))?,
            ),
        }],
        ..Default::default()
    })
}
fn required(value: &str, message: &str) -> Result<()> {
    if value.trim().is_empty() || value.len() > 16_384 || value.contains('\0') {
        return Err(sources::invalid(message));
    }
    Ok(())
}
fn valid_digest(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
}
fn valid_commit(value: &str) -> bool {
    matches!(value.len(), 40 | 64)
        && value
            .bytes()
            .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
}
fn valid_stage(stage: Stage) -> Result<()> {
    if stage == Stage::Unmarked {
        return Err(sources::invalid("请选择具体生命周期阶段"));
    }
    Ok(())
}
fn ordered_time(value: &str) -> (Option<OffsetDateTime>, &str) {
    (OffsetDateTime::parse(value, &Rfc3339).ok(), value)
}
fn decision_order(record: &DecisionRecord) -> ((Option<OffsetDateTime>, &str), &str) {
    (ordered_time(&record.recorded_at), &record.id)
}
fn spec_view<'a>(snapshot: &'a Snapshot, id: &str) -> Result<&'a SpecView> {
    snapshot
        .specs
        .iter()
        .find(|s| s.record.id == id)
        .ok_or_else(|| sources::missing("Spec 不存在"))
}
pub(super) fn goal<'a>(records: &'a ProgressRecords, spec_id: &str) -> Option<&'a GoalRecord> {
    let selected = records
        .current_goals
        .iter()
        .find(|record| record.spec_id == spec_id);
    select_goal(&records.goals, spec_id, selected)
}
fn initial_round_id(spec_id: &str) -> String {
    digest(&("initial-round", spec_id))
}
pub(super) fn goal_round_id(records: &ProgressRecords, goal: &GoalRecord) -> String {
    if let Some(record) = records.goal_rounds.iter().find(|record| {
        record.spec_id == goal.goal.spec_id && record.goal_version == goal.goal_version
    }) {
        return record.round_id.clone();
    }
    // The original writer used the input hash only for versions in the initial round.
    if goal.goal_version == digest(&goal.goal) {
        return initial_round_id(&goal.goal.spec_id);
    }
    goal.goal_version.clone()
}
fn select_goal<'a>(
    goals: &'a [GoalRecord],
    spec_id: &str,
    selected: Option<&CurrentGoalRecord>,
) -> Option<&'a GoalRecord> {
    let mut candidates = goals.iter().filter(|g| g.goal.spec_id == spec_id);
    if let Some(selected) = selected {
        return candidates.find(|g| g.goal_version == selected.goal_version);
    }
    candidates.max_by_key(|g| (ordered_time(&g.created_at), g.goal_version.as_str()))
}
pub(super) fn load_goal_version(store: &mut Store, spec_id: &str) -> Result<Option<String>> {
    if let Some(selected) = store.record::<CurrentGoalRecord>(CURRENT_GOAL_KIND, spec_id)? {
        return Ok(store
            .record::<GoalRecord>(
                RECORD_KINDS[0],
                &format!("{spec_id}:{}", selected.goal_version),
            )?
            .map(|record| record.goal_version));
    }
    let goals = store.records(RECORD_KINDS[0])?;
    Ok(select_goal(&goals, spec_id, None).map(|record| record.goal_version.clone()))
}
fn cancellation<'a>(
    records: &'a ProgressRecords,
    spec_id: &str,
    version: &str,
) -> Option<&'a CancellationRecord> {
    records
        .cancellations
        .iter()
        .find(|record| record.spec_id == spec_id && record.goal_version == version)
}
pub(super) fn is_goal_canceled(records: &ProgressRecords, spec_id: &str, version: &str) -> bool {
    cancellation(records, spec_id, version).is_some()
}
pub(super) fn load_goal_canceled(store: &Store, spec_id: &str, version: &str) -> Result<bool> {
    store.has_record(CANCELLATION_KIND, &format!("{spec_id}:{version}"))
}
fn current_goal<'a>(
    records: &'a ProgressRecords,
    spec_id: &str,
    version: &str,
) -> Result<&'a GoalRecord> {
    let goal =
        goal(records, spec_id).ok_or_else(|| sources::invalid("请先设置此 Spec 的生命周期目标"))?;
    if goal.goal_version != version {
        return Err(sources::invalid("目标版本已变化，请刷新后继续").context("httpStatus", 409));
    }
    Ok(goal)
}
fn active_goal<'a>(
    records: &'a ProgressRecords,
    spec_id: &str,
    version: &str,
) -> Result<&'a GoalRecord> {
    let goal = current_goal(records, spec_id, version)?;
    if is_goal_canceled(records, spec_id, version) {
        return Err(
            sources::invalid("当前目标已取消，请开启新一轮后继续").context("httpStatus", 409)
        );
    }
    Ok(goal)
}
fn project_exists(snapshot: &Snapshot, id: &str) -> bool {
    snapshot.projects.iter().any(|p| p.record.id == id)
}

pub(crate) fn prepare_goal(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: LifecycleGoalInput,
    at: &str,
) -> Result<CommitEffects> {
    validate_goal_input(snapshot, &input)?;
    let current = goal(records, &input.spec_id);
    if let Some(current) = current {
        active_goal(records, &input.spec_id, &current.goal_version)?;
        if current.goal == input {
            return Ok(CommitEffects::default());
        }
    }
    let (version, round_id) = current.map_or_else(
        || (digest(&input), initial_round_id(&input.spec_id)),
        |current| {
            (
                digest(&("set-goal", &current.goal_version, &input)),
                goal_round_id(records, current),
            )
        },
    );
    write_goal(input, version, round_id, at)
}

fn validate_goal_input(snapshot: &Snapshot, input: &LifecycleGoalInput) -> Result<()> {
    let spec = spec_view(snapshot, &input.spec_id)?;
    required(&input.scope, "目标范围不能为空或过长")?;
    required(&input.target.label, "交付目标说明不能为空或过长")?;
    if input.target.kind == DeliveryTargetKind::Environment
        && input
            .target
            .reference
            .as_deref()
            .is_none_or(|r| r.trim().is_empty())
    {
        return Err(sources::invalid("环境验证目标需要环境引用"));
    }
    if !valid_digest(&input.spec_digest)
        || spec.content.artifact_digest.as_deref() != Some(input.spec_digest.as_str())
    {
        return Err(
            sources::invalid("规格摘要已变化，请重新读取目标依据").context("httpStatus", 409)
        );
    }
    if input.stages.is_empty() || input.stages.len() > Stage::LIFECYCLE.len() {
        return Err(sources::invalid("目标至少需要一个明确阶段，最多八个阶段"));
    }
    let mut stages = BTreeSet::new();
    for scope in &input.stages {
        valid_stage(scope.stage)?;
        if !stages.insert(scope.stage) {
            return Err(sources::invalid("目标阶段重复"));
        }
        if !scope.applicable {
            required(&scope.reason, "不适用阶段需要说明范围理由")?;
        }
    }
    if !input.stages.iter().any(|s| s.applicable) {
        return Err(sources::invalid("目标需要至少一个适用阶段"));
    }
    let mut participants = BTreeSet::new();
    for participant in &input.participants {
        required(&participant.label, "参与工程需要名称")?;
        if !project_exists(snapshot, &participant.project_id)
            || !participants.insert((&participant.project_id, participant.role))
        {
            return Err(sources::invalid("参与工程不存在或角色重复"));
        }
        if participant.role == ParticipantRole::Spec
            && participant.project_id != spec.record.project_id
        {
            return Err(sources::invalid("Spec 角色应绑定当前规格所属工程"));
        }
    }
    for reference in &input.acceptance_refs {
        required(reference, "验收依据不能为空")?;
    }
    Ok(())
}

fn write_goal(
    input: LifecycleGoalInput,
    goal_version: String,
    round_id: String,
    at: &str,
) -> Result<CommitEffects> {
    let record = GoalRecord {
        schema_version: 1,
        goal_version: goal_version.clone(),
        created_at: at.into(),
        goal: input,
    };
    let spec_id = &record.goal.spec_id;
    let mut effects = write(
        RECORD_KINDS[0],
        format!("{spec_id}:{goal_version}"),
        &record,
    )?;
    let selected = CurrentGoalRecord {
        schema_version: 1,
        spec_id: spec_id.clone(),
        goal_version: goal_version.clone(),
    };
    effects
        .records
        .extend(write(CURRENT_GOAL_KIND, spec_id.clone(), &selected)?.records);
    let round = GoalRoundRecord {
        schema_version: 1,
        spec_id: spec_id.clone(),
        goal_version: goal_version.clone(),
        round_id,
    };
    effects
        .records
        .extend(write(GOAL_ROUND_KIND, format!("{spec_id}:{goal_version}"), &round)?.records);
    Ok(effects)
}

pub(crate) fn prepare_cancel_goal(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: CancelGoalInput,
    at: &str,
) -> Result<CommitEffects> {
    let spec = spec_view(snapshot, &input.spec_id)?;
    active_goal(records, &input.spec_id, &input.goal_version)?;
    if project_one(snapshot, records, spec).status == Status::Done {
        return Err(sources::invalid("当前目标已完成，可开启新一轮").context("httpStatus", 409));
    }
    required(&input.reason, "取消本轮目标需要非空原因")?;
    if let Some(source) = &input.source {
        required(source, "取消来源不能为空或过长")?;
    }
    let record = CancellationRecord {
        schema_version: 1,
        spec_id: input.spec_id,
        goal_version: input.goal_version,
        reason: input.reason.trim().to_owned(),
        recorded_at: at.into(),
        source: input.source,
    };
    write(
        CANCELLATION_KIND,
        format!("{}:{}", record.spec_id, record.goal_version),
        &record,
    )
}

pub(crate) fn prepare_reopen_goal(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: ReopenGoalInput,
    at: &str,
) -> Result<CommitEffects> {
    let spec = spec_view(snapshot, &input.spec_id)?;
    current_goal(records, &input.spec_id, &input.goal_version)?;
    if !matches!(
        project_one(snapshot, records, spec).status,
        Status::Done | Status::Canceled
    ) {
        return Err(sources::invalid("当前目标尚未结束，无法开启新一轮").context("httpStatus", 409));
    }
    if input.spec_id != input.goal.spec_id {
        return Err(sources::invalid("新目标必须和当前目标属于同一 Spec"));
    }
    validate_goal_input(snapshot, &input.goal)?;
    let version = digest(&("reopen", &input.goal_version, &input.goal));
    let record = ReopenRecord {
        schema_version: 1,
        spec_id: input.spec_id,
        goal_version: version.clone(),
        previous_goal_version: input.goal_version,
        recorded_at: at.into(),
    };
    let mut effects = write_goal(input.goal, version.clone(), version, at)?;
    effects.records.extend(
        write(
            REOPEN_KIND,
            format!("{}:{}", record.spec_id, record.goal_version),
            &record,
        )?
        .records,
    );
    Ok(effects)
}

pub(crate) fn prepare_report(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: ProgressReportInput,
    at: &str,
) -> Result<CommitEffects> {
    let spec = spec_view(snapshot, &input.spec_id)?;
    required(&input.id, "进度报告需要幂等标识")?;
    required(&input.summary, "进度报告需要摘要")?;
    valid_stage(input.stage)?;
    if let Some(existing) = records.reports.iter().find(|r| r.report.id == input.id) {
        return if existing.report == input {
            Ok(CommitEffects::default())
        } else {
            Err(sources::invalid("相同报告标识已用于不同内容").context("httpStatus", 409))
        };
    }
    let target = records
        .goals
        .iter()
        .find(|g| g.goal.spec_id == input.spec_id && g.goal_version == input.goal_version)
        .ok_or_else(|| {
            sources::invalid(
                "报告引用的目标版本不存在。运行 `yss workbench lifecycle --spec <该 Spec ID> --json`，如使用自定义目录，在 `workbench` 后沿用原 `--data-dir`。从 `result.progress.goalVersion` 读取当前目标版本。历史目标可查 `result.progressHistory.goals`。",
            )
        })?;
    let history_only = is_goal_canceled(records, &input.spec_id, &input.goal_version)
        || goal(records, &input.spec_id)
            .is_none_or(|current| current.goal_version != input.goal_version);
    if input.spec_digest != target.goal.spec_digest {
        return Err(sources::invalid(
            "报告规格摘要与引用目标不一致。运行 `yss workbench lifecycle --spec <该 Spec ID> --json`，如使用自定义目录，在 `workbench` 后沿用原 `--data-dir`。当前目标的摘要取 `result.progress.goal.specDigest`。历史目标在 `result.progressHistory.goals` 中按报告的 `goalVersion` 核对 `specDigest`。",
        ));
    }
    if input.stage != Stage::Delivery
        && !target
            .goal
            .stages
            .iter()
            .any(|s| s.stage == input.stage && s.applicable)
    {
        return Err(sources::invalid("报告阶段未纳入引用目标"));
    }
    match (&input.project_id, input.role) {
        (None, ParticipantRole::Spec) => {}
        (Some(id), ParticipantRole::Spec) if id == &spec.record.project_id => {}
        (Some(id), role)
            if target
                .goal
                .participants
                .iter()
                .any(|p| &p.project_id == id && p.role == role) => {}
        _ => return Err(sources::invalid("报告角色与目标参与工程不匹配")),
    }
    if input.progress == Progress::Blocked {
        required(&input.blocker, "阻塞报告需要具体原因")?;
    }
    if input.progress == Progress::Skipped && input.scope_refs.is_empty() {
        return Err(sources::invalid("跳过阶段需要明确范围引用与摘要理由"));
    }
    let mut versions = BTreeSet::new();
    for version in &input.versions {
        if !versions.insert(&version.project_id)
            || !history_only && !project_exists(snapshot, &version.project_id)
            || (version.project_id != spec.record.project_id
                && !target
                    .goal
                    .participants
                    .iter()
                    .any(|p| p.project_id == version.project_id))
        {
            return Err(sources::invalid("报告工程版本未绑定到引用目标或重复"));
        }
        if version.commit.as_deref().is_some_and(|v| !valid_commit(v))
            || version
                .working_tree_digest
                .as_deref()
                .is_some_and(|v| !valid_digest(v))
            || (version.commit.is_none() && version.working_tree_digest.is_none())
        {
            return Err(sources::invalid(
                "报告工程版本需要完整 commit 或工作树 SHA-256 摘要",
            ));
        }
    }
    for evidence in &input.evidence {
        required(&evidence.label, "证据需要说明")?;
        required(&evidence.reference, "证据需要实际引用")?;
    }
    for scope in &input.scope_refs {
        required(scope, "范围引用不能为空")?;
    }
    for id in reused_report_ids(&input) {
        if !records.reports.iter().any(|record| {
            record.report.id == id
                && record.report.spec_id == input.spec_id
                && record.report.goal_version != input.goal_version
                && !is_activity_report(id)
        }) {
            return Err(sources::invalid("引用的原报告不属于此 Spec 的历史目标"));
        }
    }
    let (checked_inputs, input_limitations) =
        capture_report_inputs(snapshot, records, &input, history_only)?;
    let historical = history_only.then(|| LateReportRecord {
        schema_version: 1,
        spec_id: input.spec_id.clone(),
        goal_version: input.goal_version.clone(),
        report_id: input.id.clone(),
        input_limitations,
    });
    let mut effects = write(
        RECORD_KINDS[1],
        input.id.clone(),
        &ReportRecord {
            schema_version: 1,
            recorded_at: at.into(),
            report: input,
            checked_inputs,
        },
    )?;
    if let Some(historical) = historical {
        effects
            .records
            .extend(write(LATE_REPORT_KIND, historical.report_id.clone(), &historical)?.records);
    }
    Ok(effects)
}

fn validate_override_target(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    spec_id: &str,
    version: &str,
    stage: Stage,
    project_id: Option<&str>,
) -> Result<()> {
    spec_view(snapshot, spec_id)?;
    valid_stage(stage)?;
    let goal = active_goal(records, spec_id, version)?;
    if !goal
        .goal
        .stages
        .iter()
        .any(|s| s.stage == stage && s.applicable)
    {
        return Err(sources::invalid("人工更正阶段未纳入当前目标"));
    }
    if project_id.is_some_and(|id| !goal.goal.participants.iter().any(|p| p.project_id == id)) {
        return Err(sources::invalid("人工更正工程未绑定当前目标"));
    }
    Ok(())
}
pub(crate) fn prepare_override(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: OverrideInput,
    at: &str,
) -> Result<CommitEffects> {
    validate_override_target(
        snapshot,
        records,
        &input.spec_id,
        &input.goal_version,
        input.stage,
        input.project_id.as_deref(),
    )?;
    required(&input.reason, "人工更正需要具体原因")?;
    let id = digest(&(at, &input));
    write(
        RECORD_KINDS[2],
        id.clone(),
        &OverrideRecord {
            schema_version: 1,
            id,
            recorded_at: at.into(),
            cleared_at: None,
            value: input,
        },
    )
}
pub(crate) fn prepare_clear_override(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: ClearOverrideInput,
    at: &str,
) -> Result<CommitEffects> {
    validate_override_target(
        snapshot,
        records,
        &input.spec_id,
        &input.goal_version,
        input.stage,
        input.project_id.as_deref(),
    )?;
    let mut effects = CommitEffects::default();
    for existing in records.overrides.iter().filter(|r| {
        r.cleared_at.is_none()
            && r.value.spec_id == input.spec_id
            && r.value.goal_version == input.goal_version
            && r.value.stage == input.stage
            && r.value.project_id == input.project_id
    }) {
        let mut cleared = existing.clone();
        cleared.cleared_at = Some(at.into());
        effects
            .records
            .extend(write(RECORD_KINDS[2], cleared.id.clone(), &cleared)?.records);
    }
    Ok(effects)
}

pub(crate) fn prepare_confirmation(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: ConfirmationInput,
    at: &str,
) -> Result<CommitEffects> {
    let spec = spec_view(snapshot, &input.spec_id)?;
    if !spec.record.active
        || !snapshot
            .projects
            .iter()
            .any(|project| project.record.id == spec.record.project_id && project.record.active)
    {
        return Err(sources::invalid("请先恢复工程和 Spec，再保存确认"));
    }
    let goal = active_goal(records, &input.spec_id, &input.goal_version)?;
    if !matches!(
        input.stage,
        Stage::Spec | Stage::Verification | Stage::Delivery
    ) {
        return Err(sources::invalid(
            "该阶段由证据核实，人工确认适用于规格、业务验收和最终交付",
        ));
    }
    required(&input.source, "人工确认需要实际确认人或确认出处")?;
    if input.scope != goal.goal.scope {
        return Err(sources::invalid("确认范围与当前目标不一致").context("httpStatus", 409));
    }
    let mut summary = project_one(snapshot, records, spec);
    if !valid_digest(&input.evidence_digest) || summary.evidence_digest != input.evidence_digest {
        return Err(sources::invalid("证据已变化，请刷新后重新确认").context("httpStatus", 409));
    }
    if input.decision == ConfirmationDecision::Confirm
        && latest_manual_rejection(records, Some(goal), input.stage).is_some()
    {
        // An explicit new user decision can supersede a previous rejection of this stage.
        let mut basis = records.clone();
        basis.decisions.retain(|record| {
            !(is_manual_stage_decision(record, goal, input.stage)
                && record.confirmation.decision == ConfirmationDecision::Reject)
        });
        summary = project_one(snapshot, &basis, spec);
    }
    let stage = summary
        .stages
        .iter()
        .find(|s| s.stage == input.stage && (s.applicable || input.stage == Stage::Delivery))
        .ok_or_else(|| sources::invalid("确认阶段未纳入当前目标"))?;
    if input.decision == ConfirmationDecision::Confirm {
        if spec.content.material_review() {
            return Err(sources::invalid("关联材料影响待核实，核实后再确认当前目标"));
        }
        if input.stage == Stage::Spec
            && !spec.content.can_confirm
            && spec.content.status != ContentStatus::Confirmed
        {
            return Err(sources::invalid("规格内容仍有待处理问题，当前无法人工确认"));
        }
        if stage.validity != Validity::Current
            || !matches!(stage.progress, Progress::Done | Progress::Skipped)
        {
            return Err(sources::invalid("当前阶段材料或版本仍待核实，无法确认完成"));
        }
        if input.stage == Stage::Delivery && !delivery_ready(&summary, true) {
            return Err(sources::invalid(
                "目标适用阶段、参与工程或关键确认仍未满足交付条件",
            ));
        }
    }
    let id = digest(&(at, &input));
    write(
        RECORD_KINDS[3],
        id.clone(),
        &DecisionRecord {
            schema_version: 1,
            id,
            recorded_at: at.into(),
            origin: DecisionOrigin::Manual,
            stage_evidence_digest: Some(stage_evidence_digest(
                records,
                spec,
                Some(goal),
                input.stage,
                false,
            )),
            confirmation: input,
        },
    )
}

pub(crate) fn prepare_automatic_confirmations(
    snapshot: &Snapshot,
    records: &mut ProgressRecords,
    spec_id: &str,
    at: &str,
) -> Result<CommitEffects> {
    let spec = spec_view(snapshot, spec_id)?;
    let mut effects = CommitEffects::default();
    let mut summary = project_one(snapshot, records, spec);
    for stage in [Stage::Spec, Stage::Verification, Stage::Delivery] {
        if !summary.auto_confirmable_stages.contains(&stage) {
            continue;
        }
        let goal = summary
            .goal
            .as_ref()
            .expect("automatic confirmation requires a goal");
        let stage_digest = stage_evidence_digest(records, spec, Some(goal), stage, false);
        let id = digest(&(
            spec_id,
            &goal.goal_version,
            stage,
            &stage_digest,
            DecisionOrigin::Automatic,
        ));
        let record = DecisionRecord {
            schema_version: 1,
            id: id.clone(),
            recorded_at: at.into(),
            origin: DecisionOrigin::Automatic,
            stage_evidence_digest: Some(stage_digest),
            confirmation: ConfirmationInput {
                spec_id: spec_id.into(),
                goal_version: goal.goal_version.clone(),
                stage,
                scope: goal.goal.scope.clone(),
                evidence_digest: summary.evidence_digest.clone(),
                source: "YSS 自动核实".into(),
                decision: ConfirmationDecision::Confirm,
            },
        };
        effects
            .records
            .extend(write(AUTOMATIC_DECISION_KIND, id, &record)?.records);
        records.decisions.push(record);
        if stage != Stage::Delivery {
            summary = project_one(snapshot, records, spec);
        }
    }
    Ok(effects)
}

/// Reconciliation uses verified facts in projection and preserves unknown historical times.
pub(crate) fn prepare_reconcile(
    snapshot: &Snapshot,
    _records: &ProgressRecords,
    spec_id: &str,
    _at: &str,
) -> Result<CommitEffects> {
    spec_view(snapshot, spec_id)?;
    Ok(CommitEffects::default())
}

pub fn project(
    snapshot: &Snapshot,
    records: &ProgressRecords,
) -> BTreeMap<String, ProgressSummary> {
    snapshot
        .specs
        .iter()
        .map(|spec| (spec.record.id.clone(), project_one(snapshot, records, spec)))
        .collect()
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum EvidenceDigestKind {
    Current,
    Previous,
    Legacy,
}

pub(super) fn is_activity_report(id: &str) -> bool {
    id.starts_with("activity:")
}

fn is_late_report(records: &ProgressRecords, record: &ReportRecord) -> bool {
    records
        .late_reports
        .iter()
        .any(|item| item.report_id == record.report.id)
}

fn reused_report_ids(input: &ProgressReportInput) -> impl Iterator<Item = &str> {
    input.evidence.iter().filter_map(|evidence| {
        if evidence.kind == EvidenceKind::Other {
            evidence.reference.strip_prefix("report:")
        } else {
            None
        }
    })
}

fn covers_reference(report: &ProgressReportInput, reference: &str) -> bool {
    report.scope_refs.iter().any(|item| item == reference)
        || report.evidence.iter().any(|evidence| {
            evidence.reference == reference && evidence.outcome == Some(EvidenceOutcome::Passed)
        })
}

fn same_round(records: &ProgressRecords, goal: &GoalRecord, version: &str) -> bool {
    version == goal.goal_version
        || !is_goal_canceled(records, &goal.goal.spec_id, &goal.goal_version)
            && records.goals.iter().any(|previous| {
                previous.goal.spec_id == goal.goal.spec_id
                    && previous.goal_version == version
                    && goal_round_id(records, previous) == goal_round_id(records, goal)
            })
}

fn evidence_digest(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    kind: EvidenceDigestKind,
) -> String {
    let version = goal.map(|g| g.goal_version.as_str());
    let mut reports: Vec<_> = records
        .reports
        .iter()
        .filter(|r| {
            r.report.spec_id == spec.record.id
                && Some(r.report.goal_version.as_str()) == version
                && !is_late_report(records, r)
                && (kind != EvidenceDigestKind::Current || !is_activity_report(&r.report.id))
        })
        .collect();
    reports.sort_by(|a, b| a.report.id.cmp(&b.report.id));
    let mut overrides: Vec<_> = records
        .overrides
        .iter()
        .filter(|r| {
            r.value.spec_id == spec.record.id
                && Some(r.value.goal_version.as_str()) == version
                && r.cleared_at.is_none()
        })
        .collect();
    overrides.sort_by(|a, b| a.id.cmp(&b.id));
    if kind != EvidenceDigestKind::Legacy {
        let validity: Vec<_> = reports
            .iter()
            .map(|record| report_validity(records, spec, goal, record))
            .collect();
        let mut decisions: Vec<_> = records
            .decisions
            .iter()
            .filter(|decision| {
                decision.confirmation.spec_id == spec.record.id
                    && Some(decision.confirmation.goal_version.as_str()) == version
            })
            .collect();
        decisions.sort_by(|a, b| a.id.cmp(&b.id));
        return digest(&(
            version,
            &spec.content.artifact_digest,
            &spec.content.scope,
            &spec.content.blockers,
            spec.content.status,
            reports,
            validity,
            overrides,
            decisions,
        ));
    }
    let mut versions: Vec<_> = records
        .current_versions
        .iter()
        .filter(|v| {
            v.project_id == spec.record.project_id
                || goal.is_some_and(|g| {
                    g.goal
                        .participants
                        .iter()
                        .any(|p| p.project_id == v.project_id)
                })
        })
        .collect();
    versions.sort_by(|a, b| a.project_id.cmp(&b.project_id));
    digest(&(
        version,
        &spec.content.artifact_digest,
        spec.content.status,
        &reports,
        &overrides,
        &versions,
    ))
}

fn stage_evidence_digest(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    stage: Stage,
    legacy: bool,
) -> String {
    let relevant = |candidate: Stage| match stage {
        Stage::Verification => matches!(candidate, Stage::Implementation | Stage::Verification),
        Stage::Delivery => true,
        _ => candidate == stage,
    };
    let mut reports = BTreeMap::new();
    for report in records.reports.iter().filter(|r| {
        r.report.spec_id == spec.record.id
            && goal.is_some_and(|g| r.report.goal_version == g.goal_version)
            && relevant(r.report.stage)
            && !is_activity_report(&r.report.id)
            && !is_late_report(records, r)
    }) {
        let key = (
            report.report.stage,
            report.report.project_id.as_deref(),
            report.report.role,
        );
        if reports.get(&key).is_none_or(|existing: &&ReportRecord| {
            (&existing.recorded_at, &existing.report.id) < (&report.recorded_at, &report.report.id)
        }) {
            reports.insert(key, report);
        }
    }
    let mut overrides: Vec<_> = records
        .overrides
        .iter()
        .filter(|r| {
            r.value.spec_id == spec.record.id
                && goal.is_some_and(|g| r.value.goal_version == g.goal_version)
                && relevant(r.value.stage)
                && r.cleared_at.is_none()
        })
        .collect();
    overrides.sort_by(|a, b| a.id.cmp(&b.id));
    let mut versions: Vec<_> = records
        .current_versions
        .iter()
        .filter(|v| {
            matches!(
                stage,
                Stage::Implementation | Stage::Verification | Stage::Delivery
            ) && (v.project_id == spec.record.project_id
                || goal.is_some_and(|g| {
                    g.goal
                        .participants
                        .iter()
                        .any(|p| p.project_id == v.project_id)
                }))
        })
        .collect();
    versions.sort_by(|a, b| a.project_id.cmp(&b.project_id));
    let mut decisions = BTreeMap::new();
    for decision in records.decisions.iter().filter(|d| {
        d.confirmation.spec_id == spec.record.id
            && goal.is_some_and(|g| d.confirmation.goal_version == g.goal_version)
            && d.confirmation.stage < stage
            && matches!(d.confirmation.stage, Stage::Spec | Stage::Verification)
    }) {
        if decisions
            .get(&decision.confirmation.stage)
            .is_none_or(|existing: &&DecisionRecord| {
                decision_order(existing) < decision_order(decision)
            })
        {
            decisions.insert(decision.confirmation.stage, decision);
        }
    }
    if !legacy {
        let validity: Vec<_> = reports
            .values()
            .map(|record| report_validity(records, spec, goal, record))
            .collect();
        let scoped = matches!(stage, Stage::Verification | Stage::Delivery)
            && !reports.is_empty()
            && reports
                .values()
                .all(|record| observed_scope(record, &spec.record.project_id).is_some());
        let spec_basis = goal
            .filter(|_| scoped)
            .map(|goal| goal.goal.spec_digest.as_str())
            .or(spec.content.artifact_digest.as_deref());
        return digest(&(
            goal.map(|g| &g.goal_version),
            spec_basis,
            &spec.content.scope,
            &spec.content.blockers,
            reports.values().collect::<Vec<_>>(),
            validity,
            overrides,
            decisions.values().collect::<Vec<_>>(),
        ));
    }
    digest(&(
        goal.map(|g| &g.goal_version),
        &spec.content.artifact_digest,
        &spec.content.scope,
        &spec.content.blockers,
        reports.values().collect::<Vec<_>>(),
        overrides,
        versions,
        decisions.values().collect::<Vec<_>>(),
    ))
}

fn matching_decision<'a>(
    records: &'a ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    stage: Stage,
    global_digest: &str,
) -> Option<&'a DecisionRecord> {
    let goal = goal?;
    let mut decisions = records.decisions.iter().filter(|decision| {
        decision.confirmation.spec_id == spec.record.id
            && decision.confirmation.stage == stage
            && decision.confirmation.goal_version == goal.goal_version
            && decision.confirmation.scope == goal.goal.scope
    });
    let first = decisions.next()?;
    let mut stage_digests = None;
    let mut older_global_digests = None;
    std::iter::once(first)
        .chain(decisions)
        .filter(|decision| match decision.stage_evidence_digest.as_deref() {
            Some(digest) => stage_digests
                .get_or_insert_with(|| {
                    [false, true].map(|legacy| {
                        stage_evidence_digest(records, spec, Some(goal), stage, legacy)
                    })
                })
                .iter()
                .take(if decision.origin == DecisionOrigin::Automatic {
                    1
                } else {
                    2
                })
                .any(|value| value == digest),
            None if decision.origin == DecisionOrigin::Automatic => false,
            None => {
                let digest = &decision.confirmation.evidence_digest;
                digest == global_digest
                    || older_global_digests
                        .get_or_insert_with(|| {
                            [EvidenceDigestKind::Previous, EvidenceDigestKind::Legacy]
                                .map(|kind| evidence_digest(records, spec, Some(goal), kind))
                        })
                        .contains(digest)
            }
        })
        .max_by_key(|decision| decision_order(decision))
}

fn is_manual_stage_decision(record: &DecisionRecord, goal: &GoalRecord, stage: Stage) -> bool {
    record.origin == DecisionOrigin::Manual
        && record.confirmation.spec_id == goal.goal.spec_id
        && record.confirmation.goal_version == goal.goal_version
        && record.confirmation.stage == stage
        && record.confirmation.scope == goal.goal.scope
}

fn latest_manual_rejection<'a>(
    records: &'a ProgressRecords,
    goal: Option<&GoalRecord>,
    stage: Stage,
) -> Option<&'a DecisionRecord> {
    let goal = goal?;
    records
        .decisions
        .iter()
        .filter(|record| is_manual_stage_decision(record, goal, stage))
        .max_by_key(|record| decision_order(record))
        .filter(|record| record.confirmation.decision == ConfirmationDecision::Reject)
}

pub(super) fn stage_participants(goal: Option<&GoalRecord>, stage: Stage) -> Vec<&Participant> {
    goal.into_iter()
        .flat_map(|g| &g.goal.participants)
        .filter(|participant| match stage {
            Stage::Handoff | Stage::Implementation => matches!(
                participant.role,
                ParticipantRole::Backend | ParticipantRole::Frontend
            ),
            Stage::Verification => participant.role != ParticipantRole::Spec,
            _ => false,
        })
        .collect()
}

fn omits_scoped_dependency(report: &ReportRecord) -> bool {
    report.report.id.starts_with("remote:")
        && report.report.validity == Validity::Current
        && matches!(report.report.progress, Progress::Done | Progress::Skipped)
        && report.checked_inputs.is_empty()
}

fn latest_report<'a>(
    records: &'a ProgressRecords,
    spec_id: &str,
    goal: Option<&GoalRecord>,
    stage: Stage,
    project_id: Option<&str>,
    role: ParticipantRole,
) -> Option<&'a ReportRecord> {
    let goal = goal?;
    let participant_stage = role == ParticipantRole::Spec
        && matches!(stage, Stage::Implementation | Stage::Verification)
        && !stage_participants(Some(goal), stage).is_empty();
    let matching = |r: &&ReportRecord| {
        r.report.spec_id == spec_id
            && !is_activity_report(&r.report.id)
            && !is_late_report(records, r)
            && !(participant_stage && r.report.id.starts_with("remote:"))
            && r.report.stage == stage
            && r.report.project_id.as_deref() == project_id
            && r.report.role == role
    };
    let current = records
        .reports
        .iter()
        .filter(matching)
        .filter(|r| r.report.goal_version == goal.goal_version);
    let ordered = |a: &&ReportRecord, b: &&ReportRecord| {
        (&a.recorded_at, &a.report.id).cmp(&(&b.recorded_at, &b.report.id))
    };
    let scoped = current
        .clone()
        .filter(|r| !r.report.id.starts_with("remote:"))
        .max_by(ordered)
        .is_some_and(|r| !r.checked_inputs.is_empty());
    // Older combined reports omitted the local scope they were based on.
    let current = current
        .filter(|report| !scoped || !omits_scoped_dependency(report))
        .max_by(ordered);
    current.or_else(|| {
        records
            .reports
            .iter()
            .filter(matching)
            .filter(|report| same_round(records, goal, &report.report.goal_version))
            .max_by(ordered)
    })
}
fn latest_override<'a>(
    records: &'a ProgressRecords,
    spec_id: &str,
    goal: Option<&GoalRecord>,
    stage: Stage,
    project_id: Option<&str>,
) -> Option<&'a OverrideRecord> {
    records
        .overrides
        .iter()
        .filter(|r| {
            r.cleared_at.is_none()
                && r.value.spec_id == spec_id
                && r.value.stage == stage
                && r.value.project_id.as_deref() == project_id
                && goal.is_some_and(|g| r.value.goal_version == g.goal_version)
        })
        .max_by(|a, b| (&a.recorded_at, &a.id).cmp(&(&b.recorded_at, &b.id)))
}
fn stale_validity(stage: Stage) -> Validity {
    if matches!(
        stage,
        Stage::Handoff | Stage::Implementation | Stage::Verification | Stage::Delivery
    ) {
        Validity::NeedsSync
    } else {
        Validity::NeedsReview
    }
}
fn versions_match(reported: &ProjectVersion, actual: &ProjectVersion) -> bool {
    if actual.commit.is_none() && actual.working_tree_digest.is_none() {
        return false;
    }
    if reported
        .commit
        .as_ref()
        .is_some_and(|c| actual.commit.as_ref() != Some(c))
    {
        return false;
    }
    if let Some(digest) = &reported.working_tree_digest {
        return actual.working_tree_digest.as_ref() == Some(digest);
    }
    // A commit-only statement describes the clean tree at that commit.
    reported.commit.is_some()
        && actual.working_tree_digest.as_deref() == Some(Sha256::of(&[]).as_str())
}

fn observed_scope<'a>(
    record: &'a ReportRecord,
    project_id: &str,
) -> Option<(&'a ProjectInputs, &'a str)> {
    let scope = record
        .report
        .input_paths
        .iter()
        .find(|input| input.project_id == project_id)?;
    let saved = record
        .checked_inputs
        .iter()
        .find(|input| input.project_id == project_id)?;
    (scope.observed_digest.as_deref() == Some(&saved.digest))
        .then_some((scope, saved.digest.as_str()))
}

fn scoped_validity(records: &ProgressRecords, record: &ReportRecord, project_id: &str) -> Validity {
    let Some((scope, saved)) = observed_scope(record, project_id) else {
        return Validity::Unverified;
    };
    let current = records
        .checked_inputs
        .get(&(scope.project_id.clone(), scope.paths.clone()))
        .and_then(Option::as_deref);
    match current {
        Some(current) if saved == current => Validity::Current,
        Some(_) => Validity::NeedsSync,
        None => Validity::Unverified,
    }
}

fn sync_diagnostic(
    snapshot: &Snapshot,
    stage: Stage,
    report: Option<&ReportRecord>,
    project_id: Option<&str>,
    scope_changed: bool,
) -> EvidenceSync {
    let project_name = project_id.and_then(|id| {
        snapshot
            .projects
            .iter()
            .find(|project| project.record.id == id)
            .map(|project| project.record.name.clone())
    });
    let paths = report
        .and_then(|record| {
            record
                .report
                .input_paths
                .iter()
                .find(|input| Some(input.project_id.as_str()) == project_id)
        })
        .map_or_else(Vec::new, |input| input.paths.clone());
    EvidenceSync {
        stage,
        project_id: project_id.map(str::to_owned),
        project_name,
        role: report.map(|record| record.report.role),
        report_id: report.map(|record| record.report.id.clone()),
        recorded_at: report.map(|record| record.recorded_at.clone()),
        reason: if scope_changed {
            "报告登记的输入范围摘要已变化"
        } else {
            "现有凭证需要按当前目标、工程版本和输入核对"
        }
        .into(),
        paths,
        next_action: SYNC_EVIDENCE_NEXT_ACTION.into(),
    }
}

fn changed_report_inputs<'a>(
    records: &'a ProgressRecords,
    report: &'a ReportRecord,
) -> impl Iterator<Item = &'a str> {
    report
        .checked_inputs
        .iter()
        .map(|inputs| inputs.project_id.as_str())
        .filter(|id| scoped_validity(records, report, id) == Validity::NeedsSync)
}

fn append_report_sync_diagnostics(
    diagnostics: &mut Vec<EvidenceSync>,
    snapshot: &Snapshot,
    records: &ProgressRecords,
    report: &ReportRecord,
) {
    let stage = report.report.stage;
    let count = diagnostics.len();
    diagnostics.extend(
        changed_report_inputs(records, report)
            .map(|id| sync_diagnostic(snapshot, stage, Some(report), Some(id), true)),
    );
    if diagnostics.len() == count {
        let project_id = report.report.project_id.as_deref().or({
            match report.report.input_paths.as_slice() {
                [inputs] => Some(inputs.project_id.as_str()),
                _ => match report.report.versions.as_slice() {
                    [version] => Some(version.project_id.as_str()),
                    _ => None,
                },
            }
        });
        diagnostics.push(sync_diagnostic(
            snapshot,
            stage,
            Some(report),
            project_id,
            false,
        ));
    }
}

fn engineering_validity(
    records: &ProgressRecords,
    record: &ReportRecord,
    project_id: &str,
    exact_version: bool,
) -> Validity {
    let Some(reported) = record
        .report
        .versions
        .iter()
        .find(|version| version.project_id == project_id)
    else {
        return Validity::Unverified;
    };
    let has_scope = record
        .report
        .input_paths
        .iter()
        .any(|input| input.project_id == project_id);
    if has_scope && !exact_version {
        return scoped_validity(records, record, project_id);
    }
    let Some(actual) = records
        .current_versions
        .iter()
        .find(|version| version.project_id == project_id)
        .filter(|actual| actual.commit.is_some() || actual.working_tree_digest.is_some())
    else {
        return Validity::Unverified;
    };
    if exact_version && !versions_match(reported, actual) {
        return Validity::NeedsSync;
    }
    if has_scope {
        return scoped_validity(records, record, project_id);
    }
    if versions_match(reported, actual) {
        Validity::Current
    } else {
        Validity::NeedsSync
    }
}
fn has_stage_evidence(report: &ProgressReportInput, goal: &GoalRecord) -> bool {
    let good = |e: &&Evidence| e.outcome != Some(EvidenceOutcome::Failed);
    let passed = |e: &&Evidence| e.outcome == Some(EvidenceOutcome::Passed);
    match report.stage {
        Stage::Research | Stage::Analysis | Stage::Spec | Stage::Prototype | Stage::Handoff => {
            report.evidence.iter().filter(good).any(|e| {
                matches!(
                    e.kind,
                    EvidenceKind::Document
                        | EvidenceKind::Artifact
                        | EvidenceKind::Acceptance
                        | EvidenceKind::Other
                )
            })
        }
        Stage::Implementation => report.evidence.iter().filter(good).any(|e| {
            matches!(
                e.kind,
                EvidenceKind::Artifact
                    | EvidenceKind::Command
                    | EvidenceKind::Test
                    | EvidenceKind::Merge
            )
        }),
        Stage::Verification => report.evidence.iter().filter(passed).any(|e| {
            matches!(
                e.kind,
                EvidenceKind::Command
                    | EvidenceKind::Test
                    | EvidenceKind::Environment
                    | EvidenceKind::Acceptance
            )
        }),
        Stage::Delivery => match goal.goal.target.kind {
            DeliveryTargetKind::Local => report.evidence.iter().filter(good).any(|e| {
                matches!(
                    e.kind,
                    EvidenceKind::Artifact
                        | EvidenceKind::Command
                        | EvidenceKind::Test
                        | EvidenceKind::Acceptance
                )
            }),
            DeliveryTargetKind::Merged => report.evidence.iter().filter(passed).any(|e| {
                e.kind == EvidenceKind::Merge
                    && goal.goal.target.reference.as_ref().is_none_or(|reference| {
                        e.reference == *reference || report.scope_refs.contains(reference)
                    })
            }),
            DeliveryTargetKind::Environment => report.evidence.iter().filter(passed).any(|e| {
                e.kind == EvidenceKind::Environment
                    && goal.goal.target.reference.as_ref() == Some(&e.reference)
            }),
        },
        Stage::Unmarked => false,
    }
}

fn needs_exact_candidate(report: &ProgressReportInput, goal: &GoalRecord) -> bool {
    report.role == ParticipantRole::Integration
        || report.evidence.iter().any(|evidence| {
            matches!(
                evidence.kind,
                EvidenceKind::Merge | EvidenceKind::Environment
            )
        })
        || goal.goal.target.kind != DeliveryTargetKind::Local
            && matches!(report.stage, Stage::Verification | Stage::Delivery)
}

fn required_project_versions<'a>(
    report: &'a ProgressReportInput,
    goal: &'a GoalRecord,
    spec: &'a SpecView,
) -> BTreeSet<&'a str> {
    let mut projects = BTreeSet::new();
    if let Some(project_id) = &report.project_id {
        projects.insert(project_id.as_str());
    }
    if report.project_id.is_none()
        || report.stage == Stage::Delivery && goal.goal.target.kind != DeliveryTargetKind::Local
    {
        projects.extend(
            goal.goal
                .participants
                .iter()
                .filter(|participant| participant.role != ParticipantRole::Spec)
                .map(|participant| participant.project_id.as_str()),
        );
    }
    if report.stage == Stage::Verification && report.role == ParticipantRole::Integration {
        projects.extend(
            goal.goal
                .participants
                .iter()
                .filter(|participant| {
                    matches!(
                        participant.role,
                        ParticipantRole::Backend | ParticipantRole::Frontend
                    )
                })
                .map(|participant| participant.project_id.as_str()),
        );
    }
    if projects.is_empty() {
        projects.insert(&spec.record.project_id);
    }
    projects
}

fn same_checked_scope(previous: &ReportRecord, current: &ReportRecord, project_id: &str) -> bool {
    observed_scope(previous, project_id)
        .zip(observed_scope(current, project_id))
        .is_some_and(|((old, old_digest), (new, new_digest))| {
            old_digest == new_digest
                && old.paths.len() == new.paths.len()
                && old.paths.iter().all(|path| new.paths.contains(path))
        })
}

fn validate_reused_reports(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: &GoalRecord,
    current: &ReportRecord,
) -> std::result::Result<Option<bool>, &'static str> {
    let report = &current.report;
    let mut originals = Vec::new();
    let mut exact_candidate = false;
    for id in reused_report_ids(report) {
        let Some(original) = records.reports.iter().find(|item| {
            item.report.id == id
                && item.report.spec_id == report.spec_id
                && item.report.goal_version != report.goal_version
                && !is_activity_report(id)
        }) else {
            return Err("历史原报告无法读取");
        };
        let old = &original.report;
        let Some(old_goal) = records
            .goals
            .iter()
            .find(|item| item.goal.spec_id == old.spec_id && item.goal_version == old.goal_version)
        else {
            return Err("历史原报告的目标无法读取");
        };
        let same_project = old.project_id == report.project_id
            || report.role == ParticipantRole::Spec
                && old.project_id.as_deref().unwrap_or(&spec.record.project_id)
                    == report
                        .project_id
                        .as_deref()
                        .unwrap_or(&spec.record.project_id);
        if old.stage != report.stage
            || old.role != report.role
            || !same_project
            || old.validity != Validity::Current
            || old.progress != Progress::Done
            || !has_stage_evidence(old, old_goal)
            || old
                .evidence
                .iter()
                .any(|item| item.outcome == Some(EvidenceOutcome::Failed))
            || reused_report_ids(old).next().is_some()
        {
            return Err("请引用能核实本阶段和工程的原始成功报告");
        }
        if old.spec_digest != report.spec_digest
            && !same_checked_scope(original, current, &spec.record.project_id)
        {
            return Err("原报告的规格版本或相关输入与本轮不一致");
        }
        let exact = needs_exact_candidate(old, old_goal) || needs_exact_candidate(report, goal);
        exact_candidate |= exact;
        if exact
            && (old_goal.goal.target.kind != goal.goal.target.kind
                || old_goal.goal.target.reference != goal.goal.target.reference
                || old.versions.len() != report.versions.len())
        {
            return Err("原报告未覆盖本轮候选组合或交付目标");
        }
        if old.versions.iter().any(|previous| {
            !report
                .versions
                .iter()
                .any(|v| v.project_id == previous.project_id)
        }) || report.versions.iter().any(|version| {
            let same_version = old.versions.iter().any(|previous| {
                previous.project_id == version.project_id
                    && previous.commit == version.commit
                    && (previous == version
                        || versions_match(previous, version)
                        || versions_match(version, previous))
            });
            !same_version && (exact || !same_checked_scope(original, current, &version.project_id))
        }) || old
            .input_paths
            .iter()
            .any(|scope| !same_checked_scope(original, current, &scope.project_id))
        {
            return Err("原报告的工程版本或已核实输入与本轮不一致");
        }
        if old_goal.goal.scope != goal.goal.scope && report.scope_refs.is_empty() {
            return Err("请明确记录原报告覆盖的本轮范围");
        }
        originals.push(old);
    }
    if originals.is_empty() {
        return Ok(None);
    }
    if exact_candidate
        && report.evidence.iter().any(|evidence| {
            matches!(
                evidence.kind,
                EvidenceKind::Merge | EvidenceKind::Environment
            ) && evidence.outcome == Some(EvidenceOutcome::Passed)
                && !originals.iter().any(|old| {
                    old.evidence.iter().any(|previous| {
                        previous.kind == evidence.kind
                            && previous.reference == evidence.reference
                            && previous.outcome == evidence.outcome
                    })
                })
        })
    {
        return Err("原报告未验证本轮引用的合并或环境凭证");
    }
    let acceptance = &goal.goal.acceptance_refs;
    if !acceptance.is_empty()
        && matches!(
            report.stage,
            Stage::Implementation | Stage::Verification | Stage::Delivery
        )
        && !acceptance
            .iter()
            .any(|reference| covers_reference(report, reference))
    {
        return Err("请明确记录原报告覆盖的本轮验收项");
    }
    if report
        .scope_refs
        .iter()
        .chain(
            acceptance
                .iter()
                .filter(|reference| covers_reference(report, reference)),
        )
        .any(|reference| !originals.iter().any(|old| covers_reference(old, reference)))
    {
        return Err("原报告未覆盖本轮上报的验收或范围引用");
    }
    Ok(Some(exact_candidate))
}

fn report_evidence_issue(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: &GoalRecord,
    record: &ReportRecord,
) -> Option<&'static str> {
    let report = &record.report;
    if report.goal_version != goal.goal_version {
        return None;
    }
    if let Err(issue) = validate_reused_reports(records, spec, goal, record) {
        return Some(issue);
    }
    if report.stage != Stage::Delivery
        || !matches!(report.progress, Progress::Done | Progress::Skipped)
        || goal.goal.target.kind == DeliveryTargetKind::Local
    {
        return None;
    }
    if !has_stage_evidence(report, goal) {
        return Some(match goal.goal.target.kind {
            DeliveryTargetKind::Merged => "补齐与当前交付目标一致的合并凭证",
            DeliveryTargetKind::Environment => "补齐实际指定环境的成功验证凭证",
            DeliveryTargetKind::Local => unreachable!("local evidence was excluded"),
        });
    }
    required_project_versions(report, goal, spec)
        .iter()
        .any(|project| {
            !report
                .versions
                .iter()
                .any(|version| &version.project_id == project)
        })
        .then_some("补齐当前交付候选组合中各参与工程的实际版本")
}

pub(super) fn report_validity(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    record: &ReportRecord,
) -> Validity {
    let Some(goal) = goal else {
        return Validity::Unverified;
    };
    let report = &record.report;
    if is_late_report(records, record) {
        return Validity::Unverified;
    }
    if report.goal_version != goal.goal_version || report.spec_digest != goal.goal.spec_digest {
        return stale_validity(report.stage);
    }
    if report.validity != Validity::Current {
        return report.validity;
    }
    let Ok(reused_exact_candidate) = validate_reused_reports(records, spec, goal, record) else {
        return Validity::Unverified;
    };
    let exact_version =
        reused_exact_candidate.unwrap_or_else(|| needs_exact_candidate(report, goal));
    if reused_exact_candidate.is_some() && !report.versions.is_empty() {
        let validity = aggregate_validity(report.versions.iter().map(|version| {
            engineering_validity(records, record, &version.project_id, exact_version)
        }));
        if validity != Validity::Current {
            return validity;
        }
    }
    if report.progress == Progress::Unmarked {
        return Validity::Unverified;
    }
    if spec.content.artifact_digest.as_deref() != Some(report.spec_digest.as_str()) {
        match scoped_validity(records, record, &spec.record.project_id) {
            Validity::Current if spec.content.artifact_digest.is_some() => {}
            Validity::NeedsSync => return stale_validity(report.stage),
            _ => return Validity::Unverified,
        }
    }
    if matches!(report.progress, Progress::Done | Progress::Skipped) {
        let target_evidence_required =
            report.stage == Stage::Delivery && goal.goal.target.kind != DeliveryTargetKind::Local;
        if (report.progress == Progress::Done || target_evidence_required)
            && !has_stage_evidence(report, goal)
        {
            return Validity::Unverified;
        }
        if report.progress == Progress::Skipped && report.scope_refs.is_empty() {
            return Validity::Unverified;
        }
        if report.progress == Progress::Skipped && !target_evidence_required {
            return Validity::Current;
        }
        if matches!(
            report.stage,
            Stage::Implementation | Stage::Verification | Stage::Delivery
        ) {
            let validity = aggregate_validity(
                required_project_versions(report, goal, spec)
                    .into_iter()
                    .map(|id| engineering_validity(records, record, id, exact_version)),
            );
            if validity != Validity::Current {
                return validity;
            }
        }
    }
    Validity::Current
}

fn last_successful_report(
    records: &ProgressRecords,
    spec: &SpecView,
    current: Option<&GoalRecord>,
    stage: Stage,
    project_id: Option<&str>,
    role: ParticipantRole,
) -> Option<String> {
    let current = current?;
    records
        .reports
        .iter()
        .filter(|record| {
            let report = &record.report;
            if report.spec_id != spec.record.id
                || !same_round(records, current, &report.goal_version)
                || report.stage != stage
                || report.role != role
                || is_activity_report(&report.id)
                || is_late_report(records, record)
                || report.progress != Progress::Done
                || report.validity != Validity::Current
            {
                return false;
            }
            if report.project_id.as_deref() != project_id
                && !(role == ParticipantRole::Spec
                    && project_id.is_none()
                    && report.project_id.as_deref() == Some(spec.record.project_id.as_str()))
            {
                return false;
            }
            let Some(goal) = records.goals.iter().find(|g| {
                g.goal_version == report.goal_version && g.goal.spec_id == report.spec_id
            }) else {
                return false;
            };
            if !has_stage_evidence(report, goal) {
                return false;
            }
            if validate_reused_reports(records, spec, goal, record).is_err() {
                return false;
            }
            if matches!(
                stage,
                Stage::Implementation | Stage::Verification | Stage::Delivery
            ) && !required_project_versions(report, goal, spec)
                .into_iter()
                .all(|id| {
                    report.versions.iter().any(|v| {
                        v.project_id == id
                            && (v.commit.is_some() || v.working_tree_digest.is_some())
                    })
                })
            {
                return false;
            }
            true
        })
        .map(|record| record.recorded_at.clone())
        .max()
}

fn participant_progress(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    stage: Stage,
    participant: &Participant,
) -> ParticipantProgress {
    let report = latest_report(
        records,
        &spec.record.id,
        goal,
        stage,
        Some(&participant.project_id),
        participant.role,
    );
    let mut view = ParticipantProgress {
        project_id: participant.project_id.clone(),
        role: participant.role,
        label: participant.label.clone(),
        progress: Progress::Unmarked,
        validity: Validity::Unverified,
        source: ProgressSource::Unmarked,
        summary: "尚无当前范围的核实报告".into(),
        blocker: String::new(),
        next_action: "由原 Agent 核对范围与证据后上报".into(),
        evidence: Vec::new(),
        report_id: None,
        last_verified_at: last_successful_report(
            records,
            spec,
            goal,
            stage,
            Some(&participant.project_id),
            participant.role,
        ),
        has_override: false,
        conflicts: Vec::new(),
        versions: Vec::new(),
        current_version: records
            .current_versions
            .iter()
            .find(|v| v.project_id == participant.project_id)
            .cloned(),
        override_record: None,
    };
    if let Some(report) = report {
        view.progress = report.report.progress;
        view.validity = report_validity(records, spec, goal, report);
        view.source = ProgressSource::Evidence;
        view.summary = report.report.summary.clone();
        view.blocker = report.report.blocker.clone();
        view.next_action = goal
            .and_then(|goal| report_evidence_issue(records, spec, goal, report))
            .map_or_else(|| report.report.next_action.clone(), str::to_owned);
        view.evidence = report.report.evidence.clone();
        view.report_id = Some(report.report.id.clone());
        view.versions = report.report.versions.clone();
    }
    if let Some(manual) = latest_override(
        records,
        &spec.record.id,
        goal,
        stage,
        Some(&participant.project_id),
    ) {
        apply_manual(
            &mut view.progress,
            &mut view.validity,
            &mut view.conflicts,
            manual,
        );
        view.source = ProgressSource::Manual;
        view.has_override = true;
        view.override_record = Some(manual.clone());
        view.summary = manual.value.reason.clone();
        if manual.value.progress == Progress::Blocked {
            view.blocker = manual.value.reason.clone();
        }
    }
    view
}

fn apply_manual(
    progress: &mut Progress,
    validity: &mut Validity,
    conflicts: &mut Vec<String>,
    manual: &OverrideRecord,
) {
    if *progress != manual.value.progress {
        conflicts.push(format!(
            "人工更正优先，自动证据进度为 {}",
            progress_label(*progress)
        ));
    }
    if manual.value.progress == Progress::Done && *progress != Progress::Done {
        *validity = Validity::Unverified;
    }
    if manual.value.progress == Progress::Skipped && *validity == Validity::Unverified {
        *validity = Validity::Current;
    }
    *progress = manual.value.progress;
}
fn progress_label(progress: Progress) -> &'static str {
    match progress {
        Progress::Unmarked => "未标注",
        Progress::Todo => "待办",
        Progress::Doing => "进行中",
        Progress::Blocked => "阻塞",
        Progress::Done => "完成",
        Progress::Skipped => "跳过",
    }
}
fn aggregate_progress(values: impl Iterator<Item = Progress>) -> Progress {
    let values: Vec<_> = values.collect();
    if values.is_empty() {
        return Progress::Unmarked;
    }
    if values.contains(&Progress::Doing) {
        return Progress::Doing;
    }
    if values.contains(&Progress::Blocked) {
        return Progress::Blocked;
    }
    if values
        .iter()
        .all(|p| matches!(p, Progress::Done | Progress::Skipped))
    {
        return if values.iter().all(|p| *p == Progress::Skipped) {
            Progress::Skipped
        } else {
            Progress::Done
        };
    }
    if values
        .iter()
        .any(|p| matches!(p, Progress::Doing | Progress::Done | Progress::Skipped))
    {
        return Progress::Doing;
    }
    if values.contains(&Progress::Todo) {
        return Progress::Todo;
    }
    Progress::Unmarked
}
fn aggregate_validity(values: impl Iterator<Item = Validity>) -> Validity {
    let values: Vec<_> = values.collect();
    if values.contains(&Validity::NeedsSync) {
        Validity::NeedsSync
    } else if values.contains(&Validity::NeedsReview) {
        Validity::NeedsReview
    } else if values.is_empty() || values.contains(&Validity::Unverified) {
        Validity::Unverified
    } else {
        Validity::Current
    }
}

fn main_stage_report<'a>(
    records: &'a ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    stage: Stage,
) -> Option<&'a ReportRecord> {
    let reports = [None, Some(spec.record.project_id.as_str())]
        .into_iter()
        .filter_map(|project_id| {
            latest_report(
                records,
                &spec.record.id,
                goal,
                stage,
                project_id,
                ParticipantRole::Spec,
            )
        });
    let scoped = reports.clone().any(|r| {
        !r.report.id.starts_with("remote:")
            && !r.checked_inputs.is_empty()
            && goal.is_some_and(|g| r.report.goal_version == g.goal_version)
    });
    reports
        .filter(|report| !scoped || !omits_scoped_dependency(report))
        .max_by(|a, b| {
            let current =
                |r: &ReportRecord| goal.is_some_and(|g| r.report.goal_version == g.goal_version);
            (current(a), &a.recorded_at, &a.report.id).cmp(&(
                current(b),
                &b.recorded_at,
                &b.report.id,
            ))
        })
}

fn stage_summary(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    stage: Stage,
    evidence_digest: &str,
) -> StageSummary {
    let scope = goal.and_then(|g| g.goal.stages.iter().find(|s| s.stage == stage));
    let applicable = scope.is_some_and(|s| s.applicable);
    let scope_reason = scope.map_or_else(
        || {
            if goal.is_some() {
                "未纳入当前目标".into()
            } else {
                String::new()
            }
        },
        |s| s.reason.clone(),
    );
    let legacy = snapshot
        .stage_records
        .iter()
        .find(|r| r.spec_id == spec.record.id && r.stage == stage)
        .cloned();
    let mut view = StageSummary {
        stage,
        applicable,
        scope_reason,
        progress: Progress::Unmarked,
        validity: Validity::Unverified,
        source: ProgressSource::Unmarked,
        summary: "尚无可核实证据".into(),
        blocker: String::new(),
        next_action: "由原 Agent 核对阶段范围与证据".into(),
        evidence: Vec::new(),
        participants: Vec::new(),
        confirmation_required: matches!(stage, Stage::Spec | Stage::Verification | Stage::Delivery),
        confirmation: None,
        last_verified_at: last_successful_report(
            records,
            spec,
            goal,
            stage,
            None,
            ParticipantRole::Spec,
        ),
        has_override: false,
        conflicts: Vec::new(),
        legacy,
        versions: Vec::new(),
        override_record: None,
    };
    if goal.is_none() {
        view.confirmation_required = false;
        view.next_action.clear();
        return view;
    }
    if !applicable
        && !(stage == Stage::Delivery
            && goal.is_some_and(|g| g.goal.target.kind != DeliveryTargetKind::Local))
    {
        view.progress = Progress::Skipped;
        view.validity = if scope.is_some() {
            Validity::Current
        } else {
            Validity::Unverified
        };
        view.source = ProgressSource::Goal;
        view.summary = view.scope_reason.clone();
        view.next_action.clear();
        view.confirmation_required = goal.is_some() && stage == Stage::Delivery;
        if view.confirmation_required {
            view.summary = "目标结果确认".into();
            view.confirmation =
                matching_decision(records, spec, goal, stage, evidence_digest).cloned();
        }
        return view;
    }
    let content_fits_goal = spec
        .content
        .artifact_digest
        .as_deref()
        .is_some_and(|digest| {
            goal.is_none_or(|goal| {
                goal.goal.spec_digest == digest && goal.goal.scope == spec.content.scope
            })
        });
    let report = main_stage_report(records, spec, goal, stage).filter(|report| {
        if stage != Stage::Spec || !content_fits_goal {
            return true;
        }
        let current = report_validity(records, spec, goal, report) == Validity::Current;
        match spec.content.status {
            ContentStatus::Confirmed => current && report.report.progress == Progress::Blocked,
            ContentStatus::Ready => current,
            _ => true,
        }
    });
    if let Some(report) = report {
        view.progress = report.report.progress;
        view.validity = report_validity(records, spec, goal, report);
        view.source = ProgressSource::Evidence;
        view.summary = report.report.summary.clone();
        view.blocker = report.report.blocker.clone();
        view.next_action = goal
            .and_then(|goal| report_evidence_issue(records, spec, goal, report))
            .map_or_else(|| report.report.next_action.clone(), str::to_owned);
        view.evidence = report.report.evidence.clone();
        view.versions = report.report.versions.clone();
    } else if stage == Stage::Spec && spec.content.artifact_digest.is_some() {
        match spec.content.status {
            ContentStatus::Confirmed if content_fits_goal => {
                view.progress = Progress::Done;
                view.validity = Validity::Current;
                view.source = ProgressSource::Content;
                view.summary = "当前规格内容已人工确认".into();
                view.confirmation_required = false;
                view.next_action.clear();
                view.evidence.push(Evidence {
                    kind: EvidenceKind::Acceptance,
                    label: "当前规格内容确认".into(),
                    reference: spec.record.source_path.clone(),
                    outcome: Some(EvidenceOutcome::Passed),
                });
                view.last_verified_at = spec
                    .content
                    .confirmations
                    .iter()
                    .filter(|c| {
                        c.scope == spec.content.scope
                            && Some(c.artifact_digest.as_str())
                                == spec.content.artifact_digest.as_deref()
                    })
                    .map(|c| c.created_at.clone())
                    .chain(view.last_verified_at)
                    .max();
            }
            ContentStatus::Ready | ContentStatus::Confirmed => {
                view.progress = Progress::Done;
                view.validity = Validity::Current;
                view.source = ProgressSource::Content;
                view.summary = "规格材料已就绪".into();
                view.next_action = "核对当前目标范围的规格材料".into();
                view.evidence.push(Evidence {
                    kind: EvidenceKind::Document,
                    label: "当前规格材料".into(),
                    reference: spec.record.source_path.clone(),
                    outcome: Some(EvidenceOutcome::Observed),
                });
            }
            ContentStatus::Draft => {
                view.progress = Progress::Doing;
                view.source = ProgressSource::Content;
                view.summary = "规格正在编写，完成范围待核实".into();
            }
            ContentStatus::Unmarked => {}
        }
    } else if view.legacy.is_some() {
        view.source = ProgressSource::Historical;
        view.summary = "历史人工阶段记录，当前范围与时间待核实".into();
    }
    if stage == Stage::Prototype && report.is_none() {
        view.evidence.extend(
            spec.resources
                .iter()
                .filter(|resource| {
                    resource.record.kind == ResourceKind::Prototype
                        && resource.health == ResourceHealth::Available
                })
                .map(|resource| Evidence {
                    kind: EvidenceKind::Artifact,
                    label: resource.record.label.clone(),
                    reference: resource.record.target.clone(),
                    outcome: Some(EvidenceOutcome::Observed),
                }),
        );
        if !view.evidence.is_empty() {
            view.source = ProgressSource::Historical;
            view.summary = "已发现登记的原型产物，完成范围待核实".into();
        }
    }
    view.participants = stage_participants(goal, stage)
        .into_iter()
        .map(|p| participant_progress(records, spec, goal, stage, p))
        .collect();
    if !view.participants.is_empty() {
        let progress = view.participants.iter().map(|p| p.progress);
        view.progress = aggregate_progress(if report.is_some() {
            std::iter::once(view.progress)
                .chain(progress)
                .collect::<Vec<_>>()
                .into_iter()
        } else {
            progress.collect::<Vec<_>>().into_iter()
        });
        let validity = view.participants.iter().map(|p| p.validity);
        view.validity = aggregate_validity(if report.is_some() {
            std::iter::once(view.validity)
                .chain(validity)
                .collect::<Vec<_>>()
                .into_iter()
        } else {
            validity.collect::<Vec<_>>().into_iter()
        });
        if view.source == ProgressSource::Unmarked {
            view.source = ProgressSource::Evidence;
            view.summary = "按参与工程分别核实".into();
        }
        if let Some(blocked) = view
            .participants
            .iter()
            .find(|p| p.progress == Progress::Blocked)
        {
            view.blocker = blocked.blocker.clone();
        }
        view.evidence
            .extend(view.participants.iter().flat_map(|p| p.evidence.clone()));
        view.versions
            .extend(view.participants.iter().flat_map(|p| p.versions.clone()));
        view.has_override = view.participants.iter().any(|p| p.has_override);
        view.conflicts
            .extend(view.participants.iter().flat_map(|p| p.conflicts.clone()));
        view.last_verified_at = view
            .participants
            .iter()
            .filter_map(|p| p.last_verified_at.clone())
            .chain(view.last_verified_at)
            .max();
    }
    if report.is_none()
        && view.participants.is_empty()
        && goal.is_some_and(|g| {
            spec.content.artifact_digest.as_deref() != Some(g.goal.spec_digest.as_str())
        })
    {
        view.validity = Validity::Unverified;
    }
    if stage == Stage::Spec && spec.content.status == ContentStatus::Confirmed {
        view.confirmation_required = !content_fits_goal;
    }
    if let Some(manual) = latest_override(records, &spec.record.id, goal, stage, None) {
        apply_manual(
            &mut view.progress,
            &mut view.validity,
            &mut view.conflicts,
            manual,
        );
        view.source = ProgressSource::Manual;
        view.summary = manual.value.reason.clone();
        view.has_override = true;
        view.override_record = Some(manual.clone());
        if manual.value.progress == Progress::Blocked {
            view.blocker = manual.value.reason.clone();
        }
    }
    let decision = latest_manual_rejection(records, goal, stage)
        .or_else(|| matching_decision(records, spec, goal, stage, evidence_digest));
    if let Some(decision) = decision {
        view.confirmation = Some(decision.clone());
        if decision.confirmation.decision == ConfirmationDecision::Reject {
            view.progress = Progress::Blocked;
            view.blocker = format!("人工确认未通过，出处 {}", decision.confirmation.source);
        }
    }
    view
}

fn confirmation_satisfied(stage: &StageSummary) -> bool {
    !stage.confirmation_required
        || stage
            .confirmation
            .as_ref()
            .is_some_and(|d| d.confirmation.decision == ConfirmationDecision::Confirm)
}
fn completed(stage: &StageSummary) -> bool {
    matches!(stage.progress, Progress::Done | Progress::Skipped)
        && stage.validity == Validity::Current
        && confirmation_satisfied(stage)
}
fn delivery_ready(summary: &ProgressSummary, exclude_delivery_confirmation: bool) -> bool {
    summary.stages.iter().filter(|s| s.applicable).all(|stage| {
        let own = if stage.stage == Stage::Delivery && exclude_delivery_confirmation {
            matches!(stage.progress, Progress::Done | Progress::Skipped)
                && stage.validity == Validity::Current
        } else {
            completed(stage)
        };
        own && stage.participants.iter().all(|p| {
            matches!(p.progress, Progress::Done | Progress::Skipped)
                && p.validity == Validity::Current
        })
    })
}
fn participant_completed(participant: &ParticipantProgress) -> bool {
    matches!(participant.progress, Progress::Done | Progress::Skipped)
        && participant.validity == Validity::Current
}
fn stage_completed(stage: &StageSummary) -> bool {
    completed(stage) && stage.participants.iter().all(participant_completed)
}

fn automatic_context(snapshot: &Snapshot, spec: &SpecView) -> bool {
    spec.record.active
        && snapshot.projects.iter().any(|project| {
            project.record.id == spec.record.project_id
                && project.record.active
                && project.available
        })
        && spec.source_health == SourceHealth::Available
        && spec.content.metadata_error.is_none()
        && spec.content.artifact_digest.is_some()
        && !spec.content.material_review()
        && spec.content.questions.iter().all(|view| {
            !view.question.blocking
                || matches!(
                    view.state,
                    QuestionState::Resolved | QuestionState::Deferred
                )
        })
}

fn usable_stage_evidence(stage: &StageSummary) -> bool {
    stage.validity == Validity::Current
        && matches!(stage.progress, Progress::Done | Progress::Skipped)
        && !stage.has_override
        && stage.blocker.is_empty()
        && stage.conflicts.is_empty()
        && stage
            .participants
            .iter()
            .all(|participant| participant_completed(participant) && participant.blocker.is_empty())
        && stage
            .evidence
            .iter()
            .all(|e| e.outcome != Some(EvidenceOutcome::Failed))
}

fn verification_evidence_covers(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: &GoalRecord,
    stage: &StageSummary,
) -> bool {
    let reports: Vec<_> = current_stage_reports(records, spec, goal, stage).collect();
    if stage.progress == Progress::Skipped {
        return goal.goal.acceptance_refs.is_empty()
            && !reports.is_empty()
            && reports.iter().all(|report| {
                report.progress == Progress::Skipped && !report.scope_refs.is_empty()
            });
    }
    let reports: Vec<_> = reports
        .into_iter()
        .filter(|report| report.progress == Progress::Done && has_stage_evidence(report, goal))
        .collect();
    !reports.is_empty()
        && goal.goal.acceptance_refs.iter().all(|reference| {
            reports
                .iter()
                .any(|report| covers_reference(report, reference))
        })
}

fn current_stage_reports<'a>(
    records: &'a ProgressRecords,
    spec: &'a SpecView,
    goal: &'a GoalRecord,
    stage: &'a StageSummary,
) -> impl Iterator<Item = &'a ProgressReportInput> + 'a {
    main_stage_report(records, spec, Some(goal), stage.stage)
        .into_iter()
        .chain(stage.participants.iter().filter_map(|participant| {
            let id = participant.report_id.as_deref()?;
            records.reports.iter().find(|report| report.report.id == id)
        }))
        .filter(|report| report_validity(records, spec, Some(goal), report) == Validity::Current)
        .map(|record| &record.report)
}

fn reused_acceptance_gaps<'a>(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: &'a GoalRecord,
    stages: &[StageSummary],
) -> Vec<&'a str> {
    let acceptance = &goal.goal.acceptance_refs;
    if acceptance.is_empty()
        || !records.reports.iter().any(|record| {
            record.report.goal_version == goal.goal_version
                && record.report.spec_id == goal.goal.spec_id
                && reused_report_ids(&record.report).next().is_some()
        })
    {
        return Vec::new();
    }
    let reports: Vec<_> = stages
        .iter()
        .filter(|stage| {
            (stage.applicable
                || goal.goal.target.kind != DeliveryTargetKind::Local
                    && stage.stage == Stage::Delivery)
                && matches!(
                    stage.stage,
                    Stage::Implementation | Stage::Verification | Stage::Delivery
                )
        })
        .flat_map(|stage| current_stage_reports(records, spec, goal, stage))
        .filter(|report| report.progress == Progress::Done && has_stage_evidence(report, goal))
        .collect();
    if !reports
        .iter()
        .any(|report| reused_report_ids(report).next().is_some())
    {
        return Vec::new();
    }
    acceptance
        .iter()
        .filter(|reference| {
            !reports
                .iter()
                .any(|report| covers_reference(report, reference))
        })
        .map(String::as_str)
        .collect()
}

fn automatic_delivery_ready(stages: &[StageSummary]) -> bool {
    stages
        .iter()
        .filter(|stage| stage.applicable || stage.stage == Stage::Delivery)
        .all(|stage| {
            usable_stage_evidence(stage)
                && if stage.stage == Stage::Delivery {
                    stage.progress == Progress::Done
                } else {
                    stage_completed(stage)
                }
        })
}

fn delivery_decisions<'a>(
    records: &'a ProgressRecords,
    spec: &str,
) -> BTreeMap<&'a str, &'a DecisionRecord> {
    let mut latest = BTreeMap::new();
    for decision in records.decisions.iter().filter(|d| {
        d.origin == DecisionOrigin::Manual
            && d.confirmation.spec_id == spec
            && d.confirmation.stage == Stage::Delivery
    }) {
        let version = decision.confirmation.goal_version.as_str();
        if latest
            .get(version)
            .is_none_or(|previous: &&DecisionRecord| {
                decision_order(decision) > decision_order(previous)
            })
            && records.goals.iter().any(|g| {
                g.goal.spec_id == spec
                    && g.goal_version == version
                    && g.goal.scope == decision.confirmation.scope
            })
        {
            latest.insert(version, decision);
        }
    }
    latest
}

fn project_one(snapshot: &Snapshot, records: &ProgressRecords, spec: &SpecView) -> ProgressSummary {
    let goal = goal(records, &spec.record.id);
    let cancellation =
        goal.and_then(|goal| cancellation(records, &spec.record.id, &goal.goal_version));
    let delivery = delivery_decisions(records, &spec.record.id);
    let current_delivery = goal.and_then(|goal| delivery.get(goal.goal_version.as_str()).copied());
    let accepted = current_delivery
        .filter(|decision| decision.confirmation.decision == ConfirmationDecision::Confirm);
    let last_delivery = delivery
        .values()
        .copied()
        .chain(records.decisions.iter().filter(|decision| {
            decision.origin == DecisionOrigin::Automatic
                && decision.confirmation.stage == Stage::Delivery
                && decision.confirmation.spec_id == spec.record.id
                && !delivery.contains_key(decision.confirmation.goal_version.as_str())
        }))
        .filter(|decision| decision.confirmation.decision == ConfirmationDecision::Confirm)
        .filter(|decision| {
            goal.is_some_and(|goal| same_round(records, goal, &decision.confirmation.goal_version))
        })
        .max_by_key(|d| decision_order(d))
        .cloned();
    let evidence_digest = evidence_digest(records, spec, goal, EvidenceDigestKind::Current);
    let mut stages: Vec<_> = Stage::LIFECYCLE
        .into_iter()
        .map(|stage| stage_summary(snapshot, records, spec, goal, stage, &evidence_digest))
        .collect();
    let corrected = stages
        .iter()
        .filter(|stage| stage.applicable)
        .flat_map(|stage| {
            stage.override_record.iter().chain(
                stage
                    .participants
                    .iter()
                    .filter_map(|participant| participant.override_record.as_ref()),
            )
        })
        .any(|record| !matches!(record.value.progress, Progress::Done | Progress::Skipped));
    let retains_manual_completion = accepted.is_some() && !corrected;
    if goal.is_some()
        && stages
            .iter()
            .any(|s| s.stage == Stage::Delivery && !s.applicable)
    {
        let scope_ready = stages.iter().filter(|s| s.applicable).all(stage_completed);
        let scope_validity =
            aggregate_validity(stages.iter().filter(|s| s.applicable).map(|s| s.validity));
        let delivery = stages
            .iter_mut()
            .find(|s| s.stage == Stage::Delivery)
            .expect("eight lifecycle stages");
        let local = goal.is_some_and(|g| g.goal.target.kind == DeliveryTargetKind::Local);
        let target_ready = local
            || matches!(delivery.progress, Progress::Done | Progress::Skipped)
                && delivery.validity == Validity::Current;
        let ready = scope_ready && target_ready;
        delivery.progress = if ready {
            Progress::Done
        } else if matches!(delivery.progress, Progress::Doing | Progress::Blocked) {
            delivery.progress
        } else {
            Progress::Todo
        };
        delivery.validity = if local {
            scope_validity
        } else {
            aggregate_validity([scope_validity, delivery.validity].into_iter())
        };
        delivery.summary = "目标结果确认".into();
        let target_action = std::mem::take(&mut delivery.next_action);
        delivery.next_action = if ready {
            "可按当前范围和成果证据自动确认".into()
        } else if !scope_ready {
            "核对目标适用阶段及参与工程后确认本次目标结果".into()
        } else if delivery.source == ProgressSource::Evidence && !target_action.is_empty() {
            target_action
        } else {
            "由原 Agent 上报当前版本的合并或环境验证目标凭证".into()
        };
        if delivery
            .confirmation
            .as_ref()
            .is_some_and(|d| d.confirmation.decision == ConfirmationDecision::Reject)
        {
            delivery.progress = Progress::Blocked;
            delivery.blocker = "用户已退回本次目标结果，请由原 Agent 继续处理".into();
        }
    }
    if let Some(goal) = goal {
        let missing = reused_acceptance_gaps(records, spec, goal, &stages);
        if !missing.is_empty()
            && let Some(delivery) = stages
                .iter_mut()
                .find(|stage| stage.stage == Stage::Delivery)
        {
            delivery.validity =
                aggregate_validity([delivery.validity, Validity::Unverified].into_iter());
            delivery.next_action = format!("补齐本轮尚未覆盖的验收项：{}", missing.join("、"));
        }
    }
    if let Some(decision) = current_delivery
        && let Some(stage) = stages
            .iter_mut()
            .find(|stage| stage.stage == Stage::Delivery)
    {
        stage.confirmation = Some(decision.clone());
        if accepted.is_some() {
            if stage.override_record.is_none() {
                if stage.progress != Progress::Done || stage.validity != Validity::Current {
                    stage.source = ProgressSource::Historical;
                    stage.summary = "该目标版本已验收".into();
                }
                stage.progress = Progress::Done;
                stage.next_action.clear();
            }
        } else {
            stage.progress = Progress::Blocked;
            stage.blocker = "用户已退回本次目标结果，请由原 Agent 继续处理".into();
        }
    }
    let can_automate =
        goal.is_some() && cancellation.is_none() && automatic_context(snapshot, spec);
    let auto_delivery_ready = can_automate && automatic_delivery_ready(&stages);
    let mut retains_completion = retains_manual_completion;
    if current_delivery.is_none()
        && let Some(stage) = stages
            .iter_mut()
            .find(|stage| stage.stage == Stage::Delivery)
        && stage
            .confirmation
            .as_ref()
            .is_some_and(|d| d.origin == DecisionOrigin::Automatic)
    {
        if auto_delivery_ready {
            retains_completion = true;
            stage.next_action.clear();
        } else {
            stage.confirmation = None;
        }
    }
    let mut auto_confirmable_stages = Vec::new();
    if let Some(goal) = goal.filter(|_| can_automate && !retains_completion) {
        for stage in &stages {
            if !stage.confirmation_required
                || stage.confirmation.is_some()
                || !(stage.applicable || stage.stage == Stage::Delivery)
                || !usable_stage_evidence(stage)
            {
                continue;
            }
            let ready = match stage.stage {
                Stage::Spec => {
                    (spec.content.can_confirm || spec.content.status == ContentStatus::Confirmed)
                        && (stage.source != ProgressSource::Content
                            || (spec.content.scope == goal.goal.scope
                                && spec.content.artifact_digest.as_deref()
                                    == Some(goal.goal.spec_digest.as_str())))
                }
                Stage::Verification => verification_evidence_covers(records, spec, goal, stage),
                Stage::Delivery => auto_delivery_ready,
                _ => false,
            };
            if ready {
                auto_confirmable_stages.push(stage.stage);
            }
        }
    }
    let in_scope = |stage: &&StageSummary| {
        stage.applicable || goal.is_some() && stage.stage == Stage::Delivery
    };
    let material_review = spec.content.material_review();
    let accepted_material_changed = retains_manual_completion
        && goal.is_some_and(|goal| {
            spec.content
                .artifact_digest
                .as_deref()
                .is_some_and(|current| current != goal.goal.spec_digest)
        });
    let has_stale_evidence = |stage: &StageSummary| {
        matches!(stage.validity, Validity::NeedsSync | Validity::NeedsReview)
            || stage.participants.iter().any(|participant| {
                matches!(
                    participant.validity,
                    Validity::NeedsSync | Validity::NeedsReview
                )
            })
    };
    let awaiting = |stage: &StageSummary| {
        cancellation.is_none()
            && !retains_completion
            && goal.is_some()
            && !material_review
            && stage.confirmation_required
            && !confirmation_satisfied(stage)
            && matches!(stage.progress, Progress::Done | Progress::Skipped)
            && stage.validity == Validity::Current
    };
    let mut active_stages: Vec<_> = stages
        .iter()
        .filter(in_scope)
        .filter(|stage| {
            matches!(stage.progress, Progress::Doing | Progress::Blocked)
                || stage
                    .participants
                    .iter()
                    .any(|p| matches!(p.progress, Progress::Doing | Progress::Blocked))
                || awaiting(stage)
                || has_stale_evidence(stage)
        })
        .map(|s| s.stage)
        .collect();
    let mut primary = stages
        .iter()
        .find(|s| s.applicable && !stage_completed(s))
        .or_else(|| {
            stages
                .iter()
                .find(|s| goal.is_some() && s.stage == Stage::Delivery && !stage_completed(s))
        });
    let validity = aggregate_validity(stages.iter().filter(in_scope).map(|s| s.validity));
    let mut attention = BTreeSet::new();
    let mut sync_evidence = Vec::new();
    if goal.is_none() {
        attention.insert("goalMissing".into());
    }
    for stage in stages.iter().filter(in_scope) {
        if stage.validity == Validity::NeedsSync
            && let Some(report) =
                main_stage_report(records, spec, goal, stage.stage).filter(|report| {
                    report_validity(records, spec, goal, report) == Validity::NeedsSync
                })
        {
            append_report_sync_diagnostics(&mut sync_evidence, snapshot, records, report);
        }
        for participant in stage
            .participants
            .iter()
            .filter(|participant| participant.validity == Validity::NeedsSync)
        {
            if let Some(report) = records
                .reports
                .iter()
                .find(|report| Some(report.report.id.as_str()) == participant.report_id.as_deref())
            {
                append_report_sync_diagnostics(&mut sync_evidence, snapshot, records, report);
            }
        }
        for validity in std::iter::once(stage.validity).chain(
            stage
                .participants
                .iter()
                .map(|participant| participant.validity),
        ) {
            match validity {
                Validity::Current => {}
                Validity::Unverified if retains_completion => {}
                Validity::Unverified => {
                    attention.insert("unverified".into());
                }
                Validity::NeedsSync => {
                    attention.insert("needsSync".into());
                }
                Validity::NeedsReview => {
                    attention.insert("needsReview".into());
                }
            }
        }
        if !retains_completion && (stage.progress == Progress::Blocked || !stage.blocker.is_empty())
        {
            attention.insert("blocked".into());
        }
        if awaiting(stage) {
            attention.insert(if auto_confirmable_stages.contains(&stage.stage) {
                "awaitingConfirmation".into()
            } else {
                "unverified".into()
            });
        }
        if stage.has_override {
            attention.insert("manualOverride".into());
        }
        if !stage.conflicts.is_empty() {
            attention.insert("conflict".into());
        }
    }
    if material_review || accepted_material_changed {
        attention.insert("needsReview".into());
    }
    if retains_completion {
        active_stages = stages
            .iter()
            .filter(in_scope)
            .filter(|stage| {
                has_stale_evidence(stage) || stage.stage == Stage::Spec && accepted_material_changed
            })
            .map(|stage| stage.stage)
            .collect();
        primary = None;
        for report in records.reports.iter().filter(|r| {
            r.report.spec_id == spec.record.id
                && goal.is_some_and(|g| g.goal_version == r.report.goal_version)
        }) {
            let mut changed = changed_report_inputs(records, report).peekable();
            if changed.peek().is_some()
                && latest_report(
                    records,
                    &spec.record.id,
                    goal,
                    report.report.stage,
                    report.report.project_id.as_deref(),
                    report.report.role,
                )
                .is_some_and(|latest| latest.report.id == report.report.id)
            {
                attention.insert("needsSync".into());
                sync_evidence.extend(changed.map(|id| {
                    sync_diagnostic(snapshot, report.report.stage, Some(report), Some(id), true)
                }));
                if !active_stages.contains(&report.report.stage) {
                    active_stages.push(report.report.stage);
                }
            }
        }
    }
    if sync_evidence.is_empty()
        && attention.contains("needsSync")
        && let Some(stage) = stages.iter().filter(in_scope).find(|stage| {
            stage.validity == Validity::NeedsSync
                || stage
                    .participants
                    .iter()
                    .any(|participant| participant.validity == Validity::NeedsSync)
        })
    {
        sync_evidence.push(sync_diagnostic(snapshot, stage.stage, None, None, false));
    }
    sync_evidence.sort_by(|a, b| {
        (a.paths.is_empty(), a.stage, &a.project_id, &a.report_id).cmp(&(
            b.paths.is_empty(),
            b.stage,
            &b.project_id,
            &b.report_id,
        ))
    });
    sync_evidence.dedup_by(|b, a| {
        a.stage == b.stage && a.project_id == b.project_id && a.report_id == b.report_id
    });
    let ongoing = stages.iter().filter(in_scope).any(|s| {
        s.progress == Progress::Doing
            || s.participants.iter().any(|p| p.progress == Progress::Doing)
    });
    let blocked = stages.iter().filter(in_scope).any(|s| {
        s.progress == Progress::Blocked
            || s.participants
                .iter()
                .any(|p| p.progress == Progress::Blocked)
    });
    let status = if cancellation.is_some() {
        active_stages.clear();
        primary = None;
        attention.clear();
        sync_evidence.clear();
        Status::Canceled
    } else if retains_completion {
        Status::Done
    } else if ongoing {
        Status::Doing
    } else if blocked {
        Status::Blocked
    } else if stages
        .iter()
        .filter(in_scope)
        .any(|s| matches!(s.progress, Progress::Done | Progress::Skipped))
    {
        Status::Doing
    } else if goal.is_some() || stages.iter().any(|s| s.progress == Progress::Todo) {
        Status::Todo
    } else {
        Status::Unmarked
    };
    let completion = goal.map(|_| {
        let scoped_stages = stages.iter().filter(in_scope);
        let total = scoped_stages.clone().count();
        let completed = if status == Status::Done {
            // An accepted target version can retain completion as its evidence changes.
            total
        } else {
            scoped_stages
                .filter(|stage| stage.stage != Stage::Delivery && stage_completed(stage))
                .count()
        };
        ProgressCompletion { completed, total }
    });
    let blocker = stages
        .iter()
        .filter(in_scope)
        .filter(|_| !retains_completion && cancellation.is_none())
        .find(|s| !s.blocker.is_empty())
        .map(|s| s.blocker.clone())
        .unwrap_or_default();
    ProgressSummary {
        goal: goal.cloned(),
        goal_version: goal.map(|g| g.goal_version.clone()),
        round_id: goal.map(|goal| goal_round_id(records, goal)),
        cancellation: cancellation.cloned(),
        participants: goal
            .map(|g| g.goal.participants.clone())
            .unwrap_or_default(),
        active_stages,
        primary_stage: if goal.is_none() || cancellation.is_some() {
            Stage::Unmarked
        } else {
            primary.map(|s| s.stage).unwrap_or(Stage::Delivery)
        },
        status,
        completion,
        validity,
        attention: attention.into_iter().collect(),
        sync_evidence,
        summary: if goal.is_none() {
            "未设置本轮目标".into()
        } else if let Some(record) = cancellation {
            format!("本轮目标已取消：{}", record.reason)
        } else {
            primary
                .map(|s| s.summary.clone())
                .unwrap_or_else(|| "该目标版本已验收".into())
        },
        blocker,
        next_action: primary
            .map(|s| {
                if material_review {
                    "由 Agent 核实关联材料和适用范围".into()
                } else if awaiting(s) {
                    if auto_confirmable_stages.contains(&s.stage) {
                        "按当前证据自动确认".into()
                    } else {
                        "补齐阶段的范围与验收证据，再自动确认".into()
                    }
                } else {
                    s.next_action.clone()
                }
            })
            .unwrap_or_default(),
        last_verified_at: stages
            .iter()
            .filter_map(|s| s.last_verified_at.clone())
            .max(),
        evidence_digest,
        auto_confirmable_stages,
        has_override: stages.iter().any(|s| s.has_override),
        conflicts: stages.iter().flat_map(|s| s.conflicts.clone()).collect(),
        current_versions: records
            .current_versions
            .iter()
            .filter(|v| {
                v.project_id == spec.record.project_id
                    || goal.is_some_and(|g| {
                        g.goal
                            .participants
                            .iter()
                            .any(|p| p.project_id == v.project_id)
                    })
            })
            .cloned()
            .collect(),
        last_delivery,
        material_review,
        stages,
    }
}
