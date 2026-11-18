//! Connect explicit run bindings to lifecycle activity without inferring completion.
use super::{
    agent::AgentRun,
    jobs::{JobContext, lock_store},
    model::{Action, MutationRequest, Stage},
    progress::{
        self, Evidence, EvidenceKind, EvidenceOutcome, ParticipantRole, Progress,
        ProgressReportInput, Validity,
    },
    service,
    store::Store,
};
use crate::{cancel::Cancellation, error::Result, model::Sha256};
use serde::Serialize;
use serde_json::json;

#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum Activity {
    Started,
    Completed,
    Interrupted,
    Accepted,
}

pub fn bind_run(store: &mut Store, run: &mut AgentRun, requested: Option<Stage>) -> Result<()> {
    let records = progress::load(store)?;
    let Some(goal) = records
        .goals
        .iter()
        .filter(|g| g.goal.spec_id == run.spec_id)
        .max_by(|a, b| (&a.created_at, &a.goal_version).cmp(&(&b.created_at, &b.goal_version)))
    else {
        return Ok(());
    };
    let snapshot = service::snapshot_from_store(store, &Cancellation::new())?;
    let candidate = requested
        .or_else(|| {
            if run.goal_version.as_deref() == Some(goal.goal_version.as_str()) {
                run.lifecycle_stage
            } else {
                None
            }
        })
        .or_else(|| snapshot.progress.get(&run.spec_id).map(|s| s.primary_stage));
    let Some(stage) = candidate.filter(|stage| {
        goal.goal
            .stages
            .iter()
            .any(|s| s.stage == *stage && s.applicable)
    }) else {
        run.lifecycle_stage = None;
        run.goal_version = None;
        run.spec_digest = None;
        return Ok(());
    };
    run.lifecycle_stage = Some(stage);
    run.goal_version = Some(goal.goal_version.clone());
    run.spec_digest = Some(goal.goal.spec_digest.clone());
    Ok(())
}

/// Progress failure is recorded as telemetry and does not interrupt authorized work.
pub fn note(context: &JobContext, run: &AgentRun, activity: Activity) {
    let result = lock_store(&context.store).and_then(|mut store| record(&mut store, run, activity));
    if let Err(error) = result {
        let _ = context.emit(
            "progressError",
            json!({"runId":run.id,"activity":activity,"error":error}),
        );
    }
}

pub fn record(store: &mut Store, run: &AgentRun, activity: Activity) -> Result<()> {
    let (Some(stage), Some(goal_version), Some(spec_digest)) = (
        run.lifecycle_stage,
        run.goal_version.as_ref(),
        run.spec_digest.as_ref(),
    ) else {
        return Ok(());
    };
    let records = progress::load(store)?;
    let Some(goal) = records
        .goals
        .iter()
        .find(|g| &g.goal_version == goal_version && g.goal.spec_id == run.spec_id)
    else {
        return Ok(());
    };
    if !goal
        .goal
        .stages
        .iter()
        .any(|s| s.stage == stage && s.applicable)
    {
        return Ok(());
    }
    let targets: Vec<_> = if matches!(
        stage,
        Stage::Research | Stage::Analysis | Stage::Spec | Stage::Prototype | Stage::Delivery
    ) {
        vec![(None, ParticipantRole::Spec)]
    } else {
        let participants: Vec<_> = goal
            .goal
            .participants
            .iter()
            .filter(|p| match stage {
                Stage::Handoff | Stage::Implementation => {
                    matches!(p.role, ParticipantRole::Backend | ParticipantRole::Frontend)
                }
                Stage::Verification => p.role != ParticipantRole::Spec,
                _ => false,
            })
            .collect();
        if participants.is_empty() {
            vec![(None, ParticipantRole::Spec)]
        } else {
            participants
                .into_iter()
                .filter(|p| run.project_ids.contains(&p.project_id))
                .map(|p| (Some(p.project_id.clone()), p.role))
                .collect()
        }
    };
    for (project_id, role) in targets {
        let original_spec_project = store
            .state()
            .specs
            .iter()
            .find(|s| s.id == run.spec_id)
            .map(|s| s.project_id.as_str());
        let newer_result = records.reports.iter().any(|r| {
            r.report.spec_id == run.spec_id
                && r.report.goal_version == *goal_version
                && r.report.stage == stage
                && (r.report.project_id == project_id
                    || role == ParticipantRole::Spec
                        && r.report.project_id.as_deref() == original_spec_project)
                && r.report.role == role
                && r.recorded_at >= run.created_at
                && !r.report.id.starts_with("activity:")
                && !r.report.id.starts_with("remote:")
        });
        if newer_result {
            continue;
        }
        let (progress, validity, summary, blocker, next_action) = match activity {
            Activity::Started => (
                Progress::Doing,
                Validity::Current,
                "原 Agent 正在继续当前阶段的授权工作",
                "",
                "原 Agent 完成当前范围的产物与适用检查后上报证据",
            ),
            Activity::Completed => (
                Progress::Doing,
                Validity::Unverified,
                "Agent 本轮已结束，实际成果与完成范围待核实",
                "",
                "审阅隔离成果并接收所需文件，再由原 Agent 核对原工程版本和适用检查",
            ),
            Activity::Interrupted => (
                Progress::Blocked,
                Validity::Current,
                "Agent 运行已中断，当前阶段等待续接",
                "原 Agent 本轮未完成，运行事实和隔离成果已保留",
                "沿原运行继续，核对阻塞与已有成果后上报",
            ),
            Activity::Accepted => (
                Progress::Doing,
                Validity::Unverified,
                "已接收所选隔离成果，当前目标完成范围待核实",
                "",
                "由原 Agent 核对接收后原工程的规格、代码版本与适用检查",
            ),
        };
        let identity = serde_json::to_vec(&(
            &run.id,
            &run.job_id,
            activity,
            stage,
            &project_id,
            role,
            &run.applied_files,
        ))
        .map_err(|_| super::sources::invalid("运行进度身份编码失败"))?;
        let digest = Sha256::of(&identity).as_str().to_owned();
        let input = ProgressReportInput {
            id: format!("activity:{digest}"),
            spec_id: run.spec_id.clone(),
            goal_version: goal_version.clone(),
            spec_digest: spec_digest.clone(),
            stage,
            project_id,
            role,
            progress,
            validity,
            summary: summary.into(),
            blocker: blocker.into(),
            next_action: next_action.into(),
            scope_refs: vec![goal.goal.scope.clone()],
            versions: Vec::new(),
            evidence: vec![Evidence {
                kind: EvidenceKind::Other,
                label: summary.into(),
                reference: format!("agent-run:{}", run.id),
                outcome: Some(EvidenceOutcome::Observed),
            }],
        };
        let expected_revision = store.state().revision;
        service::mutate(
            store,
            MutationRequest {
                expected_revision,
                expected_target: None,
                request_id: Some(format!("activity-{digest}")),
                action: Action::ReportProgress { input },
            },
        )?;
    }
    Ok(())
}

pub fn run_context(store: &mut Store, run: &AgentRun) -> Result<String> {
    let mut instructions = String::from(
        "\n工作台会记录作业开始、结束与成果接收。请在最终答复中列出本次实际成果、检查结果和阻塞，留待原工程接收后核实。\n",
    );
    let (Some(stage), Some(version)) = (run.lifecycle_stage, run.goal_version.as_ref()) else {
        return Ok(instructions);
    };
    let records = progress::load(store)?;
    if let Some(goal) = records
        .goals
        .iter()
        .find(|goal| goal.goal.spec_id == run.spec_id && &goal.goal_version == version)
    {
        let binding = json!({
            "specId": run.spec_id,
            "stage": stage,
            "goalVersion": version,
            "specDigest": run.spec_digest,
            "scope": goal.goal.scope,
            "target": goal.goal.target,
            "acceptanceRefs": goal.goal.acceptance_refs,
            "participatingProjectIds": run.project_ids,
        });
        instructions.push_str(&format!(
            "\n本次运行绑定的生命周期目标\n{}\n",
            serde_json::to_string_pretty(&binding)
                .map_err(|_| super::sources::invalid("运行目标上下文编码失败"))?
        ));
    }
    Ok(instructions)
}
