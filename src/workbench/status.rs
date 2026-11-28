use super::{
    model::{ResourceHealth, Snapshot, SourceHealth, SpecView, Stage, Status},
    progress::{
        ConfirmationDecision, DeliveryTargetKind, Evidence, EvidenceOutcome, ParticipantRole,
        Progress, ProgressSummary, Validity,
    },
    service,
    sources::missing,
};
use crate::{cancel::Cancellation, error::Result};
use serde::Serialize;
use serde_json::{Value, json};
use std::path::Path;

pub(crate) enum ReportKind {
    Spec,
    Lifecycle,
    Recommendation,
}

pub(crate) fn read(
    data_dir: &Path,
    spec_id: &str,
    kind: ReportKind,
    cancel: &Cancellation,
) -> Result<Value> {
    let snapshot = service::read_snapshot_scoped(data_dir, Some(spec_id), cancel)?;
    let spec = snapshot
        .specs
        .iter()
        .find(|spec| spec.record.id == spec_id)
        .ok_or_else(|| missing("未找到 Spec 登记，请核对工作台数据目录、工程根和 Spec ID"))?;
    let project = snapshot
        .projects
        .iter()
        .find(|project| project.record.id == spec.record.project_id)
        .ok_or_else(|| missing("未找到 Spec 所属工程登记，请核对工作台数据目录和工程根"))?;
    let active = project.record.active && spec.record.active;
    let progress = snapshot.progress.get(spec_id);
    let goal_state = goal_state(progress);
    let mut report = json!({
        "revision": snapshot.revision,
        "readAt": snapshot.read_at,
        "specId": spec_id,
        "projectId": spec.record.project_id,
        "projectRoot": project.record.root_path,
        "sourcePath": spec.record.source_path,
        "title": spec.record.title,
        "active": active,
        "goalState": goal_state,
        "sourceHealth": spec.source_health,
        "sourceMessage": spec.source_message,
        "sourceModifiedAt": spec.source_modified_at,
    });
    match kind {
        ReportKind::Spec => {
            report["content"] = spec_content(spec);
            report["progress"] = json!(progress.map(progress_overview));
            if let Some(overview) = report["progress"].as_object_mut() {
                overview.insert(
                    "nextAction".into(),
                    json!(read_next_action(spec, active, progress)),
                );
            }
        }
        ReportKind::Lifecycle => {
            report["contentStatus"] = json!(spec.content.status);
            report["progress"] = json!(progress);
        }
        ReportKind::Recommendation => {
            let recommendations = recommendations(&snapshot, spec, active, progress);
            let primary = recommendations.first().expect("下一步建议");
            report["contentStatus"] = json!(spec.content.status);
            report["metadataError"] = json!(spec.content.metadata_error);
            report["blockers"] = json!(spec.content.blockers);
            report["artifactDigest"] = json!(spec.content.artifact_digest);
            let goal = progress.and_then(|value| value.goal.as_ref());
            report["goalVersion"] = json!(goal.map(|value| &value.goal_version));
            report["roundId"] = json!(progress.and_then(|value| value.round_id.as_deref()));
            report["goalSpecDigest"] = json!(goal.map(|value| &value.goal.spec_digest));
            report["target"] = json!(goal.map(|value| &value.goal.target));
            report["evidenceDigest"] = json!(progress.map(|value| &value.evidence_digest));
            report["lastVerifiedAt"] =
                json!(progress.and_then(|value| value.last_verified_at.as_ref()));
            report["observedStatus"] = json!(progress.map(|value| value.status));
            report["observedStage"] = json!(progress.map(|value| value.primary_stage));
            report["recommendedStatus"] = json!(
                progress
                    .filter(|_| primary.action == "complete"
                        || (goal_state == "active" && !primary.registration_diagnosis))
                    .map(|value| value.status)
            );
            report["recommendedStage"] = json!(primary.stage);
            report["recommendedAction"] = json!(primary.action);
            report["recommendationItems"] = json!(recommendations);
            report["autoConfirmableStages"] = json!(
                progress
                    .filter(|_| goal_state == "active")
                    .map_or(&[][..], |value| value.auto_confirmable_stages.as_slice())
            );
            report["validity"] =
                json!(progress.map_or(Validity::Unverified, |value| value.validity));
            report["attention"] =
                json!(progress.map_or(&[][..], |value| value.attention.as_slice()));
        }
    }
    Ok(report)
}

fn spec_content(spec: &SpecView) -> Value {
    let content = &spec.content;
    let questions: Vec<_> = content
        .questions
        .iter()
        .map(|view| {
            json!({
                "id": view.question.id,
                "title": view.question.title,
                "state": view.state,
                "blocking": view.question.blocking,
            })
        })
        .collect();
    json!({
        "status": content.status,
        "scope": content.scope,
        "artifactDigest": content.artifact_digest,
        "observedArtifactDigest": content.observed_artifact_digest,
        "materialReviewDigest": content.material_review_digest,
        "confirmedVersion": content.confirmed_version,
        "canConfirm": content.can_confirm,
        "metadataError": content.metadata_error,
        "questions": questions,
        "blockers": content.blockers,
        "needsReconfirmation": content.needs_reconfirmation,
        "materialChanges": content.material_changes,
    })
}

fn progress_overview(progress: &ProgressSummary) -> Value {
    json!({
        "status": progress.status,
        "primaryStage": progress.primary_stage,
        "activeStages": progress.active_stages,
        "validity": progress.validity,
        "attention": progress.attention,
        "autoConfirmableStages": progress.auto_confirmable_stages,
        "goalVersion": progress.goal_version,
        "roundId": progress.round_id,
        "goalSpecDigest": progress.goal.as_ref().map(|record| &record.goal.spec_digest),
        "target": progress.goal.as_ref().map(|record| &record.goal.target),
        "cancellation": progress.cancellation,
        "lastVerifiedAt": progress.last_verified_at,
        "blocker": progress.blocker,
        "nextAction": progress.next_action,
        "currentVersions": progress.current_versions,
    })
}

pub(super) fn goal_state(progress: Option<&ProgressSummary>) -> &'static str {
    match progress {
        None => "unverified",
        Some(progress) if progress.goal.is_none() => "missing",
        Some(progress) if progress.status == Status::Canceled => "canceled",
        Some(progress) if progress.status == Status::Done => "completed",
        Some(_) => "active",
    }
}

struct Diagnosis {
    action: &'static str,
    stage: Option<Stage>,
    reason: String,
    next_action: &'static str,
}

fn diagnosis(
    spec: &SpecView,
    active: bool,
    progress: Option<&ProgressSummary>,
) -> Option<Diagnosis> {
    let (action, stage, reason, next_action) = if !active {
        (
            "restoreRegistration",
            None,
            "Spec 或所属工程登记已停用".into(),
            "在工作台恢复登记后重新读取",
        )
    } else if spec.source_health != SourceHealth::Available {
        (
            "checkSource",
            Some(Stage::Spec),
            format!(
                "主 Spec {} 无法读取：{}",
                spec.record.source_path,
                spec.source_message.as_deref().unwrap_or("来源不可用")
            ),
            "恢复主 Spec 路径和读取权限后重新读取",
        )
    } else if let Some(error) = &spec.content.metadata_error {
        (
            "reviewContent",
            Some(Stage::Spec),
            format!("主 Spec {} 无法核对：{error}", spec.record.source_path),
            "修复主 Spec 元数据或关联材料后重新读取",
        )
    } else if spec.content.artifact_digest.is_none() {
        let unavailable = spec.resources.iter().find(|resource| {
            !matches!(
                resource.health,
                ResourceHealth::Available | ResourceHealth::External
            )
        });
        let reason = unavailable.map_or_else(
            || format!("主 Spec {} 的采用材料尚不可核对", spec.record.source_path),
            |resource| {
                format!(
                    "主 Spec {} 的关联材料 {} 无法核对：{}",
                    spec.record.source_path,
                    resource.record.target,
                    resource.message.as_deref().unwrap_or("来源不可用"),
                )
            },
        );
        (
            "reviewContent",
            Some(Stage::Spec),
            reason,
            "核对主 Spec 内容和关联材料后重新读取",
        )
    } else if progress.is_none() {
        (
            "retryRead",
            None,
            "当前生命周期待核实".into(),
            "重新读取状态后核对本轮目标",
        )
    } else if progress.is_some_and(|value| value.goal.is_none()) && spec.content.material_review() {
        (
            "reviewContent",
            Some(Stage::Spec),
            "主 Spec 关联材料的适用性待复核".into(),
            "核对待复查的材料和适用范围后重新读取",
        )
    } else if progress.is_some_and(|value| value.goal.is_none()) {
        (
            "registerGoal",
            None,
            "未设置本轮目标".into(),
            "核对本轮采用的 Spec、范围、终点和参与工程后登记目标",
        )
    } else {
        return None;
    };
    Some(Diagnosis {
        action,
        stage,
        reason,
        next_action,
    })
}

pub(super) fn read_next_action(
    spec: &SpecView,
    active: bool,
    progress: Option<&ProgressSummary>,
) -> String {
    diagnosis(spec, active, progress).map_or_else(
        || {
            progress.map_or_else(String::new, |value| {
                if value.status == Status::Canceled {
                    "查看历史。如需继续，核对范围后开启新一轮".into()
                } else {
                    value.next_action.clone()
                }
            })
        },
        |value| value.next_action.into(),
    )
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Recommendation<'a> {
    pub(super) action: &'static str,
    pub(super) stage: Option<Stage>,
    project_id: &'a str,
    project_name: Option<&'a str>,
    project_root: Option<&'a str>,
    role: ParticipantRole,
    reason: String,
    next_action: String,
    #[serde(skip)]
    registration_diagnosis: bool,
}

struct Observation<'a> {
    progress: Progress,
    validity: Validity,
    summary: &'a str,
    blocker: &'a str,
    conflicts: &'a [String],
    evidence: &'a [Evidence],
}

fn evidence_action<'a>(
    observed: Observation<'a>,
    completed: bool,
    include_unverified: bool,
) -> Option<(&'static str, String, &'a str)> {
    if !completed && (observed.progress == Progress::Blocked || !observed.blocker.is_empty()) {
        let reason = if observed.blocker.is_empty() {
            observed.summary
        } else {
            observed.blocker
        };
        return Some(("resolveBlocker", reason.into(), "解除阻塞后重新核实并上报"));
    }
    if !observed.conflicts.is_empty() {
        return Some((
            "reviewEvidence",
            observed.conflicts.join("；"),
            "核对冲突证据和当前有效的人工决定",
        ));
    }
    if let Some(evidence) = observed
        .evidence
        .iter()
        .find(|item| item.outcome == Some(EvidenceOutcome::Failed))
    {
        return Some((
            "reviewEvidence",
            format!("检查未通过：{}（{}）", evidence.label, evidence.reference),
            "复核失败原因并重新取得当前范围的检查结果",
        ));
    }
    let (action, reason, next_action) = match observed.validity {
        Validity::NeedsReview => (
            "reviewEvidence",
            "现有证据与本轮采用的材料或工程版本需要复核",
            "核对当前版本、适用范围和原凭证",
        ),
        Validity::NeedsSync => (
            "syncEvidence",
            "工程版本或输入已变化，现有证据需要同步",
            "在现用工程版本核实并补报本轮证据",
        ),
        Validity::Unverified if include_unverified => (
            "collectEvidence",
            "当前目标所需证据待核实",
            "按当前版本、阶段范围和验收引用核实并补报",
        ),
        _ => return None,
    };
    Some((
        action,
        format!("{reason}。{}", observed.summary),
        next_action,
    ))
}

pub(super) fn recommendations<'a>(
    snapshot: &'a Snapshot,
    spec: &'a SpecView,
    active: bool,
    progress: Option<&'a ProgressSummary>,
) -> Vec<Recommendation<'a>> {
    let item = |project_id: &'a str,
                role,
                stage,
                (action, reason, next_action): (&'static str, String, &str)| {
        let project = snapshot
            .projects
            .iter()
            .find(|project| project.record.id == project_id);
        Recommendation {
            action,
            stage,
            project_id,
            project_name: project.map(|value| value.record.name.as_str()),
            project_root: project.map(|value| value.record.root_path.as_str()),
            role,
            reason,
            next_action: next_action.into(),
            registration_diagnosis: false,
        }
    };
    let main_project = spec.record.project_id.as_str();
    if let Some(diagnosis) = diagnosis(spec, active, progress) {
        let mut first = item(
            main_project,
            ParticipantRole::Spec,
            diagnosis.stage,
            (diagnosis.action, diagnosis.reason, diagnosis.next_action),
        );
        first.registration_diagnosis = true;
        return vec![first];
    }
    let progress = progress.expect("已核实本轮目标");
    let goal = progress.goal.as_ref().expect("已核实本轮目标");
    if progress.status == Status::Canceled {
        let reason = progress.cancellation.as_ref().map_or_else(
            || "本轮目标已取消".into(),
            |record| format!("本轮目标已取消：{}", record.reason),
        );
        return vec![item(
            main_project,
            ParticipantRole::Spec,
            None,
            (
                "reopenGoal",
                reason,
                "查看本轮历史。如需继续，核对范围后开启新一轮",
            ),
        )];
    }
    let completed = progress.status == Status::Done;
    let mut items = Vec::new();
    if progress.material_review
        || completed
            && spec.content.artifact_digest.as_deref() != Some(goal.goal.spec_digest.as_str())
    {
        let reason = if progress.material_review {
            "主 Spec 的共用材料影响待复核"
        } else {
            "当前主 Spec 材料已变化，可以回查本轮验收采用的版本"
        };
        items.push(item(
            main_project,
            ParticipantRole::Spec,
            Some(Stage::Spec),
            (
                "reviewEvidence",
                reason.into(),
                "核对主 Spec 和关联材料变化涉及的规则、范围和验收",
            ),
        ));
    }
    for stage in progress.stages.iter().filter(|stage| {
        stage.applicable || stage.stage == Stage::Delivery && !stage.evidence.is_empty()
    }) {
        let include_unverified = completed || stage.stage == progress.primary_stage;
        let participant_start = items.len();
        for participant in &stage.participants {
            let observed = Observation {
                progress: participant.progress,
                validity: participant.validity,
                summary: &participant.summary,
                blocker: &participant.blocker,
                conflicts: &participant.conflicts,
                evidence: &participant.evidence,
            };
            if let Some(mut advice) = evidence_action(observed, completed, include_unverified) {
                if advice.0 == "resolveBlocker" && !participant.next_action.is_empty() {
                    advice.2 = &participant.next_action;
                }
                items.push(item(
                    &participant.project_id,
                    participant.role,
                    Some(stage.stage),
                    advice,
                ));
            }
        }
        let observed = Observation {
            progress: stage.progress,
            validity: stage.validity,
            summary: &stage.summary,
            blocker: &stage.blocker,
            conflicts: &stage.conflicts,
            evidence: &stage.evidence,
        };
        let rejected = stage
            .confirmation
            .as_ref()
            .is_some_and(|value| value.confirmation.decision == ConfirmationDecision::Reject);
        if let Some(mut advice) = evidence_action(observed, completed, include_unverified)
            && (rejected
                || !items[participant_start..]
                    .iter()
                    .any(|value| value.action == advice.0))
            && !items.iter().any(|value| {
                value.action == advice.0
                    && value.project_id == main_project
                    && value.stage == Some(stage.stage)
                    && value.role == ParticipantRole::Spec
            })
        {
            if rejected {
                advice.2 = "核对退回意见，处理后提交本轮证据供用户重新决定";
            } else if advice.0 == "resolveBlocker" && !stage.next_action.is_empty() {
                advice.2 = &stage.next_action;
            } else if advice.0 == "collectEvidence" && stage.stage == Stage::Delivery {
                let target = &goal.goal.target;
                if target.kind != DeliveryTargetKind::Local {
                    let reference = target.reference.as_deref().unwrap_or(&target.label);
                    advice.1 = format!(
                        "本轮终点「{}」缺少可核实的目标凭证（{reference}）",
                        target.label
                    );
                    advice.2 = match target.kind {
                        DeliveryTargetKind::Merged => "核对本轮候选提交及实际合并凭证后补报",
                        DeliveryTargetKind::Environment => {
                            "核对本轮候选版本与目标环境的实际验证结果后补报"
                        }
                        DeliveryTargetKind::Local => advice.2,
                    };
                }
            }
            items.push(item(
                main_project,
                ParticipantRole::Spec,
                Some(stage.stage),
                advice,
            ));
        }
    }
    items.sort_by_key(|value| match value.action {
        "resolveBlocker" => 0,
        "reviewEvidence" => 1,
        "syncEvidence" => 2,
        _ => 3,
    });
    if !items.is_empty() {
        return items;
    }
    if completed {
        return vec![item(
            main_project,
            ParticipantRole::Spec,
            Some(Stage::Delivery),
            ("complete", "当前目标已完成".into(), ""),
        )];
    }
    let Some(stage) = progress
        .stages
        .iter()
        .find(|stage| stage.stage == progress.primary_stage)
    else {
        return vec![item(
            main_project,
            ParticipantRole::Spec,
            None,
            (
                "collectEvidence",
                "当前适用阶段待核实".into(),
                "核对目标范围与参与工程后补齐证据",
            ),
        )];
    };
    let (action, next_action) = if progress.auto_confirmable_stages.contains(&stage.stage) {
        ("autoConfirm", "按当前证据自动确认")
    } else if stage.confirmation_required
        && stage.confirmation.is_none()
        && matches!(stage.progress, Progress::Done | Progress::Skipped)
    {
        (
            "collectEvidence",
            "核对阶段范围和验收引用后补齐确认所需证据",
        )
    } else if !stage.next_action.is_empty() {
        ("continue", stage.next_action.as_str())
    } else {
        ("continue", "按本轮目标继续当前阶段并上报进展")
    };
    vec![item(
        main_project,
        ParticipantRole::Spec,
        Some(stage.stage),
        (action, stage.summary.clone(), next_action),
    )]
}
