use super::{
    Instances, Location, Platform, ProjectRecord, Remote, Repository, Snapshot, SourceCache,
    home_sources, lock, providers,
};
use crate::{
    cancel::Cancellation,
    codec::{Limits, decode_yaml},
    error::{ErrorCode, Result},
    fs::Root,
    model::{RelPath, Sha256},
    project::ISSUE_TRACKER_PATH,
    workbench::store::Store,
};
use serde::{Serialize, Serializer, ser::SerializeStruct};
use serde_json::{Map, Value};
use std::{
    collections::BTreeMap,
    path::Path,
    sync::{Arc, Mutex},
};

const INVALID_TRACKER: &str = "Issue 跟踪配置无效或存在冲突，请核对平台、实例和项目路径";
const UNREADABLE_TRACKER: &str = "无法读取已登记工程的 Issue 跟踪配置，请核对工程目录和文件";
const INVALID_WORKTREE: &str = "无法核对已登记 Git 工作树，请检查工程目录和 Git 配置";
const PLATFORM_FIELDS: &[&str] = &["platform", "provider", "平台"];
const HOST_FIELDS: &[&str] = &["host", "实例"];
const PROJECT_FIELDS: &[&str] = &[
    "repository",
    "project",
    "projectpath",
    "projectid",
    "项目",
    "项目路径",
];

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(in crate::workbench) struct IssueInventory {
    pub(in crate::workbench) sources: Vec<IssueSource>,
    pub(in crate::workbench) warnings: Vec<IssueSourceWarning>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(in crate::workbench) struct IssueSourceWarning {
    project_id: String,
    project_name: String,
    message: &'static str,
}

#[derive(Serialize)]
#[serde(rename_all = "lowercase")]
enum IssueBasis {
    Tracker,
    Remote,
}

pub(in crate::workbench) struct IssueSource {
    pub(in crate::workbench) id: String,
    pub(in crate::workbench) repository: Repository,
    project_id: String,
    project_name: String,
    basis: IssueBasis,
}

impl Serialize for IssueSource {
    fn serialize<S: Serializer>(&self, serializer: S) -> std::result::Result<S::Ok, S::Error> {
        let mut source = serializer.serialize_struct("IssueSource", 8)?;
        source.serialize_field("id", &self.id)?;
        source.serialize_field("projectId", &self.project_id)?;
        source.serialize_field("projectName", &self.project_name)?;
        source.serialize_field("platform", &self.repository.platform)?;
        source.serialize_field("origin", &self.repository.origin)?;
        source.serialize_field("slug", &self.repository.slug)?;
        source.serialize_field("url", &self.repository.url)?;
        source.serialize_field("basis", &self.basis)?;
        source.end()
    }
}

impl IssueInventory {
    fn add(&mut self, project: &ProjectRecord, repository: Repository, basis: IssueBasis) {
        let key = format!("workbench-issue\n{}\n{}", repository.id, project.id);
        self.sources.push(IssueSource {
            id: Sha256::of(key.as_bytes()).as_str().to_owned(),
            repository,
            project_id: project.id.clone(),
            project_name: project.name.clone(),
            basis,
        });
    }

    fn warn(&mut self, project: &ProjectRecord, message: &'static str) {
        self.warnings.push(IssueSourceWarning {
            project_id: project.id.clone(),
            project_name: project.name.clone(),
            message,
        });
    }
}

type SourceResult<T> = std::result::Result<T, &'static str>;

fn tracker_key(key: &str) -> bool {
    PLATFORM_FIELDS.contains(&key)
        || HOST_FIELDS.contains(&key)
        || PROJECT_FIELDS.contains(&key)
        || matches!(key, "url" | "root")
}

fn legacy_fields(source: &str) -> SourceResult<Map<String, Value>> {
    let mut fields = Map::new();
    let mut fence = None;
    for raw in source.lines().map(str::trim) {
        if let Some(marker) = ["```", "~~~"]
            .into_iter()
            .find(|marker| raw.starts_with(marker))
        {
            if fence == Some(marker) {
                fence = None;
            } else if fence.is_none() {
                fence = Some(marker);
            }
            continue;
        }
        if fence.is_some() {
            continue;
        }
        let line = raw.strip_prefix("- ").unwrap_or(raw);
        let Some((key, value)) = line.split_once(':').or_else(|| line.split_once('：')) else {
            continue;
        };
        let key = key.trim().trim_matches('`').to_ascii_lowercase();
        if tracker_key(&key) {
            let value = Value::String(value.trim().trim_matches(['`', '"', '\'']).to_owned());
            if fields
                .insert(key, value.clone())
                .is_some_and(|previous| previous != value)
            {
                return Err(INVALID_TRACKER);
            }
        } else if key == "tracker" {
            return Err(INVALID_TRACKER);
        }
    }
    Ok(fields)
}

fn tracker_fields(source: &str) -> SourceResult<Map<String, Value>> {
    let source = source.trim_start_matches('\u{feff}');
    let mut lines = source.lines();
    if lines.next() != Some("---") {
        return legacy_fields(source);
    }
    let mut header = String::new();
    for line in lines.by_ref() {
        if line == "---" {
            let metadata =
                decode_yaml(header.as_bytes(), Limits::yaml()).map_err(|_| INVALID_TRACKER)?;
            let root = metadata.as_object().ok_or(INVALID_TRACKER)?;
            let mut trackers = root
                .iter()
                .filter_map(|(key, value)| key.eq_ignore_ascii_case("tracker").then_some(value));
            if let Some(tracker) = trackers.next() {
                if trackers.next().is_some() {
                    return Err(INVALID_TRACKER);
                }
                return tracker.as_object().cloned().ok_or(INVALID_TRACKER);
            }
            // Older trackers may have stored their fields directly in frontmatter.
            return Ok(root
                .iter()
                .filter_map(|(key, value)| {
                    tracker_key(&key.to_ascii_lowercase()).then(|| (key.clone(), value.clone()))
                })
                .collect());
        }
        header.push_str(line);
        header.push('\n');
    }
    Err(INVALID_TRACKER)
}

fn field<'a>(fields: &'a Map<String, Value>, aliases: &[&str]) -> SourceResult<Option<&'a str>> {
    let mut selected = None;
    for (key, value) in fields {
        if !aliases.iter().any(|name| key.eq_ignore_ascii_case(name)) {
            continue;
        }
        let value = value
            .as_str()
            .or_else(|| value.is_null().then_some(""))
            .ok_or(INVALID_TRACKER)?
            .trim();
        if selected
            .replace(value)
            .is_some_and(|previous| previous != value)
        {
            return Err(INVALID_TRACKER);
        }
    }
    Ok(selected)
}

fn explicit_origin(raw: &str) -> SourceResult<String> {
    let authority = raw.split_once("://").map_or(raw, |(_, rest)| rest);
    if raw.len() > 2048
        || raw.chars().any(|c| c.is_whitespace() || c.is_control())
        || raw.contains(['%', '\\', '?', '#'])
        || authority
            .split_once('/')
            .is_some_and(|(_, path)| !path.is_empty())
    {
        return Err(INVALID_TRACKER);
    }
    providers::configured_origin(raw, "https").ok_or(INVALID_TRACKER)
}

fn tracker_repository(fields: &Map<String, Value>) -> SourceResult<Option<Repository>> {
    if fields.is_empty() {
        return Ok(None);
    }
    let platform = match field(fields, PLATFORM_FIELDS)?
        .map(str::to_ascii_lowercase)
        .as_deref()
    {
        Some("github") => Platform::Github,
        Some("gitlab") => Platform::Gitlab,
        Some("local-markdown") => {
            return Err(
                "该工程的 Issue 跟踪配置使用 Local Markdown，工作台 Issues 支持 GitHub 和 GitLab",
            );
        }
        Some(value) if !value.is_empty() => {
            return Err("该工程的 Issue 跟踪平台不受支持，请核对工程配置");
        }
        _ => return Err(INVALID_TRACKER),
    };
    let project = field(fields, PROJECT_FIELDS)?;

    // Older templates used GitHub with an empty repository and optional draft root.
    if platform == Platform::Github
        && field(fields, &["repository"])? == Some("")
        && field(fields, &["root"]).is_ok()
        && fields.keys().all(|key| {
            matches!(
                key.to_ascii_lowercase().as_str(),
                "platform" | "repository" | "root"
            )
        })
    {
        return Ok(None);
    }

    let host = field(fields, HOST_FIELDS)?
        .map(explicit_origin)
        .transpose()?;
    let url = field(fields, &["url"])?;
    let url_repository = url.and_then(Location::parse);
    let url_origin = match (&url_repository, url) {
        (Some(location), _) => Some(location.web_origin.clone().ok_or(INVALID_TRACKER)?),
        (None, Some(value)) => Some(explicit_origin(value)?),
        (None, None) => None,
    };
    if host
        .as_ref()
        .zip(url_origin.as_ref())
        .is_some_and(|(host, url)| host != url)
    {
        return Err(INVALID_TRACKER);
    }
    let origin = host
        .or(url_origin)
        .or_else(|| (platform == Platform::Github).then(|| "https://github.com".to_owned()))
        .ok_or(INVALID_TRACKER)?;
    let url_slug = url_repository
        .as_ref()
        .map(|location| location.slug.as_str());
    if project
        .zip(url_slug)
        .is_some_and(|(project, url)| match platform {
            Platform::Github => !project.eq_ignore_ascii_case(url),
            Platform::Gitlab => project != url,
        })
    {
        return Err(INVALID_TRACKER);
    }
    let slug = project.or(url_slug).ok_or(INVALID_TRACKER)?;
    let location = Location::parse(&format!("{origin}/{slug}")).ok_or(INVALID_TRACKER)?;
    if location.slug != slug
        || matches!(
            (location.host.as_str(), platform),
            ("github.com", Platform::Gitlab) | ("gitlab.com", Platform::Github)
        )
    {
        return Err(INVALID_TRACKER);
    }
    let mut instances = Instances::default();
    instances.add(&origin, platform);
    instances
        .repository(&location)
        .map(Some)
        .ok_or(INVALID_TRACKER)
}

fn read_tracker(project: &ProjectRecord) -> SourceResult<Option<Repository>> {
    let root = Root::open(Path::new(&project.root_path)).map_err(|_| UNREADABLE_TRACKER)?;
    let path = RelPath::new(ISSUE_TRACKER_PATH).map_err(|_| UNREADABLE_TRACKER)?;
    match root.is_link(&path).map_err(|_| UNREADABLE_TRACKER)? {
        None => return Ok(None),
        Some(true) => return Err(UNREADABLE_TRACKER),
        Some(false) => {}
    }
    let checked = root
        .read_checked(&path, 2 * 1024 * 1024)
        .map_err(|_| UNREADABLE_TRACKER)?;
    let source = std::str::from_utf8(&checked.bytes).map_err(|_| UNREADABLE_TRACKER)?;
    tracker_repository(&tracker_fields(source)?)
}

fn unique_remote(remotes: &[Remote], instances: &Instances) -> SourceResult<Repository> {
    let mut recognized = BTreeMap::new();
    let mut unresolved = false;
    for location in remotes.iter().flat_map(|remote| &remote.locations) {
        if let Some(repository) = location
            .as_ref()
            .and_then(|location| instances.repository(location))
        {
            recognized.insert(repository.id.clone(), repository);
        } else {
            unresolved = true;
        }
    }
    if recognized.len() > 1 {
        return Err("Git 远端指向多个仓库，请为该工程配置 Issue 跟踪仓库");
    }
    if unresolved {
        return Err("无法核对已登记工程的全部 Git 远端身份，请核对平台实例或配置 Issue 跟踪仓库");
    }
    recognized
        .into_values()
        .next()
        .ok_or("没有可核对的 GitHub 或 GitLab 远端，请为该工程配置 Issue 跟踪仓库")
}

pub(in crate::workbench) fn discover_issues(
    store: &Arc<Mutex<Store>>,
    cache: &SourceCache,
    cancel: &Cancellation,
) -> Result<IssueInventory> {
    cancel.check()?;
    let (snapshot, data_dir) = {
        let mut store = lock(store)?;
        (Snapshot::capture(&mut store)?, store.data_dir().to_owned())
    };
    let mut inventory = IssueInventory {
        sources: Vec::new(),
        warnings: Vec::new(),
    };
    let mut needs_remote = Vec::new();
    for project in &snapshot.projects {
        cancel.check()?;
        match read_tracker(project) {
            Ok(Some(repository)) => inventory.add(project, repository, IssueBasis::Tracker),
            Ok(None) => needs_remote.push(project),
            Err(message) => inventory.warn(project, message),
        }
    }
    if !needs_remote.is_empty() {
        let homes = home_sources(&snapshot, cancel)?;
        let mut instances = Instances::new();
        for (origin, platform) in snapshot.external_instances()? {
            instances.add(&origin, platform);
        }
        for source in &inventory.sources {
            instances.add(&source.repository.origin, source.repository.platform);
        }
        cache.retain(&snapshot.projects);
        let config = cache.config(&data_dir, cancel);
        let mut fallback = Vec::new();
        for project in needs_remote {
            cancel.check()?;
            let home = homes.get(&project.id).map(Vec::as_slice).unwrap_or(&[]);
            match cache.remotes(config.as_ref(), project, home, cancel) {
                Ok((remotes, _)) => fallback.push((project, remotes)),
                Err(error) if error.code == ErrorCode::Cancelled => return Err(error),
                Err(_) => inventory.warn(project, INVALID_WORKTREE),
            }
        }
        instances.local_cli(
            &data_dir,
            fallback
                .iter()
                .flat_map(|(_, remotes)| remotes)
                .flat_map(|remote| &remote.locations)
                .flatten(),
            cancel,
        )?;
        for (project, remotes) in fallback {
            match unique_remote(&remotes, &instances) {
                Ok(repository) => inventory.add(project, repository, IssueBasis::Remote),
                Err(message) => inventory.warn(project, message),
            }
        }
    }
    inventory.sources.sort_by(|a, b| {
        a.project_name
            .cmp(&b.project_name)
            .then_with(|| a.project_id.cmp(&b.project_id))
    });
    inventory.warnings.sort_by(|a, b| {
        a.project_name
            .cmp(&b.project_name)
            .then_with(|| a.project_id.cmp(&b.project_id))
    });
    cancel.check()?;
    Ok(inventory)
}
