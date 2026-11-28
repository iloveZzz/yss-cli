use super::{
    merge_requests::{Repository, SourceCache, providers as cli, repositories::discover_issues},
    sources,
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::{Result, YssError},
};
use serde::{Deserialize, Serialize};
use std::sync::{Arc, Mutex};

mod provider;

#[derive(Clone, Copy, Deserialize, PartialEq, Serialize)]
#[serde(rename_all = "lowercase")]
enum State {
    Open,
    Closed,
    All,
}

impl State {
    fn includes(self, state: Self) -> bool {
        self == Self::All || self == state
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct PageRequest {
    source_id: String,
    state: State,
    query: String,
    page: u32,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct DetailRequest {
    source_id: String,
    number: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Summary {
    number: u64,
    title: Option<String>,
    status: State,
    labels: Vec<String>,
    updated_at: Option<String>,
    url: String,
}

#[derive(Serialize)]
struct Detail {
    #[serde(flatten)]
    summary: Summary,
    body: Option<String>,
}

#[derive(Default, Serialize)]
#[serde(rename_all = "camelCase")]
struct Page {
    items: Vec<Summary>,
    next_page: Option<u32>,
    incomplete: bool,
}

#[derive(Clone, Copy, PartialEq, Serialize)]
#[serde(rename_all = "lowercase")]
enum Scope {
    Item,
    Source,
}

#[derive(Serialize)]
struct ReadIssue {
    #[serde(flatten)]
    issue: super::merge_requests::ReadIssue,
    #[serde(skip_serializing_if = "Option::is_none")]
    scope: Option<Scope>,
}

impl From<YssError> for ReadIssue {
    fn from(error: YssError) -> Self {
        let issue = cli::classify(&error);
        let scope = if error.context.contains_key("issueItem") {
            Some(Scope::Item)
        } else if matches!(issue.code, "authentication" | "permission" | "missingTool") {
            Some(Scope::Source)
        } else {
            None
        };
        Self { issue, scope }
    }
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

impl<T> ReadResponse<T> {
    fn failure(error: impl Into<ReadIssue>) -> Self {
        let mut issue = error.into();
        // The account and repository could not both be verified for this read.
        // No data cached for the previous account can be assumed visible.
        issue.scope = Some(Scope::Source);
        Self::Failure {
            issue,
            identity: None,
        }
    }
}

pub(super) fn discover(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    cancel: &Cancellation,
) -> Result<impl Serialize> {
    discover_issues(store, cache, cancel)
}

fn active_repository(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    id: &str,
    cancel: &Cancellation,
) -> Result<Option<Repository>> {
    cancel.check()?;
    let inventory = discover_issues(store, cache, cancel)?;
    cancel.check()?;
    Ok(inventory
        .sources
        .into_iter()
        .find(|source| source.id == id)
        .map(|source| source.repository))
}

fn changed_source() -> ReadIssue {
    ReadIssue {
        issue: super::merge_requests::ReadIssue {
            code: "invalidSource",
            message: "Issue 来源已失效，请刷新来源后重试".into(),
        },
        scope: Some(Scope::Source),
    }
}

fn read<T>(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    id: &str,
    cancel: &Cancellation,
    operation: impl FnOnce(&Repository) -> Result<T>,
) -> Result<ReadResponse<T>> {
    if id.is_empty() || id.len() > 128 {
        return Err(sources::invalid("Issue 来源标识无效"));
    }
    let Some(repository) = active_repository(store, cache, id, cancel)? else {
        return Ok(ReadResponse::failure(changed_source()));
    };
    let identity = cli::identity(&repository, cancel);
    cancel.check()?;
    let identity = match identity {
        Ok(identity) => identity,
        Err(error) => return Ok(ReadResponse::failure(error)),
    };
    let data = operation(&repository);
    cancel.check()?;
    let data = match data {
        Ok(data) => Ok(data),
        Err(error) => {
            let issue = ReadIssue::from(error);
            if issue.scope == Some(Scope::Source) {
                return Ok(ReadResponse::failure(issue));
            }
            Err(issue)
        }
    };

    let current_identity = cli::identity(&repository, cancel);
    cancel.check()?;
    match current_identity {
        Ok(current) if current == identity => {}
        Ok(_) => {
            return Ok(ReadResponse::failure(cli::read_error(
                "authentication",
                "平台连接身份已变化，请使用当前连接重新读取",
            )));
        }
        Err(error) => {
            let issue = ReadIssue::from(error);
            return Ok(ReadResponse::failure(match data {
                Err(original) if issue.scope.is_none() => original,
                _ => issue,
            }));
        }
    }
    if active_repository(store, cache, id, cancel)?.as_ref() != Some(&repository) {
        return Ok(ReadResponse::failure(changed_source()));
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
        return Err(sources::invalid("Issue 页码必须从 1 开始"));
    }
    let query = request.query.trim();
    if query.chars().count() > 256 || query.chars().any(char::is_control) {
        return Err(sources::invalid("Issue 关键词格式无效或超过 256 个字符"));
    }
    let exact = query
        .strip_prefix('#')
        .filter(|digits| digits.bytes().all(|byte| byte.is_ascii_digit()))
        .map(|digits| digits.trim_start_matches('0'))
        .filter(|digits| !digits.is_empty())
        .map(|digits| {
            digits
                .parse::<u64>()
                .ok()
                .filter(|number| provider::valid_number(*number) && request.page == 1)
                .ok_or_else(|| sources::invalid("按编号查找须提供有效的 Issue 编号并从第一页读取"))
        })
        .transpose()?;

    read(store, cache, &request.source_id, cancel, |repository| {
        if let Some(number) = exact {
            return provider::numbered_page(repository, request.state, number, cancel);
        }
        provider::page(repository, request.state, query, request.page, cancel)
    })
}

pub(super) fn detail(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    request: DetailRequest,
    cancel: &Cancellation,
) -> Result<impl Serialize> {
    if !provider::valid_number(request.number) {
        return Err(sources::invalid("Issue 编号无效"));
    }
    read(store, cache, &request.source_id, cancel, |repository| {
        provider::detail(repository, request.number, cancel)
    })
}
