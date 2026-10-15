package gitops

import (
	"errors"
	"io/fs"
	"os"
	"regexp"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// PolicyRef sits beside project-ci.yaml; yss-project.yaml stays identity only.
const PolicyRef = ".template-spec/process/git-policy.yaml"

const policyLimit = 1 << 20

type Policy struct {
	Source            string   `json:"source"`
	SchemaVersion     int      `json:"schemaVersion"`
	DefaultBranch     string   `json:"defaultBranch"`
	ProtectedBranches []string `json:"protectedBranches"`
	BranchPattern     string   `json:"branchPattern"`
	TicketTrailer     string   `json:"ticketTrailer"`
	TicketPattern     string   `json:"ticketPattern"`
	ReviewTrailer     string   `json:"reviewTrailer"`
	RequireSigned     bool     `json:"requireSigned"`

	branch, ticket *regexp.Regexp
}

var (
	safeBranch  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	trailerName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)
)

func DefaultPolicy() Policy {
	return Policy{
		Source: "default", SchemaVersion: 1, DefaultBranch: "main", ProtectedBranches: []string{"main"},
		BranchPattern: `^(feat|fix|chore|docs|refactor|test)/[A-Za-z0-9._-]+$`,
		TicketTrailer: "Ticket", TicketPattern: `^[A-Z][A-Z0-9]*-[0-9]+$`, ReviewTrailer: "Reviewed-by",
	}
}

// LoadPolicy reads the optional policy file; absence yields the defaults and
// any unknown key or invalid value is refused rather than ignored.
func LoadPolicy(root string) (Policy, error) {
	p := DefaultPolicy()
	path, err := safefs.Path(root, PolicyRef)
	if err != nil {
		return p, err
	}
	st, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, p.compile()
	}
	if err != nil {
		return p, err
	}
	if !st.Mode().IsRegular() || st.Size() > policyLimit {
		return p, domain.Fail("INPUT", PolicyRef+" 必须是不超过 1MiB 的普通文件")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	doc, err := schema.Parse(data)
	if err != nil {
		return p, domain.Fail("INPUT", PolicyRef+" 无法解析: "+err.Error())
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return p, domain.Fail("INPUT", PolicyRef+" 顶层必须是对象")
	}
	p.Source = PolicyRef
	for key, value := range m {
		bad := false
		switch key {
		case "schema_version":
			bad = value == nil || toString(value) != "1"
		case "default_branch":
			p.DefaultBranch, bad = text(value)
		case "protected_branches":
			p.ProtectedBranches, bad = list(value)
		case "branch_pattern":
			p.BranchPattern, bad = text(value)
		case "ticket_trailer":
			p.TicketTrailer, bad = text(value)
		case "ticket_pattern":
			p.TicketPattern, bad = text(value)
		case "review_trailer":
			p.ReviewTrailer, bad = text(value)
		case "require_signed":
			p.RequireSigned, ok = value.(bool)
			bad = !ok
		default:
			return p, domain.Fail("INPUT", PolicyRef+" 含未知字段: "+key)
		}
		if bad {
			return p, domain.Fail("INPUT", PolicyRef+" 字段无效: "+key)
		}
	}
	if _, present := m["schema_version"]; !present {
		return p, domain.Fail("INPUT", PolicyRef+" 缺少 schema_version: 1")
	}
	return p, p.compile()
}

func (p *Policy) compile() error {
	for _, b := range append([]string{p.DefaultBranch}, p.ProtectedBranches...) {
		if !safeBranch.MatchString(b) {
			return domain.Fail("INPUT", "分支名无效: "+b)
		}
	}
	for _, t := range []string{p.TicketTrailer, p.ReviewTrailer} {
		if !trailerName.MatchString(t) {
			return domain.Fail("INPUT", "trailer 名称无效: "+t)
		}
	}
	var err error
	if p.branch, err = regexp.Compile(p.BranchPattern); err != nil {
		return domain.Fail("INPUT", "branch_pattern 不是合法正则")
	}
	if p.ticket, err = regexp.Compile(p.TicketPattern); err != nil {
		return domain.Fail("INPUT", "ticket_pattern 不是合法正则")
	}
	return nil
}

func (p Policy) BranchAllowed(name string) bool { return p.branch.MatchString(name) }
func (p Policy) TicketValid(id string) bool     { return p.ticket.MatchString(id) }
func (p Policy) Protected(name string) bool {
	for _, b := range p.ProtectedBranches {
		if b == name {
			return true
		}
	}
	return false
}

func text(v any) (string, bool) {
	s, ok := v.(string)
	return s, !ok || s == ""
}

func list(v any) ([]string, bool) {
	items, ok := v.([]any)
	if !ok {
		return nil, true
	}
	out := []string{}
	for _, item := range items {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil, true
		}
		out = append(out, s)
	}
	return out, false
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case interface{ String() string }:
		return t.String()
	}
	return ""
}
