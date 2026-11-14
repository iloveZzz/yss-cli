use super::{
    history::{self, EventTarget},
    model::*,
    sources::{self, invalid},
};
use crate::{
    codec::{self, Limits},
    error::Result,
    fs::{self, Root},
    model::{RelPath, Sha256},
};
use std::{collections::BTreeSet, path::Path};

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
pub fn merged_resources(spec: &SpecRecord, meta: Option<&SpecMetadata>) -> Vec<ResourceRef> {
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
    today(response.review_utc_offset_minutes)?;
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
fn question_view(state: &WorkbenchState, spec_id: &str, q: &SpecQuestion) -> Result<QuestionView> {
    let digest = question_digest(q)?;
    let response = state
        .question_responses
        .iter()
        .rev()
        .find(|r| r.spec_id == spec_id && r.question_id == q.id)
        .cloned();
    let derived = match response.as_ref() {
        Some(r) if r.question_digest != digest => QuestionState::NeedsReview,
        Some(r) if q.applied_decision_id.as_deref() != Some(&r.id) => {
            QuestionState::PendingWriteback
        }
        Some(r)
            if r.kind == ResponseKind::Answer
                && q.status == QuestionStatus::Resolved
                && !q.resolution.trim().is_empty() =>
        {
            QuestionState::Resolved
        }
        Some(r)
            if r.kind == ResponseKind::Defer
                && q.status == QuestionStatus::Deferred
                && !q.resolution.trim().is_empty() =>
        {
            if q.blocking && r.excluded_scope.trim() != q.affected_scope.trim() {
                QuestionState::NeedsReview
            } else if !r.review_at.is_empty()
                && date(&r.review_at)? <= today(r.review_utc_offset_minutes)?
            {
                QuestionState::Overdue
            } else {
                QuestionState::Deferred
            }
        }
        Some(_) => QuestionState::PendingWriteback,
        None if q.kind == QuestionKind::Research
            && q.status == QuestionStatus::Resolved
            && !q.resolution.trim().is_empty() =>
        {
            QuestionState::Resolved
        }
        None => QuestionState::Open,
    };
    Ok(QuestionView {
        question: q.clone(),
        digest,
        state: derived,
        response,
    })
}
pub fn view(state: &WorkbenchState, spec: &SpecRecord, root: &Result<Root>) -> ContentView {
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
        let source = sources::text(root, &spec.source_path)?;
        let Some(meta) = metadata(&source)? else {
            view.blockers
                .push("主 Spec 尚未声明内容状态和确认范围".into());
            return Ok(());
        };
        view.scope = meta.scope.clone();
        view.status = ContentStatus::Draft;
        view.questions = meta
            .questions
            .iter()
            .map(|q| question_view(state, &spec.id, q))
            .collect::<Result<_>>()?;
        // A deleted question must not silently discard an unapplied user answer.
        for response in state
            .question_responses
            .iter()
            .filter(|r| r.spec_id == spec.id)
        {
            if !meta.questions.iter().any(|q| q.id == response.question_id) {
                view.blockers.push(format!(
                    "问题 {} 已从来源移除，须恢复并核对答复历史",
                    response.question_id
                ));
            }
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
        let resources = merged_resources(spec, Some(&meta));
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
                .push("内容仍为草稿，Agent 完成正文和关联产物后须标记 ready".into());
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
pub fn respond(state: &mut WorkbenchState, mut response: QuestionResponse, at: &str) -> Result<()> {
    let spec = super::tasks::active_spec(state, &response.spec_id)?.clone();
    let content = read_view(state, &spec.id)?;
    let question = content
        .questions
        .iter()
        .find(|q| q.question.id == response.question_id)
        .ok_or_else(|| invalid("问题不存在或元数据无效，请刷新来源"))?;
    if question.digest != response.question_digest {
        return Err(invalid("问题已变化，请刷新后重新答复"));
    }
    validate_response(&response)?;
    if response.kind == ResponseKind::Defer {
        if question.question.blocking
            && response.excluded_scope.trim() != question.question.affected_scope.trim()
        {
            return Err(invalid("关键问题延期须完整排除该问题的受影响范围"));
        }
        if !response.review_at.is_empty()
            && date(&response.review_at)? <= today(response.review_utc_offset_minutes)?
        {
            return Err(invalid("复查日期须晚于今天；已到期事项请补充答复"));
        }
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
pub fn agent_request(
    state: &WorkbenchState,
    spec_id: &str,
    data_dir: &Path,
) -> Result<AgentRequest> {
    let spec = sources::spec(state, spec_id)?;
    let project = sources::project(state, &spec.project_id)?;
    let source = sources::read_source(state, spec_id)?;
    let meta = metadata(&source.text)?;
    let content = read_view(state, spec_id)?;
    let resources = merged_resources(spec, meta.as_ref());
    let mut text = format!(
        "请继续更新此主 Spec 及关联产物。\n项目：{}\n主 Spec：{}\nSpec ID：{}\n范围：{}\n来源摘要：{}\n内容状态：{:?}\n\n答复不等于解决：先把决定落实到正文、AC、交互说明和原型，再把对应问题 status 写为 resolved，并以 appliedDecisionId 引用本次实际采用的回复 ID；不要仅改问题状态。延期只暂停受影响范围，保留影响、排除范围和复查日期/条件，写为 deferred，禁止伪装解决。事实问题自行调查并在 resolution 写明证据。关键业务决定仍未知时，当场向用户提问。不得代替用户写入个人确认。\n",
        project.root_path, spec.source_path, spec.id, content.scope, source.digest, content.status
    );
    text.push_str(&format!(
        "\n关联资源：\n{}\n",
        serde_json::to_string_pretty(&resources).map_err(|_| invalid("资源无法编码"))?
    ));
    text.push_str(&format!(
        "\n当前问题与最新答复（保留 ID、摘要与来源）：\n{}\n",
        serde_json::to_string_pretty(&content.questions).map_err(|_| invalid("问题无法编码"))?
    ));
    let quote = |value: &str| format!("'{}'", value.replace('\'', "'\\''"));
    text.push_str(&format!("\n待处理提醒：{}\n此工作台的所有后续命令须使用同一 --data-dir，避免写入其他个人记录。\n完成回写后刷新：yss workbench --data-dir {} register --root {} --source {} --json\n", content.blockers.join("；"), quote(&data_dir.to_string_lossy()), quote(&project.root_path), quote(&spec.source_path)));
    Ok(AgentRequest {
        spec_id: spec_id.into(),
        text,
        read_at: sources::now(),
    })
}
