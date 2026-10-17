// Package trace 聚合功能包内已有资产，生成从需求、验收到切片、验证、提交的追溯矩阵与缺口。
// 只读：不写项目文件，不判定批准或完成；缺口是提示，--strict 时才作为失败。
package trace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/gitops"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
)

const maxFiles = 2000

type BusinessTicket struct {
	ID           string   `json:"id"`
	Ref          string   `json:"ref"`
	Requirements []string `json:"requirement_refs"`
	Acceptance   []string `json:"acceptance_refs"`
}

type SliceTicket struct {
	Ref             string   `json:"ref"`
	Status          string   `json:"status,omitempty"`
	BusinessTickets []string `json:"business_ticket_refs"`
	Acceptance      []string `json:"acceptance_refs"`
}

type Verification struct {
	Name       string   `json:"name"`
	Command    string   `json:"command"`
	Acceptance []string `json:"acceptance_refs"`
}

type BasisDrift struct {
	Key      string `json:"key"`
	Ref      string `json:"ref"`
	Recorded string `json:"recorded_digest"`
	Current  string `json:"current_digest"`
}

type Slice struct {
	SliceID       string         `json:"slice_id"`
	Ref           string         `json:"ref"`
	Acceptance    []string       `json:"acceptance_refs"`
	Verifications []Verification `json:"verifications"`
	BasisDrift    []BasisDrift   `json:"basis_drift"`
	Commits       []string       `json:"commits"`
}

type Commit struct {
	SHA     string   `json:"sha"`
	Subject string   `json:"subject"`
	Tickets []string `json:"tickets"`
	Files   []string `json:"files"`
	Slices  []string `json:"slices"`
}

type Row struct {
	Acceptance      string   `json:"acceptance"`
	BusinessTickets []string `json:"business_tickets"`
	Requirements    []string `json:"requirements"`
	Slices          []string `json:"slices"`
	Verifications   []string `json:"verifications"`
	Commits         []string `json:"commits"`
}

type Gaps struct {
	AcceptanceWithoutSlice []string     `json:"acceptance_without_slice"`
	AcceptanceWithoutTest  []string     `json:"acceptance_without_verification"`
	CommitsWithoutTicket   []string     `json:"commits_without_ticket"`
	CommitsUnmapped        []string     `json:"commits_unmapped_ticket"`
	BasisChanged           []BasisDrift `json:"basis_changed"`
}

type Report struct {
	Kind            string            `json:"kind"`
	Feature         string            `json:"feature"`
	Status          string            `json:"status"`
	ReadOnly        bool              `json:"read_only"`
	BusinessTickets []BusinessTicket  `json:"business_tickets"`
	SliceTickets    []SliceTicket     `json:"slice_tickets"`
	Slices          []Slice           `json:"slices"`
	Commits         []Commit          `json:"commits"`
	CommitRange     map[string]string `json:"commit_range,omitempty"`
	Matrix          []Row             `json:"matrix"`
	Gaps            Gaps              `json:"gaps"`
	Unread          []string          `json:"unread"`
}

type Options struct {
	Feature, ImplementationRoot, Since, Until string
}

// GapsFound 在 --strict 时返回，携带完整报告。
type GapsFound struct{ Report *Report }

func (e *GapsFound) Error() string    { return "追溯存在缺口；逐项见 gaps" }
func (e *GapsFound) ErrorResult() any { return e.Report }
func (e *GapsFound) Unwrap() error {
	return &domain.Error{Code: "TRACE_GAPS", Message: e.Error(), Exit: 1}
}

func Build(ctx context.Context, root string, opts Options) (*Report, error) {
	layout, err := worklayout.Read(root)
	if err != nil {
		return nil, err
	}
	featureRoot, err := layout.FeatureRoot(opts.Feature)
	if err != nil {
		return nil, domain.Fail("ARGUMENT", "--feature 必须是 tracker 根下的功能包名")
	}
	base, err := safefs.Path(root, featureRoot)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(base); err != nil || !st.IsDir() {
		return nil, domain.Fail("INPUT", "功能包不存在: "+featureRoot)
	}
	r := &Report{Kind: "yss-trace", Feature: opts.Feature, ReadOnly: true, BusinessTickets: []BusinessTicket{}, SliceTickets: []SliceTicket{}, Slices: []Slice{}, Commits: []Commit{}, Matrix: []Row{}, Unread: []string{}}
	files := []string{}
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if !d.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".md" || ext == ".yaml" || ext == ".yml" || ext == ".json" {
				files = append(files, path)
			}
		}
		if len(files) > maxFiles {
			return domain.Fail("INPUT", "功能包文件超过上限")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			r.Unread = append(r.Unread, rel)
			continue
		}
		doc, ok := document(rel, raw)
		if !ok {
			continue
		}
		collect(r, root, rel, doc)
	}
	if opts.ImplementationRoot != "" {
		if err := commits(ctx, r, opts); err != nil {
			return nil, err
		}
	}
	link(r)
	return r, nil
}

// document 读取 YAML/JSON 文件或 Markdown front matter；无法解析的文件不参与追溯。
func document(ref string, raw []byte) (map[string]any, bool) {
	text := string(raw)
	if strings.HasSuffix(ref, ".md") {
		if !strings.HasPrefix(text, "---\n") {
			return nil, false
		}
		end := strings.Index(text[4:], "\n---")
		if end < 0 {
			return nil, false
		}
		text = text[4 : 4+end]
	}
	value, err := schema.Parse([]byte(text))
	if err != nil {
		return nil, false
	}
	m, ok := value.(map[string]any)
	return m, ok
}

func str(v any) string { s, _ := v.(string); return s }

func strs(v any) []string {
	out := []string{}
	list, _ := v.([]any)
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func collect(r *Report, root, ref string, doc map[string]any) {
	switch {
	case str(doc["kind"]) == "business-ticket":
		r.BusinessTickets = append(r.BusinessTickets, BusinessTicket{ID: str(doc["id"]), Ref: ref, Requirements: strs(doc["requirement_refs"]), Acceptance: strs(doc["acceptance_refs"])})
	case str(doc["kind"]) == "vertical-slice-ticket":
		r.SliceTickets = append(r.SliceTickets, SliceTicket{Ref: ref, Status: str(doc["status"]), BusinessTickets: strs(doc["business_ticket_refs"]), Acceptance: strs(doc["acceptance_refs"])})
	case str(doc["contract_id"]) != "" && str(doc["slice_id"]) != "" && doc["verification"] != nil:
		s := Slice{SliceID: str(doc["slice_id"]), Ref: ref, Acceptance: []string{}, Verifications: []Verification{}, BasisDrift: []BasisDrift{}, Commits: []string{}}
		seen := map[string]bool{}
		add := func(ids []string) {
			for _, id := range ids {
				if !seen[id] {
					seen[id] = true
					s.Acceptance = append(s.Acceptance, id)
				}
			}
		}
		if acc, ok := doc["acceptance"].(map[string]any); ok {
			keys := make([]string, 0, len(acc))
			for k := range acc {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			add(keys)
		}
		if verification, ok := doc["verification"].(map[string]any); ok {
			names := make([]string, 0, len(verification))
			for name := range verification {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				v, _ := verification[name].(map[string]any)
				refs := strs(v["acceptance_refs"])
				add(refs)
				s.Verifications = append(s.Verifications, Verification{Name: name, Command: str(v["command"]), Acceptance: refs})
			}
		}
		if units, ok := doc["work_units"].([]any); ok {
			for _, u := range units {
				m, _ := u.(map[string]any)
				add(strs(m["acceptance_refs"]))
			}
		}
		sort.Strings(s.Acceptance)
		if basis, ok := doc["basis"].(map[string]any); ok {
			keys := make([]string, 0, len(basis))
			for k := range basis {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				b, ok := basis[k].(map[string]any)
				if !ok || str(b["ref"]) == "" || str(b["digest"]) == "" {
					continue
				}
				current := currentDigest(root, str(b["ref"]))
				if current != str(b["digest"]) {
					s.BasisDrift = append(s.BasisDrift, BasisDrift{Key: k, Ref: str(b["ref"]), Recorded: str(b["digest"]), Current: current})
				}
			}
		}
		r.Slices = append(r.Slices, s)
	}
}

// currentDigest 返回项目内文件当前原字节摘要；缺失或越界返回 missing。
func currentDigest(root, ref string) string {
	path, err := safefs.Path(root, ref)
	if err != nil {
		return "unreadable"
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "missing"
	}
	if err != nil {
		return "unreadable"
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func commits(ctx context.Context, r *Report, opts Options) error {
	if opts.Since == "" {
		return domain.Fail("ARGUMENT", "读取提交需要 --since <起点引用>")
	}
	runner, err := gitops.NewRunner(opts.ImplementationRoot)
	if err != nil {
		return err
	}
	// 策略决定 Ticket trailer 名；策略无效时拒绝而不是回退默认值，避免按错误 trailer 归属提交。
	policy, err := gitops.LoadPolicy(opts.ImplementationRoot)
	if err != nil {
		return err
	}
	until := opts.Until
	if until == "" {
		until = "HEAD"
	}
	from, err := runner.Commit(ctx, opts.Since)
	if err != nil {
		return domain.Fail("BASELINE", "--since 无法解析: "+opts.Since)
	}
	to, err := runner.Commit(ctx, until)
	if err != nil {
		return domain.Fail("BASELINE", "--until 无法解析: "+until)
	}
	r.CommitRange = map[string]string{"since": from, "until": to}
	list, err := runner.Log(ctx, policy, from, to, false)
	if err != nil {
		return err
	}
	out, err := runner.Query(ctx, "log", "--format=%x1e%H", "--name-only", from+".."+to)
	if err != nil {
		return err
	}
	files := map[string][]string{}
	for _, record := range strings.Split(string(out), "\x1e") {
		lines := strings.Split(strings.TrimSpace(record), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		for _, f := range lines[1:] {
			if f = strings.TrimSpace(f); f != "" {
				files[lines[0]] = append(files[lines[0]], f)
			}
		}
	}
	for _, c := range list {
		fl := files[c.SHA]
		if fl == nil {
			fl = []string{}
		}
		r.Commits = append(r.Commits, Commit{SHA: c.SHA, Subject: c.Subject, Tickets: c.Tickets, Files: fl, Slices: []string{}})
	}
	return nil
}

func unique(list []string) []string {
	sort.Strings(list)
	out := []string{}
	for i, v := range list {
		if i == 0 || v != list[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// link 以验收 ID 为行组装矩阵，并按 Ticket trailer 与 slice_id 关联提交。
func link(r *Report) {
	rows := map[string]*Row{}
	row := func(id string) *Row {
		if rows[id] == nil {
			rows[id] = &Row{Acceptance: id, BusinessTickets: []string{}, Requirements: []string{}, Slices: []string{}, Verifications: []string{}, Commits: []string{}}
		}
		return rows[id]
	}
	for _, bt := range r.BusinessTickets {
		for _, ac := range bt.Acceptance {
			x := row(ac)
			x.BusinessTickets = append(x.BusinessTickets, bt.ID)
			x.Requirements = append(x.Requirements, bt.Requirements...)
		}
	}
	slices := map[string]int{}
	for i := range r.Slices {
		slices[r.Slices[i].SliceID] = i
	}
	for i := range r.Commits {
		c := &r.Commits[i]
		for _, ticket := range c.Tickets {
			if idx, ok := slices[ticket]; ok {
				r.Slices[idx].Commits = append(r.Slices[idx].Commits, c.SHA)
				c.Slices = append(c.Slices, ticket)
			}
		}
		switch {
		case len(c.Tickets) == 0:
			r.Gaps.CommitsWithoutTicket = append(r.Gaps.CommitsWithoutTicket, c.SHA)
		case len(c.Slices) == 0:
			r.Gaps.CommitsUnmapped = append(r.Gaps.CommitsUnmapped, c.SHA)
		}
	}
	for _, s := range r.Slices {
		for _, ac := range s.Acceptance {
			x := row(ac)
			x.Slices = append(x.Slices, s.SliceID)
			x.Commits = append(x.Commits, s.Commits...)
		}
		for _, v := range s.Verifications {
			for _, ac := range v.Acceptance {
				row(ac).Verifications = append(row(ac).Verifications, s.SliceID+"/"+v.Name)
			}
		}
		r.Gaps.BasisChanged = append(r.Gaps.BasisChanged, s.BasisDrift...)
	}
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		x := rows[id]
		x.BusinessTickets, x.Requirements, x.Slices, x.Verifications, x.Commits = unique(x.BusinessTickets), unique(x.Requirements), unique(x.Slices), unique(x.Verifications), unique(x.Commits)
		if len(x.Slices) == 0 {
			r.Gaps.AcceptanceWithoutSlice = append(r.Gaps.AcceptanceWithoutSlice, id)
		}
		if len(x.Verifications) == 0 {
			r.Gaps.AcceptanceWithoutTest = append(r.Gaps.AcceptanceWithoutTest, id)
		}
		r.Matrix = append(r.Matrix, *x)
	}
	for _, list := range []*[]string{&r.Gaps.AcceptanceWithoutSlice, &r.Gaps.AcceptanceWithoutTest, &r.Gaps.CommitsWithoutTicket, &r.Gaps.CommitsUnmapped} {
		if *list == nil {
			*list = []string{}
		}
	}
	if r.Gaps.BasisChanged == nil {
		r.Gaps.BasisChanged = []BasisDrift{}
	}
	r.Status = "complete"
	if r.HasGaps() {
		r.Status = "gaps-found"
	}
	if len(r.Matrix) == 0 {
		r.Status = "no-acceptance"
	}
}

func (r *Report) HasGaps() bool {
	g := r.Gaps
	return len(g.AcceptanceWithoutSlice)+len(g.AcceptanceWithoutTest)+len(g.CommitsWithoutTicket)+len(g.CommitsUnmapped)+len(g.BasisChanged) > 0
}
