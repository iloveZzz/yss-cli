use super::{
    BoundHome, HomeBinding, HomeImport, HomeRow, home_stale, lock, remote_identity, row_remote,
    row_subpath, text,
};
use crate::{
    cancel::Cancellation,
    error::Result,
    fs::{Root, alias_key},
    workbench::{agent_workspace, model::ProjectRecord, process, sources, store::Store},
};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    path::Path,
    sync::{Arc, Mutex},
    time::Duration,
};

struct Checkout {
    head: String,
    branch: Option<String>,
    prefix: String,
    remote: Option<String>,
}

impl Checkout {
    fn probe(project: &ProjectRecord, cancel: &Cancellation) -> Result<Self> {
        let path = Path::new(&project.root_path);
        let root = agent_workspace::agent_git_root(path, cancel)?;
        let baseline = agent_workspace::git_baseline(&root, cancel)?;
        let prefix = path
            .strip_prefix(&root)
            .ok()
            .and_then(Path::to_str)
            .ok_or_else(|| sources::invalid("工程目录与 Git 根不一致"))?;
        let mut command = crate::fs::git::command(&root);
        command.args(["remote", "get-url", "origin"]);
        let remote = process::run_command(command, cancel, Duration::from_secs(10));
        cancel.check()?;
        Ok(Self {
            head: baseline.head,
            branch: baseline
                .branch
                .and_then(|branch| branch.strip_prefix("refs/heads/").map(str::to_owned)),
            prefix: if prefix.is_empty() { "." } else { prefix }.to_owned(),
            remote: remote
                .ok()
                .and_then(|output| remote_identity(std::str::from_utf8(&output).ok()?)),
        })
    }
}

fn binding_matches(entry: &HomeBinding, project: &ProjectRecord) -> bool {
    entry.project_id.as_ref() == Some(&project.id)
        && alias_key(&entry.root_path) == alias_key(&project.root_path)
}

fn project_status(project: &ProjectRecord, available: bool) -> &'static str {
    if !project.active {
        "inactive"
    } else if available {
        "ready"
    } else {
        "unavailable"
    }
}

fn home_entry(
    row: &HomeRow,
    bound: Option<&HomeBinding>,
    stale: bool,
    projects: &[ProjectRecord],
    checkouts: &BTreeMap<&str, Checkout>,
) -> Value {
    let project = bound.and_then(|entry| {
        projects
            .iter()
            .find(|project| binding_matches(entry, project))
    });
    let checkout = project.and_then(|project| checkouts.get(project.id.as_str()));
    let remote = row_remote(row);
    let path = row_subpath(row);
    let path_valid = path.is_ok();
    let prefix = path
        .ok()
        .flatten()
        .or_else(|| checkout.map(|checkout| checkout.prefix.clone()));
    let matches = |checkout: &Checkout| {
        path_valid
            && prefix
                .as_ref()
                .is_none_or(|prefix| prefix == &checkout.prefix)
            && remote
                .as_ref()
                .is_none_or(|remote| checkout.remote.as_ref() == Some(remote))
    };
    let status = if stale {
        "stale"
    } else if let Some(project) = project {
        project_status(project, checkout.is_some_and(matches))
    } else if bound.is_some() {
        "unavailable"
    } else {
        "unbound"
    };
    let mut candidates = Vec::new();
    if status == "ready"
        && let Some(project) = project
    {
        candidates.push(project.id.as_str());
    }
    if !stale && bound.is_some() && remote.is_some() && prefix.is_some() {
        for (id, checkout) in checkouts {
            if !candidates.contains(id) && matches(checkout) {
                candidates.push(id);
            }
        }
    }
    json!({
        "id":row.id,
        "projectId":bound.and_then(|entry| entry.project_id.as_ref()),
        "projectIds":candidates,
        "status":status,
        "name":project.map(|project| &project.name),
        "rootPath":bound.map(|entry| &entry.root_path),
    })
}

pub(in crate::workbench) fn agent_projects(
    store: &Arc<Mutex<Store>>,
    spec_id: &str,
    cancel: &Cancellation,
) -> Result<Value> {
    let (main, projects, imports, bindings) = {
        let mut store = lock(store)?;
        let spec = sources::spec(store.state(), spec_id)?;
        let main = sources::project(store.state(), &spec.project_id)?.clone();
        (
            main,
            store.state().projects.clone(),
            store.records::<HomeImport>("homeImport")?,
            store.records::<BoundHome>("homeBinding")?,
        )
    };
    let mut checkouts = BTreeMap::new();
    let mut entries = Vec::new();
    for project in &projects {
        cancel.check()?;
        let checkout = project
            .active
            .then(|| Checkout::probe(project, cancel))
            .and_then(Result::ok);
        entries.push(json!({
            "id":project.id, "name":project.name, "rootPath":project.root_path,
            "active":project.active, "status":project_status(project, checkout.is_some()),
            "branch":checkout.as_ref().and_then(|checkout| checkout.branch.as_deref()),
            "head":checkout.as_ref().map(|checkout| &checkout.head),
            "repositoryKey":checkout.as_ref().and_then(|checkout| checkout.remote.as_ref().map(|remote| {
                crate::model::Sha256::of(format!("{remote}\n{}", checkout.prefix).as_bytes()).as_str().to_owned()
            })),
        }));
        if let Some(checkout) = checkout {
            checkouts.insert(project.id.as_str(), checkout);
        }
    }
    let mut homes = Vec::new();
    for home in imports {
        cancel.check()?;
        let binding = bindings.iter().find(|binding| binding.home_id == home.id);
        let is_member = binding.is_some_and(|binding| {
            binding
                .bindings
                .iter()
                .any(|entry| binding_matches(entry, &main))
        });
        if alias_key(&home.root) != alias_key(&main.root_path) && !is_member {
            continue;
        }
        let current =
            Root::open(Path::new(&home.root)).and_then(|root| text(&root, &home.source_path));
        let stale = home_stale(&home, binding, current.as_deref().ok())?;
        let rows: Vec<_> = home
            .rows
            .iter()
            .map(|row| {
                let bound = binding.and_then(|binding| {
                    binding
                        .bindings
                        .iter()
                        .find(|entry| entry.entry_id == row.id)
                });
                home_entry(row, bound, stale, &projects, &checkouts)
            })
            .collect();
        homes.push(json!({
            "id":home.id, "root":home.root, "sourcePath":home.source_path,
            "stale":stale, "entries":rows,
        }));
    }
    cancel.check()?;
    Ok(json!({"projects":entries,"homes":homes}))
}
