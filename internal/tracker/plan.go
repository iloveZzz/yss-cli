package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// Selection records the original scope so application can rebuild every item.
type Selection struct {
	Ticket string `json:"ticket"`
}

func planError() error {
	return domain.Fail("PLAN", "Tracker 计划必须是来源与当前语义一致的 v2；请重新运行 yss tracker sync --root <项目> --plan --out <项目外新文件>")
}

func readSyncPlan(raw []byte) (SyncPlan, error) {
	var p SyncPlan
	if !json.Valid(raw) {
		return p, planError()
	}
	if _, err := schema.Parse(raw); err != nil {
		return p, planError()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, planError()
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return p, planError()
	}
	if p.SchemaVersion != 2 || p.Kind != "tracker-sync-plan" || p.PlanDigest == "" || p.PlanDigest != p.digest() {
		return p, planError()
	}
	return p, nil
}

func localSyncPlan(root string, selection Selection, api string) (SyncPlan, []Ticket, error) {
	p := SyncPlan{SchemaVersion: 2, APIBase: api, Selection: selection, Kind: "tracker-sync-plan", Root: root, Items: []SyncItem{}, Skipped: []Skip{}}
	list, err := tickets(root)
	if err != nil {
		return p, nil, err
	}
	selected := []Ticket{}
	found := selection.Ticket == ""
	for _, t := range list {
		if selection.Ticket != "" && t.Ref != selection.Ticket {
			continue
		}
		found = true
		switch {
		case t.Repository == "":
			p.Skipped = append(p.Skipped, Skip{t.Ref, "未链接 Issue"})
		case !contains(States, t.Status):
			p.Skipped = append(p.Skipped, Skip{t.Ref, "Status 不是五态之一: " + t.Status})
		default:
			selected = append(selected, t)
		}
	}
	if !found {
		return p, nil, domain.Fail("ARGUMENT", "未找到选择的 Ticket: "+selection.Ticket)
	}
	return p, selected, nil
}

func syncItem(t Ticket, issue Issue) SyncItem {
	desired := desiredLabels(issue.Labels, t.Status)
	add, remove := diff(issue.Labels, desired)
	return SyncItem{Ticket: t.Ref, TicketDigest: t.Digest, TicketMode: t.before.Mode, Status: t.Status, Repository: t.Repository, Number: t.Number, ObservedLabel: issue.Labels, ObservedAt: issue.UpdatedAt, Desired: desired, Add: add, Remove: remove}
}

func buildSyncPlan(ctx context.Context, root string, selection Selection, client *Client) (SyncPlan, error) {
	p, selected, err := localSyncPlan(root, selection, client.Base)
	if err != nil {
		return p, err
	}
	for _, t := range selected {
		if !repoPattern.MatchString(t.Repository) || t.Number <= 0 {
			return p, domain.Fail("TRACKER", "Ticket 链接无效: "+t.Ref)
		}
		issue, err := client.GetIssue(ctx, t.Repository, t.Number)
		if err != nil {
			return p, err
		}
		p.Items = append(p.Items, syncItem(t, issue))
	}
	p.PlanDigest = p.digest()
	return p, nil
}

func validateSyncPlan(ctx context.Context, root string, saved SyncPlan, client *Client) error {
	if saved.Root != root || saved.APIBase != client.Base {
		return planError()
	}
	local, selected, err := localSyncPlan(root, saved.Selection, client.Base)
	if err != nil {
		return err
	}
	if len(selected) != len(saved.Items) || !reflect.DeepEqual(local.Skipped, saved.Skipped) {
		return planError()
	}
	seenIssues := map[string]map[int]bool{}
	for i, t := range selected {
		item := saved.Items[i]
		if item.Ticket != t.Ref {
			return planError()
		}
		if t.Digest != item.TicketDigest || t.before.Mode != item.TicketMode {
			return domain.Fail("INPUT_DRIFT", "Ticket 已变化，重新规划: "+t.Ref)
		}
		if item.Repository != t.Repository || item.Number != t.Number || item.Status != t.Status || !repoPattern.MatchString(t.Repository) || t.Number <= 0 {
			return planError()
		}
		if seenIssues[t.Repository] == nil {
			seenIssues[t.Repository] = map[int]bool{}
		}
		if seenIssues[t.Repository][t.Number] {
			return planError()
		}
		seenIssues[t.Repository][t.Number] = true
	}
	// Only query destinations derived from the current local authority.
	fresh, err := buildSyncPlan(ctx, root, saved.Selection, client)
	if err != nil {
		return err
	}
	for i, item := range fresh.Items {
		old := saved.Items[i]
		if item.ObservedAt != old.ObservedAt || !reflect.DeepEqual(item.ObservedLabel, old.ObservedLabel) {
			return domain.Fail("TRACKER_DRIFT", "远端 Issue 自规划后已变化，重新规划: "+item.Ticket)
		}
	}
	if fresh.PlanDigest != saved.PlanDigest {
		return planError()
	}
	return nil
}
