use super::{Platform, Repository, providers};
use crate::{
    cancel::Cancellation,
    error::{ErrorCode, Result, YssError},
    fs::{Root, git},
    model::{RelPath, Sha256},
    workbench::{
        integrations::{self, BoundHome, ExternalLink, HomeImport, Provider},
        model::ProjectRecord,
        process,
        sqlite::ExtensionRecords,
        store::Store,
    },
};
use reqwest::Url;
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use sha2::Digest;
use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    process::Command,
    sync::{Arc, Mutex, MutexGuard},
    time::Duration,
};

const PREFERENCES: &str = "mergeRequestSources";
const PREFERENCE_ID: &str = "selection";
const RECORD_KINDS: &[&str] = &["homeImport", "homeBinding", "externalLink", PREFERENCES];
const LOCAL_TIMEOUT: Duration = Duration::from_secs(5);

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(in crate::workbench) struct SourceInventory {
    pub(super) repositories: Vec<Repository>,
    projects: Vec<SourceProject>,
    revision: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct SourceProject {
    project_id: String,
    name: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    issue: Option<&'static str>,
    candidates: Vec<Candidate>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Candidate {
    repository_id: String,
    remote: String,
    basis: Basis,
    selected: bool,
    default: bool,
}

#[derive(Clone, Copy, Serialize)]
#[serde(rename_all = "camelCase")]
enum Basis {
    Home,
    Origin,
    Manual,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(in crate::workbench) struct SaveSources {
    revision: u64,
    selected: Vec<Selection>,
}

#[derive(Clone, Eq, Ord, PartialEq, PartialOrd, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Selection {
    project_id: String,
    repository_id: String,
}

#[derive(Default, Eq, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Preferences {
    generation: u64,
    selected: BTreeSet<Selection>,
}

struct Snapshot {
    projects: Vec<ProjectRecord>,
    records: ExtensionRecords,
}

impl Snapshot {
    fn capture(store: &mut Store) -> Result<Self> {
        Ok(Self {
            projects: store
                .state()
                .projects
                .iter()
                .filter(|project| project.active)
                .cloned()
                .collect(),
            records: store.capture_read_records(RECORD_KINDS)?,
        })
    }

    fn records<T: DeserializeOwned>(&self, kind: &str) -> Result<Vec<T>> {
        self.records
            .get(kind)
            .into_iter()
            .flatten()
            .map(|(_, payload)| deserialize_record(payload))
            .collect()
    }

    fn preferences(&self) -> Result<Preferences> {
        self.records
            .get(PREFERENCES)
            .into_iter()
            .flatten()
            .find(|(id, _)| id == PREFERENCE_ID)
            .map(|(_, payload)| deserialize_record(payload))
            .transpose()
            .map(Option::unwrap_or_default)
    }

    fn external_instances(&self) -> Result<BTreeSet<(String, Platform)>> {
        self.records::<ExternalLink>("externalLink").map(|links| {
            links
                .into_iter()
                .filter_map(|link| {
                    let instance = integrations::validate_external(&link.target).ok()?;
                    Some((instance.origin, platform(link.target.provider)))
                })
                .collect()
        })
    }
}

fn deserialize_record<T: DeserializeOwned>(payload: &str) -> Result<T> {
    serde_json::from_str(payload)
        .map_err(|_| YssError::new(ErrorCode::UnsupportedFormat, "合并请求来源相关记录格式无效"))
}

fn lock(store: &Arc<Mutex<Store>>) -> Result<MutexGuard<'_, Store>> {
    store
        .lock()
        .map_err(|_| YssError::new(ErrorCode::Locked, "工作台存储锁异常"))
}

fn platform(provider: Provider) -> Platform {
    match provider {
        Provider::Github => Platform::Github,
        Provider::Gitlab => Platform::Gitlab,
    }
}

#[derive(Clone, Eq, PartialEq, Ord, PartialOrd)]
struct Location {
    host: String,
    port: Option<u16>,
    web_origin: Option<String>,
    slug: String,
}

impl Location {
    fn parse(raw: &str) -> Option<Self> {
        if raw.len() > 2048
            || raw.chars().any(|c| c.is_whitespace() || c.is_control())
            || raw.contains(['%', '\\', '?', '#'])
        {
            return None;
        }
        let url_text;
        let address = if let Some((scheme, _)) = raw.split_once("://") {
            if !matches!(scheme, "http" | "https" | "ssh") {
                return None;
            }
            raw
        } else {
            let (host, path) = raw.strip_prefix("git@")?.split_once(':')?;
            url_text = format!("ssh://git@{host}/{path}");
            &url_text
        };
        // Reject path segments that the URL parser would otherwise silently normalize.
        let raw_path = address.split_once("://")?.1.split_once('/')?.1;
        let slug = raw_path.trim_end_matches('/');
        let slug = slug.strip_suffix(".git").unwrap_or(slug);
        if slug.len() > 512
            || slug.split('/').count() < 2
            || !slug.split('/').all(|part| {
                !part.is_empty()
                    && part != "."
                    && part != ".."
                    && part
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'.' | b'-'))
            })
        {
            return None;
        }
        let url = Url::parse(address).ok()?;
        if url.path().strip_prefix('/') != Some(raw_path) {
            return None;
        }
        let host = url.host_str()?.to_ascii_lowercase();
        let (port, web_origin) = match url.scheme() {
            "ssh" if url.password().is_none() && matches!(url.username(), "" | "git") => {
                (url.port().filter(|port| *port != 22), None)
            }
            "http" | "https" if url.password().is_none() && url.username().is_empty() => {
                (url.port(), Some(url.origin().ascii_serialization()))
            }
            _ => return None,
        };
        Some(Self {
            host,
            port,
            web_origin,
            slug: slug.to_owned(),
        })
    }
}

fn cli_setting(
    tool: &Path,
    data_dir: &Path,
    host: &str,
    key: &str,
    cancel: &Cancellation,
) -> Option<String> {
    let mut command = Command::new(tool);
    command
        .current_dir(data_dir)
        .env("GH_NO_UPDATE_NOTIFIER", "1")
        .env("GH_PROMPT_DISABLED", "1")
        .env("GLAB_CHECK_UPDATE", "false")
        .env("GLAB_NO_PROMPT", "1")
        .env("GLAB_SEND_TELEMETRY", "false")
        .env_remove("GH_DEBUG")
        .env_remove("GLAB_DEBUG")
        .env_remove("GLAB_DEBUG_HTTP")
        .env_remove("USER")
        .args(["config", "get", key, "--host", host]);
    let bytes = process::run_command(command, cancel, LOCAL_TIMEOUT).ok()?;
    let value = std::str::from_utf8(&bytes).ok()?.trim();
    (!value.is_empty()).then(|| value.to_owned())
}

// Instance evidence is keyed by host and full web origin. Conflicting declarations
// are unusable, and an SSH remote can only select an unambiguous web instance.
#[derive(Default)]
struct Instances {
    web: BTreeMap<String, BTreeMap<String, Option<Platform>>>,
    ssh: BTreeMap<String, BTreeSet<(Option<u16>, String)>>,
}

impl Instances {
    fn new() -> Self {
        let mut instances = Self::default();
        instances.add("https://github.com", Platform::Github);
        instances.add("https://gitlab.com", Platform::Gitlab);
        for platform in [Platform::Github, Platform::Gitlab] {
            let (_, host_keys, credential_keys) = providers::credentials(platform);
            let has_credential = credential_keys.iter().any(|key| {
                *key != "GLAB_IS_OAUTH2"
                    && std::env::var_os(key).is_some_and(|value| !value.is_empty())
            });
            if has_credential
                && let Some(host) = host_keys.iter().find_map(|key| {
                    std::env::var(key)
                        .ok()
                        .filter(|value| !value.trim().is_empty())
                })
                && let Some(origin) = providers::configured_origin(&host, "https")
            {
                instances.add(&origin, platform);
            }
        }
        instances
    }

    fn add(&mut self, raw_origin: &str, platform: Platform) {
        let Ok(url) = Url::parse(raw_origin) else {
            return;
        };
        let Some(host) = url.host_str() else {
            return;
        };
        if !matches!(url.scheme(), "http" | "https")
            || !url.username().is_empty()
            || url.password().is_some()
            || url.path() != "/"
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return;
        }
        let origin = url.origin().ascii_serialization();
        if (origin == "https://github.com" && platform != Platform::Github)
            || (origin == "https://gitlab.com" && platform != Platform::Gitlab)
        {
            return;
        }
        self.web
            .entry(host.to_owned())
            .or_default()
            .entry(origin)
            .and_modify(|existing| {
                if *existing != Some(platform) {
                    *existing = None;
                }
            })
            .or_insert(Some(platform));
    }

    fn add_gitlab_ssh_host(&mut self, raw: &str, origin: String) {
        let Ok(url) = Url::parse(&format!("ssh://{raw}/")) else {
            return;
        };
        let Some(host) = url.host_str() else {
            return;
        };
        let host = host.to_ascii_lowercase();
        if matches!(host.as_str(), "github.com" | "gitlab.com")
            || !url.username().is_empty()
            || url.password().is_some()
            || url.path() != "/"
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return;
        }
        self.ssh
            .entry(host)
            .or_default()
            .insert((url.port(), origin));
    }

    fn instance(&self, location: &Location) -> Option<(&String, Platform)> {
        let web = |origin: &str| {
            let url = Url::parse(origin).ok()?;
            let (origin, platform) = self.web.get(url.host_str()?)?.get_key_value(origin)?;
            Some((origin, (*platform)?))
        };
        if let Some(origin) = &location.web_origin {
            return web(origin);
        }
        if let Some(aliases) = self.ssh.get(&location.host) {
            let mut origins = aliases
                .iter()
                .filter(|(port, _)| port.is_none_or(|port| port == location.port.unwrap_or(22)))
                .map(|(_, origin)| origin);
            if let Some(origin) = origins.next() {
                return if origins.all(|other| other == origin) {
                    web(origin).filter(|(_, platform)| *platform == Platform::Gitlab)
                } else {
                    None
                };
            }
        }
        let instances = self.web.get(&location.host)?;
        if location.port.is_some() || instances.len() != 1 {
            return None;
        }
        let (origin, platform) = instances.first_key_value()?;
        Some((origin, (*platform)?))
    }

    fn repository(&self, location: &Location) -> Option<Repository> {
        let (origin, platform) = self.instance(location)?;
        let slug = match platform {
            Platform::Github if location.slug.split('/').count() == 2 => {
                location.slug.to_ascii_lowercase()
            }
            Platform::Github => return None,
            Platform::Gitlab => location.slug.clone(),
        };
        let url = format!("{origin}/{slug}");
        let provider = match platform {
            Platform::Github => "github",
            Platform::Gitlab => "gitlab",
        };
        Some(Repository {
            id: Sha256::of(format!("{provider}\n{url}").as_bytes())
                .as_str()
                .to_owned(),
            platform,
            origin: origin.clone(),
            slug,
            url,
            included: false,
        })
    }

    fn local_cli<'a>(
        &mut self,
        data_dir: &Path,
        locations: impl Iterator<Item = &'a Location>,
        cancel: &Cancellation,
    ) -> Result<()> {
        let hosts: BTreeSet<_> = locations
            .filter(|location| {
                !matches!(location.host.as_str(), "github.com" | "gitlab.com")
                    && (location.web_origin.is_none() || self.instance(location).is_none())
            })
            .map(|location| location.host.as_str())
            .collect();
        let Some(path) = std::env::var_os("PATH") else {
            return Ok(());
        };
        let gh = process::system_tool_path("gh", &path);
        let glab = process::system_tool_path("glab", &path);
        for host in hosts {
            cancel.check()?;
            // The host-specific CLI account setting is local. The actual read
            // still verifies authentication.
            if let Some(tool) = &gh
                && cli_setting(tool, data_dir, host, "user", cancel).is_some()
            {
                self.add(&format!("https://{host}"), Platform::Github);
            }
            if let Some(tool) = &glab
                && cli_setting(tool, data_dir, host, "user", cancel).is_some()
            {
                let protocol = cli_setting(tool, data_dir, host, "api_protocol", cancel)
                    .unwrap_or_else(|| "https".into());
                let authority = cli_setting(tool, data_dir, host, "api_host", cancel)
                    .unwrap_or_else(|| host.to_owned());
                let origin = if authority.contains("://") {
                    authority
                } else {
                    format!("{protocol}://{authority}")
                };
                if let Ok(url) = Url::parse(&origin)
                    && url.host_str() == Some(host)
                    && url.scheme() == protocol
                {
                    self.add(&origin, Platform::Gitlab);
                    if let Some(ssh_host) = cli_setting(tool, data_dir, host, "ssh_host", cancel) {
                        self.add_gitlab_ssh_host(&ssh_host, url.origin().ascii_serialization());
                    }
                }
            }
        }
        cancel.check()
    }
}

struct HomeSource {
    current: bool,
    locations: Vec<Location>,
    subpath: Option<String>,
}

fn home_sources(
    snapshot: &Snapshot,
    cancel: &Cancellation,
) -> Result<BTreeMap<String, Vec<HomeSource>>> {
    let homes: Vec<HomeImport> = snapshot.records("homeImport")?;
    let bindings: Vec<BoundHome> = snapshot.records("homeBinding")?;
    let mut sources = BTreeMap::<String, Vec<HomeSource>>::new();
    for binding in &bindings {
        cancel.check()?;
        let Some(home) = homes.iter().find(|home| home.id == binding.home_id) else {
            continue;
        };
        let references: Vec<_> = binding
            .bindings
            .iter()
            .filter_map(|relation| {
                let bound_path = Path::new(&relation.root_path);
                let mut projects = snapshot.projects.iter().filter(|project| {
                    relation
                        .project_id
                        .as_ref()
                        .is_none_or(|id| id == &project.id)
                });
                let project = projects
                    .clone()
                    .find(|project| Path::new(&project.root_path) == bound_path)
                    .or_else(|| {
                        // Bindings can retain the entered spelling while project roots
                        // are canonicalized at registration.
                        if !bound_path.is_absolute() {
                            return None;
                        }
                        let bound = Root::open(bound_path).ok()?;
                        let canonical = std::fs::canonicalize(&bound.path).ok()?;
                        projects.find(|project| Path::new(&project.root_path) == canonical)
                    })?;
                Some((project, relation))
            })
            .collect();
        if references.is_empty() {
            continue;
        }
        let current_content = (|| -> Result<String> {
            let root = Root::open(Path::new(&home.root))?;
            let checked = root.read_checked(&RelPath::new(&home.source_path)?, 2 * 1024 * 1024)?;
            String::from_utf8(checked.bytes)
                .map_err(|_| YssError::new(ErrorCode::InvalidInput, "产品 Home 地图编码无效"))
        })()
        .ok();
        let current = !integrations::home_stale(home, Some(binding), current_content.as_deref())?;
        for (project, relation) in references {
            let row = home.rows.iter().find(|row| row.id == relation.entry_id);
            let locations = row
                .map(|row| {
                    row.git
                        .split(|c: char| {
                            c.is_whitespace()
                                || matches!(
                                    c,
                                    '`' | '<' | '>' | '(' | ')' | '；' | '，' | ';' | ','
                                )
                        })
                        .filter_map(Location::parse)
                        .collect()
                })
                .unwrap_or_default();
            let subpath = row.map(integrations::row_subpath).transpose();
            sources
                .entry(project.id.clone())
                .or_default()
                .push(HomeSource {
                    current: current && row.is_some() && subpath.is_ok(),
                    locations,
                    subpath: subpath.ok().flatten().flatten(),
                });
        }
    }
    Ok(sources)
}

struct Remote {
    name: String,
    locations: Vec<Option<Location>>,
}

struct LocalProject<'a> {
    project: &'a ProjectRecord,
    home: &'a [HomeSource],
    remotes: Vec<Remote>,
    git_subpath: PathBuf,
    issue: Option<&'static str>,
}

fn invalid_git() -> YssError {
    YssError::new(ErrorCode::InvalidInput, "无法核对已登记 Git 工作树")
}

fn git_remotes(
    project: &ProjectRecord,
    homes: &[HomeSource],
    cancel: &Cancellation,
) -> Result<(Vec<Remote>, PathBuf)> {
    let root = Root::open(Path::new(&project.root_path))?;
    let mut command = git::command(&root.path);
    command.args(["rev-parse", "--show-toplevel"]);
    let output = process::run_command(command, cancel, LOCAL_TIMEOUT)?;
    let top = std::str::from_utf8(&output)
        .map_err(|_| invalid_git())?
        .trim_end_matches(['\r', '\n']);
    let top = std::fs::canonicalize(top)?;
    let checked = std::fs::canonicalize(&root.path)?;
    let subpath = checked.strip_prefix(&top).map_err(|_| invalid_git())?;
    if !subpath.as_os_str().is_empty()
        && !homes.iter().any(|home| {
            home.subpath
                .as_ref()
                .is_some_and(|suffix| Path::new(suffix) == subpath)
        })
    {
        return Err(invalid_git());
    }

    let mut command = git::command(&root.path);
    command.args([
        "config",
        "--null",
        "--get-regexp",
        r"^remote\..*\.(url|fetch)$",
    ]);
    let (status, output, _) = process::capture_command(command, cancel, LOCAL_TIMEOUT)?;
    if !status.success() && !(status.code() == Some(1) && output.is_empty()) {
        return Err(invalid_git());
    }
    let mut remotes = BTreeMap::<&str, usize>::new();
    for record in output
        .split(|byte| *byte == 0)
        .filter(|record| !record.is_empty())
    {
        let separator = record
            .iter()
            .position(|byte| *byte == b'\n')
            .ok_or_else(invalid_git)?;
        let (key, value) = record.split_at(separator);
        let key = std::str::from_utf8(key).map_err(|_| invalid_git())?;
        let (name, property) = key
            .strip_prefix("remote.")
            .and_then(|key| key.rsplit_once('.'))
            .ok_or_else(invalid_git)?;
        if name.is_empty()
            || name.len() > 256
            || name.chars().any(|c| c.is_whitespace() || c.is_control())
        {
            continue;
        }
        let count = remotes.entry(name).or_default();
        if property == "url" {
            if value[1..].contains(&b'\n') {
                return Err(invalid_git());
            }
            *count += 1;
        }
    }
    let mut result = Vec::new();
    for (name, count) in remotes {
        cancel.check()?;
        let mut locations = vec![None];
        if count > 0 {
            let mut command = git::command(&root.path);
            command.args(["remote", "get-url", "--all", "--", name]);
            if let Ok(bytes) = process::run_command(command, cancel, LOCAL_TIMEOUT) {
                let resolved: Vec<_> = bytes
                    .strip_suffix(b"\n")
                    .unwrap_or(&bytes)
                    .split(|byte| *byte == b'\n')
                    .map(|line| {
                        std::str::from_utf8(line)
                            .ok()
                            .and_then(|line| Location::parse(line.trim_end_matches('\r')))
                    })
                    .collect();
                if resolved.len() == count {
                    locations = resolved;
                }
            }
        }
        result.push(Remote {
            name: name.to_owned(),
            locations,
        });
    }
    cancel.check()?;
    root.revalidate()?;
    Ok((result, subpath.to_owned()))
}

fn tracker_instance(project: &ProjectRecord, instances: &mut Instances) {
    let Ok(tracker) = integrations::tracker(&project.root_path) else {
        return;
    };
    let Some(fields) = tracker.get("fields") else {
        return;
    };
    let providers: BTreeSet<_> = ["provider", "platform", "平台"]
        .iter()
        .filter_map(|key| fields.get(key).and_then(|value| value.as_str()))
        .map(str::to_ascii_lowercase)
        .collect();
    let provider = match providers.first().map(String::as_str) {
        Some("github") if providers.len() == 1 => Platform::Github,
        Some("gitlab") if providers.len() == 1 => Platform::Gitlab,
        _ => return,
    };
    for key in ["host", "实例", "url"] {
        let Some(value) = fields.get(key).and_then(|value| value.as_str()) else {
            continue;
        };
        let address = if value.contains("://") {
            value.to_owned()
        } else {
            format!("https://{value}")
        };
        // A tracker URL can include a project path, but its origin still names
        // the configured instance.
        if let Ok(url) = Url::parse(&address)
            && url.username().is_empty()
            && url.password().is_none()
            && url.query().is_none()
            && url.fragment().is_none()
            && (key == "url" || url.path() == "/")
        {
            instances.add(&url.origin().ascii_serialization(), provider);
        }
    }
}

fn describe_project(
    project: &LocalProject<'_>,
    instances: &Instances,
    repositories: &mut BTreeMap<String, Repository>,
) -> SourceProject {
    let mut candidates = BTreeMap::<String, Candidate>::new();
    let mut origin_ids = BTreeSet::new();
    let mut unknown_origin = false;
    for remote in &project.remotes {
        let is_origin = remote.name == "origin";
        for location in &remote.locations {
            let repository = location
                .as_ref()
                .and_then(|location| instances.repository(location));
            let Some(repository) = repository else {
                unknown_origin |= is_origin;
                continue;
            };
            let id = repository.id.clone();
            repositories.entry(id.clone()).or_insert(repository);
            let basis = if is_origin {
                origin_ids.insert(id.clone());
                Basis::Origin
            } else {
                Basis::Manual
            };
            let candidate = Candidate {
                repository_id: id.clone(),
                remote: remote.name.clone(),
                basis,
                selected: false,
                default: false,
            };
            candidates
                .entry(id)
                .and_modify(|existing| {
                    if is_origin {
                        existing.remote = candidate.remote.clone();
                        existing.basis = basis;
                    }
                })
                .or_insert(candidate);
        }
    }
    let mut home_ids = BTreeSet::new();
    let mut current_home_ids = BTreeSet::new();
    let origin_repository = origin_ids.first().and_then(|id| repositories.get(id));
    let mut unverified_home_conflict = false;
    for home in project.home {
        let ids: Vec<_> = home
            .locations
            .iter()
            .map(|location| {
                let resolved = instances.repository(location);
                if resolved.is_none()
                    && let Some(origin) = origin_repository
                {
                    unverified_home_conflict |= Url::parse(&origin.origin).is_ok_and(|url| {
                        url.host_str() != Some(location.host.as_str())
                            || location
                                .web_origin
                                .as_ref()
                                .is_some_and(|web_origin| web_origin != &origin.origin)
                            || location.web_origin.is_none() && location.port.is_some()
                            || match origin.platform {
                                Platform::Github => {
                                    !location.slug.eq_ignore_ascii_case(&origin.slug)
                                }
                                Platform::Gitlab => location.slug != origin.slug,
                            }
                    });
                }
                resolved.map(|repository| repository.id)
            })
            .collect();
        let matches_subpath = home.subpath.as_deref().is_none_or(|subpath| {
            (subpath == "." && project.git_subpath.as_os_str().is_empty())
                || Path::new(subpath) == project.git_subpath
        });
        if home.current && matches_subpath && ids.iter().all(Option::is_some) {
            current_home_ids.extend(ids.iter().flatten().cloned());
        }
        home_ids.extend(ids.into_iter().flatten());
    }
    let origin = origin_ids.first();
    let home = home_ids.first();
    let origin_conflict = origin_ids.len() > 1 || (origin.is_some() && unknown_origin);
    let home_conflict = unverified_home_conflict
        || home_ids.len() > 1
        || home.zip(origin).is_some_and(|(a, b)| a != b);
    let (default, issue) = if project.issue.is_some() {
        (None, project.issue)
    } else if origin_conflict {
        (
            None,
            Some("origin 指向多个或无法全部核对的远端，请确认候选来源"),
        )
    } else if home_conflict {
        (
            None,
            Some("产品 Home 与当前 Git 仓库身份存在冲突，请确认候选来源"),
        )
    } else if unknown_origin {
        (None, Some("无法核对 origin 的平台实例或完整仓库路径"))
    } else if let Some(home) = home.filter(|id| {
        origin.is_some() && current_home_ids.contains(*id) && candidates.contains_key(*id)
    }) {
        (Some((home, Basis::Home)), None)
    } else if let Some(origin) = origin {
        let issue = (!project.home.is_empty())
            .then_some("产品 Home 绑定已过期或无法核对，已改按 origin 读取");
        (Some((origin, Basis::Origin)), issue)
    } else if !candidates.is_empty() {
        (None, Some("没有可自动纳入的 origin，请确认候选来源"))
    } else {
        (None, Some("没有可核对的 GitHub 或 GitLab 远端"))
    };

    if let Some((id, basis)) = default
        && let Some(candidate) = candidates.get_mut(id)
    {
        candidate.default = true;
        candidate.basis = basis;
    }
    SourceProject {
        project_id: project.project.id.clone(),
        name: project.project.name.clone(),
        issue,
        candidates: candidates.into_values().collect(),
    }
}

impl SourceInventory {
    fn apply_preferences(&mut self, preferences: &Preferences) -> Result<()> {
        let mut included = BTreeSet::new();
        for project in &mut self.projects {
            for candidate in &mut project.candidates {
                candidate.selected = preferences.selected.contains(&Selection {
                    project_id: project.project_id.clone(),
                    repository_id: candidate.repository_id.clone(),
                });
                if candidate.default || candidate.selected {
                    included.insert(candidate.repository_id.as_str());
                }
            }
        }
        for repository in &mut self.repositories {
            repository.included = included.contains(repository.id.as_str());
        }

        // Include live eligibility and the saved selection generation in an opaque
        // browser-safe revision. A tab cannot save against old remote identities.
        let encoded =
            serde_json::to_vec(&(preferences.generation, &self.projects, &self.repositories))
                .map_err(|_| YssError::new(ErrorCode::InvalidInput, "无法编码合并请求来源"))?;
        let digest = sha2::Sha256::digest(&encoded);
        let mut head = [0; 8];
        head.copy_from_slice(&digest[..8]);
        self.revision = u64::from_be_bytes(head) >> 11;
        Ok(())
    }
}

fn inventory(
    snapshot: &Snapshot,
    preferences: &Preferences,
    data_dir: &Path,
    cancel: &Cancellation,
) -> Result<SourceInventory> {
    let home_sources = home_sources(snapshot, cancel)?;
    let mut instances = Instances::new();
    for (origin, platform) in snapshot.external_instances()? {
        instances.add(&origin, platform);
    }
    let mut projects = Vec::new();
    for project in &snapshot.projects {
        cancel.check()?;
        let home = home_sources
            .get(&project.id)
            .map(Vec::as_slice)
            .unwrap_or(&[]);
        let (remotes, git_subpath, issue) = match git_remotes(project, home, cancel) {
            Ok((remotes, git_subpath)) => {
                tracker_instance(project, &mut instances);
                (remotes, git_subpath, None)
            }
            Err(error) if error.code == ErrorCode::Cancelled => return Err(error),
            Err(_) => (
                Vec::new(),
                PathBuf::new(),
                Some("无法核对已登记 Git 工作树，请检查工程目录和 Git 配置"),
            ),
        };
        projects.push(LocalProject {
            project,
            home,
            remotes,
            git_subpath,
            issue,
        });
    }

    instances.local_cli(
        data_dir,
        projects
            .iter()
            .flat_map(|project| &project.remotes)
            .flat_map(|remote| &remote.locations)
            .flatten()
            .chain(
                home_sources
                    .values()
                    .flatten()
                    .flat_map(|home| &home.locations),
            ),
        cancel,
    )?;
    let mut repositories = BTreeMap::new();
    let projects = projects
        .iter()
        .map(|project| describe_project(project, &instances, &mut repositories))
        .collect();
    let mut inventory = SourceInventory {
        repositories: repositories.into_values().collect(),
        projects,
        revision: 0,
    };
    inventory
        .repositories
        .sort_by(|left, right| left.url.cmp(&right.url));
    inventory.apply_preferences(preferences)?;
    cancel.check()?;
    Ok(inventory)
}

pub(in crate::workbench) fn discover(
    store: &Arc<Mutex<Store>>,
    cancel: &Cancellation,
) -> Result<SourceInventory> {
    let (snapshot, data_dir) = {
        let mut store = lock(store)?;
        (Snapshot::capture(&mut store)?, store.data_dir().to_owned())
    };
    inventory(&snapshot, &snapshot.preferences()?, &data_dir, cancel)
}

fn changed() -> YssError {
    YssError::new(
        ErrorCode::ConcurrentChange,
        "仓库来源已改变，请重新打开来源后保存",
    )
}

pub(in crate::workbench) fn save(
    store: &Arc<Mutex<Store>>,
    request: SaveSources,
    cancel: &Cancellation,
) -> Result<SourceInventory> {
    let (snapshot, data_dir) = {
        let mut store = lock(store)?;
        (Snapshot::capture(&mut store)?, store.data_dir().to_owned())
    };
    let previous = snapshot.preferences()?;
    let mut inventory = inventory(&snapshot, &previous, &data_dir, cancel)?;
    if request.revision != inventory.revision {
        return Err(changed());
    }

    let eligible: BTreeSet<_> = inventory
        .projects
        .iter()
        .flat_map(|project| {
            project
                .candidates
                .iter()
                .filter(|candidate| !candidate.default || candidate.selected)
                .map(|candidate| {
                    (
                        project.project_id.as_str(),
                        candidate.repository_id.as_str(),
                    )
                })
        })
        .collect();
    // The form can only remove choices that still appear in its inventory.
    let mut selected: BTreeSet<_> = previous
        .selected
        .iter()
        .filter(|selection| {
            !eligible.contains(&(
                selection.project_id.as_str(),
                selection.repository_id.as_str(),
            ))
        })
        .cloned()
        .collect();
    for selection in request.selected {
        if !eligible.contains(&(
            selection.project_id.as_str(),
            selection.repository_id.as_str(),
        )) || !selected.insert(selection)
        {
            return Err(YssError::new(
                ErrorCode::InvalidInput,
                "只能保存当前已登记工程中可核对的额外来源",
            ));
        }
    }
    let mut store = lock(store)?;
    let current = Snapshot::capture(&mut store)?;
    if current.projects != snapshot.projects
        || current.records.get("homeBinding") != snapshot.records.get("homeBinding")
        || current.records.get("homeImport") != snapshot.records.get("homeImport")
        || current.external_instances()? != snapshot.external_instances()?
        || current.preferences()? != previous
    {
        return Err(changed());
    }
    cancel.check()?;
    if selected == previous.selected {
        return Ok(inventory);
    }
    let generation = previous.generation.checked_add(1).ok_or_else(changed)?;
    let preferences = Preferences {
        generation,
        selected,
    };
    inventory.apply_preferences(&preferences)?;
    store.put_record(PREFERENCES, PREFERENCE_ID, &preferences)?;
    Ok(inventory)
}
