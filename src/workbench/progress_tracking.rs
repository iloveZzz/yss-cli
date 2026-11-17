//! Read-only remote follow-up for explicitly linked, active Specs.
//!
//! Polling reuses the controlled external-refresh jobs and their resource locks.
//! Remote observations supplement a scope-bound Agent report. A passing pipeline
//! supplies a technical fact, and semantic completion and business acceptance
//! continue to use the versioned lifecycle evidence and manual decisions.
use super::{
    integrations::{self, ExternalKind, ExternalLink, RefreshExternal},
    jobs::{JobManager, lock_store},
    model::{Action, MutationRequest, Snapshot, Stage},
    progress::{
        self, Evidence, EvidenceKind, EvidenceOutcome, GoalRecord, ParticipantRole, Progress,
        ProgressRecords, ProgressReportInput, ProjectVersion, Validity,
    },
    service,
};
use crate::{cancel::Cancellation, error::Result, model::Sha256};
use serde::Serialize;
use serde_json::Value;
use std::{
    collections::{BTreeMap, BTreeSet},
    sync::Arc,
    thread::JoinHandle,
    time::{Duration, Instant},
};

const INTERVAL: Duration = Duration::from_secs(60);

struct PollState {
    source: String,
    last_attempt: Option<String>,
    last_fact: Option<String>,
    pending_job: Option<String>,
    failures: u32,
    paused: bool,
    next_poll: Instant,
}
impl PollState {
    fn new(link: &ExternalLink) -> Self {
        let elapsed = link
            .last_attempt_at
            .as_deref()
            .and_then(|at| {
                time::OffsetDateTime::parse(at, &time::format_description::well_known::Rfc3339).ok()
            })
            .map(|at| {
                (time::OffsetDateTime::now_utc() - at)
                    .whole_seconds()
                    .max(0) as u64
            })
            .unwrap_or(60);
        Self {
            source: fingerprint(&link.target),
            last_attempt: link.last_attempt_at.clone(),
            last_fact: None,
            pending_job: None,
            failures: 0,
            paused: authentication_error(link.last_error.as_deref()),
            next_poll: Instant::now() + INTERVAL.saturating_sub(Duration::from_secs(elapsed)),
        }
    }
}
fn fingerprint<T: Serialize>(value: &T) -> String {
    Sha256::of(&serde_json::to_vec(value).expect("remote observation serializes"))
        .as_str()
        .to_owned()
}
fn authentication_error(message: Option<&str>) -> bool {
    message.is_some_and(|message| {
        message.contains("认证")
            || message.contains("登录")
            || message.to_ascii_lowercase().contains("authentication")
    })
}
fn retry_delay(failures: u32) -> Duration {
    Duration::from_secs(
        60u64
            .saturating_mul(1u64 << failures.saturating_sub(1).min(4))
            .min(600),
    )
}

/// Start one bounded, cancellable polling loop. Dropping its handle detaches it.
pub fn start(
    manager: Arc<JobManager>,
    cancel: Cancellation,
    shutdown: Cancellation,
) -> JoinHandle<()> {
    std::thread::spawn(move || {
        let mut states = BTreeMap::<String, PollState>::new();
        while !cancel.is_cancelled() && !shutdown.is_cancelled() {
            let _ = tick(&manager, &mut states);
            for _ in 0..10 {
                if cancel.is_cancelled() || shutdown.is_cancelled() {
                    break;
                }
                std::thread::sleep(Duration::from_millis(100));
            }
        }
    })
}

fn tick(manager: &Arc<JobManager>, states: &mut BTreeMap<String, PollState>) -> Result<()> {
    let links = integrations::external_list(&manager.store)?;
    let (active_specs, goal_versions) = {
        let mut store = lock_store(&manager.store)?;
        let active_specs: BTreeSet<_> = store
            .state()
            .specs
            .iter()
            .filter(|s| s.active)
            .map(|s| s.id.clone())
            .collect();
        let records = progress::load(&mut store)?;
        let mut latest: BTreeMap<String, &GoalRecord> = BTreeMap::new();
        for goal in &records.goals {
            if latest.get(&goal.goal.spec_id).is_none_or(|existing| {
                (&existing.created_at, &existing.goal_version)
                    < (&goal.created_at, &goal.goal_version)
            }) {
                latest.insert(goal.goal.spec_id.clone(), goal);
            }
        }
        let versions = latest
            .into_iter()
            .map(|(spec_id, goal)| {
                let mut semantic_ids: Vec<_> = records
                    .reports
                    .iter()
                    .filter(|r| {
                        r.report.spec_id == spec_id
                            && r.report.goal_version == goal.goal_version
                            && !r.report.id.starts_with("remote:")
                    })
                    .map(|r| &r.report.id)
                    .collect();
                semantic_ids.sort();
                (spec_id, fingerprint(&(&goal.goal_version, semantic_ids)))
            })
            .collect::<BTreeMap<_, _>>();
        (active_specs, versions)
    };
    let known: BTreeSet<_> = links.iter().map(|l| l.id.clone()).collect();
    states.retain(|id, _| known.contains(id));
    for link in links {
        let Some(spec_id) = link
            .target
            .spec_id
            .as_ref()
            .filter(|id| active_specs.contains(*id))
        else {
            continue;
        };
        let state = states
            .entry(link.id.clone())
            .or_insert_with(|| PollState::new(&link));
        let source_changed = state.source != fingerprint(&link.target);
        if source_changed {
            *state = PollState::new(&link);
            state.paused = false;
            state.next_poll = Instant::now();
        }
        if let Some(job_id) = state.pending_job.clone() {
            let job = manager.get(&job_id)?;
            if job.terminal() {
                state.pending_job = None;
                state.last_attempt = link.last_attempt_at.clone();
                if job.status == "completed" {
                    state.failures = 0;
                    state.paused = false;
                    state.next_poll = Instant::now() + INTERVAL;
                    state.last_fact = None;
                } else {
                    state.failures = state.failures.saturating_add(1);
                    state.paused = authentication_error(
                        job.error
                            .as_ref()
                            .and_then(|e| e.get("message"))
                            .and_then(Value::as_str),
                    );
                    state.next_poll = Instant::now() + retry_delay(state.failures);
                }
            }
        } else if state.last_attempt != link.last_attempt_at {
            // A manual refresh is an explicit retry after credentials/source changes.
            state.last_attempt = link.last_attempt_at.clone();
            state.failures = 0;
            state.paused =
                authentication_error(link.last_error.as_deref()) && link.status != "refreshing";
            state.next_poll = Instant::now() + INTERVAL;
        }
        if state.pending_job.is_none()
            && !source_changed
            && link.status == "stale"
            && authentication_error(link.last_error.as_deref())
        {
            state.paused = true;
        }
        if link.status == "current" {
            state.paused = false;
        }
        if matches!(link.status.as_str(), "current" | "stale" | "canceled") {
            let fact = fingerprint(&(
                &link.target,
                &link.status,
                &link.last_error,
                &link.data,
                goal_versions.get(spec_id),
            ));
            if state.last_fact.as_ref() != Some(&fact) && submit_observation(manager, &link).is_ok()
            {
                state.last_fact = Some(fact);
            }
        }
        if state.pending_job.is_none() && !state.paused && Instant::now() >= state.next_poll {
            let request_id = format!("tracking-{}", crate::fs::unique_id()?);
            match integrations::external_refresh(
                manager.clone(),
                RefreshExternal {
                    id: link.id.clone(),
                    request_id,
                },
            ) {
                Ok(job) => state.pending_job = Some(job.id),
                Err(_) => state.next_poll = Instant::now() + Duration::from_secs(5),
            }
        }
    }
    Ok(())
}

fn submit_observation(manager: &JobManager, link: &ExternalLink) -> Result<()> {
    let mut store = lock_store(&manager.store)?;
    let mut records = progress::load(&mut store)?;
    let Some(spec_id) = &link.target.spec_id else {
        return Ok(());
    };
    let Some(goal) = records
        .goals
        .iter()
        .filter(|g| &g.goal.spec_id == spec_id)
        .max_by(|a, b| (&a.created_at, &a.goal_version).cmp(&(&b.created_at, &b.goal_version)))
        .cloned()
    else {
        return Ok(());
    };
    let Some(spec) = store.state().specs.iter().find(|s| &s.id == spec_id) else {
        return Ok(());
    };
    let project_id = link
        .target
        .project_id
        .as_deref()
        .unwrap_or(&spec.project_id)
        .to_owned();
    let mut projects: BTreeSet<_> = goal
        .goal
        .participants
        .iter()
        .map(|p| p.project_id.clone())
        .collect();
    projects.insert(spec.project_id.clone());
    records.current_versions = progress::current_versions(store.state(), &projects);
    let snapshot = service::snapshot(store.state(), &Cancellation::new())?;
    let reports = observation_reports(&snapshot, &records, &goal, link, &project_id);
    for input in reports {
        let expected_revision = store.state().revision;
        service::mutate(
            &mut store,
            MutationRequest {
                expected_revision,
                expected_target: None,
                request_id: Some(format!("tracking-{}", &input.id[7..])),
                action: Action::ReportProgress { input },
            },
        )?;
    }
    Ok(())
}

fn value_str<'a>(data: &'a Value, key: &str) -> Option<&'a str> {
    data.get(key).and_then(Value::as_str)
}
fn remote_sha(data: &Value) -> Option<&str> {
    value_str(data, "head_sha").or_else(|| value_str(data, "sha"))
}
fn is_merged(data: &Value) -> bool {
    data.get("merged").and_then(Value::as_bool) == Some(true)
        || value_str(data, "merged_at").is_some()
        || value_str(data, "state") == Some("merged")
}
fn pipeline_outcome(data: &Value) -> Option<EvidenceOutcome> {
    match value_str(data, "conclusion").or_else(|| value_str(data, "status")) {
        Some("success" | "passed") => Some(EvidenceOutcome::Passed),
        Some("failure" | "failed" | "timed_out" | "canceled" | "cancelled" | "action_required") => {
            Some(EvidenceOutcome::Failed)
        }
        _ => None,
    }
}

fn observation_reports(
    snapshot: &Snapshot,
    records: &ProgressRecords,
    goal: &GoalRecord,
    link: &ExternalLink,
    project_id: &str,
) -> Vec<ProgressReportInput> {
    let Some(data) = &link.data else {
        return Vec::new();
    };
    let Some(spec) = snapshot
        .specs
        .iter()
        .find(|s| s.record.id == goal.goal.spec_id)
    else {
        return Vec::new();
    };
    let roles: Vec<_> = if project_id == spec.record.project_id {
        vec![ParticipantRole::Spec]
    } else {
        goal.goal
            .participants
            .iter()
            .filter(|p| p.project_id == project_id)
            .map(|p| p.role)
            .collect()
    };
    if roles.is_empty() {
        return Vec::new();
    }
    let stages: Vec<_> = match link.target.kind {
        ExternalKind::Issue => return Vec::new(),
        ExternalKind::Pipeline => vec![Stage::Verification],
        ExternalKind::PullRequest
            if goal.goal.target.kind == progress::DeliveryTargetKind::Merged =>
        {
            vec![Stage::Implementation, Stage::Delivery]
        }
        ExternalKind::PullRequest => vec![Stage::Implementation],
    };
    let actual = records
        .current_versions
        .iter()
        .find(|v| v.project_id == project_id);
    let observed_sha = actual
        .and_then(|a| a.commit.as_deref())
        .filter(|commit| {
            remote_sha(data) == Some(*commit)
                || is_merged(data) && value_str(data, "merge_commit_sha") == Some(*commit)
        })
        .or_else(|| remote_sha(data));
    let versions = observed_sha
        .map(|sha| {
            vec![ProjectVersion {
                project_id: project_id.into(),
                commit: Some(sha.into()),
                working_tree_digest: None,
            }]
        })
        .unwrap_or_default();
    let clean = actual.is_some_and(|version| {
        version.working_tree_digest.as_deref() == Some(Sha256::of(&[]).as_str())
    });
    let matches_head =
        clean && observed_sha.is_some() && actual.and_then(|v| v.commit.as_deref()) == observed_sha;
    let fresh = link.status == "current"
        && spec.content.artifact_digest.as_deref() == Some(goal.goal.spec_digest.as_str());
    let reference = value_str(data, "url").unwrap_or("").to_owned();
    if reference.is_empty() {
        return Vec::new();
    }
    let mut result = Vec::new();
    for stage in stages.into_iter().filter(|stage| {
        *stage == Stage::Delivery
            || goal
                .goal
                .stages
                .iter()
                .any(|s| s.stage == *stage && s.applicable)
    }) {
        let stage_roles = if stage == Stage::Delivery {
            vec![ParticipantRole::Spec]
        } else {
            roles.clone()
        };
        for role in &stage_roles {
            let semantic = records
                .reports
                .iter()
                .filter(|r| {
                    !r.report.id.starts_with("remote:")
                        && r.report.goal_version == goal.goal_version
                        && r.report.stage == stage
                        && r.report.role == *role
                        && r.report.spec_id == goal.goal.spec_id
                        && (stage == Stage::Delivery && r.report.project_id.is_none()
                            || stage != Stage::Delivery
                                && (r.report.project_id.as_deref() == Some(project_id)
                                    || *role == ParticipantRole::Spec
                                        && r.report.project_id.is_none()))
                })
                .max_by(|a, b| (&a.recorded_at, &a.report.id).cmp(&(&b.recorded_at, &b.report.id)));
            let explicitly_referenced = goal.goal.acceptance_refs.contains(&reference)
                || goal.goal.target.reference.as_ref() == Some(&reference)
                || semantic.is_some_and(|r| {
                    r.report.scope_refs.contains(&reference)
                        || r.report.evidence.iter().any(|e| e.reference == reference)
                });
            if semantic.is_some() && !explicitly_referenced {
                continue;
            }
            let scope_bound = explicitly_referenced
                && semantic.is_some_and(|r| {
                    r.report.spec_digest == goal.goal.spec_digest
                        && r.report.validity == Validity::Current
                        && r.report.versions.iter().any(|v| {
                            v.project_id == project_id && v.commit.as_deref() == observed_sha
                        })
                });
            let outcome = if link.target.kind == ExternalKind::PullRequest {
                if is_merged(data) {
                    Some(EvidenceOutcome::Passed)
                } else {
                    Some(EvidenceOutcome::Observed)
                }
            } else {
                pipeline_outcome(data)
            };
            let technical_done = match link.target.kind {
                ExternalKind::PullRequest => is_merged(data),
                ExternalKind::Pipeline => outcome == Some(EvidenceOutcome::Passed),
                ExternalKind::Issue => false,
            };
            let progress = if fresh
                && matches_head
                && scope_bound
                && (technical_done || stage == Stage::Implementation)
            {
                semantic
                    .map(|r| r.report.progress)
                    .unwrap_or(Progress::Doing)
            } else if fresh
                && matches_head
                && scope_bound
                && outcome == Some(EvidenceOutcome::Failed)
            {
                Progress::Blocked
            } else {
                Progress::Doing
            };
            let validity = if fresh && matches_head && scope_bound {
                Validity::Current
            } else {
                Validity::Unverified
            };
            let mut evidence = semantic
                .map(|r| r.report.evidence.clone())
                .unwrap_or_default();
            evidence.push(Evidence {
                kind: if link.target.kind == ExternalKind::PullRequest {
                    EvidenceKind::Merge
                } else {
                    EvidenceKind::Test
                },
                label: format!("远程 {:?} #{}", link.target.kind, link.target.number),
                reference: reference.clone(),
                outcome,
            });
            let mut scope_refs = semantic
                .map(|r| r.report.scope_refs.clone())
                .unwrap_or_default();
            scope_refs.push(reference.clone());
            let mut all_versions = semantic
                .map(|r| r.report.versions.clone())
                .unwrap_or_default();
            all_versions.retain(|v| v.project_id != project_id);
            all_versions.extend(versions.clone());
            let summary = if !matches_head {
                "远程记录与当前工程版本尚未匹配，待原 Agent 核实"
            } else if !fresh {
                "远程事实已过期，待重新读取与核对规格版本"
            } else if !scope_bound {
                "远程技术事实已读取，当前目标完成范围待原 Agent 核实"
            } else if technical_done {
                "当前版本远程技术事实已核实，沿用原 Agent 的范围判断"
            } else {
                "当前版本远程技术执行尚未完成"
            };
            let key = fingerprint(&(
                &link.id,
                &goal.goal_version,
                stage,
                role,
                &project_id,
                &link.status,
                data,
                semantic.map(|r| &r.report.id),
                &all_versions,
                actual,
                progress,
                validity,
            ));
            result.push(ProgressReportInput {
                id: format!("remote:{key}"),
                spec_id: goal.goal.spec_id.clone(),
                goal_version: goal.goal_version.clone(),
                spec_digest: goal.goal.spec_digest.clone(),
                stage,
                project_id: if stage == Stage::Delivery {
                    None
                } else {
                    Some(project_id.into())
                },
                role: *role,
                progress,
                validity,
                summary: summary.into(),
                blocker: if progress == Progress::Blocked {
                    "当前目标明确引用的远程执行失败，请由原 Agent 核对并处理".into()
                } else {
                    String::new()
                },
                next_action: if validity == Validity::Current && progress == Progress::Done {
                    "核对当前范围的人工验收与最终交付确认".into()
                } else {
                    "由原 Agent 核对目标、代码版本和远程执行范围后上报".into()
                },
                scope_refs,
                versions: all_versions,
                evidence,
            });
        }
    }
    result
}
