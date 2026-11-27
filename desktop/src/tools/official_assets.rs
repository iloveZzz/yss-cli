use super::{Platform, client_for};
use reqwest::{StatusCode, Url, header::LOCATION};
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeSet,
    io::{Cursor, Read},
    time::Duration,
};

const MAX_DOWNLOAD: u64 = 64 * 1024 * 1024;
const MAX_CHECKSUMS: u64 = 64 * 1024;
const MAX_EXECUTABLE: u64 = 96 * 1024 * 1024;
const MAX_EXPANDED: u64 = 128 * 1024 * 1024;
const MAX_ENTRIES: usize = 4096;
const MAX_REDIRECTS: usize = 2;
const GITHUB_GH_ASSET_ROOT: &str = "/github-production-release-asset/212613049/";

#[derive(Clone)]
pub(crate) struct OfficialArchive {
    source: Source,
    name: String,
    entry: String,
    tar_gzip: bool,
}

#[derive(Clone)]
enum Source {
    GitHub {
        version: String,
        sha256: [u8; 32],
        size: u64,
    },
    GitLab {
        version: String,
    },
}

pub(super) fn gh_asset_name(version: &str, platform: Platform) -> String {
    let suffix = match platform {
        Platform::MacArm64 => "macOS_arm64",
        Platform::WindowsX64 => "windows_amd64",
    };
    format!("gh_{version}_{suffix}.zip")
}

fn hex_sha256(value: &str) -> Option<[u8; 32]> {
    let mut digest = [0; 32];
    if value.len() != digest.len() * 2 || !value.as_bytes().iter().all(u8::is_ascii_hexdigit) {
        return None;
    }
    for (position, byte) in digest.iter_mut().enumerate() {
        *byte = u8::from_str_radix(&value[position * 2..position * 2 + 2], 16).ok()?;
    }
    Some(digest)
}

impl OfficialArchive {
    pub(super) fn github(
        version: &str,
        platform: Platform,
        digest: &str,
        size: u64,
    ) -> Option<Self> {
        if !(1..=MAX_DOWNLOAD).contains(&size) {
            return None;
        }
        let sha256 = hex_sha256(digest.strip_prefix("sha256:")?)?;
        let name = gh_asset_name(version, platform);
        let entry = match platform {
            Platform::MacArm64 => format!("gh_{version}_macOS_arm64/bin/gh"),
            Platform::WindowsX64 => "bin/gh.exe".into(),
        };
        Some(Self {
            source: Source::GitHub {
                version: version.into(),
                sha256,
                size,
            },
            name,
            entry,
            tar_gzip: false,
        })
    }

    pub(super) fn gitlab(
        version: &str,
        platform: Platform,
        contains: impl Fn(&str, &str, &str) -> bool,
    ) -> Option<Self> {
        let (suffix, entry, tar_gzip) = match platform {
            Platform::MacArm64 => ("darwin_arm64.tar.gz", "bin/glab", true),
            Platform::WindowsX64 => ("windows_amd64.zip", "bin/glab.exe", false),
        };
        let name = format!("glab_{version}_{suffix}");
        for asset in [&name, "checksums.txt"] {
            let (release, package) = gitlab_urls(version, asset);
            if !contains(asset, &release, &package) {
                return None;
            }
        }
        Some(Self {
            source: Source::GitLab {
                version: version.into(),
            },
            name,
            entry: entry.into(),
            tar_gzip,
        })
    }

    pub(crate) fn executable(&self) -> Result<Vec<u8>, String> {
        let client = client_for(Duration::from_secs(180))?;
        let (archive, expected) = match &self.source {
            Source::GitHub {
                version,
                sha256,
                size,
            } => {
                let url = format!(
                    "https://github.com/cli/cli/releases/download/v{version}/{}",
                    self.name
                );
                let archive = download(&client, &url, OfficialRedirect::GitHub, *size)?;
                if archive.len() as u64 != *size {
                    return Err("官方便携包大小与发布记录不匹配".into());
                }
                (archive, *sha256)
            }
            Source::GitLab { version } => {
                let (url, package) = gitlab_urls(version, "checksums.txt");
                let checksums = download(
                    &client,
                    &url,
                    OfficialRedirect::GitLab(&package),
                    MAX_CHECKSUMS,
                )?;
                let sha256 = checksum_of(&checksums, &self.name)?;
                let (url, package) = gitlab_urls(version, &self.name);
                let archive = download(
                    &client,
                    &url,
                    OfficialRedirect::GitLab(&package),
                    MAX_DOWNLOAD,
                )?;
                (archive, sha256)
            }
        };
        let actual: [u8; 32] = Sha256::digest(&archive).into();
        if actual != expected {
            return Err("官方便携包 SHA-256 校验失败".into());
        }
        if self.tar_gzip {
            extract_tar_gzip(&archive, &self.entry)
        } else {
            extract_zip(&archive, &self.entry)
        }
    }
}

fn gitlab_urls(version: &str, filename: &str) -> (String, String) {
    (
        format!("https://gitlab.com/gitlab-org/cli/-/releases/v{version}/downloads/{filename}"),
        format!(
            "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/{}/{}",
            version.replace('.', "%2E"),
            filename.replace('.', "%2E")
        ),
    )
}

fn checksum_of(checksums: &[u8], name: &str) -> Result<[u8; 32], String> {
    let text = std::str::from_utf8(checksums).map_err(|_| "GitLab 官方校验文件格式无效")?;
    let mut matches = text.lines().filter_map(|line| {
        let mut fields = line.split_ascii_whitespace();
        let hash = fields.next()?;
        let candidate = fields.next()?;
        (candidate.strip_prefix('*').unwrap_or(candidate) == name)
            .then_some((hash, fields.next().is_none()))
    });
    let (hash, true) = matches.next().ok_or("GitLab 校验文件缺少便携包记录")? else {
        return Err("GitLab 便携包校验记录格式无效".into());
    };
    if matches.next().is_some() {
        return Err("GitLab 校验文件包含重复记录".into());
    }
    hex_sha256(hash).ok_or_else(|| "GitLab 便携包 SHA-256 校验值无效".into())
}

#[derive(Clone, Copy)]
enum OfficialRedirect<'a> {
    GitHub,
    GitLab(&'a str),
}

fn allowed_redirect(kind: OfficialRedirect<'_>, from: &Url, target: &Url) -> bool {
    if target.scheme() != "https"
        || !target.username().is_empty()
        || target.password().is_some()
        || target.port().is_some()
        || target.fragment().is_some()
    {
        return false;
    }
    match kind {
        OfficialRedirect::GitHub => {
            from.host_str() == Some("github.com")
                && target.host_str() == Some("release-assets.githubusercontent.com")
                && target
                    .path()
                    .strip_prefix(GITHUB_GH_ASSET_ROOT)
                    .is_some_and(|key| {
                        key.len() == 36 && key.bytes().all(|c| c.is_ascii_hexdigit() || c == b'-')
                    })
        }
        OfficialRedirect::GitLab(package) => {
            from.path().starts_with("/gitlab-org/cli/-/releases/") && target.as_str() == package
        }
    }
}

fn download(
    client: &reqwest::blocking::Client,
    initial: &str,
    kind: OfficialRedirect<'_>,
    limit: u64,
) -> Result<Vec<u8>, String> {
    let mut url = Url::parse(initial).map_err(|_| "无法构造官方便携包地址")?;
    for hop in 0..=MAX_REDIRECTS {
        let response = client
            .get(url.clone())
            .send()
            .map_err(|_| "无法下载官方便携包或校验文件，请检查网络后重试")?;
        if response.status().is_redirection() {
            let location = response
                .headers()
                .get(LOCATION)
                .and_then(|header| header.to_str().ok())
                .ok_or("官方便携包重定向缺少目标地址")?;
            let target = url.join(location).map_err(|_| "官方便携包重定向地址无效")?;
            if hop == MAX_REDIRECTS || !allowed_redirect(kind, &url, &target) {
                return Err("拒绝非预期的官方便携包重定向".into());
            }
            url = target;
            continue;
        }
        if response.status() != StatusCode::OK {
            return Err(format!(
                "官方便携包无法下载（HTTP {}）",
                response.status().as_u16()
            ));
        }
        if response
            .content_length()
            .is_some_and(|length| !(1..=limit).contains(&length))
        {
            return Err("官方便携包超过允许的大小或为空".into());
        }
        let mut bytes = Vec::new();
        response
            .take(limit + 1)
            .read_to_end(&mut bytes)
            .map_err(|_| "无法完整读取官方便携包或校验文件")?;
        if bytes.is_empty() || bytes.len() as u64 > limit {
            return Err("官方便携包超过允许的大小或为空".into());
        }
        return Ok(bytes);
    }
    Err("官方便携包重定向次数超限".into())
}

fn archive_path(name: &str, directory: bool) -> Option<&str> {
    let path = if directory {
        name.strip_suffix('/').unwrap_or(name)
    } else {
        name
    };
    if path.is_empty()
        || path.len() > 512
        || path.contains(['\\', ':'])
        || path.chars().any(char::is_control)
        || path
            .split('/')
            .any(|part| part.is_empty() || part == "." || part == "..")
    {
        return None;
    }
    Some(path)
}

fn track_entry(
    names: &mut BTreeSet<String>,
    expanded: &mut u64,
    name: &str,
    size: u64,
    directory: bool,
) -> Result<(), String> {
    let path = archive_path(name, directory).ok_or("官方便携包包含无效条目路径")?;
    if directory && size != 0 {
        return Err("官方便携包包含非空目录条目".into());
    }
    *expanded = expanded
        .checked_add(size)
        .filter(|total| *total <= MAX_EXPANDED)
        .ok_or("官方便携包展开大小超过限制")?;
    if names.len() >= MAX_ENTRIES || !names.insert(path.to_lowercase()) {
        return Err("官方便携包包含重复条目或条目数量超过限制".into());
    }
    Ok(())
}

fn read_executable(file: impl Read, size: u64) -> Result<Vec<u8>, String> {
    if !(1..=MAX_EXECUTABLE).contains(&size) {
        return Err("官方便携程序大小超过限制或为空".into());
    }
    let mut bytes = Vec::new();
    file.take(size + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| "无法读取官方便携程序")?;
    if bytes.len() as u64 != size {
        return Err("官方便携程序大小与归档不匹配".into());
    }
    Ok(bytes)
}

fn extract_zip(archive: &[u8], expected: &str) -> Result<Vec<u8>, String> {
    let mut zip = zip::ZipArchive::new(Cursor::new(archive)).map_err(|_| "官方 ZIP 格式无效")?;
    if zip.len() > MAX_ENTRIES {
        return Err("官方 ZIP 条目数量超过限制".into());
    }
    zip.validate_header_consistency()
        .map_err(|_| "官方 ZIP 包含重复条目或目录记录不一致")?;
    let mut names = BTreeSet::new();
    let mut expanded = 0;
    let mut result = None;
    for index in 0..zip.len() {
        let file = zip.by_index(index).map_err(|_| "官方 ZIP 条目格式无效")?;
        let name = std::str::from_utf8(file.name_raw()).map_err(|_| "官方 ZIP 条目名无效")?;
        let directory = file.is_dir();
        let mode = file.unix_mode().unwrap_or_default() & 0o170000;
        if file.is_symlink() || (mode != 0 && mode != if directory { 0o040000 } else { 0o100000 }) {
            return Err("官方 ZIP 包含链接或特殊条目".into());
        }
        let size = file.size();
        track_entry(&mut names, &mut expanded, name, size, directory)?;
        if name == expected && !directory {
            result = Some(read_executable(file, size)?);
        }
    }
    result.ok_or_else(|| "官方 ZIP 缺少预期的便携程序".into())
}

#[cfg(target_os = "macos")]
fn extract_tar_gzip(archive: &[u8], expected: &str) -> Result<Vec<u8>, String> {
    let gzip = flate2::read::GzDecoder::new(archive);
    // Include headers and padding in the decompressed upper bound.
    let mut tar = tar::Archive::new(gzip.take(MAX_EXPANDED + 1));
    let mut names = BTreeSet::new();
    let mut expanded = 0;
    let mut result = None;
    for item in tar.entries().map_err(|_| "官方 tar.gz 格式无效")? {
        let file = item.map_err(|_| "官方 tar.gz 条目格式无效")?;
        let path = file.path_bytes();
        let name = std::str::from_utf8(&path).map_err(|_| "官方 tar.gz 条目名无效")?;
        let kind = file.header().entry_type();
        if !kind.is_file() && !kind.is_dir() {
            return Err("官方 tar.gz 包含链接或特殊条目".into());
        }
        let size = file.size();
        track_entry(&mut names, &mut expanded, name, size, kind.is_dir())?;
        if name == expected && kind.is_file() {
            result = Some(read_executable(file, size)?);
        }
    }
    // Read any bytes past tar's end marker to verify the gzip trailer.
    let mut gzip = tar.into_inner();
    std::io::copy(&mut gzip, &mut std::io::sink()).map_err(|_| "官方 tar.gz 数据不完整")?;
    if gzip.limit() == 0 {
        return Err("官方 tar.gz 展开大小超过限制".into());
    }
    result.ok_or_else(|| "官方 tar.gz 缺少预期的便携程序".into())
}

#[cfg(not(target_os = "macos"))]
fn extract_tar_gzip(_: &[u8], _: &str) -> Result<Vec<u8>, String> {
    Err("当前系统不支持此官方便携包格式".into())
}
