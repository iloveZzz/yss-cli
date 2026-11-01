mod plan;
pub mod state;
use crate::{
    bundle::Bundle,
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, Root},
    model::{InstallationKind, Profile, RelPath, RenderedTree},
    transaction::{self, ChangeSet, TransactionReport, TransactionStatus},
};
pub use plan::plan;
use serde::Serialize;
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
};

pub struct InstallRequest {
    pub root: PathBuf,
    pub profile: Profile,
    pub installation: Option<InstallationKind>,
    pub variables: BTreeMap<String, String>,
    pub dry_run: bool,
}
pub struct SyncRequest {
    pub root: PathBuf,
    pub profile: Option<Profile>,
    pub installation: Option<InstallationKind>,
    pub variables: BTreeMap<String, String>,
    pub dry_run: bool,
}
pub struct EnsureRequest {
    pub root: PathBuf,
    pub profile: Option<Profile>,
    pub skills: BTreeSet<String>,
    pub dry_run: bool,
}
pub enum ProjectRequest {
    Init(InstallRequest),
    Attach(InstallRequest),
    Sync(SyncRequest),
    Ensure(EnsureRequest),
}
pub struct ProjectPlan {
    pub root: PathBuf,
    pub report: ProjectReport,
    pub(crate) action: Action,
}
pub(crate) enum Action {
    Unchanged,
    Transaction(ChangeSet),
    New(RenderedTree),
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProjectReport {
    pub root: String,
    pub profile: Profile,
    pub installation: InstallationKind,
    pub status: String,
    pub dry_run: bool,
    pub changes: Vec<PathChange>,
    pub preserved: Vec<RelPath>,
    pub diagnostics: Vec<SkillDiagnostic>,
    pub transaction: Option<TransactionReport>,
}
#[derive(Debug, Serialize)]
pub struct PathChange {
    pub path: RelPath,
    pub action: &'static str,
}
#[derive(Debug, Serialize)]
pub struct SkillDiagnostic {
    pub skill: String,
    pub status: &'static str,
    pub reason: String,
    pub replacements: Vec<String>,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SkillsReport {
    pub profile: Profile,
    pub installation: InstallationKind,
    pub available: BTreeSet<String>,
    pub installed: BTreeMap<String, &'static str>,
    pub defaults: BTreeSet<String>,
    pub explicit: BTreeSet<String>,
    pub selected: BTreeSet<String>,
    pub diagnostics: Vec<SkillDiagnostic>,
}
#[derive(Serialize)]
pub struct DoctorReport {
    pub installation: Option<state::ProjectState>,
    pub differences: Vec<PathChange>,
    pub transactions: TransactionStatus,
}

pub fn execute(mut plan: ProjectPlan, cancel: &Cancellation) -> Result<ProjectReport> {
    cancel.check()?;
    if plan.report.dry_run {
        return Ok(plan.report);
    }
    match plan.action {
        Action::Unchanged => {}
        Action::Transaction(changes) => {
            plan.report.transaction = Some(transaction::apply(&plan.root, changes, cancel)?);
        }
        Action::New(tree) => {
            fs::publish_new_tree(&plan.root, &tree, cancel)?;
        }
    }
    if plan.report.status != "unchanged" {
        plan.report.status = "applied".into();
    }
    Ok(plan.report)
}
pub fn list_skills(path: &Path, bundle: &Bundle) -> Result<SkillsReport> {
    let root = Root::open(path)?;
    let (state, _) = state::detect(&root)?;
    let state = state
        .ok_or_else(|| YssError::new(ErrorCode::NotReady, "项目尚未接入；请先 init 或 attach"))?;
    let profile = bundle.profile(state.profile)?;
    let (explicit, diagnostics) = available_explicit(profile, &state.explicit_skills);
    let selected = bundle.select(state.profile, state.installation, &explicit)?;
    let mut installed = BTreeMap::new();
    for name in profile.skills() {
        let prefix = format!(".agents/skills/{name}/");
        let mut status = "not-installed";
        for (path, file) in state
            .managed
            .iter()
            .filter(|(p, _)| p.as_str().starts_with(&prefix))
        {
            let actual = root.inspect(path)?;
            if actual == fs::FileSnapshot::Missing {
                status = "missing";
                break;
            }
            if actual.descriptor() != Some(&file.last_installed) {
                status = "modified";
                break;
            }
            status = "installed";
        }
        if status == "not-installed"
            && root.inspect(&RelPath::new(format!("{prefix}SKILL.md"))?)?
                != fs::FileSnapshot::Missing
        {
            status = "unmanaged";
        }
        installed.insert(name, status);
    }
    Ok(SkillsReport {
        installed,
        profile: state.profile,
        installation: state.installation,
        available: profile.skills(),
        defaults: selected.defaults,
        explicit: state.explicit_skills,
        selected: selected.closure,
        diagnostics,
    })
}
pub fn doctor(path: &Path, _bundle: &Bundle) -> Result<DoctorReport> {
    let root = Root::open(path)?;
    let (state, _) = state::detect(&root)?;
    let mut differences = Vec::new();
    if let Some(state) = &state {
        for (path, managed) in &state.managed {
            let actual = root.inspect(path)?;
            if actual.descriptor() != Some(&managed.last_installed) {
                differences.push(PathChange {
                    path: path.clone(),
                    action: if actual == fs::FileSnapshot::Missing {
                        "deleted"
                    } else {
                        "modified"
                    },
                });
            }
        }
    }
    Ok(DoctorReport {
        installation: state,
        differences,
        transactions: transaction::status(path)?,
    })
}
fn available_explicit(
    profile: &crate::bundle::manifest::ProfileBundle,
    explicit: &BTreeSet<String>,
) -> (BTreeSet<String>, Vec<SkillDiagnostic>) {
    let available = profile.skills();
    let mut diagnostics = Vec::new();
    for name in explicit.difference(&available) {
        let retired = profile.retired_skills.get(name);
        diagnostics.push(SkillDiagnostic {
            skill: name.clone(),
            status: if retired.is_some() {
                "retired"
            } else {
                "unavailable"
            },
            reason: retired
                .map_or_else(|| "当前 Bundle 不再提供该技能".into(), |r| r.reason.clone()),
            replacements: retired.map_or_else(Vec::new, |r| r.replacements.clone()),
        });
    }
    (
        explicit.intersection(&available).cloned().collect(),
        diagnostics,
    )
}
