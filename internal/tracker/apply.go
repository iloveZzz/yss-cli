package tracker

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func checkTicket(root string, item SyncItem) (Ticket, error) {
	t, err := readTicket(root, item.Ticket)
	if err != nil {
		return t, err
	}
	if t.Digest != item.TicketDigest || t.before.Mode != item.TicketMode {
		return t, domain.Fail("INPUT_DRIFT", "Ticket 已变化，重新规划: "+item.Ticket)
	}
	return t, nil
}

func stateLabels(labels []string) []string {
	out := []string{}
	for _, label := range labels {
		if contains(States, label) {
			out = append(out, label)
		}
	}
	return out
}

// Re-check local authority around each remote query, and never replace unrelated labels.
func applySyncItem(ctx context.Context, root string, item SyncItem, client *Client) (Ticket, Issue, bool, error) {
	expected := Issue{Labels: item.ObservedLabel, UpdatedAt: item.ObservedAt}
	changed := false
	inspect := func() (Ticket, Issue, error) {
		t, err := checkTicket(root, item)
		if err != nil {
			return t, Issue{}, err
		}
		issue, err := client.GetIssue(ctx, item.Repository, item.Number)
		if err != nil {
			return t, issue, err
		}
		if issue.UpdatedAt != expected.UpdatedAt || !reflect.DeepEqual(issue.Labels, expected.Labels) {
			return t, issue, domain.Fail("TRACKER_DRIFT", "远端 Issue 已变化，重新规划: "+item.Ticket)
		}
		t, err = checkTicket(root, item)
		return t, issue, err
	}
	t, issue, err := inspect()
	if err != nil {
		return t, issue, changed, err
	}
	type labelChange struct {
		label string
		add   bool
	}
	changes := []labelChange{}
	for _, label := range item.Add {
		changes = append(changes, labelChange{label, true})
	}
	for _, label := range item.Remove {
		changes = append(changes, labelChange{label, false})
	}
	for index, op := range changes {
		if index > 0 {
			t, issue, err = inspect()
			if err != nil {
				return t, issue, changed, err
			}
		}
		wantStates := stateLabels(issue.Labels)
		if op.add {
			// Addition precedes removal: other workflow states remain until their own requests.
			wantStates = stateLabels(issue.Labels)
			if !contains(wantStates, op.label) {
				wantStates = append(wantStates, op.label)
			}
			err = client.AddLabel(ctx, item.Repository, item.Number, op.label)
		} else {
			filtered := []string{}
			for _, label := range wantStates {
				if label != op.label {
					filtered = append(filtered, label)
				}
			}
			wantStates = filtered
			err = client.RemoveLabel(ctx, item.Repository, item.Number, op.label)
		}
		if err != nil {
			return t, issue, changed, err
		}
		changed = true
		issue, err = client.GetIssue(ctx, item.Repository, item.Number)
		if err != nil {
			return t, issue, changed, err
		}
		if !sameStates(stateLabels(issue.Labels), wantStates) {
			return t, issue, changed, domain.Fail("TRACKER_DRIFT", "写后五态标签与预期不符: "+item.Ticket)
		}
		expected = issue
		t, err = checkTicket(root, item)
		if err != nil {
			return t, issue, changed, err
		}
	}
	if !sameStates(stateLabels(issue.Labels), []string{item.Status}) {
		return t, issue, changed, domain.Fail("TRACKER_DRIFT", "写后五态标签与本地状态不符: "+item.Ticket)
	}
	return t, issue, changed, nil
}

func sameStates(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, label := range a {
		if !contains(b, label) {
			return false
		}
	}
	return true
}

func syncApply(ctx context.Context, root string, args map[string]string, client *Client, env Env) (any, error) {
	if args["confirm-remote"] != "github" {
		return nil, domain.Fail("CAPABILITY", "写入 GitHub 需要用户确认：--confirm-remote github；未触达远端")
	}
	raw, err := os.ReadFile(args["plan-file"])
	if err != nil {
		return nil, domain.Fail("PLAN", "无法读取计划文件: "+args["plan-file"])
	}
	plan, err := readSyncPlan(raw)
	if err != nil {
		return nil, err
	}
	if err = validateSyncPlan(ctx, root, plan, client); err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	files := map[string]ticketWrite{}
	stamp := env.now().Format(time.RFC3339)
	changed := false
	state := "unchanged"
	var failure error
	for index, item := range plan.Items {
		row := map[string]any{"ticket": item.Ticket, "issue": item.Repository + "#" + strconv.Itoa(item.Number), "add": item.Add, "remove": item.Remove, "local_recorded": false}
		t, issue, wrote, err := applySyncItem(ctx, root, item, client)
		changed = changed || wrote
		state = remoteState(changed)
		rows = append(rows, row)
		if err != nil {
			failure = err
			row["status"], row["error"] = "failed", err.Error()
			if writeUnknown(err) {
				row["status"], state = "unknown", "unknown"
			}
			for _, pending := range plan.Items[index+1:] {
				rows = append(rows, map[string]any{"ticket": pending.Ticket, "issue": pending.Repository + "#" + strconv.Itoa(pending.Number), "status": "not-attempted", "local_recorded": false})
			}
			break
		}
		row["status"], row["labels"] = "unchanged", issue.Labels
		if wrote {
			row["status"] = "updated"
		}
		files[item.Ticket] = ticketWrite{Data: setSynced(t.raw, stamp+" labels="+strings.Join(issue.Labels, ",")), Before: t.before}
	}
	result := map[string]any{"status": "applied", "remote_changed": changed, "remote_state": state, "items": rows, "authority": "local-markdown", "local_recorded": len(files) == 0 && failure == nil}
	if len(files) > 0 {
		tx, err := writeTickets(ctx, root, "tracker-sync", files)
		if err != nil {
			result["local_error"] = err.Error()
			result["local_error_code"] = failureCode(err)
			if tx.Status != "" {
				result["transaction"] = tx
			}
			if failure == nil {
				failure = domain.WithMessage(err, "远端同步已处理，但本地同步记录写入失败: "+err.Error())
			}
		} else {
			result["local_recorded"], result["transaction"] = true, tx
			for _, row := range rows {
				if _, ok := files[row["ticket"].(string)]; ok {
					row["local_recorded"] = true
				}
			}
		}
	}
	if failure != nil {
		result["status"] = "partial"
		return failedSync(result, failure)
	}
	return result, nil
}
