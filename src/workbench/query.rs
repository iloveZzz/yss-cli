use super::{
    model::*,
    sources::{invalid, missing},
};
use crate::{error::Result, fs};
use std::{cmp::Reverse, collections::BTreeMap};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};

pub fn normalize_query(mut query: WorkQuery) -> Result<WorkQuery> {
    query.q = query.q.trim().into();
    if query
        .statuses
        .iter()
        .any(|s| matches!(s, Status::Done | Status::Canceled))
    {
        query.include_closed = true;
    }
    Ok(query)
}
fn broken(resources: &[ResourceView]) -> bool {
    resources.iter().any(|r| {
        !matches!(
            r.health,
            ResourceHealth::Available | ResourceHealth::External
        )
    })
}
pub fn query(snapshot: &Snapshot, request: &WorkQuery) -> Result<QueryResult> {
    let request = normalize_query(request.clone())?;
    let projects: BTreeMap<_, _> = snapshot
        .projects
        .iter()
        .map(|p| (&p.record.id, &p.record))
        .collect();
    let specs: BTreeMap<_, _> = snapshot.specs.iter().map(|s| (&s.record.id, s)).collect();
    let mut items = Vec::new();
    for spec in &snapshot.specs {
        let s = &spec.record;
        let Some(project) = projects.get(&s.project_id) else {
            continue;
        };
        if request.entity == WorkEntity::Spec {
            items.push(WorkItem {
                entity: WorkEntity::Spec,
                id: s.id.clone(),
                project_id: s.project_id.clone(),
                project_name: project.name.clone(),
                spec_id: s.id.clone(),
                spec_title: s.title.clone(),
                source_path: s.source_path.clone(),
                title: s.title.clone(),
                stage: s.stage,
                status: s.status,
                next_action: s.next_action.clone(),
                blocker: s.blocker.clone(),
                notes: s.notes.clone(),
                active: project.active && s.active,
                updated_at: s.updated_at.clone(),
                has_source_problem: spec.source_health != SourceHealth::Available
                    || broken(&spec.resources),
            });
        }
    }
    if request.entity == WorkEntity::Task {
        for task in &snapshot.tasks {
            let t = &task.record;
            let Some(spec) = specs.get(&t.spec_id) else {
                continue;
            };
            let s = &spec.record;
            let Some(project) = projects.get(&s.project_id) else {
                continue;
            };
            items.push(WorkItem {
                entity: WorkEntity::Task,
                id: t.id.clone(),
                project_id: s.project_id.clone(),
                project_name: project.name.clone(),
                spec_id: s.id.clone(),
                spec_title: s.title.clone(),
                source_path: s.source_path.clone(),
                title: t.title.clone(),
                stage: t.stage,
                status: t.status,
                next_action: String::new(),
                blocker: t.blocker.clone(),
                notes: t.notes.clone(),
                active: project.active && s.active && t.active,
                updated_at: t.updated_at.clone(),
                has_source_problem: spec.source_health != SourceHealth::Available
                    || broken(&spec.resources)
                    || broken(&task.resources),
            });
        }
    }
    let needle = request.q.to_lowercase();
    items.retain(|item| {
        (match request.activity {
            ActivityScope::Active => item.active,
            ActivityScope::Inactive => !item.active,
            ActivityScope::All => true,
        }) && (request.include_closed || !matches!(item.status, Status::Done | Status::Canceled))
            && (request.project_ids.is_empty() || request.project_ids.contains(&item.project_id))
            && (request.spec_ids.is_empty() || request.spec_ids.contains(&item.spec_id))
            && (request.stages.is_empty() || request.stages.contains(&item.stage))
            && (request.statuses.is_empty() || request.statuses.contains(&item.status))
            && (!request.source_problems_only || item.has_source_problem)
            && (needle.is_empty()
                || [
                    &item.title,
                    &item.project_name,
                    &item.spec_title,
                    &item.source_path,
                    &item.next_action,
                    &item.blocker,
                    &item.notes,
                ]
                .iter()
                .any(|value| value.to_lowercase().contains(&needle)))
    });
    match request.sort {
        WorkSort::UpdatedDesc => items.sort_by_cached_key(|item| {
            // 旧记录的无效时间排在末尾，仍保留可见。
            (
                Reverse(OffsetDateTime::parse(&item.updated_at, &Rfc3339).ok()),
                item.id.clone(),
            )
        }),
        WorkSort::TitleAsc => items.sort_by(|a, b| {
            a.title
                .to_lowercase()
                .cmp(&b.title.to_lowercase())
                .then(a.id.cmp(&b.id))
        }),
    }
    Ok(QueryResult {
        revision: snapshot.revision,
        read_at: snapshot.read_at.clone(),
        total: items.len(),
        items,
    })
}
pub fn save_view(
    state: &mut WorkbenchState,
    view_id: Option<&str>,
    name: String,
    query: WorkQuery,
    layout: Layout,
    group_by: GroupBy,
    at: &str,
) -> Result<String> {
    let name = name.trim().to_owned();
    if name.is_empty() || name.chars().count() > 80 {
        return Err(invalid("视图名称须为 1–80 个字符"));
    }
    let query = normalize_query(query)?;
    if let Some(id) = view_id {
        let view = state
            .saved_views
            .iter_mut()
            .find(|v| v.id == id)
            .ok_or_else(|| missing("保存视图不存在"))?;
        if view.name != name
            || view.query != query
            || view.layout != layout
            || view.group_by != group_by
        {
            view.name = name;
            view.query = query;
            view.layout = layout;
            view.group_by = group_by;
            view.updated_at = at.into();
        }
        return Ok(id.into());
    }
    let id = fs::unique_id()?;
    state.saved_views.push(SavedView {
        id: id.clone(),
        name,
        query,
        layout,
        group_by,
        created_at: at.into(),
        updated_at: at.into(),
    });
    Ok(id)
}
pub fn remove_view(state: &mut WorkbenchState, view_id: &str) -> Result<bool> {
    let index = state
        .saved_views
        .iter()
        .position(|v| v.id == view_id)
        .ok_or_else(|| missing("保存视图不存在"))?;
    state.saved_views.remove(index);
    Ok(true)
}
