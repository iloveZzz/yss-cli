package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// Issue 是同步所需的最小远端事实；labels 已排序，便于比较与绑定。
type Issue struct {
	Repository string   `json:"repository"`
	Number     int      `json:"number"`
	State      string   `json:"state"`
	Labels     []string `json:"labels"`
	UpdatedAt  string   `json:"updated_at"`
	URL        string   `json:"url"`
}

// Client 读取 Issue 并逐项添加/移除五态标签。不创建、不关闭、不评论。
type Client struct {
	Base, Token string
	HTTP        *http.Client
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)

func NewClient(env func(string) string) (*Client, error) {
	token := env("GH_TOKEN")
	if token == "" {
		token = env("GITHUB_TOKEN")
	}
	if token == "" {
		return nil, domain.Fail("TRACKER_AUTH", "需要 GH_TOKEN（或 GITHUB_TOKEN）才能读取或写入 GitHub Issue")
	}
	base := strings.TrimRight(env("GITHUB_API_URL"), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"))) {
		return nil, domain.Fail("TRACKER_REMOTE", "GITHUB_API_URL 必须是 https 地址")
	}
	u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	base = u.String()
	return &Client{Base: base, Token: token, HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var cause error
		if ctx.Err() != nil {
			cause = domain.Wrap("CANCELLED", ctx.Err())
		} else {
			cause = domain.WithMessage(domain.Wrap("NETWORK", err), "GitHub 请求失败: "+method+" "+path)
		}
		if method != http.MethodGet {
			cause = &uncertainWrite{cause}
		}
		return nil, cause
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		cause := domain.WithMessage(domain.Wrap("NETWORK", err), "GitHub 响应读取失败")
		if ctx.Err() != nil {
			cause = domain.Wrap("CANCELLED", ctx.Err())
		}
		if method != http.MethodGet {
			cause = &uncertainWrite{cause}
		}
		return nil, cause
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, domain.Fail("TRACKER_AUTH", fmt.Sprintf("GitHub 拒绝访问（HTTP %d）：检查令牌权限", resp.StatusCode))
	case resp.StatusCode == http.StatusNotFound:
		return nil, domain.Fail("TRACKER_REMOTE", "GitHub Issue 不存在或无权读取: "+path)
	case resp.StatusCode >= 300:
		cause := error(domain.Fail("TRACKER_REMOTE", fmt.Sprintf("GitHub 返回 HTTP %d: %s %s", resp.StatusCode, method, path)))
		if method != http.MethodGet && resp.StatusCode >= 500 {
			cause = &uncertainWrite{cause}
		}
		return nil, cause
	}
	return data, nil
}

func (c *Client) GetIssue(ctx context.Context, repository string, number int) (Issue, error) {
	data, err := c.do(ctx, http.MethodGet, issuePath(repository, number), nil)
	if err != nil {
		return Issue{}, err
	}
	return parseIssue(repository, data)
}

// AddLabel and RemoveLabel mutate only one known workflow state.
func (c *Client) AddLabel(ctx context.Context, repository string, number int, label string) error {
	if !contains(States, label) {
		return domain.Fail("PLAN", "只允许写入五态标签")
	}
	_, err := c.do(ctx, http.MethodPost, issuePath(repository, number)+"/labels", map[string]any{"labels": []string{label}})
	return err
}
func (c *Client) RemoveLabel(ctx context.Context, repository string, number int, label string) error {
	if !contains(States, label) {
		return domain.Fail("PLAN", "只允许移除五态标签")
	}
	_, err := c.do(ctx, http.MethodDelete, issuePath(repository, number)+"/labels/"+url.PathEscape(label), nil)
	return err
}

func issuePath(repository string, number int) string {
	return "/repos/" + repository + "/issues/" + strconv.Itoa(number)
}

func parseIssue(repository string, data []byte) (Issue, error) {
	var raw struct {
		Number    int    `json:"number"`
		State     string `json:"state"`
		UpdatedAt string `json:"updated_at"`
		HTMLURL   string `json:"html_url"`
		Labels    []struct {
			Name string `json:"name"`
		} `json:"labels"`
		PullRequest any `json:"pull_request"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Number == 0 {
		return Issue{}, domain.Fail("TRACKER_REMOTE", "GitHub Issue 响应无效")
	}
	if raw.PullRequest != nil {
		return Issue{}, domain.Fail("TRACKER_REMOTE", "链接目标是 Pull Request，不是 Issue")
	}
	labels := []string{}
	for _, l := range raw.Labels {
		labels = append(labels, l.Name)
	}
	sort.Strings(labels)
	return Issue{Repository: repository, Number: raw.Number, State: raw.State, Labels: labels, UpdatedAt: raw.UpdatedAt, URL: raw.HTMLURL}, nil
}
