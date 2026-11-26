use super::{
    AcceptFile, AcceptRequest, AcceptanceLease, AgentRun, Store,
    activity::{AcceptanceStatus, ProjectActivity},
    current_descriptor, invalid, lease_owner,
};
use crate::{
    error::{ErrorCode, Result, YssError},
    fs::{Root, RootIdentity},
    model::{RelPath, Sha256},
    transaction::{self, TransactionPhase},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

pub(super) const ATTEMPT: &str = "agentReviewAttempt";
pub(super) const RECEIPT: &str = "agentReviewReceipt";

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ProjectBinding {
    root: String,
    identity: RootIdentity,
    prefix: String,
}

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Attempt {
    pub job_id: String,
    binding_revision: u64,
    #[serde(default)]
    attempt_id: Option<String>,
    #[serde(default)]
    request_digest: Option<String>,
    files: Vec<AcceptFile>,
    projects: Option<Vec<Value>>,
    previous_reports: Option<Vec<Value>>,
    #[serde(default)]
    previous_files: Vec<AcceptFile>,
    #[serde(default)]
    bindings: BTreeMap<String, ProjectBinding>,
}

#[derive(Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Receipt {
    job_id: String,
    request_digest: Option<String>,
    response: Value,
}

pub(super) fn attempt_id(id: &str) -> Result<()> {
    if id.is_empty()
        || id.len() > 128
        || !id
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
    {
        return Err(invalid("接收请求编号无效"));
    }
    Ok(())
}

pub(super) fn receipt_key(run_id: &str, id: &str) -> String {
    format!("{run_id}/{id}")
}

fn request_digest(request: &AcceptRequest) -> Result<String> {
    let bytes = serde_json::to_vec(request).map_err(|_| invalid("接收请求编码失败"))?;
    Ok(Sha256::of(&bytes).as_str().into())
}

pub(super) fn replay(
    store: &mut Store,
    run_id: &str,
    request: &AcceptRequest,
) -> Result<Option<Value>> {
    let Some(id) = &request.attempt_id else {
        return Ok(None);
    };
    attempt_id(id)?;
    let Some(receipt) = store.record::<Receipt>(RECEIPT, &receipt_key(run_id, id))? else {
        return Ok(None);
    };
    if receipt.request_digest.is_none() {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "该接收请求已经核实未受理，请重新审阅后提交",
        ));
    }
    if receipt.request_digest.as_deref() != Some(&request_digest(request)?) {
        return Err(YssError::new(
            ErrorCode::Conflict,
            "接收请求编号已用于不同的选择",
        ));
    }
    Ok(Some(receipt.response))
}

pub(super) fn roots(run: &AgentRun) -> Vec<String> {
    run.workspaces
        .iter()
        .map(|workspace| workspace.original_root.clone())
        .collect()
}

fn project_bindings(run: &AgentRun) -> BTreeMap<String, ProjectBinding> {
    run.workspaces
        .iter()
        .flat_map(|workspace| {
            workspace.project_prefixes.iter().map(|(project, prefix)| {
                (
                    project.clone(),
                    ProjectBinding {
                        root: workspace.original_root.clone(),
                        identity: workspace.original_identity.clone(),
                        prefix: prefix.clone(),
                    },
                )
            })
        })
        .collect()
}

pub(super) fn requires_verification(disposition: &str) -> bool {
    matches!(
        disposition,
        "unverified" | "recoveryRequired" | "committedPendingCleanup"
    )
}

pub(super) fn round_activity(
    store: &mut Store,
    run_id: &str,
    job_id: &str,
    binding_revision: u64,
    applied: &BTreeSet<&str>,
) -> Result<Vec<ProjectActivity>> {
    let attempt = Attempt::load(store, run_id)?;
    let eligible = attempt.as_ref().is_none_or(|attempt| {
        attempt.job_id == job_id && attempt.binding_revision == binding_revision
    });
    let mut statuses: BTreeMap<String, AcceptanceStatus> = if eligible && binding_revision == 0 {
        applied
            .iter()
            .map(|id| ((*id).to_owned(), AcceptanceStatus::Accepted))
            .collect()
    } else {
        BTreeMap::new()
    };
    if let Some(attempt) = attempt.filter(|_| eligible) {
        for report in attempt
            .projects
            .as_deref()
            .or(attempt.previous_reports.as_deref())
            .unwrap_or_default()
        {
            let Some(id) = report["projectId"].as_str() else {
                continue;
            };
            if report["jobId"]
                .as_str()
                .is_some_and(|value| value != job_id)
            {
                continue;
            }
            let status = match report["disposition"].as_str() {
                Some("applied") if applied.contains(id) => AcceptanceStatus::Accepted,
                Some("noWrite" | "rolledBack") if report["status"] == "failed" => {
                    AcceptanceStatus::Failed
                }
                Some("noWrite" | "rolledBack") => AcceptanceStatus::NoWrite,
                _ => AcceptanceStatus::NeedsVerification,
            };
            statuses.insert(id.to_owned(), status);
        }
        if attempt.projects.is_none() {
            let status = if lease_active(store, run_id)? {
                AcceptanceStatus::Writing
            } else {
                AcceptanceStatus::NeedsVerification
            };
            for file in &attempt.files {
                statuses.insert(file.project_id.clone(), status);
            }
        }
    }
    Ok(statuses
        .into_iter()
        .map(|(project_id, status)| ProjectActivity { project_id, status })
        .collect())
}

fn unresolved_report(report: &Value) -> bool {
    report["disposition"]
        .as_str()
        .is_some_and(requires_verification)
}

fn shares_root(left: &str, right: &str) -> bool {
    Path::new(left).starts_with(right) || Path::new(right).starts_with(left)
}

fn merge(mut reports: Vec<Value>, previous: &[Value]) -> Vec<Value> {
    for report in previous {
        if !reports
            .iter()
            .any(|item| item["projectId"] == report["projectId"])
        {
            reports.push(report.clone());
        }
    }
    reports
}

fn record_round(reports: &mut [Value], job_id: &str) {
    for report in reports {
        if report["jobId"].is_null() {
            report["jobId"] = json!(job_id);
        }
    }
}

impl Attempt {
    pub(super) fn load(store: &mut Store, run_id: &str) -> Result<Option<Self>> {
        store.record(ATTEMPT, run_id)
    }

    fn with_bindings(mut self, store: &mut Store, run_id: &str) -> Result<Self> {
        if self.bindings.is_empty() {
            let current = store.record::<AgentRun>("agentRun", run_id)?;
            let run = match current {
                Some(run) if run.binding_revision == self.binding_revision => Some(run),
                _ => store
                    .record::<AgentRun>("agentRound", &self.job_id)?
                    .filter(|run| {
                        run.id == run_id && run.binding_revision == self.binding_revision
                    }),
            };
            if let Some(run) = run {
                self.bindings = project_bindings(&run);
            }
        }
        Ok(self)
    }

    fn reports(&self, active: bool) -> Result<Vec<Value>> {
        let mut reports = if let Some(reports) = &self.projects {
            reports.clone()
        } else {
            let ids: BTreeSet<_> = self
                .files
                .iter()
                .map(|file| file.project_id.as_str())
                .collect();
            ids.into_iter()
                .map(|id| {
                    let binding = self
                        .bindings
                        .get(id)
                        .ok_or_else(|| invalid("接收记录缺少工程绑定"))?;
                    Ok(json!({"projectId":id,"root":binding.root,
                    "disposition": if active { "writing" } else { "unverified" }}))
                })
                .collect::<Result<Vec<_>>>()?
        };
        record_round(&mut reports, &self.job_id);
        Ok(merge(
            reports,
            self.previous_reports.as_deref().unwrap_or_default(),
        ))
    }

    fn observations(
        &self,
        project: &str,
        binding: &ProjectBinding,
        root: &Root,
    ) -> Result<Vec<Value>> {
        self.files
            .iter()
            .chain(&self.previous_files)
            .filter(|file| file.project_id == project)
            .map(|file| {
                let path = if binding.prefix.is_empty() {
                    file.path.clone()
                } else {
                    RelPath::new(&binding.prefix)?
                        .join(&file.path)?
                        .as_str()
                        .into()
                };
                let current = current_descriptor(root, &path)?;
                Ok(json!({"path":file.path,
                    "matchesResult":current == file.expected_result,
                    "matchesBefore":current == file.expected_current}))
            })
            .collect()
    }

    fn inspect(&self, report: &mut Value, verify: bool) -> Result<()> {
        let disposition = report["disposition"]
            .as_str()
            .unwrap_or_default()
            .to_owned();
        let id = report["projectId"]
            .as_str()
            .ok_or_else(|| invalid("接收记录缺少工程编号"))?;
        let binding = self
            .bindings
            .get(id)
            .ok_or_else(|| invalid("接收记录缺少可信的工程绑定"))?;
        let root = Root::open(Path::new(&binding.root))?;
        if root.identity != binding.identity {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "接收时绑定的 Git 工程目录身份已变化",
            )
            .at(&binding.root));
        }
        let observed = self.observations(id, binding, &root)?;
        if !observed.is_empty() {
            report["observed"] = json!(observed);
        }
        if !verify {
            return Ok(());
        }

        let transactions = transaction::status(Path::new(&binding.root))?;
        root.revalidate()?;
        let pending_commit = transactions.transactions.iter().any(|transaction| {
            transaction.phase == TransactionPhase::Committed
                && transactions.cleanup_pending.contains(&transaction.id)
        });
        report["disposition"] = json!(if !transactions.pending.is_empty() {
            "recoveryRequired"
        } else if pending_commit {
            "committedPendingCleanup"
        } else if !transactions.cleanup_pending.is_empty() {
            "recoveryRequired"
        } else if disposition == "committedPendingCleanup" && report["status"] == "applied" {
            "applied"
        } else {
            "verified"
        });
        Ok(())
    }

    pub(super) fn begin(store: &mut Store, run: &AgentRun, request: &AcceptRequest) -> Result<()> {
        let mut previous_reports = None;
        let mut previous_files = Vec::new();
        if let Some(previous) = Self::load(store, &run.id)? {
            let previous = previous.with_bindings(store, &run.id)?;
            let mut reports = previous.reports(false)?;
            if previous.binding_revision != run.binding_revision {
                if let Some(report) = reports.iter().find(|report| unresolved_report(report)) {
                    return Err(recovery_error(
                        &run.id,
                        &previous.job_id,
                        report,
                        report["root"].as_str().unwrap_or_default(),
                    ));
                }
                reports.clear();
            } else if previous.job_id != run.job_id {
                reports.retain(unresolved_report);
            }
            let selected: BTreeSet<_> = request
                .files
                .iter()
                .map(|file| file.project_id.as_str())
                .collect();
            previous_files.extend(
                previous
                    .files
                    .into_iter()
                    .chain(previous.previous_files)
                    .filter(|file| {
                        !selected.contains(file.project_id.as_str())
                            && reports.iter().any(|report| {
                                unresolved_report(report) && report["projectId"] == file.project_id
                            })
                    }),
            );
            previous_reports = Some(reports);
        }
        store.put_record(
            ATTEMPT,
            &run.id,
            &Self {
                job_id: run.job_id.clone(),
                binding_revision: run.binding_revision,
                attempt_id: request.attempt_id.clone(),
                request_digest: request
                    .attempt_id
                    .as_ref()
                    .map(|_| request_digest(request))
                    .transpose()?,
                files: request.files.clone(),
                projects: None,
                previous_reports,
                previous_files,
                bindings: project_bindings(run),
            },
        )
    }

    pub(super) fn complete(&mut self, mut reports: Vec<Value>) -> &[Value] {
        record_round(&mut reports, &self.job_id);
        self.projects = Some(merge(
            reports,
            self.previous_reports.as_deref().unwrap_or_default(),
        ));
        self.previous_reports = None;
        self.projects.as_deref().unwrap_or_default()
    }

    pub(super) fn record(&self, run_id: &str) -> Result<super::super::sqlite::RecordWrite> {
        Ok(super::super::sqlite::RecordWrite {
            kind: ATTEMPT.into(),
            id: run_id.into(),
            payload: Some(serde_json::to_string(self).map_err(|_| invalid("接收记录编码失败"))?),
        })
    }

    pub(super) fn receipt(
        &self,
        run_id: &str,
        mut response: Value,
    ) -> Result<Option<super::super::sqlite::RecordWrite>> {
        self.attempt_id
            .as_ref()
            .map(|id| {
                response["attemptId"] = json!(id);
                let receipt = Receipt {
                    job_id: self.job_id.clone(),
                    request_digest: self.request_digest.clone(),
                    response,
                };
                Ok(super::super::sqlite::RecordWrite {
                    kind: RECEIPT.into(),
                    id: receipt_key(run_id, id),
                    payload: Some(
                        serde_json::to_string(&receipt).map_err(|_| invalid("接收回执编码失败"))?,
                    ),
                })
            })
            .transpose()
    }

    pub(super) fn id(&self) -> Option<&str> {
        self.attempt_id.as_deref()
    }
}

fn recovery_error(run_id: &str, job_id: &str, report: &Value, root: &str) -> YssError {
    YssError::new(
        ErrorCode::RecoveryRequired,
        "工程有待恢复或核实的接收结果，请先核对该轮成果",
    )
    .at(root)
    .context("runId", run_id)
    .context("jobId", report["jobId"].as_str().unwrap_or(job_id))
}

pub(super) fn recovery_block(store: &mut Store, roots: &[String]) -> Result<Option<YssError>> {
    if roots.is_empty() {
        return Ok(None);
    }
    let identities: Vec<_> = roots
        .iter()
        .filter_map(|root| Root::open(Path::new(root)).ok().map(|root| root.identity))
        .collect();
    for (run_id, attempt) in store.record_entries::<Attempt>(ATTEMPT)? {
        if attempt
            .projects
            .as_ref()
            .is_some_and(|reports| !reports.iter().any(unresolved_report))
            && attempt
                .previous_reports
                .as_ref()
                .is_none_or(|reports| !reports.iter().any(unresolved_report))
        {
            continue;
        }
        let attempt = attempt.with_bindings(store, &run_id)?;
        for report in attempt
            .reports(false)?
            .iter()
            .filter(|report| unresolved_report(report))
        {
            let binding = report["projectId"]
                .as_str()
                .and_then(|id| attempt.bindings.get(id));
            let Some(root) = binding
                .map(|binding| binding.root.as_str())
                .or_else(|| report["root"].as_str())
            else {
                return Err(invalid("接收恢复记录缺少 Git 工程根"));
            };
            if roots.iter().any(|candidate| shares_root(candidate, root))
                || binding.is_some_and(|binding| identities.contains(&binding.identity))
            {
                return Ok(Some(recovery_error(&run_id, &attempt.job_id, report, root)));
            }
        }
    }
    Ok(None)
}

pub(super) fn ensure_recovered(store: &mut Store, roots: &[String]) -> Result<()> {
    match recovery_block(store, roots)? {
        Some(error) => Err(error),
        None => Ok(()),
    }
}

pub(super) fn summary(store: &mut Store, run: &AgentRun) -> Result<Option<Value>> {
    summary_for_round(store, &run.id, &run.job_id, run.binding_revision)
}

pub(super) fn summary_for_round(
    store: &mut Store,
    run_id: &str,
    job_id: &str,
    binding_revision: u64,
) -> Result<Option<Value>> {
    let Some(attempt) = Attempt::load(store, run_id)? else {
        return Ok(None);
    };
    let active = lease_active(store, run_id)?;
    let attempt = attempt.with_bindings(store, run_id)?;
    let reports = attempt.reports(active)?;
    let belongs_to_round = attempt.job_id == job_id && attempt.binding_revision == binding_revision;
    Ok((belongs_to_round || reports.iter().any(unresolved_report))
        .then(|| json!({"jobId": attempt.job_id, "projects": reports})))
}

pub(super) fn lease_active(store: &mut Store, run_id: &str) -> Result<bool> {
    Ok(store
        .record::<AcceptanceLease>("agentAcceptance", run_id)?
        .is_some_and(|lease| lease.owner == lease_owner()))
}

pub(super) fn view(
    store: &mut Store,
    run: &AgentRun,
    verifying: bool,
    expected_attempt: Option<&str>,
) -> Result<Option<Value>> {
    let lease_active = lease_active(store, &run.id)?;
    if verifying && lease_active {
        return Err(YssError::new(ErrorCode::Locked, "接收尚未结束，请稍后核实"));
    }
    let attempt = Attempt::load(store, &run.id)?;
    if let Some(id) = expected_attempt {
        attempt_id(id)?;
        if attempt.as_ref().and_then(Attempt::id) != Some(id) {
            let key = receipt_key(&run.id, id);
            if let Some(receipt) = store.record::<Receipt>(RECEIPT, &key)? {
                if receipt.job_id != run.job_id {
                    return Err(super::stale_round(run));
                }
                return Ok(Some(receipt.response));
            }
            if !verifying {
                return Ok(None);
            }
            let response =
                json!({"jobId":run.job_id,"attemptId":id,"notReceived":true,"projects":[]});
            store.put_record(
                RECEIPT,
                &key,
                &Receipt {
                    job_id: run.job_id.clone(),
                    request_digest: None,
                    response: response.clone(),
                },
            )?;
            return Ok(Some(response));
        }
    }
    let Some(attempt) = attempt else {
        return Ok(None);
    };
    let mut attempt = attempt.with_bindings(store, &run.id)?;
    let mut reports = attempt.reports(lease_active)?;
    if !verifying && attempt.job_id != run.job_id && !reports.iter().any(unresolved_report) {
        return Ok(None);
    }
    let active_roots: Vec<_> = if verifying {
        store
            .records::<super::JobRecord>("job")?
            .into_iter()
            .filter(|job| !job.terminal())
            .flat_map(|job| job.roots)
            .collect()
    } else {
        Vec::new()
    };
    for report in reports
        .iter_mut()
        .filter(|report| unresolved_report(report))
    {
        let result = if report["root"]
            .as_str()
            .is_some_and(|root| active_roots.iter().any(|active| shares_root(root, active)))
        {
            Err(YssError::new(
                ErrorCode::Locked,
                "工程有运行中的操作，请稍后核实",
            ))
        } else {
            attempt.inspect(report, verifying)
        };
        if let Err(error) = result {
            report["verificationError"] = json!(error.message);
            if let Some(fields) = report.as_object_mut() {
                fields.remove("observed");
            }
        } else if let Some(fields) = report.as_object_mut() {
            fields.remove("verificationError");
        }
    }
    if verifying {
        attempt.projects = Some(reports.clone());
        attempt.previous_reports = None;
        store.put_record(ATTEMPT, &run.id, &attempt)?;
    }
    Ok(Some(
        json!({"jobId":attempt.job_id,"attemptId":attempt.id(),"projects":reports}),
    ))
}
