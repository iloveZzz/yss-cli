use super::{
    jobs::{JobManager, JobRecord},
    process, sources,
    store::Store,
};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{self, Root},
    model::{RelPath, Sha256},
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    path::Path,
    sync::{Arc, Mutex},
    time::Duration,
};

fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
fn lock(store: &Arc<Mutex<Store>>) -> Result<std::sync::MutexGuard<'_, Store>> {
    store.lock().map_err(|_| invalid("工作台存储锁异常"))
}
fn text(root: &Root, path: &str) -> Result<String> {
    let checked = root.read_checked(&RelPath::new(path)?, 2 * 1024 * 1024)?;
    String::from_utf8(checked.bytes).map_err(|_| invalid("资料必须为 UTF-8"))
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct HomeRow {
    pub id: String,
    pub responsibility: String,
    pub git: String,
    pub documents: String,
    pub relations: String,
    #[serde(default)]
    pub hints: String,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ColumnMapping {
    pub id: Option<String>,
    pub responsibility: Option<String>,
    pub git: Option<String>,
    pub documents: Option<String>,
    pub relations: Option<String>,
    pub hints: Option<String>,
}
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ImportHome {
    pub root: String,
    pub source_path: Option<String>,
    pub column_mapping: Option<ColumnMapping>,
    pub rows: Option<Vec<HomeRow>>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct HomeImport {
    pub id: String,
    pub root: String,
    pub source_path: String,
    pub source_digest: String,
    pub rows: Vec<HomeRow>,
    pub warnings: Vec<String>,
    pub requires_review: bool,
    pub imported_at: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct HomeBinding {
    pub entry_id: String,
    pub root_path: String,
    #[serde(default)]
    pub project_id: Option<String>,
}
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct BindHome {
    pub home_id: String,
    pub source_digest: String,
    pub bindings: Vec<HomeBinding>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct BoundHome {
    pub home_id: String,
    pub source_digest: String,
    pub bindings: Vec<HomeBinding>,
    pub updated_at: String,
}
fn cells(line: &str) -> Vec<String> {
    line.trim()
        .trim_matches('|')
        .split('|')
        .map(|s| s.trim().trim_matches('`').to_owned())
        .collect()
}
fn index(headers: &[String], explicit: &Option<String>, aliases: &[&str]) -> Option<usize> {
    headers.iter().position(|h| {
        explicit
            .as_ref()
            .map_or_else(|| aliases.contains(&h.as_str()), |e| e == h)
    })
}
fn parse_rows(source: &str, mapping: &ColumnMapping) -> (Vec<HomeRow>, Vec<String>) {
    let lines: Vec<_> = source.lines().collect();
    let mut rows = Vec::new();
    let mut warnings = Vec::new();
    for (line_number, line) in lines.iter().enumerate() {
        if !line.trim().starts_with('|') {
            continue;
        }
        let headers = cells(line);
        let Some(id) = index(&headers, &mapping.id, &["仓库标识", "工程标识"]) else {
            continue;
        };
        let responsibility = index(&headers, &mapping.responsibility, &["职责"]);
        let git = index(&headers, &mapping.git, &["Git 身份", "Git 身份与子路径"]);
        let documents = index(&headers, &mapping.documents, &["资料入口", "资料入口示意"]);
        let relations = index(&headers, &mapping.relations, &["关联", "已知关系示意"]);
        let hints = index(&headers, &mapping.hints, &["查找提示（可选）", "查找提示"]);
        if responsibility.is_none() || git.is_none() || documents.is_none() || relations.is_none() {
            warnings.push(format!("第 {} 行表头需要核对字段映射", line_number + 1));
            continue;
        }
        let mut first = true;
        for row in lines
            .iter()
            .skip(line_number + 1)
            .take_while(|l| l.trim().starts_with('|'))
        {
            let values = cells(row);
            if first
                && values
                    .iter()
                    .all(|c| c.chars().all(|ch| matches!(ch, '-' | ':' | ' ')))
            {
                first = false;
                continue;
            }
            first = false;
            if values.len() != headers.len() {
                warnings.push(format!(
                    "第 {} 行之后存在列数不一致，请提供核对行",
                    line_number + 1
                ));
                continue;
            }
            let get = |i: Option<usize>| i.and_then(|i| values.get(i)).cloned().unwrap_or_default();
            rows.push(HomeRow {
                id: values[id].clone(),
                responsibility: get(responsibility),
                git: get(git),
                documents: get(documents),
                relations: get(relations),
                hints: get(hints),
            });
        }
    }
    if rows.is_empty() {
        warnings.push("未可靠识别仓库地图，请提供字段映射或人工核对后的 rows".into());
    }
    for row in &rows {
        if row_remote(row).is_none() {
            warnings.push(format!(
                "{} 的 Git 身份需要人工核对，请提供核对后的 rows",
                row.id
            ));
        }
    }
    (rows, warnings)
}
fn validate_rows(rows: &[HomeRow]) -> Result<()> {
    if rows.len() > 256 {
        return Err(invalid("仓库地图最多导入256行"));
    }
    let mut ids = std::collections::BTreeSet::new();
    for row in rows {
        if row.id.trim().is_empty() || row.id.len() > 128 || !ids.insert(&row.id) {
            return Err(invalid("地图标识需要非空且唯一"));
        }
        for value in [
            &row.id,
            &row.responsibility,
            &row.git,
            &row.documents,
            &row.relations,
            &row.hints,
        ] {
            if value.len() > 8192 || value.chars().any(|c| c.is_control() && !matches!(c, '\t')) {
                return Err(invalid("仓库地图字段过长或包含控制字符"));
            }
        }
        for candidate in row.git.split(|c: char| {
            c.is_whitespace()
                || matches!(
                    c,
                    '`' | '<' | '>' | '(' | ')' | '[' | ']' | '；' | '，' | ';' | ','
                )
        }) {
            if candidate.contains("://")
                && reqwest::Url::parse(candidate).is_ok_and(|u| {
                    u.password().is_some()
                        || u.query().is_some()
                        || u.fragment().is_some()
                        || (!u.username().is_empty()
                            && !(u.scheme() == "ssh" && u.username() == "git"))
                })
            {
                return Err(invalid("Git 身份不得包含凭据"));
            }
        }
    }
    Ok(())
}
pub fn import_home(store: &Arc<Mutex<Store>>, request: ImportHome) -> Result<HomeImport> {
    if !Path::new(&request.root).is_absolute() {
        return Err(invalid("Home目录必须为绝对路径"));
    }
    let root = Root::open(Path::new(&request.root))?;
    let path = match request.source_path {
        Some(path) => path,
        None => {
            if root.inspect(&RelPath::new("REPOSITORIES.md")?)? == crate::fs::FileSnapshot::Missing
            {
                "README.md".into()
            } else {
                "REPOSITORIES.md".into()
            }
        }
    };
    let source = text(&root, &path)?;
    let (rows, warnings) = match request.rows {
        Some(rows) => (rows, Vec::new()),
        None => parse_rows(&source, &request.column_mapping.unwrap_or_default()),
    };
    validate_rows(&rows)?;
    root.revalidate()?;
    let existing = lock(store)?
        .records::<HomeImport>("homeImport")?
        .into_iter()
        .find(|h| h.root == request.root && h.source_path == path);
    let home = HomeImport {
        id: existing.map_or_else(fs::unique_id, |h| Ok(h.id))?,
        root: root.path.display().to_string(),
        source_path: path,
        source_digest: Sha256::of(source.as_bytes()).as_str().into(),
        requires_review: !warnings.is_empty(),
        rows,
        warnings,
        imported_at: sources::now(),
    };
    lock(store)?.put_record("homeImport", &home.id, &home)?;
    Ok(home)
}
pub fn homes(store: &Arc<Mutex<Store>>) -> Result<Value> {
    let (imports, bindings) = {
        let mut store = lock(store)?;
        (
            store.records::<HomeImport>("homeImport")?,
            store.records::<BoundHome>("homeBinding")?,
        )
    };
    let mut views = Vec::new();
    for home in imports {
        let current = Root::open(Path::new(&home.root)).and_then(|r| text(&r, &home.source_path));
        let stale = current
            .as_ref()
            .map(|t| Sha256::of(t.as_bytes()).as_str() != home.source_digest)
            .unwrap_or(true);
        let binding = bindings.iter().find(|b| b.home_id == home.id);
        let mut view = serde_json::to_value(&home).map_err(|_| invalid("无法编码Home"))?;
        view["stale"] = json!(stale);
        view["bindings"] = json!(binding.map(|b| &b.bindings).cloned().unwrap_or_default());
        view["sourceError"] = json!(current.err());
        views.push(view);
    }
    Ok(json!({"homes":views}))
}
fn remote_identity(raw: &str) -> Option<String> {
    let raw = raw.trim().trim_matches('`');
    let parsed = if raw.contains("://") {
        reqwest::Url::parse(raw).ok()?
    } else if let Some((host, path)) = raw.strip_prefix("git@").and_then(|s| s.split_once(':')) {
        reqwest::Url::parse(&format!("ssh://{host}/{path}")).ok()?
    } else {
        return None;
    };
    if parsed.password().is_some()
        || parsed.query().is_some()
        || parsed.fragment().is_some()
        || (!parsed.username().is_empty() && parsed.username() != "git")
    {
        return None;
    }
    Some(
        format!(
            "{}{}{}",
            parsed.host_str()?,
            parsed.port().map(|p| format!(":{p}")).unwrap_or_default(),
            parsed.path().trim_end_matches('/').trim_end_matches(".git")
        )
        .to_ascii_lowercase(),
    )
}
fn row_remote(row: &HomeRow) -> Option<String> {
    row.git
        .split(|c: char| {
            c.is_whitespace() || matches!(c, '`' | '<' | '>' | '(' | ')' | '；' | '，' | ';' | ',')
        })
        .find_map(remote_identity)
}
fn row_subpath(row: &HomeRow) -> Option<String> {
    let paths: Vec<_> = row
        .git
        .split('`')
        .map(|part| part.trim().trim_end_matches('/'))
        .filter(|part| {
            *part == "."
                || (part.contains('/')
                    && !part.contains(":")
                    && !part.chars().any(char::is_whitespace)
                    && RelPath::new(*part).is_ok())
        })
        .collect();
    if paths.len() == 1 {
        Some(paths[0].trim_end_matches('/').to_owned())
    } else {
        None
    }
}
pub fn bind_home(store: &Arc<Mutex<Store>>, mut request: BindHome) -> Result<Value> {
    let home: HomeImport = lock(store)?
        .record("homeImport", &request.home_id)?
        .ok_or_else(|| invalid("Home导入记录不存在"))?;
    let root = Root::open(Path::new(&home.root))?;
    let source = text(&root, &home.source_path)?;
    if home.source_digest != request.source_digest
        || Sha256::of(source.as_bytes()).as_str() != request.source_digest
    {
        return Err(YssError::new(
            ErrorCode::ConcurrentChange,
            "Home地图已改变，请重新导入核对",
        ));
    }
    if home.requires_review {
        return Err(invalid("请先用字段映射或核对行完成地图导入"));
    }
    let mut entries = std::collections::BTreeSet::new();
    for binding in &request.bindings {
        let row = home
            .rows
            .iter()
            .find(|r| r.id == binding.entry_id)
            .ok_or_else(|| invalid("地图行不存在"))?;
        if !entries.insert(&binding.entry_id) {
            return Err(invalid("同一地图行只能绑定一次"));
        }
        if !Path::new(&binding.root_path).is_absolute() {
            return Err(invalid("绑定工程目录必须为绝对路径"));
        }
        let actual = Root::open(Path::new(&binding.root_path))?;
        if let Some(expected) = row_remote(row) {
            let output = process::run(
                "git",
                &[
                    "-C".into(),
                    actual.path.display().to_string(),
                    "remote".into(),
                    "get-url".into(),
                    "origin".into(),
                ],
                &Cancellation::new(),
                Duration::from_secs(10),
            )?;
            let found = std::str::from_utf8(&output).ok().and_then(remote_identity);
            if found.as_ref() != Some(&expected) {
                return Err(
                    YssError::new(ErrorCode::Conflict, "绑定目录的Git身份与地图不一致")
                        .at(&binding.entry_id),
                );
            }
        }
        if let Some(subpath) = row_subpath(row) {
            let output = process::run(
                "git",
                &[
                    "-C".into(),
                    actual.path.display().to_string(),
                    "rev-parse".into(),
                    "--show-toplevel".into(),
                ],
                &Cancellation::new(),
                Duration::from_secs(10),
            )?;
            let git_root = std::str::from_utf8(&output)
                .map_err(|_| invalid("Git根目录不是UTF-8"))?
                .trim();
            let expected = if subpath == "." {
                Path::new(git_root).to_path_buf()
            } else {
                Path::new(git_root).join(subpath)
            };
            if fs::absolute(&expected)? != actual.path {
                return Err(YssError::new(
                    ErrorCode::Conflict,
                    "绑定目录与地图中的仓内子路径不一致",
                )
                .at(&binding.entry_id));
            }
        }
        actual.revalidate()?;
    }
    root.revalidate()?;
    // All source, path and remote checks finish before registering local targets.
    // A retry reuses records by root when a later registration or cache save failed.
    for binding in &mut request.bindings {
        let mut store = lock(store)?;
        let existing = store
            .state()
            .projects
            .iter()
            .find(|project| fs::alias_key(&project.root_path) == fs::alias_key(&binding.root_path))
            .map(|project| project.id.clone());
        binding.project_id = match existing {
            Some(id) => Some(id),
            None => {
                let revision = store.state().revision;
                let result = super::service::mutate(
                    &mut store,
                    super::model::MutationRequest {
                        expected_revision: revision,
                        expected_target: None,
                        request_id: None,
                        action: super::model::Action::AddProject {
                            root_path: binding.root_path.clone(),
                            name: Some(binding.entry_id.clone()),
                        },
                    },
                )?;
                result.project_id
            }
        };
    }
    let record = BoundHome {
        home_id: home.id.clone(),
        source_digest: home.source_digest.clone(),
        bindings: request.bindings,
        updated_at: sources::now(),
    };
    lock(store)?.put_record("homeBinding", &home.id, &record)?;
    let mut result = json!(home);
    result["bindings"] = json!(record.bindings);
    result["stale"] = json!(false);
    result["boundAt"] = json!(record.updated_at);
    Ok(result)
}

pub fn tracker(root: &str) -> Result<Value> {
    let root = Root::open(Path::new(root))?;
    let path = ".template-spec/agents/issue-tracker.md";
    if root.inspect(&RelPath::new(path)?)? == fs::FileSnapshot::Missing {
        return Ok(Value::Null);
    }
    let source = match text(&root, path) {
        Ok(source) => source,
        Err(error) if error.context.get("kind").is_some_and(|k| k == "NotFound") => {
            return Ok(Value::Null);
        }
        Err(error) => return Err(error),
    };
    let mut fields = BTreeMap::new();
    for line in source.lines() {
        let line = line.trim().trim_start_matches('-').trim();
        if let Some((key, value)) = line.split_once(':').or_else(|| line.split_once('：')) {
            let key = key.trim().trim_matches('`').to_ascii_lowercase();
            if [
                "platform",
                "provider",
                "host",
                "url",
                "project",
                "projectid",
                "projectpath",
                "repository",
                "root",
                "平台",
                "实例",
                "项目",
                "项目路径",
            ]
            .contains(&key.as_str())
            {
                let value = value.trim().trim_matches(['`', '\"', '\'']);
                let safe_url = if value.contains("://") {
                    reqwest::Url::parse(value).is_ok_and(|url| {
                        url.username().is_empty()
                            && url.password().is_none()
                            && url.query().is_none()
                    })
                } else {
                    true
                };
                if value.len() < 2048 && safe_url && !value.chars().any(char::is_control) {
                    fields.insert(key, value.to_owned());
                }
            }
        }
    }
    Ok(json!({"sourcePath":path,"fields":fields,"sourceDigest":Sha256::of(source.as_bytes())}))
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum Provider {
    Github,
    Gitlab,
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub enum ExternalKind {
    Issue,
    PullRequest,
    Pipeline,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AddExternal {
    pub provider: Provider,
    pub host: String,
    pub project: String,
    pub kind: ExternalKind,
    pub number: u64,
    pub project_id: Option<String>,
    pub spec_id: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ExternalLink {
    pub id: String,
    #[serde(flatten)]
    pub target: AddExternal,
    pub status: String,
    pub last_attempt_at: Option<String>,
    pub last_success_at: Option<String>,
    pub last_error: Option<String>,
    pub data: Option<Value>,
}
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct RefreshExternal {
    pub id: String,
    pub request_id: String,
}
struct Instance {
    origin: String,
    hostname: String,
}
fn instance(host: &str) -> Result<Instance> {
    let value = if host.contains("://") {
        host.to_owned()
    } else {
        format!("https://{host}")
    };
    let url = reqwest::Url::parse(&value).map_err(|_| invalid("实例地址无效"))?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(invalid("实例仅支持包含可选端口的HTTP(S)地址"));
    }
    let hostname = url
        .host_str()
        .ok_or_else(|| invalid("实例缺少主机名"))?
        .to_owned();
    Ok(Instance {
        origin: url.as_str().trim_end_matches('/').into(),
        hostname,
    })
}
fn validate_external(target: &AddExternal) -> Result<Instance> {
    let host = instance(&target.host)?;
    if target.number == 0
        || target.project.len() > 512
        || target.project.split('/').count() < 2
        || !target.project.split('/').all(|part| {
            !part.is_empty()
                && part != "."
                && part != ".."
                && part
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b'-'))
        })
    {
        return Err(invalid("请输入明确的项目路径和正整数编号"));
    }
    if target.provider == Provider::Github && target.project.split('/').count() != 2 {
        return Err(invalid("GitHub项目应为owner/repository"));
    }
    Ok(host)
}
pub fn external_list(store: &Arc<Mutex<Store>>) -> Result<Vec<ExternalLink>> {
    lock(store)?.records("externalLink")
}
pub fn external_add(store: &Arc<Mutex<Store>>, mut target: AddExternal) -> Result<ExternalLink> {
    let instance = validate_external(&target)?;
    target.host = instance.origin;
    let mut store = lock(store)?;
    if let Some(id) = &target.project_id
        && !store.state().projects.iter().any(|p| &p.id == id)
    {
        return Err(invalid("关联工程不存在"));
    }
    if let Some(id) = &target.spec_id {
        let spec = store
            .state()
            .specs
            .iter()
            .find(|s| &s.id == id)
            .ok_or_else(|| invalid("关联Spec不存在"))?;
        if target
            .project_id
            .as_ref()
            .is_some_and(|id| id != &spec.project_id)
        {
            return Err(invalid("关联Spec与工程不一致"));
        }
    }
    if let Some(found) = store
        .records::<ExternalLink>("externalLink")?
        .into_iter()
        .find(|r| {
            r.target.provider == target.provider
                && r.target.host == target.host
                && r.target.project == target.project
                && r.target.kind == target.kind
                && r.target.number == target.number
                && r.target.spec_id == target.spec_id
                && r.target.project_id == target.project_id
        })
    {
        return Ok(found);
    }
    let record = ExternalLink {
        id: fs::unique_id()?,
        target,
        status: "unread".into(),
        last_attempt_at: None,
        last_success_at: None,
        last_error: None,
        data: None,
    };
    store.put_record("externalLink", &record.id, &record)?;
    Ok(record)
}
fn percent(value: &str) -> String {
    value
        .bytes()
        .map(|b| {
            if b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b'-') {
                (b as char).to_string()
            } else {
                format!("%{b:02X}")
            }
        })
        .collect()
}
fn api(target: &AddExternal, path: &str, cancel: &Cancellation) -> Result<Value> {
    let instance = validate_external(target)?;
    let (program, prefix) = match target.provider {
        Provider::Github => (
            "gh",
            if instance.origin == "https://github.com" {
                "https://api.github.com".to_owned()
            } else {
                format!("{}/api/v3", instance.origin)
            },
        ),
        Provider::Gitlab => ("glab", format!("{}/api/v4", instance.origin)),
    };
    let endpoint = format!("{prefix}/{path}");
    let bytes = process::run(
        program,
        &[
            "api".into(),
            "--hostname".into(),
            instance.hostname,
            "--method".into(),
            "GET".into(),
            endpoint,
        ],
        cancel,
        Duration::from_secs(25),
    )?;
    serde_json::from_slice(&bytes)
        .map_err(|_| YssError::new(ErrorCode::Network, "外部服务返回了无效JSON"))
}
fn safe_scalar(value: &Value, key: &str) -> Value {
    match value.get(key) {
        Some(Value::String(s)) => {
            Value::String(s.chars().filter(|c| !c.is_control()).take(2048).collect())
        }
        Some(Value::Number(n)) => Value::Number(n.clone()),
        Some(Value::Bool(b)) => Value::Bool(*b),
        _ => Value::Null,
    }
}
fn summary(value: &Value) -> Value {
    let mut out = serde_json::Map::new();
    for key in [
        "id",
        "iid",
        "number",
        "title",
        "name",
        "state",
        "status",
        "conclusion",
        "updated_at",
        "created_at",
        "merged_at",
        "ref",
        "sha",
        "head_branch",
    ] {
        let item = safe_scalar(value, key);
        if !item.is_null() {
            out.insert(key.into(), item);
        }
    }
    Value::Object(out)
}
fn paged(
    target: &AddExternal,
    path: &str,
    key: Option<&str>,
    cancel: &Cancellation,
) -> Result<Value> {
    let mut items = Vec::new();
    let mut truncated = false;
    for page in 1..=3 {
        cancel.check()?;
        let result = api(target, &format!("{path}?per_page=50&page={page}"), cancel)?;
        let page_items = key
            .and_then(|k| result.get(k))
            .unwrap_or(&result)
            .as_array()
            .ok_or_else(|| YssError::new(ErrorCode::Network, "外部服务列表格式无效"))?;
        items.extend(page_items.iter().map(summary));
        if page_items.len() < 50 {
            break;
        }
        if page == 3 {
            truncated = true;
        }
    }
    Ok(json!({"items":items,"truncated":truncated,"pageLimit":3}))
}
fn fetch(target: &AddExternal, cancel: &Cancellation) -> Result<Value> {
    let instance = validate_external(target)?;
    let base = match target.provider {
        Provider::Github => format!("repos/{}", target.project),
        Provider::Gitlab => format!("projects/{}", percent(&target.project)),
    };
    let segment = match (target.provider, target.kind) {
        (Provider::Github, ExternalKind::Issue) => "issues",
        (Provider::Github, ExternalKind::PullRequest) => "pulls",
        (Provider::Github, ExternalKind::Pipeline) => "actions/runs",
        (Provider::Gitlab, ExternalKind::Issue) => "issues",
        (Provider::Gitlab, ExternalKind::PullRequest) => "merge_requests",
        (Provider::Gitlab, ExternalKind::Pipeline) => "pipelines",
    };
    let path = format!("{base}/{segment}/{}", target.number);
    let value = api(target, &path, cancel)?;
    let mut data = summary(&value);
    let web_segment = match (target.provider, target.kind) {
        (Provider::Github, ExternalKind::Issue) => "issues",
        (Provider::Github, ExternalKind::PullRequest) => "pull",
        (Provider::Github, ExternalKind::Pipeline) => "actions/runs",
        (Provider::Gitlab, ExternalKind::Issue) => "-/issues",
        (Provider::Gitlab, ExternalKind::PullRequest) => "-/merge_requests",
        (Provider::Gitlab, ExternalKind::Pipeline) => "-/pipelines",
    };
    data["url"] = json!(format!(
        "{}/{}/{web_segment}/{}",
        instance.origin, target.project, target.number
    ));
    match (target.provider, target.kind) {
        (Provider::Github, ExternalKind::PullRequest) => {
            if let Some(sha) = value.pointer("/head/sha").and_then(Value::as_str)
                && (40..=64).contains(&sha.len())
                && sha.bytes().all(|b| b.is_ascii_hexdigit())
            {
                data["checks"] = paged(
                    target,
                    &format!("{base}/commits/{sha}/check-runs"),
                    Some("check_runs"),
                    cancel,
                )?;
            }
        }
        (Provider::Github, ExternalKind::Pipeline) => {
            data["jobs"] = paged(target, &format!("{path}/jobs"), Some("jobs"), cancel)?
        }
        (Provider::Gitlab, ExternalKind::PullRequest) => {
            data["pipelines"] = paged(target, &format!("{path}/pipelines"), None, cancel)?
        }
        (Provider::Gitlab, ExternalKind::Pipeline) => {
            data["jobs"] = paged(target, &format!("{path}/jobs"), None, cancel)?
        }
        _ => {}
    }
    Ok(data)
}
fn update_external(store: &Arc<Mutex<Store>>, link: &ExternalLink) -> Result<()> {
    let mut store = lock(store)?;
    if store
        .record::<ExternalLink>("externalLink", &link.id)?
        .is_none()
    {
        return Err(invalid("外部关联已被删除"));
    }
    store.put_record("externalLink", &link.id, link)
}
pub fn external_refresh(manager: Arc<JobManager>, request: RefreshExternal) -> Result<JobRecord> {
    let link: ExternalLink = lock(&manager.store)?
        .record("externalLink", &request.id)?
        .ok_or_else(|| invalid("外部关联不存在"))?;
    manager.start(
        "externalRefresh".into(),
        format!("读取 {:?} #{}", link.target.kind, link.target.number),
        request.request_id,
        vec![format!("external/{}/{}", link.target.host, link.id)],
        move |context| {
            let mut link = link;
            let now = sources::now();
            if let Some(previous) = &link.last_attempt_at {
                let format = &time::format_description::well_known::Rfc3339;
                let previous = time::OffsetDateTime::parse(previous, format);
                let current = time::OffsetDateTime::parse(&now, format);
                if let (Ok(previous), Ok(current)) = (previous, current)
                    && current - previous < time::Duration::seconds(5)
                {
                    return Err(YssError::new(ErrorCode::Locked, "请间隔5秒后再次刷新"));
                }
            }
            link.last_attempt_at = Some(now);
            link.status = "refreshing".into();
            update_external(&context.store, &link)?;
            context.emit("query", json!({"id":link.id,"provider":link.target.provider,"host":link.target.host,"project":link.target.project}))?;
            match fetch(&link.target, &context.cancel) {
                Ok(data) => {
                    link.data = Some(data);
                    link.last_success_at = Some(sources::now());
                    link.last_error = None;
                    link.status = "current".into();
                    update_external(&context.store, &link)?;
                    Ok(json!(link))
                }
                Err(error) => {
                    link.status = if error.code == ErrorCode::Cancelled { "canceled" } else { "stale" }.into();
                    link.last_error = Some(error.message.clone());
                    update_external(&context.store, &link)?;
                    Err(error)
                }
            }
        },
    )
}
