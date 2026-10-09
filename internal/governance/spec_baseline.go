package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

const specBaselineProfile = "harness.business-ddd-strategy-handoff"
const specBaselineSchema = ".template-spec/process/schemas/spec-baseline-package.schema.json"
const specBaselineReceiptSchema = ".template-spec/process/schemas/spec-baseline-import-receipt.schema.json"
const specBaselineMaxBytes = 100 << 20

func specBaselineLimits(s *semanticSession, count, bytes int) error {
	if count > 20000 || bytes > specBaselineMaxBytes {
		return s.reject("SPEC_BASELINE_LIMIT", "基线包数量或大小超限")
	}
	return nil
}

func checkpointAsset(cp map[string]any, key string, ids ...string) string {
	if ref := text(cp[key]); ref != "" {
		return ref
	}
	for _, id := range ids {
		if ref := text(semMap(semMap(cp["artifacts"])[id])["ref"]); ref != "" {
			return ref
		}
	}
	return ""
}

// Source approval is consumed in its original policy and checkpoint scope. No
// receiving gate is populated or promoted by this reader.
func verifySpecBaselineSource(s *semanticSession, cpRef string) (map[string]any, []string, error) {
	if !contractPath(cpRef) {
		return nil, nil, s.reject("SPEC_BASELINE", "Spec 基线需要项目内当前 checkpoint")
	}
	if err := s.authorities(); err != nil {
		return nil, nil, err
	}
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return nil, nil, err
	}
	profile, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return nil, nil, err
	}
	if identity["repository_mode"] != "project-instance" || profile["profile_id"] != "harness.spec-template" {
		return nil, nil, s.reject("SPEC_BASELINE", "导出来源必须为 Spec 项目实例")
	}
	cp, err := s.doc(cpRef)
	if err != nil {
		return nil, nil, err
	}
	if semMap(semMap(cp["gates"])["gate.spec-baseline-approved"])["status"] != "approved" {
		return nil, nil, s.reject("SPEC_BASELINE", "当前 Spec 基线尚未批准")
	}
	if err = s.validateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); err != nil {
		return nil, nil, err
	}
	if cp["repository_mode"] != "project-instance" || cp["upstream_spec_baseline"] != nil {
		return nil, nil, s.reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 必须是本地批准的 Spec 项目实例")
	}
	reconciliation := semMap(cp["context_reconciliation"])
	if reconciliation["status"] != "reconciled" || text(reconciliation["ref"]) == "" {
		return nil, nil, s.reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 的 Context 对账尚未完成")
	}
	if err = s.verify("context-reconciliation", text(reconciliation["ref"]), map[string]string{}); err != nil {
		return nil, nil, err
	}
	specAsset := semMap(semMap(cp["artifacts"])["artifact.spec"])
	specRef := text(specAsset["ref"])
	if specAsset["status"] != "approved" || specRef == "" {
		return nil, nil, s.reject("SPEC_BASELINE_CURRENT", "来源 Spec 资产必须是当前已批准的 artifact.spec")
	}
	if direct := text(cp["spec_ref"]); direct != "" && direct != specRef {
		return nil, nil, s.reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 的当前 Spec 引用冲突")
	}
	specBytes, err := s.bytes(specRef)
	if err != nil {
		return nil, nil, err
	}
	currentSpecBound := false
	for _, row := range semList(semMap(semMap(cp["gates"])["gate.spec-baseline-approved"])["basis"]) {
		binding := semMap(row)
		if binding["ref"] == specRef && strings.TrimPrefix(text(binding["digest"]), "sha256:") == safefs.Digest(specBytes) {
			currentSpecBound = true
		}
	}
	if !currentSpecBound {
		return nil, nil, s.reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 的 Spec 批准依据未直接绑定当前原始字节")
	}
	if err = s.currentAsset(cpRef); err != nil {
		return nil, nil, err
	}
	for _, gate := range []string{"gate.plan-approved", "gate.spec-baseline-approved"} {
		if semMap(semMap(cp["gates"])[gate])["status"] != "approved" {
			return nil, nil, s.reject("SPEC_BASELINE", "来源当前批准缺失: "+gate)
		}
		if err = s.gateChecks(cp, cpRef, gate); err != nil {
			return nil, nil, err
		}
	}
	if err = s.verify("plan-spec-entry", cpRef, map[string]string{}); err != nil {
		return nil, nil, err
	}
	planRef := checkpointAsset(cp, "plan_ref", "artifact.plan")
	if review := text(cp["plan_review_ref"]); review != "" {
		d, e := s.doc(review)
		if e != nil {
			return nil, nil, e
		}
		if planRef == "" {
			planRef = text(d["plan_ref"])
		} else if planRef != text(d["plan_ref"]) {
			return nil, nil, s.reject("SPEC_BASELINE_CURRENT", "来源当前 Plan 资产与已批准 Plan 审阅包冲突")
		}
	}
	if declared := text(semMap(semMap(cp["artifacts"])["artifact.plan"])["ref"]); declared != "" && declared != planRef {
		return nil, nil, s.reject("SPEC_BASELINE_CURRENT", "来源 checkpoint 的当前 Plan 引用冲突")
	}
	strategyRef := checkpointAsset(cp, "domain_strategy_ref", "artifact.domain-strategy")
	stageRef := checkpointAsset(cp, "stage_decision_package_ref", "artifact.stage-decision-package")
	ticketsRef := checkpointAsset(cp, "business_ticket_set_ref", "artifact.business-ticket-set")
	if specRef == "" || ticketsRef == "" {
		return nil, nil, s.reject("SPEC_BASELINE", "缺少当前 Spec 或业务票草案引用")
	}
	business, err := contractBusinessTicketsMode(s, ticketsRef, "draft")
	if err != nil {
		return nil, nil, err
	}
	if semMap(business["spec"])["ref"] != specRef {
		return nil, nil, s.reject("SPEC_BASELINE_SCOPE", "业务票草案未绑定本次批准的 Spec")
	}
	if err = specBaselineAssetCoverage(s, cp, "gate.spec-baseline-approved", specRef); err != nil {
		return nil, nil, err
	}
	for _, ref := range []string{planRef, strategyRef, stageRef} {
		if ref != "" {
			if err = specBaselineAssetCoverage(s, cp, "gate.plan-approved", ref); err != nil {
				return nil, nil, err
			}
		}
	}
	if err = specBaselineDesignAssets(s, strategyRef, stageRef); err != nil {
		return nil, nil, err
	}
	productDesign := semMap(cp["delivery_impacts"])["ui"] == true
	if stageRef != "" {
		stage, e := s.doc(stageRef)
		if e != nil {
			return nil, nil, e
		}
		impact := semMap(stage["impact_assessment"])
		if v, ok := impact["product_design"].(bool); ok {
			productDesign = v
		} else {
			productDesign = impact["ui"] != false
		}
	} else {
		productDesign = true
	}
	cpBytes, err := s.bytes(cpRef)
	if err != nil {
		return nil, nil, err
	}
	metadata, err := s.doc(domain.MetadataFile)
	if err != nil {
		return nil, nil, err
	}
	templateCommit := text(metadata["templateCommit"])
	if !ciCommit.MatchString(templateCommit) {
		return nil, nil, s.reject("IDENTITY", "Spec 基线需要固定模板来源")
	}
	source := map[string]any{"profile_id": "harness.spec-template", "feature_id": cp["feature_id"], "checkpoint_ref": cpRef, "checkpoint_digest": "sha256:" + safefs.Digest(cpBytes), "template_commit": templateCommit, "spec_ref": specRef, "spec_digest": "sha256:" + safefs.Digest(specBytes), "plan_ref": planRef, "domain_strategy_ref": strategyRef, "stage_decision_package_ref": stageRef, "business_ticket_set_ref": ticketsRef, "product_design_required": productDesign}
	extra := []any{domain.MetadataFile, "yss-project.yaml", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/yss-skill-registry.yaml"}
	_, orchestrationRef, e := s.orchestration()
	if e != nil {
		return nil, nil, e
	}
	extra = append(extra, orchestrationRef)
	for _, ref := range []string{specRef, planRef, strategyRef, stageRef, ticketsRef} {
		if ref != "" {
			extra = append(extra, ref)
		}
	}
	fake := &nativeHandoff{Handoff: map[string]any{"source": map[string]any{}, "evidence_and_version_digests": []any{}}, Config: map[string]any{"approvals": map[string]any{}, "additional_files": extra}}
	refs, err := contractHandoffClosure(s, cpRef, fake)
	if err != nil {
		return nil, nil, err
	}
	return source, refs, nil
}

func VerifySpecBaselineSource(ctx context.Context, root, checkpointRef string) (any, error) {
	s := newSemanticSession(ctx, root, map[string]string{"checkpoint": checkpointRef})
	_, _, err := verifySpecBaselineSource(s, checkpointRef)
	return specBaselineReport(s, checkpointRef, err)
}

func specBaselineReport(s *semanticSession, ref string, err error) (any, error) {
	if err != nil {
		s.diagnostic(ref, err)
	}
	if e := s.finish(); e != nil {
		err = e
		s.diagnostic(ref, e)
	}
	s.report.Inputs = s.inputs()
	s.report.Checks = s.collectedChecks()
	s.report.GitInputs = s.gitBindings()
	if err != nil {
		return s.report, &semanticFailure{cause: err, report: s.report}
	}
	return s.report, nil
}

func baselineFileRef(original string) string {
	if original == "CONTEXT.md" {
		return "payload/files/source-context.snapshot.md"
	}
	return "payload/files/" + original
}

func specBaselineManifest(s *semanticSession, cpRef string) (map[string]any, map[string][]byte, error) {
	source, refs, err := verifySpecBaselineSource(s, cpRef)
	if err != nil {
		return nil, nil, err
	}
	cp, _ := s.doc(cpRef)
	version := text(cp["version"])
	if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(version) {
		version = "v1"
	}
	id := "spec-baseline." + strings.TrimPrefix(text(source["feature_id"]), "feature.")
	if !regexp.MustCompile(`^spec-baseline\.[A-Za-z0-9][A-Za-z0-9._-]*$`).MatchString(id) {
		return nil, nil, s.reject("SPEC_BASELINE", "功能 ID 无法形成安全的基线目录")
	}
	files := []any{}
	payload := map[string][]byte{}
	var storedPaths safefs.PathSet
	total := 0
	for _, ref := range refs {
		bytes, e := s.bytes(ref)
		if e != nil {
			return nil, nil, e
		}
		total += len(bytes)
		if e = specBaselineLimits(s, len(refs), total); e != nil {
			return nil, nil, e
		}
		stored := baselineFileRef(ref)
		if e = storedPaths.Add(stored); e != nil {
			return nil, nil, e
		}
		payload[stored] = bytes
		files = append(files, map[string]any{"path": stored, "original_ref": ref, "sha256": "sha256:" + safefs.Digest(bytes), "size_bytes": len(bytes)})
	}
	manifest := map[string]any{"schema_version": 1, "kind": "spec-baseline", "baseline_id": id, "version": version, "source": source, "files": files}
	manifest["bundle_digest"] = contractDigest(manifest)
	if err = s.validateSchema(specBaselineSchema, manifest); err != nil {
		return nil, nil, err
	}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	payload["manifest.json"] = append(b, '\n')
	return manifest, payload, nil
}

func openSpecBaseline(s *semanticSession, prefix string) (map[string]any, *semanticSession, error) {
	if !contractPath(prefix) {
		return nil, nil, s.reject("SPEC_BASELINE_PATH", "基线包路径必须受当前读取根约束")
	}
	manifest, err := s.doc(path.Join(prefix, "manifest.json"))
	if err != nil {
		return nil, nil, err
	}
	if err = s.validateSchema(specBaselineSchema, manifest); err != nil {
		return nil, nil, err
	}
	if text(manifest["bundle_digest"]) != contractDigest(contractWithout(manifest, "bundle_digest")) {
		return nil, nil, s.reject("SPEC_BASELINE_DIGEST", "基线清单摘要不匹配")
	}
	expected := []string{"manifest.json"}
	aliases := map[string]string{}
	var names safefs.PathSet
	total := 0
	for _, v := range semList(manifest["files"]) {
		row := semMap(v)
		ref, original := text(row["path"]), text(row["original_ref"])
		for _, candidate := range []string{ref, original} {
			if err = rejectProgressionEvidence(s, candidate); err != nil {
				return nil, nil, err
			}
		}
		if !contractPath(original) || ref != baselineFileRef(original) {
			return nil, nil, s.reject("SPEC_BASELINE_PATH", "基线文件映射非法")
		}
		if err = names.Add(ref); err != nil {
			return nil, nil, err
		}
		if _, exists := aliases[original]; exists {
			return nil, nil, s.reject("SPEC_BASELINE_PATH", "原始引用重复")
		}
		b, e := s.bytes(path.Join(prefix, ref))
		if e != nil {
			return nil, nil, e
		}
		total += len(b)
		if len(b) != contractN(row["size_bytes"]) || "sha256:"+safefs.Digest(b) != text(row["sha256"]) {
			return nil, nil, s.reject("SPEC_BASELINE_DIGEST", "基线原始字节已变化: "+original)
		}
		expected = append(expected, ref)
		aliases[original] = strings.TrimPrefix(ref, "payload/files/")
	}
	if e := specBaselineLimits(s, len(expected)-1, total); e != nil {
		return nil, nil, e
	}
	actual, e := s.scan(prefix)
	if e != nil {
		return nil, nil, e
	}
	for i, ref := range actual {
		actual[i] = strings.TrimPrefix(ref, prefix+"/")
	}
	if !contractSame(contractSorted(actual), contractSorted(expected)) {
		return nil, nil, s.reject("SPEC_BASELINE_CLOSURE", "基线文件清单不完整或含额外文件")
	}
	for original, stored := range contractCopyStrings(aliases) {
		if original == "CONTEXT.md" {
			continue
		}
		for a, b := path.Dir(original), path.Dir(stored); a != "." && b != "."; a, b = path.Dir(a), path.Dir(b) {
			if old, ok := aliases[a]; ok && old != b {
				return nil, nil, s.reject("SPEC_BASELINE_PATH", "基线目录映射冲突")
			}
			aliases[a] = b
		}
	}
	source, e := s.sourceSnapshotSession(path.Join(prefix, "payload/files"), aliases)
	if e != nil {
		return nil, nil, e
	}
	originalSource, refs, e := verifySpecBaselineSource(source, text(semMap(manifest["source"])["checkpoint_ref"]))
	if e != nil {
		return nil, nil, e
	}
	if !contractSame(originalSource, manifest["source"]) {
		return nil, nil, s.reject("SPEC_BASELINE_SOURCE", "基线来源身份或当前批准依据不一致")
	}
	if manifest["baseline_id"] != "spec-baseline."+strings.TrimPrefix(text(originalSource["feature_id"]), "feature.") {
		return nil, nil, s.reject("SPEC_BASELINE_SOURCE", "基线身份未绑定来源功能")
	}

	originals := []string{}
	for _, v := range semList(manifest["files"]) {
		originals = append(originals, text(semMap(v)["original_ref"]))
	}
	if !contractSame(contractSorted(refs), contractSorted(originals)) {
		return nil, nil, s.reject("SPEC_BASELINE_CLOSURE", "基线依赖闭包与来源不一致")
	}
	return manifest, source, nil
}
func contractCopyStrings(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func verifySpecBaselineReceipt(s *semanticSession, ref string, reconcile bool) (map[string]any, *semanticSession, error) {
	receipt, err := s.doc(ref)
	if err != nil {
		return nil, nil, err
	}
	if err = s.validateSchema(specBaselineReceiptSchema, receipt); err != nil {
		return nil, nil, err
	}
	profile, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return nil, nil, err
	}
	if profile["profile_id"] != specBaselineProfile || receipt["target_profile_id"] != profile["profile_id"] {
		return nil, nil, s.reject("SPEC_BASELINE_TARGET", "Spec 基线仅允许独立 Design 接收")
	}
	base := path.Join("docs/spec-baselines", text(receipt["baseline_id"]), text(receipt["version"]))
	if ref != base+"/receipt.json" || receipt["package_ref"] != base+"/package" || receipt["working_set_ref"] != base+"/working-set.json" {
		return nil, nil, s.reject("SPEC_BASELINE_PATH", "接收记录路径或版本布局非法")
	}
	manifest, source, err := openSpecBaseline(s, text(receipt["package_ref"]))
	if err != nil {
		return nil, nil, err
	}
	if receipt["baseline_id"] != manifest["baseline_id"] || receipt["version"] != manifest["version"] || receipt["bundle_digest"] != manifest["bundle_digest"] {
		return nil, nil, s.reject("SPEC_BASELINE_DIGEST", "接收记录与来源包身份冲突")
	}
	if err = verifySpecBaselineWorkingSet(s, receipt, manifest, source); err != nil {
		return nil, nil, err
	}

	if reconcile {
		cpRef := s.checkpointRef
		if cpRef == "" {
			return nil, nil, s.reject("SPEC_BASELINE_CONTEXT", "承接必须指定目标自己的 checkpoint 和 Context 对账")
		}
		cp, e := s.doc(cpRef)
		if e != nil {
			return nil, nil, e
		}
		if e = s.validateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); e != nil {
			return nil, nil, e
		}
		binding := semMap(cp["upstream_spec_baseline"])
		bytes, e := s.bytes(ref)
		if e != nil {
			return nil, nil, e
		}
		if binding["receipt_ref"] != ref || strings.TrimPrefix(text(binding["receipt_digest"]), "sha256:") != safefs.Digest(bytes) {
			return nil, nil, s.reject("SPEC_BASELINE_DIGEST", "checkpoint 接收记录绑定过期")
		}
		if cp["feature_id"] != semMap(manifest["source"])["feature_id"] {
			return nil, nil, s.reject("SPEC_BASELINE_TARGET", "目标功能身份与来源不一致")
		}
		recon := semMap(cp["context_reconciliation"])
		if recon["status"] != "reconciled" || text(recon["ref"]) == "" {
			return nil, nil, s.reject("SPEC_BASELINE_CONTEXT", "目标 Context 尚未对账")
		}
		if e = s.verify("context-reconciliation", text(recon["ref"]), map[string]string{}); e != nil {
			return nil, nil, e
		}
		localSpecApproved := false
		if semMap(semMap(cp["gates"])["gate.spec-baseline-approved"])["status"] == "approved" && semMap(semMap(cp["gates"])["gate.plan-approved"])["status"] == "approved" {
			localSpecRef := checkpointAsset(cp, "spec_ref", "artifact.spec", "artifact.spec-baseline")
			if localSpecRef == "" {
				return nil, nil, s.reject("SPEC_BASELINE_SCOPE", "本地Spec批准缺少当前Spec资产")
			}
			if e = s.gateChecks(cp, cpRef, "gate.plan-approved"); e != nil {
				return nil, nil, e
			}
			if e = s.gateChecks(cp, cpRef, "gate.spec-baseline-approved"); e != nil {
				return nil, nil, e
			}
			if e = specBaselineAssetCoverage(s, cp, "gate.spec-baseline-approved", localSpecRef); e != nil {
				return nil, nil, e
			}
			localSpecApproved = true
		}
		if !localSpecApproved {
			if e = specBaselineContextEvidence(s, text(recon["ref"]), ref, text(receipt["package_ref"])); e != nil {
				return nil, nil, e
			}
		}
	}
	return manifest, source, nil
}

func verifySpecBaselineWorkingSet(s *semanticSession, receipt, manifest map[string]any, source *semanticSession) error {
	working, err := s.doc(text(receipt["working_set_ref"]))
	if err != nil {
		return err
	}
	if contractN(working["schema_version"]) != 1 || working["kind"] != "spec-baseline-working-set" || working["baseline_id"] != manifest["baseline_id"] || working["version"] != manifest["version"] {
		return s.reject("SPEC_BASELINE_WORKING_SET", "接入工作集身份或版本不一致")
	}
	base := path.Dir(text(receipt["working_set_ref"]))
	setRef := text(semMap(manifest["source"])["business_ticket_set_ref"])
	mutable := map[string]bool{}
	if setRef != "" {
		set, e := source.doc(setRef)
		if e != nil {
			return e
		}
		mutable[setRef] = true
		for _, row := range semList(set["tickets"]) {
			mutable[text(semMap(row)["ref"])] = true
		}
	}
	assets := semMap(working["assets"])
	if len(assets) != len(semList(manifest["files"])) {
		return s.reject("SPEC_BASELINE_WORKING_SET", "工作集资产集合不完整或包含额外映射")
	}
	for _, row := range semList(manifest["files"]) {
		original := text(semMap(row)["original_ref"])
		expected := path.Join(text(receipt["package_ref"]), baselineFileRef(original))
		if mutable[original] {
			expected = path.Join(base, "working-files", original)
		}
		if assets[original] != expected {
			return s.reject("SPEC_BASELINE_WORKING_SET", "工作集资产映射越出批准接入范围: "+original)
		}
		if mutable[original] {
			if _, e := s.bytes(expected); e != nil {
				return e
			}
		}
	}
	expectedSet := ""
	if setRef != "" {
		expectedSet = path.Join(base, "working-files", setRef)
	}
	if working["business_ticket_set_ref"] != expectedSet {
		return s.reject("SPEC_BASELINE_WORKING_SET", "工作集业务票集合引用非法")
	}
	return nil
}

func ValidateSpecBaselineImportTransaction(root, expectedProfile string, refs []string) error {
	if expectedProfile != "design" && expectedProfile != specBaselineProfile {
		return domain.Fail("TRANSACTION_PROFILE", "Spec 导入只允许 Design")
	}
	pattern := regexp.MustCompile(`^docs/spec-baselines/spec-baseline\.[A-Za-z0-9][A-Za-z0-9._-]*/v[1-9][0-9]*/(?:package/(?:manifest\.json|payload/files/.+)|working-files/.+|receipt\.json|working-set\.json|context-reconciliation\.draft\.yaml)$`)
	base := ""
	for _, ref := range refs {
		if !contractPath(ref) || !pattern.MatchString(ref) {
			return domain.Fail("TRANSACTION_SCOPE", "Spec 导入归档包含非法写入: "+ref)
		}
		if _, e := safefs.Path(root, ref); e != nil {
			return e
		}
		parts := strings.SplitN(ref, "/", 5)
		prefix := strings.Join(parts[:4], "/")
		if base != "" && base != prefix {
			return domain.Fail("TRANSACTION_SCOPE", "一次导入只能写入一个基线版本")
		}
		base = prefix
	}
	return nil
}

type specBaselineImportPlan struct {
	SchemaVersion int                          `json:"schema_version"`
	Kind          string                       `json:"kind"`
	Root          string                       `json:"root"`
	PackageRoot   string                       `json:"package_root"`
	Digest        string                       `json:"digest"`
	BaselineID    string                       `json:"baseline_id"`
	Version       string                       `json:"version"`
	Operations    []transaction.Operation      `json:"operations"`
	Guards        map[string]domain.Descriptor `json:"guards"`
	SourceInputs  []SemanticInput              `json:"source_inputs"`
	Reused        bool                         `json:"reused"`
}

func baselinePlanDigest(p specBaselineImportPlan) string {
	p.Digest = ""
	b, _ := json.Marshal(p)
	return "sha256:" + safefs.Digest(b)
}

func buildSpecBaselineImport(ctx context.Context, root, packageRoot string) (specBaselineImportPlan, error) {
	p := specBaselineImportPlan{SchemaVersion: 1, Kind: "spec-baseline-import", Root: root, PackageRoot: packageRoot, Operations: []transaction.Operation{}, Guards: map[string]domain.Descriptor{}}
	s := newSemanticSession(ctx, root, nil)
	if e := s.authorities(); e != nil {
		return p, e
	}
	identity, e := s.doc("yss-project.yaml")
	if e != nil {
		return p, e
	}
	profile, e := s.doc(".template-spec/process/harness-profile.yaml")
	if e != nil {
		return p, e
	}
	if identity["repository_mode"] != "project-instance" || profile["profile_id"] != specBaselineProfile {
		return p, s.reject("SPEC_BASELINE_TARGET", "导入目标必须是独立 Design 项目实例")
	}
	contextBytes, e := s.bytes("CONTEXT.md")
	if e != nil {
		return p, e
	}
	// External packages are a separate readonly observation root. They never grant
	// the target transaction permission to write outside its own root.
	pkg := newSemanticSession(ctx, filepath.Dir(packageRoot), nil)
	pkg.ruleSession = s
	packagePrefix := filepath.Base(packageRoot)
	manifest, _, e := openSpecBaseline(pkg, packagePrefix)
	if e != nil {
		return p, e
	}
	p.BaselineID, p.Version = text(manifest["baseline_id"]), text(manifest["version"])
	base := path.Join("docs/spec-baselines", p.BaselineID, p.Version)
	receiptRef := base + "/receipt.json"
	exists, e := s.exists(receiptRef)
	if e != nil {
		return p, e
	}
	if exists {
		old, _, err := verifySpecBaselineReceipt(s, receiptRef, false)
		if err != nil {
			return p, err
		}
		if old["bundle_digest"] != manifest["bundle_digest"] {
			return p, s.reject("SPEC_BASELINE_VERSION_CONFLICT", "同一来源身份及版本已有不同内容")
		}
		p.Reused = true
	} else {
		files, e := pkg.scan(packagePrefix)
		if e != nil {
			return p, e
		}
		for _, ref := range files {
			b, e := pkg.bytes(ref)
			if e != nil {
				return p, e
			}
			target := base + "/package/" + strings.TrimPrefix(ref, packagePrefix+"/")
			if present, e := s.exists(target); e != nil {
				return p, e
			} else if present {
				return p, s.reject("SPEC_BASELINE_VERSION_CONFLICT", "基线目标已存在且没有可核验接收记录")
			}
			p.Operations = append(p.Operations, transaction.Operation{Path: target, Data: b, Mode: 0444})
		}
		packageFiles := newSemanticSession(ctx, packageRoot, nil)
		working, workingOps, err := specBaselineWorkingSet(packageFiles, manifest, base)
		if err != nil {
			return p, err
		}
		p.Operations = append(p.Operations, workingOps...)
		b, _ := json.MarshalIndent(working, "", "  ")
		p.Operations = append(p.Operations, transaction.Operation{Path: base + "/working-set.json", Data: append(b, '\n'), Mode: 0644})
		receipt := map[string]any{"schema_version": 1, "kind": "spec-baseline-import", "baseline_id": p.BaselineID, "version": p.Version, "bundle_digest": manifest["bundle_digest"], "package_ref": base + "/package", "target_profile_id": specBaselineProfile, "target_context_digest": "sha256:" + safefs.Digest(contextBytes), "status": "imported-pending-context-reconciliation", "ready_for_agent": false, "working_set_ref": base + "/working-set.json"}
		if e = s.validateSchema(specBaselineReceiptSchema, receipt); e != nil {
			return p, e
		}
		b, _ = json.MarshalIndent(receipt, "", "  ")
		p.Operations = append(p.Operations, transaction.Operation{Path: receiptRef, Data: append(b, '\n'), Mode: 0644})
		draft := map[string]any{"schema_version": 1, "status": "draft", "source_context_ref": base + "/package/payload/files/source-context.snapshot.md", "target_context_ref": "CONTEXT.md", "source_context_digest": func() string {
			for _, v := range semList(manifest["files"]) {
				m := semMap(v)
				if m["original_ref"] == "CONTEXT.md" {
					return text(m["sha256"])
				}
			}
			return ""
		}(), "target_context_digest": "sha256:" + safefs.Digest(contextBytes), "import_receipt_ref": receiptRef, "instruction": "核验来源与目标词汇、显式解决冲突后创建目标 Context 对账记录；此草案不授予执行资格"}
		b, _ = json.MarshalIndent(draft, "", "  ")
		p.Operations = append(p.Operations, transaction.Operation{Path: base + "/context-reconciliation.draft.yaml", Data: append(b, '\n'), Mode: 0644})
	}
	if e = s.finish(); e != nil {
		return p, e
	}
	if e = pkg.finish(); e != nil {
		return p, e
	}
	for _, input := range s.inputs() {
		if input.Root == root && !strings.HasPrefix(input.Ref, ".yss/transactions") {
			p.Guards[input.Ref] = input.Descriptor
		}
	}
	p.SourceInputs = pkg.inputs()
	sort.Slice(p.Operations, func(i, j int) bool { return p.Operations[i].Path < p.Operations[j].Path })
	paths := []string{}
	for _, op := range p.Operations {
		paths = append(paths, op.Path)
	}
	if e = ValidateSpecBaselineImportTransaction(root, "design", paths); e != nil {
		return p, e
	}
	p.Digest = baselinePlanDigest(p)
	return p, nil
}

func saveBaselineJSON(out string, value any) error {
	if out == "" {
		return domain.Fail("ARGUMENT", "需要 --out 新文件路径")
	}
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(append(b, '\n'))
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}

func specBaselineRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	allowed := map[string]bool{"root": true, "profile": true, "target-dir": true, "json": true, "kind": true}
	for _, key := range map[string][]string{"export": {"checkpoint", "out"}, "import": {"package", "plan", "out", "apply", "plan-file"}, "verify": {"package", "file", "checkpoint"}}[action] {
		allowed[key] = true
	}
	for key := range args {
		if !allowed[key] {
			return nil, domain.Fail("ARGUMENT", "Spec 基线接口不接受参数: --"+key)
		}
	}
	if action == "verify" && args["package"] != "" && args["file"] != "" {
		return nil, domain.Fail("ARGUMENT", "--package 与 --file 互斥")
	}
	s := newSemanticSession(ctx, root, args)
	switch action {
	case "export":
		if args["checkpoint"] == "" || args["out"] == "" {
			return nil, domain.Fail("ARGUMENT", "导出需要 --checkpoint 与 --out")
		}
		manifest, payload, e := specBaselineManifest(s, args["checkpoint"])
		if e != nil {
			return specBaselineReport(s, args["checkpoint"], e)
		}
		if e = s.finish(); e != nil {
			return specBaselineReport(s, args["checkpoint"], e)
		}
		destination, e := filepath.Abs(args["out"])
		if e != nil {
			return nil, e
		}
		if _, e = safefs.Path(filepath.Dir(destination), filepath.Base(destination)); e != nil {
			return nil, e
		}
		if e = os.Mkdir(destination, 0755); e != nil {
			return nil, e
		}
		for ref, b := range payload {
			file, e := safefs.Path(destination, ref)
			if e != nil {
				return nil, e
			}
			if e = os.MkdirAll(filepath.Dir(file), 0755); e != nil {
				return nil, e
			}
			f, e := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
			if e != nil {
				return nil, e
			}
			_, e = f.Write(b)
			closeErr := f.Close()
			if e != nil {
				return nil, e
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
		if relativeOutput, relErr := filepath.Rel(root, destination); relErr == nil && relativeOutput != "." && !strings.HasPrefix(relativeOutput, ".."+string(filepath.Separator)) && relativeOutput != ".." {
			inventory, scanErr := s.contextFiles()
			if scanErr != nil {
				return specBaselineReport(s, args["checkpoint"], scanErr)
			}
			previous := map[string]bool{}
			for _, ref := range s.contextInventory {
				previous[ref] = true
			}
			prefix := filepath.ToSlash(relativeOutput)
			for _, ref := range inventory {
				if previous[ref] {
					delete(previous, ref)
					continue
				}
				if ref != prefix && !strings.HasPrefix(ref, prefix+"/") {
					return specBaselineReport(s, args["checkpoint"], s.unavailable("INPUT_DRIFT", "导出期间源工程新增了包输出范围外的输入: "+ref))
				}
			}
			if len(previous) > 0 {
				return specBaselineReport(s, args["checkpoint"], s.unavailable("INPUT_DRIFT", "导出期间源工程输入集合减少"))
			}
			s.contextInventory = inventory
		}
		if e = s.finish(); e != nil {
			return specBaselineReport(s, args["checkpoint"], e)
		}
		return map[string]any{"kind": "spec-baseline", "status": "exported", "package_root": destination, "baseline_id": manifest["baseline_id"], "version": manifest["version"], "bundle_digest": manifest["bundle_digest"]}, nil
	case "import":
		if args["apply"] == "true" {
			if args["plan-file"] == "" || args["package"] != "" || args["plan"] != "" {
				return nil, domain.Fail("ARGUMENT", "应用只消费 --plan-file 已保存导入计划")
			}
			bytes, e := os.ReadFile(args["plan-file"])
			if e != nil {
				return nil, e
			}
			var plan specBaselineImportPlan
			if e = json.Unmarshal(bytes, &plan); e != nil {
				return nil, e
			}
			if plan.SchemaVersion != 1 || plan.Kind != "spec-baseline-import" || plan.Root != root || plan.Digest != baselinePlanDigest(plan) {
				return nil, domain.Fail("PLAN", "导入计划身份或摘要非法")
			}
			rebuilt, e := buildSpecBaselineImport(ctx, root, plan.PackageRoot)
			if e != nil {
				return nil, e
			}
			if rebuilt.Digest != plan.Digest {
				return nil, domain.Fail("INPUT_DRIFT", "Spec 导入计划输入已变化；重新保存计划")
			}
			if plan.Reused {
				return map[string]any{"status": "reused", "receipt_ref": path.Join("docs/spec-baselines", plan.BaselineID, plan.Version, "receipt.json")}, nil
			}
			recheck := func() error {
				for _, input := range plan.SourceInputs {
					actual, e := safefs.Describe(input.Root, input.Ref)
					if e != nil {
						return e
					}
					if actual != input.Descriptor {
						return domain.Fail("INPUT_DRIFT", "基线包在写入期间变化")
					}
				}
				packageSession := newSemanticSession(ctx, filepath.Dir(plan.PackageRoot), nil)
				packageSession.ruleSession = newSemanticSession(ctx, root, nil)
				if _, _, err := openSpecBaseline(packageSession, filepath.Base(plan.PackageRoot)); err != nil {
					return err
				}
				if err := packageSession.finish(); err != nil {
					return err
				}
				return nil
			}
			if e = recheck(); e != nil {
				return nil, e
			}
			result, e := transaction.ApplyContextWithValidation(ctx, root, "spec-baseline-import", plan.Operations, plan.Guards, nil, func() error {
				if e := recheck(); e != nil {
					return e
				}
				target := newSemanticSession(ctx, root, nil)
				_, _, e := verifySpecBaselineReceipt(target, path.Join("docs/spec-baselines", plan.BaselineID, plan.Version, "receipt.json"), false)
				if e != nil {
					return e
				}
				return target.finish()
			})
			return result, e
		}
		if args["plan"] != "true" || args["package"] == "" {
			return nil, domain.Fail("ARGUMENT", "导入先用 --package --plan --out 保存计划")
		}
		packageRoot, e := filepath.Abs(args["package"])
		if e != nil {
			return nil, e
		}
		plan, e := buildSpecBaselineImport(ctx, root, packageRoot)
		if e != nil {
			return nil, e
		}
		if e = saveBaselineJSON(args["out"], plan); e != nil {
			return nil, e
		}
		return plan, nil
	case "verify":
		if e := s.authorities(); e != nil {
			return specBaselineReport(s, "", e)
		}
		ref := first(args["file"], args["package"])
		var e error
		if args["package"] != "" {
			packageRoot, err := filepath.Abs(args["package"])
			if err != nil {
				return nil, err
			}
			pkg := newSemanticSession(ctx, filepath.Dir(packageRoot), nil)
			pkg.ruleSession = s
			_, _, e = openSpecBaseline(pkg, filepath.Base(packageRoot))
			s.children = append(s.children, pkg)
		} else if args["file"] != "" {
			_, _, e = verifySpecBaselineReceipt(s, ref, args["checkpoint"] != "")
		} else {
			e = domain.Fail("ARGUMENT", "核验需 --package 或 --file")
		}
		return specBaselineReport(s, ref, e)
	}
	return nil, fmt.Errorf("不支持的 Spec 基线动作: %s", action)
}

func verifyInheritedSpecCheckpoint(s *semanticSession, cpRef string, cp map[string]any) error {
	binding := semMap(cp["upstream_spec_baseline"])
	old := s.checkpointRef
	s.checkpointRef = cpRef
	defer func() { s.checkpointRef = old }()
	manifest, _, err := verifySpecBaselineReceipt(s, text(binding["receipt_ref"]), true)
	if err != nil {
		return err
	}
	source := semMap(manifest["source"])
	for _, key := range []string{"spec_ref", "plan_ref", "domain_strategy_ref", "stage_decision_package_ref"} {
		ids := map[string][]string{"spec_ref": {"artifact.spec", "artifact.spec-baseline"}, "plan_ref": {"artifact.plan"}, "domain_strategy_ref": {"artifact.domain-strategy"}, "stage_decision_package_ref": {"artifact.stage-decision-package"}}
		ref := checkpointAsset(cp, key, ids[key]...)
		if ref == "" {
			continue
		}
		original := text(source[key])

		receipt, e := s.doc(text(binding["receipt_ref"]))
		if e != nil {
			return e
		}
		var originalBytes []byte
		if original != "" {
			originalBytes, e = s.bytes(path.Join(text(receipt["package_ref"]), baselineFileRef(original)))
			if e != nil {
				return e
			}
		}
		targetBytes, e := s.bytes(ref)
		if e != nil {
			return e
		}
		if original == "" || safefs.Digest(originalBytes) != safefs.Digest(targetBytes) {
			gate := "gate.plan-approved"
			if key == "spec_ref" {
				gate = "gate.spec-baseline-approved"
			}
			if semMap(semMap(cp["gates"])[gate])["status"] != "approved" {
				return s.reject("SPEC_BASELINE_SCOPE", "目标 Plan/Spec 业务语义已改变，须恢复本地批准流程")
			}
			if err := s.gateChecks(cp, cpRef, gate); err != nil {
				return err
			}
			if err := specBaselineAssetCoverage(s, cp, gate, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

func inheritedSourceApproval(s *semanticSession, gate string, binding map[string]any, approvalRef string) (bool, error) {
	marker := semMap(semMap(binding["approval_context"])["source_baseline"])
	if len(marker) == 0 {
		return false, nil
	}
	receiptRef := text(marker["receipt_ref"])
	receiptBytes, err := s.bytes(receiptRef)
	if err != nil {
		return true, err
	}
	if strings.TrimPrefix(text(marker["receipt_digest"]), "sha256:") != safefs.Digest(receiptBytes) {
		return true, s.reject("SPEC_BASELINE_DIGEST", "来源批准接收记录绑定过期")
	}
	manifest, source, err := verifySpecBaselineReceipt(s, receiptRef, false)
	if err != nil {
		return true, err
	}
	receipt, err := s.doc(receiptRef)
	if err != nil {
		return true, err
	}
	reconRef := text(marker["context_reconciliation_ref"])
	if reconRef == "" {
		return true, s.reject("SPEC_BASELINE_CONTEXT", "继承来源批准缺少目标 Context 对账绑定")
	}
	if err = s.verify("context-reconciliation", reconRef, map[string]string{}); err != nil {
		return true, err
	}
	if err = specBaselineContextEvidence(s, reconRef, receiptRef, text(receipt["package_ref"])); err != nil {
		return true, err
	}
	sourceRef := text(marker["source_ref"])
	expectedGate := ""
	for _, key := range []string{"spec_ref", "plan_ref", "domain_strategy_ref", "stage_decision_package_ref"} {
		if semMap(manifest["source"])[key] == sourceRef {
			expectedGate = "gate.plan-approved"
			if key == "spec_ref" {
				expectedGate = "gate.spec-baseline-approved"
			}
		}
	}
	if expectedGate == "" || gate != expectedGate {
		return true, s.reject("SPEC_BASELINE_SCOPE", "继承的来源批准不属于当前资产或边界")
	}
	expectedRef := path.Join(text(receipt["package_ref"]), baselineFileRef(sourceRef))
	if binding["ref"] != expectedRef {
		return true, s.reject("SPEC_BASELINE_SCOPE", "继承批准只允许原始冻结资产")
	}
	targetBytes, err := s.bytes(expectedRef)
	if err != nil {
		return true, err
	}
	sourceBytes, err := source.bytes(sourceRef)
	if err != nil {
		return true, err
	}
	if safefs.Digest(targetBytes) != safefs.Digest(sourceBytes) {
		return true, s.reject("SPEC_BASELINE_DIGEST", "继承资产字节与来源不一致")
	}
	cp, err := source.doc(text(semMap(manifest["source"])["checkpoint_ref"]))
	if err != nil {
		return true, err
	}
	gateState := semMap(semMap(cp["gates"])[gate])
	expectedApprovalRef := path.Join(text(receipt["package_ref"]), baselineFileRef(text(gateState["approval_ref"])))
	if approvalRef != expectedApprovalRef {
		return true, s.reject("SPEC_BASELINE_SCOPE", "来源审批记录不属于冻结批准")
	}
	return true, nil
}

func specBaselineWorkingSet(pkg *semanticSession, manifest map[string]any, base string) (map[string]any, []transaction.Operation, error) {
	assets := map[string]any{}
	for _, v := range semList(manifest["files"]) {
		original := text(semMap(v)["original_ref"])
		assets[original] = base + "/package/" + baselineFileRef(original)
	}
	setRef := text(semMap(manifest["source"])["business_ticket_set_ref"])
	mutable := []string{}
	if setRef != "" {
		set, e := pkg.doc(baselineFileRef(setRef))
		if e != nil {
			return nil, nil, e
		}
		for _, v := range semList(set["tickets"]) {
			mutable = append(mutable, text(semMap(v)["ref"]))
		}
		mutable = append(mutable, setRef)
	}
	for _, ref := range mutable {
		if assets[ref] == nil {
			return nil, nil, pkg.reject("SPEC_BASELINE_CLOSURE", "业务票不在冻结闭包")
		}
		assets[ref] = base + "/working-files/" + ref
	}
	rewritten := map[string][]byte{}
	ops := []transaction.Operation{}
	for _, ref := range mutable {
		raw, e := pkg.bytes(baselineFileRef(ref))
		if e != nil {
			return nil, nil, e
		}
		docBytes, body := raw, []byte(nil)
		markdown := path.Ext(ref) == ".md"
		if markdown {
			docBytes, body, e = frontmatter(raw)
			if e != nil {
				return nil, nil, e
			}
			body, e = specBaselineMarkdownLinks(body, ref, text(assets[ref]), assets)
			if e != nil {
				return nil, nil, e
			}
		}
		value, e := schema.Parse(docBytes)
		if e != nil {
			return nil, nil, e
		}
		var rebind func(any) any
		rebind = func(v any) any {
			switch x := v.(type) {
			case string:
				if mapped, ok := assets[x]; ok {
					return mapped
				}
				return x
			case []any:
				for i, v := range x {
					x[i] = rebind(v)
				}
			case map[string]any:
				for key, v := range x {
					x[key] = rebind(v)
				}
				if sourceRef := text(x["ref"]); sourceRef != "" {
					for original, mapped := range assets {
						if mapped == sourceRef && rewritten[original] != nil {
							x["digest"] = "sha256:" + safefs.Digest(rewritten[original])
							break
						}
					}
				}
			}
			return v
		}
		value = rebind(value)
		var b []byte
		if markdown {
			b, e = json.MarshalIndent(value, "", "  ")
			b = append(append(append([]byte("---\n"), b...), []byte("\n---")...), body...)
		} else {
			b, e = json.MarshalIndent(value, "", "  ")
			b = append(b, '\n')
		}
		if e != nil {
			return nil, nil, e
		}
		rewritten[ref] = b
		ops = append(ops, transaction.Operation{Path: text(assets[ref]), Data: b, Mode: 0644})
	}
	working := map[string]any{"schema_version": 1, "kind": "spec-baseline-working-set", "baseline_id": manifest["baseline_id"], "version": manifest["version"], "assets": assets, "business_ticket_set_ref": assets[setRef]}
	if setRef == "" {
		working["business_ticket_set_ref"] = ""
	}
	return working, ops, nil
}

// Editable Tickets keep their prose while local links follow the same approved
// asset mapping as frontmatter references. The frozen source bytes stay intact.
func specBaselineMarkdownLinks(body []byte, originalRef, targetRef string, assets map[string]any) ([]byte, error) {
	pattern := regexp.MustCompile(`!?\[[^\]]*\]\(<?([^\s)>]+)>?(?:\s+[^)]*)?\)`)
	out, last := []byte{}, 0
	for _, match := range pattern.FindAllSubmatchIndex(body, -1) {
		href := string(body[match[2]:match[3]])
		dependency, err := contractLocalDependency(originalRef, href)
		if err != nil {
			return nil, err
		}
		mapped := text(assets[dependency])
		if mapped == "" {
			continue
		}
		relative, err := filepath.Rel(filepath.FromSlash(path.Dir(targetRef)), filepath.FromSlash(mapped))
		if err != nil {
			return nil, err
		}
		address := &url.URL{Path: filepath.ToSlash(relative)}
		newHref := address.EscapedPath()
		if suffix := strings.IndexAny(href, "?#"); suffix >= 0 {
			newHref += href[suffix:]
		}
		out = append(out, body[last:match[2]]...)
		out = append(out, newHref...)
		last = match[3]
	}
	return append(out, body[last:]...), nil
}

func specBaselineContextEvidence(s *semanticSession, reconRef, receiptRef, packageRef string) error {
	recon, err := s.doc(reconRef)
	if err != nil {
		return err
	}
	evidence := semStrings(recon["evidence_refs"])
	for _, ref := range []string{receiptRef, path.Join(packageRef, "payload/files/source-context.snapshot.md")} {
		if !semHas(evidence, ref) {
			return s.reject("SPEC_BASELINE_CONTEXT", "Context 对账未绑定来源接收记录和冻结词汇: "+ref)
		}
	}
	manifest, err := s.doc(path.Join(packageRef, "manifest.json"))
	if err != nil {
		return err
	}
	sourceBytes, err := s.bytes(path.Join(packageRef, "payload/files/source-context.snapshot.md"))
	if err != nil {
		return err
	}
	sourceContext, err := contextContractBytes(sourceBytes)
	if err != nil {
		return s.reject("SPEC_BASELINE_CONTEXT", err.Error())
	}
	targetContext, err := s.contextContract()
	if err != nil {
		return err
	}
	sourceTerms := map[string]Term{}
	targetTerms := map[string]Term{}
	for _, term := range sourceContext["business_terms"].([]Term) {
		sourceTerms[term.TermRef] = term
	}
	for _, term := range targetContext["business_terms"].([]Term) {
		targetTerms[term.TermRef] = term
	}
	related := map[string]bool{}
	var collect func(any)
	collect = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for _, ref := range semStrings(semMap(v["context_snapshot"])["term_refs"]) {
				related[ref] = true
			}
			for _, child := range v {
				collect(child)
			}
		case []any:
			for _, child := range v {
				collect(child)
			}
		}
	}
	refs := []string{}
	for _, key := range []string{"checkpoint_ref", "spec_ref", "plan_ref", "domain_strategy_ref", "stage_decision_package_ref", "business_ticket_set_ref"} {
		if ref := text(semMap(manifest["source"])[key]); ref != "" {
			refs = append(refs, ref)
		}
	}
	for _, ref := range refs {
		frozenRef := path.Join(packageRef, baselineFileRef(ref))
		raw, e := s.bytes(frozenRef)
		if e != nil {
			return e
		}
		if path.Ext(ref) == ".md" && !strings.HasPrefix(string(raw), "---\n") && !strings.HasPrefix(string(raw), "---\r\n") {
			continue
		}
		doc, e := s.doc(frozenRef)
		if e != nil {
			return e
		}
		collect(doc)
		if ref == text(semMap(manifest["source"])["business_ticket_set_ref"]) {
			for _, row := range semList(doc["tickets"]) {
				ticket, e := s.doc(path.Join(packageRef, baselineFileRef(text(semMap(row)["ref"]))))
				if e != nil {
					return e
				}
				collect(ticket)
			}
		}
	}
	if len(related) == 0 {
		for ref := range sourceTerms {
			related[ref] = true
		}
	}
	covered := semStrings(semMap(recon["context_snapshot"])["term_refs"])
	for ref := range related {
		expected, exists := sourceTerms[ref]
		actual, present := targetTerms[ref]
		if !exists || !present || !semHas(covered, ref) {
			return s.reject("SPEC_BASELINE_CONTEXT", "目标 Context 对账未覆盖来源相关术语: "+ref)
		}
		expectedDigest, e := termsDigest([]Term{expected})
		if e != nil {
			return e
		}
		actualDigest, e := termsDigest([]Term{actual})
		if e != nil {
			return e
		}
		if expectedDigest != actualDigest {
			return s.reject("SPEC_BASELINE_CONTEXT", "目标相关术语与来源业务含义冲突: "+ref)
		}
	}

	return nil
}

func specBaselineAssetCoverage(s *semanticSession, cp map[string]any, gate, ref string) error {
	state := semMap(semMap(cp["gates"])[gate])
	record, err := s.doc(text(state["approval_ref"]))
	if err != nil {
		return err
	}
	record, err = selectApprovalRecordSemantic(s, record, gate)
	if err != nil {
		return err
	}
	// gateChecks has verified this actual approval record and its subject.
	// A checkpoint's extra basis rows only declare inputs; they grant no scope.
	basis := semList(record["basis"])
	if gate == "gate.plan-approved" {
		// plan-spec-entry verified the review, its actual user decision and the
		// aggregate approval's coverage of every review basis row.
		review, err := s.doc(text(cp["plan_review_ref"]))
		if err != nil {
			return err
		}
		basis = semList(review["basis"])
	}
	bytes, err := s.bytes(ref)
	if err != nil {
		return err
	}
	if gate == "gate.spec-baseline-approved" && record["subject_ref"] == ref && strings.TrimPrefix(text(record["subject_digest"]), "sha256:") == safefs.Digest(bytes) {
		return nil
	}
	for _, row := range basis {
		binding := semMap(row)
		if binding["ref"] == ref && strings.TrimPrefix(text(binding["digest"]), "sha256:") == safefs.Digest(bytes) {
			return nil
		}
	}
	return s.reject("SPEC_BASELINE_SCOPE", "导出资产不在当前批准的实际依据范围: "+ref)
}

func specBaselineDesignAssets(s *semanticSession, strategyRef, stageRef string) error {
	documents := map[string]map[string]any{}
	for _, asset := range []struct{ label, ref string }{{"domain-strategy", strategyRef}, {"stage-decision-package", stageRef}} {
		if asset.ref == "" {
			continue
		}
		doc, err := s.doc(asset.ref)
		if err != nil {
			return err
		}
		version, valid := integer(doc["schema_version"])
		if !valid || (version != 2 && version != 3) {
			return s.reject("SPEC_BASELINE_SOURCE", "不支持的来源资产版本: "+asset.label)
		}
		suffix := ""
		if version == 3 {
			suffix = "-v3"
		}
		if err = s.validateSchema(".template-spec/process/schemas/strategic-handoff-"+asset.label+suffix+".schema.json", doc); err != nil {
			return err
		}
		if doc["status"] != "approved" {
			return s.reject("SPEC_BASELINE_SOURCE", "来源资产尚未批准: "+asset.ref)
		}
		if _, err = s.contextSnapshot(semMap(doc["context_snapshot"])); err != nil {
			return err
		}
		documents[asset.label] = doc
	}
	strategy, stage := documents["domain-strategy"], documents["stage-decision-package"]
	if stage != nil {
		if strategy == nil {
			return s.reject("SPEC_BASELINE_SOURCE", "当前方案决策缺少已登记的领域战略来源")
		}
		binding := semMap(stage["domain_strategy_ref"])
		if binding["domain_strategy_id"] != strategy["domain_strategy_id"] || binding["domain_version"] != strategy["domain_version"] || binding["status"] != "approved" || binding["persisted_ref"] != strategyRef || binding["digest"] != contractDigest(strategy) {
			return s.reject("SPEC_BASELINE_SOURCE", "当前方案决策未绑定本次批准的领域战略")
		}
	}
	return nil
}
