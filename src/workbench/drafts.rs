use super::{model::*, sources, store::Store};
use crate::{
    error::{ErrorCode, Result, YssError},
    model::Sha256,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Draft {
    pub id: String,
    pub editor_session_id: String,
    pub target_type: String,
    pub target_id: String,
    pub base_token: String,
    pub base: Value,
    pub payload: Value,
    pub revision: u64,
    pub updated_at: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SaveDraft {
    pub expected_revision: u64,
    pub editor_session_id: String,
    pub target_type: String,
    pub target_id: String,
    pub base_token: String,
    pub base: Value,
    pub payload: Value,
}

fn selected_fields(value: &impl Serialize, keys: &[&str]) -> Result<Value> {
    let source = serde_json::to_value(value).map_err(|_| sources::invalid("编辑内容编码失败"))?;
    Ok(Value::Object(
        keys.iter()
            .map(|key| {
                (
                    (*key).into(),
                    source.get(*key).cloned().unwrap_or(Value::Null),
                )
            })
            .collect(),
    ))
}

pub fn target(state: &WorkbenchState, action: &Action) -> Result<Value> {
    let mut question_view = None;
    let (values, guard) = match action {
        Action::EditSpec { spec_id, .. }
        | Action::SetWorkStatus {
            entity: WorkEntity::Spec,
            id: spec_id,
            ..
        }
        | Action::SetWorkStage {
            entity: WorkEntity::Spec,
            id: spec_id,
            ..
        } => {
            let spec = sources::spec(state, spec_id)?;
            let values = selected_fields(
                spec,
                &[
                    "sourcePath",
                    "title",
                    "stage",
                    "status",
                    "nextAction",
                    "blocker",
                    "notes",
                    "resources",
                    "active",
                ],
            )?;
            let project = sources::project(state, &spec.project_id)?;
            (
                values,
                json!({"projectActive":project.active,"projectRoot":project.root_path}),
            )
        }
        Action::RespondQuestion {
            spec_id,
            question_id,
            ..
        } => {
            let content = super::content::read_view(state, spec_id)?;
            let question = content
                .questions
                .into_iter()
                .find(|item| item.question.id == *question_id)
                .ok_or_else(|| sources::invalid("问题不存在"))?;
            let response = question.response.as_ref();
            let values = json!({"responseKind":response.map(|value|value.kind).unwrap_or(ResponseKind::Answer),"answer":response.map(|value|value.answer.as_str()).unwrap_or(""),"reason":response.map(|value|value.reason.as_str()).unwrap_or(""),"impact":response.map(|value|value.impact.as_str()).unwrap_or(""),"excludedScope":response.map(|value|value.excluded_scope.as_str()).unwrap_or(""),"reviewAt":response.map(|value|value.review_at.as_str()).unwrap_or(""),"reviewWhen":response.map(|value|value.review_when.as_str()).unwrap_or(""),"reviewUtcOffsetMinutes":response.and_then(|value|value.review_utc_offset_minutes).unwrap_or(0)});
            let guard = json!({"questionDigest":question.digest,"responseId":response.map(|value|&value.id)});
            question_view = Some(question);
            (values, guard)
        }
        Action::SetStageRecord { spec_id, stage, .. } => {
            let spec = sources::spec(state, spec_id)?;
            let existing = state
                .stage_records
                .iter()
                .find(|record| record.spec_id == *spec_id && record.stage == *stage);
            let values=existing.map(|record|json!({"specId":spec_id,"stage":stage,"progress":record.progress,"note":record.note,"resources":record.resources})).unwrap_or_else(||json!({"specId":spec_id,"stage":stage,"progress":"unmarked","note":"","resources":[]}));
            (values, json!({"active":spec.active}))
        }
        _ => return Err(sources::invalid("此操作采用工作台版本校验")),
    };
    let bytes = serde_json::to_vec(&json!({"values":values,"guard":guard}))
        .map_err(|_| sources::invalid("编辑基线编码失败"))?;
    let mut target = json!({"token":Sha256::of(&bytes).as_str(),"values":values});
    if let Some(question) = question_view {
        target["question"] =
            serde_json::to_value(question).map_err(|_| sources::invalid("问题无法编码"))?;
    }
    Ok(target)
}

fn validate_target(store: &Store, target_type: &str, target_id: &str) -> Result<()> {
    let spec_id = match target_type {
        "spec" => target_id,
        "question" | "stage" => target_id.split(':').next().unwrap_or(""),
        _ => return Err(sources::invalid("草稿类型不支持")),
    };
    sources::spec(store.state(), spec_id)?;
    Ok(())
}

pub fn list(
    store: &mut Store,
    target_type: Option<&str>,
    target_id: Option<&str>,
) -> Result<Vec<Draft>> {
    let mut drafts = store.records::<Draft>("draft")?;
    drafts.retain(|draft| {
        target_type.is_none_or(|kind| draft.target_type == kind)
            && target_id.is_none_or(|id| draft.target_id == id)
    });
    drafts.sort_by(|a, b| b.updated_at.cmp(&a.updated_at));
    Ok(drafts)
}

pub fn save(store: &mut Store, id: &str, request: SaveDraft) -> Result<Draft> {
    if !(32..=36).contains(&id.len())
        || id.replace('-', "").len() != 32
        || !id
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() || byte == b'-')
        || request.editor_session_id.is_empty()
    {
        return Err(sources::invalid("草稿标识无效"));
    }
    if request.target_type == "desktopPi" {
        // The stable desktop editor ID is scoped to one data directory. It is
        // deliberately not an authorization credential; the API session still
        // controls access to the store.
        if id.len() != 32
            || !id
                .bytes()
                .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
            || request.target_id != id
            || request.editor_session_id != id
            || !request.payload.is_object()
        {
            return Err(sources::invalid("桌面草稿身份不合法"));
        }
    } else {
        validate_target(store, &request.target_type, &request.target_id)?;
    }
    let previous = store.record::<Draft>("draft", id)?;
    if previous.as_ref().map_or(0, |draft| draft.revision) != request.expected_revision {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "草稿已在其他窗口更新，请选择最新版本",
        )
        .context("httpStatus", 409));
    }
    if previous.as_ref().is_some_and(|draft| {
        draft.target_id != request.target_id
            || draft.target_type != request.target_type
            || draft.editor_session_id != request.editor_session_id
    }) {
        return Err(sources::invalid("草稿身份与编辑目标不匹配"));
    }
    let draft = Draft {
        id: id.into(),
        editor_session_id: request.editor_session_id,
        target_type: request.target_type,
        target_id: request.target_id,
        base_token: request.base_token,
        base: request.base,
        payload: request.payload,
        revision: request
            .expected_revision
            .checked_add(1)
            .ok_or_else(|| sources::invalid("草稿版本超限"))?,
        updated_at: sources::now(),
    };
    store.put_record("draft", id, &draft)?;
    Ok(draft)
}

pub fn delete(store: &mut Store, id: &str, expected_revision: u64) -> Result<()> {
    if let Some(draft) = store.record::<Draft>("draft", id)? {
        if draft.revision != expected_revision {
            return Err(YssError::new(
                ErrorCode::ConcurrentChange,
                "草稿已更新，请重新核对",
            ));
        }
        store.delete_record("draft", id)?;
    }
    Ok(())
}
