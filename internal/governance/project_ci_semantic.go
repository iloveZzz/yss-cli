package governance

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

var ciStableID = regexp.MustCompile(`^(gate|artifact|work-unit|stage|evidence|check|skill|runtime|role|module|slice|test-seam|rule|scenario|route|operation|business-ticket|context)\.[a-z0-9][a-z0-9.-]*$`)
var ciURI = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
var ciCheckpointName = regexp.MustCompile(`(^|/)checkpoint\.(yaml|yml|json)$`)
var ciReadyTicket = regexp.MustCompile(`(?:^|\n)Status:\s*ready-for-agent\s*(?:\n|$)`)
var ciCommit = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
var ciSourceDeliveryEvidence = regexp.MustCompile(`^docs/handoffs/[^/]+/v[1-9][0-9]*/source-delivery-record\.json$`)
var ciCapturedSource = regexp.MustCompile(`^docs/(?:handoffs|backend-deliveries|spec-baselines)/[^/]+/v[1-9][0-9]*/package/`)

func ciEvidenceReceipt(ref string) string {
	if ciSourceDeliveryEvidence.MatchString(ref) {
		return path.Join(path.Dir(ref), "import-receipt.json")
	}
	return ""
}

func init() {
	registerSemanticValidator("handoff-source-delivery-evidence", func(s *semanticSession, ref string, _ map[string]string) error {
		receiptRef := ciEvidenceReceipt(ref)
		if receiptRef == "" {
			return s.reject("HANDOFF_RECEIPT", "来源交付证据缺少合法接收记录路径")
		}
		receipt, err := s.doc(receiptRef)
		if err != nil {
			return err
		}
		if receipt["source_delivery_record_ref"] != ref || contractN(receipt["schema_version"]) != 3 {
			return s.reject("HANDOFF_RECEIPT", "来源交付证据未由receipt v3绑定")
		}
		return contractReceipt(s, receipt)
	})
	registerSemanticValidator("handoff-import-receipt", func(s *semanticSession, ref string, _ map[string]string) error {
		receipt, err := s.doc(ref)
		if err != nil {
			return err
		}
		if ref != path.Join("docs/handoffs", text(receipt["bundle_id"]), text(receipt["version"]), "import-receipt.json") {
			return s.reject("HANDOFF_RECEIPT", "导入收据路径非法")
		}
		if err = contractReceipt(s, receipt); err != nil {
			return err
		}
		for _, capability := range semStrings(receipt["selected_consumer_capabilities"]) {
			if err = contractReceiptConsumer(s, receipt, capability); err != nil {
				return err
			}
		}
		return nil
	})
	registerSemanticValidator("spec-baseline-package", func(s *semanticSession, ref string, _ map[string]string) error {
		_, _, err := openSpecBaseline(s, ref)
		return err
	})
	registerSemanticValidator("spec-baseline-import-receipt", func(s *semanticSession, ref string, _ map[string]string) error {
		_, _, err := verifySpecBaselineReceipt(s, ref, false)
		return err
	})
}

func ciFileRef(v any) bool {
	r, ok := v.(string)
	return ok && !ciURI.MatchString(r) && !ciStableID.MatchString(r) && strings.ContainsAny(r, "./")
}
func ciReferences(value any) []string {
	found := map[string]bool{}
	skip := map[string]bool{}
	for _, k := range []string{"term_ref", "term_refs", "skill_refs", "principal_ref", "drafter_principal_ref", "requester_ref", "responder_ref", "runtime_instance_ref", "original_ref", "owner_ref"} {
		skip[k] = true
	}
	var visit func(any)
	visit = func(v any) {
		if m, ok := object(v); ok {
			for k, item := range m {
				if skip[k] {
					continue
				}
				if (k == "ref" || strings.HasSuffix(k, "_ref")) && ciFileRef(item) {
					found[text(item)] = true
				}
				if strings.HasSuffix(k, "_refs") || k == "inputs" {
					for _, row := range semList(item) {
						if ciFileRef(row) {
							found[text(row)] = true
						}
					}
				}
				visit(item)
			}
		} else {
			for _, row := range semList(v) {
				visit(row)
			}
		}
	}
	visit(value)
	out := []string{}
	for r := range found {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
func ciBindings(value any) []map[string]any {
	out := []map[string]any{}
	var visit func(any)
	visit = func(v any) {
		if m, ok := object(v); ok {
			if text(m["ref"]) != "" && (m["digest"] != nil || m["sha256"] != nil) {
				hash := m["digest"]
				if hash == nil {
					hash = m["sha256"]
				}
				out = append(out, map[string]any{"ref": m["ref"], "digest": hash})
			}
			for k, item := range m {
				if strings.HasSuffix(k, "_ref") && text(item) != "" && m[strings.TrimSuffix(k, "_ref")+"_digest"] != nil {
					out = append(out, map[string]any{"ref": item, "digest": m[strings.TrimSuffix(k, "_ref")+"_digest"]})
				}
				visit(item)
			}
		} else {
			for _, row := range semList(v) {
				visit(row)
			}
		}
	}
	visit(value)
	return out
}
func ciClaimed(v map[string]any) bool {
	if v == nil {
		return false
	}
	if semHas([]string{"approved", "completed", "verified", "implemented", "accepted", "ready-for-agent", "running", "paused-human-gate"}, text(v["status"])) || semHas([]string{"active", "paused", "failed", "resolved"}, text(v["workflow_status"])) || semHas([]string{"completed", "verified"}, text(v["result"])) || v["decision"] == "approved" {
		return true
	}
	for _, bucket := range []string{"gates", "artifacts"} {
		for _, row := range semMap(v[bucket]) {
			if semMap(row)["status"] == "approved" {
				return true
			}
		}
	}
	return false
}

// Recognize protocol families, then let their native validator enforce version,
// approval and currentness. Recognition never constitutes an acceptance check.
func ciDomainKind(v map[string]any) string {
	if v["kind"] == "spec-baseline-import" {
		return "spec-baseline-import-receipt"
	}
	if v["bundle_id"] != nil && v["package_ref"] != nil && v["target_context_digest"] != nil {
		return "handoff-import-receipt"
	}
	if v["handoff_id"] != nil && v["handoff_version"] != nil && v["package_export"] != nil {
		return "handoff-source"
	}
	if text(v["kind"]) == "strategic-handoff-delivery-v1" {
		return "strategic-delivery"
	}
	if v["slice_contract"] != nil || text(v["contract_id"]) != "" && v["work_units"] != nil {
		return "slice"
	}
	if v["generation_policy"] != nil && v["generator_skill"] != nil || v["scaffold_status"] != nil && v["delivery_role"] != nil {
		return "scaffold"
	}
	if v["execution_result"] != nil || v["consumed_contract"] != nil || semHas([]string{"template-verification-report", "frontend-scaffold-verification", "backend-contract", "backend-deployment", "strategic-delivery-verification-v1"}, text(v["kind"])) {
		return "verification"
	}
	if v["strategic_handoff"] != nil {
		if v["frontend_cases"] != nil || v["frontend_implementation_plan"] != nil || text(v["kind"]) == "frontend-implementation-plan" {
			return "handoff-consumption:frontend"
		}
		return "handoff-consumption:tactical"
	}
	return ""
}
func ciParse(bytes []byte, ref string) (map[string]any, error) {
	if strings.HasSuffix(ref, ".md") {
		if !strings.HasPrefix(string(bytes), "---\n") && !strings.HasPrefix(string(bytes), "---\r\n") {
			return nil, nil
		}
		var err error
		bytes, _, err = frontmatter(bytes)
		if err != nil {
			return nil, err
		}
	}
	value, err := schema.Parse(bytes)
	if err != nil {
		return nil, err
	}
	m, _ := object(value)
	return m, nil
}
func fullProjectCIRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	s := newSemanticSession(ctx, root, args)
	s.report.Kind = "project-governance-verification"
	s.report.Scope = "complete-governance"
	roots := []string{}
	files := []string{}
	checkpoints := []string{}
	tasks := []string{}
	unknown := []string{}
	missing := []string{}
	fail := func(ref string, err error) {
		s.diagnostic(ref, err)
		if semanticExit(err) == 2 {
			missing = append(missing, ref)
		}
	}
	run := func(kind, ref string, opts map[string]string) {
		if err := s.verify(kind, ref, opts); err != nil {
			fail(ref, err)
		}
	}
	setup := func() error {
		for key := range args {
			if !semHas([]string{"root", "profile", "json", "scope", "runtime-store", "base", "checkpoint", "task", "recover", "home", "run-dir", "tool-root", "template-checkout"}, key) {
				return s.unavailable("ARGUMENT", "完整治理检查不接受参数: --"+key)
			}
		}
		if args["scope"] != "" && args["scope"] != "full" {
			return s.unavailable("ARGUMENT", "未知治理范围；有限检查须显式 --scope native-go")
		}
		if args["runtime-store"] != "" && args["runtime-store"] != "off" {
			return s.unavailable("CAPABILITY", "完整治理本批仅支持 runtime-store off")
		}
		if err := s.authorities(); err != nil {
			return err
		}
		var err error
		roots, _, err = governanceScope(s.v)
		if err != nil {
			return s.unavailable("INPUT", err.Error())
		}
		for _, r := range roots {
			set, err := s.scan(r)
			if err != nil {
				return err
			}
			files = append(files, set...)
		}
		files = uniqueStrings(files)
		return nil
	}
	err := setup()
	if err != nil {
		fail(args["checkpoint"], err)
	} else {
		if _, err = s.contextContract(); err != nil {
			fail("CONTEXT.md", err)
		} else {
			s.report.Checks = append(s.report.Checks, SemanticCheck{ID: "context", SourceRef: "CONTEXT.md", Status: "passed"})
		}
		values := map[string]map[string]any{}
		claims := map[string]bool{}
		tickets := []string{}
		seen := map[string]bool{}
		strict := map[string]bool{}
		queue := []string{}
		packageRoots := []string{}
		for _, ref := range files {
			if filepath.Base(ref) != "manifest.json" {
				continue
			}
			bytes, e := s.bytes(ref)
			if e != nil {
				fail(ref, e)
				continue
			}
			value, e := ciParse(bytes, ref)
			if e == nil && value["bundle_id"] != nil && value["handoff_ref"] != nil && value["files"] != nil {
				prefix := filepath.ToSlash(filepath.Dir(ref))
				run("handoff-package", prefix, semanticOptions(args))
				packageRoots = append(packageRoots, prefix)
			} else if e == nil && value["kind"] == "spec-baseline" && value["baseline_id"] != nil && value["files"] != nil {
				prefix := filepath.ToSlash(filepath.Dir(ref))
				run("spec-baseline-package", prefix, nil)
				packageRoots = append(packageRoots, prefix)
			}
		}
		for _, ref := range files {
			if semHas([]string{".json", ".yaml", ".yml", ".md"}, filepath.Ext(ref)) {
				queue = append(queue, ref)
			}
		}
		if args["checkpoint"] != "" {
			queue = append(queue, args["checkpoint"])
			strict[args["checkpoint"]] = true
		}
		if args["task"] != "" {
			queue = append(queue, args["task"])
			strict[args["task"]] = true
		}
		for len(queue) > 0 {
			ref := queue[0]
			queue = queue[1:]
			inPackage := false
			for _, prefix := range packageRoots {
				inPackage = inPackage || strings.HasPrefix(ref, prefix+"/")
			}
			if inPackage {
				// The whole closed package has already been validated using its
				// captured source Context/rules, rather than receiver authorities.
				continue
			}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			bytes, e := s.bytes(ref)
			if e != nil {
				fail(ref, e)
				continue
			}
			value, e := ciParse(bytes, ref)
			if e != nil {
				fail(ref, s.unavailable("INPUT", e.Error()))
				continue
			}
			values[ref] = value
			if receiptRef := ciEvidenceReceipt(ref); receiptRef != "" {
				run("handoff-source-delivery-evidence", ref, nil)
				// This copied record's raw refs belong to its captured source.
				// Its receiver-side dependency is the receipt that binds it.
				values[ref] = map[string]any{"ref": receiptRef}
				if !strict[receiptRef] {
					strict[receiptRef] = true
					delete(seen, receiptRef)
					queue = append(queue, receiptRef)
				}
				continue
			}
			if prefix := text(value["package_ref"]); prefix != "" && !ciURI.MatchString(prefix) && !filepath.IsAbs(prefix) {
				if present, e := s.exists(filepath.ToSlash(filepath.Join(prefix, "manifest.json"))); e != nil {
					fail(ref, e)
				} else if present {
					kind := "handoff-package"
					if value["kind"] == "spec-baseline-import" {
						kind = "spec-baseline-package"
					}
					run(kind, prefix, semanticOptions(args))
					packageRoots = append(packageRoots, prefix)
				}
			}
			cp := ciCheckpointName.MatchString(ref) || value["repository_mode"] != nil && value["stage"] != nil && value["gates"] != nil
			task := text(value["task_id"]) != "" && text(value["work_unit_id"]) != "" && (value["contract"] != nil || semHas([]string{"active", "paused", "failed", "resolved"}, text(value["workflow_status"])))
			if cp {
				checkpoints = append(checkpoints, ref)
				run("checkpoint", ref, map[string]string{})
				if text(semMap(value["stage_trace"])["completed_work_unit"]) != "" {
					run("next-route", ref, map[string]string{})
				} else if semHas([]string{"running", "completed"}, text(value["status"])) && semHas([]string{"resume", "orchestrate"}, text(value["mode"])) {
					fail(ref, s.reject("TRANSITION_ORIGIN_REQUIRED", "当前流转缺少 completed_work_unit"))
				}
				if args["recover"] == "true" && (len(semList(value["blockers"])) > 0 || semHas([]string{"blocked", "stale"}, text(value["status"]))) {
					fail(ref, s.reject("RECOVERY_BLOCKED", "当前 checkpoint 存在阻塞"))
				}
			} else if task {
				tasks = append(tasks, ref)
				run("task", ref, map[string]string{})
				if args["recover"] == "true" {
					if value["workflow_status"] != "resolved" {
						fail(ref, s.reject("TASK_NOT_RESOLVED", "先核查原任务，禁止重复派发"))
					} else if len(semList(semMap(value["result"])["evidence_refs"])) == 0 && len(semList(value["verification_results"])) == 0 {
						fail(ref, s.reject("TASK_RESULT_REQUIRED", "resolved 任务缺少实际结果"))
					}
				}
			} else if ciClaimed(value) || ciDomainKind(value) != "" {
				claims[ref] = true
			} else {
				unknown = append(unknown, ref)
			}
			if ciReadyTicket.Match(bytes) {
				tickets = append(tickets, ref)
			}
			if cp || task || ciClaimed(value) || ciDomainKind(value) != "" || value["gate_id"] != nil || strict[ref] {
				for _, dependency := range ciReferences(value) {
					if semHas(packageRoots, dependency) {
						continue
					}
					if ciDomainKind(value) != "" && filepath.IsAbs(dependency) {
						// Domain validators bind external roots through approved
						// contracts; the generic project view must not adopt them.
						continue
					}
					if !strict[dependency] {
						strict[dependency] = true
						delete(seen, dependency)
					}
					if _, e = s.bytes(dependency); e != nil {
						fail(ref, e)
					} else if semHas([]string{".yaml", ".yml", ".json", ".md"}, filepath.Ext(dependency)) {
						queue = append(queue, dependency)
					}
				}
				for _, binding := range ciBindings(value) {
					if semHas(packageRoots, text(binding["ref"])) {
						continue
					}
					if ciDomainKind(value) != "" && filepath.IsAbs(text(binding["ref"])) {
						continue
					}
					if e = s.basis([]any{binding}); e != nil {
						fail(ref, e)
					}
				}
			}
		}
		if args["checkpoint"] != "" && !semHas(checkpoints, args["checkpoint"]) {
			fail(args["checkpoint"], s.reject("CHECKPOINT_IDENTITY", "指定引用不是 checkpoint"))
		}
		if args["task"] != "" && !semHas(tasks, args["task"]) {
			fail(args["task"], s.reject("TASK_IDENTITY", "指定引用不是任务包"))
		}
		actorGroups := map[string][]map[string]any{}
		for _, ref := range tasks {
			task := values[ref]
			contract := semMap(task["contract"])
			key := text(contract["contract_id"]) + "\x00" + text(contract["contract_version"])
			actorGroups[key] = append(actorGroups[key], task)
		}
		for _, group := range actorGroups {
			if e := validateTaskActorSetSemantic(s, group); e != nil {
				fail("task-actors", e)
			}
		}
		owner := map[string][]string{}
		for _, cpRef := range checkpoints {
			pending := []string{cpRef}
			visited := map[string]bool{}
			for len(pending) > 0 {
				ref := pending[0]
				pending = pending[1:]
				if visited[ref] {
					continue
				}
				visited[ref] = true
				for _, dep := range ciReferences(values[ref]) {
					owner[dep] = append(owner[dep], cpRef)
					pending = append(pending, dep)
				}
			}
		}
		approvalOwners, discoveryErr := s.ciApprovalOwners()
		if discoveryErr != nil {
			fail("approval-owners", discoveryErr)
		}
		for ref := range claims {
			value := values[ref]
			if value["gate_id"] != nil && value["decision"] == "approved" {
				candidates := approvalOwners[ref]
				candidates = uniqueStrings(candidates)
				if len(candidates) != 1 {
					fail(ref, s.reject("APPROVAL_CONTEXT_REQUIRED", fmt.Sprintf("批准记录需要唯一 checkpoint（找到%d个）", len(candidates))))
				} else {
					run("approval", ref, map[string]string{"checkpoint": candidates[0]})
				}
				continue
			}
			if len(owner[ref]) == 0 {
				fail(ref, s.reject("CLAIM_WITHOUT_CHECKPOINT", "已声明完成或批准的资产缺少 checkpoint 归属"))
				continue
			}
			kind := ciDomainKind(value)
			if kind == "" && (value["domain_strategy_id"] != nil || value["stage_decision_id"] != nil) {
				consumers := []string{}
				for candidate, doc := range values {
					if ciDomainKind(doc) == "handoff-source" && semHas(ciReferences(doc), ref) {
						consumers = append(consumers, candidate)
					}
				}
				if len(consumers) == 1 {
					run("handoff-source", consumers[0], semanticOptions(args))
					continue
				}
			}
			if kind != "" {
				owners := uniqueStrings(owner[ref])
				if len(owners) != 1 {
					fail(ref, s.reject("CLAIM_CONTEXT_REQUIRED", "领域资产需要唯一当前 checkpoint 归属"))
					continue
				}
				opts := semanticOptions(args)
				opts["checkpoint"] = owners[0]
				if kind == "verification" && text(value["kind"]) == "frontend-scaffold-verification" {
					binding := semMap(semMap(values[owners[0]]["artifacts"])["artifact.project-scaffold-contract"])
					if binding["status"] != "approved" || text(binding["ref"]) == "" {
						fail(ref, s.reject("VERIFICATION_CURRENT_REQUIRED", "完整 CI 的脚手架报告需要 checkpoint 当前批准合同"))
						continue
					}
					if _, e := contractBoundDoc(s, binding); e != nil {
						fail(ref, e)
						continue
					}
					opts["contract"] = text(binding["ref"])
				}
				if strings.HasPrefix(kind, "handoff-consumption:") {
					opts["consumer"] = strings.TrimPrefix(kind, "handoff-consumption:")
					kind = "handoff-consumption"
				}
				run(kind, ref, opts)
			} else {
				unknown = append(unknown, ref)
				fail(ref, s.unavailable("CAPABILITY", "当前声称批准或完成的资产缺少专属校验能力: "+ref))
			}
		}
		for _, ref := range tickets {
			if len(owner[ref]) == 0 {
				ready := false
				for _, taskRef := range tasks {
					if semHas(ciReferences(values[taskRef]), ref) {
						ready = true
					}
				}
				if !ready {
					fail(ref, s.reject("TICKET_READINESS_REQUIRED", "ready-for-agent Ticket 缺少当前 checkpoint/任务包证据"))
				}
			}
		}
		if args["base"] != "" {
			if e := s.ciBaseProtection(args["base"], roots, values, checkpoints); e != nil {
				fail("--base", e)
			}
		}
	}
	if final := s.finish(); final != nil {
		fail("final-input-check", final)
	}
	s.report.Inputs = s.inputs()
	s.report.GitInputs = s.gitBindings()
	s.report.Checks = s.collectedChecks()
	s.report.Coverage = map[string]any{"roots": roots, "files": files, "checkpoints": uniqueStrings(checkpoints), "tasks": uniqueStrings(tasks), "unrecognized": uniqueStrings(unknown), "missing_capabilities": uniqueStrings(missing), "base": args["base"], "runtime_store": "off", "dispatch": false}
	if s.report.Status != "passed" {
		exit := 1
		if s.report.Status == "error" {
			exit = 2
		}
		return s.report, &semanticFailure{cause: &domain.Error{Code: "PROJECT_CI_REJECTED", Message: "完整治理检查未通过；参见逐项诊断", Exit: exit}, report: s.report}
	}
	return s.report, nil
}

// Discovery covers the legacy owner search boundary independently of --checkpoint
// and configured CI roots. An out-of-scope second owner still makes approval ambiguous.
func (s *semanticSession) ciApprovalOwners() (map[string][]string, error) {
	owners := map[string][]string{}
	refs := []string{}
	layout, err := viewWorkLayout(s.v)
	if err != nil {
		return nil, err
	}
	for _, root := range uniqueStrings(append(append([]string{}, layout.ScanRoots...), "docs", ".yss")) {
		files, err := s.scan(root)
		if err != nil {
			return nil, err
		}
		refs = append(refs, files...)
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, s.unavailable("INPUT", err.Error())
	}
	// contextContract's inventory observes root membership and is rechecked by finish.
	for _, entry := range entries {
		if !entry.IsDir() && semHas([]string{".json", ".yaml", ".yml"}, filepath.Ext(entry.Name())) {
			refs = append(refs, entry.Name())
		}
	}
	for _, ref := range uniqueStrings(refs) {
		if ciCapturedSource.MatchString(ref) {
			continue
		}
		if !semHas([]string{".json", ".yaml", ".yml"}, filepath.Ext(ref)) {
			continue
		}
		bytes, err := s.bytes(ref)
		if err != nil {
			return nil, err
		}
		value, err := ciParse(bytes, ref)
		if err != nil || value["gate_id"] != nil || value["gates"] == nil {
			continue
		}
		for _, bucket := range []string{"gates", "checks"} {
			for _, row := range semMap(value[bucket]) {
				approval := text(semMap(row)["approval_ref"])
				if approval != "" {
					owners[approval] = append(owners[approval], ref)
				}
			}
		}
	}
	for ref, rows := range owners {
		owners[ref] = uniqueStrings(rows)
	}
	return owners, nil
}
func (s *semanticSession) ciBaseProtection(base string, roots []string, values map[string]map[string]any, checkpoints []string) error {
	if !ciCommit.MatchString(base) {
		return s.unavailable("ARGUMENT", "--base 必须为完整40位 commit")
	}
	if _, err := s.git(s.root, "cat-file", "-e", base+"^{commit}"); err != nil {
		return err
	}
	bytes, err := s.git(s.root, "ls-tree", "-r", "--name-only", "-z", base)
	if err != nil {
		return err
	}
	oldFiles := strings.Split(strings.TrimSuffix(string(bytes), "\x00"), "\x00")
	known := map[string]bool{}
	for _, r := range oldFiles {
		known[r] = true
	}
	oldValues := map[string]map[string]any{}
	read := func(ref string) (map[string]any, error) {
		if m, ok := oldValues[ref]; ok {
			return m, nil
		}
		b, e := s.git(s.root, "show", base+":"+ref)
		if e != nil {
			return nil, e
		}
		m, e := ciParse(b, ref)
		if e != nil {
			return nil, s.unavailable("INPUT", e.Error())
		}
		oldValues[ref] = m
		return m, nil
	}
	oldTracker, err := read(".template-spec/agents/issue-tracker.md")
	if err != nil {
		return err
	}
	oldRoots := []string{strings.TrimSuffix(text(semMap(oldTracker["tracker"])["root"]), "/")}
	if known[ciConfigRef] {
		config, e := read(ciConfigRef)
		if e != nil {
			return e
		}
		oldRoots = append(oldRoots, semStrings(config["additional_paths"])...)
	}
	for _, r := range oldRoots {
		covered := false
		for _, now := range roots {
			if r == now || strings.HasPrefix(r, now+"/") {
				covered = true
			}
		}
		if !covered {
			return s.reject("CI_SCOPE_REDUCED", "相对基线缩小检查范围: "+r)
		}
	}
	queue := []string{}
	for _, ref := range oldFiles {
		inside := false
		for _, r := range oldRoots {
			if ref == r || strings.HasPrefix(ref, r+"/") {
				inside = true
			}
		}
		if !inside || !semHas([]string{".md", ".yaml", ".yml", ".json"}, filepath.Ext(ref)) {
			continue
		}
		old, e := read(ref)
		if e != nil {
			return e
		}
		if ciClaimed(old) {
			queue = append(queue, ref)
		}
		cp := ciCheckpointName.MatchString(ref) || old["repository_mode"] != nil && old["stage"] != nil && old["gates"] != nil
		if cp {
			exists, e := s.exists(ref)
			if e != nil {
				return e
			}
			if !exists {
				return s.reject("CHECKPOINT_DELETED", "基线 checkpoint 已删除: "+ref)
			}
			if !semHas(checkpoints, ref) {
				return s.reject("CHECKPOINT_UNASSESSED", "基线 checkpoint 不再被识别或检查: "+ref)
			}
			now := values[ref]
			for id, state := range semMap(old["gates"]) {
				if semMap(state)["status"] == "approved" && semMap(now["gates"])[id] == nil {
					return s.reject("APPROVAL_CLAIM_REMOVED", "基线批准被移除: "+id)
				}
			}
		}
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		exists, e := s.exists(ref)
		if e != nil {
			return e
		}
		if !exists {
			return s.reject("APPROVED_EVIDENCE_DELETED", "基线批准依据已删除: "+ref)
		}
		if known[ref] && semHas([]string{".md", ".yaml", ".yml", ".json"}, filepath.Ext(ref)) {
			old, e := read(ref)
			if e != nil {
				return e
			}
			for _, dep := range ciReferences(old) {
				if known[dep] {
					queue = append(queue, dep)
				}
			}
		}
	}
	return nil
}
