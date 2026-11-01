use crate::{
    cancel::Cancellation,
    codec::{self, Limits},
    error::{ErrorCode, Result, YssError},
    model::{MAX_ASSET, MAX_MANIFEST, Sha256},
    updater::release,
};
use reqwest::{
    Client, Method, Response, Url,
    header::{HeaderValue, LOCATION},
};
use rustls::pki_types::{CertificateDer, pem::PemObject};
use serde::Deserialize;
use sha2::Digest;
use std::{
    collections::{BTreeMap, BTreeSet},
    future::Future,
    path::Path,
    time::Duration,
};
const ORIGIN: &str = "https://gitlab.yss.test:9443";
const PROJECT: &str = "yss/yss-cli";
const PROJECT_LOOKUP: &str = "https://gitlab.yss.test:9443/api/v4/projects/yss%2Fyss-cli";
fn network(message: &str) -> YssError {
    YssError::new(ErrorCode::Network, message)
}
fn invalid(message: &str) -> YssError {
    YssError::new(ErrorCode::InvalidInput, message)
}
pub struct GitlabAuth {
    client: Client,
    token: HeaderValue,
}
impl GitlabAuth {
    pub fn from_environment() -> Result<Self> {
        let ca = std::env::var_os("YSS_GITLAB_CA_FILE")
            .ok_or_else(|| network("固定 GitLab 需要显式 YSS_GITLAB_CA_FILE"))?;
        let ca = release::read_local(Path::new(&ca), MAX_MANIFEST)
            .map_err(|_| network("无法安全读取 GitLab CA 文件"))?;
        let token = std::env::var("YSS_GITLAB_TOKEN")
            .map_err(|_| network("固定 GitLab 需要 YSS_GITLAB_TOKEN（受限 PAT）"))?;
        Self::new(&ca, &token)
    }
    fn new(ca: &[u8], token: &str) -> Result<Self> {
        if token.is_empty() || token.len() > 4096 || !token.bytes().all(|b| b.is_ascii_graphic()) {
            return Err(network("GitLab 令牌格式无效"));
        }
        let mut token = HeaderValue::from_str(token).map_err(|_| network("GitLab 令牌格式无效"))?;
        token.set_sensitive(true);
        let mut roots = rustls::RootCertStore::empty();
        for certificate in CertificateDer::pem_slice_iter(ca) {
            roots
                .add(certificate.map_err(|_| network("GitLab CA 不是有效 PEM"))?)
                .map_err(|_| network("GitLab CA 无效"))?;
        }
        if roots.is_empty() {
            return Err(network("GitLab CA 文件没有证书"));
        }
        let tls = rustls::ClientConfig::builder_with_provider(std::sync::Arc::new(
            rustls::crypto::ring::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .map_err(|_| network("TLS 配置失败"))?
        .with_root_certificates(roots)
        .with_no_client_auth();
        let client = Client::builder()
            .tls_backend_preconfigured(tls)
            .https_only(true)
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .referer(false)
            .connect_timeout(Duration::from_secs(10))
            .read_timeout(Duration::from_secs(20))
            .timeout(Duration::from_secs(120))
            .user_agent(concat!("yss/", env!("CARGO_PKG_VERSION")))
            .build()
            .map_err(|_| network("无法创建 GitLab 客户端"))?;
        Ok(Self { client, token })
    }
}
pub struct RemoteRelease {
    version: semver::Version,
    target: String,
    url: String,
    sha256: Sha256,
    size: u64,
    allowed: BTreeSet<String>,
}
impl RemoteRelease {
    pub fn version(&self) -> &semver::Version {
        &self.version
    }
    pub fn sha256(&self) -> &Sha256 {
        &self.sha256
    }
    pub fn size(&self) -> u64 {
        self.size
    }
}
#[derive(Deserialize)]
struct Project {
    id: u64,
    path_with_namespace: String,
    web_url: String,
}
#[derive(Deserialize)]
struct Release {
    tag_name: String,
    released_at: String,
    #[serde(default)]
    upcoming_release: bool,
    #[serde(rename = "_links")]
    links: ReleaseLinks,
    assets: Assets,
}
#[derive(Deserialize)]
struct ReleaseLinks {
    #[serde(rename = "self")]
    url: String,
}
#[derive(Deserialize)]
struct Assets {
    links: Vec<Asset>,
}
#[derive(Deserialize)]
struct Asset {
    name: String,
    url: String,
    #[serde(default)]
    direct_asset_url: Option<String>,
}
fn allowed_url(value: &str, allowed: &BTreeSet<String>) -> Result<Url> {
    let url = Url::parse(value).map_err(|_| network("GitLab URL 不合法"))?;
    if url.origin().ascii_serialization() != ORIGIN
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || !allowed.contains(value)
        || url.as_str() != value
    {
        return Err(network("拒绝向未核验来源发送请求或凭据"));
    }
    Ok(url)
}
async fn request(
    auth: &GitlabAuth,
    method: Method,
    value: &str,
    allowed: &BTreeSet<String>,
    cancel: &Cancellation,
) -> Result<Response> {
    let mut allowed = allowed.clone();
    let mut url = allowed_url(value, &allowed)?;
    for _ in 0..=5 {
        cancel.check()?;
        let request = auth
            .client
            .request(method.clone(), url.clone())
            .header("PRIVATE-TOKEN", auth.token.clone())
            .header("Accept-Encoding", "identity");
        let response = tokio::select! {biased;_ = cancel.cancelled()=>return Err(YssError::new(ErrorCode::Cancelled,"网络操作已取消")),r=request.send()=>r.map_err(|_|network("固定 GitLab 请求失败；检查 CA、网络与鉴权"))?};
        if response.status().is_redirection() {
            let location = response
                .headers()
                .get(LOCATION)
                .and_then(|v| v.to_str().ok())
                .ok_or_else(|| network("GitLab 重定向缺少合法位置"))?;
            let next = url
                .join(location)
                .map_err(|_| network("GitLab 重定向位置不合法"))?;
            if let Some(base) = url.as_str().strip_suffix("permalink/latest")
                && let Some(project) = base
                    .strip_prefix(&format!("{ORIGIN}/api/v4/projects/"))
                    .and_then(|s| s.strip_suffix("/releases/"))
                && project
                    .parse::<u64>()
                    .is_ok_and(|id| id > 0 && id.to_string() == project)
                && let Some(tag) = next.as_str().strip_prefix(base)
                && let Ok(version) = release::version(tag, true)
                && tag == format!("v{version}")
            {
                allowed.insert(next.to_string());
            }
            url = allowed_url(next.as_str(), &allowed)?;
            continue;
        }
        if response.status().as_u16() == 404 {
            return Err(YssError::new(
                ErrorCode::NotReady,
                "固定 GitLab 项目或正式 Rust 发行尚未就绪",
            ));
        }
        if !response.status().is_success() {
            return Err(
                network("GitLab 返回非成功状态").context("httpStatus", response.status().as_u16())
            );
        }
        if response
            .headers()
            .get("content-encoding")
            .is_some_and(|h| h != "identity")
        {
            return Err(network("GitLab 返回未请求的内容编码"));
        }
        return Ok(response);
    }
    Err(network("GitLab 重定向次数超限"))
}
fn response_length(response: &Response) -> Result<Option<u64>> {
    let mut values = response
        .headers()
        .get_all(reqwest::header::CONTENT_LENGTH)
        .iter();
    let Some(value) = values.next() else {
        return Ok(None);
    };
    if values.next().is_some() {
        return Err(network("响应包含重复长度头"));
    }
    let value = value.to_str().map_err(|_| network("响应长度头无效"))?;
    if value.is_empty() || !value.bytes().all(|b| b.is_ascii_digit()) {
        return Err(network("响应长度头无效"));
    }
    Ok(Some(value.parse().map_err(|_| network("响应长度超限"))?))
}
async fn collect(
    mut response: Response,
    limit: u64,
    expected: Option<u64>,
    cancel: &Cancellation,
) -> Result<(Vec<u8>, Sha256)> {
    let declared = response_length(&response)?;
    if declared.is_some_and(|n| n > limit)
        || expected.is_some_and(|n| declared.is_some_and(|m| m != n))
    {
        return Err(network("响应声明长度不合法"));
    }
    let mut output = Vec::new();
    let mut hasher = sha2::Sha256::new();
    loop {
        let chunk = tokio::select! {biased;_ = cancel.cancelled()=>return Err(YssError::new(ErrorCode::Cancelled,"网络读取已取消")),r=response.chunk()=>r.map_err(|_|network("GitLab 响应流中断或超时"))?};
        let Some(chunk) = chunk else { break };
        let total = (output.len() as u64)
            .checked_add(chunk.len() as u64)
            .ok_or_else(|| network("响应长度溢出"))?;
        if total > limit || expected.is_some_and(|n| total > n) {
            return Err(network("响应读取超过长度上限"));
        }
        hasher.update(&chunk);
        output.extend_from_slice(&chunk);
    }
    if declared.is_some_and(|n| n != output.len() as u64)
        || expected.is_some_and(|n| n != output.len() as u64)
    {
        return Err(network("响应长度与声明不匹配"));
    }
    Ok((output, format!("{:x}", hasher.finalize()).try_into()?))
}
async fn metadata<T: serde::de::DeserializeOwned>(
    auth: &GitlabAuth,
    url: &str,
    allowed: &BTreeSet<String>,
    cancel: &Cancellation,
) -> Result<(T, Url)> {
    let response = request(auth, Method::GET, url, allowed, cancel).await?;
    let resolved_url = response.url().clone();
    let (bytes, _) = collect(response, MAX_MANIFEST, None, cancel).await?;
    Ok((
        codec::decode_json(&bytes, Limits::manifest())?,
        resolved_url,
    ))
}
fn asset(
    release: &Release,
    name: &str,
    project: u64,
    allowed: &mut BTreeSet<String>,
) -> Result<String> {
    let found: Vec<_> = release
        .assets
        .links
        .iter()
        .filter(|a| a.name == name)
        .collect();
    if found.len() != 1 {
        return Err(YssError::new(
            if found.is_empty() {
                ErrorCode::NotReady
            } else {
                ErrorCode::InvalidInput
            },
            "发行缺少唯一的当前平台资产",
        ));
    }
    let asset = found[0];
    let direct = format!(
        "{ORIGIN}/{PROJECT}/-/releases/{}/downloads/{name}",
        release.tag_name
    );
    let package = format!(
        "{ORIGIN}/api/v4/projects/{project}/packages/generic/yss/{}/{name}",
        release.tag_name
    );
    let upload = asset
        .url
        .strip_prefix(&format!("{ORIGIN}/{PROJECT}/uploads/"))
        .and_then(|p| p.split_once('/'))
        .is_some_and(|(hash, file)| {
            hash.len() == 32
                && hash
                    .bytes()
                    .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
                && file == name
        });
    if asset.url != direct && asset.url != package && !upload {
        return Err(invalid("发行资产不属于固定项目、版本和文件"));
    }
    let mut scoped = BTreeSet::from([asset.url.clone()]);
    allowed_url(&asset.url, &scoped)?;
    let selected = if let Some(url) = asset.direct_asset_url.as_deref().filter(|s| !s.is_empty()) {
        if url != direct {
            return Err(invalid("发行资产下载 URL 不匹配"));
        }
        scoped.insert(direct.clone());
        direct
    } else {
        asset.url.clone()
    };
    allowed.extend(scoped);
    Ok(selected)
}
fn run<T>(
    cancel: &Cancellation,
    duration: Duration,
    future: impl Future<Output = Result<T>>,
) -> Result<T> {
    cancel.check()?;
    let rt = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(|_| network("无法启动网络运行时"))?;
    rt.block_on(async{tokio::select!{biased;_=cancel.cancelled()=>Err(YssError::new(ErrorCode::Cancelled,"网络操作已取消")),result=tokio::time::timeout(duration,future)=>result.map_err(|_|network("网络操作总时限已到"))?}})
}
pub fn resolve_release(
    version: Option<&semver::Version>,
    target: &str,
    auth: &GitlabAuth,
    cancel: &Cancellation,
) -> Result<RemoteRelease> {
    release::binary_name(target)?;
    run(
        cancel,
        Duration::from_secs(90),
        resolve(version, target, auth, cancel),
    )
}
async fn resolve(
    version: Option<&semver::Version>,
    target: &str,
    auth: &GitlabAuth,
    cancel: &Cancellation,
) -> Result<RemoteRelease> {
    let mut allowed = BTreeSet::from([PROJECT_LOOKUP.to_owned()]);
    let (project, _): (Project, _) = metadata(auth, PROJECT_LOOKUP, &allowed, cancel).await?;
    if project.id == 0
        || project.path_with_namespace != PROJECT
        || project.web_url != format!("{ORIGIN}/{PROJECT}")
    {
        return Err(invalid("GitLab 项目 ID、完整路径或 Web URL 不匹配"));
    }
    let endpoint = format!(
        "{ORIGIN}/api/v4/projects/{}/releases/{}",
        project.id,
        version.map_or_else(|| "permalink/latest".into(), |v| format!("v{v}"))
    );
    allowed.insert(endpoint.clone());
    let (item, resolved_url): (Release, _) = metadata(auth, &endpoint, &allowed, cancel).await?;
    let selected =
        release::version(&item.tag_name, true).map_err(|_| invalid("发行不是正式 SemVer"))?;
    let released = time::OffsetDateTime::parse(
        &item.released_at,
        &time::format_description::well_known::Rfc3339,
    )
    .map_err(|_| invalid("发行时间不合法"))?;
    if item.upcoming_release
        || released > time::OffsetDateTime::now_utc()
        || item.tag_name != format!("v{selected}")
        || resolved_url.as_str()
            != format!(
                "{ORIGIN}/api/v4/projects/{}/releases/v{selected}",
                project.id
            )
        || item.links.url != format!("{ORIGIN}/{PROJECT}/-/releases/{}", item.tag_name)
        || version.is_some_and(|v| v != &selected)
    {
        return Err(invalid("发行版本、来源或稳定状态不一致"));
    }
    let name = release::filename(&selected.to_string(), target)?;
    let checksum = asset(&item, "SHA256SUMS", project.id, &mut allowed)?;
    let url = asset(&item, &name, project.id, &mut allowed)?;
    let response = request(auth, Method::GET, &checksum, &allowed, cancel).await?;
    let (bytes, _) = collect(response, MAX_MANIFEST, None, cancel).await?;
    let mut sums = BTreeMap::new();
    for line in std::str::from_utf8(&bytes)
        .map_err(|_| invalid("SHA256SUMS 不是 UTF-8"))?
        .lines()
        .filter(|s| !s.trim().is_empty())
    {
        let fields: Vec<_> = line.split_whitespace().collect();
        if fields.len() != 2 {
            return Err(invalid("SHA256SUMS 格式不合法"));
        }
        let name = fields[1].strip_prefix('*').unwrap_or(fields[1]);
        let path = crate::model::RelPath::new(name)?;
        if path.as_str().contains('/')
            || sums
                .insert(name.to_owned(), Sha256::try_from(fields[0].to_owned())?)
                .is_some()
        {
            return Err(invalid("SHA256SUMS 名称重复或不合法"));
        }
    }
    let sha256 = sums
        .remove(&name)
        .ok_or_else(|| invalid("SHA256SUMS 缺少当前平台包"))?;
    let response = request(auth, Method::HEAD, &url, &allowed, cancel).await?;
    let size = response_length(&response)?.ok_or_else(|| network("发行资产 HEAD 未提供长度"))?;
    if size == 0 || size > MAX_ASSET {
        return Err(network("发行资产长度不合法"));
    }
    Ok(RemoteRelease {
        version: selected,
        target: target.into(),
        url,
        sha256,
        size,
        allowed,
    })
}
pub fn download(
    release: &RemoteRelease,
    auth: &GitlabAuth,
    cancel: &Cancellation,
) -> Result<Vec<u8>> {
    run(cancel, Duration::from_secs(180), async {
        release::binary_name(&release.target)?;
        let response = request(auth, Method::GET, &release.url, &release.allowed, cancel).await?;
        let (bytes, sha) = collect(response, MAX_ASSET, Some(release.size), cancel).await?;
        if sha != release.sha256 {
            return Err(YssError::new(
                ErrorCode::DigestMismatch,
                "下载归档 SHA-256 与发行清单不同",
            ));
        }
        Ok(bytes)
    })
}
