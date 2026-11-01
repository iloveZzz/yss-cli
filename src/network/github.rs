use super::*;

const API: &str = "https://api.github.com/repos/iloveZzz/yss-cli/releases/";
const RELEASES: &str = "https://github.com/iloveZzz/yss-cli/releases/";

pub(super) fn client() -> Result<Client> {
    let _ = rustls::crypto::ring::default_provider().install_default();
    Client::builder()
        .tls_backend_rustls()
        .https_only(true)
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .referer(false)
        .connect_timeout(Duration::from_secs(10))
        .read_timeout(Duration::from_secs(20))
        .timeout(Duration::from_secs(120))
        .user_agent(concat!("yss/", env!("CARGO_PKG_VERSION")))
        .build()
        .map_err(|_| network("无法创建 GitHub 客户端"))
}

fn cdn(url: &Url) -> bool {
    url.scheme() == "https"
        && url.username().is_empty()
        && url.password().is_none()
        && url.port().is_none()
        && url.fragment().is_none()
        && matches!(
            url.host_str(),
            Some(
                "release-assets.githubusercontent.com"
                    | "objects.githubusercontent.com"
                    | "github-releases.githubusercontent.com"
            )
        )
}

async fn request(
    client: &Client,
    method: Method,
    value: &str,
    allowed: &BTreeSet<String>,
    cancel: &Cancellation,
) -> Result<Response> {
    if !allowed.contains(value) {
        return Err(network("拒绝请求未核验的 GitHub URL"));
    }
    let mut url = Url::parse(value).map_err(|_| network("GitHub URL 不合法"))?;
    let download = value.starts_with(&format!("{RELEASES}download/"));
    for _ in 0..=5 {
        cancel.check()?;
        let pending = client
            .request(method.clone(), url.clone())
            .header("Accept", "application/vnd.github+json")
            .header("Accept-Encoding", "identity")
            .send();
        let response = tokio::select! {biased;_ = cancel.cancelled()=>return Err(YssError::new(ErrorCode::Cancelled,"网络操作已取消")),r=pending=>r.map_err(|_|network("固定 GitHub 请求失败；检查网络"))?};
        if response.status().is_redirection() {
            let next = response
                .headers()
                .get(LOCATION)
                .and_then(|v| v.to_str().ok())
                .and_then(|s| url.join(s).ok())
                .ok_or_else(|| network("GitHub 重定向位置不合法"))?;
            if !download || !cdn(&next) {
                return Err(network("拒绝 GitHub 资产重定向到未核验来源"));
            }
            url = next;
            continue;
        }
        if response.status().as_u16() == 404 {
            return Err(YssError::new(
                ErrorCode::NotReady,
                "GitHub 正式 Rust 发行尚未就绪",
            ));
        }
        if response.status().as_u16() != 200 {
            return Err(
                network("GitHub 返回非成功状态").context("httpStatus", response.status().as_u16())
            );
        }
        if response
            .headers()
            .get("content-encoding")
            .is_some_and(|h| h != "identity")
        {
            return Err(network("GitHub 返回未请求的内容编码"));
        }
        return Ok(response);
    }
    Err(network("GitHub 重定向次数超限"))
}

#[derive(Deserialize)]
struct GithubRelease {
    tag_name: String,
    html_url: String,
    draft: bool,
    prerelease: bool,
    published_at: String,
    assets: Vec<GithubAsset>,
}
#[derive(Deserialize)]
struct GithubAsset {
    name: String,
    browser_download_url: String,
    size: u64,
}
fn asset<'a>(item: &'a GithubRelease, name: &str, limit: u64) -> Result<&'a GithubAsset> {
    let mut matches = item.assets.iter().filter(|a| a.name == name);
    let found = matches
        .next()
        .ok_or_else(|| YssError::new(ErrorCode::NotReady, "GitHub 发行缺少当前平台资产"))?;
    if matches.next().is_some()
        || found.size == 0
        || found.size > limit
        || found.browser_download_url != format!("{RELEASES}download/{}/{name}", item.tag_name)
    {
        return Err(invalid("GitHub 资产名称、来源或大小不合法"));
    }
    Ok(found)
}

pub(super) async fn resolve(
    client: &Client,
    version: Option<&semver::Version>,
    target: &str,
    cancel: &Cancellation,
) -> Result<RemoteRelease> {
    release::binary_name(target)?;
    let endpoint = format!(
        "{API}{}",
        version.map_or_else(|| "latest".into(), |v| format!("tags/v{v}"))
    );
    let mut allowed = BTreeSet::from([endpoint.clone()]);
    let response = request(client, Method::GET, &endpoint, &allowed, cancel).await?;
    let (bytes, _) = collect(response, MAX_MANIFEST, None, cancel).await?;
    let item: GithubRelease = codec::decode_json(&bytes, Limits::manifest())?;
    let selected = release::version(&item.tag_name, true)
        .map_err(|_| invalid("GitHub 发行不是正式 SemVer"))?;
    let published = time::OffsetDateTime::parse(
        &item.published_at,
        &time::format_description::well_known::Rfc3339,
    )
    .map_err(|_| invalid("GitHub 发行时间不合法"))?;
    if item.draft
        || item.prerelease
        || published > time::OffsetDateTime::now_utc()
        || item.tag_name != format!("v{selected}")
        || version.is_some_and(|v| v != &selected)
        || item.html_url != format!("{RELEASES}tag/{}", item.tag_name)
    {
        return Err(invalid("GitHub 发行版本、来源或稳定状态不一致"));
    }
    let name = release::filename(&selected.to_string(), target)?;
    let sums = asset(&item, "SHA256SUMS", MAX_MANIFEST)?;
    let archive = asset(&item, &name, MAX_ASSET)?;
    allowed.extend([
        sums.browser_download_url.clone(),
        archive.browser_download_url.clone(),
    ]);
    let response = request(
        client,
        Method::GET,
        &sums.browser_download_url,
        &allowed,
        cancel,
    )
    .await?;
    let (bytes, _) = collect(response, MAX_MANIFEST, Some(sums.size), cancel).await?;
    let sha256 = checksum_for(&bytes, &name)?;
    let response = request(
        client,
        Method::HEAD,
        &archive.browser_download_url,
        &allowed,
        cancel,
    )
    .await?;
    if response_length(&response)? != Some(archive.size) {
        return Err(network("GitHub 资产 HEAD 与发行长度不同"));
    }
    Ok(RemoteRelease {
        version: selected,
        target: target.into(),
        url: archive.browser_download_url.clone(),
        sha256,
        size: archive.size,
        allowed,
    })
}

pub(super) async fn download(
    client: &Client,
    remote: &RemoteRelease,
    cancel: &Cancellation,
) -> Result<Vec<u8>> {
    let response = request(client, Method::GET, &remote.url, &remote.allowed, cancel).await?;
    let (bytes, sha) = collect(response, MAX_ASSET, Some(remote.size), cancel).await?;
    if sha != remote.sha256 {
        return Err(YssError::new(
            ErrorCode::DigestMismatch,
            "下载归档 SHA-256 与发行清单不同",
        ));
    }
    Ok(bytes)
}
