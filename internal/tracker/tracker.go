// Package tracker 把本地主 tracker 的 Ticket 五态镜像到已链接的 GitHub Issue 标签。
// 本地 Markdown 始终是权威；镜像只改链接 Issue 的五态标签，不创建、不关闭、不评论，
// 远端写入必须先保存计划，再由用户以 --confirm-remote github 确认。
package tracker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
)

// States 是 triage-labels.md 定义的五态，同名作为 Tracker 标签。
var States = []string{"needs-triage", "needs-info", "ready-for-agent", "ready-for-human", "wontfix"}

const headerLines = 30

var (
	statusLine  = regexp.MustCompile(`^Status:\s*(\S+)\s*$`)
	linkLine    = regexp.MustCompile(`^Tracker-Link:\s*github:(\S+)#([1-9][0-9]*)\s*$`)
	syncedLine  = regexp.MustCompile(`^Tracker-Synced:`)
	issueArg    = regexp.MustCompile(`^([^#\s]+)#([1-9][0-9]{0,9})$`)
	featureName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

type Ticket struct {
	Ref        string `json:"ref"`
	Title      string `json:"title,omitempty"`
	Status     string `json:"status"`
	Repository string `json:"repository,omitempty"`
	Number     int    `json:"number,omitempty"`
	Synced     string `json:"synced,omitempty"`
	Digest     string `json:"digest"`
	raw        []byte
	before     domain.Descriptor
}

// Env 便于测试注入环境变量与时钟。
type Env struct {
	Getenv func(string) string
	Now    func() time.Time
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return os.Getenv(k)
	}
	return e.Getenv(k)
}

func (e Env) now() time.Time {
	if e.Now == nil {
		return time.Now().UTC()
	}
	return e.Now().UTC()
}

func Run(ctx context.Context, action, root string, args map[string]string, env Env) (any, error) {
	switch action {
	case "status":
		return status(ctx, root, args, env)
	case "link":
		return link(ctx, root, args)
	case "sync":
		return syncRun(ctx, root, args, env)
	}
	return nil, domain.Fail("ARGUMENT", "不支持的 tracker 动作: "+action)
}

// mirrorConfig 返回是否允许远端镜像；只认 local-markdown + remote_mirror: true 或主 tracker 为 github。
func mirrorConfig(root string) (bool, string, error) {
	t, err := kernel.Tracker(kernel.NewView(root))
	if err != nil {
		return false, "", err
	}
	platform := kernel.Text(t["platform"])
	enabled := platform == "github" || platform == "local-markdown" && t["remote_mirror"] == true
	return enabled, platform, nil
}

func parseTicket(ref string, raw []byte) Ticket {
	sum := sha256.Sum256(raw)
	t := Ticket{Ref: ref, Digest: "sha256:" + hex.EncodeToString(sum[:]), raw: raw}
	for i, line := range strings.Split(string(raw), "\n") {
		if i >= headerLines {
			break
		}
		line = strings.TrimRight(line, "\r")
		switch {
		case t.Title == "" && strings.HasPrefix(line, "# "):
			t.Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		case t.Status == "" && statusLine.MatchString(line):
			t.Status = statusLine.FindStringSubmatch(line)[1]
		case t.Repository == "" && linkLine.MatchString(line):
			m := linkLine.FindStringSubmatch(line)
			t.Repository = m[1]
			t.Number, _ = strconv.Atoi(m[2])
		case t.Synced == "" && syncedLine.MatchString(line):
			t.Synced = strings.TrimSpace(strings.TrimPrefix(line, "Tracker-Synced:"))
		}
	}
	return t
}

func readTicket(root, ref string) (Ticket, error) {
	layout, err := worklayout.Read(root)
	if err != nil {
		return Ticket{}, err
	}
	if !layout.IsTicket(ref) && !(strings.HasPrefix(ref, layout.Root+"/") && strings.HasSuffix(ref, "/parent-ticket.md") && strings.Count(strings.TrimPrefix(ref, layout.Root+"/"), "/") == 1) {
		return Ticket{}, domain.Fail("TRACKER", "只接受 tracker 根下的 parent-ticket.md 或 issues/*.md: "+ref)
	}
	path, err := safefs.Path(root, ref)
	if err != nil {
		return Ticket{}, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Ticket{}, domain.Fail("TRACKER", "Ticket 必须是普通文件: "+ref)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Ticket{}, domain.Fail("TRACKER", "无法读取 Ticket: "+ref)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || info.Mode() != after.Mode() {
		return Ticket{}, domain.Fail("INPUT_DRIFT", "读取期间 Ticket 身份或权限已变化: "+ref)
	}
	t := parseTicket(ref, raw)
	t.before = domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: domain.FileMode(uint32(info.Mode().Perm()))}
	return t, nil
}

// tickets 列出 tracker 根下每个功能包的父 Ticket 与 issues/*.md，按路径排序。
func tickets(root string) ([]Ticket, error) {
	layout, err := worklayout.Read(root)
	if err != nil {
		return nil, err
	}
	base, err := safefs.Path(root, layout.Root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return []Ticket{}, nil
	}
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for _, e := range entries {
		if !e.IsDir() || !featureName.MatchString(e.Name()) {
			continue
		}
		feature := layout.Root + "/" + e.Name()
		if _, err := os.Stat(filepath.Join(base, e.Name(), "parent-ticket.md")); err == nil {
			refs = append(refs, feature+"/parent-ticket.md")
		}
		issues, _ := os.ReadDir(filepath.Join(base, e.Name(), "issues"))
		for _, i := range issues {
			if !i.IsDir() && strings.HasSuffix(i.Name(), ".md") {
				refs = append(refs, feature+"/issues/"+i.Name())
			}
		}
	}
	sort.Strings(refs)
	out := []Ticket{}
	for _, ref := range refs {
		t, err := readTicket(root, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// desiredLabels 保留非五态标签，只把五态标签替换为本地 Status。
func desiredLabels(current []string, status string) []string {
	out := []string{status}
	for _, l := range current {
		if !contains(States, l) {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

func diff(current, desired []string) (add, remove []string) {
	add, remove = []string{}, []string{}
	for _, l := range desired {
		if !contains(current, l) {
			add = append(add, l)
		}
	}
	for _, l := range current {
		if !contains(desired, l) {
			remove = append(remove, l)
		}
	}
	return add, remove
}

func status(ctx context.Context, root string, args map[string]string, env Env) (any, error) {
	enabled, platform, err := mirrorConfig(root)
	if err != nil {
		return nil, err
	}
	list, err := tickets(root)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	var client *Client
	if args["fetch"] == "true" {
		if client, err = NewClient(env.getenv); err != nil {
			return nil, err
		}
	}
	for _, t := range list {
		row := map[string]any{"ticket": t}
		if client != nil && t.Repository != "" {
			issue, err := client.GetIssue(ctx, t.Repository, t.Number)
			if err != nil {
				row["remote_error"] = err.Error()
			} else {
				add, remove := diff(issue.Labels, desiredLabels(issue.Labels, t.Status))
				row["remote"] = issue
				row["in_sync"] = contains(States, t.Status) && len(add) == 0 && len(remove) == 0
			}
		}
		rows = append(rows, row)
	}
	return map[string]any{"kind": "tracker-status", "platform": platform, "remote_mirror": enabled, "authority": "local-markdown", "remote_fetched": client != nil, "tickets": rows, "read_only": true}, nil
}

// link 把 Tracker-Link 行写在 Status 行之后；默认只预览，--apply 以当前字节为守卫事务写入。
func link(ctx context.Context, root string, args map[string]string) (any, error) {
	ref, issue := args["ticket"], args["issue"]
	m := issueArg.FindStringSubmatch(issue)
	if ref == "" || m == nil || !repoPattern.MatchString(m[1]) {
		return nil, domain.Fail("ARGUMENT", "需要 --ticket <Ticket 文件> 与 --issue <owner/repo#编号>")
	}
	t, err := readTicket(root, ref)
	if err != nil {
		return nil, err
	}
	number, _ := strconv.Atoi(m[2])
	if t.Repository != "" {
		if t.Repository == m[1] && t.Number == number {
			return map[string]any{"kind": "tracker-link", "ticket": ref, "status": "unchanged"}, nil
		}
		return nil, domain.Fail("TRACKER_CONFLICT", "Ticket 已链接到 "+t.Repository+"#"+strconv.Itoa(t.Number)+"；不覆盖既有链接")
	}
	if t.Status == "" {
		return nil, domain.Fail("TRACKER", "Ticket 顶部缺少 Status: 行，不能推断状态")
	}
	data := insertAfter(t.raw, statusLine, "Tracker-Link: github:"+m[1]+"#"+m[2])
	result := map[string]any{"kind": "tracker-link", "ticket": ref, "line": "Tracker-Link: github:" + m[1] + "#" + m[2], "remote_changed": false}
	if args["apply"] != "true" {
		result["status"] = "planned"
		return result, nil
	}
	tx, err := writeTickets(ctx, root, "tracker-link", map[string]ticketWrite{ref: {Data: data, Before: t.before}})
	if err != nil {
		return nil, err
	}
	result["status"], result["transaction"] = tx.Status, tx
	return result, nil
}

func insertAfter(raw []byte, after *regexp.Regexp, line string) []byte {
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if i < headerLines && after.MatchString(strings.TrimRight(l, "\r")) {
			out := append(append(append([]string{}, lines[:i+1]...), line), lines[i+1:]...)
			return []byte(strings.Join(out, "\n"))
		}
	}
	return raw
}

func setSynced(raw []byte, value string) []byte {
	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if i < headerLines && syncedLine.MatchString(l) {
			lines[i] = "Tracker-Synced: " + value
			return []byte(strings.Join(lines, "\n"))
		}
	}
	return insertAfter(raw, linkLine, "Tracker-Synced: "+value)
}

// writeTickets 以读取时的字节为守卫，在一个本地事务里写入。
type ticketWrite struct {
	Data   []byte
	Before domain.Descriptor
}

func writeTickets(ctx context.Context, root, kind string, files map[string]ticketWrite) (transaction.Result, error) {
	refs := make([]string, 0, len(files))
	for ref := range files {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	ops := []transaction.Operation{}
	guards := map[string]domain.Descriptor{}
	for _, ref := range refs {
		file := files[ref]
		before := file.Before
		guards[ref] = before
		ops = append(ops, transaction.Operation{Path: ref, Data: file.Data, Mode: before.Mode, Before: &before})
	}
	return transaction.ApplyContextWithGuards(ctx, root, kind, ops, guards)
}

// SyncPlan 绑定每个 Ticket 的本地字节与规划时的远端标签和更新时间。
type SyncPlan struct {
	SchemaVersion int        `json:"schema_version"`
	APIBase       string     `json:"api_base"`
	Selection     Selection  `json:"selection"`
	Kind          string     `json:"kind"`
	Root          string     `json:"root"`
	Items         []SyncItem `json:"items"`
	Skipped       []Skip     `json:"skipped"`
	PlanDigest    string     `json:"plan_digest"`
}

type SyncItem struct {
	Ticket        string   `json:"ticket"`
	TicketDigest  string   `json:"ticket_digest"`
	TicketMode    uint32   `json:"ticket_mode"`
	Status        string   `json:"status"`
	Repository    string   `json:"repository"`
	Number        int      `json:"number"`
	ObservedLabel []string `json:"observed_labels"`
	ObservedAt    string   `json:"observed_updated_at"`
	Desired       []string `json:"desired_labels"`
	Add           []string `json:"add"`
	Remove        []string `json:"remove"`
}

type Skip struct {
	Ticket string `json:"ticket"`
	Reason string `json:"reason"`
}

func (p SyncPlan) digest() string {
	p.PlanDigest = ""
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func syncRun(ctx context.Context, root string, args map[string]string, env Env) (any, error) {
	planning, applying := args["plan"] == "true", args["apply"] == "true"
	if args["confirm-remote"] != "" && !applying {
		return nil, domain.Fail("ARGUMENT", "--confirm-remote 只用于 --apply")
	}
	if planning == applying {
		return nil, domain.Fail("PLAN_REQUIRED", "同步需要 --plan [--out <新文件>]，或 --apply --plan-file <保存计划> --confirm-remote github")
	}
	enabled, _, err := mirrorConfig(root)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, domain.Fail("TRACKER", "未启用远程镜像：issue-tracker.md 需 remote_mirror: true（或主 tracker 为 github）")
	}
	client, err := NewClient(env.getenv)
	if err != nil {
		return nil, err
	}
	if applying {
		return syncApply(ctx, root, args, client, env)
	}
	plan, err := buildSyncPlan(ctx, root, Selection{Ticket: args["ticket"]}, client)
	if err != nil {
		return nil, err
	}
	if out := args["out"]; out != "" {
		if err := savePlan(root, out, plan); err != nil {
			return nil, err
		}
	}
	return map[string]any{"status": "planned", "plan": plan, "remote_changed": false}, nil
}

// savePlan 把计划保存在仓库之外且不覆盖已有文件，避免计划改变自己绑定的状态。
func savePlan(root, out string, plan SyncPlan) error {
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return domain.Fail("PATH", "计划文件所在目录不存在: "+filepath.Dir(abs))
	}
	top, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if dir == top || strings.HasPrefix(dir+string(filepath.Separator), top+string(filepath.Separator)) {
		return domain.Fail("PATH", "计划文件须保存在项目之外")
	}
	raw, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, filepath.Base(abs)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return domain.Fail("EXISTS", "计划文件已存在: "+out)
		}
		return err
	}
	_, werr := f.Write(append(raw, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
