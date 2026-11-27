use super::ToolId;
use reqwest::blocking::Client;
use semver::Version;
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use std::{cmp::Ordering, io::Read, sync::Arc, time::Duration};

#[path = "official_assets.rs"]
mod official_assets;
pub(super) use official_assets::OfficialArchive;

const GIT_TAGS: &str = "https://api.github.com/repos/git/git/tags?per_page=100";
const WINDOWS_GIT_REPOSITORY: &str = "git-for-windows/git";
const GH_REPOSITORY: &str = "cli/cli";
// GitLab's stable numeric project ID avoids the redirect from /permalink/latest.
const GLAB_RELEASES: &str = "https://gitlab.com/api/v4/projects/34675721/releases?per_page=5";
const PI_PACKAGE: &str = "@earendil-works/pi-coding-agent";
const PI_LATEST: &str = "https://registry.npmjs.org/@earendil-works%2Fpi-coding-agent/latest";
const MAX_METADATA: u64 = 2 * 1024 * 1024;

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct Release {
    pub version: String,
    pub release_url: String,
    pub asset_url: Option<String>,
    pub node_requirement: Option<String>,
    #[serde(skip)]
    pub official_archive: Option<OfficialArchive>,
}

#[derive(Clone, Copy)]
enum Platform {
    MacArm64,
    WindowsX64,
}

fn platform() -> Option<Platform> {
    match (std::env::consts::OS, std::env::consts::ARCH) {
        ("macos", "aarch64") => Some(Platform::MacArm64),
        ("windows", "x86_64") => Some(Platform::WindowsX64),
        _ => None,
    }
}

fn client() -> Result<Client, String> {
    client_for(Duration::from_secs(10))
}

fn client_for(timeout: Duration) -> Result<Client, String> {
    let mut roots = rustls::RootCertStore::empty();
    roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
    let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(|_| "无法配置官方版本查询的 HTTPS 连接")?
    .with_root_certificates(roots)
    .with_no_client_auth();
    Client::builder()
        .user_agent(concat!("yss-desktop/", env!("CARGO_PKG_VERSION")))
        .https_only(true)
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(4))
        .timeout(timeout)
        .use_preconfigured_tls(tls)
        .build()
        .map_err(|_| "无法建立官方版本查询连接".into())
}

fn get<T: DeserializeOwned>(client: &Client, url: &str, source: &str) -> Result<T, String> {
    let response = client
        .get(url)
        .header(reqwest::header::ACCEPT, "application/json")
        .send()
        .map_err(|_| format!("无法连接 {source}，请检查网络后重试"))?;
    if !response.status().is_success() {
        return Err(format!(
            "{source} 暂未返回发布信息（HTTP {}），请稍后重试",
            response.status().as_u16()
        ));
    }
    let mut bytes = Vec::new();
    response
        .take(MAX_METADATA + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| format!("无法完整读取 {source} 发布信息"))?;
    if bytes.len() as u64 > MAX_METADATA {
        return Err(format!("{source} 发布信息过大"));
    }
    serde_json::from_slice(&bytes).map_err(|_| format!("{source} 发布信息格式无效"))
}

fn stable(value: &str) -> Option<Version> {
    Version::parse(value)
        .ok()
        .filter(|version| version.pre.is_empty() && version.build.is_empty())
}

fn stable_tag(tag: &str) -> Option<Version> {
    stable(tag.strip_prefix('v')?)
}

fn git_windows_version(value: &str) -> Option<(Version, u64)> {
    let (version, suffix) = value.split_once(".windows.")?;
    let revision: u64 = suffix.parse().ok()?;
    let version = stable(version)?;
    (revision > 0 && revision.to_string() == suffix).then_some((version, revision))
}

fn comparable(id: ToolId, value: &str) -> Option<(Version, u64)> {
    let value = value.trim();
    let version = match id {
        ToolId::Git => value.strip_prefix("git version "),
        ToolId::Gh => value.strip_prefix("gh version "),
        ToolId::Glab => value
            .strip_prefix("glab version ")
            .or_else(|| value.strip_prefix("glab ")),
        ToolId::Pi => None,
    }
    .unwrap_or(value)
    .split_ascii_whitespace()
    .next()?;
    let version = version.strip_prefix('v').unwrap_or(version);
    if matches!(id, ToolId::Git) && version.contains(".windows.") {
        return git_windows_version(version);
    }
    Some((Version::parse(version).ok()?, 0))
}

pub(super) fn is_newer(id: ToolId, candidate: &str, detected: &str) -> Option<bool> {
    let (candidate, candidate_revision) = comparable(id, candidate)?;
    let (detected, detected_revision) = comparable(id, detected)?;
    Some(
        candidate
            .cmp_precedence(&detected)
            .then(candidate_revision.cmp(&detected_revision))
            == Ordering::Greater,
    )
}

#[derive(Deserialize)]
struct GithubAsset {
    name: String,
    state: String,
    browser_download_url: String,
    digest: Option<String>,
    size: Option<u64>,
}

#[derive(Deserialize)]
struct GithubRelease {
    tag_name: String,
    draft: bool,
    prerelease: bool,
    assets: Vec<GithubAsset>,
}

fn github_asset<'a>(
    release: &'a GithubRelease,
    repository: &str,
    name: &str,
) -> Option<&'a GithubAsset> {
    let expected = format!(
        "https://github.com/{repository}/releases/download/{}/{name}",
        release.tag_name
    );
    release.assets.iter().find(|asset| {
        asset.name == name && asset.state == "uploaded" && asset.browser_download_url == expected
    })
}

fn github_release(release: GithubRelease, id: ToolId, platform: Platform) -> Option<Release> {
    if release.draft || release.prerelease {
        return None;
    }
    let (repository, version, name) = match id {
        ToolId::Git => {
            let (base, revision) = git_windows_version(release.tag_name.strip_prefix('v')?)?;
            let filename_version = if revision == 1 {
                base.to_string()
            } else {
                format!("{base}.{revision}")
            };
            (
                WINDOWS_GIT_REPOSITORY,
                format!("{base}.windows.{revision}"),
                format!("Git-{filename_version}-64-bit.exe"),
            )
        }
        ToolId::Gh => {
            let version = stable_tag(&release.tag_name)?.to_string();
            let name = match platform {
                Platform::MacArm64 => format!("gh_{version}_macOS_universal.pkg"),
                Platform::WindowsX64 => format!("gh_{version}_windows_amd64.msi"),
            };
            (GH_REPOSITORY, version, name)
        }
        _ => return None,
    };
    let official_archive = if matches!(id, ToolId::Gh) {
        let name = official_assets::gh_asset_name(&version, platform);
        github_asset(&release, GH_REPOSITORY, &name).and_then(|asset| {
            OfficialArchive::github(&version, platform, asset.digest.as_deref()?, asset.size?)
        })
    } else {
        None
    };
    Some(Release {
        version,
        release_url: format!(
            "https://github.com/{repository}/releases/tag/{}",
            release.tag_name
        ),
        asset_url: github_asset(&release, repository, &name)
            .map(|asset| asset.browser_download_url.clone()),
        node_requirement: None,
        official_archive,
    })
}

#[derive(Deserialize)]
struct GithubTag {
    name: String,
}

fn git_macos(client: &Client) -> Result<Release, String> {
    let tags: Vec<GithubTag> = get(client, GIT_TAGS, "Git 官方仓库")?;
    let version = tags
        .iter()
        .filter_map(|tag| stable_tag(&tag.name))
        .max()
        .ok_or("Git 官方仓库未返回稳定版本")?;
    Ok(Release {
        release_url: format!("https://github.com/git/git/tree/v{version}"),
        version: version.to_string(),
        asset_url: None,
        node_requirement: None,
        official_archive: None,
    })
}

#[derive(Deserialize)]
struct GitlabAsset {
    name: String,
    direct_asset_url: Option<String>,
    url: Option<String>,
}

#[derive(Deserialize)]
struct GitlabAssets {
    links: Vec<GitlabAsset>,
}

#[derive(Deserialize)]
struct GitlabRelease {
    tag_name: String,
    assets: GitlabAssets,
    #[serde(default)]
    upcoming_release: bool,
}

fn gitlab_release(releases: Vec<GitlabRelease>, platform: Platform) -> Option<Release> {
    let (version, release) = releases
        .into_iter()
        .filter(|release| !release.upcoming_release)
        .filter_map(|release| Some((stable_tag(&release.tag_name)?, release)))
        .max_by(|(left, _), (right, _)| left.cmp(right))?;
    let name = match platform {
        Platform::MacArm64 => format!("glab_{version}_darwin_arm64.tar.gz"),
        Platform::WindowsX64 => format!("glab_{version}_Windows_x86_64_installer.exe"),
    };
    let release_url = format!("https://gitlab.com/gitlab-org/cli/-/releases/v{version}");
    let download = format!("{release_url}/downloads/{name}");
    let asset_url = release
        .assets
        .links
        .iter()
        .any(|asset| {
            asset.name == name && asset.direct_asset_url.as_deref() == Some(download.as_str())
        })
        .then_some(download);
    let official_archive =
        OfficialArchive::gitlab(&version.to_string(), platform, |name, direct, package| {
            release.assets.links.iter().any(|asset| {
                asset.name == name
                    && asset.direct_asset_url.as_deref() == Some(direct)
                    && asset.url.as_deref() == Some(package)
            })
        });
    Some(Release {
        version: version.to_string(),
        release_url,
        asset_url,
        node_requirement: None,
        official_archive,
    })
}

#[derive(Deserialize)]
struct NpmEngines {
    node: String,
}

#[derive(Deserialize)]
struct NpmDist {
    tarball: String,
}

#[derive(Deserialize)]
struct NpmRelease {
    name: String,
    version: String,
    engines: NpmEngines,
    dist: NpmDist,
}

fn pi_release(package: NpmRelease) -> Option<Release> {
    let version = stable(&package.version)?;
    let tarball =
        format!("https://registry.npmjs.org/{PI_PACKAGE}/-/pi-coding-agent-{version}.tgz");
    let requirement = package.engines.node.trim();
    if package.name != PI_PACKAGE
        || package.dist.tarball != tarball
        || requirement.is_empty()
        || requirement.len() > 128
        || !requirement
            .bytes()
            .all(|character| b"0123456789.^~<>=xX*| +v-".contains(&character))
    {
        return None;
    }
    Some(Release {
        release_url: format!("https://www.npmjs.com/package/{PI_PACKAGE}/v/{version}"),
        version: version.to_string(),
        asset_url: Some(tarball),
        node_requirement: Some(requirement.to_owned()),
        official_archive: None,
    })
}

pub(super) fn latest(id: ToolId) -> Result<Release, String> {
    let platform = platform().ok_or("当前平台不支持本机工具版本查询")?;
    let client = client()?;
    match (id, platform) {
        (ToolId::Git, Platform::MacArm64) => git_macos(&client),
        (ToolId::Git | ToolId::Gh, _) => {
            let repository = if matches!(id, ToolId::Git) {
                WINDOWS_GIT_REPOSITORY
            } else {
                GH_REPOSITORY
            };
            let url = format!("https://api.github.com/repos/{repository}/releases/latest");
            github_release(get(&client, &url, "GitHub Releases")?, id, platform)
                .ok_or_else(|| "GitHub Releases 未返回有效的稳定版本".into())
        }
        (ToolId::Glab, _) => {
            gitlab_release(get(&client, GLAB_RELEASES, "GitLab Releases")?, platform)
                .ok_or_else(|| "GitLab Releases 未返回稳定版本".into())
        }
        (ToolId::Pi, _) => pi_release(get(&client, PI_LATEST, "npm 官方注册表")?)
            .ok_or_else(|| "npm 官方注册表未返回有效的 Pi 稳定版本和 Node 要求".into()),
    }
}
