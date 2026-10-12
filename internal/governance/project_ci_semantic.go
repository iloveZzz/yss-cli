package governance

import (
	"context"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var ciReadyTicket = regexp.MustCompile(`(?:^|\n)Status:\s*ready-for-agent\s*(?:\n|$)`)

var ciSourceDeliveryEvidence = regexp.MustCompile(`^docs/handoffs/[^/]+/v[1-9][0-9]*/source-delivery-record\.json$`)

func ciEvidenceReceipt(ref string) string {
	if ciSourceDeliveryEvidence.MatchString(ref) {
		return path.Join(path.Dir(ref), "import-receipt.json")
	}
	return ""
}

func init() {
	registerSemanticValidator("handoff-source-delivery-evidence", func(s *kernel.SemanticSession, ref string, _ map[string]string) error {
		receiptRef := ciEvidenceReceipt(ref)
		if receiptRef == "" {
			return s.Reject("HANDOFF_RECEIPT", "来源交付证据缺少合法接收记录路径")
		}
		receipt, err := s.Doc(receiptRef)
		if err != nil {
			return err
		}
		if receipt["source_delivery_record_ref"] != ref || kernel.ContractN(receipt["schema_version"]) != 3 {
			return s.Reject("HANDOFF_RECEIPT", "来源交付证据未由receipt v3绑定")
		}
		return contractReceipt(s, receipt)
	})
	registerSemanticValidator("handoff-import-receipt", func(s *kernel.SemanticSession, ref string, _ map[string]string) error {
		receipt, err := s.Doc(ref)
		if err != nil {
			return err
		}
		if ref != path.Join("docs/handoffs", kernel.Text(receipt["bundle_id"]), kernel.Text(receipt["version"]), "import-receipt.json") {
			return s.Reject("HANDOFF_RECEIPT", "导入收据路径非法")
		}
		if err = contractReceipt(s, receipt); err != nil {
			return err
		}
		for _, capability := range kernel.SemStrings(receipt["selected_consumer_capabilities"]) {
			if err = contractReceiptConsumer(s, receipt, capability); err != nil {
				return err
			}
		}
		return nil
	})
	registerSemanticValidator("spec-baseline-package", func(s *kernel.SemanticSession, ref string, _ map[string]string) error {
		_, _, err := openSpecBaseline(s, ref)
		return err
	})
	registerSemanticValidator("spec-baseline-import-receipt", func(s *kernel.SemanticSession, ref string, _ map[string]string) error {
		_, _, err := verifySpecBaselineReceipt(s, ref, false)
		return err
	})
}

func ciBindings(value any) []map[string]any {
	out := []map[string]any{}
	var visit func(any)
	visit = func(v any) {
		if m, ok := kernel.Object(v); ok {
			if kernel.Text(m["ref"]) != "" && (m["digest"] != nil || m["sha256"] != nil) {
				hash := m["digest"]
				if hash == nil {
					hash = m["sha256"]
				}
				out = append(out, map[string]any{"ref": m["ref"], "digest": hash})
			}
			for k, item := range m {
				if strings.HasSuffix(k, "_ref") && kernel.Text(item) != "" && m[strings.TrimSuffix(k, "_ref")+"_digest"] != nil {
					out = append(out, map[string]any{"ref": item, "digest": m[strings.TrimSuffix(k, "_ref")+"_digest"]})
				}
				visit(item)
			}
		} else {
			for _, row := range kernel.SemList(v) {
				visit(row)
			}
		}
	}
	visit(value)
	return out
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
	if kernel.Text(v["kind"]) == "strategic-handoff-delivery-v1" {
		return "strategic-delivery"
	}
	if v["slice_contract"] != nil || kernel.Text(v["contract_id"]) != "" && v["work_units"] != nil {
		return "slice"
	}
	if v["generation_policy"] != nil && v["generator_skill"] != nil || v["scaffold_status"] != nil && v["delivery_role"] != nil {
		return "scaffold"
	}
	if v["execution_result"] != nil || v["consumed_contract"] != nil || kernel.SemHas([]string{"template-verification-report", "frontend-scaffold-verification", "backend-contract", "backend-deployment", "strategic-delivery-verification-v1"}, kernel.Text(v["kind"])) {
		return "verification"
	}
	if v["strategic_handoff"] != nil {
		if v["frontend_cases"] != nil || v["frontend_implementation_plan"] != nil || kernel.Text(v["kind"]) == "frontend-implementation-plan" {
			return "handoff-consumption:frontend"
		}
		return "handoff-consumption:tactical"
	}
	return ""
}

func fullProjectCIRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	s := kernel.NewSemanticSession(ctx, root, args)
	s.Report.Kind = "project-governance-verification"
	s.Report.Scope = "complete-governance"
	roots := []string{}
	files := []string{}
	checkpoints := []string{}
	tasks := []string{}
	unknown := []string{}
	missing := []string{}
	fail := func(ref string, err error) {
		s.Diagnostic(ref, err)
		if kernel.SemanticExit(err) == 2 {
			missing = append(missing, ref)
		}
	}
	run := func(kind, ref string, opts map[string]string) {
		if err := s.Verify(kind, ref, opts); err != nil {
			fail(ref, err)
		}
	}
	setup := func() error {
		for key := range args {
			if !kernel.SemHas([]string{"root", "profile", "json", "scope", "runtime-store", "base", "checkpoint", "task", "recover", "home", "run-dir", "tool-root", "template-checkout"}, key) {
				return s.Unavailable("ARGUMENT", "完整治理检查不接受参数: --"+key)
			}
		}
		if args["scope"] != "" && args["scope"] != "full" {
			return s.Unavailable("ARGUMENT", "未知治理范围；有限检查须显式 --scope native-go")
		}
		if args["runtime-store"] != "" && args["runtime-store"] != "off" {
			return s.Unavailable("CAPABILITY", "完整治理本批仅支持 runtime-store off")
		}
		if err := s.Authorities(); err != nil {
			return err
		}
		var err error
		roots, _, err = governanceScope(s.V)
		if err != nil {
			return s.Unavailable("INPUT", err.Error())
		}
		for _, r := range roots {
			set, err := s.Scan(r)
			if err != nil {
				return err
			}
			files = append(files, set...)
		}
		files = kernel.UniqueStrings(files)
		return nil
	}
	err := setup()
	if err != nil {
		fail(args["checkpoint"], err)
	} else {
		if _, err = s.ContextContract(); err != nil {
			fail("CONTEXT.md", err)
		} else {
			s.Report.Checks = append(s.Report.Checks, kernel.SemanticCheck{ID: "context", SourceRef: "CONTEXT.md", Status: "passed"})
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
			bytes, e := s.Bytes(ref)
			if e != nil {
				fail(ref, e)
				continue
			}
			value, e := kernel.CiParse(bytes, ref)
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
			if kernel.SemHas([]string{".json", ".yaml", ".yml", ".md"}, filepath.Ext(ref)) {
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
			bytes, e := s.Bytes(ref)
			if e != nil {
				fail(ref, e)
				continue
			}
			value, e := kernel.CiParse(bytes, ref)
			if e != nil {
				fail(ref, s.Unavailable("INPUT", e.Error()))
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
			if prefix := kernel.Text(value["package_ref"]); prefix != "" && !kernel.CiURI.MatchString(prefix) && !filepath.IsAbs(prefix) {
				if present, e := s.Exists(filepath.ToSlash(filepath.Join(prefix, "manifest.json"))); e != nil {
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
			cp := kernel.CiCheckpointName.MatchString(ref) || value["repository_mode"] != nil && value["stage"] != nil && value["gates"] != nil
			task := kernel.Text(value["task_id"]) != "" && kernel.Text(value["work_unit_id"]) != "" && (value["contract"] != nil || kernel.SemHas([]string{"active", "paused", "failed", "resolved"}, kernel.Text(value["workflow_status"])))
			if cp {
				checkpoints = append(checkpoints, ref)
				run("checkpoint", ref, map[string]string{})
				if kernel.Text(kernel.SemMap(value["stage_trace"])["completed_work_unit"]) != "" {
					run("next-route", ref, map[string]string{})
				} else if kernel.SemHas([]string{"running", "completed"}, kernel.Text(value["status"])) && kernel.SemHas([]string{"resume", "orchestrate"}, kernel.Text(value["mode"])) {
					fail(ref, s.Reject("TRANSITION_ORIGIN_REQUIRED", "当前流转缺少 completed_work_unit"))
				}
				if args["recover"] == "true" && (len(kernel.SemList(value["blockers"])) > 0 || kernel.SemHas([]string{"blocked", "stale"}, kernel.Text(value["status"]))) {
					fail(ref, s.Reject("RECOVERY_BLOCKED", "当前 checkpoint 存在阻塞"))
				}
			} else if task {
				tasks = append(tasks, ref)
				run("task", ref, map[string]string{})
				if args["recover"] == "true" {
					if value["workflow_status"] != "resolved" {
						fail(ref, s.Reject("TASK_NOT_RESOLVED", "先核查原任务，禁止重复派发"))
					} else if len(kernel.SemList(kernel.SemMap(value["result"])["evidence_refs"])) == 0 && len(kernel.SemList(value["verification_results"])) == 0 {
						fail(ref, s.Reject("TASK_RESULT_REQUIRED", "resolved 任务缺少实际结果"))
					}
				}
			} else if kernel.CiClaimed(value) || ciDomainKind(value) != "" {
				claims[ref] = true
			} else {
				unknown = append(unknown, ref)
			}
			if ciReadyTicket.Match(bytes) {
				tickets = append(tickets, ref)
			}
			if cp || task || kernel.CiClaimed(value) || ciDomainKind(value) != "" || value["gate_id"] != nil || strict[ref] {
				for _, dependency := range kernel.CiReferences(value) {
					if kernel.SemHas(packageRoots, dependency) {
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
					if _, e = s.Bytes(dependency); e != nil {
						fail(ref, e)
					} else if kernel.SemHas([]string{".yaml", ".yml", ".json", ".md"}, filepath.Ext(dependency)) {
						queue = append(queue, dependency)
					}
				}
				for _, binding := range ciBindings(value) {
					if kernel.SemHas(packageRoots, kernel.Text(binding["ref"])) {
						continue
					}
					if ciDomainKind(value) != "" && filepath.IsAbs(kernel.Text(binding["ref"])) {
						continue
					}
					if e = s.Basis([]any{binding}); e != nil {
						fail(ref, e)
					}
				}
			}
		}
		if args["checkpoint"] != "" && !kernel.SemHas(checkpoints, args["checkpoint"]) {
			fail(args["checkpoint"], s.Reject("CHECKPOINT_IDENTITY", "指定引用不是 checkpoint"))
		}
		if args["task"] != "" && !kernel.SemHas(tasks, args["task"]) {
			fail(args["task"], s.Reject("TASK_IDENTITY", "指定引用不是任务包"))
		}
		actorGroups := map[string][]map[string]any{}
		for _, ref := range tasks {
			task := values[ref]
			contract := kernel.SemMap(task["contract"])
			key := kernel.Text(contract["contract_id"]) + "\x00" + kernel.Text(contract["contract_version"])
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
				for _, dep := range kernel.CiReferences(values[ref]) {
					owner[dep] = append(owner[dep], cpRef)
					pending = append(pending, dep)
				}
			}
		}
		approvalOwners, discoveryErr := s.CiApprovalOwners()
		if discoveryErr != nil {
			fail("approval-owners", discoveryErr)
		}
		for ref := range claims {
			value := values[ref]
			if value["gate_id"] != nil && value["decision"] == "approved" {
				candidates := approvalOwners[ref]
				candidates = kernel.UniqueStrings(candidates)
				if len(candidates) != 1 {
					fail(ref, s.Reject("APPROVAL_CONTEXT_REQUIRED", fmt.Sprintf("批准记录需要唯一 checkpoint（找到%d个）", len(candidates))))
				} else {
					run("approval", ref, map[string]string{"checkpoint": candidates[0]})
				}
				continue
			}
			if len(owner[ref]) == 0 {
				fail(ref, s.Reject("CLAIM_WITHOUT_CHECKPOINT", "已声明完成或批准的资产缺少 checkpoint 归属"))
				continue
			}
			kind := ciDomainKind(value)
			if kind == "" && (value["domain_strategy_id"] != nil || value["stage_decision_id"] != nil) {
				consumers := []string{}
				for candidate, doc := range values {
					if ciDomainKind(doc) == "handoff-source" && kernel.SemHas(kernel.CiReferences(doc), ref) {
						consumers = append(consumers, candidate)
					}
				}
				if len(consumers) == 1 {
					run("handoff-source", consumers[0], semanticOptions(args))
					continue
				}
			}
			if kind != "" {
				owners := kernel.UniqueStrings(owner[ref])
				if len(owners) != 1 {
					fail(ref, s.Reject("CLAIM_CONTEXT_REQUIRED", "领域资产需要唯一当前 checkpoint 归属"))
					continue
				}
				opts := semanticOptions(args)
				opts["checkpoint"] = owners[0]
				if kind == "verification" && kernel.Text(value["kind"]) == "frontend-scaffold-verification" {
					binding := kernel.SemMap(kernel.SemMap(values[owners[0]]["artifacts"])["artifact.project-scaffold-contract"])
					if binding["status"] != "approved" || kernel.Text(binding["ref"]) == "" {
						fail(ref, s.Reject("VERIFICATION_CURRENT_REQUIRED", "完整 CI 的脚手架报告需要 checkpoint 当前批准合同"))
						continue
					}
					if _, e := contractBoundDoc(s, binding); e != nil {
						fail(ref, e)
						continue
					}
					opts["contract"] = kernel.Text(binding["ref"])
				}
				if strings.HasPrefix(kind, "handoff-consumption:") {
					opts["consumer"] = strings.TrimPrefix(kind, "handoff-consumption:")
					kind = "handoff-consumption"
				}
				run(kind, ref, opts)
			} else {
				unknown = append(unknown, ref)
				fail(ref, s.Unavailable("CAPABILITY", "当前声称批准或完成的资产缺少专属校验能力: "+ref))
			}
		}
		for _, ref := range tickets {
			if len(owner[ref]) == 0 {
				ready := false
				for _, taskRef := range tasks {
					if kernel.SemHas(kernel.CiReferences(values[taskRef]), ref) {
						ready = true
					}
				}
				if !ready {
					fail(ref, s.Reject("TICKET_READINESS_REQUIRED", "ready-for-agent Ticket 缺少当前 checkpoint/任务包证据"))
				}
			}
		}
		if args["base"] != "" {
			if e := s.CiBaseProtection(args["base"], roots, values, checkpoints); e != nil {
				fail("--base", e)
			}
		}
	}
	if final := s.Finish(); final != nil {
		fail("final-input-check", final)
	}
	s.Report.Inputs = s.Inputs()
	s.Report.GitInputs = s.GitBindings()
	s.Report.Checks = s.CollectedChecks()
	s.Report.Coverage = map[string]any{"roots": roots, "files": files, "checkpoints": kernel.UniqueStrings(checkpoints), "tasks": kernel.UniqueStrings(tasks), "unrecognized": kernel.UniqueStrings(unknown), "missing_capabilities": kernel.UniqueStrings(missing), "base": args["base"], "runtime_store": "off", "dispatch": false}
	if s.Report.Status != "passed" {
		exit := 1
		if s.Report.Status == "error" {
			exit = 2
		}
		return s.Report, &semanticFailure{cause: &domain.Error{Code: "PROJECT_CI_REJECTED", Message: "完整治理检查未通过；参见逐项诊断", Exit: exit}, report: s.Report}
	}
	return s.Report, nil
}
