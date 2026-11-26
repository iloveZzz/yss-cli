use super::{
    agent::AgentRun,
    jobs::{JobRecord, lock_store},
    sources,
    store::Store,
};
use crate::error::Result;
use serde::{Deserialize, Serialize};
use std::sync::{Arc, Mutex};

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct ContinuationSource {
    pub run_id: String,
    pub job_id: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct PriorWorkspace {
    project_ids: Vec<String>,
    branch: String,
    head: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Continuation {
    run_id: String,
    job_id: String,
    prompt: String,
    project_ids: Vec<String>,
    workspaces: Vec<PriorWorkspace>,
}

fn load(store: &mut Store, spec_id: &str, source: &ContinuationSource) -> Result<Continuation> {
    sources::spec(store.state(), spec_id)?;
    let job = store
        .record::<JobRecord>("job", &source.job_id)?
        .filter(|job| {
            job.kind == "agent"
                && job
                    .agent_run_id
                    .as_deref()
                    .is_none_or(|id| id == source.run_id)
                && job.agent_spec_id.as_deref().is_none_or(|id| id == spec_id)
                && job.terminal()
        })
        .ok_or_else(|| sources::invalid("续办来源须为当前 Spec 已结束的运行轮次"))?;
    let latest = store
        .record::<AgentRun>("agentRun", &source.run_id)?
        .filter(|run| run.id == source.run_id && run.spec_id == spec_id)
        .ok_or_else(|| sources::invalid("来源会话已移除"))?;
    let archived_round = if latest.job_id == job.id {
        None
    } else {
        store
            .record::<AgentRun>("agentRound", &job.id)?
            .filter(|run| run.id == source.run_id && run.spec_id == spec_id && run.job_id == job.id)
    };
    let round = if latest.job_id == job.id {
        Some(&latest)
    } else {
        archived_round.as_ref()
    };
    let event = store
        .run_event(&job.id)?
        .filter(|event| event["runId"].as_str() == Some(source.run_id.as_str()));
    // Older rounds may not have the job-level association.
    if round.is_none()
        && event.is_none()
        && (job.agent_run_id.is_none() || job.agent_spec_id.is_none())
    {
        return Err(sources::invalid("无法核对该轮次与来源会话的归属"));
    }
    let prompt = event
        .and_then(|event| event["prompt"].as_str().map(str::to_owned))
        .or_else(|| round.map(|run| run.prompt.clone()))
        .unwrap_or_default();
    let active = |id: &String| {
        store
            .state()
            .projects
            .iter()
            .any(|project| &project.id == id && project.active)
    };
    // A preparation failure can leave an event without a frozen round. Ordinary
    // continuation preserves the session's project IDs.
    let project_ids = round
        .unwrap_or(&latest)
        .project_ids
        .iter()
        .filter(|id| active(id))
        .cloned()
        .collect();
    let workspaces = round
        .filter(|run| !run.preparation_failed)
        .into_iter()
        .flat_map(|run| &run.workspaces)
        .map(|workspace| PriorWorkspace {
            project_ids: workspace.project_ids.clone(),
            branch: workspace.branch.clone(),
            head: workspace.head.clone(),
        })
        .collect();
    Ok(Continuation {
        run_id: source.run_id.clone(),
        job_id: job.id,
        prompt,
        project_ids,
        workspaces,
    })
}

pub(super) fn preview(
    store: &Arc<Mutex<Store>>,
    spec_id: &str,
    source: &ContinuationSource,
) -> Result<Continuation> {
    load(&mut *lock_store(store)?, spec_id, source)
}

pub(super) fn continuation(
    store: &mut Store,
    spec_id: &str,
    source: &ContinuationSource,
) -> Result<String> {
    let prior = load(store, spec_id, source)?;
    let metadata = serde_json::json!({
        "runId": prior.run_id,
        "jobId": prior.job_id
    });
    Ok(format!(
        "\n本轮从同一 Spec 的旧轮次创建。原轮次标识供追溯，本轮任务和权限以本次提交为准。\n{metadata}\n"
    ))
}
