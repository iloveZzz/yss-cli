//! Bind each Agent round to its current lifecycle target and preserve that basis.
use super::{
    agent::AgentRun,
    model::{Snapshot, Stage, Status},
    progress::{GoalRecord, ProgressSummary},
    service,
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
};
use serde::{Deserialize, Serialize};
use serde_json::json;

pub use super::agent::activity::{RunActivity, read_activities};

pub(super) struct RoundBinding {
    stage: Stage,
    goal: GoalRecord,
}

impl RoundBinding {
    pub(super) fn goal_version(&self) -> &str {
        &self.goal.goal_version
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct GoalExpectation {
    pub(super) version: Option<String>,
    pub(super) canceled: bool,
}

impl GoalExpectation {
    pub(super) fn from_summary(summary: &ProgressSummary) -> Self {
        Self {
            version: summary.goal_version.clone(),
            canceled: summary.status == Status::Canceled,
        }
    }

    fn verify(&self, version: Option<&str>, canceled: bool) -> Result<()> {
        if self.version.as_deref() != version {
            return Err(stale_binding("本轮目标版本已变化，请核对后发送"));
        }
        if self.canceled != canceled {
            return Err(stale_binding("本轮目标已取消，请核对后发送"));
        }
        Ok(())
    }
}

fn stale_binding(message: &str) -> YssError {
    YssError::new(ErrorCode::ConcurrentChange, message)
        .context("httpStatus", 409)
        .context("progressBinding", true)
}

pub(super) fn select_binding(
    store: &mut Store,
    spec_id: &str,
    requested: Option<Stage>,
    expected_version: Option<&str>,
    expectation: Option<&GoalExpectation>,
    cancel: &Cancellation,
) -> Result<Option<RoundBinding>> {
    let snapshot = service::snapshot_for_spec(store, spec_id, cancel)?;
    select_snapshot_binding(&snapshot, spec_id, requested, expected_version, expectation)
}

pub(super) fn select_snapshot_binding(
    snapshot: &Snapshot,
    spec_id: &str,
    requested: Option<Stage>,
    expected_version: Option<&str>,
    expectation: Option<&GoalExpectation>,
) -> Result<Option<RoundBinding>> {
    let Some(summary) = snapshot.progress.get(spec_id) else {
        return Err(super::sources::missing("Spec 不存在"));
    };
    if let Some(expectation) = expectation {
        expectation.verify(
            summary.goal_version.as_deref(),
            summary.status == Status::Canceled,
        )?;
    }
    if let Some(expected) = expected_version
        && summary.goal_version.as_deref() != Some(expected)
    {
        return Err(stale_binding("本轮目标版本已变化，请核对阶段选择后发送"));
    }
    let Some(goal) = &summary.goal else {
        if requested.is_some() {
            return Err(stale_binding(
                "当前没有可绑定的生命周期目标，请核对阶段选择",
            ));
        }
        return Ok(None);
    };
    if summary.status == Status::Canceled {
        if requested.is_some() {
            return Err(stale_binding("本轮目标已取消，请核对目标后继续"));
        }
        return Ok(None);
    }
    let current_digest = snapshot
        .specs
        .iter()
        .find(|spec| spec.record.id == spec_id)
        .and_then(|spec| spec.content.artifact_digest.as_deref());
    if current_digest != Some(&goal.goal.spec_digest) {
        if requested.is_some() {
            return Err(stale_binding("当前规格材料已变化，请核对目标版本和阶段"));
        }
        return Ok(None);
    }
    if requested.is_none() && summary.status == Status::Done {
        return Ok(None);
    }
    let stage = requested.unwrap_or(summary.primary_stage);
    if !goal
        .goal
        .stages
        .iter()
        .any(|scope| scope.stage == stage && scope.applicable)
    {
        if requested.is_some() {
            return Err(stale_binding("所选阶段未纳入当前目标，请重新选择"));
        }
        return Ok(None);
    }
    Ok(Some(RoundBinding {
        stage,
        goal: goal.clone(),
    }))
}

pub(super) fn apply_binding(run: &mut AgentRun, binding: Option<&RoundBinding>) {
    run.lifecycle_stage = binding.map(|binding| binding.stage);
    run.goal_version = binding.map(|binding| binding.goal.goal_version.clone());
    run.spec_digest = binding.map(|binding| binding.goal.goal.spec_digest.clone());
}

pub(super) fn revalidate_material(
    binding: Option<&RoundBinding>,
    digest: Option<&str>,
) -> Result<()> {
    if binding.is_some_and(|bound| digest != Some(bound.goal.goal.spec_digest.as_str())) {
        return Err(stale_binding("准备期间的规格材料版本已变化，请重新核对"));
    }
    Ok(())
}

pub(super) fn revalidate_goal(
    store: &mut Store,
    spec_id: &str,
    version: Option<&str>,
    require_exact: bool,
    expectation: Option<&GoalExpectation>,
) -> Result<()> {
    if let Some(expectation) = expectation {
        let current_version = super::progress::load_goal_version(store, spec_id)?;
        let canceled = if let Some(version) = current_version.as_deref() {
            super::progress::load_goal_canceled(store, spec_id, version)?
        } else {
            false
        };
        return expectation.verify(current_version.as_deref(), canceled);
    }
    if (version.is_some() || require_exact)
        && super::progress::load_goal_version(store, spec_id)?.as_deref() != version
    {
        return Err(stale_binding("准备期间的生命周期目标已变化，请重新核对"));
    }
    if let Some(version) = version
        && super::progress::load_goal_canceled(store, spec_id, version)?
    {
        return Err(stale_binding("准备期间的生命周期目标已取消，请重新核对"));
    }
    Ok(())
}

pub(super) fn run_context(run: &AgentRun, binding: Option<&RoundBinding>) -> String {
    let mut instructions = String::from(
        "\n工作台会记录本轮作业状态与实际接收情况。请在最终答复中列出本次实际成果、检查结果和阻塞，留待原工程接收后核实。\n",
    );
    if let Some(binding) = binding {
        let goal = &binding.goal;
        let context = json!({
            "specId": run.spec_id,
            "stage": binding.stage,
            "goalVersion": goal.goal_version,
            "specDigest": goal.goal.spec_digest,
            "scope": goal.goal.scope,
            "target": goal.goal.target,
            "acceptanceRefs": goal.goal.acceptance_refs,
            "participatingProjectIds": run.project_ids,
        });
        instructions.push_str(&format!("\n本次运行绑定的生命周期目标\n{context:#}\n"));
    }
    instructions
}
