package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const ciConfigRef = ".template-spec/process/project-ci.yaml"
const nativeCIConfigRef = ".template-spec/process/project-ci-native.json"
const nativeCIReceiptRef = ".template-spec/process/project-ci-native-install.json"
const nativeCIWorkflowRef = ".github/workflows/yss-governance-native.yml"

var unportedProjectChecks = []string{"checkpoint-boundary-and-deep-verification", "digital-human-task-role-and-contract", "approval-ownership-and-current-context", "lifecycle-next-route", "slice-contract-approval", "git-base-deletion-protection"}

func governanceScope(v *view) ([]string, map[string]any, error) {
	if err := projectIdentity(v); err != nil {
		return nil, nil, err
	}
	layout, err := viewWorkLayout(v)
	if err != nil {
		return nil, nil, err
	}
	config := map[string]any{"schema_version": json.Number("1"), "provider": "github", "branch": "main", "additional_paths": []any{}}
	d, err := v.watch(ciConfigRef)
	if err != nil {
		return nil, nil, err
	}
	if d.Type == "file" {
		config, err = v.document(ciConfigRef)
		if err != nil {
			return nil, nil, err
		}
	}
	n, ok := integer(config["schema_version"])
	paths, valid := config["additional_paths"].([]any)
	if !ok || n != 1 || text(config["provider"]) != "github" || !valid {
		return nil, nil, domain.Fail("CI_CONFIG", "project-ci 配置版本、provider 或 additional_paths 无效")
	}
	roots := []string{layout.Root}
	for _, p := range paths {
		s, ok := p.(string)
		if !ok {
			return nil, nil, domain.Fail("CI_CONFIG", "additional_paths 必须是字符串数组")
		}
		roots = append(roots, s)
	}
	roots = uniqueStrings(roots)
	// A scoped native installation extends discovery only for its explicitly registered roots.
	d, err = v.watch(nativeCIConfigRef)
	if err != nil {
		return nil, nil, err
	}
	if d.Type == "file" {
		c, err := v.document(nativeCIConfigRef)
		if err != nil {
			return nil, nil, err
		}
		n, valid := integer(c["schema_version"])
		paths, list := c["roots"].([]any)
		if !valid || n != 1 || !list || text(c["kind"]) != "project-ci-native-scope" || text(c["scope"]) != "native-go" || c["full_project_governance"] != false {
			return nil, nil, domain.Fail("CI_CONFIG", "未知原生 CI 范围配置")
		}
		for _, p := range paths {
			s, ok := p.(string)
			if !ok {
				return nil, nil, domain.Fail("CI_CONFIG", "原生 CI roots 必须是字符串数组")
			}
			roots = append(roots, s)
		}
		roots = uniqueStrings(roots)
	}
	for _, ref := range roots {
		if _, err = safefs.Path(v.root, ref); err != nil {
			return nil, nil, err
		}
	}
	return roots, config, nil
}
func uniqueStrings(values []string) []string {
	set := map[string]bool{}
	for _, s := range values {
		set[s] = true
	}
	out := []string{}
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
func governanceFiles(root string, roots []string) ([]string, error) {
	files := []string{}
	seen := map[string]bool{}
	nodes := 0
	var visit func(string) error
	visit = func(ref string) error {
		nodes++
		if nodes > 20000 {
			return domain.Fail("CI_SCOPE", "治理范围超过 20000 项")
		}
		p, err := safefs.Path(root, ref)
		if err != nil {
			return err
		}
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if st.IsDir() {
			entries, err := os.ReadDir(p)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if err = visit(ref + "/" + e.Name()); err != nil {
					return err
				}
			}
			return nil
		}
		if !st.Mode().IsRegular() {
			return domain.Fail("CI_SCOPE", "治理范围含不支持的文件: "+ref)
		}
		if !seen[ref] {
			seen[ref] = true
			files = append(files, ref)
		}
		return nil
	}
	for _, ref := range roots {
		if err := visit(ref); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

var identityRefs = map[string]bool{"term_refs": true, "skill_refs": true, "principal_ref": true, "drafter_principal_ref": true, "requester_ref": true, "responder_ref": true, "runtime_instance_ref": true, "original_ref": true, "owner_ref": true}

func fileReference(s string) bool {
	return s != "" && !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`).MatchString(s) && !regexp.MustCompile(`^(?:gate|artifact|work-unit|stage|evidence|check|skill|runtime|role)\.[a-z0-9-]+$`).MatchString(s) && strings.ContainsAny(s, "./")
}
func assetReferences(value any, refs map[string]bool, bindings *[]Binding) {
	if list, ok := value.([]any); ok {
		for _, v := range list {
			assetReferences(v, refs, bindings)
		}
		return
	}
	m, ok := object(value)
	if !ok {
		return
	}
	if ref := text(m["ref"]); ref != "" {
		digest := first(text(m["digest"]), text(m["sha256"]))
		if digest != "" {
			*bindings = append(*bindings, Binding{ref, digest})
		}
	}
	for key, v := range m {
		if identityRefs[key] {
			continue
		}
		if key == "ref" || strings.HasSuffix(key, "_ref") {
			s := text(v)
			if fileReference(s) {
				refs[s] = true
			}
			if strings.HasSuffix(key, "_ref") {
				if hash := text(m[strings.TrimSuffix(key, "_ref")+"_digest"]); hash != "" {
					*bindings = append(*bindings, Binding{s, hash})
				}
			}
		}
		if strings.HasSuffix(key, "_refs") || key == "inputs" {
			if list, ok := v.([]any); ok {
				for _, x := range list {
					if s := text(x); fileReference(s) {
						refs[s] = true
					}
				}
			}
		}
		assetReferences(v, refs, bindings)
	}
}
func claimedAsset(m map[string]any) bool {
	if contains([]string{"approved", "completed", "ready-for-agent", "running", "paused-human-gate"}, text(m["status"])) || contains([]string{"active", "paused", "failed", "resolved"}, text(m["workflow_status"])) || text(m["result"]) == "completed" || text(m["decision"]) == "approved" {
		return true
	}
	for _, key := range []string{"gates", "artifacts"} {
		if set, ok := object(m[key]); ok {
			for _, raw := range set {
				if x, ok := object(raw); ok && text(x["status"]) == "approved" {
					return true
				}
			}
		}
	}
	return false
}
func nativeProjectCheck(root string) (map[string]any, error) {
	v := newView(root)
	if err := pendingOldTransaction(v); err != nil {
		return nil, err
	}
	roots, _, err := governanceScope(v)
	if err != nil {
		return nil, err
	}
	if _, err = v.read("CONTEXT.md"); err != nil {
		return nil, err
	}
	ctx, err := contextContract(root)
	if err != nil {
		return nil, err
	}
	if _, err = v.watch("CONTEXT.md"); err != nil {
		return nil, err
	}
	files, err := governanceFiles(root, roots)
	if err != nil {
		return nil, err
	}
	queue := append([]string{}, files...)
	seen := map[string]bool{}
	checks := []map[string]any{{"id": "context", "status": "passed", "snapshot": ctx}}
	unported := []map[string]string{}
	unstructured := []string{}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if len(seen) > 20000 {
			return nil, domain.Fail("CI_SCOPE", "引用闭包超过 20000 项")
		}
		b, err := v.read(ref)
		if err != nil {
			return nil, err
		}
		if !regexp.MustCompile(`\.(?:json|yaml|yml|md)$`).MatchString(ref) {
			continue
		}
		input := b
		if strings.HasSuffix(ref, ".md") {
			if !strings.HasPrefix(strings.ReplaceAll(string(b), "\r\n", "\n"), "---\n") {
				unstructured = append(unstructured, ref)
				if regexp.MustCompile(`(?m)^Status:\s*ready-for-agent\s*$`).Match(b) {
					unported = append(unported, map[string]string{"ref": ref, "check": "ticket-readiness-ownership"})
				}
				continue
			}
			input, _, err = frontmatter(b)
			if err != nil {
				return nil, err
			}
		}
		parsed, err := schema.Parse(input)
		if err != nil {
			return nil, domain.Fail("CI_INPUT", ref+": "+err.Error())
		}
		m, ok := object(parsed)
		if !ok {
			continue
		}
		isCP := regexp.MustCompile(`(?:^|/)checkpoint\.(?:json|yaml|yml)$`).MatchString(ref) || (text(m["repository_mode"]) != "" && text(m["stage"]) != "" && m["gates"] != nil)
		isTask := text(m["task_id"]) != "" && text(m["work_unit_id"]) != ""
		if isCP {
			if text(m["repository_mode"]) != "project-instance" {
				return nil, domain.Fail("CI_IDENTITY", "checkpoint 身份与项目不一致: "+ref)
			}
			stage, err := checkStage(v, m, ref)
			if err != nil {
				return nil, err
			}
			checks = append(checks, map[string]any{"id": "stage-work", "ref": ref, "result": stage})
			unported = append(unported, map[string]string{"ref": ref, "check": "checkpoint-boundary-and-deep-verification"})
		}
		if isTask {
			unported = append(unported, map[string]string{"ref": ref, "check": "digital-human-task-role-and-contract"})
		}
		if text(m["gate_id"]) != "" && text(m["decision"]) == "approved" {
			unported = append(unported, map[string]string{"ref": ref, "check": "approval-ownership-and-current-context"})
		} else if claimedAsset(m) && !isCP && !isTask {
			unported = append(unported, map[string]string{"ref": ref, "check": "claimed-asset-specific-verification"})
		}
		refs := map[string]bool{}
		bindings := []Binding{}
		assetReferences(m, refs, &bindings)
		for target := range refs {
			if _, err = v.read(target); err != nil {
				return nil, domain.Fail("CI_REFERENCE", ref+": "+err.Error())
			}
			queue = append(queue, target)
		}
		for _, binding := range bindings {
			actual, err := v.bind(binding.Ref)
			if err != nil {
				return nil, err
			}
			if actual.Digest != "sha256:"+strings.TrimPrefix(binding.Digest, "sha256:") {
				return nil, domain.Fail("CI_EVIDENCE_DRIFT", "绑定摘要不一致: "+binding.Ref)
			}
		}
	}
	// The read-only check closes its input set and file inventory again before reporting.
	after, err := governanceFiles(root, roots)
	if err != nil {
		return nil, err
	}
	if strings.Join(after, "\x00") != strings.Join(files, "\x00") {
		return nil, domain.Fail("INPUT_DRIFT", "治理范围文件集合变化")
	}
	for ref := range v.observed {
		if _, err = v.watch(ref); err != nil {
			return nil, err
		}
	}
	return map[string]any{"schema_version": 1, "kind": "project-governance-native-scope-verification", "status": "scoped-passed", "scope": "native-go", "full_project_governance": false, "read_only": true, "approval_created": false, "execution_authorization": "not-evaluated", "checks": checks, "roots": roots, "files": files, "inputs": v.observed, "unported_checks": unported, "unstructured_assets": unstructured}, nil
}
func nativeWorkflow(branch, source string) []byte {
	// source and branch are validated tokens, so they cannot inject YAML or shell syntax.
	return []byte(fmt.Sprintf("# Managed by Go project-ci explicit plan/apply. Scope: Context, stage work, local references and digests.\nname: YSS Context and stage work\non:\n  pull_request:\n  push:\n    branches: [%q]\n  workflow_dispatch:\npermissions:\n  contents: read\njobs:\n  native-scope:\n    runs-on: ubuntu-latest\n    timeout-minutes: 15\n    steps:\n      - uses: actions/checkout@v4\n        with:\n          persist-credentials: false\n      - uses: actions/setup-go@v5\n        with:\n          go-version-file: %s/go.mod\n          cache: false\n      - name: Verify native implementation\n        run: go -C %s test ./internal/governance ./internal/schema ./internal/transaction\n      - name: Check Context and stage work only\n        shell: bash\n        run: go -C %s run ./cmd/yss project-ci check --root \"$GITHUB_WORKSPACE\" --scope native-go --json > \"$RUNNER_TEMP/yss-governance-native.json\"\n      - uses: actions/upload-artifact@v4\n        if: always()\n        with:\n          name: yss-governance-native-scope\n          path: ${{ runner.temp }}/yss-governance-native.json\n          if-no-files-found: error\n", branch, source, source, source))
}
func buildCIPlan(root string, args map[string]string) (WritePlan, error) {
	plan := WritePlan{SchemaVersion: 1, ProtocolVersion: 1, Kind: "project-ci-native-go-plan", Root: root, Action: "install", Scope: "native-go", Options: map[string]string{}, Observed: map[string]domain.Descriptor{}, Operations: []transaction.Operation{}}
	v := newView(root)
	if err := pendingOldTransaction(v); err != nil {
		return plan, err
	}
	roots, config, err := governanceScope(v)
	if err != nil {
		return plan, err
	}
	for _, ref := range []string{".template-spec/process/.project-ci-transaction.json", ".github/workflows/yss-governance.yml", ".template-spec/process/project-ci-install.json"} {
		d, err := v.watch(ref)
		if err != nil {
			return plan, err
		}
		if d.Type != "missing" {
			return plan, domain.Fail("UNPORTED", "存在旧 CI 安装或事务，不能冒充等价升级: "+ref)
		}
	}
	provider := first(args["provider"], "github")
	if provider != "github" {
		return plan, domain.Fail("UNPORTED", "原生 CI 当前只支持 github")
	}
	branch := first(args["branch"], text(config["branch"]), "main")
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`).MatchString(branch) || strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".lock") {
		return plan, domain.Fail("CI_CONFIG", "目标分支无效")
	}
	source := args["cli-source"]
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_/-]*$`).MatchString(source) {
		return plan, domain.Fail("CI_SOURCE", "--cli-source 必须指定安全的项目内 Go CLI 源码目录")
	}
	if _, err = safefs.Path(root, source); err != nil {
		return plan, err
	}
	mod, err := v.read(source + "/go.mod")
	if err != nil {
		return plan, err
	}
	if !regexp.MustCompile(`(?m)^module github\.com/iloveZzz/yss-cli\s*$`).Match(mod) || !regexp.MustCompile(`(?m)^go 1\.27(?:\.\d+)?\s*$`).Match(mod) {
		return plan, domain.Fail("CI_SOURCE", "CLI 源码 module/Go 版本不符合当前原生合同")
	}
	if _, err = v.read(source + "/cmd/yss/main.go"); err != nil {
		return plan, err
	}
	sourceFiles, err := governanceFiles(root, []string{source})
	if err != nil {
		return plan, err
	}
	for _, ref := range sourceFiles {
		if _, err = v.read(ref); err != nil {
			return plan, err
		}
	}
	if p := args["additional-path"]; p != "" {
		for _, ref := range strings.Split(p, ",") {
			if _, err = safefs.Path(root, ref); err != nil {
				return plan, err
			}
			roots = append(roots, ref)
		}
		roots = uniqueStrings(roots)
	}
	plan.Options = map[string]string{"scope": "native-go", "provider": provider, "branch": branch, "cli-source": source, "additional-path": args["additional-path"]}
	nativeConfig := map[string]any{"schema_version": 1, "kind": "project-ci-native-scope", "scope": "native-go", "full_project_governance": false, "provider": provider, "branch": branch, "roots": roots, "cli_source": source, "verification_status": "not-executed"}
	conf, err := jsonBytes(nativeConfig)
	if err != nil {
		return plan, err
	}
	outputs := map[string][]byte{nativeCIConfigRef: conf, nativeCIWorkflowRef: nativeWorkflow(branch, source)}
	owned := map[string]any(nil)
	d, err := v.watch(nativeCIReceiptRef)
	if err != nil {
		return plan, err
	}
	if d.Type == "file" {
		owned, err = v.document(nativeCIReceiptRef)
		if err != nil {
			return plan, err
		}
		n, ok := integer(owned["schema_version"])
		if !ok || n != 1 || text(owned["kind"]) != "project-ci-native-installation" {
			return plan, domain.Fail("CI_RECEIPT", "原生 CI 托管凭据无效")
		}
	}
	managed := map[string]string{}
	for ref, data := range outputs {
		d, err := v.watch(ref)
		if err != nil {
			return plan, err
		}
		expected := ""
		if m, ok := object(owned["managed"]); ok {
			expected = text(m[ref])
		}
		if (d.Type == "file" && (expected == "" || expected != "sha256:"+d.Digest)) || (d.Type == "missing" && expected != "") {
			return plan, domain.Fail("CI_CONFLICT", "已有人工文件或托管输出漂移，保留原内容: "+ref)
		}
		managed[ref] = "sha256:" + safefs.Digest(data)
		if err = v.set(ref, data); err != nil {
			return plan, err
		}
	}
	receipt, err := jsonBytes(map[string]any{"schema_version": 1, "kind": "project-ci-native-installation", "scope": "native-go", "full_project_governance": false, "managed": managed})
	if err != nil {
		return plan, err
	}
	if err = v.set(nativeCIReceiptRef, receipt); err != nil {
		return plan, err
	}
	plan.Observed = v.observed
	plan.Operations, err = v.ops()
	if err != nil {
		return plan, err
	}
	plan.PlanDigest, err = nativePlanDigest(plan)
	return plan, err
}
func projectCIRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	if (action == "check" || action == "verify") && args["scope"] != "native-go" {
		return fullProjectCIRun(ctx, action, root, args)
	}
	if args["scope"] != "native-go" {
		return nil, domain.Fail("UNPORTED", "完整 project-ci 深层门禁尚未迁移: "+strings.Join(unportedProjectChecks, ", ")+"；仅显式 --scope native-go 可校验 Context、stage work 和当前本地引用摘要")
	}
	for _, key := range []string{"base", "runtime-store", "recover"} {
		if args[key] != "" {
			return nil, domain.Fail("UNPORTED", "旧 CI 选项不能降级忽略: --"+key)
		}
	}
	if action == "verify" || action == "check" {
		return nativeProjectCheck(root)
	}
	if action == "transition" {
		return nativeStageTransition(root, args)
	}
	if action != "install" && action != "plan" && action != "apply" {
		return nil, domain.Fail("UNPORTED", "project-ci 动作尚未迁移: "+action)
	}
	if action == "apply" || args["apply"] == "true" {
		ref := args["plan-file"]
		if ref == "" {
			return nil, domain.Fail("PLAN_REQUIRED", "CI 写入必须消费 --plan-file")
		}
		v := newView(root)
		b, err := v.read(ref)
		if err != nil {
			return nil, err
		}
		if _, err = schema.Parse(b); err != nil {
			return nil, err
		}
		var plan WritePlan
		if err = json.Unmarshal(b, &plan); err != nil {
			return nil, err
		}
		if plan.Kind != "project-ci-native-go-plan" || plan.Root != root || plan.SchemaVersion != 1 || plan.ProtocolVersion != 1 || plan.Scope != "native-go" || plan.Action != "install" {
			return nil, domain.Fail("UNPORTED", "未知或旧 CI 计划不能由 Go 直接应用")
		}
		digest, err := nativePlanDigest(plan)
		if err != nil || digest != plan.PlanDigest {
			return nil, domain.Fail("PLAN", "CI 计划摘要不一致")
		}
		fresh, err := buildCIPlan(root, plan.Options)
		if err != nil {
			return nil, err
		}
		if fresh.PlanDigest != plan.PlanDigest {
			return nil, domain.Fail("INPUT_DRIFT", "CI 计划输入已变化")
		}
		tx, err := transaction.ApplyContextWithGuards(ctx, root, "project-ci-native-install", plan.Operations, plan.Observed)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": tx.Status, "transaction": tx, "scope": "native-go", "full_project_governance": false, "remote_changed": false, "verification_status": "not-executed"}, nil
	}
	return buildCIPlan(root, args)
}
func nativeStageTransition(root string, args map[string]string) (any, error) {
	ref := first(args["checkpoint"], args["file"], args["arg0"])
	v := newView(root)
	if err := pendingOldTransaction(v); err != nil {
		return nil, err
	}
	cp, err := v.document(ref)
	if err != nil {
		return nil, err
	}
	result, err := checkStage(v, cp, ref)
	if err != nil {
		return nil, err
	}
	t, err := asTracking(cp)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, domain.Fail("TRACKING_REQUIRED", "阶段条件核验需要既有 stage_tracking")
	}
	current := args["current-work-unit"]
	if current == "" {
		if trace, ok := object(cp["stage_trace"]); ok {
			current = text(trace["completed_work_unit"])
		}
	}
	next := first(args["next-work-unit"], text(cp["next_work_unit"]))
	if current == "" || next == "" {
		return nil, domain.Fail("TRACKING_TRANSITION", "需要明确当前/下一工作单元，不能推断阶段")
	}
	r, err := v.document(".template-spec/process/lifecycle-registry.yaml")
	if err != nil {
		return nil, err
	}
	for _, unit := range []string{current, next} {
		kind, _, ok := findRegistry(r, unit)
		if !ok || kind != "work_units" {
			return nil, domain.Fail("TRACKING_TRANSITION", "工作单元未登记: "+unit)
		}
	}
	units := map[string]string{}
	for unit, stage := range trackedUnits {
		units[unit] = stage
	}
	design, _, err := designProfile(v)
	if err != nil {
		return nil, err
	}
	if design {
		units["work-unit.business-ticket-formalization"] = "stage.ticket-formalization"
	}
	byUnit := map[string][]WorkItem{}
	for _, item := range t.Items {
		byUnit[item.WorkUnit] = append(byUnit[item.WorkUnit], item)
	}
	for _, item := range byUnit[current] {
		if item.Progress != "completed" && item.Progress != "cancelled" && item.Deferred == nil {
			return nil, domain.Fail("TRACKING_TRANSITION", "当前工作单元仍有未完成工作: "+item.ID)
		}
	}
	if units[current] != "" && len(byUnit[current]) == 0 {
		return nil, domain.Fail("TRACKING_TRANSITION", "当前工作单元缺少登记工作")
	}
	if units[next] != "" && len(byUnit[next]) == 0 {
		return nil, domain.Fail("TRACKING_TRANSITION", "下一工作单元缺少登记工作")
	}
	if units[current] != "" && units[current] != units[next] {
		for _, item := range t.Items {
			if item.Stage == units[current] && item.Progress != "completed" && item.Progress != "cancelled" && item.Deferred == nil {
				return nil, domain.Fail("TRACKING_TRANSITION", "当前阶段仍有未完成工作")
			}
		}
	}
	stale, _ := result["stale_item_ids"].([]string)
	for _, item := range byUnit[next] {
		if item.Progress == "cancelled" {
			continue
		}
		if contains(stale, item.ID) {
			return nil, domain.Fail("TRACKING_STALE", "下一工作项来源漂移")
		}
		for _, dep := range item.Dependencies {
			for _, other := range t.Items {
				if other.ID == dep && other.Progress != "completed" {
					return nil, domain.Fail("TRACKING_DEPENDENCY", "下一工作项前置工作未完成")
				}
			}
		}
	}
	return map[string]any{"status": "work-conditions-satisfied", "scope": "stage-work-only", "full_project_governance": false, "transition_allowed": "not-evaluated", "approval_created": false, "read_only": true, "current_work_unit": current, "next_work_unit": next, "unported_checks": unportedProjectChecks}, nil
}
