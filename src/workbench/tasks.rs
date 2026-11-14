use super::{
    history::{self, EventTarget},
    model::*,
    service::validate_resources,
    sources::{self, invalid, missing},
};
use crate::{error::Result, fs};

pub fn active_spec<'a>(state: &'a WorkbenchState, id: &str) -> Result<&'a SpecRecord> {
    let spec = sources::spec(state, id)?;
    if !spec.active || !sources::project(state, &spec.project_id)?.active {
        return Err(invalid("请先恢复所属项目和 Spec"));
    }
    Ok(spec)
}
pub fn task<'a>(state: &'a WorkbenchState, id: &str) -> Result<&'a TaskRecord> {
    state
        .tasks
        .iter()
        .find(|t| t.id == id)
        .ok_or_else(|| missing("任务不存在"))
}
pub fn add_tasks(
    state: &mut WorkbenchState,
    spec_id: &str,
    drafts: Vec<TaskDraft>,
    at: &str,
) -> Result<Vec<String>> {
    let project_id = active_spec(state, spec_id)?.project_id.clone();
    if drafts.is_empty() || drafts.len() > 200 {
        return Err(invalid("一次须添加 1–200 项任务"));
    }
    for draft in &drafts {
        if draft.title.trim().is_empty() {
            return Err(invalid("任务标题不能为空"));
        }
        validate_resources(&draft.resources)?;
    }
    let mut ids = Vec::new();
    for draft in drafts {
        let record = TaskRecord {
            id: fs::unique_id()?,
            spec_id: spec_id.into(),
            title: draft.title.trim().into(),
            stage: draft.stage,
            status: Status::Todo,
            blocker: String::new(),
            notes: String::new(),
            resources: draft.resources,
            active: true,
            created_at: at.into(),
            updated_at: at.into(),
        };
        history::record_change(
            state,
            EventTarget {
                project_id: project_id.clone(),
                spec_id: Some(spec_id.into()),
                task_id: Some(record.id.clone()),
                lifecycle_stage: None,
            },
            None,
            &record,
            at,
        )?;
        ids.push(record.id.clone());
        state.tasks.push(record);
    }
    touch_spec(state, spec_id, at);
    Ok(ids)
}
pub fn edit_task(
    state: &mut WorkbenchState,
    task_id: &str,
    edit: TaskEdit,
    at: &str,
) -> Result<bool> {
    let before = task(state, task_id)?.clone();
    let project_id = active_spec(state, &before.spec_id)?.project_id.clone();
    if edit.title.trim().is_empty() {
        return Err(invalid("任务标题不能为空"));
    }
    if edit.status == Status::Blocked && edit.blocker.trim().is_empty() {
        return Err(invalid("阻塞任务必须填写说明"));
    }
    validate_resources(&edit.resources)?;
    let record = TaskRecord {
        title: edit.title.trim().into(),
        stage: edit.stage,
        status: edit.status,
        blocker: edit.blocker,
        notes: edit.notes,
        resources: edit.resources,
        active: edit.active,
        updated_at: at.into(),
        ..before.clone()
    };
    let changed = history::record_change(
        state,
        EventTarget {
            project_id,
            spec_id: Some(record.spec_id.clone()),
            task_id: Some(task_id.into()),
            lifecycle_stage: None,
        },
        Some(&before),
        &record,
        at,
    )?;
    if changed {
        touch_spec(state, &record.spec_id, at);
        *state
            .tasks
            .iter_mut()
            .find(|t| t.id == task_id)
            .ok_or_else(|| missing("任务不存在"))? = record;
    }
    Ok(changed)
}
pub fn touch_spec(state: &mut WorkbenchState, spec_id: &str, at: &str) {
    if let Some(spec) = state.specs.iter_mut().find(|s| s.id == spec_id) {
        spec.updated_at = at.into();
    }
}
impl From<&TaskRecord> for TaskEdit {
    fn from(t: &TaskRecord) -> Self {
        Self {
            title: t.title.clone(),
            stage: t.stage,
            status: t.status,
            blocker: t.blocker.clone(),
            notes: t.notes.clone(),
            resources: t.resources.clone(),
            active: t.active,
        }
    }
}

pub fn expand_action(state: &WorkbenchState, action: Action) -> Result<Action> {
    let (entity, id, status, stage) = match action {
        Action::SetWorkStatus {
            entity,
            id,
            status,
            blocker,
        } => (entity, id, Some((status, blocker)), None),
        Action::SetWorkStage { entity, id, stage } => (entity, id, None, Some(stage)),
        action => return Ok(action),
    };
    match entity {
        WorkEntity::Spec => {
            let s = active_spec(state, &id)?;
            let (status, blocker) = status.unwrap_or((s.status, s.blocker.clone()));
            Ok(Action::EditSpec {
                spec_id: id,
                title: s.title.clone(),
                source_path: s.source_path.clone(),
                stage: stage.unwrap_or(s.stage),
                status,
                blocker,
                notes: s.notes.clone(),
                next_action: s.next_action.clone(),
                resources: s.resources.clone(),
                active: s.active,
            })
        }
        WorkEntity::Task => {
            let t = task(state, &id)?;
            if !t.active {
                return Err(invalid("请先恢复任务"));
            }
            let mut edit = TaskEdit::from(t);
            if let Some((status, blocker)) = status {
                edit.status = status;
                edit.blocker = blocker;
            }
            if let Some(stage) = stage {
                edit.stage = stage;
            }
            Ok(Action::EditTask { task_id: id, edit })
        }
    }
}
