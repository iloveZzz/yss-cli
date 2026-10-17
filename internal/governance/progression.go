package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/contracts"
	"github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/governance/lifecycle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const progressionKind = "lifecycle-target"

var progressionTargets = []string{"spec-approved", "product-design-completed", "backend-deliverable", "business-accepted"}

type ProgressionConsumer = lifecycle.ProgressionConsumer
type ProgressionTarget = lifecycle.ProgressionTarget

func parseProgressionTarget(raw []byte) (ProgressionTarget, error) {
	var target ProgressionTarget
	if _, err := schema.Parse(raw); err != nil {
		return target, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&target); err != nil {
		return target, domain.Wrap("PROGRESSION_TARGET", err)
	}
	if target.SchemaVersion != 1 || target.Kind != "lifecycle-progression-target" || !kernel.SemHas(progressionTargets, target.Target) || strings.TrimSpace(target.IntentSource) == "" || target.Consumers == nil {
		return target, domain.Fail("PROGRESSION_TARGET", "推进目标版本、目标、来源意图或消费者列表非法")
	}
	seen := map[string]bool{}
	for _, c := range target.Consumers {
		if !kernel.SemHas([]string{"design", "backend"}, c.Profile) || seen[c.Profile] || !filepath.IsAbs(c.Root) || filepath.Clean(c.Root) != c.Root || !kernel.ContractPath(c.CheckpointRef) {
			return target, domain.Fail("PROGRESSION_TARGET", "消费者必须唯一且显式指定 Profile、绝对根和项目内 checkpoint")
		}
		seen[c.Profile] = true
	}
	return target, nil
}

func progressionScope(s *kernel.SemanticSession, target string) error {
	scope, err := s.ExecutionScope()
	if err != nil {
		return err
	}
	if scope != nil && !kernel.SemHas(progressionTargets[:3], target) {
		return s.Reject("EXECUTION_SCOPE", "推进目标超出后端职责上限")
	}
	if scope != nil {
		_, policy, e := kernel.ProgressionPolicy(s)
		if e != nil {
			return e
		}
		if !kernel.SemHas(kernel.SemMap(kernel.SemMap(policy["completion_policy"])[target])["allowed_execution_scopes"], "plan-to-backend") {
			return s.Reject("EXECUTION_SCOPE", "当前目标政策未准许已有后端职责范围")
		}
	}
	return nil
}

func buildProgressionPlan(ctx context.Context, root, cpRef, inputRef string) (WritePlan, error) {
	plan := WritePlan{SchemaVersion: 1, ProtocolVersion: 1, Kind: "lifecycle-target-go-plan", Root: root, Action: "target", CheckpointRef: cpRef, InputRef: inputRef, Options: map[string]string{}, Observed: map[string]domain.Descriptor{}, Operations: []transaction.Operation{}, Scope: "progression-intent-only"}
	s := kernel.NewSemanticSession(ctx, root, map[string]string{"checkpoint": cpRef})
	if err := s.RequireGovernanceWrite(); err != nil {
		return plan, err
	}
	if err := pendingOldTransaction(s.V); err != nil {
		return plan, err
	}
	if _, err := s.ContextContract(); err != nil {
		return plan, err
	}
	profile, policy, err := kernel.ProgressionPolicy(s)
	if err != nil {
		return plan, err
	}
	if profile != "spec" || !kernel.SemHas(policy["writer_profiles"], profile) {
		return plan, s.Reject("PROFILE_ROUTE", "仅 Spec 主控可设置功能推进目标")
	}
	configRef, cp, err := kernel.ProgressionLocation(s, cpRef)
	if err != nil {
		return plan, err
	}
	if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
		return plan, s.Reject("IDENTITY", "checkpoint Profile 与工程不符")
	}
	if inputRef == configRef {
		return plan, s.Reject("PROTECTED", "目标输入不能复用正在修改的配置")
	}
	raw, err := s.Bytes(inputRef)
	if err != nil {
		return plan, err
	}
	target, err := parseProgressionTarget(raw)
	if err != nil {
		return plan, err
	}
	if target.CheckpointRef != cpRef || target.FeatureID != kernel.Text(cp["feature_id"]) {
		return plan, s.Reject("PROGRESSION_BINDING", "目标输入与显式 checkpoint/feature 不匹配")
	}
	if len(kernel.SemMap(kernel.SemMap(policy["completion_policy"])[target.Target])) == 0 {
		return plan, s.Unavailable("CAPABILITY", "当前政策缺少目标退出条件")
	}
	if err = progressionScope(s, target.Target); err != nil {
		return plan, err
	}
	for _, c := range target.Consumers {
		if c.Root == root {
			return plan, s.Reject("PROGRESSION_BINDING", "独立消费者不能指向主控工程")
		}
		if _, err = safefs.Path(c.Root, c.CheckpointRef); err != nil {
			return plan, err
		}
		// Uncreated targets remain intent; existing targets must match explicitly.
		child := kernel.NewSemanticSession(ctx, c.Root, nil)
		for _, identityRef := range []string{".template-spec/process/harness-profile.yaml", domain.MetadataFile} {
			found, e := child.Exists(identityRef)
			if e != nil {
				return plan, e
			}
			if found {
				identity, e := child.Doc(identityRef)
				if e != nil {
					return plan, e
				}
				key := "profile_id"
				if identityRef == domain.MetadataFile {
					key = "profileId"
				}
				if identity[key] != domain.Profiles[c.Profile].ID {
					return plan, s.Reject("PROGRESSION_BINDING", "未来消费者已存在不匹配Profile身份")
				}
			}
		}
		present, e := child.Exists(c.CheckpointRef)
		if e != nil {
			return plan, e
		}
		if present {
			_, receiver, e := kernel.ProgressionLocation(child, c.CheckpointRef)
			if e != nil {
				return plan, e
			}
			p, e := child.Doc(".template-spec/process/harness-profile.yaml")
			if e != nil {
				return plan, e
			}
			if receiver["feature_id"] != target.FeatureID || p["profile_id"] != domain.Profiles[c.Profile].ID || receiver["profile_id"] != nil && receiver["profile_id"] != p["profile_id"] {
				return plan, s.Reject("PROGRESSION_BINDING", "消费者 Profile/feature 不匹配")
			}
		}
		s.Children = append(s.Children, child)
	}
	data, err := jsonBytes(target)
	if err != nil {
		return plan, err
	}
	if err = s.V.Set(configRef, data); err != nil {
		return plan, err
	}
	plan.Operations, err = s.V.Ops()
	if err != nil {
		return plan, err
	}
	plan.Options["config_ref"] = configRef
	if err = s.Finish(); err != nil {
		return plan, err
	}
	for _, input := range s.Inputs() {
		if input.Root == root {
			if input.Ref == ".yss/transactions" || strings.HasPrefix(input.Ref, ".yss/transactions/") {
				continue
			}
			plan.Observed[input.Ref] = input.Descriptor
		}
	}
	// Consumer observations remain bound to their own root for apply rechecks.
	external := []kernel.SemanticInput{}
	for _, input := range s.Inputs() {
		if input.Root != root {
			external = append(external, input)
		}
	}
	externalBytes, err := json.Marshal(external)
	if err != nil {
		return plan, err
	}
	plan.Options["external_inputs"] = string(externalBytes)
	scanBytes, err := json.Marshal(progressionScans(s, root, configRef))
	if err != nil {
		return plan, err
	}
	plan.Options["scan_inputs"] = string(scanBytes)
	plan.PlanDigest, err = nativePlanDigest(plan)
	return plan, err
}

func progressionTargetRun(ctx context.Context, root string, args map[string]string) (any, error) {
	if args["plan"] == "" && args["apply"] == "" && args["input"] == "" && args["out"] == "" && args["plan-file"] == "" {
		return progressionRead(ctx, root, args["checkpoint"])
	}
	if args["apply"] != "true" {
		if args["plan"] != "true" || args["plan-file"] != "" {
			return nil, domain.Fail("PLAN_REQUIRED", "推进目标需要 --plan --out；应用使用 --apply --plan-file")
		}
		plan, err := buildProgressionPlan(ctx, root, args["checkpoint"], args["input"])
		if err != nil {
			return nil, err
		}
		if args["out"] == "" {
			return nil, domain.Fail("PLAN_REQUIRED", "推进目标计划需要 --out 新文件")
		}
		abs, err := filepath.Abs(args["out"])
		if err != nil {
			return nil, err
		}
		if abs == filepath.Join(root, plan.Options["config_ref"]) {
			return nil, domain.Fail("PROTECTED", "计划不能占用目标配置路径")
		}
		if err = handoff.SaveBaselineJSON(abs, plan); err != nil {
			return nil, err
		}
		return plan, nil
	}
	if args["plan-file"] == "" || args["plan"] != "" || args["checkpoint"] != "" || args["input"] != "" || args["out"] != "" {
		return nil, domain.Fail("ARGUMENT", "应用只接受原保存计划")
	}
	raw, err := os.ReadFile(args["plan-file"])
	if err != nil {
		return nil, err
	}
	if _, err = schema.Parse(raw); err != nil {
		return nil, err
	}
	var plan WritePlan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&plan); err != nil {
		return nil, domain.Wrap("PLAN", err)
	}
	digest, e := nativePlanDigest(plan)
	if e != nil || plan.Kind != "lifecycle-target-go-plan" || plan.SchemaVersion != 1 || plan.ProtocolVersion != 1 || plan.Root != root || digest != plan.PlanDigest || plan.ApprovalCreated || plan.Scope != "progression-intent-only" {
		return nil, domain.Fail("PLAN", "推进目标计划身份或摘要非法")
	}
	if err = validateProgressionPlanOperations(plan); err != nil {
		return nil, err
	}
	fresh, err := buildProgressionPlan(ctx, root, plan.CheckpointRef, plan.InputRef)
	if err != nil {
		return nil, err
	}
	if fresh.PlanDigest != plan.PlanDigest {
		// A successfully applied plan may be retried, with every other input current.
		config := fresh.Options["config_ref"]
		same := len(plan.Operations) == 1 && len(fresh.Operations) == 0 && plan.Operations[0].Path == config && !plan.Operations[0].Delete && !plan.Operations[0].Directory
		current, readErr := os.ReadFile(filepath.Join(root, config))
		same = same && readErr == nil && bytes.Equal(current, plan.Operations[0].Data) && len(plan.Observed) == len(fresh.Observed) && contracts.ContractSame(plan.Options, fresh.Options)
		for ref, before := range plan.Observed {
			if ref != config && fresh.Observed[ref] != before {
				same = false
			}
		}
		if same {
			return map[string]any{"status": "unchanged", "scope": "progression-intent-only", "approval_created": false, "ready_for_agent_created": false}, nil
		}
		return nil, domain.Fail("INPUT_DRIFT", "推进目标计划输入或内容已变化；重新保存计划")
	}
	paths := []string{}
	for _, op := range fresh.Operations {
		paths = append(paths, op.Path)
	}
	if err = ValidateProgressionTransaction(root, "spec", paths); err != nil {
		return nil, err
	}
	tx, err := transaction.ApplyContextWithValidation(ctx, root, progressionKind, fresh.Operations, fresh.Observed, nil, func() error { return validateProgressionInputs(ctx, root, fresh) })
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": tx.Status, "transaction": tx, "scope": "progression-intent-only", "config_ref": fresh.Options["config_ref"], "approval_created": false, "ready_for_agent_created": false, "execution_authorization": "not-evaluated"}, nil
}

func validateProgressionInputs(ctx context.Context, root string, plan WritePlan) error {
	var inputs []kernel.SemanticInput
	if e := json.Unmarshal([]byte(plan.Options["external_inputs"]), &inputs); e != nil {
		return e
	}
	for _, input := range inputs {
		now, e := safefs.Describe(input.Root, input.Ref)
		if e != nil {
			return e
		}
		if now != input.Descriptor {
			return domain.Fail("INPUT_DRIFT", "写入前消费者输入变化")
		}
	}
	var scans []progressionScanInput
	if e := json.Unmarshal([]byte(plan.Options["scan_inputs"]), &scans); e != nil {
		return e
	}
	for _, scan := range scans {
		session := kernel.NewSemanticSession(ctx, scan.Root, nil)
		files, e := session.ScanFresh(scan.Ref, false)
		if e != nil {
			return e
		}
		files = progressionScanFiles(files, scan.Root, root, plan.Options["config_ref"])
		if !kernel.EqualStrings(files, scan.Files) {
			return domain.Fail("INPUT_DRIFT", "写入前功能登记文件集合变化")
		}
	}
	return nil
}

type progressionScanInput struct {
	Root  string   `json:"root"`
	Ref   string   `json:"ref"`
	Files []string `json:"files"`
}

func progressionScanFiles(files []string, root, writerRoot, configRef string) []string {
	filtered := []string{}
	for _, ref := range files {
		if root != writerRoot || ref != "file:"+configRef {
			filtered = append(filtered, ref)
		}
	}
	return filtered
}

func progressionScans(s *kernel.SemanticSession, writerRoot, configRef string) []progressionScanInput {
	rows := []progressionScanInput{}
	for ref, files := range s.Scans {
		// The transaction manager creates its own journal after compilation;
		// it owns pending-operation exclusion under the project lock.
		if s.Root == writerRoot && ref == ".yss/transactions" {
			continue
		}
		rows = append(rows, progressionScanInput{s.Root, ref, progressionScanFiles(files, s.Root, writerRoot, configRef)})
	}
	for _, child := range s.Children {
		rows = append(rows, progressionScans(child, writerRoot, configRef)...)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Root == rows[j].Root {
			return rows[i].Ref < rows[j].Ref
		}
		return rows[i].Root < rows[j].Root
	})
	return rows
}

func validateProgressionPlanOperations(plan WritePlan) error {
	if len(plan.Operations) > 1 {
		return domain.Fail("PLAN", "目标计划仅可修改单个意图文件")
	}
	for _, op := range plan.Operations {
		before := plan.Observed[op.Path]
		mode := uint32(0644)
		if before.Type == "file" {
			mode = before.Mode
		}
		validBefore := op.Before == nil && before.Type == "missing" || op.Before != nil && *op.Before == before && before.Type == "file"
		if op.Path != plan.Options["config_ref"] || op.Delete || op.Directory || op.Mode != mode || !validBefore {
			return domain.Fail("PLAN", "目标计划操作字段被篡改或越界")
		}
	}
	return nil
}

func ValidateProgressionTransaction(root, profile string, paths []string) error {
	if profile != "spec" || len(paths) > 1 {
		return domain.Fail("IDENTITY", "推进目标事务只允许 Spec 的单功能意图配置")
	}
	for _, ref := range paths {
		if path.Base(ref) != kernel.ProgressionFile {
			return domain.Fail("PROTECTED", "推进目标事务越界")
		}
		s := kernel.NewSemanticSession(context.Background(), root, nil)
		meta, err := kernel.ContractTicketMetadata(s, path.Join(path.Dir(ref), "map.md"))
		if err != nil {
			return err
		}
		want, _, err := kernel.ProgressionLocation(s, kernel.Text(meta["checkpoint_ref"]))
		if err != nil {
			return err
		}
		if ref != want {
			return domain.Fail("PROTECTED", "推进目标路径不匹配登记功能")
		}
	}
	return nil
}
