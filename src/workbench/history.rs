use super::{model::*, sources::invalid};
use crate::{error::Result, fs};
use serde::Serialize;
use std::collections::BTreeMap;
pub(super) fn changes<T: Serialize>(
    before: Option<&T>,
    after: &T,
) -> Result<BTreeMap<String, FieldChange>> {
    let before = before
        .map(serde_json::to_value)
        .transpose()
        .map_err(|_| invalid("变更无法编码"))?
        .unwrap_or_default();
    let after = serde_json::to_value(after).map_err(|_| invalid("变更无法编码"))?;
    let mut changes = BTreeMap::new();
    for (key, value) in after.as_object().ok_or_else(|| invalid("变更不是对象"))? {
        if [
            "id",
            "projectId",
            "specId",
            "createdAt",
            "updatedAt",
            "lastVisitedAt",
        ]
        .contains(&key.as_str())
        {
            continue;
        }
        let old = before.get(key).cloned().unwrap_or_default();
        if &old != value {
            changes.insert(
                key.clone(),
                FieldChange {
                    before: old,
                    after: value.clone(),
                },
            );
        }
    }
    Ok(changes)
}

pub struct EventTarget {
    pub project_id: String,
    pub spec_id: Option<String>,
    pub task_id: Option<String>,
    pub lifecycle_stage: Option<Stage>,
}
pub fn record_change<T: Serialize>(
    state: &mut WorkbenchState,
    target: EventTarget,
    before: Option<&T>,
    after: &T,
    at: &str,
) -> Result<bool> {
    let delta = changes(before, after)?;
    if delta.is_empty() {
        return Ok(false);
    }
    state.history.push(ChangeEvent {
        id: fs::unique_id()?,
        project_id: target.project_id,
        spec_id: target.spec_id,
        task_id: target.task_id,
        lifecycle_stage: target.lifecycle_stage,
        at: at.into(),
        changes: delta,
    });
    Ok(true)
}
