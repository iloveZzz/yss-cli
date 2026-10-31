package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const githubAPI = "https://api.github.com/repos/iloveZzz/yss-cli/releases/"
const releaseBase = "https://github.com/iloveZzz/yss-cli/releases/"
const metadataLimit = 256 << 10

// UpgradeClient's optional fields are OS/network seams. The production CLI
// uses their defaults; the repository and release channel cannot be changed.
type UpgradeClient struct {
	HTTPClient *http.Client
	Executable func() (string, error)
}

type UpgradeRequest struct {
	Check    bool
	DryRun   bool
	Artifact string
	SHA256   string
	To       string
	ToolRoot string
}

type UpgradeResult struct {
	Status          string              `json:"status"`
	CurrentVersion  string              `json:"currentVersion"`
	TargetVersion   string              `json:"targetVersion"`
	UpdateAvailable bool                `json:"updateAvailable"`
	Platform        string              `json:"platform"`
	ToolRoot        string              `json:"toolRoot,omitempty"`
	ReleaseURL      string              `json:"releaseUrl"`
	ArchiveSHA256   string              `json:"archiveSha256"`
	Transaction     *transaction.Result `json:"transaction,omitempty"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type githubRelease struct {
	Tag        string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	URL        string        `json:"html_url"`
	Assets     []githubAsset `json:"assets"`
}

type onlineArtifact struct {
	Archive string
	SHA256  string
	Bytes   int64
}
type onlineRelease struct {
	githubRelease
	version  string
	artifact onlineArtifact
	asset    githubAsset
}

func stableVersion(raw string) (string, error) {
	version := strings.TrimPrefix(raw, "v")
	if _, err := versionParts(version); err != nil {
		return "", err
	}
	if strings.ContainsAny(version, "-+") {
		return "", fail("VERSION", "在线升级只支持无预发布或构建后缀的稳定版本")
	}
	return version, nil
}

func (c UpgradeClient) client() *http.Client {
	client := http.Client{Timeout: 5 * time.Minute}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fail("NETWORK", "发行下载重定向次数超限")
		}
		allowed := map[string]bool{"api.github.com": true, "github.com": true, "release-assets.githubusercontent.com": true, "objects.githubusercontent.com": true, "github-releases.githubusercontent.com": true}
		if req.URL.Scheme != "https" || !allowed[req.URL.Hostname()] || req.URL.User != nil {
			return fail("NETWORK", "拒绝发行下载重定向到未知来源")
		}
		return nil
	}
	return &client
}

func (c UpgradeClient) get(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, domain.Explain(domain.Wrap("NETWORK", err), "RELEASE_REQUEST_INVALID", "固定发行来源请求无法构建。", map[string]any{"source": address, "phase": "request"})
	}
	req.Header.Set("User-Agent", "yss/"+domain.Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, domain.Wrap("CANCELLED", ctx.Err())
		}
		return nil, domain.Explain(domain.Wrap("NETWORK", err), "RELEASE_REQUEST_FAILED", "连接固定发行来源失败。", map[string]any{"source": address, "phase": "request"})
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		detail := map[string]any{"source": address, "phase": "request", "httpStatus": resp.StatusCode}
		// Only bounded numeric limits and a GitHub request ID are safe to retain; never copy bodies or arbitrary headers.
		for header, key := range map[string]string{"X-RateLimit-Limit": "githubRateLimit", "X-RateLimit-Remaining": "githubRateRemaining", "X-RateLimit-Reset": "githubRateReset", "Retry-After": "retryAfter"} {
			value := resp.Header.Get(header)
			if value != "" && len(value) <= 20 && strings.Trim(value, "0123456789") == "" {
				detail[key] = value
			}
		}
		if value := resp.Header.Get("X-GitHub-Request-Id"); value != "" && len(value) <= 128 && strings.Trim(value, "0123456789abcdefABCDEF:") == "" {
			detail["githubRequestId"] = value
		}
		return nil, domain.Explain(fail("NETWORK", fmt.Sprintf("GitHub 返回 HTTP %d；检查网络、版本是否发布或稍后重试", resp.StatusCode)), "RELEASE_HTTP_STATUS", "固定发行来源返回了非成功 HTTP 状态。", detail)
	}
	return resp, nil
}

func (c UpgradeClient) metadata(ctx context.Context, address string, v any, strict bool) error {
	r, err := c.get(ctx, address)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, metadataLimit+1))
	if err != nil {
		if ctx.Err() != nil {
			return domain.Wrap("CANCELLED", ctx.Err())
		}
		return domain.Wrap("NETWORK", err)
	}
	if len(b) > metadataLimit {
		return fail("ARTIFACT", "发行 metadata 超过 256 KiB")
	}
	if !json.Valid(b) {
		return fail("ARTIFACT", "发行 metadata 必须为单个 JSON 文档")
	}
	if _, err = schema.Parse(b); err != nil {
		return domain.Wrap("ARTIFACT", err)
	}
	if strict {
		return parseJSON(b, v)
	}
	return json.Unmarshal(b, v)
}

func assetFor(r githubRelease, name string) (githubAsset, error) {
	var found githubAsset
	for _, asset := range r.Assets {
		if asset.Name != name {
			continue
		}
		if found.Name != "" {
			return found, fail("ARTIFACT", "发行资产名称重复: "+name)
		}
		if asset.URL != releaseBase+"download/"+r.Tag+"/"+url.PathEscape(name) {
			return found, fail("ARTIFACT", "发行资产 URL 不属于固定仓库和版本: "+name)
		}
		found = asset
	}
	if found.Name == "" {
		return found, fail("ARTIFACT", "发行缺少资产: "+name)
	}
	return found, nil
}

func (c UpgradeClient) resolve(ctx context.Context, target string) (onlineRelease, error) {
	var selected onlineRelease
	endpoint := githubAPI + "latest"
	if target != "" {
		v, err := stableVersion(target)
		if err != nil {
			return selected, err
		}
		target = v
		endpoint = githubAPI + "tags/v" + v
	}
	if err := c.metadata(ctx, endpoint, &selected.githubRelease, false); err != nil {
		return selected, err
	}
	r := selected.githubRelease
	version, err := stableVersion(r.Tag)
	if err != nil {
		return selected, err
	}
	if r.Tag != "v"+version || r.Draft || r.Prerelease || r.URL != releaseBase+"tag/"+r.Tag || target != "" && version != target {
		return selected, fail("ARTIFACT", "发行版本、来源或稳定版状态不一致")
	}
	checksum, err := assetFor(r, "SHA256SUMS")
	if err != nil {
		return selected, err
	}
	response, err := c.get(ctx, checksum.URL)
	if err != nil {
		return selected, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, metadataLimit+1))
	if err != nil {
		return selected, err
	}
	if len(data) > metadataLimit {
		return selected, fail("ARTIFACT", "SHA256SUMS 超限")
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	name := "yss_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ext
	sums := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !digestPattern.MatchString(fields[0]) {
			return selected, fail("ARTIFACT", "SHA256SUMS 格式不合法")
		}
		ref := strings.TrimPrefix(fields[1], "*")
		if _, ok := sums[ref]; ok {
			return selected, fail("ARTIFACT", "SHA256SUMS 文件重复")
		}
		sums[ref] = fields[0]
	}
	if sums[name] == "" {
		return selected, fail("ARTIFACT", "SHA256SUMS 缺少当前平台发行包")
	}
	selected.asset, err = assetFor(r, name)
	if err != nil {
		return selected, err
	}
	if selected.asset.Size <= 0 || selected.asset.Size > maxArchive {
		return selected, fail("ARTIFACT", "发行资产大小不合法")
	}
	selected.version = version
	selected.artifact = onlineArtifact{name, sums[name], selected.asset.Size}
	return selected, nil
}

func (c UpgradeClient) Run(ctx context.Context, request UpgradeRequest) (UpgradeResult, error) {
	var result UpgradeResult
	if err := cancellation(ctx); err != nil {
		return result, err
	}
	if (request.Artifact == "") != (request.SHA256 == "") {
		return result, fail("ARGUMENT", "离线升级须同时提供 --artifact 和 --sha256")
	}
	result.Platform = runtime.GOOS + "/" + runtime.GOARCH
	result.CurrentVersion = domain.Version
	readOnly := request.Check || request.DryRun
	automatic := request.ToolRoot == ""
	if automatic {
		executable := c.Executable
		if executable == nil {
			executable = os.Executable
		}
		path, err := executable()
		if err != nil {
			return result, err
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return result, err
		}
		if filepath.Base(path) != fileName() {
			return result, fail("INSTALLATION", "未识别 yss 入口；请显式指定 --tool-root")
		}
		request.ToolRoot = filepath.Dir(path)
	}
	var err error
	result.ToolRoot, err = toolRootPath(request.ToolRoot)
	if err != nil {
		return result, err
	}
	installed, _, err := inspectInstallation(result.ToolRoot, true, c.Executable)
	if err != nil {
		return result, err
	}
	if installed == nil {
		if automatic {
			return result, fail("INSTALLATION", "运行目录缺少有效安装记录；请显式选择新的 --tool-root")
		}
		result.CurrentVersion = ""
	} else {
		result.CurrentVersion = installed.receipt.CLIVersion
	}
	var release onlineRelease
	var plan *Plan
	if request.Artifact != "" {
		plan, err = Build(result.ToolRoot, request.Artifact, request.SHA256)
		if err != nil {
			return result, err
		}
		result.TargetVersion = plan.Manifest.CLIVersion
		result.ArchiveSHA256 = request.SHA256
		if request.To != "" {
			target, e := stableVersion(request.To)
			if e != nil {
				return result, e
			}
			if target != result.TargetVersion {
				return result, fail("VERSION", "离线发行包版本与 --to 不匹配")
			}
		}
	} else {
		release, err = c.resolve(ctx, request.To)
		if err != nil {
			return result, err
		}
		result.TargetVersion = release.version
		result.ReleaseURL = release.URL
		result.ArchiveSHA256 = release.artifact.SHA256
	}
	order := 1
	if result.CurrentVersion != "" {
		order, err = compareVersions(result.TargetVersion, result.CurrentVersion)
		if err != nil {
			return result, err
		}
	}
	result.UpdateAvailable = order > 0
	if order < 0 {
		return result, fail("VERSION", "目标版本低于当前安装，拒绝降级")
	}
	if readOnly {
		result.Status = "checked"
		return result, nil
	}
	if installed != nil && order == 0 && installed.receipt.ArchiveDigest == result.ArchiveSHA256 {
		result.Status = "unchanged"
		return result, nil
	}
	if plan == nil {
		tmp, e := os.MkdirTemp("", "yss-upgrade-")
		if e != nil {
			return result, e
		}
		defer os.RemoveAll(tmp)
		path := filepath.Join(tmp, release.artifact.Archive)
		if err = c.download(ctx, release, path); err != nil {
			return result, err
		}
		plan, err = Build(result.ToolRoot, path, release.artifact.SHA256)
		if err != nil {
			return result, err
		}
		if plan.Manifest.CLIVersion != result.TargetVersion {
			return result, fail("VERSION", "发行包版本与在线发行不匹配")
		}
	}
	if err = cancellation(ctx); err != nil {
		return result, err
	}
	txn, err := Apply(ctx, plan)
	if err != nil {
		return result, err
	}
	result.Transaction = &txn
	result.Status = "upgraded"
	if installed == nil {
		result.Status = "installed"
	}
	result.UpdateAvailable = false
	return result, nil
}

func (c UpgradeClient) download(ctx context.Context, release onlineRelease, path string) error {
	r, err := c.get(ctx, release.asset.URL)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.ContentLength >= 0 && r.ContentLength != release.artifact.Bytes {
		return fail("ARTIFACT", "发行下载 Content-Length 与校验清单不一致")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r.Body, release.artifact.Bytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return domain.Wrap("CANCELLED", ctx.Err())
		}
		return domain.Wrap("NETWORK", err)
	}
	if n != release.artifact.Bytes {
		return fail("ARTIFACT", "发行下载实际长度与校验清单不一致")
	}
	if hex.EncodeToString(h.Sum(nil)) != release.artifact.SHA256 {
		return domain.Explain(fail("DIGEST", "发行下载 SHA-256 不匹配"), "RELEASE_CHECKSUM_MISMATCH", "下载文件的 SHA-256 与固定发行校验清单不同，不能安装。", map[string]any{"path": path, "phase": "checksum", "expectedDigest": release.artifact.SHA256, "observedDigest": hex.EncodeToString(h.Sum(nil))})
	}
	return f.Sync()
}

type installedProgram struct {
	receipt  receipt
	manifest Manifest
}
