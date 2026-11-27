use super::{sources, store::Store};
use crate::{cancel::Cancellation, error::Result};
use serde::{Deserialize, Serialize};
use std::sync::{Arc, Mutex};

mod providers;
mod repositories;

pub(super) use repositories::{discover, save};

#[derive(Clone, Copy, Debug, Deserialize, Eq, Ord, PartialEq, PartialOrd, Serialize)]
#[serde(rename_all = "lowercase")]
enum Platform {
    Github,
    Gitlab,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
struct Repository {
    id: String,
    platform: Platform,
    origin: String,
    slug: String,
    url: String,
    included: bool,
}

#[derive(Clone, Copy, Debug, Deserialize)]
#[serde(rename_all = "lowercase")]
enum Order {
    Updated,
    Created,
}

#[derive(Serialize)]
struct ReadIssue {
    code: &'static str,
    message: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct PageRequest {
    repository_id: String,
    page: u32,
    order: Order,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct DetailRequest {
    repository_id: String,
    number: u64,
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
    id: &str,
    cancel: &Cancellation,
) -> Result<Option<Repository>> {
    cancel.check()?;
    Ok(repositories::discover(store, cancel)?
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
    id: &str,
    cancel: &Cancellation,
    operation: impl FnOnce(&Repository) -> Result<T>,
) -> Result<ReadResponse<T>> {
    if id.is_empty() || id.len() > 128 {
        return Err(sources::invalid("仓库来源标识无效"));
    }
    let Some(repo) = active_repository(store, id, cancel)? else {
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
        return Ok(failure(ReadIssue {
            code: "authentication",
            message: "平台连接身份已变化，请使用当前连接重新读取。".into(),
        }));
    }
    if active_repository(store, id, cancel)?.as_ref() != Some(&repo) {
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
    request: PageRequest,
    cancel: &Cancellation,
) -> Result<impl Serialize> {
    if request.page == 0 {
        return Err(sources::invalid("合并请求页码必须从 1 开始"));
    }
    read(store, &request.repository_id, cancel, |repo| {
        providers::page(repo, request.page, request.order, cancel)
    })
}

pub(super) fn detail(
    store: &Arc<Mutex<Store>>,
    request: DetailRequest,
    cancel: &Cancellation,
) -> Result<impl Serialize> {
    if request.number == 0 {
        return Err(sources::invalid("合并请求编号必须大于 0"));
    }
    read(store, &request.repository_id, cancel, |repo| {
        providers::detail(repo, request.number, cancel)
    })
}
