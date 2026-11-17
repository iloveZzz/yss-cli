use super::{
    model::*,
    sources::{invalid, missing},
};
use crate::{error::Result, fs};
use std::{cmp::Reverse, collections::BTreeMap};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};

pub fn normalize_query(mut query: WorkQuery) -> Result<WorkQuery> {
    query.q = query.q.trim().into();
    if query.attention.iter().any(|value| {
        ![
            "awaitingConfirmation",
            "unverified",
            "needsSync",
            "needsReview",
            "conflict",
        ]
        .contains(&value.as_str())
    }) {
        return Err(invalid("进度关注条件无效"));
    }
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
pub fn reminder_counts(content: &ContentView) -> BTreeMap<String, usize> {
    let mut counts: BTreeMap<String, usize> =
        ["decision", "research", "overdue", "writeback", "ready"]
            .into_iter()
            .map(|kind| (kind.into(), 0))
            .collect();
    for question in &content.questions {
        let kind = match question.state {
            QuestionState::Open | QuestionState::NeedsReview => {
                Some(match question.question.kind {
                    QuestionKind::Decision => "decision",
                    QuestionKind::Research => "research",
                })
            }
            QuestionState::PendingWriteback => Some("writeback"),
            QuestionState::Overdue => Some("overdue"),
            QuestionState::Deferred
                if question
                    .response
                    .as_ref()
                    .is_some_and(|response| !response.review_when.trim().is_empty()) =>
            {
                Some("overdue")
            }
            _ => None,
        };
        if let Some(kind) = kind {
            *counts.entry(kind.into()).or_default() += 1;
        }
    }
    if content.status == ContentStatus::Ready {
        counts.insert("ready".into(), 1);
    }
    counts
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
            let progress = snapshot.progress.get(&s.id).cloned();
            let effective = progress
                .as_ref()
                .filter(|_| request.progress_basis == super::progress::ProgressBasis::Effective);
            items.push(WorkItem {
                entity: WorkEntity::Spec,
                id: s.id.clone(),
                project_id: s.project_id.clone(),
                project_name: project.name.clone(),
                spec_id: s.id.clone(),
                spec_title: s.title.clone(),
                source_path: s.source_path.clone(),
                title: s.title.clone(),
                stage: effective.map_or(s.stage, |value| value.primary_stage),
                status: effective.map_or(s.status, |value| value.status),
                next_action: effective
                    .map_or_else(|| s.next_action.clone(), |value| value.next_action.clone()),
                blocker: effective.map_or_else(|| s.blocker.clone(), |value| value.blocker.clone()),
                notes: s.notes.clone(),
                active: project.active && s.active,
                updated_at: effective
                    .and_then(|value| value.last_verified_at.clone())
                    .unwrap_or_else(|| s.updated_at.clone()),
                progress,
                content_status: Some(spec.content.status),
                reminder_counts: reminder_counts(&spec.content),
                needs_reconfirmation: spec.content.needs_reconfirmation,
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
                content_status: None,
                reminder_counts: BTreeMap::new(),
                needs_reconfirmation: false,
                progress: None,
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
            && (request.stages.is_empty()
                || request.stages.contains(&item.stage)
                || (request.progress_basis == super::progress::ProgressBasis::Effective
                    && item.progress.as_ref().is_some_and(|progress| {
                        progress
                            .active_stages
                            .iter()
                            .any(|stage| request.stages.contains(stage))
                    })))
            && (request.statuses.is_empty() || request.statuses.contains(&item.status))
            && (!request.source_problems_only || item.has_source_problem)
            && (request.content_statuses.is_empty()
                || item
                    .content_status
                    .is_some_and(|status| request.content_statuses.contains(&status)))
            && (request.reminder_kinds.is_empty()
                || request.reminder_kinds.iter().any(|kind| {
                    item.reminder_counts
                        .get(kind)
                        .is_some_and(|count| *count > 0)
                }))
            && (!request.needs_reconfirmation || item.needs_reconfirmation)
            && (request.attention.is_empty()
                || item.progress.as_ref().is_some_and(|progress| {
                    request
                        .attention
                        .iter()
                        .any(|value| progress.attention.contains(value))
                }))
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
