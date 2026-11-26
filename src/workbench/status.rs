use super::{
    model::{SourceHealth, SpecView, Status},
    progress::{Progress, ProgressSummary, Validity},
    service,
    sources::missing,
};
use crate::{cancel::Cancellation, error::Result};
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
        .ok_or_else(|| missing("Spec 不存在"))?;
    let project = snapshot
        .projects
        .iter()
        .find(|project| project.record.id == spec.record.project_id)
        .ok_or_else(|| missing("工程不存在"))?;
    let active = project.record.active && spec.record.active;
    let mut report = json!({
        "revision": snapshot.revision,
        "readAt": snapshot.read_at,
        "specId": spec_id,
        "projectId": spec.record.project_id,
        "projectRoot": project.record.root_path,
        "sourcePath": spec.record.source_path,
        "title": spec.record.title,
        "active": active,
        "sourceHealth": spec.source_health,
        "sourceMessage": spec.source_message,
        "sourceModifiedAt": spec.source_modified_at,
    });
    let progress = spec.progress.as_ref();
    match kind {
        ReportKind::Spec => {
            report["content"] = spec_content(spec);
            report["progress"] = json!(progress.map(progress_overview));
        }
        ReportKind::Lifecycle => {
            report["contentStatus"] = json!(spec.content.status);
            report["progress"] = json!(progress);
        }
        ReportKind::Recommendation => {
            let progress = progress.ok_or_else(|| missing("当前生命周期汇总不可用"))?;
            let (action, recommend) = recommendation(spec, active, progress);
            report["contentStatus"] = json!(spec.content.status);
            report["metadataError"] = json!(spec.content.metadata_error);
            report["blockers"] = json!(spec.content.blockers);
            report["artifactDigest"] = json!(spec.content.artifact_digest);
            report["goalVersion"] = json!(progress.goal_version);
            report["goalSpecDigest"] =
                json!(progress.goal.as_ref().map(|goal| &goal.goal.spec_digest));
            report["evidenceDigest"] = json!(progress.evidence_digest);
            report["observedStatus"] = json!(progress.status);
            report["recommendedStatus"] = json!(recommend.then_some(progress.status));
            report["recommendedStage"] = json!(recommend.then_some(progress.primary_stage));
            report["recommendedAction"] = json!(action);
            report["autoConfirmableStages"] = json!(progress.auto_confirmable_stages);
            report["validity"] = json!(progress.validity);
            report["attention"] = json!(progress.attention);
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
        "goalSpecDigest": progress.goal.as_ref().map(|record| &record.goal.spec_digest),
        "lastVerifiedAt": progress.last_verified_at,
        "blocker": progress.blocker,
        "nextAction": progress.next_action,
        "currentVersions": progress.current_versions,
    })
}

fn recommendation(
    spec: &SpecView,
    active: bool,
    progress: &ProgressSummary,
) -> (&'static str, bool) {
    if !active {
        return ("restoreRegistration", false);
    }
    if spec.source_health != SourceHealth::Available {
        return ("checkSource", false);
    }
    if spec.content.metadata_error.is_some() || spec.content.artifact_digest.is_none() {
        return ("reviewContent", false);
    }
    let Some(goal) = &progress.goal else {
        return ("registerGoal", false);
    };
    if progress.status == Status::Done {
        // The workbench preserves historical delivery even if the current evidence needs review.
        let needs = |condition| progress.attention.iter().any(|item| item == condition);
        let action = match progress.validity {
            _ if progress.material_review || needs("conflict") => "reviewEvidence",
            Validity::NeedsReview => "reviewEvidence",
            Validity::NeedsSync => "syncEvidence",
            _ if needs("needsSync") => "syncEvidence",
            Validity::Unverified
                if spec.content.artifact_digest.as_deref()
                    != Some(goal.goal.spec_digest.as_str()) =>
            {
                "reviewEvidence"
            }
            Validity::Unverified => "collectEvidence",
            Validity::Current => return ("complete", true),
        };
        return (action, false);
    }
    let Some(stage) = progress
        .stages
        .iter()
        .find(|stage| stage.stage == progress.primary_stage)
    else {
        return ("collectEvidence", false);
    };
    let action = match stage.validity {
        _ if progress.material_review || !stage.conflicts.is_empty() => "reviewEvidence",
        Validity::NeedsReview => "reviewEvidence",
        Validity::NeedsSync => "syncEvidence",
        _ if stage.progress == Progress::Blocked || !stage.blocker.is_empty() => "resolveBlocker",
        Validity::Unverified => "collectEvidence",
        Validity::Current
            if stage.confirmation_required
                && stage.confirmation.is_none()
                && matches!(stage.progress, Progress::Done | Progress::Skipped) =>
        {
            if progress.auto_confirmable_stages.contains(&stage.stage) {
                "autoConfirm"
            } else {
                "collectEvidence"
            }
        }
        Validity::Current => "continue",
    };
    (action, true)
}
