use super::{Order, Platform, ReadIssue, Repository};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    workbench::{process, sources},
};
use reqwest::Url;
use serde::Serialize;
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::{collections::HashMap, env, path::Path, process::Command, time::Duration};
use time::{OffsetDateTime, format_description::well_known::Rfc3339};

const PAGE_SIZE: usize = 25;
const REQUEST_TIMEOUT: Duration = Duration::from_secs(15);

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Page {
    pub items: Vec<Summary>,
    pub next_page: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    continuation_issue: Option<ReadIssue>,
    pub read_at: String,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Summary {
    pub number: u64,
    pub title: Option<String>,
    pub author: Option<String>,
    pub created_at: Option<String>,
    pub updated_at: Option<String>,
    pub status: &'static str,
    pub draft: Option<bool>,
    pub url: String,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Detail {
    #[serde(flatten)]
    pub summary: Summary,
    pub description: Option<String>,
    pub description_available: bool,
    pub source_branch: Option<String>,
    pub target_branch: Option<String>,
    pub source_repository: Option<String>,
    pub target_repository: Option<String>,
    pub merged_at: Option<String>,
    pub closed_at: Option<String>,
    pub review: Signal,
    pub ci: Signal,
    pub historical_ci: Vec<Signal>,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Signal {
    pub status: &'static str,
    pub message: Option<String>,
    pub commit: Option<String>,
    pub at: Option<String>,
}

impl Signal {
    fn new(status: &'static str, message: Option<String>) -> Self {
        Self {
            status,
            message,
            commit: None,
            at: None,
        }
    }

    fn unavailable(error: YssError) -> Self {
        Self::new("unavailable", Some(classify(&error).message))
    }
}

fn read_error(kind: &'static str, message: &'static str) -> YssError {
    let code = match kind {
        "authentication" | "permission" | "missingTool" => ErrorCode::NotReady,
        _ => ErrorCode::Network,
    };
    YssError::new(code, message).context("readIssue", kind)
}

fn malformed() -> YssError {
    read_error("unavailable", "平台返回的读取结果无法识别，请在原站核对")
}

pub(super) fn classify(error: &YssError) -> ReadIssue {
    let code = match error.context.get("readIssue").map(String::as_str) {
        Some("authentication") => "authentication",
        Some("permission") => "permission",
        Some("rateLimit") => "rateLimit",
        Some("missingTool") => "missingTool",
        Some("limit") => "limit",
        Some("network") => "network",
        _ if error.code == ErrorCode::NotReady && error.message.starts_with("无法启动 ") => {
            "missingTool"
        }
        _ if error.code == ErrorCode::Network && error.message.ends_with("超时") => "network",
        _ if error.code == ErrorCode::Network && error.message.contains("输出超过限制") => {
            "limit"
        }
        _ => "unavailable",
    };
    ReadIssue {
        code,
        message: error.message.clone(),
    }
}

struct Response {
    headers: String,
    body: Value,
}

fn response_parts(output: &str) -> (&str, &str) {
    if output.starts_with("HTTP/")
        && let Some(parts) = output
            .split_once("\r\n\r\n")
            .or_else(|| output.split_once("\n\n"))
    {
        return parts;
    }
    ("", output)
}

fn header_values<'a>(headers: &'a str, name: &'a str) -> impl Iterator<Item = &'a str> {
    headers
        .lines()
        .filter_map(|line| line.split_once(':'))
        .filter(move |(key, _)| key.eq_ignore_ascii_case(name))
        .map(|(_, value)| value.trim())
}

fn request_failure(status: Option<u16>, headers: &str, diagnostics: &str) -> YssError {
    let rate_limit = diagnostics.contains("rate limit") || diagnostics.contains("rate_limit");
    let pagination_limit = diagnostics.contains("maximum offset")
        || diagnostics.contains("maximum page")
        || diagnostics.contains("offset pagination") && diagnostics.contains("limit");
    match status {
        Some(401) => read_error("authentication", "本机平台连接未登录或已失效"),
        Some(429) => read_error("rateLimit", "平台限制了读取频率，请稍后重试"),
        Some(403)
            if rate_limit
                || header_values(headers, "x-ratelimit-remaining").any(|value| value == "0")
                || header_values(headers, "retry-after").any(|value| !value.is_empty()) =>
        {
            read_error("rateLimit", "平台限制了读取频率，请稍后重试")
        }
        Some(403) => read_error("permission", "当前账号无权读取该仓库或接口"),
        Some(404 | 410 | 451) => {
            read_error("permission", "仓库或接口不可访问，请核对路径及当前账号权限")
        }
        Some(408 | 500..=599) => read_error("network", "暂时无法连接平台，请稍后重试"),
        Some(400 | 416 | 422) | None if pagination_limit => {
            read_error("limit", "平台限制了可读取的分页范围，请在原站继续查找")
        }
        None if rate_limit => read_error("rateLimit", "平台限制了读取频率，请稍后重试"),
        _ if diagnostics.contains("auth login")
            || diagnostics.contains("not logged")
            || diagnostics.contains("no token")
            || diagnostics.contains("no credentials")
            || diagnostics.contains("authentication required")
            || diagnostics.contains("authentication failed") =>
        {
            read_error("authentication", "本机平台连接未登录或已失效")
        }
        _ if diagnostics.contains("forbidden") || diagnostics.contains("not accessible") => {
            read_error("permission", "仓库或接口不可访问，请核对路径及当前账号权限")
        }
        _ if diagnostics.contains("dial tcp")
            || diagnostics.contains("lookup ")
            || diagnostics.contains("timeout")
            || diagnostics.contains("connection ")
            || diagnostics.contains("network ")
            || diagnostics.contains("tls ")
            || diagnostics.contains("x509:") =>
        {
            read_error("network", "暂时无法连接平台，请检查网络和实例连接")
        }
        _ => read_error(
            "unavailable",
            "无法读取平台数据，请核对仓库、实例和本机连接",
        ),
    }
}

pub(super) fn configured_origin(value: &str, protocol: &str) -> Option<String> {
    let value = value.trim();
    if value.is_empty() {
        return None;
    }
    let url = if value.contains("://") {
        Url::parse(value).ok()?
    } else {
        Url::parse(&format!("{protocol}://{value}")).ok()?
    };
    if !matches!(url.scheme(), "https" | "http")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return None;
    }
    Some(url.origin().ascii_serialization())
}

pub(super) fn credentials(
    platform: Platform,
) -> (
    &'static str,
    &'static [&'static str],
    &'static [&'static str],
) {
    match platform {
        Platform::Github => (
            "https://github.com",
            &["GH_HOST"],
            &[
                "GH_TOKEN",
                "GITHUB_TOKEN",
                "GH_ENTERPRISE_TOKEN",
                "GITHUB_ENTERPRISE_TOKEN",
            ],
        ),
        Platform::Gitlab => (
            "https://gitlab.com",
            &[
                "GITLAB_HOST",
                "GITLAB_URI",
                "GL_HOST",
                "GITLAB_URL",
                "GLAB_HOST",
                "CI_SERVER_URL",
            ],
            &[
                "GITLAB_TOKEN",
                "GITLAB_ACCESS_TOKEN",
                "OAUTH_TOKEN",
                "GLAB_TOKEN",
                "GLAB_IS_OAUTH2",
                "CI_JOB_TOKEN",
            ],
        ),
    }
}

fn cli_setting(tool: &Path, hostname: &str, name: &str, cancel: &Cancellation) -> Result<String> {
    let mut command = Command::new(tool);
    command
        .current_dir(env::temp_dir())
        .args(["config", "get", name, "--host", hostname])
        .env("GH_PROMPT_DISABLED", "1")
        .env("GLAB_CHECK_UPDATE", "false")
        .env("GLAB_NO_PROMPT", "1")
        .env("GLAB_SEND_TELEMETRY", "false")
        .env("NO_COLOR", "1")
        .env_remove("GH_DEBUG")
        .env_remove("GLAB_DEBUG")
        .env_remove("GLAB_DEBUG_HTTP");
    let (exit, stdout, _) = process::capture_command(command, cancel, Duration::from_secs(5))?;
    Ok(if exit.success() {
        String::from_utf8_lossy(&stdout).trim().to_owned()
    } else {
        String::new()
    })
}

fn get(repo: &Repository, path: &str, cancel: &Cancellation) -> Result<Response> {
    let url = Url::parse(&repo.origin).map_err(|_| malformed())?;
    if configured_origin(&repo.origin, "https").as_deref() != Some(&repo.origin) {
        return Err(malformed());
    }
    let hostname = url.host_str().ok_or_else(malformed)?;
    let (default_origin, host_keys, secret_keys) = credentials(repo.platform);
    let env_origin = host_keys
        .iter()
        .find_map(|key| env::var(key).ok().filter(|value| !value.trim().is_empty()));
    let explicitly_scoped = env_origin
        .as_deref()
        .and_then(|value| configured_origin(value, "https"))
        .as_deref()
        == Some(&repo.origin);
    let allow_env_token =
        explicitly_scoped || env_origin.is_none() && repo.origin == default_origin;
    let (tool, api) = match repo.platform {
        Platform::Github if repo.origin == "https://github.com" => {
            ("gh", "https://api.github.com".to_owned())
        }
        Platform::Github => ("gh", format!("{}/api/v3", repo.origin)),
        Platform::Gitlab => ("glab", format!("{}/api/v4", repo.origin)),
    };
    let executable = env::var_os("PATH")
        .and_then(|path| process::system_tool_path(tool, &path))
        .ok_or_else(|| {
            YssError::new(
                ErrorCode::NotReady,
                format!("本机缺少 {tool}，请检查平台连接"),
            )
            .context("readIssue", "missingTool")
        })?;
    // gh/glab select stored credentials by DNS host. A custom port or HTTP
    // must also be selected by the user's CLI settings before using them.
    if (url.port().is_some() || url.scheme() != "https") && !explicitly_scoped {
        let protocol = match repo.platform {
            Platform::Github => "https".to_owned(),
            Platform::Gitlab => cli_setting(&executable, hostname, "api_protocol", cancel)?,
        };
        let api_host = cli_setting(&executable, hostname, "api_host", cancel)?;
        let protocol = if protocol.is_empty() {
            "https"
        } else {
            &protocol
        };
        let api_host = if api_host.is_empty() {
            hostname
        } else {
            &api_host
        };
        if configured_origin(api_host, protocol).as_deref() != Some(&repo.origin) {
            return Err(read_error(
                "authentication",
                "本机平台连接的 API 协议或端口与仓库实例不一致",
            ));
        }
    }
    let mut command = Command::new(executable);
    if !allow_env_token {
        for key in secret_keys {
            command.env_remove(key);
        }
    }
    command.current_dir(env::temp_dir());
    command
        .args([
            "api",
            "--hostname",
            hostname,
            "--method",
            "GET",
            "--include",
        ])
        .arg(format!("{api}/{path}"))
        .env("GH_PROMPT_DISABLED", "1")
        .env("GH_NO_UPDATE_NOTIFIER", "1")
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GLAB_CHECK_UPDATE", "false")
        .env("GLAB_NO_PROMPT", "1")
        .env("GLAB_SEND_TELEMETRY", "false")
        .env("NO_COLOR", "1")
        .env_remove("GH_DEBUG")
        .env_remove("GLAB_DEBUG")
        .env_remove("GLAB_DEBUG_HTTP")
        .env_remove("GIT_TRACE")
        .env_remove("GIT_TRACE_CURL");
    let (exit, stdout, stderr) = process::capture_command(command, cancel, REQUEST_TIMEOUT)?;
    let stdout = std::str::from_utf8(&stdout).map_err(|_| malformed())?;
    let (headers, body) = response_parts(stdout);
    let status = headers
        .lines()
        .next()
        .and_then(|line| line.split_ascii_whitespace().nth(1))
        .and_then(|code| code.parse::<u16>().ok());
    if !exit.success() || status.is_some_and(|code| !(200..300).contains(&code)) {
        // Authenticated CLI diagnostics and response headers can contain secrets.
        let stderr = String::from_utf8_lossy(&stderr).to_ascii_lowercase();
        let status = status.or_else(|| {
            stderr.rsplit("http ").find_map(|tail| {
                let code = tail.get(..3)?.parse::<u16>().ok()?;
                ((400..600).contains(&code)
                    && !tail.as_bytes().get(3).is_some_and(u8::is_ascii_digit))
                .then_some(code)
            })
        });
        let diagnostics = format!("{}\n{stderr}", body.to_ascii_lowercase());
        return Err(request_failure(status, headers, &diagnostics));
    }
    Ok(Response {
        headers: headers.into(),
        body: serde_json::from_str(body).map_err(|_| malformed())?,
    })
}

fn next_page(headers: &str, page: u32, count: usize, size: usize) -> Result<Option<u32>> {
    let mut links = header_values(headers, "link").peekable();
    let has_links = links.peek().is_some();
    let linked_next = links.flat_map(|value| value.split(',')).any(|link| {
        link.split(';').skip(1).any(|parameter| {
            parameter.split_once('=').is_some_and(|(key, value)| {
                key.trim().eq_ignore_ascii_case("rel")
                    && value
                        .trim()
                        .trim_matches('"')
                        .split_ascii_whitespace()
                        .any(|relation| relation.eq_ignore_ascii_case("next"))
            })
        })
    });
    if let Some(value) = header_values(headers, "x-next-page").next() {
        if value.is_empty() && !linked_next {
            return Ok(None);
        }
        if !value.is_empty() {
            let reported = value.parse::<u32>().ok();
            if reported.is_none() || page.checked_add(1) != reported {
                return Err(read_error(
                    "limit",
                    "平台返回的后续页不可读取，请在原站继续查找",
                ));
            }
            return Ok(reported);
        }
    }
    if !linked_next && (has_links || count < size) {
        return Ok(None);
    }
    page.checked_add(1)
        .map(Some)
        .ok_or_else(|| read_error("limit", "平台分页超过可读取范围，请在原站继续查找"))
}

pub(super) fn identity(repo: &Repository, cancel: &Cancellation) -> Result<String> {
    let user = get(repo, "user", cancel)?.body;
    let id = number(&user, "id")
        .ok_or_else(|| read_error("authentication", "无法核对本机平台连接的登录身份"))?;
    let platform = match repo.platform {
        Platform::Github => "github",
        Platform::Gitlab => "gitlab",
    };
    Ok(format!(
        "{:x}",
        Sha256::digest(format!("{platform}\0{}\0{id}", repo.origin).as_bytes())
    ))
}

fn valid_slug(value: &str) -> bool {
    value.len() <= 512
        && value.split('/').count() >= 2
        && value.split('/').all(|part| {
            !part.is_empty()
                && part != "."
                && part != ".."
                && part
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_' | b'.'))
        })
}

fn request_path(repo: &Repository) -> Result<(String, String)> {
    if !valid_slug(&repo.slug) {
        return Err(malformed());
    }
    match repo.platform {
        Platform::Github if repo.slug.split('/').count() == 2 => {
            let base = format!("repos/{}", repo.slug);
            Ok((format!("{base}/pulls"), base))
        }
        Platform::Github => Err(malformed()),
        Platform::Gitlab => {
            let base = format!("projects/{}", repo.slug.replace('/', "%2F"));
            Ok((format!("{base}/merge_requests"), base))
        }
    }
}

fn number(value: &Value, key: &str) -> Option<u64> {
    value
        .get(key)
        .and_then(Value::as_u64)
        .filter(|number| *number != 0)
}

fn text(value: &Value, key: &str) -> Option<String> {
    let clean: String = value
        .get(key)?
        .as_str()?
        .chars()
        .filter(|character| !character.is_control())
        .take(2048)
        .collect();
    (!clean.trim().is_empty()).then_some(clean)
}

fn timestamp(value: &Value, key: &str) -> Option<String> {
    let stamp = value.get(key)?.as_str()?;
    let date = OffsetDateTime::parse(stamp, &Rfc3339).ok()?;
    date.to_offset(time::UtcOffset::UTC).format(&Rfc3339).ok()
}

fn commit(value: &Value) -> Option<&str> {
    value.as_str().filter(|sha| {
        matches!(sha.len(), 40 | 64) && sha.bytes().all(|byte| byte.is_ascii_hexdigit())
    })
}

fn state(platform: Platform, value: &Value) -> &'static str {
    match (platform, value["state"].as_str()) {
        (Platform::Github, Some("open")) | (Platform::Gitlab, Some("opened")) => "open",
        (Platform::Gitlab, Some("merged")) => "merged",
        (Platform::Gitlab, Some("closed")) => "closed",
        (Platform::Github, Some("closed")) => {
            let merged = value["merged"].as_bool();
            let merged_at = timestamp(value, "merged_at").is_some();
            match (merged, merged_at, value.get("merged_at")) {
                (Some(false), true, _) => "unknown",
                (Some(true), _, _) | (_, true, _) => "merged",
                (Some(false), _, _) | (_, _, Some(Value::Null)) => "closed",
                _ => "unknown",
            }
        }
        _ => "unknown",
    }
}

fn summary(repo: &Repository, value: &Value) -> Result<Summary> {
    let (key, link, segment, author, handle) = match repo.platform {
        Platform::Github => ("number", "html_url", "pull", "user", "login"),
        Platform::Gitlab => ("iid", "web_url", "-/merge_requests", "author", "username"),
    };
    let number = number(value, key).ok_or_else(malformed)?;
    let expected_url = format!("{}/{segment}/{number}", repo.url.trim_end_matches('/'));
    if let Some(Value::String(actual)) = value.get(link) {
        let actual = Url::parse(actual).map_err(|_| malformed())?;
        let expected = Url::parse(&expected_url).map_err(|_| malformed())?;
        if actual.origin() != expected.origin()
            || !actual.path().eq_ignore_ascii_case(expected.path())
            || actual.query().is_some()
            || actual.fragment().is_some()
            || !actual.username().is_empty()
            || actual.password().is_some()
        {
            return Err(read_error(
                "permission",
                "平台返回了其他仓库或请求，请核对仓库来源",
            ));
        }
    }
    Ok(Summary {
        number,
        title: text(value, "title"),
        author: text(&value[author], handle),
        created_at: timestamp(value, "created_at"),
        updated_at: timestamp(value, "updated_at"),
        status: state(repo.platform, value),
        draft: value["draft"].as_bool().or_else(|| match repo.platform {
            Platform::Github => None,
            Platform::Gitlab => value["work_in_progress"].as_bool(),
        }),
        url: expected_url,
    })
}

pub(super) fn page(
    repo: &Repository,
    page: u32,
    order: Order,
    cancel: &Cancellation,
) -> Result<Page> {
    let (path, _) = request_path(repo)?;
    let query = match (repo.platform, order) {
        (Platform::Github, Order::Updated) => "state=all&sort=updated&direction=desc",
        (Platform::Github, Order::Created) => "state=all&sort=created&direction=desc",
        (Platform::Gitlab, Order::Updated) => "scope=all&order_by=updated_at&sort=desc",
        (Platform::Gitlab, Order::Created) => "scope=all&order_by=created_at&sort=desc",
    };
    let response = get(
        repo,
        &format!("{path}?{query}&per_page={PAGE_SIZE}&page={page}"),
        cancel,
    )?;
    let items = response.body.as_array().ok_or_else(malformed)?;
    let (next_page, continuation_issue) =
        match next_page(&response.headers, page, items.len(), PAGE_SIZE) {
            Ok(next) => (next, None),
            Err(error) => (None, Some(classify(&error))),
        };
    Ok(Page {
        items: items
            .iter()
            .map(|item| summary(repo, item))
            .collect::<Result<_>>()?,
        next_page,
        continuation_issue,
        read_at: sources::now(),
    })
}

fn detail_base(
    repo: &Repository,
    path: &str,
    number: u64,
    cancel: &Cancellation,
) -> Result<(Value, Summary)> {
    let value = get(repo, path, cancel)?.body;
    let summary = summary(repo, &value)?;
    if summary.number != number {
        return Err(read_error(
            "permission",
            "平台返回了其他请求，请核对仓库来源",
        ));
    }
    Ok((value, summary))
}

fn head_commit(platform: Platform, value: &Value) -> Option<&str> {
    match platform {
        Platform::Github => commit(&value["head"]["sha"]),
        Platform::Gitlab => {
            commit(&value["sha"]).or_else(|| commit(&value["diff_refs"]["head_sha"]))
        }
    }
}

pub(super) fn detail(repo: &Repository, number: u64, cancel: &Cancellation) -> Result<Detail> {
    let (collection, base) = request_path(repo)?;
    let path = format!("{collection}/{number}");
    let (initial, _) = detail_base(repo, &path, number, cancel)?;
    let (mut review, (mut ci, mut historical_ci), source_project) = match repo.platform {
        Platform::Github => (
            github_review(repo, &path, &initial, cancel).unwrap_or_else(Signal::unavailable),
            github_ci(repo, &base, &path, &initial, cancel),
            None,
        ),
        Platform::Gitlab => (
            gitlab_review(repo, &path, &initial, cancel).unwrap_or_else(Signal::unavailable),
            gitlab_ci(repo, &base, &path, &initial, number, cancel),
            gitlab_source(repo, &initial, cancel),
        ),
    };

    // Optional calls can outlive the user's access to the base request.
    let (value, summary) = detail_base(repo, &path, number, cancel)?;
    let head_changed = head_commit(repo.platform, &initial) != head_commit(repo.platform, &value);
    let review_fields: &[&str] = match repo.platform {
        Platform::Github => &["requested_reviewers", "requested_teams"],
        Platform::Gitlab => &["reviewers", "detailed_merge_status"],
    };
    if head_changed
        || initial["updated_at"] != value["updated_at"]
        || review_fields
            .iter()
            .any(|field| initial[*field] != value[*field])
    {
        review = Signal::new(
            "unavailable",
            Some("请求在读取期间更新，请重新读取评审与审批".into()),
        );
    }
    let pipeline_changed = repo.platform == Platform::Gitlab
        && ["id", "sha", "ref", "status"]
            .iter()
            .any(|field| initial["head_pipeline"][*field] != value["head_pipeline"][*field]);
    if head_changed || pipeline_changed {
        ci = Signal::new(
            "unavailable",
            Some("请求在读取期间更新，请重新读取 CI".into()),
        );
        historical_ci.clear();
    }

    let description = value.get(match repo.platform {
        Platform::Github => "body",
        Platform::Gitlab => "description",
    });
    let description_available = matches!(description, Some(Value::String(_) | Value::Null));
    let description = description
        .and_then(Value::as_str)
        .map(|text| {
            text.chars()
                .filter(|character| !character.is_control() || matches!(character, '\n' | '\t'))
                .collect::<String>()
        })
        .filter(|text| !text.trim().is_empty());

    let (source_branch, target_branch, source_repository, target_repository) = match repo.platform {
        Platform::Github => {
            let head = &value["head"];
            let target = &value["base"];
            (
                text(head, "ref"),
                text(target, "ref"),
                text(&head["repo"], "full_name"),
                text(&target["repo"], "full_name"),
            )
        }
        Platform::Gitlab => (
            text(&value, "source_branch"),
            text(&value, "target_branch"),
            source_project.filter(|_| {
                ["source_project_id", "target_project_id"]
                    .iter()
                    .all(|field| initial[*field] == value[*field])
            }),
            Some(repo.slug.clone()),
        ),
    };
    cancel.check()?;
    Ok(Detail {
        summary,
        description,
        description_available,
        source_branch,
        target_branch,
        source_repository,
        target_repository,
        merged_at: timestamp(&value, "merged_at"),
        closed_at: timestamp(&value, "closed_at"),
        review,
        ci,
        historical_ci,
    })
}

fn github_review(
    repo: &Repository,
    path: &str,
    value: &Value,
    cancel: &Cancellation,
) -> Result<Signal> {
    let mut decisive = HashMap::new();
    let mut review_seen = false;
    for page in 1..=5 {
        let response = get(
            repo,
            &format!("{path}/reviews?per_page=50&page={page}"),
            cancel,
        )?;
        let reviews = response.body.as_array().ok_or_else(malformed)?;
        // GitHub returns reviews in chronological order. Comments do not supersede decisions.
        for review in reviews {
            let review_state = review["state"].as_str().ok_or_else(malformed)?;
            if review_state == "PENDING" {
                continue;
            }
            if !matches!(
                review_state,
                "APPROVED" | "CHANGES_REQUESTED" | "DISMISSED" | "COMMENTED"
            ) {
                return Err(malformed());
            }
            review_seen = true;
            if matches!(review_state, "APPROVED" | "CHANGES_REQUESTED" | "DISMISSED") {
                let reviewer = number(&review["user"], "id").ok_or_else(malformed)?;
                if review_state == "DISMISSED" {
                    decisive.remove(&reviewer);
                    continue;
                }
                let signal = if review_state == "CHANGES_REQUESTED" {
                    Signal::new("changesRequested", Some("GitHub 记录了变更请求".into()))
                } else {
                    Signal::new("approved", Some("GitHub 记录了评审批准".into()))
                };
                decisive.insert(
                    reviewer,
                    Signal {
                        commit: commit(&review["commit_id"]).map(str::to_owned),
                        at: timestamp(review, "submitted_at"),
                        ..signal
                    },
                );
            }
        }
        if next_page(&response.headers, page, reviews.len(), 50)?.is_none() {
            if let Some(signal) = decisive.into_values().max_by_key(|signal| {
                (
                    signal.status == "changesRequested",
                    signal
                        .at
                        .as_deref()
                        .and_then(|date| OffsetDateTime::parse(date, &Rfc3339).ok()),
                    signal.commit.clone(),
                )
            }) {
                return Ok(signal);
            }
            let requests = [&value["requested_reviewers"], &value["requested_teams"]];
            if requests
                .iter()
                .any(|list| list.as_array().is_some_and(|values| !values.is_empty()))
            {
                return Ok(Signal::new(
                    "reviewRequired",
                    Some("GitHub 记录了待处理的评审邀请".into()),
                ));
            }
            if review_seen {
                return Ok(Signal::new("neutral", Some("GitHub 记录了评审活动".into())));
            }
            return if requests.iter().all(|list| list.is_array()) {
                Ok(Signal::new("noRecords", None))
            } else {
                Err(malformed())
            };
        }
    }
    Err(read_error("limit", "评审记录读取范围受限，请在原站核对"))
}

fn gitlab_source(repo: &Repository, value: &Value, cancel: &Cancellation) -> Option<String> {
    let source_id = number(value, "source_project_id")?;
    if number(value, "target_project_id") == Some(source_id) {
        return Some(repo.slug.clone());
    }
    let project = get(repo, &format!("projects/{source_id}"), cancel)
        .ok()?
        .body;
    if number(&project, "id") != Some(source_id) {
        return None;
    }
    text(&project, "path_with_namespace").filter(|name| valid_slug(name))
}

fn gitlab_review(
    repo: &Repository,
    path: &str,
    value: &Value,
    cancel: &Cancellation,
) -> Result<Signal> {
    // GitLab explicitly describes this status as reviewers requesting changes.
    if value["detailed_merge_status"].as_str() == Some("requested_changes") {
        return Ok(Signal::new(
            "changesRequested",
            Some("GitLab 记录了变更请求".into()),
        ));
    }
    let approvals = get(repo, &format!("{path}/approvals"), cancel)?.body;
    let users = approvals["approved_by"].as_array().ok_or_else(malformed)?;
    let reviewers = if value["reviewers"].as_array().is_some_and(Vec::is_empty) {
        Ok(Vec::new())
    } else {
        get(repo, &format!("{path}/reviewers"), cancel).and_then(|response| match response.body {
            Value::Array(reviewers) => Ok(reviewers),
            _ => Err(malformed()),
        })
    };
    if reviewers.as_ref().is_ok_and(|reviewers| {
        reviewers
            .iter()
            .any(|reviewer| reviewer["state"].as_str() == Some("requested_changes"))
    }) {
        return Ok(Signal::new(
            "changesRequested",
            Some("GitLab 记录了变更请求".into()),
        ));
    }
    if approvals["approvals_left"]
        .as_u64()
        .is_some_and(|left| left > 0)
        || value["detailed_merge_status"].as_str() == Some("not_approved")
    {
        return Ok(Signal::new(
            "reviewRequired",
            Some("GitLab 报告仍需审批".into()),
        ));
    }
    if !users.is_empty() {
        let mut signal = match approvals["approved"].as_bool() {
            Some(true) => Signal::new("approved", Some("GitLab 记录了审批".into())),
            Some(false) => Signal::new(
                "neutral",
                Some("GitLab 记录了审批，审批规则尚未满足".into()),
            ),
            None => Signal::new(
                "unavailable",
                Some("GitLab 记录了审批，无法核实审批结果".into()),
            ),
        };
        signal.at = users
            .iter()
            .filter_map(|user| user["approved_at"].as_str())
            .filter_map(|date| OffsetDateTime::parse(date, &Rfc3339).ok())
            .max()
            .and_then(|date| date.to_offset(time::UtcOffset::UTC).format(&Rfc3339).ok());
        return Ok(signal);
    }
    let reviewers = reviewers?;
    let mut pending = false;
    for reviewer in &reviewers {
        match reviewer["state"].as_str() {
            Some("unreviewed" | "review_started") => pending = true,
            Some("reviewed" | "unapproved") => {}
            _ => return Err(malformed()),
        }
    }
    match (pending, reviewers.is_empty()) {
        (true, _) => Ok(Signal::new(
            "reviewRequired",
            Some("GitLab 记录了待完成的评审".into()),
        )),
        (false, false) => Ok(Signal::new("neutral", Some("GitLab 记录了评审活动".into()))),
        (false, true) => Ok(Signal::new("noRecords", None)),
    }
}

fn ci_status(value: &str) -> &'static str {
    match value {
        "success" => "passing",
        "failure" | "error" | "failed" | "timed_out" | "startup_failure" => "failing",
        "expected"
        | "pending"
        | "queued"
        | "requested"
        | "in_progress"
        | "waiting"
        | "action_required"
        | "created"
        | "preparing"
        | "running"
        | "scheduled"
        | "waiting_for_resource"
        | "manual" => "pending",
        "skipped" => "skipped",
        "cancelled" | "canceled" => "cancelled",
        "neutral" | "stale" => "neutral",
        _ => "unavailable",
    }
}

fn rank_ci(status: &str) -> u8 {
    match status {
        "failing" => 8,
        "unavailable" => 7,
        "pending" => 6,
        "cancelled" => 5,
        "passing" => 4,
        "neutral" => 3,
        "skipped" => 2,
        _ => 0,
    }
}

fn github_checks(
    repo: &Repository,
    base: &str,
    sha: &str,
    cancel: &Cancellation,
) -> Result<Signal> {
    const PER_PAGE: usize = 100;
    let mut strongest = Signal::new("noRecords", None);
    let mut received = 0;
    for page in 1..=5 {
        let response = get(
            repo,
            &format!(
                "{base}/commits/{sha}/check-runs?filter=latest&per_page={PER_PAGE}&page={page}"
            ),
            cancel,
        )?;
        let checks = response.body["check_runs"]
            .as_array()
            .ok_or_else(malformed)?;
        received += checks.len() as u64;
        for check in checks {
            if check.get("head_sha").is_some() && commit(&check["head_sha"]) != Some(sha) {
                return Err(malformed());
            }
            let status = match check["status"].as_str() {
                Some("completed") => check["conclusion"]
                    .as_str()
                    .map_or("unavailable", ci_status),
                Some(status) => ci_status(status),
                None => "unavailable",
            };
            let mut signal = Signal::new(status, text(check, "name"));
            signal.at = timestamp(check, "completed_at").or_else(|| timestamp(check, "started_at"));
            let timestamp = |signal: &Signal| {
                signal
                    .at
                    .as_deref()
                    .and_then(|date| OffsetDateTime::parse(date, &Rfc3339).ok())
            };
            if (rank_ci(signal.status), timestamp(&signal))
                > (rank_ci(strongest.status), timestamp(&strongest))
            {
                strongest = signal;
            }
        }
        // A confirmed failure is useful even when the result has more pages.
        if strongest.status == "failing" || response.body["total_count"].as_u64() == Some(received)
        {
            return Ok(strongest);
        }
        if next_page(&response.headers, page, checks.len(), PER_PAGE)?.is_none() {
            break;
        }
    }
    Err(read_error(
        "limit",
        "GitHub 检查结果未完整读取，请在原站核对",
    ))
}

fn github_commit_status(
    repo: &Repository,
    base: &str,
    sha: &str,
    cancel: &Cancellation,
) -> Result<Signal> {
    let response = get(
        repo,
        &format!("{base}/commits/{sha}/status?per_page=1"),
        cancel,
    )?;
    let body = response.body;
    let statuses = body["statuses"].as_array().ok_or_else(malformed)?;
    let status = match (body["total_count"].as_u64(), statuses.is_empty()) {
        (Some(0), true) => "noRecords",
        (Some(1..), false) => body["state"].as_str().map_or("unavailable", ci_status),
        _ => "unavailable",
    };
    let mut signal = Signal::new(status, None);
    signal.at = statuses
        .first()
        .filter(|entry| {
            entry["state"]
                .as_str()
                .is_some_and(|value| ci_status(value) == status)
        })
        .and_then(|entry| timestamp(entry, "updated_at"));
    Ok(signal)
}

fn github_commit_ci(repo: &Repository, base: &str, sha: &str, cancel: &Cancellation) -> Signal {
    let checks = github_checks(repo, base, sha, cancel).unwrap_or_else(Signal::unavailable);
    let status = github_commit_status(repo, base, sha, cancel).unwrap_or_else(Signal::unavailable);
    let checks_rank = rank_ci(checks.status);
    let status_rank = rank_ci(status.status);
    let partly_unavailable = [checks.status, status.status].contains(&"unavailable");
    let mut signal = if checks_rank >= status_rank {
        checks
    } else {
        status
    };
    if signal.status == "failing" && partly_unavailable {
        signal.message = Some("GitHub 记录了失败，部分检查不可读取".into());
    }
    signal.commit = Some(sha.into());
    signal
}

fn github_ci(
    repo: &Repository,
    base: &str,
    path: &str,
    value: &Value,
    cancel: &Cancellation,
) -> (Signal, Vec<Signal>) {
    let Some(head) = head_commit(Platform::Github, value) else {
        return (
            Signal::new("unavailable", Some("GitHub 未提供当前提交".into())),
            Vec::new(),
        );
    };
    let mut current = github_commit_ci(repo, base, head, cancel);
    if !matches!(current.status, "noRecords" | "unavailable") {
        return (current, Vec::new());
    }

    const COMMIT_LIMIT: u64 = 250;
    const COMMITS_PER_PAGE: u64 = 50;
    const HISTORICAL_LOOKUPS: usize = 8;
    let total = number(value, "commits");
    let mut incomplete = total.is_none_or(|total| total > COMMIT_LIMIT);
    let last_page = total
        .unwrap_or(COMMITS_PER_PAGE)
        .min(COMMIT_LIMIT)
        .div_ceil(COMMITS_PER_PAGE);
    let mut visited = std::collections::HashSet::new();
    let mut history = Vec::new();
    'pages: for page in (1..=last_page).rev() {
        if total == Some(1) {
            break;
        }
        let Ok(response) = get(
            repo,
            &format!("{path}/commits?per_page={COMMITS_PER_PAGE}&page={page}"),
            cancel,
        ) else {
            incomplete = true;
            break;
        };
        let Some(commits) = response.body.as_array() else {
            incomplete = true;
            break;
        };
        for item in commits.iter().rev() {
            let Some(sha) = commit(&item["sha"]) else {
                incomplete = true;
                continue;
            };
            if sha == head || visited.contains(sha) {
                continue;
            }
            if visited.len() >= HISTORICAL_LOOKUPS {
                incomplete = true;
                break 'pages;
            }
            visited.insert(sha.to_owned());
            let signal = github_commit_ci(repo, base, sha, cancel);
            match signal.status {
                "unavailable" => incomplete = true,
                "noRecords" => {}
                _ => {
                    history.push(signal);
                    break 'pages;
                }
            }
        }
    }
    if incomplete {
        append_history_notice(&mut current, "GitHub 历史 CI 读取范围受限，请在原站核对");
    }
    (current, history)
}

fn pipeline(value: &Value) -> Signal {
    let mut signal = Signal::new(
        value["status"].as_str().map_or("unavailable", ci_status),
        number(value, "id").map(|id| format!("GitLab 流水线 #{id}")),
    );
    signal.commit = commit(&value["sha"]).map(str::to_owned);
    signal.at = timestamp(value, "finished_at")
        .or_else(|| timestamp(value, "updated_at"))
        .or_else(|| timestamp(value, "created_at"));
    signal
}

fn gitlab_ci(
    repo: &Repository,
    base: &str,
    path: &str,
    value: &Value,
    iid: u64,
    cancel: &Cancellation,
) -> (Signal, Vec<Signal>) {
    let Some(head) = head_commit(Platform::Gitlab, value) else {
        return (
            Signal::new("unavailable", Some("GitLab 未提供当前提交".into())),
            Vec::new(),
        );
    };
    let ref_prefix = format!("refs/merge-requests/{iid}/");
    let merged_result = |item: &Value| {
        matches!(
            item["ref"]
                .as_str()
                .and_then(|reference| reference.strip_prefix(&ref_prefix)),
            Some("merge" | "train")
        )
    };
    let platform_head = &value["head_pipeline"];
    let pipeline_head = commit(&platform_head["sha"]);
    let head_matches = pipeline_head.and_then(|sha| {
        if sha == head || !merged_result(platform_head) {
            return Some(sha == head);
        }
        // GitLab uses a shared ref for successive transient merge commits.
        // Verify the immutable merge commit's source parent before using its CI.
        let commit_base = number(platform_head, "project_id")
            .map_or_else(|| base.to_owned(), |id| format!("projects/{id}"));
        let merged = get(
            repo,
            &format!("{commit_base}/repository/commits/{sha}"),
            cancel,
        )
        .ok()?
        .body;
        let parents = merged["parent_ids"].as_array()?;
        if commit(&merged["id"]) != Some(sha) || parents.len() != 2 || commit(&parents[0]).is_none()
        {
            return None;
        }
        commit(&parents[1]).map(|source| source == head)
    });
    if head_matches == Some(true) {
        return (pipeline(platform_head), Vec::new());
    }
    const PIPELINES_PER_PAGE: usize = 50;
    const HISTORICAL_PAGES: u32 = 5;
    let mut history = Vec::new();
    if head_matches == Some(false) {
        let previous = pipeline(platform_head);
        if previous.status != "unavailable" {
            history.push(previous);
        }
    }
    let mut unknown_sha = !platform_head.is_null() && head_matches.is_none();
    let mut page = 1;
    let mut current = loop {
        let response = match get(
            repo,
            &format!("{path}/pipelines?per_page={PIPELINES_PER_PAGE}&page={page}"),
            cancel,
        ) {
            Ok(response) => response,
            Err(error) => {
                let mut signal = Signal::unavailable(error);
                append_history_notice(&mut signal, "GitLab 历史 CI 未读完，请在原站核对");
                break signal;
            }
        };
        let Some(pipelines) = response.body.as_array() else {
            break Signal::unavailable(malformed());
        };
        if let Some(current) = pipelines
            .iter()
            .filter(|item| commit(&item["sha"]) == Some(head))
            .max_by_key(|item| item["id"].as_u64())
        {
            return (pipeline(current), Vec::new());
        }
        let mut ordered: Vec<_> = pipelines.iter().collect();
        ordered.sort_by_key(|entry| std::cmp::Reverse(entry["id"].as_u64()));
        for previous in ordered {
            let signal = pipeline(previous);
            let verified_previous = !merged_result(previous)
                || (signal.commit.as_deref() == pipeline_head && head_matches == Some(false));
            unknown_sha |= signal.commit.is_none() || !verified_previous;
            if signal.commit.is_some()
                && verified_previous
                && signal.status != "unavailable"
                && history.len() < 3
                && !history.iter().any(|item| item.commit == signal.commit)
            {
                history.push(signal);
            }
        }
        match next_page(&response.headers, page, pipelines.len(), PIPELINES_PER_PAGE) {
            Ok(None) if !unknown_sha && value.get("head_pipeline").is_some() => {
                break Signal::new("noRecords", None);
            }
            Ok(None) => {
                break Signal::new(
                    "unavailable",
                    Some("无法核实 GitLab 当前提交的流水线，请在原站核对".into()),
                );
            }
            Ok(Some(next)) if next <= HISTORICAL_PAGES => page = next,
            Ok(Some(_)) => {
                break Signal::new(
                    "unavailable",
                    Some("GitLab 流水线读取范围受限，请在原站核对".into()),
                );
            }
            Err(error) => break Signal::unavailable(error),
        }
    };
    current.commit = Some(head.into());
    (current, history)
}

fn append_history_notice(signal: &mut Signal, notice: &str) {
    signal.message = Some(match &signal.message {
        Some(message) => format!("{message}。{notice}"),
        None => notice.into(),
    });
}
