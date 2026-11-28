use super::{Detail, Page, State, Summary};
use crate::{
    cancel::Cancellation,
    error::{Result, YssError},
    workbench::merge_requests::{
        Platform, Repository,
        providers::{
            Response, classify, get, header_values, malformed, next_page, number, read_error,
            timestamp, valid_slug,
        },
    },
};
use reqwest::Url;
use serde_json::Value;

const PAGE_SIZE: usize = 25;
const GITHUB_SEARCH_LIMIT: u64 = 1_000;
const MAX_NUMBER: u64 = (1 << 53) - 1;

pub(super) fn valid_number(number: u64) -> bool {
    (1..=MAX_NUMBER).contains(&number)
}

struct Endpoint {
    repository: String,
    issues: String,
}

impl Endpoint {
    fn new(repo: &Repository) -> Result<Self> {
        if !valid_slug(&repo.slug) {
            return Err(malformed());
        }
        let repository = match repo.platform {
            Platform::Github if repo.slug.split('/').count() == 2 => format!("repos/{}", repo.slug),
            Platform::Github => return Err(malformed()),
            Platform::Gitlab => format!("projects/{}", repo.slug.replace('/', "%2F")),
        };
        Ok(Self {
            issues: format!("{repository}/issues"),
            repository,
        })
    }
}

fn with_query(
    repo: &Repository,
    path: &str,
    parameters: &[(&str, &str)],
    cancel: &Cancellation,
) -> Result<Response> {
    let mut url = Url::parse(&format!("{}/{path}", repo.origin)).map_err(|_| malformed())?;
    url.query_pairs_mut()
        .extend_pairs(parameters.iter().copied());
    let path = format!("{path}?{}", url.query().ok_or_else(malformed)?);
    get(repo, &path, cancel)
}

fn list(
    repo: &Repository,
    endpoint: &Endpoint,
    state: State,
    query: &str,
    page: u32,
    size: usize,
    cancel: &Cancellation,
) -> Result<Response> {
    let state = match (repo.platform, state) {
        (Platform::Gitlab, State::Open) => "opened",
        (Platform::Github, State::Open) => "open",
        (_, State::Closed) => "closed",
        (_, State::All) => "all",
    };
    let size = size.to_string();
    let page = page.to_string();
    let mut parameters = vec![
        ("state", state),
        ("per_page", size.as_str()),
        ("page", page.as_str()),
    ];
    match repo.platform {
        Platform::Github => parameters.extend([("sort", "updated"), ("direction", "desc")]),
        Platform::Gitlab => {
            parameters.extend([
                ("scope", "all"),
                ("order_by", "updated_at"),
                ("sort", "desc"),
                ("issue_type", "issue"),
            ]);
            if !query.is_empty() {
                parameters.extend([("search", query), ("in", "title,description")]);
            }
        }
    }
    with_query(repo, &endpoint.issues, &parameters, cancel)
}

fn unexpected_source() -> YssError {
    read_error("permission", "平台返回了其他仓库或条目，请核对 Issue 来源")
}

fn matches_path(actual: &Url, expected: &Url) -> bool {
    actual.origin() == expected.origin()
        && actual.path().eq_ignore_ascii_case(expected.path())
        && actual.query().is_none()
        && actual.fragment().is_none()
        && actual.username().is_empty()
        && actual.password().is_none()
}

fn verify_repository(repo: &Repository, endpoint: &Endpoint, cancel: &Cancellation) -> Result<()> {
    let value = get(repo, &endpoint.repository, cancel)?.body;
    let (slug_key, url_key) = match repo.platform {
        Platform::Github => ("full_name", "html_url"),
        Platform::Gitlab => ("path_with_namespace", "web_url"),
    };
    let slug = value[slug_key].as_str().ok_or_else(malformed)?;
    let actual = value[url_key].as_str().ok_or_else(malformed)?;
    let actual = Url::parse(actual).map_err(|_| malformed())?;
    let expected = Url::parse(&repo.url).map_err(|_| malformed())?;
    if !slug.eq_ignore_ascii_case(&repo.slug) || !matches_path(&actual, &expected) {
        return Err(unexpected_source());
    }
    let enabled = match repo.platform {
        Platform::Github => value["has_issues"].as_bool() != Some(false),
        Platform::Gitlab => {
            value["issues_access_level"].as_str() != Some("disabled")
                && value["issues_enabled"].as_bool() != Some(false)
        }
    };
    if !enabled {
        return Err(read_error("permission", "所选仓库未启用 Issues"));
    }
    Ok(())
}

fn is_pull_request(repo: &Repository, value: &Value) -> bool {
    repo.platform == Platform::Github && value.get("pull_request").is_some()
}

fn is_other_work_item(repo: &Repository, value: &Value) -> bool {
    repo.platform == Platform::Gitlab
        && value["issue_type"]
            .as_str()
            .is_some_and(|kind| kind != "issue")
}

fn item_number(repo: &Repository, value: &Value) -> Result<u64> {
    let key = match repo.platform {
        Platform::Github => "number",
        Platform::Gitlab => "iid",
    };
    number(value, key)
        .filter(|number| valid_number(*number))
        .ok_or_else(malformed)
}

fn item_url(repo: &Repository, value: &Value, number: u64) -> Result<String> {
    let (key, segment) = match repo.platform {
        Platform::Github if is_pull_request(repo, value) => ("html_url", "pull"),
        Platform::Github => ("html_url", "issues"),
        Platform::Gitlab => ("web_url", "-/issues"),
    };
    let actual = value[key].as_str().ok_or_else(malformed)?;
    let actual = Url::parse(actual).map_err(|_| malformed())?;
    let base = repo.url.trim_end_matches('/');
    let expected = Url::parse(&format!("{base}/{segment}/{number}")).map_err(|_| malformed())?;
    if matches_path(&actual, &expected) {
        return Ok(actual.into());
    }
    if repo.platform == Platform::Gitlab {
        let work_item =
            Url::parse(&format!("{base}/-/work_items/{number}")).map_err(|_| malformed())?;
        if matches_path(&actual, &work_item) {
            return Ok(actual.into());
        }
    }
    Err(unexpected_source())
}

fn clean_text(value: &str) -> Option<String> {
    let normalized: String = value
        .chars()
        .filter(|character| !character.is_control())
        .take(2_048)
        .collect();
    (!normalized.trim().is_empty()).then_some(normalized)
}

fn summary(repo: &Repository, value: &Value) -> Result<Option<Summary>> {
    let number = item_number(repo, value)?;
    let url = item_url(repo, value, number)?;
    if is_pull_request(repo, value) || is_other_work_item(repo, value) {
        return Ok(None);
    }
    let status = match (repo.platform, value["state"].as_str()) {
        (Platform::Github, Some("open")) | (Platform::Gitlab, Some("opened")) => State::Open,
        (_, Some("closed")) => State::Closed,
        _ => return Err(malformed()),
    };
    let labels = value["labels"]
        .as_array()
        .into_iter()
        .flatten()
        .filter_map(|label| match repo.platform {
            Platform::Github => label["name"].as_str(),
            Platform::Gitlab => label.as_str(),
        })
        .filter_map(clean_text)
        .collect();
    Ok(Some(Summary {
        number,
        title: value["title"].as_str().and_then(clean_text),
        status,
        labels,
        updated_at: timestamp(value, "updated_at"),
        url,
    }))
}

fn access(repo: &Repository, endpoint: &Endpoint, cancel: &Cancellation) -> Result<Option<u64>> {
    let response = list(repo, endpoint, State::All, "", 1, 1, cancel)?;
    let values = response.body.as_array().ok_or_else(malformed)?;
    if let Some(value) = values.first()
        && let Some(summary) = summary(repo, value)?
    {
        return Ok(Some(summary.number));
    }
    // An empty results array (or one containing only a PR) does not identify the
    // repository if the remote has silently redirected the original path.
    verify_repository(repo, endpoint, cancel)?;
    Ok(None)
}

fn make_page(
    repo: &Repository,
    state: State,
    values: &[Value],
    headers: &str,
    page: u32,
    literal: Option<&str>,
) -> Result<Page> {
    if values.len() > PAGE_SIZE {
        return Err(malformed());
    }
    let mut items = Vec::new();
    for value in values {
        if let Some(item) = summary(repo, value)?
            && state.includes(item.status)
            && literal.is_none_or(|phrase| {
                ["title", "body"].into_iter().any(|key| {
                    value[key]
                        .as_str()
                        .is_some_and(|text| text.to_lowercase().contains(phrase))
                })
            })
        {
            items.push(item);
        }
    }
    // Provider pages include GitHub pull requests. Use the raw count and headers
    // so that a page containing only pull requests can still have a successor.
    let (next_page, incomplete) = match next_page(headers, page, values.len(), PAGE_SIZE) {
        Ok(next) => (next, false),
        Err(_) => (None, true),
    };
    Ok(Page {
        items,
        next_page,
        incomplete,
    })
}

pub(super) fn page(
    repo: &Repository,
    state: State,
    query: &str,
    page: u32,
    cancel: &Cancellation,
) -> Result<Page> {
    let endpoint = Endpoint::new(repo)?;
    if repo.platform == Platform::Github && !query.is_empty() {
        return github_search(repo, &endpoint, state, query, page, cancel);
    }
    let response = list(repo, &endpoint, state, query, page, PAGE_SIZE, cancel)?;
    let values = response.body.as_array().ok_or_else(malformed)?;
    if values.is_empty() {
        verify_repository(repo, &endpoint, cancel)?;
    }
    let mut result = make_page(repo, state, values, &response.headers, page, None)?;
    if repo.platform == Platform::Github
        && !response.headers.is_empty()
        && header_values(&response.headers, "link").next().is_none()
    {
        result.next_page = None;
    }
    Ok(result)
}

fn github_search(
    repo: &Repository,
    endpoint: &Endpoint,
    state: State,
    query: &str,
    page: u32,
    cancel: &Cancellation,
) -> Result<Page> {
    let max_page = (GITHUB_SEARCH_LIMIT / PAGE_SIZE as u64) as u32;
    // Keep search delimiters and the qualifier separator outside the user phrase.
    // Only the fixed qualifiers can set its repository, state, and search fields.
    let phrase = query.replace(['"', '\\', ':'], " ");
    let phrase = phrase.trim();
    let altered = phrase != query;
    if phrase.is_empty() || page > max_page {
        access(repo, endpoint, cancel)?;
        return Ok(Page {
            incomplete: true,
            ..Page::default()
        });
    }
    let state_filter = match state {
        State::Open => " is:open",
        State::Closed => " is:closed",
        State::All => "",
    };
    let expression = format!(
        "\"{phrase}\" repo:{} is:issue in:title,body{state_filter}",
        repo.slug
    );
    let response = match with_query(
        repo,
        "search/issues",
        &[
            ("q", &expression),
            ("sort", "updated"),
            ("order", "desc"),
            ("per_page", &PAGE_SIZE.to_string()),
            ("page", &page.to_string()),
        ],
        cancel,
    ) {
        Ok(response) => response,
        Err(error) => {
            let code = classify(&error).code;
            if matches!(code, "permission" | "unavailable") {
                access(repo, endpoint, cancel)?;
            }
            return Err(if code == "permission" {
                read_error("unavailable", "当前连接无法在所选仓库检索 Issue")
            } else {
                error
            });
        }
    };
    // Search may return 422 for an inaccessible repository, or zero when the
    // search index has no matches. Independently check the Issues endpoint.
    access(repo, endpoint, cancel)?;
    let values = response.body["items"].as_array().ok_or_else(malformed)?;
    let incomplete = response.body["incomplete_results"]
        .as_bool()
        .ok_or_else(malformed)?;
    let total = response.body["total_count"]
        .as_u64()
        .ok_or_else(malformed)?;
    // The index can ignore punctuation. After changing the search phrase to
    // preserve its scope, keep only candidates containing the original text.
    let literal = altered.then(|| query.to_lowercase());
    let mut result = make_page(
        repo,
        state,
        values,
        &response.headers,
        page,
        literal.as_deref(),
    )?;
    if u64::from(page) * PAGE_SIZE as u64 >= total && !incomplete {
        result.next_page = None;
    }
    result.incomplete |= altered || incomplete || total > GITHUB_SEARCH_LIMIT;
    if page == max_page && result.next_page.is_some() {
        result.next_page = None;
        result.incomplete |= total != GITHUB_SEARCH_LIMIT;
    }
    Ok(result)
}

fn item_unavailable() -> YssError {
    read_error("unavailable", "Issue 已移除、不可读或访问受限，请核对来源")
        .context("issueItem", "true")
}

fn access_after_item_failure(
    repo: &Repository,
    endpoint: &Endpoint,
    cancel: &Cancellation,
) -> Result<Option<u64>> {
    access(repo, endpoint, cancel).map_err(|error| {
        if matches!(
            classify(&error).code,
            "authentication" | "permission" | "missingTool"
        ) {
            error
        } else {
            // If checking the whole repository fails temporarily, the failed
            // item still must not remain visible in the existing list.
            error.context("issueItem", "true")
        }
    })
}

fn lookup(
    repo: &Repository,
    requested: u64,
    cancel: &Cancellation,
) -> Result<Option<(Value, Summary)>> {
    let endpoint = Endpoint::new(repo)?;
    let path = format!("{}/{requested}", endpoint.issues);
    let value = match get(repo, &path, cancel) {
        Ok(response) => response.body,
        Err(error) if classify(&error).code == "permission" => {
            let seen = access_after_item_failure(repo, &endpoint, cancel)?;
            let not_found = error
                .context
                .get("httpStatus")
                .is_some_and(|status| status == "404");
            if not_found && seen != Some(requested) {
                return Ok(None);
            }
            return Err(item_unavailable());
        }
        Err(error) => return Err(error),
    };
    let summary = match summary(repo, &value) {
        Ok(Some(summary)) if summary.number == requested => summary,
        Ok(None) if item_number(repo, &value)? == requested => {
            access_after_item_failure(repo, &endpoint, cancel)?;
            return Ok(None);
        }
        Err(error) if classify(&error).code != "permission" => return Err(error),
        _ => {
            // An issue may have been transferred independently of its repository.
            access_after_item_failure(repo, &endpoint, cancel)?;
            return Err(item_unavailable());
        }
    };
    Ok(Some((value, summary)))
}

pub(super) fn numbered_page(
    repo: &Repository,
    state: State,
    number: u64,
    cancel: &Cancellation,
) -> Result<Page> {
    let items = lookup(repo, number, cancel)?
        .map(|(_, summary)| summary)
        .filter(|summary| state.includes(summary.status))
        .into_iter()
        .collect();
    Ok(Page {
        items,
        ..Page::default()
    })
}

pub(super) fn detail(repo: &Repository, number: u64, cancel: &Cancellation) -> Result<Detail> {
    let (mut value, summary) = lookup(repo, number, cancel)?.ok_or_else(item_unavailable)?;
    let key = match repo.platform {
        Platform::Github => "body",
        Platform::Gitlab => "description",
    };
    let body = match value.get_mut(key).map(Value::take) {
        Some(Value::Null) => None,
        Some(Value::String(body)) => Some(body),
        _ => return Err(malformed()),
    };
    Ok(Detail { summary, body })
}
