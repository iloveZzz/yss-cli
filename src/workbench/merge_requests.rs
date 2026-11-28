use super::{sources, store::Store};
use crate::{cancel::Cancellation, error::Result};
use serde::{Deserialize, Serialize};
use std::sync::{Arc, Mutex};

pub(in crate::workbench) mod providers;
pub(in crate::workbench) mod repositories;

pub(super) use repositories::SourceCache;
pub(super) use repositories::{discover, save};

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "lowercase")]
pub(in crate::workbench) enum Platform {
    Github,
    Gitlab,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub(in crate::workbench) struct Repository {
    pub(in crate::workbench) id: String,
    pub(in crate::workbench) platform: Platform,
    pub(in crate::workbench) origin: String,
    pub(in crate::workbench) slug: String,
    pub(in crate::workbench) url: String,
    pub(in crate::workbench) included: bool,
}

#[derive(Clone, Copy, Debug, Deserialize)]
#[serde(rename_all = "lowercase")]
enum Order {
    Updated,
    Created,
}

#[derive(Clone, Copy, Default, Deserialize)]
#[serde(rename_all = "lowercase")]
enum ListStatus {
    Open,
    Merged,
    Closed,
    #[default]
    All,
}

#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all = "lowercase")]
enum DetailSection {
    Base,
    Review,
    Ci,
    History,
}

#[derive(Serialize)]
pub(in crate::workbench) struct ReadIssue {
    pub(in crate::workbench) code: &'static str,
    pub(in crate::workbench) message: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct PageRequest {
    repository_id: String,
    page: u32,
    order: Order,
    #[serde(default)]
    status: ListStatus,
    number: Option<u64>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct DetailRequest {
    repository_id: String,
    number: u64,
    section: Option<DetailSection>,
}

#[derive(Serialize)]
#[serde(untagged)]
enum DetailData {
    Complete(Box<providers::Detail>),
    Section(providers::DetailSectionData),
}

#[derive(Serialize)]
#[serde(untagged)]
enum ReadResponse<T> {
    Data {
        #[serde(flatten)]
        data: T,
        identity: String,
    },
    Failure {
        issue: ReadIssue,
        #[serde(skip_serializing_if = "Option::is_none")]
        identity: Option<String>,
    },
}

fn failure<T>(issue: ReadIssue) -> ReadResponse<T> {
    ReadResponse::Failure {
        issue,
        identity: None,
    }
}

fn lost_access(issue: &ReadIssue) -> bool {
    matches!(issue.code, "authentication" | "permission" | "missingTool")
}

fn active_repository(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    id: &str,
    cancel: &Cancellation,
) -> Result<Option<Repository>> {
    cancel.check()?;
    Ok(repositories::discover(store, cache, cancel)?
        .repositories
        .into_iter()
        .find(|repo| repo.id == id && repo.included))
}

fn changed_scope<T>() -> ReadResponse<T> {
    failure(ReadIssue {
        code: "permission",
        message: "仓库来源已失效或移出浏览范围，请刷新仓库来源。".into(),
    })
}

fn read<T>(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    id: &str,
    cancel: &Cancellation,
    operation: impl FnOnce(&Repository) -> Result<T>,
) -> Result<ReadResponse<T>> {
    if id.is_empty() || id.len() > 128 {
        return Err(sources::invalid("仓库来源标识无效"));
    }
    let Some(repo) = active_repository(store, cache, id, cancel)? else {
        return Ok(changed_scope());
    };
    let identity_result = providers::identity(&repo, cancel);
    cancel.check()?;
    let identity = match identity_result {
        Ok(identity) => identity,
        Err(error) => return Ok(failure(providers::classify(&error))),
    };
    let operation_result = operation(&repo);
    cancel.check()?;
    let data = match operation_result {
        Ok(data) => Ok(data),
        Err(error) => {
            let issue = providers::classify(&error);
            if lost_access(&issue) {
                return Ok(failure(issue));
            }
            Err(issue)
        }
    };
    let current_identity_result = providers::identity(&repo, cancel);
    cancel.check()?;
    let current_identity = match current_identity_result {
        Ok(identity) => identity,
        Err(error) => {
            let issue = providers::classify(&error);
            let issue = match data {
                Err(original) if !lost_access(&issue) => original,
                _ => issue,
            };
            return Ok(failure(issue));
        }
    };
    if identity != current_identity {
        return Ok(ReadResponse::Failure {
            issue: ReadIssue {
                code: "authentication",
                message: "平台连接身份已变化，请使用当前连接重新读取。".into(),
            },
            identity: Some(current_identity),
        });
    }
    if active_repository(store, cache, id, cancel)?.as_ref() != Some(&repo) {
        return Ok(changed_scope());
    }
    Ok(match data {
        Ok(data) => ReadResponse::Data { data, identity },
        Err(issue) => ReadResponse::Failure {
            issue,
            identity: Some(identity),
        },
    })
}

pub(super) fn page(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    request: PageRequest,
    cancel: &Cancellation,
) -> Result<impl Serialize> {
    if request.page == 0 {
        return Err(sources::invalid("合并请求页码必须从 1 开始"));
    }
    if request
        .number
        .is_some_and(|number| number == 0 || request.page != 1)
    {
        return Err(sources::invalid("按编号查询须提供有效编号并从第一页读取"));
    }
    read(store, cache, &request.repository_id, cancel, |repo| {
        providers::page(
            repo,
            request.page,
            request.order,
            request.status,
            request.number,
            cancel,
        )
    })
}

pub(super) fn detail(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    request: DetailRequest,
    cancel: &Cancellation,
) -> Result<impl Serialize> {
    if request.number == 0 {
        return Err(sources::invalid("合并请求编号必须大于 0"));
    }
    read(
        store,
        cache,
        &request.repository_id,
        cancel,
        |repo| match request.section {
            Some(section) => providers::detail_section(repo, request.number, section, cancel)
                .map(DetailData::Section),
            None => providers::detail(repo, request.number, cancel)
                .map(Box::new)
                .map(DetailData::Complete),
        },
    )
}
