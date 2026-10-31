package updater

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

const githubAPI = "https://api.github.com/repos/iloveZzz/yss-cli/releases/"
const githubReleaseBase = "https://github.com/iloveZzz/yss-cli/releases/"

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
type githubSource struct {
	client  *http.Client
	allowed map[string]bool
}

func (c UpgradeClient) github() *githubSource {
	source := &githubSource{allowed: map[string]bool{}}
	client := &http.Client{Timeout: 5 * time.Minute}
	if c.HTTPClient != nil {
		*client = *c.HTTPClient
	}
	// Public releases need no credential; never forward another platform's auth.
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || len(via) == 0 {
			return fail("NETWORK", "发行下载重定向次数超限")
		}
		u := req.URL
		first := via[0].URL.String()
		if !source.allowed[first] || !strings.HasPrefix(first, githubReleaseBase+"download/") || !githubDownloadURL(u) {
			return fail("NETWORK", "拒绝发行下载重定向到未批准来源")
		}
		req.Header.Del("Authorization")
		req.Header.Del("PRIVATE-TOKEN")
		req.Header.Del("JOB-TOKEN")
		req.Header.Del("Cookie")
		return nil
	}
	source.client = client
	return source
}
func githubDownloadURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return false
	}
	switch u.Host {
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	}
	return false
}
func (s *githubSource) request(ctx context.Context, method, address string) (*http.Response, error) {
	if !s.allowed[address] {
		return nil, fail("NETWORK", "拒绝请求未批准的 GitHub URL")
	}
	req, err := http.NewRequestWithContext(ctx, method, address, nil)
	if err != nil {
		return nil, fail("NETWORK", "无法构建固定 GitHub 请求")
	}
	req.Header.Set("User-Agent", "yss/"+domain.Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, domain.Wrap("CANCELLED", ctx.Err())
		}
		return nil, domain.Explain(fail("NETWORK", "连接固定 GitHub 来源失败"), "RELEASE_REQUEST_FAILED", "固定 GitHub 请求失败。", map[string]any{"phase": "request"})
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, domain.Explain(fail("NOT_READY", "GitHub 正式 Release 尚未就绪（HTTP 404）；不回退其他来源"), "RELEASE_NOT_READY", "固定来源没有对应发行。", map[string]any{"httpStatus": 404})
		}
		return nil, domain.Explain(fail("NETWORK", fmt.Sprintf("GitHub 返回 HTTP %d", response.StatusCode)), "RELEASE_HTTP_STATUS", "固定来源返回非成功 HTTP 状态。", map[string]any{"httpStatus": response.StatusCode})
	}
	return response, nil
}
func (s *githubSource) asset(r githubRelease, name string) (releaseAsset, error) {
	var found releaseAsset
	for _, a := range r.Assets {
		if a.Name != name {
			continue
		}
		if found.Name != "" {
			return found, fail("ARTIFACT", "发行资产名称重复: "+name)
		}
		if a.URL != githubReleaseBase+"download/"+r.Tag+"/"+url.PathEscape(name) || a.Size <= 0 || a.Size > maxArchive || (name == "SHA256SUMS" && a.Size > metadataLimit) {
			return found, fail("ARTIFACT", "发行资产 URL 或大小不属于固定仓库、版本和文件: "+name)
		}
		found = releaseAsset{Name: a.Name, URL: a.URL, Size: a.Size}
		s.allowed[a.URL] = true
	}
	if found.Name == "" {
		return found, fail("ARTIFACT", "发行缺少资产: "+name)
	}
	return found, nil
}
func (c UpgradeClient) resolveGithub(ctx context.Context, target string) (onlineRelease, error) {
	var selected onlineRelease
	source := c.github()
	endpoint := githubAPI + "latest"
	if target != "" {
		version, err := stableVersion(target)
		if err != nil {
			return selected, err
		}
		target = version
		endpoint = githubAPI + "tags/v" + version
	}
	source.allowed[endpoint] = true
	var r githubRelease
	if err := releaseMetadata(ctx, source, endpoint, &r); err != nil {
		return selected, err
	}
	version, err := stableVersion(r.Tag)
	if err != nil || r.Tag != "v"+version || r.Draft || r.Prerelease || r.URL != githubReleaseBase+"tag/"+r.Tag || target != "" && version != target {
		return selected, fail("ARTIFACT", "发行版本、来源或稳定版状态不一致")
	}
	checksum, err := source.asset(r, "SHA256SUMS")
	if err != nil {
		return selected, err
	}
	selected.asset, err = source.asset(r, archiveName(version))
	if err != nil {
		return selected, err
	}
	selected.URL, selected.version, selected.source = r.URL, version, source
	return finishRelease(ctx, selected, checksum.URL)
}
