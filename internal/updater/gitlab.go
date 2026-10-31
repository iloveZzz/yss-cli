package updater

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

const gitlabOrigin = "https://gitlab.yss.test:9443"
const gitlabProject = "yss/yss-cli"
const releaseBase = gitlabOrigin + "/" + gitlabProject + "/-/releases/"

// Only exact approved URLs receive credentials, including after a redirect.
type gitlabSource struct {
	client        *http.Client
	header, token string
	projectID     int64
	allowed       map[string]bool
}

func (c UpgradeClient) gitlab() (*gitlabSource, error) {
	caPath := os.Getenv("YSS_GITLAB_CA_FILE")
	if caPath == "" {
		return nil, fail("NETWORK", "固定 GitLab 来源需要显式 YSS_GITLAB_CA_FILE")
	}
	ca, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fail("NETWORK", "无法读取 GitLab CA 文件")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fail("NETWORK", "GitLab CA 文件没有有效证书")
	}
	source := &gitlabSource{allowed: map[string]bool{gitlabOrigin + "/api/v4/projects/" + url.PathEscape(gitlabProject): true}}
	source.header, source.token = "PRIVATE-TOKEN", os.Getenv("YSS_GITLAB_TOKEN")
	if os.Getenv("YSS_GITLAB_CI_AUTH") != "" {
		if os.Getenv("YSS_GITLAB_CI_AUTH") != "1" || os.Getenv("CI_SERVER_URL") != gitlabOrigin {
			return nil, fail("NETWORK", "CI Job Token 仅允许显式授权的固定 GitLab")
		}
		source.header, source.token = "JOB-TOKEN", os.Getenv("CI_JOB_TOKEN")
	}
	if source.token == "" || strings.ContainsAny(source.token, "\r\n") {
		return nil, fail("NETWORK", "固定私有 GitLab 来源缺少有效鉴权环境变量")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	client := &http.Client{Timeout: 5 * time.Minute, Transport: transport}
	if c.HTTPClient != nil {
		*client = *c.HTTPClient
		// A custom transport is an internal offline seam, never a CLI source override.
		if client.Transport == nil {
			client.Transport = transport
		}
		if supplied, ok := client.Transport.(*http.Transport); ok {
			clone := supplied.Clone()
			clone.TLSClientConfig = transport.TLSClientConfig.Clone()
			client.Transport = clone
		}
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !source.approved(req.URL) {
			return fail("NETWORK", "拒绝发行下载重定向到未批准来源")
		}
		req.Header.Del("Authorization")
		req.Header.Del("PRIVATE-TOKEN")
		req.Header.Del("JOB-TOKEN")
		req.Header.Set(source.header, source.token)
		return nil
	}
	source.client = client
	return source, nil
}

func (s *gitlabSource) approved(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.Host == "gitlab.yss.test:9443" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && s.allowed[u.String()]
}

func (s *gitlabSource) request(ctx context.Context, method, address string) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil || !s.approved(u) {
		return nil, fail("NETWORK", "拒绝向未批准的 GitLab URL 发送请求")
	}
	req, err := http.NewRequestWithContext(ctx, method, address, nil)
	if err != nil {
		return nil, fail("NETWORK", "无法构建固定 GitLab 请求")
	}
	req.Header.Set("User-Agent", "yss/"+domain.Version)
	req.Header.Set("Accept", "application/json")
	req.Header.Set(s.header, s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, domain.Wrap("CANCELLED", ctx.Err())
		}
		// Transport errors and remote Location values can contain secrets; retain no arbitrary text.
		return nil, domain.Explain(fail("NETWORK", "连接固定 GitLab 来源失败；检查 CA、网络与鉴权"), "RELEASE_REQUEST_FAILED", "固定 GitLab 请求失败。", map[string]any{"phase": "request"})
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, domain.Explain(fail("NOT_READY", "GitLab 来源或正式 Release 尚未就绪（HTTP 404）；不回退其他来源"), "RELEASE_NOT_READY", "固定来源返回未找到；也可能没有读取权限。", map[string]any{"httpStatus": 404})
		}
		return nil, domain.Explain(fail("NETWORK", fmt.Sprintf("GitLab 返回 HTTP %d", resp.StatusCode)), "RELEASE_HTTP_STATUS", "固定来源返回非成功 HTTP 状态。", map[string]any{"httpStatus": resp.StatusCode})
	}
	return resp, nil
}
func (s *gitlabSource) get(ctx context.Context, address string) (*http.Response, error) {
	return s.request(ctx, http.MethodGet, address)
}

func (s *gitlabSource) identify(ctx context.Context) error {
	var project struct {
		ID   int64  `json:"id"`
		Path string `json:"path_with_namespace"`
		URL  string `json:"web_url"`
	}
	if err := s.metadata(ctx, gitlabOrigin+"/api/v4/projects/"+url.PathEscape(gitlabProject), &project); err != nil {
		return err
	}
	if project.ID <= 0 || project.Path != gitlabProject || project.URL != gitlabOrigin+"/"+gitlabProject {
		return fail("ARTIFACT", "GitLab 项目 ID、完整路径或 Web URL 不匹配")
	}
	s.projectID = project.ID
	return nil
}
func (s *gitlabSource) api() string {
	return gitlabOrigin + "/api/v4/projects/" + strconv.FormatInt(s.projectID, 10)
}

var uploadPath = regexp.MustCompile(`^/yss/yss-cli/uploads/[a-f0-9]{32}/[A-Za-z0-9_.-]+$`)

func (s *gitlabSource) asset(r gitlabRelease, name string) (gitlabAsset, error) {
	var found gitlabAsset
	for _, asset := range r.Assets.Links {
		if asset.Name != name {
			continue
		}
		if found.Name != "" {
			return found, fail("ARTIFACT", "发行资产名称重复: "+name)
		}
		direct := releaseBase + r.Tag + "/downloads/" + url.PathEscape(name)
		u, err := url.Parse(asset.URL)
		if err != nil || u.Scheme != "https" || u.Host != "gitlab.yss.test:9443" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
			return found, fail("ARTIFACT", "发行资产 URL 不属于固定 GitLab 项目")
		}
		packageURL := s.api() + "/packages/generic/yss/" + r.Tag + "/" + url.PathEscape(name)
		if asset.URL != direct && asset.URL != packageURL && !(uploadPath.MatchString(u.Path) && strings.HasSuffix(u.Path, "/"+name)) {
			return found, fail("ARTIFACT", "发行资产 URL 不属于固定项目、版本和文件")
		}
		if asset.DirectURL != "" && asset.DirectURL != direct {
			return found, fail("ARTIFACT", "发行资产直接下载 URL 不匹配")
		}
		s.allowed[asset.URL] = true
		if asset.DirectURL != "" {
			s.allowed[direct] = true
			asset.URL = direct
		}
		found = asset
	}
	if found.Name == "" {
		return found, fail("ARTIFACT", "发行缺少资产: "+name)
	}
	return found, nil
}
