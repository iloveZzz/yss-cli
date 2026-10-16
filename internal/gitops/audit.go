package gitops

import (
	"context"
	"strings"
)

// Commit is one log record with the trailers the policy names.
type Commit struct {
	SHA       string   `json:"sha"`
	Parents   []string `json:"parents"`
	Author    string   `json:"author"`
	Email     string   `json:"email"`
	Subject   string   `json:"subject"`
	Tickets   []string `json:"tickets"`
	Reviewers []string `json:"reviewers"`
	Signature string   `json:"signature,omitempty"`
}

// Log reads from..to, both already resolved to full SHAs by Runner.Commit.
// Signature status runs gpg, so it is read only when the policy needs it.
func (r *Runner) Log(ctx context.Context, p Policy, from, to string, firstParent bool) ([]Commit, error) {
	format := "%H%x1f%P%x1f%an%x1f%ae%x1f%s%x1f%(trailers:key=" + p.TicketTrailer + ",valueonly,separator=%x1d)%x1f%(trailers:key=" + p.ReviewTrailer + ",valueonly,separator=%x1d)%x1f"
	if p.RequireSigned {
		format += "%G?"
	}
	args := []string{"log", "--format=" + format + "%x1e"}
	if firstParent {
		args = append(args, "--first-parent")
	}
	args = append(args, from+".."+to)
	out, err := r.Query(ctx, args...)
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, record := range strings.Split(string(out), "\x1e") {
		record = strings.TrimLeft(record, "\n")
		if record == "" {
			continue
		}
		f := strings.Split(record, "\x1f")
		if len(f) < 8 {
			continue
		}
		commits = append(commits, Commit{
			SHA: f[0], Parents: strings.Fields(f[1]), Author: f[2], Email: f[3], Subject: f[4],
			Tickets: values(f[5]), Reviewers: values(f[6]), Signature: strings.TrimSpace(f[7]),
		})
	}
	return commits, nil
}

func values(s string) []string {
	out := []string{}
	for _, v := range strings.Split(s, "\x1d") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

type Finding struct {
	Rule    string `json:"rule"`
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// Audit applies the policy rules to non-merge commits. directCommits marks
// the range as first-parent history of a protected branch, where a
// non-merge commit did not arrive through a merge.
func Audit(p Policy, commits []Commit, directCommits bool) []Finding {
	findings := []Finding{}
	for _, c := range commits {
		if len(c.Parents) > 1 {
			continue
		}
		valid := false
		for _, t := range c.Tickets {
			if p.TicketValid(t) {
				valid = true
			}
		}
		if !valid {
			findings = append(findings, Finding{"ticket-trailer", c.SHA, c.Subject, "缺少符合 ticket_pattern 的 " + p.TicketTrailer + " trailer"})
		}
		for _, reviewer := range c.Reviewers {
			if strings.Contains(reviewer, "<"+c.Email+">") || strings.HasPrefix(reviewer, c.Author+" ") || reviewer == c.Author {
				findings = append(findings, Finding{"self-review", c.SHA, c.Subject, p.ReviewTrailer + " 与作者相同"})
				break
			}
		}
		if p.RequireSigned && c.Signature != "G" && c.Signature != "U" {
			findings = append(findings, Finding{"unsigned", c.SHA, c.Subject, "签名状态 " + c.Signature})
		}
		if directCommits {
			findings = append(findings, Finding{"direct-commit", c.SHA, c.Subject, "保护分支一级历史中的非合并提交；squash/rebase 合并流程下属预期"})
		}
	}
	return findings
}

type ChangeGroup struct {
	Ticket  string   `json:"ticket"`
	Commits []Commit `json:"commits"`
}

// Changelog groups commits by ticket in first-seen order; commits without a
// ticket fall into one "unlinked" group at the end.
func Changelog(commits []Commit) []ChangeGroup {
	groups := []ChangeGroup{}
	index := map[string]int{}
	unlinked := ChangeGroup{Ticket: "unlinked", Commits: []Commit{}}
	for _, c := range commits {
		if len(c.Tickets) == 0 {
			unlinked.Commits = append(unlinked.Commits, c)
			continue
		}
		for _, t := range c.Tickets {
			i, ok := index[t]
			if !ok {
				i = len(groups)
				index[t] = i
				groups = append(groups, ChangeGroup{Ticket: t, Commits: []Commit{}})
			}
			groups[i].Commits = append(groups[i].Commits, c)
		}
	}
	if len(unlinked.Commits) > 0 {
		groups = append(groups, unlinked)
	}
	return groups
}
