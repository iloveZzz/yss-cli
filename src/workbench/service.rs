use super::history::changes;
use super::{
    model::*,
    sources::{self, invalid, missing, now},
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, Root},
    model::RelPath,
};
use std::{collections::BTreeMap, path::Path};

pub use super::sources::{discover, read_source};

fn record_kinds() -> Vec<&'static str> {
    let mut kinds = super::progress::RECORD_KINDS.to_vec();
    kinds.extend([super::materials::SNAPSHOTS, super::materials::REVIEWS]);
    kinds
}

fn attach_progress(
    snapshot: &mut Snapshot,
    mut records: super::progress::ProgressRecords,
    state: &WorkbenchState,
) {
    let mut project_ids: std::collections::BTreeSet<String> = records
        .goals
        .iter()
        .flat_map(|goal| {
            goal.goal
                .participants
                .iter()
                .map(|participant| participant.project_id.clone())
        })
        .collect();
    for goal in &records.goals {
        if let Some(spec) = state.specs.iter().find(|spec| spec.id == goal.goal.spec_id) {
            project_ids.insert(spec.project_id.clone());
        }
    }
    records.current_versions = super::progress::current_versions(state, &project_ids);
    super::progress::refresh_inputs(&mut records, state, &Cancellation::new());
    snapshot.progress = super::progress::project(snapshot, &records);
    for spec in &mut snapshot.specs {
        spec.progress = snapshot.progress.get(&spec.record.id).cloned();
    }
    snapshot.progress_records = records;
}

pub fn snapshot_from_store(store: &mut Store, cancel: &Cancellation) -> Result<Snapshot> {
    let records = super::progress::load(store)?;
    let materials = super::materials::Records::load(store)?;
    let mut value = snapshot(store.state(), &materials, cancel)?;
    attach_progress(&mut value, records, store.state());
    Ok(value)
}

pub fn read_snapshot(data_dir: &Path, cancel: &Cancellation) -> Result<Snapshot> {
    let (state, records) = super::store::read_state_with_records(data_dir, &record_kinds())?;
    let materials = super::materials::Records::from_raw(&records)?;
    let mut value = snapshot(&state, &materials, cancel)?;
    attach_progress(
        &mut value,
        super::progress::ProgressRecords::from_raw(&records)?,
        &state,
    );
    Ok(value)
}

pub fn progress_context(snapshot: &Snapshot, spec_id: &str, data_dir: &Path) -> Result<String> {
    let spec = snapshot
        .specs
        .iter()
        .find(|spec| spec.record.id == spec_id)
        .ok_or_else(|| missing("Spec 不存在"))?;
    let context = serde_json::json!({
        "specId": spec_id,
        "dataDir": data_dir,
        "cliExecutable": std::env::current_exe().ok(),
        "specDigest": spec.content.artifact_digest,
        "observedArtifactDigest": spec.content.observed_artifact_digest,
        "materialReviewDigest": spec.content.material_review_digest,
        "materialChanges": spec.content.material_changes,
        "progress": snapshot.progress.get(spec_id),
        "project": snapshot.projects.iter().find(|project| project.record.id == spec.record.project_id)
            .map(|project| &project.record),
    });
    Ok(format!(
        "\n生命周期上报上下文（沿用此 Spec 身份与数据目录）\n{}\n\n在实际工作开始、产物与适用检查完成、遇到阻塞及版本影响核实后，依据当前程序的 workbench progress 帮助上报。目标和验收沿本次用户授权，报告关联实际成果、采用版本和检查结果。共享材料的影响待核实时，先核对原条款和验收，再根据 workbench review-materials 帮助记录影响及采用依据。该操作不产生人工确认。隔离工作区成果沿原工程身份记录，接收后的文件由工作台刷新原 Spec。关键确认记录用户对当前范围与版本的明确决定及真实来源。进度能力暂不可用时，在原任务结果中保留待同步事实并继续已授权工作。\n",
        serde_json::to_string_pretty(&context).map_err(|_| invalid("进度上下文编码失败"))?
    ))
}

pub fn read_agent_request(
    data_dir: &Path,
    spec_id: &str,
    cancel: &Cancellation,
) -> Result<AgentRequest> {
    let (state, raw) = super::store::read_state_with_records(data_dir, &record_kinds())?;
    let materials = super::materials::Records::from_raw(&raw)?;
    let mut view = snapshot(&state, &materials, cancel)?;
    attach_progress(
        &mut view,
        super::progress::ProgressRecords::from_raw(&raw)?,
        &state,
    );
    let mut request = super::content::agent_request_with_materials(
        &state,
        spec_id,
        Some(data_dir),
        Some(&materials),
    )?;
    request
        .text
        .push_str(&progress_context(&view, spec_id, data_dir)?);
    Ok(request)
}

pub(super) fn agent_request_from_store(
    store: &mut Store,
    spec_id: &str,
    cancel: &Cancellation,
) -> Result<AgentRequest> {
    let records = super::progress::load(store)?;
    let materials = super::materials::Records::load(store)?;
    let mut view = snapshot(store.state(), &materials, cancel)?;
    attach_progress(&mut view, records, store.state());
    let mut request = super::content::agent_request_with_materials(
        store.state(),
        spec_id,
        Some(store.data_dir()),
        Some(&materials),
    )?;
    request
        .text
        .push_str(&progress_context(&view, spec_id, store.data_dir())?);
    Ok(request)
}

fn snapshot(
    state: &WorkbenchState,
    materials: &super::materials::Records,
    cancel: &Cancellation,
) -> Result<Snapshot> {
    let mut projects = vec![];
    let mut specs = vec![];
    let mut tasks = vec![];
    for project in &state.projects {
        cancel.check()?;
        let root = Root::open(Path::new(&project.root_path));
        let shared = root
            .as_ref()
            .map(|root| super::materials::shared(state, materials, &project.id, root))
            .unwrap_or_default();
        let context = super::materials::Context {
            records: materials,
            shared: &shared,
        };
        projects.push(ProjectView {
            source_directories: root.as_ref().map_or_else(
                |_| vec![],
                |root| {
                    sources::source_directories(root, cancel)
                        .into_iter()
                        .map(|path| path.as_str().to_owned())
                        .collect()
                },
            ),
            record: project.clone(),
            available: root.is_ok(),
            message: root.as_ref().err().map(|e| e.message.clone()),
        });
        for s in state.specs.iter().filter(|s| s.project_id == project.id) {
            cancel.check()?;
            specs.push(sources::spec_view(state, s, &root, Some(&context)));
            for task in state.tasks.iter().filter(|t| t.spec_id == s.id) {
                cancel.check()?;
                tasks.push(TaskView {
                    record: task.clone(),
                    resources: sources::resource_views(&task.resources, &root),
                });
            }
        }
        if let Ok(root) = root
            && let Err(e) = root.revalidate()
        {
            let p = projects.last_mut().expect("project");
            p.available = false;
            p.message = Some(e.message.clone());
            for s in specs
                .iter_mut()
                .filter(|s| s.record.project_id == project.id)
            {
                s.source_health = SourceHealth::Unreadable;
                s.source_message = Some(e.message.clone());
            }
        }
    }
    Ok(Snapshot {
        progress_records: super::progress::ProgressRecords::default(),
        progress: BTreeMap::new(),
        revision: state.revision,
        read_at: now(),
        projects,
        specs,
        history: state.history.clone(),
        tasks,
        stage_records: state.stage_records.clone(),
        saved_views: state.saved_views.clone(),
    })
}
pub(super) fn project_root(path: &str, data: &Path) -> Result<String> {
    if !Path::new(path).is_absolute() {
        return Err(invalid("项目根必须是绝对目录"));
    }
    let path = std::fs::canonicalize(fs::absolute(Path::new(path))?)?;
    if data.starts_with(&path) {
        return Err(YssError::new(
            ErrorCode::UnsafePath,
            "项目不能包含工作台数据目录",
        ));
    }
    Root::open(&path)?.revalidate()?;
    path.to_str()
        .map(str::to_owned)
        .ok_or_else(|| invalid("项目路径不是 UTF-8"))
}
fn nonempty(value: String, label: &str) -> Result<String> {
    let value = value.trim().to_owned();
    if value.is_empty() {
        Err(invalid(&format!("{label}不能为空")))
    } else {
        Ok(value)
    }
}
fn check_project_unique(state: &WorkbenchState, root: &str, except: &str) -> Result<()> {
    if state
        .projects
        .iter()
        .any(|p| p.id != except && fs::alias_key(&p.root_path) == fs::alias_key(root))
    {
        Err(YssError::new(
            ErrorCode::Conflict,
            "此项目已登记（含停用项目）；请打开原记录或恢复",
        ))
    } else {
        Ok(())
    }
}
fn check_source_unique(
    state: &WorkbenchState,
    project_id: &str,
    path: &str,
    except: &str,
) -> Result<()> {
    if state.specs.iter().any(|s| {
        s.id != except
            && s.project_id == project_id
            && fs::alias_key(&s.source_path) == fs::alias_key(path)
    }) {
        Err(YssError::new(
            ErrorCode::Conflict,
            "此文件已关联（含停用 Spec）；请打开原记录或恢复",
        ))
    } else {
        Ok(())
    }
}
pub(super) fn validate_resources(resources: &[ResourceRef]) -> Result<()> {
    for r in resources {
        if r.label.trim().is_empty() {
            return Err(invalid("资源名称不能为空"));
        }
        match r.target_type {
            TargetType::Local => {
                RelPath::new(&r.target)?;
            }
            TargetType::Web => {
                let url = reqwest::Url::parse(&r.target).map_err(|_| invalid("资源链接不合法"))?;
                if !["http", "https"].contains(&url.scheme())
                    || url.host_str().is_none()
                    || !url.username().is_empty()
                    || url.password().is_some()
                {
                    return Err(invalid("仅支持不带账号密码的 HTTP(S) 资源链接"));
                }
            }
        }
    }
    Ok(())
}
#[derive(serde::Serialize, serde::Deserialize)]
struct MutationReceipt {
    request_digest: String,
    result: MutationResult,
}

pub(crate) fn mutation_digest(action: &Action, expected_target: &Option<String>) -> Result<String> {
    Ok(crate::model::Sha256::of(
        &serde_json::to_vec(&(action, expected_target)).map_err(|_| invalid("请求编码失败"))?,
    )
    .as_str()
    .to_owned())
}

pub fn mutate(store: &mut Store, mut request: MutationRequest) -> Result<MutationResult> {
    let digest = mutation_digest(&request.action, &request.expected_target)?;
    if let Some(id) = &request.request_id {
        if id.is_empty() || id.len() > 128 {
            return Err(invalid("提交请求 ID 无效"));
        }
        if let Some(receipt) = store.record::<MutationReceipt>("mutationReceipt", id)? {
            if receipt.request_digest != digest {
                return Err(YssError::new(
                    ErrorCode::Conflict,
                    "该请求 ID 已用于不同的提交",
                ));
            }
            return Ok(receipt.result);
        }
    }
    if let Some(expected) = &request.expected_target {
        let current = super::drafts::target(store.state(), &request.action)?;
        if current["token"].as_str() != Some(expected) {
            return Err(
                YssError::new(ErrorCode::ConcurrentChange, "编辑内容已更新，请比较后保存")
                    .context("revision", store.state().revision)
                    .context(
                        "currentTargetDigest",
                        current["token"].as_str().unwrap_or(""),
                    )
                    .context("currentEditableValues", current["values"].to_string()),
            );
        }
        request.expected_revision = store.state().revision;
    } else if request.expected_revision < store.state().revision
        && matches!(
            request.action,
            Action::ConfirmContent { .. }
                | Action::ConfirmProgress { .. }
                | Action::ReviewMaterials { .. }
        )
    {
        // Each of these writes revalidates the exact displayed inputs under this store lock.
        request.expected_revision = store.state().revision;
    }
    mutate_inner(store, request, &digest)
}

fn finish_mutation(
    store: &mut Store,
    expected_revision: u64,
    state: WorkbenchState,
    mut result: MutationResult,
    mut effects: super::sqlite::CommitEffects,
    request_id: Option<&str>,
    request_digest: &str,
) -> Result<MutationResult> {
    let changed = &state != store.state()
        || !effects.records.is_empty()
        || !effects.blobs.is_empty()
        || request_id.is_some();
    result.revision = if changed {
        expected_revision
            .checked_add(1)
            .ok_or_else(|| invalid("版本已溢出"))?
    } else {
        expected_revision
    };
    if let Some(id) = request_id {
        let receipt = MutationReceipt {
            request_digest: request_digest.into(),
            result: result.clone(),
        };
        effects.records.push(super::sqlite::RecordWrite {
            kind: "mutationReceipt".into(),
            id: id.into(),
            payload: Some(
                serde_json::to_string(&receipt).map_err(|_| invalid("提交回执编码失败"))?,
            ),
        });
    }
    result.revision = store.commit_with_effects(expected_revision, state, &effects)?;
    Ok(result)
}

fn mutate_inner(
    store: &mut Store,
    request: MutationRequest,
    request_digest: &str,
) -> Result<MutationResult> {
    if request.expected_revision != store.state().revision {
        return Err(
            YssError::new(ErrorCode::ConcurrentChange, "内容已改变，请刷新后重新保存")
                .context("revision", store.state().revision),
        );
    }
    let mut state = store.state().clone();
    let at = now();
    let progress_spec_id = match &request.action {
        Action::SetLifecycleGoal { input } => Some(input.spec_id.clone()),
        Action::ReportProgress { input } => Some(input.spec_id.clone()),
        Action::ConfirmProgress { input } => Some(input.spec_id.clone()),
        Action::SetProgressOverride { input } => Some(input.spec_id.clone()),
        Action::ClearProgressOverride { input } => Some(input.spec_id.clone()),
        Action::ReconcileProgress { spec_id } => Some(spec_id.clone()),
        _ => None,
    };
    if let Some(spec_id) = progress_spec_id {
        let project_id = sources::spec(&state, &spec_id)?.project_id.clone();
        let mut records = super::progress::load(store)?;
        let mut project_ids: std::collections::BTreeSet<String> = records
            .goals
            .iter()
            .flat_map(|goal| {
                goal.goal
                    .participants
                    .iter()
                    .map(|participant| participant.project_id.clone())
            })
            .chain(match &request.action {
                Action::SetLifecycleGoal { input } => input
                    .participants
                    .iter()
                    .map(|participant| participant.project_id.clone())
                    .collect(),
                _ => Vec::new(),
            })
            .collect();
        project_ids.insert(project_id.clone());
        records.current_versions = super::progress::current_versions(&state, &project_ids);
        super::progress::refresh_inputs(&mut records, &state, &Cancellation::new());
        let materials = super::materials::Records::load(store)?;
        let view = snapshot(&state, &materials, &Cancellation::new())?;
        let effects = match request.action.clone() {
            Action::SetLifecycleGoal { input } => {
                super::progress::prepare_goal(&view, &records, input, &at)?
            }
            Action::ReportProgress { input } => {
                super::progress::prepare_report(&view, &records, input, &at)?
            }
            Action::ConfirmProgress { input } => {
                super::progress::prepare_confirmation(&view, &records, input, &at)?
            }
            Action::SetProgressOverride { input } => {
                super::progress::prepare_override(&view, &records, input, &at)?
            }
            Action::ClearProgressOverride { input } => {
                super::progress::prepare_clear_override(&view, &records, input, &at)?
            }
            Action::ReconcileProgress { .. } => {
                super::progress::prepare_reconcile(&view, &records, &spec_id, &at)?
            }
            _ => unreachable!("progress action was selected above"),
        };
        if !effects.records.is_empty() {
            let operation =
                serde_json::to_value(&request.action).map_err(|_| invalid("进展操作编码失败"))?;
            let details = serde_json::json!({
                "operation": operation["kind"],
                "goalVersion": operation.pointer("/input/goalVersion"),
                "reportId": operation.pointer("/input/id"),
                "stage": operation.pointer("/input/stage")
            });
            super::history::record_change(
                &mut state,
                super::history::EventTarget {
                    project_id: project_id.clone(),
                    spec_id: Some(spec_id.clone()),
                    task_id: None,
                    lifecycle_stage: None,
                },
                None::<&serde_json::Value>,
                &serde_json::json!({"progress":details}),
                &at,
            )?;
            super::tasks::touch_spec(&mut state, &spec_id, &at);
        }
        return finish_mutation(
            store,
            request.expected_revision,
            state,
            MutationResult {
                response_id: None,
                revision: 0,
                project_id: Some(project_id),
                spec_id: Some(spec_id),
                task_ids: Vec::new(),
                view_id: None,
            },
            effects,
            request.request_id.as_deref(),
            request_digest,
        );
    }
    if let Action::SaveView {
        view_id,
        name,
        query,
        layout,
        group_by,
    } = &request.action
    {
        let id = super::query::save_view(
            &mut state,
            view_id.as_deref(),
            name.clone(),
            query.clone(),
            *layout,
            *group_by,
            &at,
        )?;
        return finish_mutation(
            store,
            request.expected_revision,
            state,
            MutationResult {
                response_id: None,
                revision: 0,
                project_id: None,
                spec_id: None,
                task_ids: vec![],
                view_id: Some(id),
            },
            super::sqlite::CommitEffects::default(),
            request.request_id.as_deref(),
            request_digest,
        );
    }
    if let Action::RemoveView { view_id } = &request.action {
        super::query::remove_view(&mut state, view_id)?;
        return finish_mutation(
            store,
            request.expected_revision,
            state,
            MutationResult {
                response_id: None,
                revision: 0,
                project_id: None,
                spec_id: None,
                task_ids: vec![],
                view_id: None,
            },
            super::sqlite::CommitEffects::default(),
            request.request_id.as_deref(),
            request_digest,
        );
    }
    let project_id;
    let mut spec_id = None;
    let mut response_id = None;
    let mut task_ids = vec![];
    let mut delta = BTreeMap::new();
    let mut effects = super::sqlite::CommitEffects::default();
    let scan_new_project = matches!(&request.action, Action::AddProject { .. });
    match super::tasks::expand_action(&state, request.action)? {
        Action::RespondQuestion {
            spec_id: id,
            question_id,
            question_digest,
            response_kind,
            answer,
            reason,
            impact,
            excluded_scope,
            review_at,
            review_when,
            review_utc_offset_minutes,
        } => {
            project_id = sources::spec(&state, &id)?.project_id.clone();
            super::agent::ensure_response_available(store, &id, &question_id)?;
            super::content::respond(
                &mut state,
                QuestionResponse {
                    id: String::new(),
                    spec_id: id.clone(),
                    question_id,
                    kind: response_kind,
                    answer,
                    reason,
                    impact,
                    excluded_scope,
                    review_at,
                    review_when,
                    review_utc_offset_minutes: Some(review_utc_offset_minutes),
                    question_digest,
                    created_at: String::new(),
                },
                &at,
            )?;
            response_id = state.question_responses.last().map(|r| r.id.clone());
            spec_id = Some(id);
        }
        Action::ConfirmContent {
            spec_id: id,
            scope,
            artifact_digest,
        } => {
            project_id = sources::spec(&state, &id)?.project_id.clone();
            let records = super::materials::Records::load(store)?;
            let (view, captured) = super::content::inspect_record(&state, &id, &records, true)?;
            let count = state.confirmations.len();
            super::content::confirm(&mut state, &id, scope, artifact_digest, &at, &view)?;
            if state.confirmations.len() > count {
                effects = super::artifacts::confirmation_effects(
                    store,
                    &state.confirmations[count],
                    captured.ok_or_else(|| invalid("确认材料无法保存"))?,
                )?;
            }
            spec_id = Some(id);
        }
        Action::ReviewMaterials { input } => {
            let id = input.spec_id.clone();
            project_id = sources::spec(&state, &id)?.project_id.clone();
            effects = super::materials::prepare_review(&state, store, input, &at)?;
            super::tasks::touch_spec(&mut state, &id, &at);
            spec_id = Some(id);
        }
        Action::RefreshSpec { spec_id: id } => {
            project_id = sources::spec(&state, &id)?.project_id.clone();
            super::registration::refresh(&mut state, &id, &at)?;
            spec_id = Some(id);
        }

        Action::SaveView { .. } | Action::RemoveView { .. } => {
            return Err(invalid("无法解析视图动作"));
        }
        Action::AddTasks {
            spec_id: id,
            drafts,
        } => {
            project_id = sources::spec(&state, &id)?.project_id.clone();
            task_ids = super::tasks::add_tasks(&mut state, &id, drafts, &at)?;
            spec_id = Some(id);
        }
        Action::EditTask { task_id, edit } => {
            let id = super::tasks::task(&state, &task_id)?.spec_id.clone();
            project_id = sources::spec(&state, &id)?.project_id.clone();
            super::tasks::edit_task(&mut state, &task_id, edit, &at)?;
            task_ids.push(task_id);
            spec_id = Some(id);
        }
        Action::SetStageRecord {
            spec_id: id,
            stage,
            progress,
            note,
            resources,
        } => {
            project_id = sources::spec(&state, &id)?.project_id.clone();
            super::lifecycle::set_stage_record(
                &mut state,
                StageRecord {
                    spec_id: id.clone(),
                    stage,
                    progress,
                    note,
                    resources,
                    updated_at: at.clone(),
                },
                &at,
            )?;
            spec_id = Some(id);
        }
        Action::SetWorkStatus { .. } | Action::SetWorkStage { .. } => {
            return Err(invalid("无法解析工作项动作"));
        }
        Action::AddProject { root_path, name } => {
            let root_path = project_root(&root_path, store.data_dir())?;
            check_project_unique(&state, &root_path, "")?;
            let name = nonempty(
                name.unwrap_or_else(|| {
                    Path::new(&root_path)
                        .file_name()
                        .unwrap_or_default()
                        .to_string_lossy()
                        .into_owned()
                }),
                "项目名",
            )?;
            let record = ProjectRecord {
                id: fs::unique_id()?,
                name,
                root_path,
                active: true,
                created_at: at.clone(),
                updated_at: at.clone(),
            };
            project_id = record.id.clone();
            delta = changes(None, &record)?;
            state.projects.push(record);
        }
        Action::UpdateProject {
            project_id: id,
            name,
            root_path,
            active,
        } => {
            let before = sources::project(&state, &id)?.clone();
            let root_path = if root_path == before.root_path {
                root_path
            } else {
                super::agent::ensure_roots_available(
                    store,
                    std::slice::from_ref(&before.root_path),
                )?;
                project_root(&root_path, store.data_dir())?
            };
            check_project_unique(&state, &root_path, &id)?;
            let mut record = ProjectRecord {
                name: nonempty(name, "项目名")?,
                root_path,
                active,
                ..before.clone()
            };
            delta = changes(Some(&before), &record)?;
            if !delta.is_empty() {
                record.updated_at = at.clone();
            }
            project_id = id;
            *state
                .projects
                .iter_mut()
                .find(|p| p.id == project_id)
                .ok_or_else(|| missing("项目不存在"))? = record;
        }
        Action::AddSpec {
            project_id: id,
            source_path,
        } => {
            let project = sources::project(&state, &id)?;
            if !project.active {
                return Err(invalid("请先恢复项目"));
            }
            let root = Root::open(Path::new(&project.root_path))?;
            let source_path = super::registration::main_source(&root, &source_path)?;
            check_source_unique(&state, &id, &source_path, "")?;
            let text = sources::text(&root, &source_path)?;
            root.revalidate()?;
            let title = sources::title(&text, &source_path);
            let record = super::registration::new_record(&id, source_path, title, &at)?;
            project_id = id;
            spec_id = Some(record.id.clone());
            delta = changes(None, &record)?;
            state.specs.push(record);
            super::registration::refresh(&mut state, spec_id.as_deref().expect("new spec"), &at)?;
        }
        Action::EditSpec {
            spec_id: id,
            source_path,
            title,
            stage,
            status,
            next_action,
            blocker,
            notes,
            resources,
            active,
        } => {
            let before = sources::spec(&state, &id)?.clone();
            let project = sources::project(&state, &before.project_id)?;
            if status == Status::Blocked && blocker.trim().is_empty() {
                return Err(invalid("阻塞状态必须填写阻塞说明"));
            }
            sources::markdown_path(&source_path)?;
            check_source_unique(&state, &before.project_id, &source_path, &id)?;
            validate_resources(&resources)?;
            if source_path != before.source_path {
                super::agent::ensure_source_path_available(store, &id)?;
                let root = Root::open(Path::new(&project.root_path))?;
                let main = super::registration::main_source(&root, &source_path)?;
                if fs::alias_key(&main) != fs::alias_key(&source_path) {
                    return Err(invalid("该路径为关联内容，请使用主规格路径"));
                }
                root.revalidate()?;
            }
            let mut record = SpecRecord {
                source_path,
                title: nonempty(title, "标题")?,
                stage,
                status,
                next_action,
                blocker,
                notes,
                resources,
                active,
                ..before.clone()
            };
            delta = changes(Some(&before), &record)?;
            if !delta.is_empty() {
                record.updated_at = at.clone();
            }
            project_id = record.project_id.clone();
            spec_id = Some(id.clone());
            *state
                .specs
                .iter_mut()
                .find(|s| s.id == id)
                .ok_or_else(|| missing("Spec 不存在"))? = record;
        }
        Action::VisitSpec { spec_id: id } => {
            let record = state
                .specs
                .iter_mut()
                .find(|s| s.id == id)
                .ok_or_else(|| missing("Spec 不存在"))?;
            project_id = record.project_id.clone();
            spec_id = Some(id);
            record.last_visited_at = Some(at.clone());
        }
        Action::SetLifecycleGoal { .. }
        | Action::ReportProgress { .. }
        | Action::ConfirmProgress { .. }
        | Action::SetProgressOverride { .. }
        | Action::ClearProgressOverride { .. }
        | Action::ReconcileProgress { .. } => unreachable!("progress actions are handled above"),
    }
    if !delta.is_empty() {
        state.history.push(ChangeEvent {
            id: fs::unique_id()?,
            task_id: None,
            lifecycle_stage: None,
            project_id: project_id.clone(),
            spec_id: spec_id.clone(),
            at: at.clone(),
            changes: delta,
        });
    }
    if scan_new_project {
        let project = sources::project(&state, &project_id)?.clone();
        super::registration::scan_project(&mut state, &project, &at, &Cancellation::new())?;
    }
    finish_mutation(
        store,
        request.expected_revision,
        state,
        MutationResult {
            response_id,
            revision: 0,
            project_id: Some(project_id),
            spec_id,
            task_ids,
            view_id: None,
        },
        effects,
        request.request_id.as_deref(),
        request_digest,
    )
}
