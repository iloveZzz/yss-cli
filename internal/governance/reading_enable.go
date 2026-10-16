package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/governance/reading"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const (
	readingEnableKind     = "reading-enable"
	readingEnablePlanKind = "reading-enable-plan"
)

type readingPreview struct {
	Ref          string  `json:"ref"`
	BeforeDigest *string `json:"before_digest"`
	After        string  `json:"after"`
}

// readingEnablePlan 是 Go 定义的启用计划。Node 旧计划（无 protocol_version、字段不同）一律按 PLAN 拒绝。
type readingEnablePlan struct {
	SchemaVersion   int                          `json:"schema_version"`
	ProtocolVersion int                          `json:"protocol_version"`
	Kind            string                       `json:"kind"`
	Root            string                       `json:"root"`
	CheckpointRef   string                       `json:"checkpoint_ref"`
	PolicyBefore    domain.Descriptor            `json:"policy_before"`
	PolicyAfter     reading.PolicyDoc            `json:"policy_after"`
	Operations      []transaction.Operation      `json:"operations"`
	Observed        map[string]domain.Descriptor `json:"observed"`
	Preview         []readingPreview             `json:"preview"`
	PlanDigest      string                       `json:"plan_digest"`
}

func readingEnableDigest(plan readingEnablePlan) (string, error) {
	plan.PlanDigest = ""
	b, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	return safefs.Digest(b), nil
}

// buildReadingEnable 在当前磁盘状态上规划“写策略 + 渲染”，两者进入同一个事务。
// 返回的会话保留全部观察，供提交时作为只读守卫。
func buildReadingEnable(ctx context.Context, root string, args map[string]string, ref string) (readingEnablePlan, *kernel.SemanticSession, *reading.RenderPlan, error) {
	plan := readingEnablePlan{SchemaVersion: 1, ProtocolVersion: 1, Kind: readingEnablePlanKind, Root: root, CheckpointRef: ref, Observed: map[string]domain.Descriptor{}, Operations: []transaction.Operation{}, Preview: []readingPreview{}}
	s := kernel.NewSemanticSession(ctx, root, args)
	if err := readingSessionReady(s); err != nil {
		return plan, nil, nil, err
	}
	policyAfter, doc, policyBefore, present, err := reading.EnablePolicy(s, ref)
	if err != nil {
		return plan, nil, nil, err
	}
	plan.PolicyAfter = doc
	if plan.PolicyBefore, err = s.V.Watch(reading.PolicyRef); err != nil {
		return plan, nil, nil, err
	}
	render, err := reading.PlanReadingWith(s, ref, map[string][]byte{reading.PolicyRef: policyAfter})
	if err != nil {
		return plan, nil, nil, err
	}
	if err = s.Finish(); err != nil {
		return plan, nil, nil, err
	}
	changes := render.Changes
	if !present || !bytes.Equal(policyBefore, policyAfter) {
		changes = append([]reading.RenderChange{{Ref: reading.PolicyRef, Before: policyBefore, After: policyAfter, Exists: present}}, changes...)
	}
	render.Changes = changes
	for _, c := range render.Changes {
		if c.Ref == reading.PolicyRef {
			continue
		}
		var before *string
		if c.Exists {
			digest := "sha256:" + safefs.Digest(c.Before)
			before = &digest
		}
		plan.Preview = append(plan.Preview, readingPreview{Ref: c.Ref, BeforeDigest: before, After: string(c.After)})
	}
	ops, err := readingOperations(s, render)
	if err != nil {
		return plan, nil, nil, err
	}
	plan.Operations = ops
	for path, d := range s.V.Observed {
		if path != ".yss/transactions" && !hasPrefix(path, ".yss/transactions/") {
			plan.Observed[path] = d
		}
	}
	plan.PlanDigest, err = readingEnableDigest(plan)
	return plan, s, render, err
}

func hasPrefix(s, prefix string) bool { return len(s) >= len(prefix) && s[:len(prefix)] == prefix }

func readingPlanEnable(ctx context.Context, root string, args map[string]string) (any, error) {
	ref := args["checkpoint"]
	if ref == "" {
		return nil, domain.Fail("ARGUMENT", "plan-enable 需要 --checkpoint")
	}
	if err := readingPendingTransaction(root); err != nil {
		return nil, err
	}
	plan, _, _, err := buildReadingEnable(ctx, root, args, ref)
	if err != nil {
		return nil, err
	}
	if out := args["output"]; out != "" {
		path, err := safefs.Path(root, filepath.ToSlash(out))
		if err != nil {
			return nil, err
		}
		if err = saveReadingPlan(path, plan); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func saveReadingPlan(path string, plan readingEnablePlan) error {
	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return domain.Wrap("PLAN", err)
	}
	_, err = f.Write(append(b, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func readingPendingTransaction(root string) error {
	status, err := transaction.Status(root)
	if err != nil {
		return err
	}
	if len(status.Pending) > 0 {
		return domain.Fail("INTERRUPTED", "存在未完成 Go 事务，先 recover")
	}
	return nil
}

func readingApplyEnable(ctx context.Context, root string, args map[string]string) (any, error) {
	file := args["arg0"]
	if file == "" {
		return nil, domain.Fail("ARGUMENT", "apply-enable 需要计划文件")
	}
	path, err := safefs.Path(root, filepath.ToSlash(file))
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, domain.Wrap("PLAN", err)
	}
	if _, err = schema.Parse(raw); err != nil {
		return nil, domain.Wrap("PLAN", err)
	}
	var plan readingEnablePlan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&plan); err != nil {
		return nil, domain.Fail("PLAN", "阅读启用计划格式不受支持（旧版 Node 计划须重新生成）: "+err.Error())
	}
	digest, err := readingEnableDigest(plan)
	if err != nil || plan.Kind != readingEnablePlanKind || plan.SchemaVersion != 1 || plan.ProtocolVersion != 1 || plan.Root != root || digest != plan.PlanDigest {
		return nil, domain.Fail("PLAN", "阅读启用计划身份或摘要非法；请重新生成")
	}
	if err = readingPendingTransaction(root); err != nil {
		return nil, err
	}
	fresh, s, render, err := buildReadingEnable(ctx, root, args, plan.CheckpointRef)
	if err != nil {
		return nil, err
	}
	if fresh.PlanDigest != plan.PlanDigest {
		// 已成功应用过的计划可以安全重试：策略已等于计划目标，且视图当前一致。
		if retry, rerr := readingEnableApplied(ctx, root, args, plan); rerr == nil && retry {
			return map[string]any{"status": "unchanged", "checkpoint_ref": plan.CheckpointRef, "approval_created": false}, nil
		}
		changed := []string{}
		for ref, before := range plan.Observed {
			if now, ok := fresh.Observed[ref]; !ok || now != before {
				changed = append(changed, ref)
			}
		}
		for ref := range fresh.Observed {
			if _, ok := plan.Observed[ref]; !ok {
				changed = append(changed, ref)
			}
		}
		sort.Strings(changed)
		return nil, domain.Fail("READING_ENABLE_PLAN_STALE", "阅读启用计划绑定的输入已变化，请重新 plan-enable；变化: "+joinFirst(changed, 5))
	}
	if len(render.Changes) == 0 {
		return map[string]any{"status": "unchanged", "checkpoint_ref": plan.CheckpointRef, "approval_created": false}, nil
	}
	tx, err := readingCommit(ctx, root, args, s, render, plan.CheckpointRef, readingEnableKind)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	for _, c := range render.Changes {
		if c.Ref != reading.PolicyRef {
			changed = append(changed, c.Ref)
		}
	}
	return map[string]any{"status": "enabled", "checkpoint_ref": plan.CheckpointRef, "policy_after": plan.PolicyAfter, "reading_update": map[string]any{"status": "rendered", "checkpoint_ref": plan.CheckpointRef, "changed_refs": changed, "diagnostics": []any{}}, "transaction": tx, "approval_created": false}, nil
}

// readingEnableApplied 判断计划是否已被应用：策略已等于计划目标且视图当前一致。
func readingEnableApplied(ctx context.Context, root string, args map[string]string, plan readingEnablePlan) (bool, error) {
	s := kernel.NewSemanticSession(ctx, root, args)
	if err := readingSessionReady(s); err != nil {
		return false, err
	}
	policy, present, err := reading.LoadPolicy(s)
	if err != nil || !present {
		return false, err
	}
	canon := func(v any) []byte {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var generic any
		if json.Unmarshal(raw, &generic) != nil {
			return nil
		}
		out, _ := json.Marshal(generic)
		return out
	}
	want, have := canon(plan.PolicyAfter), canon(policy)
	if want == nil || !bytes.Equal(want, have) {
		return false, nil
	}
	report, err := reading.CheckViews(s, plan.CheckpointRef, false)
	return err == nil && report["status"] == "current", nil
}

func joinFirst(items []string, n int) string {
	if len(items) > n {
		items = items[:n]
	}
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}
