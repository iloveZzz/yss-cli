package governance

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
)

type ProfileRecommendation struct {
	Profile           string     `json:"profile"`
	Recommendation    string     `json:"recommendation"`
	Basis             string     `json:"basis"`
	TargetRoot        string     `json:"target_root,omitempty"`
	EngineeringStatus string     `json:"engineering_status"`
	InputStatus       string     `json:"input_status"`
	Missing           []string   `json:"missing"`
	Commands          [][]string `json:"commands"`
}
type ProfileGuidanceResult struct {
	SchemaVersion    int                     `json:"schema_version"`
	Status           string                  `json:"status"`
	Reason           string                  `json:"reason,omitempty"`
	ReadOnly         bool                    `json:"read_only"`
	ExecutionAllowed bool                    `json:"execution_allowed"`
	Profile          string                  `json:"profile,omitempty"`
	Default          string                  `json:"default,omitempty"`
	DefaultCommands  [][]string              `json:"default_commands"`
	Recommendations  []ProfileRecommendation `json:"recommendations"`
	Missing          []string                `json:"missing"`
}

func guidanceContractRef(profile string) string {
	skill := "yss-product-lifecycle"
	if profile == "design" {
		skill = "yss-strategic-design"
	}
	if profile == "backend" || profile == "frontend" {
		skill = "harness-orchestrator"
	}
	return ".agents/skills/" + skill + "/references/orchestration-contract.yaml"
}

func guidancePolicy(s *semanticSession) (string, map[string]any, error) {
	p, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return "", nil, err
	}
	profile := ""
	for name, known := range domain.Profiles {
		if known.ID == p["profile_id"] {
			profile = name
		}
	}
	if profile == "" {
		return "", nil, s.unavailable("IDENTITY", "无法识别当前 Profile")
	}
	doc, err := s.doc(guidanceContractRef(profile))
	if err != nil {
		return profile, nil, err
	}
	policy := semMap(doc["profile_guidance"])
	if contractN(policy["schema_version"]) != 1 || policy["links_file"] != ".yss-profile-links.json" || len(semMap(semMap(policy["routes"])[profile])) == 0 {
		return profile, nil, s.unavailable("CAPABILITY", "当前实例不支持 Profile 引导；先同步模板")
	}
	for _, capability := range semStrings(policy["required_capabilities"]) {
		if !semHas([]string{"profile-guidance-v1", "profile-prepare", "spec-baseline-v1"}, capability) {
			return profile, nil, s.unavailable("CAPABILITY", "当前 CLI 不支持引导合同所需能力："+capability)
		}
	}
	return profile, policy, nil
}

func guidanceArtifact(cp map[string]any, id string, fallback ...string) string {
	if ref := text(semMap(semMap(cp["artifacts"])[id])["ref"]); ref != "" {
		return ref
	}
	for _, k := range fallback {
		if ref := text(cp[k]); ref != "" {
			return ref
		}
	}
	return ""
}

func guidanceStrategicHandoff(s *semanticSession, cp map[string]any) (*nativeHandoff, bool, error) {
	for _, asset := range []struct {
		id       string
		fallback []string
		delivery bool
	}{
		{"artifact.strategic-handoff-delivery", []string{"strategic_delivery_ref"}, true},
		{"artifact.strategic-design-handoff", []string{"strategic_handoff_ref", "handoff_ref"}, false},
	} {
		ref := guidanceArtifact(cp, asset.id, asset.fallback...)
		if ref == "" {
			continue
		}
		declared := semMap(semMap(cp["artifacts"])[asset.id])
		if declared["ref"] == ref && declared["status"] != nil && declared["status"] != "approved" {
			return nil, true, s.reject("PROFILE_BLOCKED", "当前显式战略交接资产尚未批准或已过期")
		}
		if asset.delivery {
			h, _, err := contractOpenStrategicDelivery(s, ref)
			return h, true, err
		}
		if err := s.authorities(); err != nil {
			return nil, true, err
		}
		h, err := contractInspectHandoff(s, ref)
		return h, true, err
	}
	return nil, false, nil
}

// A completed string is never a qualification. These are the same readers used
// by handoff consumers; every call observes and rechecks current evidence.
func guidanceSource(s *semanticSession, profile, cpRef string, cp map[string]any) (map[string]string, string, error) {
	if cpRef == "" {
		return nil, "", s.unavailable("CHECKPOINT_REQUIRED", "缺少当前 checkpoint；输入待核验")
	}
	if cp == nil {
		var err error
		cp, err = s.doc(cpRef)
		if err != nil {
			return nil, "", err
		}
	}
	if cp["profile_id"] != nil && cp["profile_id"] != domain.Profiles[profile].ID {
		return nil, "", s.reject("IDENTITY", "checkpoint Profile 不匹配")
	}
	if len(semList(cp["blockers"])) > 0 {
		return nil, "", s.reject("PROFILE_BLOCKED", "当前工作存在已登记阻断项")
	}
	switch profile {
	case "spec":
		if h, present, err := guidanceStrategicHandoff(s, cp); present {
			if err != nil {
				return nil, "", err
			}
			routes := guidanceConsumerRoutes(h.Handoff)
			routes["design"] = "not-applicable"
			return routes, "当前 Spec 的战略交接、来源批准和消费者路由已核验", nil
		}
		if semMap(semMap(cp["gates"])["gate.spec-baseline-approved"])["status"] != "approved" {
			return nil, "", s.reject("WAITING_PROFILE_INPUT", "等待当前 Spec 基线批准")
		}
		source, _, err := verifySpecBaselineSource(s, cpRef)
		if err != nil {
			return nil, "", err
		}
		if source["product_design_required"] != true {
			return map[string]string{"design": "not-applicable"}, "当前批准 Spec 无产品设计影响", nil
		}
		return map[string]string{"design": "optional"}, "当前批准 Spec 已核验；默认在本 Spec 继续设计", nil
	case "design":
		h, present, err := guidanceStrategicHandoff(s, cp)
		if err != nil {
			return nil, "", err
		}
		if !present {
			return nil, "", s.reject("WAITING_PROFILE_INPUT", "等待当前战略交接及来源批准")
		}
		return guidanceConsumerRoutes(h.Handoff), "当前战略交接、来源批准和消费者路由已核验", nil
	case "backend":
		ref := guidanceArtifact(cp, "artifact.backend-delivery", "backend_delivery_ref")
		if ref == "" {
			present, err := s.exists(".yss-backend-delivery.json")
			if err != nil {
				return nil, "", err
			}
			if !present {
				return nil, "", s.reject("WAITING_PROFILE_INPUT", "等待真实后端交付记录")
			}
			if err = s.verify("backend-terminal", ".yss-backend-delivery.json", nil); err != nil {
				return nil, "", err
			}
			doc, err := s.doc(".yss-backend-delivery.json")
			if err != nil {
				return nil, "", err
			}
			ref = text(semMap(doc["delivery"])["ref"])
		}
		delivery, err := backendInspectDelivery(s, ref, nil)
		if err != nil {
			return nil, "", err
		}
		h, err := contractOpenHandoff(s, text(delivery["strategic_bundle_ref"]))
		if err != nil {
			return nil, "", err
		}
		return guidanceConsumerRoutes(h.Handoff), "真实后端交付及下游消费者路由已核验", nil
	case "frontend":
		return map[string]string{}, "当前 Profile 无下游初始化职责", nil
	}
	return nil, "", s.unavailable("CAPABILITY", "不支持的 Profile 引导")
}
func guidanceConsumerRoutes(h map[string]any) map[string]string {
	result := map[string]string{}
	for profile, capability := range map[string]string{"backend": "backend-technical-design", "frontend": "frontend-engineering-design"} {
		route := apFind(h["consumer_routes"], "capability", capability)
		if route != nil {
			result[profile] = text(route["activation"])
		}
	}
	return result
}

func guidanceEngineering(root, profile string) (string, string) {
	if root == "" {
		return "unlinked", ""
	}
	if _, err := safefs.Path(root, domain.MetadataFile); err != nil {
		return "needs-attention", err.Error()
	}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return "not-initialized", ""
	} else if err != nil {
		return "needs-attention", err.Error()
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "needs-attention", err.Error()
	}
	if len(entries) == 0 {
		return "not-initialized", ""
	}
	s := newSemanticSession(context.Background(), root, nil)
	profileRef := ".template-spec/process/harness-profile.yaml"
	present, err := s.exists(profileRef)
	if err != nil {
		return "needs-attention", err.Error()
	}
	if !present {
		return "needs-attention", "目标目录为已有普通工程；请先 yss attach"
	}
	p, err := s.doc(profileRef)
	if err != nil {
		return "needs-attention", err.Error()
	}
	if p["profile_id"] != domain.Profiles[profile].ID {
		return "needs-attention", "目标工程 Profile 冲突；选择正确目录并显式重新登记关联"
	}
	if err := s.authorities(); err != nil {
		return "needs-attention", err.Error()
	}
	raw, err := s.bytes(domain.MetadataFile)
	if err != nil {
		return "needs-attention", "当前工程需要迁移 native 身份；已有普通工程请先 yss attach"
	}
	meta, err := identitymeta.ValidateNative(root, raw, profile)
	if err != nil || meta.Profile != profile {
		return "needs-attention", "目标原生身份不能核验"
	}
	if err = s.finish(); err != nil {
		return "needs-attention", err.Error()
	}
	state, err := transaction.Status(root)
	if err != nil {
		return "needs-attention", err.Error()
	}
	if len(state.Pending) > 0 || len(state.Preparations) > 0 {
		return "needs-attention", "先恢复未完成事务"
	}
	return "initialized", ""
}

// Source approval qualifies the recommendation, not the target's inputs. Only
// an explicit receipt and a registered target checkpoint can qualify intake.
func guidanceTargetInput(ctx context.Context, source *semanticSession, profile, cpRef string, cp map[string]any, targetRoot, target, engineering string) (string, string, []string) {
	specBaseline := profile == "spec" && target == "design"
	if engineering == "needs-attention" {
		return "blocked", "目标身份或未完成事务需要处理后才能核验输入", nil
	}
	if engineering != "initialized" {
		return "waiting-input", "目标工程尚未接入当前来源输入", nil
	}
	if cp == nil {
		var err error
		cp, err = source.doc(cpRef)
		if err != nil {
			return "blocked", err.Error(), nil
		}
	}
	feature := text(cp["feature_id"])
	receiptRef, sourceDigest := "", ""
	var sourceHandoff map[string]any
	var sourceDelivery map[string]any
	if specBaseline {
		version := text(cp["version"])
		if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(version) {
			version = "v1"
		}
		receiptRef = path.Join("docs/spec-baselines", "spec-baseline."+strings.TrimPrefix(feature, "feature."), version, "receipt.json")
		raw, err := source.bytes(cpRef)
		if err != nil {
			return "blocked", err.Error(), nil
		}
		sourceDigest = "sha256:" + safefs.Digest(raw)
	} else {
		var h *nativeHandoff
		var err error
		if profile == "design" || profile == "spec" {
			var present bool
			h, present, err = guidanceStrategicHandoff(source, cp)
			if err == nil && !present {
				err = source.reject("WAITING_PROFILE_INPUT", "等待当前战略交接及来源批准")
			}
		} else {
			ref := guidanceArtifact(cp, "artifact.backend-delivery", "backend_delivery_ref")
			if ref == "" {
				terminal, e := source.doc(".yss-backend-delivery.json")
				if e != nil {
					return "blocked", e.Error(), nil
				}
				ref = text(semMap(terminal["delivery"])["ref"])
			}
			delivery, e := backendInspectDelivery(source, ref, nil)
			if e != nil {
				return "blocked", e.Error(), nil
			}
			sourceDelivery = delivery
			h, err = contractOpenHandoff(source, text(delivery["strategic_bundle_ref"]))
		}
		if err != nil {
			return "blocked", err.Error(), nil
		}
		sourceHandoff = h.Handoff
		receiptRef = path.Join("docs/handoffs", text(h.Handoff["handoff_id"]), text(h.Handoff["handoff_version"]), "import-receipt.json")
		sourceDigest = text(h.Manifest["bundle_digest"])
	}
	s := newSemanticSession(ctx, targetRoot, nil)
	present, err := s.exists(receiptRef)
	if err != nil {
		return "blocked", err.Error(), nil
	}
	if !present {
		if specBaseline {
			return "waiting-input", "等待接入当前来源包和接收记录：" + receiptRef, []string{"yss", "handoff", "export", "--root", source.root, "--kind", "spec-baseline", "--checkpoint", cpRef, "--out", "<新基线包目录>"}
		}
		return "waiting-input", "等待接入当前来源包和接收记录：" + receiptRef, nil
	}
	if specBaseline {
		manifest, _, e := verifySpecBaselineReceipt(s, receiptRef, false)
		if e != nil {
			return "blocked", e.Error(), nil
		}
		if semMap(manifest["source"])["checkpoint_digest"] != sourceDigest {
			return "blocked", "目标接收记录未绑定当前 Spec 来源", nil
		}
	} else {
		receipt, e := s.doc(receiptRef)
		if e != nil {
			return "blocked", e.Error(), nil
		}
		if e = contractReceipt(s, receipt); e != nil {
			return "blocked", e.Error(), nil
		}
		if sourceDigest != "" && receipt["bundle_digest"] != sourceDigest {
			return "blocked", "目标接收记录未绑定当前战略交接", nil
		}
		bundle, e := contractOpenHandoff(s, text(receipt["package_ref"]))
		if e != nil {
			return "blocked", e.Error(), nil
		}
		if !contractSame(bundle.Handoff, sourceHandoff) {
			return "blocked", "目标包中的战略交接与当前来源不同", nil
		}
		capability := map[string]string{"backend": "backend-technical-design", "frontend": "frontend-engineering-design"}[target]
		if e = contractReceiptConsumer(s, receipt, capability); e != nil {
			return "blocked", e.Error(), nil
		}
	}
	trackerBytes, err := s.bytes(worklayout.TrackerRef)
	if err != nil {
		return "pending-verification", "目标尚未登记当前工作：" + err.Error(), nil
	}
	layout, err := worklayout.FromDocument(targetRoot, trackerBytes)
	if err != nil {
		return "blocked", err.Error(), nil
	}
	targetCPRef, targetCP, err := guidanceRegisteredCheckpoint(s, layout.Root, feature)
	if err != nil {
		return "blocked", err.Error(), nil
	}
	if targetCPRef == "" {
		return "pending-verification", "接收记录已核验；等待明确登记目标自己的当前 checkpoint 和 Context 对账", nil
	}
	s.checkpointRef = targetCPRef
	recon := semMap(targetCP["context_reconciliation"])
	if recon["status"] != "reconciled" || text(recon["ref"]) == "" {
		return "pending-verification", "目标 Context 尚未对账", nil
	}
	if specBaseline {
		_, _, err = verifySpecBaselineReceipt(s, receiptRef, true)
	} else {
		err = s.verify("context-reconciliation", text(recon["ref"]), map[string]string{"import-receipt": receiptRef})
		if err == nil {
			err = guidanceHandoffContext(s, sourceHandoff)
		}
		if err == nil && profile == "backend" {
			acceptance := guidanceArtifact(targetCP, "artifact.frontend-delivery-acceptance", "frontend_delivery_ref", "acceptance_ref")
			if acceptance == "" {
				return "waiting-input", "等待前端对真实后端交付的接收合同", nil
			}
			doc, e := s.doc(acceptance)
			if e != nil {
				return "blocked", e.Error(), nil
			}
			binding := semMap(doc["strategic_handoff"])
			if binding["import_receipt_ref"] != receiptRef || binding["bundle_digest"] != sourceDigest {
				return "blocked", "前端接收合同未绑定当前战略输入", nil
			}
			slice := text(semMap(sourceDelivery["scope"])["slice_id"])
			err = s.verify("frontend-delivery", acceptance, map[string]string{"slice": slice, "phase": "inputs"})
			if err == nil {
				backendReceipt, e := s.doc(text(semMap(doc["backend_delivery"])["import_receipt_ref"]))
				if e != nil {
					return "blocked", e.Error(), nil
				}
				manifest, imported, e := backendOpenDelivery(s, text(backendReceipt["package_ref"]), map[string]string{"phase": "inputs"})
				if e != nil {
					return "blocked", e.Error(), nil
				}
				current, e := imported.doc(text(manifest["delivery_ref"]))
				if e != nil {
					return "blocked", e.Error(), nil
				}
				if !contractSame(current, sourceDelivery) {
					return "blocked", "前端接收的后端交付与当前来源不同", nil
				}
			}
		}
	}
	if err == nil {
		err = s.finish()
	}
	if err == nil {
		err = source.finish()
	}
	if err != nil {
		return "blocked", err.Error(), nil
	}
	if specBaseline {
		return "verified", "", []string{"yss", "handoff", "verify", "--root", targetRoot, "--kind", "spec-baseline", "--file", receiptRef, "--checkpoint", targetCPRef, "--json"}
	}
	return "verified", "", nil
}

// Intake uses the approved Context delta, as the strategic consumption reader
// does. A receiver's self-consistent snapshot cannot redefine incoming terms.
func guidanceHandoffContext(s *semanticSession, handoff map[string]any) error {
	context, err := s.contextContract()
	if err != nil {
		return err
	}
	terms := map[string]Term{}
	for _, term := range context["business_terms"].([]Term) {
		terms[term.TermRef] = term
	}
	for _, operation := range []string{"added", "updated", "deprecated"} {
		for _, row := range semList(semMap(handoff["context_delta"])[operation]) {
			expected := semMap(row)
			term, present := terms[text(expected["term_ref"])]
			if operation == "deprecated" {
				if present {
					return s.reject("HANDOFF_CONTEXT", "废弃术语仍存在")
				}
				continue
			}
			if !present {
				return s.reject("HANDOFF_CONTEXT", "目标术语未对账")
			}
			actual := semMap(mustParseContract(mustMarshalContract(term)))
			for _, key := range []string{"term", "meaning", "english_identifier", "context_id", "forbidden_aliases"} {
				if !contractSame(expected[key], actual[key]) {
					return s.reject("HANDOFF_CONTEXT", "目标术语字段未对账")
				}
			}
		}
	}
	return nil
}

func guidanceRegisteredCheckpoint(s *semanticSession, workRoot, feature string) (string, map[string]any, error) {
	present, err := s.exists(workRoot)
	if err != nil || !present {
		return "", nil, err
	}
	names, err := s.list(workRoot)
	if err != nil {
		return "", nil, err
	}
	selected := ""
	var checkpoint map[string]any
	for _, name := range names {
		mapRef := path.Join(workRoot, name, "map.md")
		present, e := s.exists(mapRef)
		if e != nil || !present {
			continue
		}
		registration, e := contractTicketMetadata(s, mapRef)
		if e != nil {
			continue
		}
		ref := text(registration["checkpoint_ref"])
		if ref == "" {
			continue
		}
		cp, e := s.doc(ref)
		if e != nil || cp["feature_id"] != feature {
			continue
		}
		if selected != "" && selected != ref {
			return "", nil, s.reject("PROFILE_INPUT_AMBIGUOUS", "同一来源功能登记了多个目标 checkpoint；先明确当前工作入口")
		}
		selected, checkpoint = ref, cp
	}
	return selected, checkpoint, nil
}

// ProfileGuidance is an optional readonly projection. Missing new policy never
// breaks the pre-existing lifecycle status response.
func ProfileGuidance(ctx context.Context, root, cpRef string, cp map[string]any) *ProfileGuidanceResult {
	g := &ProfileGuidanceResult{SchemaVersion: 1, Status: "unsupported", ReadOnly: true, DefaultCommands: [][]string{}, Recommendations: []ProfileRecommendation{}, Missing: []string{}}
	s := newSemanticSession(ctx, root, map[string]string{"checkpoint": cpRef})
	profile, policy, err := guidancePolicy(s)
	g.Profile = profile
	if err != nil {
		g.Missing = append(g.Missing, err.Error())
		if profile != "" && semanticCode(err) == "CAPABILITY" {
			g.DefaultCommands = append(g.DefaultCommands, []string{"yss", "sync", "--root", root, "--plan", "--out", "<新同步计划文件>"})
		}
		return g
	}
	g.Status = "available"
	entry := semMap(semMap(policy["routes"])[profile])
	g.Default = text(entry["default"])
	if len(semStrings(entry["targets"])) == 0 {
		g.Reason = "当前 Profile 没有下游初始化职责"
		return g
	}
	links, err := identitymeta.ReadProfileLinks(root)
	if err != nil {
		g.Status = "blocked"
		g.Missing = append(g.Missing, err.Error())
		return g
	}
	if cp == nil && cpRef != "" {
		cp, _ = s.doc(cpRef)
	}
	explicitSpecHandoff := profile == "spec" && (guidanceArtifact(cp, "artifact.strategic-handoff-delivery", "strategic_delivery_ref") != "" || guidanceArtifact(cp, "artifact.strategic-design-handoff", "strategic_handoff_ref", "handoff_ref") != "")
	routes, basis, sourceErr := guidanceSource(s, profile, cpRef, cp)
	if sourceErr == nil {
		sourceErr = s.finish()
	}
	if sourceErr == nil && g.Default == "continue-current-spec" && cp != nil {
		next := text(cp["next_work_unit"])
		if s.registry != nil && semanticDefinition(s.registry, "work_units", next) != nil {
			g.DefaultCommands = append(g.DefaultCommands, []string{"yss", "lifecycle", "query", "--root", root, "--id", next, "--json"})
		}
	}
	for _, target := range semStrings(entry["targets"]) {
		if _, valid := domain.Profiles[target]; !valid {
			g.Status = "blocked"
			g.Missing = append(g.Missing, "未知下游 Profile: "+target)
			continue
		}
		targetRoot := links.Links[target]
		engineering, problem := guidanceEngineering(targetRoot, target)
		r := ProfileRecommendation{Profile: target, Recommendation: "pending-verification", Basis: basis, TargetRoot: targetRoot, EngineeringStatus: engineering, InputStatus: "waiting-input", Missing: []string{}, Commands: [][]string{}}
		if sourceErr != nil {
			r.InputStatus = "pending-verification"
			if semanticExit(sourceErr) == 1 || explicitSpecHandoff {
				r.InputStatus = "blocked"
			}
			if semanticCode(sourceErr) == "WAITING_PROFILE_INPUT" {
				r.InputStatus = "waiting-input"
			}
			r.Missing = append(r.Missing, sourceErr.Error())
			r.Commands = append(r.Commands, []string{"yss", "lifecycle", "verify", "--root", root, "--checkpoint", first(cpRef, "<当前checkpoint>"), "--json"})
		} else if activation := routes[target]; semHas([]string{"required", "optional", "not-applicable"}, activation) {
			r.Recommendation = activation
			if activation != "not-applicable" {
				var missing string
				var command []string
				r.InputStatus, missing, command = guidanceTargetInput(ctx, s, profile, cpRef, cp, targetRoot, target, engineering)
				if len(command) > 0 {
					r.Commands = append(r.Commands, command)
					if profile == "spec" && target == "design" && r.InputStatus == "waiting-input" {
						r.Commands = append(r.Commands, []string{"yss", "handoff", "import", "--root", targetRoot, "--kind", "spec-baseline", "--package", "<基线包目录>", "--plan", "--out", "<新接入计划文件>"})
					}
				}
				if missing != "" {
					r.Missing = append(r.Missing, missing)
				}
			}
			if activation == "not-applicable" {
				if profile != "spec" {
					r.Basis = "当前批准消费者路由不适用该交付面"
				}
				r.InputStatus = "waiting-input"
			}
		} else {
			r.InputStatus = "pending-verification"
			r.Missing = append(r.Missing, "缺少可核验消费者路由")
		}
		if problem != "" {
			r.Missing = append(r.Missing, problem)
			if strings.Contains(problem, "已有普通工程") {
				r.Commands = append(r.Commands, []string{"yss", "attach", "--root", targetRoot, "--profile", target, "--plan", "--out", "<新接管计划文件>"})
			}
		}
		if r.Recommendation == "required" || r.Recommendation == "optional" {
			if engineering == "initialized" && len(r.Commands) == 0 {
				kind := "consumption"
				if profile == "spec" && target == "design" {
					kind = "spec-baseline"
				}
				file := "<当前消费合同文件>"
				if kind == "spec-baseline" {
					file = "<目标接收记录>"
				}
				argv := []string{"yss", "handoff", "verify", "--root", targetRoot, "--kind", kind, "--file", file}
				if kind == "consumption" && target == "frontend" {
					argv = append(argv, "--consumer", "frontend")
				}
				r.Commands = append(r.Commands, append(argv, "--json"))
			} else if engineering != "initialized" && engineering != "needs-attention" {
				r.Commands = append(r.Commands, []string{"yss", "profile", "prepare", "--root", root, "--" + target + "-root", first(targetRoot, "<"+target+"目录>"), "--checkpoint", cpRef, "--plan", "--out", "<新计划文件>"})
			}
		}
		g.Recommendations = append(g.Recommendations, r)
	}
	return g
}

// ProfilePreparationSource returns the exact source inputs for the saved plan.
// Directory preparation is allowed while downstream inputs are still pending;
// an explicitly supplied checkpoint must verify before it can bind the plan.
func ProfilePreparationSource(ctx context.Context, root, cpRef string, targets []string) ([]string, error) {
	s := newSemanticSession(ctx, root, map[string]string{"checkpoint": cpRef})
	profile, policy, err := guidancePolicy(s)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if !semHas(semMap(semMap(policy["routes"])[profile])["targets"], target) {
			return nil, domain.Fail("PROFILE_ROUTE", fmt.Sprintf("当前 %s 不支持准备 %s", profile, target))
		}
	}
	if _, err = s.contextContract(); err != nil {
		return nil, err
	}
	if cpRef != "" {
		if _, _, err = guidanceSource(s, profile, cpRef, nil); err != nil {
			return nil, err
		}
	}
	if err = s.finish(); err != nil {
		return nil, err
	}
	refs := []string{}
	for _, input := range s.inputs() {
		prefix, err := filepath.Rel(root, input.Root)
		if err != nil || prefix == ".." || strings.HasPrefix(prefix, ".."+string(filepath.Separator)) {
			continue
		}
		ref := filepath.ToSlash(filepath.Join(prefix, input.Ref))
		if safefs.ValidateRef(ref) == nil && ref != ".yss/transactions" && !strings.HasPrefix(ref, ".yss/transactions/") && ref != ".yss/asset-transactions" && !strings.HasPrefix(ref, ".yss/asset-transactions/") {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}
