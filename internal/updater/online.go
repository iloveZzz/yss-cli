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
	"github.com/iloveZzz/yss-cli/internal/safefs"
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
	Platform              string `json:"platform"`
	Archive               string `json:"archive"`
	SHA256                string `json:"sha256"`
	Bytes                 int64  `json:"bytes"`
	Compiled              bool   `json:"compiled"`
	NativeRuntimeVerified bool   `json:"nativeRuntimeVerified"`
	BinarySHA256          string `json:"binarySha256"`
	NativeReceiptSHA256   string `json:"nativeReceiptSha256"`
}

type onlineProof struct {
	SchemaVersion       int              `json:"schemaVersion"`
	Version             string           `json:"version"`
	CLICommit           string           `json:"cliCommit"`
	SourceState         string           `json:"sourceState"`
	StableReady         bool             `json:"stableReady"`
	Pending             []string         `json:"pending"`
	SourceLockSHA256    string           `json:"sourceLockSha256"`
	ReleaseGateSHA256   string           `json:"releaseGateSha256"`
	InputManifestSHA256 string           `json:"inputManifestSha256"`
	QualificationScope  string           `json:"qualificationScope"`
	RequiredPlatforms   []string         `json:"requiredPlatforms"`
	SupportedPlatforms  []string         `json:"supportedPlatforms"`
	Bundles             json.RawMessage  `json:"bundles"`
	Artifacts           []onlineArtifact `json:"artifacts"`
}

type onlineRelease struct {
	githubRelease
	proof    onlineProof
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
		return nil, domain.Wrap("NETWORK", err)
	}
	req.Header.Set("User-Agent", "yss/"+domain.Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, domain.Wrap("CANCELLED", ctx.Err())
		}
		return nil, domain.Wrap("NETWORK", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fail("NETWORK", fmt.Sprintf("GitHub 返回 HTTP %d；检查网络、版本是否发布或稍后重试", resp.StatusCode))
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
	checksum, err := assetFor(r, "checksums.json")
	if err != nil {
		return selected, err
	}
	if err = c.metadata(ctx, checksum.URL, &selected.proof, true); err != nil {
		return selected, err
	}
	p := selected.proof
	if p.SchemaVersion != 2 || p.Version != version || p.SourceState != "committed" || !p.StableReady || p.Pending == nil || len(p.Pending) != 0 || !commitPattern.MatchString(p.CLICommit) || !digestPattern.MatchString(p.SourceLockSHA256) || !digestPattern.MatchString(p.ReleaseGateSHA256) {
		return selected, fail("ARTIFACT", "发行校验清单未满足固定稳定版资格")
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	qualified := false
	seen := map[string]bool{}
	for _, platformRef := range p.RequiredPlatforms {
		if seen[platformRef] {
			return selected, fail("ARTIFACT", "发行验收平台重复")
		}
		seen[platformRef] = true
		qualified = qualified || platformRef == platform
	}
	if !qualified {
		return selected, fail("ARTIFACT", "当前平台未列入稳定发行验收范围: "+platform)
	}
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	name := "yss_" + version + "_" + runtime.GOOS + "_" + runtime.GOARCH + ext
	count := 0
	for _, a := range p.Artifacts {
		if a.Platform == platform {
			selected.artifact = a
			count++
		}
	}
	a := selected.artifact
	if count != 1 || a.Archive != name || a.Bytes <= 0 || a.Bytes > maxArchive || a.Compiled || !a.NativeRuntimeVerified || !digestPattern.MatchString(a.SHA256) || !digestPattern.MatchString(a.BinarySHA256) || !digestPattern.MatchString(a.NativeReceiptSHA256) {
		return selected, fail("ARTIFACT", "当前平台缺少唯一、已验收且摘要完整的发行包: "+platform)
	}
	selected.asset, err = assetFor(r, name)
	if err != nil {
		return selected, err
	}
	if selected.asset.Size != 0 && selected.asset.Size != a.Bytes {
		return selected, fail("ARTIFACT", "发行资产长度与校验清单不一致")
	}
	return selected, nil
}

func (c UpgradeClient) Run(ctx context.Context, request UpgradeRequest) (UpgradeResult, error) {
	var result UpgradeResult
	if err := cancellation(ctx); err != nil {
		return result, err
	}
	if request.To != "" {
		if _, err := stableVersion(request.To); err != nil {
			return result, err
		}
	}
	result.Platform = runtime.GOOS + "/" + runtime.GOARCH
	result.CurrentVersion = domain.Version
	automatic := request.ToolRoot == "" && !request.Check
	if automatic {
		executable := c.Executable
		if executable == nil {
			executable = os.Executable
		}
		path, err := executable()
		if err != nil {
			return result, domain.Wrap("INSTALLATION", err)
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return result, domain.Wrap("INSTALLATION", err)
		}
		if filepath.Base(path) != fileName() {
			return result, fail("INSTALLATION", "未识别受管 yss 入口；运行 yss help tutorial 查看安装方法，或显式使用 --tool-root <新目录>")
		}
		request.ToolRoot = filepath.Dir(path)
	}
	var installed *installedProgram
	if request.ToolRoot != "" {
		var err error
		result.ToolRoot, err = toolRootPath(request.ToolRoot)
		if err != nil {
			return result, err
		}
		installed, err = inspectProgram(result.ToolRoot, !request.Check)
		if err != nil {
			return result, err
		}
		if installed == nil {
			if automatic {
				return result, fail("INSTALLATION", "实际运行目录缺少安装收据；运行 yss help tutorial 查看安装方法，或显式使用 --tool-root <新目录>")
			}
			result.CurrentVersion = ""
		} else {
			result.CurrentVersion = installed.receipt.CLIVersion
			if automatic && (result.CurrentVersion != domain.Version || domain.BuildSourceState == "committed" && installed.manifest.CLICommit != domain.BuildCommit) {
				return result, fail("INSTALLATION", "安装身份与实际运行 CLI 不一致；请先检查 yss update status --tool-root <目录>")
			}
		}
	}
	release, err := c.resolve(ctx, request.To)
	if err != nil {
		return result, err
	}
	result.TargetVersion = release.proof.Version
	result.ReleaseURL = release.URL
	result.ArchiveSHA256 = release.artifact.SHA256
	order := 1
	if result.CurrentVersion != "" {
		order, err = compareVersions(result.TargetVersion, result.CurrentVersion)
		if err != nil {
			return result, err
		}
	}
	result.UpdateAvailable = order > 0
	if request.Check {
		result.Status = "checked"
		return result, nil
	}
	if order < 0 {
		return result, fail("VERSION", "目标版本低于当前安装，拒绝降级；回退请使用 yss update rollback --tool-root <目录>")
	}
	if installed != nil && order == 0 && installed.receipt.StableReady && installed.receipt.ArchiveDigest == release.artifact.SHA256 && installed.manifest.CLICommit == release.proof.CLICommit && installed.manifest.BinarySHA256 == release.artifact.BinarySHA256 {
		result.Status = "unchanged"
		return result, nil
	}
	if err = cancellation(ctx); err != nil {
		return result, err
	}
	tmp, err := os.MkdirTemp("", "yss-upgrade-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmp)
	archivePath := filepath.Join(tmp, release.artifact.Archive)
	if err = c.download(ctx, release, archivePath); err != nil {
		return result, err
	}
	plan, err := Build(result.ToolRoot, archivePath, release.artifact.SHA256)
	if err != nil {
		return result, err
	}
	m, p, a := plan.Manifest, release.proof, release.artifact
	if !m.StableReady || m.SourceState != "committed" || m.RuntimeVerification != "passed" || m.CLIVersion != p.Version || m.CLICommit != p.CLICommit || m.Platform != result.Platform || m.BinarySHA256 != a.BinarySHA256 || m.SourceLockSHA256 != p.SourceLockSHA256 || m.NativeReceiptSHA256 != a.NativeReceiptSHA256 || m.ReleaseGateSHA256 != p.ReleaseGateSHA256 {
		return result, fail("ARTIFACT", "包内来源与在线发行校验清单不一致")
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
		return fail("DIGEST", "发行下载 SHA-256 不匹配")
	}
	return f.Sync()
}

type installedProgram struct {
	receipt  receipt
	manifest Manifest
}

// Inspect before networking so interrupted transactions and user modifications
// cannot be mistaken for an up-to-date installation. Apply checks again under
// the existing transaction guards after the download.
func inspectProgram(root string, requireSettled bool) (*installedProgram, error) {
	txn, err := transaction.Status(root)
	if err != nil {
		return nil, err
	}
	if requireSettled && (len(txn.Pending) > 0 || len(txn.Preparations) > 0) {
		return nil, fail("STATE", fmt.Sprintf("工具目录有未完成事务；先运行 yss update recover --tool-root %q", root))
	}
	receiptPath, err := safefs.Path(root, receiptRef)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(receiptPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var installed installedProgram
	if err = parseJSON(b, &installed.receipt); err != nil {
		return nil, err
	}
	r := installed.receipt
	if r.SchemaVersion != 1 || r.ProtocolVersion != domain.ProtocolVersion || r.Platform != runtime.GOOS+"/"+runtime.GOARCH || !digestPattern.MatchString(r.ArchiveDigest) || r.SourceState != "committed" && r.SourceState != "working-tree" || r.StableReady && r.SourceState != "committed" {
		return nil, fail("ARTIFACT", "未知程序安装合同")
	}
	if _, err = versionParts(r.CLIVersion); err != nil {
		return nil, err
	}
	path, err := safefs.Path(root, "release-manifest.json")
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, fail("ARTIFACT", "安装 manifest 非普通文件或超过 8 MiB")
	}
	b, err = os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err = parseJSON(b, &installed.manifest); err != nil {
		return nil, err
	}
	m := installed.manifest
	if m.SchemaVersion != 1 || m.CLIVersion != r.CLIVersion || m.Platform != r.Platform || m.ProtocolVersion != r.ProtocolVersion || m.SourceState != r.SourceState || m.StableReady != r.StableReady || m.CGO || m.StableReady && m.RuntimeVerification != "passed" {
		return nil, fail("ARTIFACT", "安装收据与 manifest 身份不一致")
	}
	for _, ref := range []string{fileName(), "README.md", "docs/source-lock.json", "docs/compatibility.md"} {
		if _, ok := m.Files[ref]; !ok {
			return nil, fail("ARTIFACT", "安装 manifest 缺少核心文件: "+ref)
		}
	}
	for ref, want := range m.Files {
		if !expectedFiles()[ref] || ref == "release-manifest.json" || want.Type != "file" || !digestPattern.MatchString(want.Digest) || want.Mode != 0644 && want.Mode != 0755 || ref == fileName() && want.Mode != 0755 {
			return nil, fail("ARTIFACT", "安装 manifest 描述不合法: "+ref)
		}
		want.Mode = domain.FileMode(want.Mode)
		got, err := safefs.Describe(root, ref)
		if err != nil {
			return nil, err
		}
		if got != want {
			return nil, fail("CONFLICT", "现有程序文件发生用户修改: "+ref)
		}
	}
	lock, err := os.ReadFile(filepath.Join(root, "docs/source-lock.json"))
	if err != nil {
		return nil, err
	}
	if err = validateProvenance(m, lock); err != nil {
		return nil, err
	}
	if err = validateStableProof(m, lock, b); err != nil {
		return nil, err
	}
	return &installed, nil
}
