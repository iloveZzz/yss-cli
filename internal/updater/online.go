package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

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

type gitlabAsset struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	DirectURL string `json:"direct_asset_url"`
	Size      int64  `json:"-"`
}
type gitlabRelease struct {
	Tag        string `json:"tag_name"`
	Upcoming   bool   `json:"upcoming_release"`
	ReleasedAt string `json:"released_at"`
	Links      struct {
		Self string `json:"self"`
	} `json:"_links"`
	Assets struct {
		Links []gitlabAsset `json:"links"`
	} `json:"assets"`
	URL string `json:"-"`
}

type onlineArtifact struct {
	Archive string
	SHA256  string
	Bytes   int64
}
type onlineRelease struct {
	gitlabRelease
	version  string
	artifact onlineArtifact
	asset    gitlabAsset
	source   *gitlabSource
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

func (c *gitlabSource) metadata(ctx context.Context, address string, v any) error {
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
		return fail("NETWORK", "读取固定 GitLab 发行数据失败")
	}
	if len(b) > metadataLimit {
		return fail("ARTIFACT", "发行 metadata 超过 256 KiB")
	}
	if !json.Valid(b) {
		return fail("ARTIFACT", "发行 metadata 必须为单个 JSON 文档")
	}
	if _, err = schema.Parse(b); err != nil {
		return fail("ARTIFACT", "发行 metadata JSON 结构不合法")
	}
	if err = json.Unmarshal(b, v); err != nil {
		return fail("ARTIFACT", "发行 metadata 字段类型不合法")
	}
	return nil
}

func (c UpgradeClient) resolve(ctx context.Context, target string) (onlineRelease, error) {
	var selected onlineRelease
	source, err := c.gitlab()
	if err != nil {
		return selected, err
	}
	if err = source.identify(ctx); err != nil {
		return selected, err
	}
	endpoint := source.api() + "/releases/permalink/latest"
	if target != "" {
		v, err := stableVersion(target)
		if err != nil {
			return selected, err
		}
		target = v
		endpoint = source.api() + "/releases/v" + v
	}
	source.allowed[endpoint] = true
	if err = source.metadata(ctx, endpoint, &selected.gitlabRelease); err != nil {
		return selected, err
	}
	r := selected.gitlabRelease
	version, err := stableVersion(r.Tag)
	if err != nil {
		return selected, fail("ARTIFACT", "发行版本不是有效稳定 SemVer")
	}
	released, err := time.Parse(time.RFC3339, r.ReleasedAt)
	if err != nil || released.After(time.Now()) || r.Upcoming || r.Tag != "v"+version || r.Links.Self != releaseBase+r.Tag || target != "" && version != target {
		return selected, fail("ARTIFACT", "发行版本、来源或稳定版状态不一致")
	}
	selected.URL = r.Links.Self
	selected.source = source
	checksum, err := source.asset(r, "SHA256SUMS")
	if err != nil {
		return selected, err
	}
	response, err := source.get(ctx, checksum.URL)
	if err != nil {
		return selected, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, metadataLimit+1))
	if err != nil {
		if ctx.Err() != nil {
			return selected, domain.Wrap("CANCELLED", ctx.Err())
		}
		return selected, fail("NETWORK", "读取固定 GitLab checksum 失败")
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
	selected.asset, err = source.asset(r, name)
	if err != nil {
		return selected, err
	}
	responseSize, err := source.request(ctx, http.MethodHead, selected.asset.URL)
	if err != nil {
		return selected, err
	}
	selected.asset.Size = responseSize.ContentLength
	responseSize.Body.Close()
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
	r, err := release.source.get(ctx, release.asset.URL)
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
		return fail("NETWORK", "读取固定 GitLab 发行数据失败")
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
