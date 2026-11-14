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
pub fn snapshot(state: &WorkbenchState, cancel: &Cancellation) -> Result<Snapshot> {
    let mut projects = vec![];
    let mut specs = vec![];
    let mut tasks = vec![];
    for project in &state.projects {
        cancel.check()?;
        let root = Root::open(Path::new(&project.root_path));
        projects.push(ProjectView {
            source_directories: ["docs/specs", "specs", ".work"]
                .into_iter()
                .filter(|path| {
                    root.as_ref().is_ok_and(|root| {
                        root.source_entries(RelPath::new(*path).ok().as_ref(), &mut 0, cancel)
                            .is_ok()
                    })
                })
                .map(str::to_owned)
                .collect(),
            record: project.clone(),
            available: root.is_ok(),
            message: root.as_ref().err().map(|e| e.message.clone()),
        });
        for s in state.specs.iter().filter(|s| s.project_id == project.id) {
            cancel.check()?;
            specs.push(sources::spec_view(s, &root));
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
fn project_root(path: &str, data: &Path) -> Result<String> {
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
pub fn mutate(store: &mut Store, request: MutationRequest) -> Result<MutationResult> {
    if request.expected_revision != store.state().revision {
        return Err(
            YssError::new(ErrorCode::ConcurrentChange, "内容已改变，请刷新后重新保存")
                .context("revision", store.state().revision),
        );
    }
    let mut state = store.state().clone();
    let at = now();
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
        let revision = if &state != store.state() {
            store.commit(request.expected_revision, state)?
        } else {
            state.revision
        };
        return Ok(MutationResult {
            revision,
            project_id: None,
            spec_id: None,
            task_ids: vec![],
            view_id: Some(id),
        });
    }
    if let Action::RemoveView { view_id } = &request.action {
        super::query::remove_view(&mut state, view_id)?;
        let revision = store.commit(request.expected_revision, state)?;
        return Ok(MutationResult {
            revision,
            project_id: None,
            spec_id: None,
            task_ids: vec![],
            view_id: None,
        });
    }
    let project_id;
    let mut spec_id = None;
    let mut task_ids = vec![];
    let mut delta = BTreeMap::new();
    match super::tasks::expand_action(&state, request.action)? {
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
            check_source_unique(&state, &id, &source_path, "")?;
            let root = Root::open(Path::new(&project.root_path))?;
            let text = sources::text(&root, &source_path)?;
            root.revalidate()?;
            let record = SpecRecord {
                id: fs::unique_id()?,
                project_id: id.clone(),
                title: sources::title(&text, &source_path),
                source_path,
                stage: Stage::Unmarked,
                status: Status::Unmarked,
                next_action: String::new(),
                blocker: String::new(),
                notes: String::new(),
                resources: vec![],
                active: true,
                created_at: at.clone(),
                updated_at: at.clone(),
                last_visited_at: None,
            };
            project_id = id;
            spec_id = Some(record.id.clone());
            delta = changes(None, &record)?;
            state.specs.push(record);
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
                let root = Root::open(Path::new(&project.root_path))?;
                sources::text(&root, &source_path)?;
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
    }
    if !delta.is_empty() {
        state.history.push(ChangeEvent {
            id: fs::unique_id()?,
            task_id: None,
            lifecycle_stage: None,
            project_id: project_id.clone(),
            spec_id: spec_id.clone(),
            at,
            changes: delta,
        });
    }
    let revision = if &state != store.state() {
        store.commit(request.expected_revision, state)?
    } else {
        state.revision
    };
    Ok(MutationResult {
        revision,
        project_id: Some(project_id),
        spec_id,
        task_ids,
        view_id: None,
    })
}
