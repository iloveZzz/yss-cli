use super::{
    history::{self, EventTarget},
    model::*,
    service::validate_resources,
    sources::{invalid, missing},
    tasks,
};
use crate::error::Result;

pub fn set_stage_record(
    state: &mut WorkbenchState,
    mut record: StageRecord,
    at: &str,
) -> Result<bool> {
    let project_id = tasks::active_spec(state, &record.spec_id)?
        .project_id
        .clone();
    if record.stage == Stage::Unmarked {
        return Err(invalid("请选择具体阶段"));
    }
    if matches!(
        record.progress,
        StageProgress::Blocked | StageProgress::Skipped
    ) && record.note.trim().is_empty()
    {
        return Err(invalid("受阻或跳过阶段必须填写说明"));
    }
    validate_resources(&record.resources)?;
    let before = state
        .stage_records
        .iter()
        .find(|r| r.spec_id == record.spec_id && r.stage == record.stage)
        .cloned();
    record.updated_at = at.into();
    if before.is_none()
        && record.progress == StageProgress::Unmarked
        && record.note.is_empty()
        && record.resources.is_empty()
    {
        return Ok(false);
    }
    let changed = history::record_change(
        state,
        EventTarget {
            project_id,
            spec_id: Some(record.spec_id.clone()),
            task_id: None,
            lifecycle_stage: Some(record.stage),
        },
        before.as_ref(),
        &record,
        at,
    )?;
    if changed {
        tasks::touch_spec(state, &record.spec_id, at);
        if let Some(old) = state
            .stage_records
            .iter_mut()
            .find(|r| r.spec_id == record.spec_id && r.stage == record.stage)
        {
            *old = record;
        } else {
            state.stage_records.push(record);
        }
    }
    Ok(changed)
}
pub fn build_lifecycle(snapshot: &Snapshot, spec_id: &str) -> Result<LifecycleView> {
    let spec = snapshot
        .specs
        .iter()
        .find(|s| s.record.id == spec_id)
        .ok_or_else(|| missing("Spec 不存在"))?
        .clone();
    let tasks: Vec<_> = snapshot
        .tasks
        .iter()
        .filter(|t| t.record.spec_id == spec_id)
        .cloned()
        .collect();
    let stages = Stage::LIFECYCLE
        .into_iter()
        .map(|stage| {
            let mut counts = TaskCounts::default();
            for task in tasks
                .iter()
                .filter(|t| t.record.active && t.record.stage == stage)
            {
                counts.total += 1;
                match task.record.status {
                    Status::Unmarked => counts.unmarked += 1,
                    Status::Todo => counts.todo += 1,
                    Status::Doing => counts.doing += 1,
                    Status::Blocked => counts.blocked += 1,
                    Status::Done => counts.done += 1,
                    Status::Canceled => counts.canceled += 1,
                }
            }
            LifecycleStageView {
                stage,
                record: snapshot
                    .stage_records
                    .iter()
                    .find(|r| r.spec_id == spec_id && r.stage == stage)
                    .cloned(),
                task_counts: counts,
            }
        })
        .collect();
    Ok(LifecycleView {
        revision: snapshot.revision,
        spec,
        stages,
        tasks,
        events: snapshot
            .history
            .iter()
            .filter(|e| e.spec_id.as_deref() == Some(spec_id))
            .cloned()
            .collect(),
    })
}
