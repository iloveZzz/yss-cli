use super::{
    model::*,
    progress::{ProgressBasis, ProgressSummary},
    sources::{invalid, missing},
};
use crate::{error::Result, fs};
use std::{cmp::Reverse, collections::BTreeMap};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};

pub fn normalize_query(mut query: WorkQuery) -> Result<WorkQuery> {
    query.q = query.q.trim().into();
    if query.attention.iter().any(|value| {
        ![
            "any",
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
    if query.statuses.iter().copied().any(is_closed) {
        query.include_closed = true;
    }
    Ok(query)
}

pub(super) fn is_closed(status: Status) -> bool {
    matches!(status, Status::Done | Status::Canceled)
}

pub(super) fn matches_stages(
    primary: Stage,
    effective: Option<&ProgressSummary>,
    stages: &[Stage],
) -> bool {
    stages.is_empty()
        || stages.contains(&primary)
        || effective.is_some_and(|progress| {
            progress
                .active_stages
                .iter()
                .any(|stage| stages.contains(stage))
        })
}

fn broken(resources: &[ResourceView]) -> bool {
    resources.iter().any(|r| {
        !matches!(
            r.health,
            ResourceHealth::Available | ResourceHealth::External
        )
    })
}
pub(super) const REMINDER_KINDS: [&str; 5] =
    ["decision", "research", "overdue", "writeback", "ready"];

pub(super) fn matches_reminder(question: &QuestionView, kind: &str) -> bool {
    match (kind, question.state) {
        ("decision", QuestionState::Open | QuestionState::NeedsReview) => {
            question.question.kind == QuestionKind::Decision
        }
        ("research", QuestionState::Open | QuestionState::NeedsReview) => {
            question.question.kind == QuestionKind::Research
        }
        ("writeback", QuestionState::PendingWriteback) => true,
        ("overdue", QuestionState::Overdue | QuestionState::NeedsReview) => true,
        ("overdue", QuestionState::Deferred) => question
            .response
            .as_ref()
            .is_some_and(|response| !response.review_when.trim().is_empty()),
        _ => false,
    }
}

pub fn reminder_counts(content: &ContentView) -> BTreeMap<String, usize> {
    let mut counts: BTreeMap<String, usize> = REMINDER_KINDS
        .into_iter()
        .map(|kind| {
            (
                kind.into(),
                content
                    .questions
                    .iter()
                    .filter(|question| matches_reminder(question, kind))
                    .count(),
            )
        })
        .collect();
    if content.status == ContentStatus::Ready {
        counts.insert("ready".into(), 1);
    }
    counts
}
pub fn query(snapshot: &Snapshot, request: &WorkQuery) -> Result<QueryResult> {
    let mut result = filter_items(snapshot, request)?;
    for item in &mut result.items {
        if item.entity == WorkEntity::Spec {
            item.progress = snapshot.progress.get(&item.spec_id).cloned();
        }
    }
    Ok(result)
}

pub(super) fn filter_items(snapshot: &Snapshot, request: &WorkQuery) -> Result<QueryResult> {
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
            let effective = snapshot
                .progress
                .get(&s.id)
                .filter(|_| request.progress_basis == ProgressBasis::Effective);
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
                progress: None,
                content_status: Some(spec.content.status),
                reminder_counts: reminder_counts(&spec.content),
                needs_reconfirmation: spec.content.needs_reconfirmation,
                material_review: spec.content.material_review(),
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
                material_review: false,
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
        let progress = (item.entity == WorkEntity::Spec)
            .then(|| snapshot.progress.get(&item.spec_id))
            .flatten();
        let effective = progress.filter(|_| request.progress_basis == ProgressBasis::Effective);
        (match request.activity {
            ActivityScope::Active => item.active,
            ActivityScope::Inactive => !item.active,
            ActivityScope::All => true,
        }) && (request.include_closed
            || !is_closed(item.status)
            || item.status == Status::Done
                && item.entity == WorkEntity::Spec
                && (item.needs_reconfirmation
                    && (request.needs_reconfirmation
                        || request.reminder_kinds.iter().any(|kind| kind == "ready"))
                    || !request.attention.is_empty()))
            && (request.project_ids.is_empty() || request.project_ids.contains(&item.project_id))
            && (request.spec_ids.is_empty() || request.spec_ids.contains(&item.spec_id))
            && matches_stages(item.stage, effective, &request.stages)
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
                || progress.is_some_and(|progress| {
                    request.attention.iter().any(|value| {
                        if value == "any" {
                            !progress.attention.is_empty()
                        } else {
                            progress.attention.contains(value)
                        }
                    })
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
