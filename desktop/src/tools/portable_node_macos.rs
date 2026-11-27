//! Prepare Node using the official Homebrew node-build definition. The Node
//! archive is pinned independently and only the runtime and npm are extracted.
use flate2::read::GzDecoder;
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeSet,
    env,
    ffi::OsString,
    fs,
    io::Read,
    os::unix::fs::symlink,
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::Arc,
    time::Duration,
};
use yss::{
    fs::{FileSnapshot, Root, alias_key},
    model::RelPath,
};

pub(super) const VERSION: &str = "22.23.5";
pub(super) const NODE: &str = "node-runtime/bin/node";
pub(super) const NPM: &str = "node-runtime/lib/node_modules/npm/bin/npm-cli.js";
const URL: &str = "https://nodejs.org/dist/v22.23.5/node-v22.23.5-darwin-arm64.tar.gz";
const SHA256: &str = "e7131995759b61a0170b646010d2ad35a5d37b4cb0d471f3e8317eac6f742f3d";
const LENGTH: u64 = 50_004_868;
const PREFIX: &str = "node-v22.23.5-darwin-arm64/";
const MAX_UNPACKED: u64 = 256 * 1024 * 1024;
const SANDBOX: &str = "/usr/bin/sandbox-exec";
const SYSTEM_CONFIG: &str = "/etc/homebrew/brew.env";
const POLICY_KEYS: &[&str] = &[
    "HOMEBREW_ALLOWED_TAPS",
    "HOMEBREW_API_DOMAIN",
    "HOMEBREW_ARTIFACT_DOMAIN",
    "HOMEBREW_ARTIFACT_DOMAIN_NO_FALLBACK",
    "HOMEBREW_BOTTLE_DOMAIN",
    "HOMEBREW_BREW_GIT_REMOTE",
    "HOMEBREW_CORE_GIT_REMOTE",
    "HOMEBREW_CURL_PATH",
    "HOMEBREW_CURLRC",
    "HOMEBREW_FORBIDDEN_FORMULAE",
    "HOMEBREW_FORBIDDEN_LICENSES",
    "HOMEBREW_FORBIDDEN_TAPS",
    "HOMEBREW_FORCE_BREW_WRAPPER",
    "HOMEBREW_FORCE_BREWED_CA_CERTIFICATES",
    "HOMEBREW_FORCE_BREWED_CURL",
    "HOMEBREW_NO_GITHUB_API",
    "HOMEBREW_NO_INSTALL_FROM_API",
    "HOMEBREW_NO_REQUIRE_TAP_TRUST",
    "HOMEBREW_NO_VERIFY_ATTESTATIONS",
];
const PROXY_KEYS: &[&str] = &[
    "HTTPS_PROXY",
    "https_proxy",
    "HTTP_PROXY",
    "http_proxy",
    "ALL_PROXY",
    "all_proxy",
    "NO_PROXY",
    "no_proxy",
];
const PROFILE: &str = r#"
    (version 1)
    (allow default)
    (deny file-write*)
    (allow file-write* (subpath (param "STAGE")))
    (allow file-write-data (literal "/dev/null"))
"#;

fn relative(path: &str) -> Result<RelPath, String> {
    RelPath::new(path).map_err(|_| "Node 归档路径不安全".into())
}

fn restricted(key: &str, value: &str) -> bool {
    if key == "HOMEBREW_FORBIDDEN_FORMULAE" {
        return value.split_ascii_whitespace().any(|formula| {
            let name = formula.rsplit('/').next().unwrap_or_default();
            matches!(name, "node-build" | "node") || name.starts_with("node@")
        });
    }
    !value.is_empty() && POLICY_KEYS.contains(&key)
}

fn policy_message(key: &str) -> String {
    format!(
        "当前 Homebrew 渠道设置 {key} 不适用于受管 Node 下载；请按本机策略准备兼容 Node 和 npm 后重试"
    )
}

fn check_config(path: &Path) -> Result<(), String> {
    const UNREADABLE: &str =
        "无法安全读取 Homebrew 渠道设置；请按本机策略准备兼容 Node 和 npm 后重试";
    let path = match fs::canonicalize(path) {
        Ok(path) => path,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(_) => return Err(UNREADABLE.into()),
    };
    let parent = path.parent().ok_or(UNREADABLE)?;
    let file = path
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or(UNREADABLE)?;
    let root = Root::open(parent).map_err(|_| UNREADABLE)?;
    let contents = root
        .read_checked_single_link(&relative(file)?, 128 * 1024)
        .map_err(|_| UNREADABLE)?;
    let contents = std::str::from_utf8(&contents.bytes).map_err(|_| UNREADABLE)?;
    for line in contents.lines().map(str::trim_ascii) {
        if let Some((key, value)) = line.split_once('=')
            && (restricted(key, value) || (!value.is_empty() && PROXY_KEYS.contains(&key)))
        {
            return Err(policy_message(key));
        }
    }
    Ok(())
}

pub(super) fn check_channel(brew: &Path) -> Result<(), String> {
    if !Path::new(SANDBOX).is_file() {
        return Err("本机缺少 macOS 沙箱，无法安全下载受管 Node；请先准备兼容 Node 和 npm".into());
    }
    for key in POLICY_KEYS {
        if env::var_os(key).is_some_and(|value| restricted(key, &value.to_string_lossy())) {
            return Err(policy_message(key));
        }
    }
    let prefix = brew
        .parent()
        .and_then(Path::parent)
        .ok_or("Homebrew 路径无效")?;
    let user_config = ["XDG_CONFIG_HOME", "HOMEBREW_XDG_CONFIG_HOME"]
        .into_iter()
        .find_map(|key| env::var_os(key).filter(|value| !value.is_empty()))
        .map(|path| PathBuf::from(path).join("homebrew"))
        .or_else(|| env::var_os("HOME").map(|path| PathBuf::from(path).join(".homebrew")))
        .filter(|path| path.is_absolute())
        .ok_or("无法确认 Homebrew 用户渠道设置的路径")?;
    // Fetch does not enforce Homebrew's install-policy settings on its own.
    // Inspect the real prefix as well as the system and user configuration,
    // since the private fetch prefix does not read the real prefix's file.
    for path in [
        PathBuf::from(SYSTEM_CONFIG),
        prefix.join("etc/homebrew/brew.env"),
        user_config.join("brew.env"),
    ] {
        check_config(&path)?;
    }
    Ok(())
}

fn brew_definition(brew: &Path, stage: &Root) -> Result<(), String> {
    check_channel(brew)?;
    const FETCH: &str = "node-fetch";
    const DOWNLOADS: &str = "node-fetch/cache/downloads";
    for name in [
        FETCH,
        "node-fetch/brew",
        "node-fetch/brew/bin",
        "node-fetch/cache",
        "node-fetch/temp",
        "node-fetch/logs",
    ] {
        stage
            .create_dir(&relative(name)?)
            .map_err(|_| "无法创建 Homebrew 私有下载目录")?;
    }
    let launch = stage.path.join("node-fetch/brew/bin/brew");
    // Homebrew derives its prefix from the link and finds its existing
    // repository through the link target. Its caches and locks stay private.
    symlink(brew, &launch).map_err(|_| "无法建立 Homebrew 私有入口")?;
    let fetch = stage.path.join(FETCH);
    let mut stage_arg = OsString::from("STAGE=");
    stage_arg.push(&fetch);
    let mut command = Command::new(SANDBOX);
    command.env_clear();
    command.envs(
        PROXY_KEYS
            .iter()
            .filter_map(|key| env::var_os(key).map(|value| (key, value))),
    );
    command
        .args(["-D"])
        .arg(stage_arg)
        .args(["-p", PROFILE])
        .arg(&launch)
        .args([
            "fetch",
            "--formula",
            "--force-bottle",
            "homebrew/core/node-build",
        ])
        .env("HOME", &fetch)
        .env("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
        .env("TMPDIR", fetch.join("temp"))
        .env("HOMEBREW_TEMP", fetch.join("temp"))
        .env("HOMEBREW_CACHE", fetch.join("cache"))
        .env("HOMEBREW_LOGS", fetch.join("logs"))
        .env("HOMEBREW_NO_AUTO_UPDATE", "1")
        .env("HOMEBREW_NO_ANALYTICS", "1")
        .env("HOMEBREW_NO_BOOTSNAP", "1")
        .current_dir(&fetch)
        .stdin(Stdio::null());
    let (status, _, _) = yss::workbench::process::capture_command(
        command,
        &yss::cancel::Cancellation::new(),
        Duration::from_secs(180),
    )
    .map_err(|_| "Homebrew 下载 Node 版本定义失败或超时，请检查网络与渠道策略")?;
    if !status.success() {
        return Err("Homebrew 无法在私有目录取得官方 node-build 包，请检查 Homebrew 更新状态、网络或安装策略".into());
    }

    let download = relative(DOWNLOADS)?;
    let names = stage
        .list(Some(&download))
        .map_err(|_| "Homebrew 未返回下载记录")?;
    let mut bottles = names.iter().filter(|name| {
        name.split_once("--").is_some_and(|(_, suffix)| {
            suffix
                .strip_prefix("node-build--")
                .and_then(|version| version.strip_suffix(".all.bottle.tar.gz"))
                .is_some_and(|version| semver::Version::parse(version).is_ok())
        })
    });
    let bottle = bottles
        .next()
        .ok_or("Homebrew 未返回官方 node-build 二进制包")?;
    if names.len() > 32 || bottles.next().is_some() {
        return Err("Homebrew 返回了多个或未知的下载结果".into());
    }
    let path = download
        .join(bottle)
        .map_err(|_| "Homebrew 下载路径不安全")?;
    let archive = stage
        .read_checked_single_link(&path, 4 * 1024 * 1024)
        .map_err(|_| "无法安全读取 Homebrew 版本定义包")?;
    check_definition(&archive.bytes)?;
    // The private fetch directory includes Homebrew's launch link and cache.
    // It is never published as part of the Node/Pi installation.
    fs::remove_dir_all(fetch).map_err(|_| "无法清理 Homebrew 私有下载目录".into())
}

fn check_definition(bytes: &[u8]) -> Result<(), String> {
    let decoder = GzDecoder::new(bytes).take(24 * 1024 * 1024);
    let mut archive = tar::Archive::new(decoder);
    let entries = archive.entries().map_err(|_| "Homebrew 版本定义包无效")?;
    let mut found = false;
    for (index, entry) in entries.enumerate() {
        if index >= 12_000 {
            return Err("Homebrew 版本定义包条目过多".into());
        }
        let mut entry = entry.map_err(|_| "Homebrew 版本定义包无法读取")?;
        let path = entry.path_bytes();
        let parts: Vec<_> = path.split(|&c| c == b'/').collect();
        if parts.len() != 5
            || parts[0] != b"node-build"
            || parts[2..] != [b"share".as_slice(), b"node-build", VERSION.as_bytes()]
            || !std::str::from_utf8(parts[1]).is_ok_and(|part| semver::Version::parse(part).is_ok())
        {
            continue;
        }
        if found || !entry.header().entry_type().is_file() || entry.size() > 8_192 {
            return Err("Homebrew Node 版本定义重复或不安全".into());
        }
        let mut value = String::new();
        entry
            .read_to_string(&mut value)
            .map_err(|_| "Homebrew Node 版本定义无效")?;
        let expected = format!("binary darwin-arm64 \"{URL}#{SHA256}\"");
        let matching: Vec<_> = value
            .lines()
            .filter(|line| line.starts_with("binary darwin-arm64 "))
            .collect();
        if matching.as_slice() != [expected.as_str()] {
            return Err("Homebrew 与 Node.js 官方归档的地址或摘要不一致".into());
        }
        found = true;
    }
    found
        .then_some(())
        .ok_or_else(|| "Homebrew 缺少所需的 macOS ARM64 Node 版本定义".into())
}

fn download() -> Result<Vec<u8>, String> {
    let mut roots = rustls::RootCertStore::empty();
    roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
    let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(|_| "Node 下载 TLS 配置无效")?
    .with_root_certificates(roots)
    .with_no_client_auth();
    let response = reqwest::blocking::Client::builder()
        .https_only(true)
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(180))
        .use_preconfigured_tls(tls)
        .build()
        .and_then(|client| client.get(URL).send())
        .and_then(reqwest::blocking::Response::error_for_status)
        .map_err(|_| "Node.js 官方归档下载失败，请检查网络")?;
    let mut bytes = Vec::new();
    response
        .take(LENGTH + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| "无法完整下载 Node.js 官方归档")?;
    if bytes.len() as u64 != LENGTH || format!("{:x}", Sha256::digest(&bytes)) != SHA256 {
        return Err("Node.js 官方归档长度或摘要不匹配".into());
    }
    Ok(bytes)
}

fn extract(bytes: &[u8], stage: &Root) -> Result<(), String> {
    let decoder = GzDecoder::new(bytes).take(MAX_UNPACKED);
    let mut archive = tar::Archive::new(decoder);
    let entries = archive.entries().map_err(|_| "Node 归档无效")?;
    let mut seen = BTreeSet::new();
    let mut written = BTreeSet::new();
    let mut total = 0_u64;
    stage
        .create_dir(&relative("node-runtime")?)
        .map_err(|_| "无法创建 Node 私有目录")?;
    for (index, entry) in entries.enumerate() {
        if index >= 8_000 {
            return Err("Node 归档条目过多".into());
        }
        let mut entry = entry.map_err(|_| "无法读取 Node 归档条目")?;
        total = total.checked_add(entry.size()).ok_or("Node 归档大小超限")?;
        if total > MAX_UNPACKED {
            return Err("Node 归档解压大小超限".into());
        }
        let name = entry.path_bytes();
        let name = std::str::from_utf8(&name).map_err(|_| "Node 归档包含非 UTF-8 路径")?;
        let name = name.strip_prefix(PREFIX).ok_or("Node 归档根目录不匹配")?;
        let name = name.trim_end_matches('/');
        let kind = entry.header().entry_type();
        if name.is_empty() && kind.is_dir() {
            continue;
        }
        let path = relative(&format!("node-runtime/{name}"))?;
        if !seen.insert(alias_key(path.as_str())) {
            return Err("Node 归档包含重复路径".into());
        }
        if kind.is_symlink() && matches!(name, "bin/npm" | "bin/npx" | "bin/corepack") {
            continue; // The workbench invokes the verified npm entry directly.
        }
        if !kind.is_file() && !kind.is_dir() {
            return Err("Node 归档包含不支持的文件类型".into());
        }
        if !kind.is_file() || (path.as_str() != NODE && !name.starts_with("lib/node_modules/npm/"))
        {
            continue;
        }
        for parent in path.parents() {
            let parent = relative(parent)?;
            match stage
                .inspect(&parent)
                .map_err(|_| "无法检查 Node 私有目录")?
            {
                FileSnapshot::Missing => {
                    stage
                        .create_dir(&parent)
                        .map_err(|_| "无法创建 Node 私有目录")?;
                }
                FileSnapshot::Directory { .. } => {}
                _ => return Err("Node 归档文件与目录冲突".into()),
            }
        }
        let size = entry.size();
        let mut output = Vec::new();
        (&mut entry)
            .take(size + 1)
            .read_to_end(&mut output)
            .map_err(|_| "无法解压 Node 归档")?;
        if output.len() as u64 != size {
            return Err("Node 归档条目长度不匹配".into());
        }
        let mode = if path.as_str() == NODE { 0o700 } else { 0o600 };
        stage
            .create_file(&path, &output, mode)
            .map_err(|_| "无法安全写入私有 Node")?;
        written.insert(path);
    }
    if !written.contains(&relative(NODE)?) || !written.contains(&relative(NPM)?) {
        return Err("Node 或 npm 入口缺失".into());
    }
    Ok(())
}

pub(super) fn prepare(brew: &Path, stage: &Root) -> Result<(), String> {
    brew_definition(brew, stage)?;
    extract(&download()?, stage)
}
