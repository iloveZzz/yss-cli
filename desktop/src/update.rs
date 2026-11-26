//! Stable desktop release discovery and verified update preparation.
//!
//! The CLI owns GitHub's `/releases/latest`; desktop tags are discovered from
//! the release listing instead. Nothing here stops a workbench: callers must
//! flush and coordinate it after `prepare` and before `ReadyUpdate::install`.
use reqwest::blocking::{Client, Response};
use reqwest::{header, redirect};
use semver::Version;
use serde::{Deserialize, Serialize};
use std::{collections::BTreeSet, io::Read, sync::Arc, time::Duration};
use tauri::AppHandle;
use tauri_plugin_updater::{Update, UpdaterExt};
use url::Url;

const API: &str = "https://api.github.com/repos/iloveZzz/yss-cli/releases";
const DOWNLOAD: &str = "https://github.com/iloveZzz/yss-cli/releases/download/";
const MAX_PAGE: u64 = 4 * 1024 * 1024;
const MAX_MANIFEST: u64 = 1024 * 1024;
const MAX_SIGNATURE: u64 = 16 * 1024;
const MAX_PACKAGE: u64 = 1024 * 1024 * 1024;
const MAX_PAGES: usize = 1000;

#[derive(Debug, Clone, Deserialize)]
struct Asset {
    name: String,
    browser_download_url: String,
    size: u64,
}

#[derive(Debug, Deserialize)]
struct GithubRelease {
    tag_name: String,
    draft: bool,
    prerelease: bool,
    assets: Vec<Asset>,
}

#[derive(Debug, Deserialize)]
struct Manifest {
    version: String,
    platforms: std::collections::BTreeMap<String, Platform>,
}

#[derive(Debug, Clone, Deserialize)]
struct Platform {
    url: String,
    signature: String,
}

/// Only `version` is exposed to UI. The remaining trusted discovery result is
/// retained in native state and never reconstructed from renderer input.
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Release {
    pub version: String,
    #[serde(skip)]
    tag: String,
    #[serde(skip)]
    manifest_url: String,
    #[serde(skip)]
    platform: Platform,
    #[serde(skip)]
    package_size: u64,
}

pub struct ReadyUpdate {
    update: Update,
    bytes: Vec<u8>,
}

fn key() -> Result<&'static str, String> {
    option_env!("YSS_DESKTOP_UPDATE_PUBKEY")
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| "桌面更新公钥尚未配置，无法检查或安装更新".into())
}

/// Register once at desktop startup. Absence of a configured public key is a
/// visible disabled state, not a fatal error for the rest of the workbench.
pub fn register(app: &AppHandle) -> Result<(), String> {
    let public_key = key()?;
    let config = app
        .config()
        .plugins
        .0
        .get("updater")
        .ok_or("桌面更新配置缺失")?;
    if config
        .get("requireSignedVersion")
        .and_then(serde_json::Value::as_bool)
        != Some(true)
    {
        return Err("桌面更新缺少版本签名校验配置".into());
    }
    app.plugin(
        tauri_plugin_updater::Builder::new()
            .pubkey(public_key)
            .build(),
    )
    .map_err(|_| "无法启用桌面更新组件".into())
}

fn redirect_policy() -> redirect::Policy {
    redirect::Policy::custom(|attempt| {
        let url = attempt.url();
        let allowed = url.scheme() == "https"
            && url.username().is_empty()
            && url.password().is_none()
            && url.port().is_none()
            && matches!(
                url.host_str(),
                Some(
                    "github.com"
                        | "release-assets.githubusercontent.com"
                        | "objects.githubusercontent.com"
                )
            );
        if attempt.previous().len() >= 5 || !allowed {
            attempt.error("untrusted redirect")
        } else {
            attempt.follow()
        }
    })
}

fn client() -> Result<Client, String> {
    let mut roots = rustls::RootCertStore::empty();
    roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
    let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(|_| "桌面更新 TLS 配置无效")?
    .with_root_certificates(roots)
    .with_no_client_auth();
    Client::builder()
        .user_agent("yss-desktop-updater/1")
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(60))
        .redirect(redirect_policy())
        .use_preconfigured_tls(tls)
        .build()
        .map_err(|_| "无法初始化桌面更新连接".into())
}

fn bytes(response: Response, limit: u64) -> Result<Vec<u8>, String> {
    if !response.status().is_success() {
        return Err(format!(
            "桌面更新请求失败（HTTP {}），请稍后重试",
            response.status().as_u16()
        ));
    }
    if response.content_length().is_some_and(|size| size > limit) {
        return Err("桌面更新响应超过大小限制".into());
    }
    let mut data = Vec::new();
    response
        .take(limit + 1)
        .read_to_end(&mut data)
        .map_err(|_| "读取桌面更新响应失败")?;
    if data.len() as u64 > limit {
        return Err("桌面更新响应超过大小限制".into());
    }
    Ok(data)
}

fn send(client: &Client, url: &str) -> Result<Response, String> {
    client
        .get(url)
        .header(header::ACCEPT, "application/vnd.github+json")
        .header(header::ACCEPT_ENCODING, "identity")
        .send()
        .map_err(|_| "无法连接桌面更新源，请检查网络后重试".into())
}

fn next_page(header_value: Option<&str>, current: usize) -> Result<Option<String>, String> {
    let Some(header_value) = header_value else {
        return Ok(None);
    };
    let mut next = None;
    for entry in header_value.split(',') {
        let mut parts = entry.trim().split(';').map(str::trim);
        let raw = parts.next().ok_or("桌面更新分页响应无效")?;
        let link = raw
            .strip_prefix('<')
            .and_then(|s| s.strip_suffix('>'))
            .ok_or("桌面更新分页地址无效")?;
        let mut relations = parts.filter_map(|part| part.strip_prefix("rel="));
        let relation = relations.next().ok_or("桌面更新分页关系无效")?;
        if relations.next().is_some() {
            return Err("桌面更新分页关系重复".into());
        }
        let relation = relation
            .strip_prefix('"')
            .and_then(|value| value.strip_suffix('"'))
            .unwrap_or(relation);
        if relation.is_empty() || relation.contains('"') {
            return Err("桌面更新分页关系无效".into());
        }
        if matches!(relation, "prev" | "first" | "last") {
            continue;
        }
        if relation != "next" {
            return Err("桌面更新分页关系不受支持".into());
        }
        if next.is_some() {
            return Err("桌面更新分页响应重复".into());
        }
        let url = Url::parse(link).map_err(|_| "桌面更新分页地址无效")?;
        if url.scheme() != "https"
            || url.host_str() != Some("api.github.com")
            || url.port().is_some()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.path() != "/repos/iloveZzz/yss-cli/releases"
            || url.fragment().is_some()
        {
            return Err("桌面更新分页来源无效".into());
        }
        let pairs = url.query_pairs().collect::<Vec<_>>();
        if pairs.len() != 2
            || !pairs.iter().any(|(k, v)| k == "per_page" && v == "100")
            || !pairs
                .iter()
                .any(|(k, v)| k == "page" && v.parse::<usize>().ok() == Some(current + 1))
        {
            return Err("桌面更新分页顺序无效".into());
        }
        next = Some(url.to_string());
    }
    Ok(next)
}

fn asset<'a>(assets: &'a [Asset], tag: &str, name: &str, limit: u64) -> Result<&'a Asset, String> {
    let mut matches = assets.iter().filter(|entry| entry.name == name);
    let found = matches.next().ok_or("桌面发行缺少必要资产")?;
    if matches.next().is_some()
        || found.size == 0
        || found.size > limit
        || found.browser_download_url != format!("{DOWNLOAD}{tag}/{name}")
    {
        return Err("桌面发行资产来源或大小无效".into());
    }
    Ok(found)
}

fn safe_name(name: &str) -> bool {
    !name.is_empty()
        && name.len() <= 180
        && name
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'.' | b'_' | b'-'))
}

fn selected_target() -> Result<&'static str, String> {
    if cfg!(all(target_os = "macos", target_arch = "aarch64")) {
        Ok("darwin-aarch64")
    } else if cfg!(all(target_os = "windows", target_arch = "x86_64")) {
        Ok("windows-x86_64")
    } else {
        Err("当前平台不支持桌面自动更新".into())
    }
}

/// Discover all release pages and select the greatest stable desktop version.
/// Errors never mean "up to date". This function performs blocking I/O and must
/// be invoked away from the native UI thread.
pub fn check() -> Result<Option<Release>, String> {
    key()?;
    let current = Version::parse(env!("CARGO_PKG_VERSION")).map_err(|_| "桌面版本无效")?;
    let client = client()?;
    let mut url = format!("{API}?per_page=100&page=1");
    let mut selected: Option<(Version, GithubRelease)> = None;
    let mut seen = BTreeSet::new();
    for page in 1..=MAX_PAGES {
        let response = send(&client, &url)?;
        let link = response
            .headers()
            .get(header::LINK)
            .map(|v| {
                v.to_str()
                    .map(str::to_owned)
                    .map_err(|_| "桌面更新分页响应无效".to_owned())
            })
            .transpose()?;
        let releases: Vec<GithubRelease> = serde_json::from_slice(&bytes(response, MAX_PAGE)?)
            .map_err(|_| "桌面发行列表格式无效")?;
        let count = releases.len();
        if count > 100 {
            return Err("桌面发行分页结果异常".into());
        }
        for release in releases {
            if release.draft || release.prerelease {
                continue;
            }
            let Some(text) = release.tag_name.strip_prefix("desktop-v") else {
                continue;
            };
            let Ok(version) = Version::parse(text) else {
                continue;
            };
            if !version.pre.is_empty() || !version.build.is_empty() || version.to_string() != text {
                continue;
            }
            if !seen.insert(version.clone()) {
                return Err("桌面发行版本重复".into());
            }
            if selected.as_ref().is_none_or(|(old, _)| version > *old) {
                selected = Some((version, release));
            }
        }
        match next_page(link.as_deref(), page)? {
            Some(next) if page < MAX_PAGES => url = next,
            Some(_) => return Err("桌面发行列表过长，无法完成检查".into()),
            None if count == 100 && page < MAX_PAGES => {
                // A missing Link header is not evidence of a complete listing
                // when the page is full. Verify the next fixed-origin page.
                url = format!("{API}?per_page=100&page={}", page + 1);
            }
            None if count == 100 => return Err("桌面发行列表过长，无法完成检查".into()),
            None => break,
        }
    }
    let Some((version, release)) = selected else {
        return Ok(None);
    };
    if version <= current {
        return Ok(None);
    }
    let metadata = asset(
        &release.assets,
        &release.tag_name,
        "latest.json",
        MAX_MANIFEST,
    )?;
    let manifest_url = metadata.browser_download_url.clone();
    let manifest_bytes = bytes(send(&client, &manifest_url)?, MAX_MANIFEST)?;
    if manifest_bytes.len() as u64 != metadata.size {
        return Err("桌面更新清单大小与发行记录不一致".into());
    }
    let manifest: Manifest =
        serde_json::from_slice(&manifest_bytes).map_err(|_| "桌面更新清单格式无效")?;
    if manifest.version != version.to_string() {
        return Err("桌面发行与更新清单版本不一致".into());
    }
    let mut selected_platform = None;
    let target = selected_target()?;
    for (platform, suffix) in [("darwin-aarch64", ".tar.gz"), ("windows-x86_64", ".exe")] {
        let item = manifest
            .platforms
            .get(platform)
            .ok_or("桌面更新清单缺少平台资产")?;
        let prefix = format!("{DOWNLOAD}{}/", release.tag_name);
        let name = item
            .url
            .strip_prefix(&prefix)
            .filter(|name| safe_name(name) && name.ends_with(suffix))
            .ok_or("桌面更新包来源无效")?;
        let package = asset(&release.assets, &release.tag_name, name, MAX_PACKAGE)?;
        let sig = asset(
            &release.assets,
            &release.tag_name,
            &format!("{name}.sig"),
            MAX_SIGNATURE,
        )?;
        let signature = bytes(send(&client, &sig.browser_download_url)?, MAX_SIGNATURE)?;
        if signature.len() as u64 != sig.size {
            return Err("桌面签名大小与发行记录不一致".into());
        }
        let signature = std::str::from_utf8(&signature)
            .map_err(|_| "桌面签名格式无效")?
            .trim();
        if signature.is_empty() || signature != item.signature.trim() {
            return Err("桌面更新签名与发行资产不一致".into());
        }
        if platform == target {
            selected_platform = Some((item.clone(), package.size));
        }
    }
    let (platform, package_size) = selected_platform.ok_or("当前平台缺少桌面更新包")?;
    Ok(Some(Release {
        version: version.to_string(),
        tag: release.tag_name,
        manifest_url,
        platform,
        package_size,
    }))
}

/// Re-read and compare the update manifest, then download and verify the entire
/// package *before* the caller quiesces or stops its workbench.
pub async fn prepare(app: &AppHandle, release: &Release) -> Result<ReadyUpdate, String> {
    let public_key = key()?;
    let url = Url::parse(&release.manifest_url).map_err(|_| "桌面更新清单地址无效")?;
    if release.manifest_url != format!("{DOWNLOAD}{}/latest.json", release.tag) {
        return Err("桌面更新来源已变化".into());
    }
    let update = app
        .updater_builder()
        .pubkey(public_key)
        .timeout(Duration::from_secs(60))
        .configure_client(|builder| builder.redirect(redirect_policy()))
        .endpoints(vec![url])
        .map_err(|_| "桌面更新地址无效")?
        .build()
        .map_err(|_| "无法初始化桌面更新")?
        .check()
        .await
        .map_err(|_| "无法读取桌面更新清单")?
        .ok_or("桌面更新版本已变化，请重新检查")?;
    if update.version != release.version
        || update.download_url.as_str() != release.platform.url
        || update.signature.trim() != release.platform.signature.trim()
    {
        return Err("桌面更新清单已变化，请重新检查".into());
    }
    let package_url = release.platform.url.clone();
    let package_size = release.package_size;
    let data = tauri::async_runtime::spawn_blocking(move || {
        let data = bytes(send(&client()?, &package_url)?, MAX_PACKAGE)?;
        if data.len() as u64 != package_size {
            return Err("桌面更新包大小与发行记录不一致".to_owned());
        }
        Ok(data)
    })
    .await
    .map_err(|_| "桌面更新下载任务异常".to_owned())??;
    update
        .verify(&data)
        .map_err(|_| "桌面更新签名或版本校验失败")?;
    Ok(ReadyUpdate {
        update,
        bytes: data,
    })
}

impl ReadyUpdate {
    /// The caller must first complete the desktop service coordination.
    pub fn install(self) -> Result<(), String> {
        self.update
            .install(&self.bytes)
            .map_err(|_| "桌面更新安装失败，原数据已保留".into())
    }
}
