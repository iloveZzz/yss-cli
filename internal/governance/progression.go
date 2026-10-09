package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
)

const progressionFile = "progression-target.json"
const progressionKind = "lifecycle-target"

var progressionTargets = []string{"spec-approved", "product-design-completed", "backend-deliverable", "frontend-accepted", "business-accepted"}

type ProgressionConsumer struct {
	Profile       string `json:"profile"`
	Root          string `json:"root"`
	CheckpointRef string `json:"checkpoint_ref"`
}

type ProgressionTarget struct {
	SchemaVersion int                   `json:"schema_version"`
	Kind          string                `json:"kind"`
	FeatureID     string                `json:"feature_id"`
	CheckpointRef string                `json:"checkpoint_ref"`
	Target        string                `json:"target"`
	IntentSource  string                `json:"intent_source"`
	Consumers     []ProgressionConsumer `json:"consumers"`
}

func progressionPolicy(s *semanticSession) (string, map[string]any, error) {
	if err := s.authorities(); err != nil {
		return "", nil, err
	}
	profileDoc, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return "", nil, err
	}
	profile := ""
	for name, known := range domain.Profiles {
		if profileDoc["profile_id"] == known.ID {
			profile = name
		}
	}
	if profile == "" {
		return "", nil, s.unavailable("IDENTITY", "未知推进目标 Profile")
	}
	contract, err := s.doc(guidanceContractRef(profile))
	if err != nil {
		return profile, nil, err
	}
	policy := semMap(contract["progression_target"])
	if contractN(policy["schema_version"]) != 1 || policy["config_file"] != progressionFile || !semHas(policy["required_capabilities"], "lifecycle-target-v1") {
		return profile, nil, s.unavailable("CAPABILITY", "当前实例不支持推进目标；先显式同步模板")
	}
	for _, capability := range semStrings(policy["required_capabilities"]) {
		if capability != "lifecycle-target-v1" {
			return profile, nil, s.unavailable("CAPABILITY", "未知推进目标能力："+capability)
		}
	}
	return profile, policy, nil
}

// The registered checkpoint selects the feature directory; a slug never does.
func progressionLocation(s *semanticSession, cpRef string) (string, map[string]any, error) {
	if !contractPath(cpRef) {
		return "", nil, s.reject("PATH", "推进目标需要项目内显式 checkpoint")
	}
	cp, err := s.doc(cpRef)
	if err != nil {
		return "", nil, err
	}
	if contractN(cp["schema_version"]) != 1 || cp["repository_mode"] != "project-instance" || !strings.HasPrefix(text(cp["feature_id"]), "feature.") || len(text(cp["feature_id"])) <= len("feature.") {
		return "", nil, s.reject("IDENTITY", "checkpoint 缺少合法功能身份")
	}
	trackerBytes, err := s.bytes(worklayout.TrackerRef)
	if err != nil {
		return "", nil, err
	}
	layout, err := worklayout.FromDocument(s.root, trackerBytes)
	if err != nil {
		return "", nil, err
	}
	files, err := s.scan(layout.Root)
	if err != nil {
		return "", nil, err
	}
	found := ""
	for _, mapRef := range files {
		if path.Base(mapRef) != "map.md" || path.Dir(path.Dir(mapRef)) != layout.Root {
			continue
		}
		meta, e := contractTicketMetadata(s, mapRef)
		if e != nil {
			return "", nil, e
		}
		registered := text(meta["checkpoint_ref"])
		if registered == "" {
			continue
		}
		other, e := s.doc(registered)
		if e != nil {
			return "", nil, e
		}
		if other["feature_id"] != cp["feature_id"] {
			continue
		}
		if registered != cpRef || found != "" {
			return "", nil, s.reject("PROFILE_INPUT_AMBIGUOUS", "同一功能未唯一登记当前 checkpoint")
		}
		found = path.Join(path.Dir(mapRef), progressionFile)
	}
	if found == "" {
		return "", nil, s.reject("PROGRESSION_BINDING", "显式 checkpoint 尚未登记到 Tracker 功能包")
	}
	return found, cp, nil
}

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
	if target.SchemaVersion != 1 || target.Kind != "lifecycle-progression-target" || !semHas(progressionTargets, target.Target) || strings.TrimSpace(target.IntentSource) == "" || target.Consumers == nil {
		return target, domain.Fail("PROGRESSION_TARGET", "推进目标版本、目标、来源意图或消费者列表非法")
	}
	seen := map[string]bool{}
	for _, c := range target.Consumers {
		if !semHas([]string{"design", "backend", "frontend"}, c.Profile) || seen[c.Profile] || !filepath.IsAbs(c.Root) || filepath.Clean(c.Root) != c.Root || !contractPath(c.CheckpointRef) {
			return target, domain.Fail("PROGRESSION_TARGET", "消费者必须唯一且显式指定 Profile、绝对根和项目内 checkpoint")
		}
		seen[c.Profile] = true
	}
	return target, nil
}

func progressionScope(s *semanticSession, target string) error {
	scope, err := s.executionScope()
	if err != nil {
		return err
	}
	if scope != nil && !semHas(progressionTargets[:3], target) {
		return s.reject("EXECUTION_SCOPE", "推进目标超出后端职责上限")
	}
	if scope != nil {
		_, policy, e := progressionPolicy(s)
		if e != nil {
			return e
		}
		if !semHas(semMap(semMap(policy["completion_policy"])[target])["allowed_execution_scopes"], "plan-to-backend") {
			return s.reject("EXECUTION_SCOPE", "当前目标政策未准许已有后端职责范围")
		}
	}
	return nil
}

func buildProgressionPlan(ctx context.Context, root, cpRef, inputRef string) (WritePlan, error) {
	plan := WritePlan{SchemaVersion: 1, ProtocolVersion: 1, Kind: "lifecycle-target-go-plan", Root: root, Action: "target", CheckpointRef: cpRef, InputRef: inputRef, Options: map[string]string{}, Observed: map[string]domain.Descriptor{}, Operations: []transaction.Operation{}, Scope: "progression-intent-only"}
	s := newSemanticSession(ctx, root, map[string]string{"checkpoint": cpRef})
	if err := projectIdentity(s.v); err != nil {
		return plan, err
	}
	if err := pendingOldTransaction(s.v); err != nil {
		return plan, err
	}
	if _, err := s.contextContract(); err != nil {
		return plan, err
	}
	profile, policy, err := progressionPolicy(s)
	if err != nil {
		return plan, err
	}
	if profile != "spec" || !semHas(policy["writer_profiles"], profile) {
		return plan, s.reject("PROFILE_ROUTE", "仅 Spec 主控可设置功能推进目标")
	}
	configRef, cp, err := progressionLocation(s, cpRef)
	if err != nil {
		return plan, err
	}
	if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
		return plan, s.reject("IDENTITY", "checkpoint Profile 与工程不符")
	}
	if inputRef == configRef {
		return plan, s.reject("PROTECTED", "目标输入不能复用正在修改的配置")
	}
	raw, err := s.bytes(inputRef)
	if err != nil {
		return plan, err
	}
	target, err := parseProgressionTarget(raw)
	if err != nil {
		return plan, err
	}
	if target.CheckpointRef != cpRef || target.FeatureID != text(cp["feature_id"]) {
		return plan, s.reject("PROGRESSION_BINDING", "目标输入与显式 checkpoint/feature 不匹配")
	}
	if len(semMap(semMap(policy["completion_policy"])[target.Target])) == 0 {
		return plan, s.unavailable("CAPABILITY", "当前政策缺少目标退出条件")
	}
	if err = progressionScope(s, target.Target); err != nil {
		return plan, err
	}
	for _, c := range target.Consumers {
		if c.Root == root {
			return plan, s.reject("PROGRESSION_BINDING", "独立消费者不能指向主控工程")
		}
		if _, err = safefs.Path(c.Root, c.CheckpointRef); err != nil {
			return plan, err
		}
		// Uncreated targets remain intent; existing targets must match explicitly.
		child := newSemanticSession(ctx, c.Root, nil)
		for _, identityRef := range []string{".template-spec/process/harness-profile.yaml", domain.MetadataFile} {
			found, e := child.exists(identityRef)
			if e != nil {
				return plan, e
			}
			if found {
				identity, e := child.doc(identityRef)
				if e != nil {
					return plan, e
				}
				key := "profile_id"
				if identityRef == domain.MetadataFile {
					key = "profileId"
				}
				if identity[key] != domain.Profiles[c.Profile].ID {
					return plan, s.reject("PROGRESSION_BINDING", "未来消费者已存在不匹配Profile身份")
				}
			}
		}
		present, e := child.exists(c.CheckpointRef)
		if e != nil {
			return plan, e
		}
		if present {
			_, receiver, e := progressionLocation(child, c.CheckpointRef)
			if e != nil {
				return plan, e
			}
			p, e := child.doc(".template-spec/process/harness-profile.yaml")
			if e != nil {
				return plan, e
			}
			if receiver["feature_id"] != target.FeatureID || p["profile_id"] != domain.Profiles[c.Profile].ID || receiver["profile_id"] != nil && receiver["profile_id"] != p["profile_id"] {
				return plan, s.reject("PROGRESSION_BINDING", "消费者 Profile/feature 不匹配")
			}
		}
		s.children = append(s.children, child)
	}
	data, err := jsonBytes(target)
	if err != nil {
		return plan, err
	}
	if err = s.v.set(configRef, data); err != nil {
		return plan, err
	}
	plan.Operations, err = s.v.ops()
	if err != nil {
		return plan, err
	}
	plan.Options["config_ref"] = configRef
	if err = s.finish(); err != nil {
		return plan, err
	}
	for _, input := range s.inputs() {
		if input.Root == root {
			if input.Ref == ".yss/transactions" || strings.HasPrefix(input.Ref, ".yss/transactions/") {
				continue
			}
			plan.Observed[input.Ref] = input.Descriptor
		}
	}
	// Consumer observations remain bound to their own root for apply rechecks.
	external := []SemanticInput{}
	for _, input := range s.inputs() {
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
		if err = saveBaselineJSON(abs, plan); err != nil {
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
		same = same && readErr == nil && bytes.Equal(current, plan.Operations[0].Data) && len(plan.Observed) == len(fresh.Observed) && contractSame(plan.Options, fresh.Options)
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
	var inputs []SemanticInput
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
		session := newSemanticSession(ctx, scan.Root, nil)
		files, e := session.scanFresh(scan.Ref, false)
		if e != nil {
			return e
		}
		files = progressionScanFiles(files, scan.Root, root, plan.Options["config_ref"])
		if !equalStrings(files, scan.Files) {
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

func progressionScans(s *semanticSession, writerRoot, configRef string) []progressionScanInput {
	rows := []progressionScanInput{}
	for ref, files := range s.scans {
		// The transaction manager creates its own journal after compilation;
		// it owns pending-operation exclusion under the project lock.
		if s.root == writerRoot && ref == ".yss/transactions" {
			continue
		}
		rows = append(rows, progressionScanInput{s.root, ref, progressionScanFiles(files, s.root, writerRoot, configRef)})
	}
	for _, child := range s.children {
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
		if path.Base(ref) != progressionFile {
			return domain.Fail("PROTECTED", "推进目标事务越界")
		}
		s := newSemanticSession(context.Background(), root, nil)
		meta, err := contractTicketMetadata(s, path.Join(path.Dir(ref), "map.md"))
		if err != nil {
			return err
		}
		want, _, err := progressionLocation(s, text(meta["checkpoint_ref"]))
		if err != nil {
			return err
		}
		if ref != want {
			return domain.Fail("PROTECTED", "推进目标路径不匹配登记功能")
		}
	}
	return nil
}
