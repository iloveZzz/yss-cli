use super::{model, sources::invalid};
use crate::{
    codec::{self, Limits},
    error::Result,
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
enum Stage {
    #[default]
    Unmarked,
    Research,
    Analysis,
    Spec,
    Prototype,
    Handoff,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
enum Status {
    #[default]
    Unmarked,
    Todo,
    Doing,
    Blocked,
    Done,
    Canceled,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ProjectRecord {
    pub id: String,
    pub name: String,
    pub root_path: String,
    pub active: bool,
    pub created_at: String,
    pub updated_at: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
enum ResourceKind {
    Supplement,
    Prototype,
    Interaction,
    Other,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
enum TargetType {
    Local,
    Web,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ResourceRef {
    pub kind: ResourceKind,
    pub label: String,
    pub target_type: TargetType,
    pub target: String,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct SpecRecord {
    pub id: String,
    pub project_id: String,
    pub source_path: String,
    pub title: String,
    pub stage: Stage,
    pub status: Status,
    pub next_action: String,
    pub blocker: String,
    pub notes: String,
    pub resources: Vec<ResourceRef>,
    pub active: bool,
    pub created_at: String,
    pub updated_at: String,
    pub last_visited_at: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct FieldChange {
    pub before: Value,
    pub after: Value,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ChangeEvent {
    pub id: String,
    pub project_id: String,
    pub spec_id: Option<String>,
    pub at: String,
    pub changes: BTreeMap<String, FieldChange>,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct WorkbenchState {
    pub schema_version: u32,
    pub revision: u64,
    pub projects: Vec<ProjectRecord>,
    pub specs: Vec<SpecRecord>,
    pub history: Vec<ChangeEvent>,
}
impl Default for WorkbenchState {
    fn default() -> Self {
        Self {
            schema_version: 1,
            revision: 0,
            projects: vec![],
            specs: vec![],
            history: vec![],
        }
    }
}

pub fn decode_state(bytes: &[u8], limits: Limits) -> Result<model::WorkbenchState> {
    let mut value: Value = codec::decode_json(bytes, limits)?;
    match value.get("schemaVersion").and_then(Value::as_u64) {
        Some(2) => {
            serde_json::from_value(value).map_err(|e| invalid(&format!("工作台数据无效：{e}")))
        }
        Some(1) => {
            let _: WorkbenchState = serde_json::from_value(value.clone())
                .map_err(|e| invalid(&format!("旧版工作台数据无效：{e}")))?;
            let obj = value
                .as_object_mut()
                .ok_or_else(|| invalid("工作台数据不是对象"))?;
            obj.insert("schemaVersion".into(), Value::from(2));
            for key in ["tasks", "stageRecords", "savedViews"] {
                obj.insert(key.into(), Value::Array(vec![]));
            }
            for event in obj
                .get_mut("history")
                .and_then(Value::as_array_mut)
                .ok_or_else(|| invalid("历史格式无效"))?
            {
                let fields = event
                    .as_object_mut()
                    .ok_or_else(|| invalid("历史格式无效"))?;
                fields.insert("taskId".into(), Value::Null);
                fields.insert("lifecycleStage".into(), Value::Null);
            }
            serde_json::from_value(value)
                .map_err(|e| invalid(&format!("旧版工作台数据转换失败：{e}")))
        }
        _ => Err(invalid("不支持的工作台数据版本；原文件已保留")),
    }
}
