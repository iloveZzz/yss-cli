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
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, clap::ValueEnum)]
#[serde(rename_all = "camelCase")]
pub enum ConfirmationDecision {
    Confirm,
    Reject,
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
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProgressSummary {
    pub goal: Option<GoalRecord>,
    pub goal_version: Option<String>,
    pub stages: Vec<StageSummary>,
    pub participants: Vec<Participant>,
    pub active_stages: Vec<Stage>,
    pub primary_stage: Stage,
    pub status: Status,
    pub validity: Validity,
    pub attention: Vec<String>,
    pub summary: String,
    pub blocker: String,
    pub next_action: String,
    pub last_verified_at: Option<String>,
    pub evidence_digest: String,
    pub has_override: bool,
    pub conflicts: Vec<String>,
    #[serde(default)]
    pub current_versions: Vec<ProjectVersion>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_delivery: Option<DecisionRecord>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub material_review: bool,
}

pub const RECORD_KINDS: &[&str] = &[
    "lifecycleGoal",
    "progressEvidence",
    "progressOverride",
    "progressDecision",
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
        let records = Self {
            goals: decode(raw, RECORD_KINDS[0])?,
            reports: decode(raw, RECORD_KINDS[1])?,
            overrides: decode(raw, RECORD_KINDS[2])?,
            decisions: decode(raw, RECORD_KINDS[3])?,
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
        {
            return Err(sources::invalid("不支持的生命周期记录版本"));
        }
        Ok(())
    }
}

pub fn load(store: &mut Store) -> Result<ProgressRecords> {
    let records = ProgressRecords {
        goals: store.records(RECORD_KINDS[0])?,
        reports: store.records(RECORD_KINDS[1])?,
        overrides: store.records(RECORD_KINDS[2])?,
        decisions: store.records(RECORD_KINDS[3])?,
        current_versions: Vec::new(),
        checked_inputs: BTreeMap::new(),
    };
    records.validate_schema()?;
    Ok(records)
}

/// Read only participating repositories. An unavailable repository stays unknown.
pub fn current_versions(
    state: &WorkbenchState,
    project_ids: &BTreeSet<String>,
) -> Vec<ProjectVersion> {
    state
        .projects
        .iter()
        .filter(|p| project_ids.contains(&p.id))
        .map(|project| {
            read_project_version(project).unwrap_or_else(|_| ProjectVersion {
                project_id: project.id.clone(),
                commit: None,
                working_tree_digest: None,
            })
        })
        .collect()
}

fn read_project_version(project: &ProjectRecord) -> Result<ProjectVersion> {
    use std::time::Duration;
    let root = Root::open(Path::new(&project.root_path))?;
    let git = |args: &[&str]| {
        let mut command = std::process::Command::new("git");
        command
            .arg("--no-optional-locks")
            .arg("-C")
            .arg(&project.root_path)
            .args(args);
        super::process::run_command(command, &Cancellation::new(), Duration::from_secs(5))
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
        if path
            .as_str()
            .split('/')
            .any(|p| matches!(p, ".git" | ".yss" | "node_modules"))
        {
            return Err(sources::invalid("凭证范围包含版本库或本机工具数据"));
        }
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
    for report in records
        .reports
        .iter()
        .filter(|r| current.contains(&(&r.report.spec_id, &r.report.goal_version)))
    {
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
    if read_project_version(project)? != *version || inputs_digest(&root, paths, cancel)? != hash {
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
    let version = read_project_version(project)?;
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
) -> Result<Vec<CheckedInputs>> {
    let mut projects = BTreeSet::new();
    let mut checked = Vec::new();
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
        let project = snapshot
            .projects
            .iter()
            .find(|p| p.record.id == scope.project_id)
            .ok_or_else(|| sources::invalid("凭证输入工程未登记"))?;
        let version = input
            .versions
            .iter()
            .find(|v| v.project_id == scope.project_id)
            .ok_or_else(|| sources::invalid("凭证输入须绑定报告实际采用的工程版本"))?;
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
            return Err(
                sources::invalid("当前输入与实际检查时的摘要不同，请对当前版本重新检查")
                    .context("httpStatus", 409),
            );
        }
        checked.push(CheckedInputs {
            project_id: scope.project_id.clone(),
            digest: hash,
        });
    }
    Ok(checked)
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
fn goal<'a>(records: &'a ProgressRecords, spec_id: &str) -> Option<&'a GoalRecord> {
    records
        .goals
        .iter()
        .filter(|g| g.goal.spec_id == spec_id)
        .max_by_key(|g| (ordered_time(&g.created_at), g.goal_version.as_str()))
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
fn project_exists(snapshot: &Snapshot, id: &str) -> bool {
    snapshot.projects.iter().any(|p| p.record.id == id)
}

pub(crate) fn prepare_goal(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    input: LifecycleGoalInput,
    at: &str,
) -> Result<CommitEffects> {
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
    let goal_version = digest(&input);
    if goal(records, &input.spec_id).is_some_and(|g| g.goal_version == goal_version) {
        return Ok(CommitEffects::default());
    }
    let record = GoalRecord {
        schema_version: 1,
        goal_version: goal_version.clone(),
        created_at: at.into(),
        goal: input,
    };
    write(
        RECORD_KINDS[0],
        format!("{}:{goal_version}", record.goal.spec_id),
        &record,
    )
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
            || !project_exists(snapshot, &version.project_id)
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
    let checked_inputs = capture_report_inputs(snapshot, records, &input)?;
    let id = input.id.clone();
    write(
        RECORD_KINDS[1],
        id,
        &ReportRecord {
            schema_version: 1,
            recorded_at: at.into(),
            report: input,
            checked_inputs,
        },
    )
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
    let goal = current_goal(records, spec_id, version)?;
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
    let goal = current_goal(records, &input.spec_id, &input.goal_version)?;
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
    let summary = project_one(snapshot, records, spec);
    if !valid_digest(&input.evidence_digest) || summary.evidence_digest != input.evidence_digest {
        return Err(sources::invalid("证据已变化，请刷新后重新确认").context("httpStatus", 409));
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

fn evidence_digest(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    legacy: bool,
) -> String {
    let version = goal.map(|g| g.goal_version.as_str());
    let mut reports: Vec<_> = records
        .reports
        .iter()
        .filter(|r| {
            r.report.spec_id == spec.record.id && Some(r.report.goal_version.as_str()) == version
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
    if !legacy {
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
            && !r.report.id.starts_with("activity:")
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
    let relevant_digest = stage_evidence_digest(records, spec, goal, stage, false);
    let previous_digest = stage_evidence_digest(records, spec, goal, stage, true);
    let previous_global = evidence_digest(records, spec, goal, true);
    records
        .decisions
        .iter()
        .filter(|d| {
            d.confirmation.spec_id == spec.record.id
                && d.confirmation.stage == stage
                && d.stage_evidence_digest.as_deref().map_or_else(
                    || {
                        d.confirmation.evidence_digest == global_digest
                            || d.confirmation.evidence_digest == previous_global
                    },
                    |digest| digest == relevant_digest || digest == previous_digest,
                )
                && goal.is_some_and(|g| {
                    d.confirmation.goal_version == g.goal_version
                        && d.confirmation.scope == g.goal.scope
                })
        })
        .max_by_key(|d| decision_order(d))
}

fn stage_participants(goal: Option<&GoalRecord>, stage: Stage) -> Vec<&Participant> {
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

fn latest_report<'a>(
    records: &'a ProgressRecords,
    spec_id: &str,
    goal: Option<&GoalRecord>,
    stage: Stage,
    project_id: Option<&str>,
    role: ParticipantRole,
) -> Option<&'a ReportRecord> {
    let matching = |r: &&ReportRecord| {
        r.report.spec_id == spec_id
            && r.report.stage == stage
            && r.report.project_id.as_deref() == project_id
            && r.report.role == role
    };
    let current = records
        .reports
        .iter()
        .filter(matching)
        .filter(|r| goal.is_some_and(|g| r.report.goal_version == g.goal_version))
        .max_by(|a, b| (&a.recorded_at, &a.report.id).cmp(&(&b.recorded_at, &b.report.id)));
    current.or_else(|| {
        records
            .reports
            .iter()
            .filter(matching)
            .max_by(|a, b| (&a.recorded_at, &a.report.id).cmp(&(&b.recorded_at, &b.report.id)))
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
        Validity::Unverified
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
                    && goal.goal.target.reference.as_ref().is_none_or(|reference| {
                        e.reference == *reference || report.scope_refs.contains(reference)
                    })
            }),
        },
        Stage::Unmarked => false,
    }
}
fn report_validity(
    records: &ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    record: &ReportRecord,
) -> Validity {
    let Some(goal) = goal else {
        return Validity::Unverified;
    };
    let report = &record.report;
    if report.goal_version != goal.goal_version || report.spec_digest != goal.goal.spec_digest {
        return stale_validity(report.stage);
    }
    if report.validity != Validity::Current {
        return report.validity;
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
            let relevant: Vec<_> = if let Some(id) = &report.project_id {
                vec![id]
            } else {
                goal.goal
                    .participants
                    .iter()
                    .filter(|p| p.role != ParticipantRole::Spec)
                    .map(|p| &p.project_id)
                    .collect()
            };
            let relevant = if relevant.is_empty() {
                vec![&spec.record.project_id]
            } else {
                relevant
            };
            let exact_version = report.role == ParticipantRole::Integration
                || report
                    .evidence
                    .iter()
                    .any(|e| matches!(e.kind, EvidenceKind::Merge | EvidenceKind::Environment))
                || goal.goal.target.kind != DeliveryTargetKind::Local
                    && matches!(report.stage, Stage::Verification | Stage::Delivery);
            let validity = aggregate_validity(
                relevant
                    .into_iter()
                    .map(|id| engineering_validity(records, record, id, exact_version)),
            );
            if validity != Validity::Current {
                return validity;
            }
        }
        // A cross-project verification statement must identify its tested combination.
        if report.stage == Stage::Verification && report.role == ParticipantRole::Integration {
            for participant in
                goal.goal.participants.iter().filter(|p| {
                    matches!(p.role, ParticipantRole::Backend | ParticipantRole::Frontend)
                })
            {
                let validity = engineering_validity(records, record, &participant.project_id, true);
                if validity != Validity::Current {
                    return validity;
                }
            }
        }
    }
    Validity::Current
}

fn last_successful_report(
    records: &ProgressRecords,
    spec: &SpecView,
    stage: Stage,
    project_id: Option<&str>,
    role: ParticipantRole,
) -> Option<String> {
    records
        .reports
        .iter()
        .filter(|record| {
            let report = &record.report;
            if report.spec_id != spec.record.id
                || report.stage != stage
                || report.role != role
                || report.id.starts_with("activity:")
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
            if matches!(
                stage,
                Stage::Implementation | Stage::Verification | Stage::Delivery
            ) {
                let mut projects: Vec<_> = if let Some(project) = &report.project_id {
                    vec![project.as_str()]
                } else {
                    goal.goal
                        .participants
                        .iter()
                        .filter(|p| p.role != ParticipantRole::Spec)
                        .map(|p| p.project_id.as_str())
                        .collect()
                };
                if projects.is_empty() {
                    projects.push(&spec.record.project_id);
                }
                if stage == Stage::Verification && role == ParticipantRole::Integration {
                    projects.extend(
                        goal.goal
                            .participants
                            .iter()
                            .filter(|p| {
                                matches!(
                                    p.role,
                                    ParticipantRole::Backend | ParticipantRole::Frontend
                                )
                            })
                            .map(|p| p.project_id.as_str()),
                    );
                }
                if !projects.into_iter().all(|id| {
                    report.versions.iter().any(|v| {
                        v.project_id == id
                            && (v.commit.is_some() || v.working_tree_digest.is_some())
                    })
                }) {
                    return false;
                }
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
        view.next_action = report.report.next_action.clone();
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

fn stage_summary(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    spec: &SpecView,
    goal: Option<&GoalRecord>,
    stage: Stage,
    evidence_digest: &str,
) -> StageSummary {
    let scope = goal.and_then(|g| g.goal.stages.iter().find(|s| s.stage == stage));
    let applicable = goal.is_none() || scope.is_some_and(|s| s.applicable);
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
        last_verified_at: last_successful_report(records, spec, stage, None, ParticipantRole::Spec),
        has_override: false,
        conflicts: Vec::new(),
        legacy,
        versions: Vec::new(),
        override_record: None,
    };
    if stage == Stage::Spec {
        view.last_verified_at = spec
            .content
            .confirmations
            .iter()
            .map(|confirmation| confirmation.created_at.clone())
            .chain(view.last_verified_at)
            .max();
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
    let report = [None, Some(spec.record.project_id.as_str())]
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
        })
        .max_by(|a, b| {
            let current =
                |r: &ReportRecord| goal.is_some_and(|g| r.report.goal_version == g.goal_version);
            (current(a), &a.recorded_at, &a.report.id).cmp(&(
                current(b),
                &b.recorded_at,
                &b.report.id,
            ))
        });
    if let Some(report) = report {
        view.progress = report.report.progress;
        view.validity = report_validity(records, spec, goal, report);
        view.source = ProgressSource::Evidence;
        view.summary = report.report.summary.clone();
        view.blocker = report.report.blocker.clone();
        view.next_action = report.report.next_action.clone();
        view.evidence = report.report.evidence.clone();
        view.versions = report.report.versions.clone();
    } else if stage == Stage::Spec && spec.content.artifact_digest.is_some() {
        match spec.content.status {
            ContentStatus::Confirmed => {
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
                        Some(c.artifact_digest.as_str()) == spec.content.artifact_digest.as_deref()
                    })
                    .map(|c| c.created_at.clone())
                    .chain(view.last_verified_at)
                    .max();
            }
            ContentStatus::Ready => {
                view.progress = Progress::Done;
                view.validity = Validity::Current;
                view.source = ProgressSource::Content;
                view.summary = "规格材料已就绪，待人工确认".into();
                view.next_action = "确认当前目标范围的规格内容".into();
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
        view.confirmation_required = goal.is_some_and(|g| g.goal.scope != spec.content.scope);
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
    let decision = matching_decision(records, spec, goal, stage, evidence_digest);
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

fn delivery_decisions<'a>(
    records: &'a ProgressRecords,
    spec: &str,
) -> BTreeMap<&'a str, &'a DecisionRecord> {
    let mut latest = BTreeMap::new();
    for decision in records
        .decisions
        .iter()
        .filter(|d| d.confirmation.spec_id == spec && d.confirmation.stage == Stage::Delivery)
    {
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
    let delivery = delivery_decisions(records, &spec.record.id);
    let current_delivery = goal.and_then(|goal| delivery.get(goal.goal_version.as_str()).copied());
    let accepted = current_delivery
        .filter(|decision| decision.confirmation.decision == ConfirmationDecision::Confirm);
    let last_delivery = delivery
        .values()
        .filter(|decision| decision.confirmation.decision == ConfirmationDecision::Confirm)
        .max_by_key(|d| decision_order(d))
        .map(|d| (**d).clone());
    let evidence_digest = evidence_digest(records, spec, goal, false);
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
    let retains_completion = accepted.is_some() && !corrected;
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
        delivery.next_action = if ready {
            "请用户确认当前目标范围与实际成果".into()
        } else if !scope_ready {
            "核对目标适用阶段及参与工程后确认本次目标结果".into()
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
    let in_scope = |stage: &&StageSummary| {
        stage.applicable || goal.is_some() && stage.stage == Stage::Delivery
    };
    let material_review = spec.content.material_review();
    let awaiting = |stage: &StageSummary| {
        !retains_completion
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
                || matches!(stage.validity, Validity::NeedsSync | Validity::NeedsReview)
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
    if goal.is_none() {
        attention.insert("goalMissing".into());
    }
    for stage in stages.iter().filter(in_scope) {
        match stage.validity {
            _ if retains_completion => {}
            Validity::Current => {}
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
        if !retains_completion && (stage.progress == Progress::Blocked || !stage.blocker.is_empty())
        {
            attention.insert("blocked".into());
        }
        if awaiting(stage) {
            attention.insert("awaitingConfirmation".into());
        }
        if stage.has_override {
            attention.insert("manualOverride".into());
        }
        if !stage.conflicts.is_empty() {
            attention.insert("conflict".into());
        }
    }
    if material_review {
        attention.insert("needsReview".into());
    }
    if retains_completion {
        active_stages.clear();
        primary = None;
        for report in records.reports.iter().filter(|r| {
            r.report.spec_id == spec.record.id
                && goal.is_some_and(|g| g.goal_version == r.report.goal_version)
        }) {
            if report.checked_inputs.iter().any(|inputs| {
                scoped_validity(records, report, &inputs.project_id) == Validity::NeedsSync
            }) && latest_report(
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
                if !active_stages.contains(&report.report.stage) {
                    active_stages.push(report.report.stage);
                }
            }
        }
    }
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
    let status = if retains_completion {
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
    let blocker = stages
        .iter()
        .filter(in_scope)
        .filter(|_| !retains_completion)
        .find(|s| !s.blocker.is_empty())
        .map(|s| s.blocker.clone())
        .unwrap_or_default();
    ProgressSummary {
        goal: goal.cloned(),
        goal_version: goal.map(|g| g.goal_version.clone()),
        participants: goal
            .map(|g| g.goal.participants.clone())
            .unwrap_or_default(),
        active_stages,
        primary_stage: primary.map(|s| s.stage).unwrap_or(Stage::Delivery),
        status,
        validity,
        attention: attention.into_iter().collect(),
        summary: primary
            .map(|s| s.summary.clone())
            .unwrap_or_else(|| "该目标版本已验收".into()),
        blocker,
        next_action: primary
            .map(|s| {
                if material_review {
                    "由 Agent 核实关联材料和适用范围".into()
                } else if awaiting(s) {
                    "请人工确认当前范围与证据".into()
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
