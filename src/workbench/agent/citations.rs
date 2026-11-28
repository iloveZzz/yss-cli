use super::{AgentHost, agent_protocol, load_round, lock_store};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    model::{RelPath, Sha256},
    workbench::{jobs::JobRecord, sources, store::Store},
};
use serde::Deserialize;
use serde_json::{Value, json};
use std::{
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};

const MAX_LINES: usize = 20;
const MAX_SNIPPET_BYTES: usize = 4 * 1024;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct FileCitation {
    project_id: String,
    path: RelPath,
    digest: Sha256,
    start_line: usize,
    end_line: usize,
    snippet: String,
}

fn missing() -> YssError {
    super::invalid("来源引用不存在").context("httpStatus", 404)
}

fn unselected_projects(store: &Store, selected: &[String]) -> Vec<PathBuf> {
    store
        .state()
        .projects
        .iter()
        .filter(|project| !selected.contains(&project.id))
        .map(|project| PathBuf::from(&project.root_path))
        .collect()
}

pub(in crate::workbench) fn current(
    shared: &Arc<Mutex<Store>>,
    job_id: &str,
    event_id: u64,
    number: u64,
    cancel: &Cancellation,
) -> Result<Value> {
    if event_id == 0 || !(1..=12).contains(&number) {
        return Err(missing());
    }
    cancel.check()?;
    let unavailable = || json!({"status": "unavailable"});
    let (citation, workspace, data_dir, unselected) = {
        let mut store = lock_store(shared)?;
        let job = store
            .record::<JobRecord>("job", job_id)?
            .filter(|job| job.kind == "agent" && job.agent_host == Some(AgentHost::Pi))
            .ok_or_else(missing)?;
        let run_id = job.agent_run_id.as_deref().ok_or_else(missing)?;
        // The displayed citation is a completed-message event written by the host, not a path
        // or a range supplied by the browser.
        let event = store
            .events_through(job_id, event_id - 1, event_id)?
            .into_iter()
            .next()
            .filter(|event| event.kind == "item/completed")
            .ok_or_else(missing)?;
        let item = &event.payload["item"];
        let citations = item["citations"]
            .as_array()
            .filter(|_| item["type"] == "agentMessage")
            .ok_or_else(missing)?;
        let value = citations
            .iter()
            .find(|entry| entry["number"].as_u64() == Some(number) && entry["kind"] == "file")
            .ok_or_else(missing)?;
        let citation: FileCitation =
            serde_json::from_value(value.clone()).map_err(|_| missing())?;
        if citation.start_line == 0
            || citation
                .end_line
                .checked_sub(citation.start_line)
                .is_none_or(|distance| distance >= MAX_LINES)
            || citation.snippet.len() > MAX_SNIPPET_BYTES
        {
            return Err(missing());
        }
        let Ok((run, _)) = load_round(&mut store, run_id, Some(job_id)) else {
            return Ok(unavailable());
        };
        if run.host != AgentHost::Pi
            || job.agent_spec_id.as_deref() != Some(run.spec_id.as_str())
            || !run.project_ids.contains(&citation.project_id)
            || run.preparation_failed
        {
            return Ok(unavailable());
        }
        let Some(workspace) = run.workspaces.into_iter().find(|workspace| {
            workspace.project_ids.contains(&citation.project_id)
                && workspace
                    .project_prefixes
                    .contains_key(&citation.project_id)
        }) else {
            return Ok(unavailable());
        };
        if !registration_matches(&store, &workspace, &citation.project_id) {
            return Ok(unavailable());
        }
        let unselected = unselected_projects(&store, &workspace.project_ids);
        (citation, workspace, store.data_dir().to_owned(), unselected)
    };

    cancel.check()?;
    let current = agent_protocol::read_citation(
        &workspace,
        &citation.project_id,
        &citation.path,
        citation.start_line..=citation.end_line,
        &data_dir,
        &unselected,
    );
    cancel.check()?;
    let (digest, snippet) = match current {
        Ok(value) => value,
        Err(error) if error.code == ErrorCode::Cancelled => return Err(error),
        Err(_) => return Ok(unavailable()),
    };
    {
        let store = lock_store(shared)?;
        if !registration_matches(&store, &workspace, &citation.project_id)
            || unselected_projects(&store, &workspace.project_ids) != unselected
        {
            return Ok(unavailable());
        }
    }
    cancel.check()?;
    let Some(snippet) = snippet.filter(|text| {
        text.len() <= MAX_SNIPPET_BYTES && (digest != citation.digest || text == &citation.snippet)
    }) else {
        return Ok(json!({"status": "unlocated", "digest": digest}));
    };
    Ok(json!({
        "status": "current", "digest": digest, "startLine": citation.start_line,
        "endLine": citation.end_line, "snippet": snippet,
    }))
}

fn registration_matches(store: &Store, workspace: &super::Workspace, project_id: &str) -> bool {
    let Some(prefix) = workspace.project_prefixes.get(project_id) else {
        return false;
    };
    if !prefix.is_empty() && RelPath::new(prefix).is_err() {
        return false;
    }
    sources::project(store.state(), project_id).is_ok_and(|project| {
        project.active
            && Path::new(&project.root_path) == Path::new(&workspace.original_root).join(prefix)
    })
}
