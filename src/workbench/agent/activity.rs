use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize, de::IgnoredAny};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};

use super::{acceptance, terminal};
use crate::{
    error::Result,
    workbench::{model::Stage, store::Store},
};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RunActivity {
    pub run_id: String,
    pub job_id: String,
    pub stage: Stage,
    pub goal_version: String,
    pub spec_digest: String,
    pub status: String,
    pub project_ids: Vec<String>,
    pub read_only: Option<bool>,
    pub result_available: bool,
    pub acceptance: Vec<ProjectActivity>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProjectActivity {
    pub project_id: String,
    pub status: AcceptanceStatus,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum AcceptanceStatus {
    Writing,
    Accepted,
    NoWrite,
    Failed,
    NeedsVerification,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct StoredRun {
    id: String,
    spec_id: String,
    job_id: String,
    #[serde(default)]
    binding_revision: u64,
    project_ids: Vec<String>,
    lifecycle_stage: Option<Stage>,
    goal_version: Option<String>,
    spec_digest: Option<String>,
    read_only: Option<bool>,
    workspaces: Vec<StoredWorkspace>,
    applied_files: Vec<AppliedFile>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct StoredWorkspace {
    result_snapshot: Option<IgnoredAny>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct AppliedFile {
    job_id: Option<String>,
    project_id: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct StoredJob {
    status: String,
    created_at: String,
}

pub fn read_activities(
    store: &mut Store,
    spec_id: Option<&str>,
) -> Result<BTreeMap<String, Vec<RunActivity>>> {
    let mut grouped = BTreeMap::<String, Vec<(Option<OffsetDateTime>, RunActivity)>>::new();
    for run in store.records::<StoredRun>("agentRun")? {
        if spec_id.is_some_and(|id| id != run.spec_id) {
            continue;
        }
        let (Some(stage), Some(goal_version), Some(spec_digest)) =
            (run.lifecycle_stage, run.goal_version, run.spec_digest)
        else {
            continue;
        };
        let Some(job) = store.record::<StoredJob>("job", &run.job_id)? else {
            continue;
        };
        let read_only = match run.read_only {
            Some(value) => Some(value),
            None => store
                .run_event(&run.job_id)?
                .and_then(|event| event.get("readOnly").and_then(serde_json::Value::as_bool)),
        };
        let accepted_projects: BTreeSet<_> = run
            .applied_files
            .iter()
            .filter(|file| file.job_id.as_deref() == Some(run.job_id.as_str()))
            .filter_map(|file| file.project_id.as_deref())
            .collect();
        let acceptance = acceptance::round_activity(
            store,
            &run.id,
            &run.job_id,
            run.binding_revision,
            &accepted_projects,
        )?;
        let result_available = terminal(&job.status)
            && !run.workspaces.is_empty()
            && run.workspaces.iter().all(|w| w.result_snapshot.is_some());
        grouped.entry(run.spec_id).or_default().push((
            OffsetDateTime::parse(&job.created_at, &Rfc3339).ok(),
            RunActivity {
                run_id: run.id,
                job_id: run.job_id,
                stage,
                goal_version,
                spec_digest,
                status: job.status,
                project_ids: run.project_ids,
                read_only,
                result_available,
                acceptance,
            },
        ));
    }
    Ok(grouped
        .into_iter()
        .map(|(id, mut entries)| {
            entries.sort_unstable_by(|a, b| (&b.0, &b.1.job_id).cmp(&(&a.0, &a.1.job_id)));
            (
                id,
                entries.into_iter().map(|(_, activity)| activity).collect(),
            )
        })
        .collect())
}
