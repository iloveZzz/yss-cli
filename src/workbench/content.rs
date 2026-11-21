use super::{
    history::{self, EventTarget},
    model::*,
    sources::{self, invalid},
};
use crate::{
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    fs::{self, Root},
    model::{RelPath, Sha256},
};
use std::{
    borrow::Cow,
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

pub fn metadata(text: &str) -> Result<Option<SpecMetadata>> {
    let marker = "<!-- yss-spec";
    let mut matches = text.match_indices(marker);
    let Some((start, _)) = matches.next() else {
        return Ok(None);
    };
    if matches.next().is_some() {
        return Err(invalid("主 Spec 只能有一个 yss-spec 元数据块"));
    }
    let json = &text[start + marker.len()..];
    let end = json
        .find("-->")
        .ok_or_else(|| invalid("yss-spec 元数据未闭合"))?;
    let meta: SpecMetadata = codec::decode_json(
        json[..end].trim().as_bytes(),
        Limits {
            bytes: MAX_SOURCE,
            ..Limits::manifest()
        },
    )?;
    if meta.schema_version != 1 || meta.scope.trim().is_empty() {
        return Err(invalid("yss-spec 版本或确认范围无效"));
    }
    super::service::validate_resources(&meta.resources)?;
    let mut ids = BTreeSet::new();
    for q in &meta.questions {
        if q.id.trim().is_empty()
            || q.title.trim().is_empty()
            || q.impact.trim().is_empty()
            || q.affected_scope.trim().is_empty()
            || !ids.insert(&q.id)
        {
            return Err(invalid("问题须有唯一 ID、标题、影响和受影响范围"));
        }
    }
    Ok(Some(meta))
}
pub fn resource_key(resource: &ResourceRef) -> String {
    format!(
        "{}:{}",
        if resource.target_type == TargetType::Local {
            "local"
        } else {
            "web"
        },
        resource.target
    )
}
fn merged_resources(spec: &SpecRecord, meta: Option<&SpecMetadata>) -> Vec<ResourceRef> {
    let mut resources: Vec<_> = spec
        .resources
        .iter()
        .filter(|resource| {
            !spec
                .source_resource_targets
                .contains(&resource_key(resource))
        })
        .cloned()
        .collect();
    let source_resources = meta
        .map(|m| &m.resources)
        .unwrap_or(&spec.inferred_resources);
    for resource in source_resources {
        if let Some(old) = resources
            .iter_mut()
            .find(|old| old.target_type == resource.target_type && old.target == resource.target)
        {
            *old = resource.clone();
        } else {
            resources.push(resource.clone());
        }
    }
    resources
}

pub fn resolved_resources(
    spec: &SpecRecord,
    meta: Option<&SpecMetadata>,
    root: Option<&Root>,
) -> Vec<ResourceRef> {
    let resources = merged_resources(spec, meta);
    match root {
        Some(root) => resolve_local_resources(root, &spec.source_path, resources),
        None => resources,
    }
}

pub(super) fn preview_target(
    spec: &SpecRecord,
    meta: Option<&SpecMetadata>,
    root: &Root,
    requested: &str,
) -> Option<String> {
    RelPath::new(requested).ok()?;
    let resources = resolved_resources(spec, meta, Some(root));
    let linked = |target: &str| {
        resources.iter().any(|resource| {
            resource.kind == ResourceKind::Prototype
                && resource.target_type == TargetType::Local
                && resource.target == target
        })
    };
    if linked(requested) {
        return Some(requested.into());
    }
    let built = super::preview::built_path(requested)?;
    (linked(&built) && super::preview::built_alternative(root, requested).as_ref() == Some(&built))
        .then_some(built)
}

pub(super) fn resolve_local_resources(
    root: &Root,
    source_path: &str,
    mut resources: Vec<ResourceRef>,
) -> Vec<ResourceRef> {
    let mut local_paths = BTreeSet::new();
    resources.retain_mut(|resource| {
        if resource.target_type != TargetType::Local {
            return true;
        }
        if resource.kind == ResourceKind::Prototype
            && let Some(built) = super::preview::built_alternative(root, &resource.target)
        {
            resource.target = built;
        }
        local_paths.insert(fs::alias_key(&resource.target))
    });

    let Some((directory, name)) = source_path.rsplit_once('/') else {
        return resources;
    };
    let Some(key) = name.strip_suffix("-spec.md").filter(|key| !key.is_empty()) else {
        return resources;
    };
    if directory.rsplit('/').next() != Some(key) {
        return resources;
    }
    let has_kind =
        |resources: &[ResourceRef], kind| resources.iter().any(|resource| resource.kind == kind);
    if !has_kind(&resources, ResourceKind::Interaction) {
        let target = format!("{directory}/{key}-interaction.md");
        if !local_paths.contains(&fs::alias_key(&target))
            && sources::text(root, &target)
                .and_then(|text| metadata(&text))
                .is_ok_and(|metadata| metadata.is_none())
        {
            resources.push(ResourceRef {
                kind: ResourceKind::Interaction,
                target_type: TargetType::Local,
                label: "交互说明".into(),
                target,
            });
        }
    }
    if !has_kind(&resources, ResourceKind::Prototype) {
        let built = format!("prototypes/{key}/dist/index.html");
        let source = format!("prototypes/{key}/index.html");
        let target = if super::preview::check_entry(root, &built).is_ok() {
            Some(built)
        } else if RelPath::new(&source)
            .and_then(|path| root.source_metadata(&path))
            .is_ok_and(|metadata| metadata.is_file())
        {
            Some(source)
        } else {
            None
        };
        if let Some(target) = target
            && !local_paths.contains(&fs::alias_key(&target))
        {
            resources.push(ResourceRef {
                kind: ResourceKind::Prototype,
                target_type: TargetType::Local,
                label: "高保真交互原型".into(),
                target,
            });
        }
    }
    resources
}

pub fn question_digest(q: &SpecQuestion) -> Result<String> {
    let value = serde_json::json!({"id":q.id,"title":q.title,"kind":q.kind,"impact":q.impact,"recommendation":q.recommendation,"blocking":q.blocking,"affectedScope":q.affected_scope});
    Ok(Sha256::of(&codec::encode_json(&value)?).as_str().into())
}
fn date(value: &str) -> Result<time::Date> {
    time::Date::parse(
        value,
        &time::format_description::parse_borrowed::<2>("[year]-[month]-[day]")
            .expect("date format"),
    )
    .map_err(|_| invalid("复查日期必须为 YYYY-MM-DD"))
}
fn today(offset: i16) -> Result<time::Date> {
    if !(-840..=840).contains(&offset) {
        return Err(invalid("日期时区偏移须在 -840 至 840 分钟之间"));
    }
    let zone = time::UtcOffset::from_whole_seconds(i32::from(offset) * 60)
        .map_err(|_| invalid("日期时区无效"))?;
    Ok(time::OffsetDateTime::now_utc().to_offset(zone).date())
}
pub fn validate_response(response: &QuestionResponse) -> Result<()> {
    if let Some(offset) = response.review_utc_offset_minutes {
        today(offset)?;
    }
    match response.kind {
        ResponseKind::Answer if response.answer.trim().is_empty() => {
            return Err(invalid("请填写答复或补充内容"));
        }
        ResponseKind::Defer => {
            if response.reason.trim().is_empty()
                || response.impact.trim().is_empty()
                || response.excluded_scope.trim().is_empty()
                || (response.review_at.trim().is_empty() && response.review_when.trim().is_empty())
            {
                return Err(invalid("延期须填写原因、影响、排除范围以及复查日期或条件"));
            }
            if !response.review_at.is_empty() {
                date(&response.review_at)?;
            }
        }
        _ => {}
    }
    Ok(())
}
fn review_reason(
    question: &SpecQuestion,
    digest: &str,
    response: &QuestionResponse,
) -> Result<Option<(QuestionState, String)>> {
    if response.question_digest != digest {
        return Ok(Some((
            QuestionState::NeedsReview,
            "问题内容已变化，原答复只适用于原问题摘要。".into(),
        )));
    }
    if response.kind == ResponseKind::Defer {
        if question.blocking && response.excluded_scope.trim() != question.affected_scope.trim() {
            return Ok(Some((
                QuestionState::NeedsReview,
                "原延期的排除范围未覆盖当前关键问题。".into(),
            )));
        }
        if !response.review_at.is_empty() {
            let Some(offset) = response.review_utc_offset_minutes else {
                return Ok(Some((
                    QuestionState::NeedsReview,
                    "原延期未记录复查日期的时区，需核对是否到期。".into(),
                )));
            };
            if date(&response.review_at)? <= today(offset)? {
                return Ok(Some((
                    QuestionState::Overdue,
                    format!("原延期的复查日期 {} 已到。", response.review_at),
                )));
            }
        }
        if !response.review_when.trim().is_empty()
            && question.status == QuestionStatus::Open
            && question.applied_decision_id.as_ref() == Some(&response.id)
        {
            return Ok(Some((
                QuestionState::NeedsReview,
                "原规格已重新打开这项条件延期，请核对复查条件。".into(),
            )));
        }
    }
    Ok(None)
}

pub(super) fn question_view(
    q: &SpecQuestion,
    records: Vec<&QuestionResponse>,
) -> Result<QuestionView> {
    let digest = question_digest(q)?;
    let mut view = QuestionView {
        question: q.clone(),
        digest,
        state: if q.kind == QuestionKind::Research
            && q.status == QuestionStatus::Resolved
            && !q.resolution.trim().is_empty()
        {
            QuestionState::Resolved
        } else {
            QuestionState::Open
        },
        response: None,
        history: Vec::new(),
    };
    for (index, record) in records.into_iter().rev().enumerate() {
        let review = review_reason(q, &view.digest, record)?;
        if index == 0 && review.is_none() {
            view.state = match (record.kind, q.status) {
                _ if q.applied_decision_id.as_ref() != Some(&record.id)
                    || q.resolution.trim().is_empty() =>
                {
                    QuestionState::PendingWriteback
                }
                (ResponseKind::Answer, QuestionStatus::Resolved) => QuestionState::Resolved,
                (ResponseKind::Defer, QuestionStatus::Deferred) => QuestionState::Deferred,
                _ => QuestionState::PendingWriteback,
            };
            view.response = Some(record.clone());
        } else {
            let reason = match review {
                Some((state, reason)) => {
                    if index == 0 {
                        view.state = state;
                    }
                    reason
                }
                None => "已有更新的个人答复，此记录仅供追溯。".into(),
            };
            view.history.push(HistoricalResponse {
                response: record.clone(),
                reason,
                source_resolution: (q.applied_decision_id.as_ref() == Some(&record.id)
                    && !q.resolution.trim().is_empty())
                .then(|| q.resolution.clone()),
            });
        }
    }
    Ok(view)
}
pub fn view(state: &WorkbenchState, spec: &SpecRecord, root: &Result<Root>) -> ContentView {
    view_with_source(state, spec, root, None)
}

fn view_with_source(
    state: &WorkbenchState,
    spec: &SpecRecord,
    root: &Result<Root>,
    source: Option<&str>,
) -> ContentView {
    let confirmations: Vec<_> = state
        .confirmations
        .iter()
        .filter(|c| c.spec_id == spec.id)
        .cloned()
        .collect();
    let mut view = ContentView {
        status: ContentStatus::Unmarked,
        scope: String::new(),
        artifact_digest: None,
        metadata_error: None,
        questions: vec![],
        confirmations,
        can_confirm: false,
        blockers: vec![],
        needs_reconfirmation: false,
    };
    let read = (|| -> Result<()> {
        let root = root.as_ref().map_err(Clone::clone)?;
        let source = match source {
            Some(text) => Cow::Borrowed(text),
            None => Cow::Owned(sources::text(root, &spec.source_path)?),
        };
        let Some(meta) = metadata(&source)? else {
            view.blockers
                .push("主 Spec 尚未声明内容状态和确认范围".into());
            return Ok(());
        };
        view.scope = meta.scope.clone();
        view.status = ContentStatus::Draft;
        let mut records = BTreeMap::<&str, Vec<&QuestionResponse>>::new();
        for response in state
            .question_responses
            .iter()
            .filter(|r| r.spec_id == spec.id)
        {
            records
                .entry(&response.question_id)
                .or_default()
                .push(response);
        }
        view.questions = meta
            .questions
            .iter()
            .map(|q| question_view(q, records.remove(q.id.as_str()).unwrap_or_default()))
            .collect::<Result<_>>()?;
        // A deleted question must not silently discard an unapplied user answer.
        for question_id in records.keys() {
            view.blockers.push(format!(
                "问题 {question_id} 已从来源移除，须恢复并核对答复历史"
            ));
        }
        for question in &view.questions {
            match question.state {
                QuestionState::Resolved => {}
                QuestionState::Deferred => {
                    if let Some(r) = &question.response {
                        view.scope.push_str(&format!(
                            "；本次排除：{}（{}）",
                            r.excluded_scope, question.question.id
                        ));
                    }
                }
                _ => view.blockers.push(format!(
                    "{}：{}（{}）",
                    question.question.id,
                    question.question.title,
                    match question.state {
                        QuestionState::PendingWriteback => "答复待 Agent 回写",
                        QuestionState::Overdue => "延期已到复查日期",
                        QuestionState::NeedsReview => "来源变化或排除范围待核对",
                        _ => "待处理",
                    }
                )),
            }
        }
        let resources = resolved_resources(spec, Some(&meta), Some(root));
        // shortcut: 字节摘要会将纯排版变化视为新版本；需要区分时再引入经过验证的语义归一化。
        let mut artifacts = vec![(
            spec.source_path.clone(),
            Sha256::of(source.as_bytes()).as_str().to_owned(),
        )];
        for resource in resources {
            if resource.target_type == TargetType::Web {
                view.blockers.push(format!(
                    "{}为外部链接，请关联本地快照以锁定确认版本",
                    resource.label
                ));
                continue;
            }
            let digest = if resource.kind == ResourceKind::Prototype {
                super::preview::artifact_digest(root, &resource.target)?
            } else {
                let file = root.source_read(&RelPath::new(&resource.target)?, MAX_STATE)?;
                Sha256::of(&file.bytes).as_str().to_owned()
            };
            artifacts.push((resource.target, digest));
        }
        root.revalidate()?;
        artifacts.sort();
        view.artifact_digest = Some(
            Sha256::of(&codec::encode_json(&(view.scope.clone(), artifacts))?)
                .as_str()
                .into(),
        );
        if meta.content_status != SourceContentStatus::Ready {
            view.blockers
                .push("整份内容仍为草稿，核对其全部正文及关联产物后可标记 ready".into());
        }
        view.can_confirm = view.blockers.is_empty();
        let confirmed = view.can_confirm
            && view.confirmations.iter().any(|c| {
                c.scope == view.scope && Some(&c.artifact_digest) == view.artifact_digest.as_ref()
            });
        view.needs_reconfirmation = !view.confirmations.is_empty() && !confirmed;
        view.status = if confirmed {
            ContentStatus::Confirmed
        } else if view.can_confirm {
            ContentStatus::Ready
        } else {
            ContentStatus::Draft
        };
        Ok(())
    })();
    if let Err(error) = read {
        view.metadata_error = Some(error.message.clone());
        view.blockers.push(error.message);
        view.can_confirm = false;
        view.status = ContentStatus::Draft;
        view.needs_reconfirmation = !view.confirmations.is_empty();
    }
    view
}
pub fn read_view(state: &WorkbenchState, spec_id: &str) -> Result<ContentView> {
    let spec = sources::spec(state, spec_id)?;
    let project = sources::project(state, &spec.project_id)?;
    Ok(view(
        state,
        spec,
        &Root::open(Path::new(&project.root_path)),
    ))
}
fn response_question(source: &str, response: &QuestionResponse) -> Result<SpecQuestion> {
    let question = metadata(source)?
        .and_then(|meta| {
            meta.questions
                .into_iter()
                .find(|q| q.id == response.question_id)
        })
        .ok_or_else(|| {
            YssError::new(
                ErrorCode::ConcurrentChange,
                "原问题已从来源移除，请核对当前主 Spec",
            )
        })?;
    if question_digest(&question)? != response.question_digest {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "问题已变化，请核对新版后重新保存",
        ));
    }
    Ok(question)
}

pub fn respond(state: &mut WorkbenchState, mut response: QuestionResponse, at: &str) -> Result<()> {
    let spec = super::tasks::active_spec(state, &response.spec_id)?.clone();
    let source = sources::read_source(state, &spec.id)?;
    let question = response_question(&source.text, &response)?;
    // New submissions use the API's default UTC offset. Historical records keep an absent offset.
    let offset = *response.review_utc_offset_minutes.get_or_insert(0);
    validate_response(&response)?;
    if response.kind == ResponseKind::Defer {
        if question.blocking && response.excluded_scope.trim() != question.affected_scope.trim() {
            return Err(invalid("关键问题延期须完整排除该问题的受影响范围"));
        }
        if !response.review_at.is_empty() && date(&response.review_at)? <= today(offset)? {
            return Err(invalid("复查日期须晚于今天；已到期事项请补充答复"));
        }
    }
    // A source editor is independent of the workbench lock. Recheck after parsing, close to the write.
    let latest = sources::read_source(state, &spec.id)?;
    if latest.digest != source.digest {
        response_question(&latest.text, &response)?;
    }
    response.id = fs::unique_id()?;
    response.created_at = at.into();
    history::record_change(
        state,
        EventTarget {
            project_id: spec.project_id,
            spec_id: Some(spec.id.clone()),
            task_id: None,
            lifecycle_stage: None,
        },
        None,
        &response,
        at,
    )?;
    state.question_responses.push(response);
    super::tasks::touch_spec(state, &spec.id, at);
    Ok(())
}
pub fn confirm(
    state: &mut WorkbenchState,
    spec_id: &str,
    scope: String,
    artifact_digest: String,
    at: &str,
) -> Result<()> {
    let spec = super::tasks::active_spec(state, spec_id)?.clone();
    let content = read_view(state, spec_id)?;
    if !content.can_confirm {
        return Err(invalid(&format!(
            "当前范围不能确认：{}",
            content.blockers.join("；")
        )));
    }
    if content.scope != scope || content.artifact_digest.as_deref() != Some(&artifact_digest) {
        return Err(invalid("来源或确认范围已改变，请刷新并核对新版本"));
    }
    if content
        .confirmations
        .iter()
        .any(|record| record.scope == scope && record.artifact_digest == artifact_digest)
    {
        return Ok(());
    }
    let record = ConfirmationRecord {
        id: fs::unique_id()?,
        spec_id: spec_id.into(),
        scope,
        artifact_digest,
        created_at: at.into(),
    };
    history::record_change(
        state,
        EventTarget {
            project_id: spec.project_id,
            spec_id: Some(spec_id.into()),
            task_id: None,
            lifecycle_stage: None,
        },
        None,
        &record,
        at,
    )?;
    state.confirmations.push(record);
    Ok(())
}
pub(super) fn source_status_label(meta: Option<&SpecMetadata>) -> &'static str {
    match meta.map(|m| m.content_status) {
        Some(SourceContentStatus::Ready) => "ready",
        Some(SourceContentStatus::Draft) => "draft",
        Some(SourceContentStatus::Unspecified) => "字段缺省（按 draft 读取）",
        None => "未声明 yss-spec 元数据",
    }
}
pub fn agent_request(
    state: &WorkbenchState,
    spec_id: &str,
    registration_dir: Option<&Path>,
) -> Result<AgentRequest> {
    let spec = sources::spec(state, spec_id)?;
    let project = sources::project(state, &spec.project_id)?;
    let source = sources::read_source(state, spec_id)?;
    let meta = metadata(&source.text)?;
    let root = Root::open(Path::new(&project.root_path));
    let content = view_with_source(state, spec, &root, Some(&source.text));
    let root = root?;
    let resources = resolved_resources(spec, meta.as_ref(), Some(&root));
    let mut text = format!(
        concat!(
            "请按本次任务的授权继续处理此主 Spec 及相关产物。\n项目：{}\n主 Spec：{}\nSpec ID：{}\n范围：{}\n来源摘要：{}\n",
            "本次读取的主 Spec 文件 contentStatus：{}\n工作台个人汇总状态：{:?}（综合问题、回写和个人确认）\n\n",
            "先把决定落实到相关正文、AC、交互说明和原型，再将对应问题的 status 写为 resolved，以 appliedDecisionId 引用实际采用的答复 ID。",
            "延期只暂停受影响范围，写为 deferred 并保留原因、影响、排除范围及复查日期或条件。",
            "可查事实自行调查，在 resolution 写明证据。任务范围内仍缺关键业务裁决时，向用户提问。",
            "局部回写保持整份 Spec 原有的 contentStatus。只有任务覆盖整份且核对完整正文与关联产物后，才可将其标记为 ready。个人内容确认由用户另行操作。\n"
        ),
        project.root_path,
        spec.source_path,
        spec.id,
        content.scope,
        source.digest,
        source_status_label(meta.as_ref()),
        content.status
    );
    text.push_str(&format!(
        "\n关联资源：\n{}\n",
        serde_json::to_string_pretty(&resources).map_err(|_| invalid("资源无法编码"))?
    ));
    let mut current = Vec::new();
    let mut history = Vec::new();
    for item in content.questions {
        let mut question = item.question;
        let usage = match item.state {
            QuestionState::PendingWriteback
                if item
                    .response
                    .as_ref()
                    .is_some_and(|r| r.kind == ResponseKind::Defer) =>
            {
                "仅回写有效延期安排"
            }
            QuestionState::PendingWriteback => "当前可回写",
            QuestionState::Resolved if item.response.is_none() => "原规格已有研究结论，引用其依据",
            QuestionState::Resolved | QuestionState::Deferred => "已回写，仅供引用",
            QuestionState::Overdue | QuestionState::NeedsReview => "待复查，没有当前有效答复",
            QuestionState::Open => "待答，没有当前有效答复",
        };
        history.extend(item.history);
        if !matches!(
            item.state,
            QuestionState::Resolved | QuestionState::Deferred
        ) {
            // An earlier writeback may still be present in the source after its question changed.
            question.resolution.clear();
            question.applied_decision_id = None;
        }
        current.push(AgentQuestion {
            question,
            digest: item.digest,
            state: item.state,
            response: item.response,
            usage: usage.into(),
        });
    }
    let question_ids: BTreeSet<_> = meta
        .iter()
        .flat_map(|m| &m.questions)
        .map(|q| q.id.as_str())
        .collect();
    for response in state
        .question_responses
        .iter()
        .rev()
        .filter(|r| r.spec_id == spec_id && !question_ids.contains(r.question_id.as_str()))
    {
        history.push(HistoricalResponse {
            response: response.clone(),
            reason: "主 Spec 已无此问题，请复查来源。".into(),
            source_resolution: None,
        });
    }
    text.push_str(concat!(
        "\n回写前核对任务范围、原条款、当前问题 ID、摘要和答复 ID。常规续办采用范围内仍待回写的有效答复，已有回写结论直接引用并跳过该次回写。",
        "保留原问题的题意、影响及字段结构，更新 status、resolution 和 appliedDecisionId。",
        "个人延期的原因、影响、排除范围、复查日期及时区或条件写入 resolution 和相关正文。推荐答案是待选择的建议。",
        "延期到达当地复查日期即待复查。按条件复查的延期须核实触发事实，确认触发后在授权范围内记录原问题的实际影响并重开，保留原采纳记录。\n\n",
        "任务明确授权修订业务规则或采纳替换决定时，按原 FR、AC 和前置问题查证适用范围。",
        "若权限、时点、结果或适用范围的变化使原问题前提失效，保留问题 ID，在原题意或影响中写明实际变化，按需要将原问题重开待复查。",
        "核对其历史答复与受影响的验收。任务只要求核对的独立问题、答复及条款维持原状。\n"
    ));
    text.push_str(&format!(
        "\n当前问题与最新答复（保留 ID、摘要与来源）：\n{}\n",
        serde_json::to_string_pretty(&current).map_err(|_| invalid("问题无法编码"))?
    ));
    text.push_str(&format!("\n待处理提醒：{}\n", content.blockers.join("；")));
    text.push_str("复查资料保留历史原文。写入原 Spec 的反例按当前已确认条款表述。任务明确要求记录决定沿革时，注明历史决定的版本和用途。\n");
    text.push_str(&format!(
        "\n历史原文（仅供复查，不可回写）：\n{}\n",
        serde_json::to_string_pretty(&history).map_err(|_| invalid("答复历史无法编码"))?
    ));
    if let Some(data_dir) = registration_dir {
        let quote = |value: &str| format!("'{}'", value.replace('\'', "'\\''"));
        text.push_str(&format!("此工作台的所有后续命令须使用同一 --data-dir，避免写入其他个人记录。\n完成回写后刷新：yss workbench --data-dir {} register --root {} --source {} --json\n", quote(&data_dir.to_string_lossy()), quote(&project.root_path), quote(&spec.source_path)));
    }
    Ok(AgentRequest {
        spec_id: spec_id.into(),
        project_root: project.root_path.clone(),
        source_path: source.source_path,
        data_dir: registration_dir.map(|dir| dir.to_string_lossy().into_owned()),
        text,
        read_at: sources::now(),
        questions: current,
        history,
    })
}
