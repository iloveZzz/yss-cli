package handoff

import approvaldomain "github.com/iloveZzz/yss-cli/internal/governance/approval"

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func init() {
	kernel.RegisterSemanticValidator("handoff-package", verifyHandoffPackageSemantic)
	kernel.RegisterSemanticValidator("handoff-source", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		h, err := ContractInspectHandoff(s, ref)
		if err == nil {
			err = handoffSourceCoverage(s, s, ref, h)
		}
		return err
	})
	kernel.RegisterSemanticValidator("handoff-delivery", verifyStrategicDeliveryPublic)
	kernel.RegisterSemanticValidator("handoff-source-approval", verifySourceApprovalPublic)
	kernel.RegisterSemanticValidator("strategic-delivery", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		_, _, err := ContractOpenStrategicDelivery(s, ref)
		return err
	})
	kernel.RegisterSemanticValidator("handoff-consumption", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		doc, e := s.Doc(ref)
		if e != nil {
			return e
		}
		consumer := kernel.First(opts["consumer"], "tactical")
		version, ok := kernel.Integer(doc["schema_version"])
		if !ok || !(consumer == "tactical" && (version == 1 || version == 2)) {
			return s.Unavailable("CAPABILITY", "未知 Handoff 消费合同类型或版本")
		}
		return ContractHandoffConsumption(s, doc, opts)
	})
}

type NativeHandoff struct {
	Manifest, Handoff, Strategy, Stage, Config, Changes, Business map[string]any
	BusinessReport                                                map[string]any
	Rules, Scenarios                                              []any
	Source                                                        *kernel.SemanticSession
	Prefix                                                        string
}

func verifyHandoffPackageSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	h, e := ContractOpenHandoff(s, ref)
	if e == nil && h != nil {
		// 命令行薄壳需要的包身份摘要，与 Node 的 strategic-handoff verify 输出一致。
		if s.Report.Coverage == nil {
			s.Report.Coverage = map[string]any{}
		}
		e = handoffPackageCoverage(s, h)
	}
	return e
}

// Consume the published v1 strategic delivery wrapper as an observed directory.
// Neither its persisted command nor its optional archive is executed/extracted.
func ContractOpenStrategicDelivery(s *kernel.SemanticSession, recordRef string) (*NativeHandoff, map[string]any, error) {
	if path.Base(recordRef) != "delivery-record.json" || path.Dir(recordRef) == "." || !kernel.ContractPath(recordRef) {
		return nil, nil, s.Reject("HANDOFF_DELIVERY", "战略交付必须登记独立目录中的 delivery-record.json")
	}
	record, e := s.Doc(recordRef)
	if e != nil {
		return nil, nil, e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-delivery.schema.json", record); e != nil {
		return nil, nil, e
	}
	prefix := path.Dir(recordRef)
	report, e := s.Doc(path.Join(prefix, kernel.Text(record["verification_ref"])))
	if e != nil {
		return nil, nil, e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-delivery-verification.schema.json", report); e != nil {
		return nil, nil, e
	}
	zip, hasZIP := kernel.Object(record["zip"])
	if hasZIP {
		if _, e = dependencies.BackendBound(s, map[string]any{"ref": path.Join(prefix, kernel.Text(zip["ref"])), "digest": zip["sha256"]}); e != nil {
			return nil, nil, e
		}
	}
	files, e := s.Scan(prefix)
	if e != nil {
		return nil, nil, e
	}
	allowed := map[string]bool{recordRef: true, path.Join(prefix, "verification.json"): true}
	if hasZIP {
		allowed[path.Join(prefix, "package.zip")] = true
	}
	for _, ref := range files {
		if !strings.HasPrefix(ref, path.Join(prefix, "package")+"/") && !allowed[ref] {
			return nil, nil, s.Reject("HANDOFF_DELIVERY", "战略交付目录包含未登记文件: "+ref)
		}
	}
	bundle, e := ContractOpenHandoff(s, path.Join(prefix, kernel.Text(record["package_ref"])))
	if e != nil {
		return nil, nil, e
	}
	if report["bundle_digest"] != bundle.Manifest["bundle_digest"] || report["package_ref"] != record["package_ref"] || report["manifest_ref"] != record["manifest_ref"] {
		return nil, nil, s.Reject("HANDOFF_DELIVERY", "交付验证与当前包身份矛盾")
	}
	if e = contractDeliveryRecordIdentity(s, bundle, record); e != nil {
		return nil, nil, e
	}
	return bundle, record, nil
}

func contractDeliveryRecordIdentity(s *kernel.SemanticSession, bundle *NativeHandoff, record map[string]any) error {
	h := kernel.SemMap(record["handoff"])
	if h["id"] != bundle.Manifest["bundle_id"] || h["version"] != bundle.Manifest["version"] || h["schema_version"] != bundle.Handoff["schema_version"] || record["bundle_digest"] != bundle.Manifest["bundle_digest"] {
		return s.Reject("HANDOFF_DELIVERY", "交付记录、验证与当前包身份矛盾")
	}
	found := false
	for _, item := range kernel.SemList(bundle.Manifest["files"]) {
		row := kernel.SemMap(item)
		found = found || row["original_ref"] == h["ref"] && row["sha256"] == h["sha256"]
	}
	if !found {
		return s.Reject("HANDOFF_DELIVERY", "交付记录缺少 Handoff 原始字节绑定")
	}
	assets, keys := []any{}, []string{}
	for key := range kernel.SemMap(bundle.Handoff["source"]) {
		keys = append(keys, key)
	}
	order := collate.New(language.English)
	sort.SliceStable(keys, func(i, j int) bool { return order.CompareString(keys[i], keys[j]) < 0 })
	for _, key := range keys {
		v := kernel.SemMap(kernel.SemMap(bundle.Handoff["source"])[key])
		id := v["id"]
		if id == nil {
			id = v["baseline_id"]
		}
		assets = append(assets, map[string]any{"source_key": key, "id": id, "version": v["version"], "ref": v["persisted_ref"], "digest": v["digest"]})
	}
	routes := []any{}
	for _, item := range kernel.SemList(bundle.Handoff["consumer_routes"]) {
		v := kernel.SemMap(item)
		routes = append(routes, map[string]any{"route_id": v["route_id"], "capability": v["capability"], "activation": v["activation"]})
	}
	var previous any
	if prior, present := kernel.Object(bundle.Manifest["previous_bundle"]); present {
		previous = map[string]any{"handoff_id": prior["bundle_id"], "version": prior["version"], "bundle_digest": prior["digest"]}
	}
	if !dependencies.ContractSame(assets, record["source_assets"]) || !dependencies.ContractSame(routes, record["consumer_routes"]) || !dependencies.ContractSame(previous, record["previous_delivery"]) {
		return s.Reject("HANDOFF_DELIVERY", "交付记录来源、路由或上一版本与批准包不一致")
	}
	return nil
}

// Prepare the immutable view once: inventory, original bytes, layout and source
// signing policy are shared by full Handoff gates and readonly business reports.
func contractOpenHandoffSource(s *kernel.SemanticSession, prefix string) (*NativeHandoff, error) {
	manifest, e := s.Doc(path.Join(prefix, "manifest.json"))
	if e != nil {
		return nil, e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-package.schema.json", manifest); e != nil {
		return nil, e
	}
	if kernel.Text(manifest["bundle_digest"]) != dependencies.ContractDigest(dependencies.ContractWithout(manifest, "bundle_digest")) {
		return nil, s.Reject("HANDOFF_MANIFEST", "包清单摘要不一致")
	}
	names, originals := []string{"manifest.json"}, map[string]bool{}
	folds := map[string]bool{"manifest.json": true}
	fold := cases.Fold()
	total := 0
	for _, v := range kernel.SemList(manifest["files"]) {
		file := kernel.SemMap(v)
		ref := kernel.Text(file["path"])
		for _, candidate := range []string{ref, kernel.Text(file["original_ref"])} {
			if e = kernel.RejectProgressionEvidence(s, candidate); e != nil {
				return nil, e
			}
		}
		if !kernel.ContractPath(ref) || folds[fold.String(ref)] {
			return nil, s.Reject("HANDOFF_PATH", "包路径非法或大小写冲突")
		}
		folds[fold.String(ref)] = true
		names = append(names, ref)
		b, e := s.Bytes(path.Join(prefix, ref))
		if e != nil {
			return nil, e
		}
		if len(b) != kernel.ContractN(file["size_bytes"]) || "sha256:"+safefs.Digest(b) != kernel.Text(file["sha256"]) {
			return nil, s.Reject("HANDOFF_FILE_STALE", "文件摘要或大小不一致: "+ref)
		}
		total += len(b)
		if original := kernel.Text(file["original_ref"]); original != "" {
			if !kernel.ContractPath(original) || originals[fold.String(original)] {
				return nil, s.Reject("HANDOFF_PATH", "原始路径非法或重复")
			}
			expected := "payload/files/" + original
			if original == "CONTEXT.md" {
				expected = "payload/files/source-context.snapshot.md"
			}
			legacyExpected := "payload/files/" + original
			if original == "CONTEXT.md" {
				legacyExpected = "payload/source-context.snapshot.md"
			}
			if original == kernel.Text(manifest["handoff_ref"]) {
				legacyExpected = "handoff.yaml"
			}
			if ref != expected && ref != legacyExpected {
				return nil, s.Unavailable("READONLY_SOURCE_LAYOUT_REQUIRED", "包需要只读来源布局")
			}
			originals[fold.String(original)] = true
		}
	}
	if total > 512<<20 || len(names) > 20001 {
		return nil, s.Reject("HANDOFF_LIMIT", "包大小或数量超限")
	}
	files, e := s.Scan(prefix)
	if e != nil {
		return nil, e
	}
	actual := []string{}
	for _, f := range files {
		actual = append(actual, strings.TrimPrefix(f, prefix+"/"))
	}
	if !dependencies.ContractSame(dependencies.ContractSorted(names), dependencies.ContractSorted(actual)) {
		return nil, s.Reject("HANDOFF_MANIFEST", "包文件未登记、缺失或额外")
	}
	aliases := map[string]string{}
	sourcePrefix := path.Join(prefix, "payload/files")
	legacy := false
	for _, entry := range kernel.SemList(manifest["files"]) {
		file := kernel.SemMap(entry)
		if kernel.Text(file["original_ref"]) == kernel.Text(manifest["handoff_ref"]) && kernel.Text(file["path"]) == "handoff.yaml" {
			legacy = true
		}
	}
	if legacy {
		sourcePrefix = prefix
	}
	for _, entry := range kernel.SemList(manifest["files"]) {
		file := kernel.SemMap(entry)
		if original := kernel.Text(file["original_ref"]); original != "" {
			stored := strings.TrimPrefix(kernel.Text(file["path"]), "payload/files/")
			if legacy {
				stored = kernel.Text(file["path"])
			}
			aliases[original] = stored
			if original != "CONTEXT.md" && !(legacy && original == kernel.Text(manifest["handoff_ref"])) {
				for a, b := path.Dir(original), path.Dir(stored); a != "." && b != "."; a, b = path.Dir(a), path.Dir(b) {
					if previous, ok := aliases[a]; ok && previous != b {
						return nil, s.Reject("HANDOFF_PATH", "目录映射冲突")
					}
					aliases[a] = b
				}
			}
		}
	}
	if legacy && aliases["CONTEXT.md"] != "payload/source-context.snapshot.md" || !legacy && aliases["CONTEXT.md"] != "source-context.snapshot.md" {
		return nil, s.Reject("HANDOFF_PATH", "来源布局混合或缺少根词汇快照")
	}
	source, e := s.SourceSnapshotSession(sourcePrefix, aliases)
	if e != nil {
		return nil, e
	}
	source.Roles, e = source.Doc(".template-spec/agents/digital-human-roles.yaml")
	if e != nil {
		return nil, e
	}
	if len(kernel.SemMap(source.Roles["gate_policy"])) == 0 {
		return nil, source.Reject("HANDOFF_POLICY", "来源角色策略缺少 gate_policy")
	}
	contractSourceSigningRegistry(source)
	handoff, e := source.Doc(kernel.Text(manifest["handoff_ref"]))
	if e != nil {
		return nil, e
	}
	if kernel.ContractN(handoff["schema_version"]) == 5 {
		for _, entry := range kernel.SemList(manifest["files"]) {
			file := kernel.SemMap(entry)
			if original := kernel.Text(file["original_ref"]); original != "" {
				expected := "payload/files/" + original
				if original == "CONTEXT.md" {
					expected = "payload/files/source-context.snapshot.md"
				}
				if kernel.Text(file["path"]) != expected {
					return nil, s.Reject("HANDOFF_PATH", "Handoff v5 必须保留固定 payload/files 来源布局")
				}
			}
		}
	}
	if e = contractHandoffSourcePolicy(source, kernel.ContractN(handoff["schema_version"])); e != nil {
		return nil, e
	}
	return &NativeHandoff{Manifest: manifest, Handoff: handoff, Source: source, Prefix: prefix}, nil
}

// Imported business references select an already authenticated v5 source view;
// a path match alone never creates aliases or accepts a distinct baseline format.
func ContractBusinessSource(s *kernel.SemanticSession, ref, reviewRef string) (*kernel.SemanticSession, string, string, error) {
	if !strings.HasPrefix(ref, "docs/spec-baselines/") && !strings.HasPrefix(ref, "docs/handoffs/") {
		return s, ref, reviewRef, nil
	}
	parts := strings.Split(ref, "/")
	if !kernel.ContractPath(ref) || len(parts) < 6 || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(parts[3]) {
		return nil, "", "", s.Unavailable("BUSINESS_SOURCE_LAYOUT_UNSUPPORTED", "来源路径必须为已登记导入布局")
	}
	base := strings.Join(parts[:4], "/")
	if parts[1] == "spec-baselines" {
		if parts[4] == "working-files" {
			if _, _, e := VerifySpecBaselineReceipt(s, base+"/receipt.json", false); e != nil {
				return nil, "", "", e
			}
			receipt, e := s.Doc(base + "/receipt.json")
			if e != nil {
				return nil, "", "", e
			}
			working, e := s.Doc(kernel.Text(receipt["working_set_ref"]))
			if e != nil {
				return nil, "", "", e
			}
			if working["business_ticket_set_ref"] != ref {
				return nil, "", "", s.Reject("SPEC_BASELINE_WORKING_SET", "业务集合不属于批准接入工作集")
			}
			return s, ref, reviewRef, nil
		}
		if len(parts) < 8 || parts[4] != "package" || parts[5] != "payload" || parts[6] != "files" {
			return nil, "", "", s.Unavailable("BUSINESS_SOURCE_LAYOUT_UNSUPPORTED", "冻结 Spec 来源需要固定 payload/files 布局")
		}
		_, source, e := OpenSpecBaseline(s, base+"/package")
		if e != nil {
			return nil, "", "", e
		}
		local := strings.Join(parts[7:], "/")
		if strings.HasPrefix(reviewRef, base+"/package/payload/files/") {
			reviewRef = strings.TrimPrefix(reviewRef, base+"/package/payload/files/")
		}
		return source, local, reviewRef, nil
	}
	if parts[4] != "package" {
		return nil, "", "", s.Unavailable("BUSINESS_SOURCE_LAYOUT_UNSUPPORTED", "Handoff 来源需要已登记 package")
	}
	prefix := base + "/package"
	opened, e := contractOpenHandoffSource(s, prefix)
	if e != nil {
		return nil, "", "", e
	}
	stored := strings.Join(parts[5:], "/")
	local := ""
	localReview := reviewRef
	for _, entry := range kernel.SemList(opened.Manifest["files"]) {
		row := kernel.SemMap(entry)
		if row["path"] == stored {
			local = kernel.Text(row["original_ref"])
		}
		if prefix+"/"+kernel.Text(row["path"]) == reviewRef {
			localReview = kernel.Text(row["original_ref"])
		}
	}
	if local == "" {
		return nil, "", "", s.Reject("SOURCE_MANIFEST", "业务集合未登记为来源文件")
	}
	return opened.Source, local, localReview, nil
}

func ContractOpenHandoff(s *kernel.SemanticSession, prefix string) (*NativeHandoff, error) {
	if strings.EqualFold(path.Ext(prefix), ".zip") {
		archive, root, e := s.ZipSourceSession(prefix)
		if e != nil {
			return nil, e
		}
		return ContractOpenHandoff(archive, root)
	}
	opened, e := contractOpenHandoffSource(s, prefix)
	if e != nil {
		return nil, e
	}
	manifest, source := opened.Manifest, opened.Source
	bundle, e := ContractInspectHandoff(source, kernel.Text(manifest["handoff_ref"]))
	if e != nil {
		return nil, e
	}
	bundle.Manifest, bundle.Source, bundle.Prefix = manifest, source, prefix
	if bundle.Handoff["handoff_id"] != manifest["bundle_id"] || bundle.Handoff["handoff_version"] != manifest["version"] {
		return nil, s.Reject("HANDOFF_IDENTITY", "包与 Handoff 身份不一致")
	}
	for kind, expected := range map[string][]any{"rules": bundle.Rules, "scenarios": bundle.Scenarios} {
		b, e := s.Bytes(path.Join(prefix, "indexes", kind+".json"))
		if e != nil {
			return nil, e
		}
		parsed, e := schema.Parse(b)
		if e != nil {
			return nil, s.Unavailable("INPUT", e.Error())
		}
		if !dependencies.ContractSame(parsed, expected) {
			return nil, s.Reject("HANDOFF_INDEX", "索引与来源不一致")
		}
	}
	closure, e := contractHandoffClosure(source, kernel.Text(manifest["handoff_ref"]), bundle)
	if e != nil {
		return nil, e
	}
	expectedOriginals := []string{}
	for _, v := range kernel.SemList(manifest["files"]) {
		if ref := kernel.Text(kernel.SemMap(v)["original_ref"]); ref != "" {
			expectedOriginals = append(expectedOriginals, ref)
		}
	}
	if !dependencies.ContractSame(dependencies.ContractSorted(closure), dependencies.ContractSorted(expectedOriginals)) {
		return nil, s.Reject("HANDOFF_CLOSURE", "源快照闭包不一致")
	}
	changes, e := s.Doc(path.Join(prefix, "indexes/changes.json"))
	if e != nil {
		return nil, e
	}
	if !dependencies.ContractSame(changes["previous_bundle"], manifest["previous_bundle"]) {
		return nil, s.Reject("HANDOFF_PREVIOUS", "差异前版绑定不一致")
	}
	old := map[string]string{}
	if manifest["previous_bundle"] != nil {
		previous, e := s.Doc(path.Join(prefix, "indexes/previous-manifest.json"))
		if e != nil {
			return nil, e
		}
		prior := kernel.SemMap(manifest["previous_bundle"])
		if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(kernel.Text(previous["version"])) || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(kernel.Text(manifest["version"])) {
			return nil, s.Reject("HANDOFF_PREVIOUS", "前版版本格式非法")
		}
		pv, _ := strconv.Atoi(kernel.Text(previous["version"])[1:])
		cv, _ := strconv.Atoi(kernel.Text(manifest["version"])[1:])
		if kernel.Text(previous["bundle_digest"]) != dependencies.ContractDigest(dependencies.ContractWithout(previous, "bundle_digest")) || previous["bundle_digest"] != prior["digest"] || previous["bundle_id"] != manifest["bundle_id"] || previous["version"] != prior["version"] || pv >= cv {
			return nil, s.Reject("HANDOFF_PREVIOUS", "前版清单身份或摘要无效")
		}
		for _, kind := range []string{"rules", "scenarios"} {
			b, e := s.Bytes(path.Join(prefix, "indexes/previous-"+kind+".json"))
			if e != nil {
				return nil, e
			}
			entry := kernel.ApFind(previous["files"], "path", "indexes/"+kind+".json")
			if entry == nil || kernel.ContractN(entry["size_bytes"]) != len(b) || kernel.Text(entry["sha256"]) != "sha256:"+safefs.Digest(b) {
				return nil, s.Reject("HANDOFF_PREVIOUS", "前版索引字节不一致")
			}
			value, e := schema.Parse(b)
			if e != nil {
				return nil, e
			}
			for _, v := range kernel.SemList(value) {
				m := kernel.SemMap(v)
				old[kernel.First(kernel.Text(m["rule_id"]), kernel.Text(m["scenario_id"]))] = kernel.Text(m["source_digest"])
			}
		}
	}
	now := map[string]string{}
	for _, v := range append(append([]any{}, bundle.Rules...), bundle.Scenarios...) {
		m := kernel.SemMap(v)
		now[kernel.First(kernel.Text(m["rule_id"]), kernel.Text(m["scenario_id"]))] = kernel.Text(m["source_digest"])
	}
	added, updated, removed := []string{}, []string{}, []string{}
	for id, d := range now {
		if old[id] == "" {
			added = append(added, id)
		} else if old[id] != d {
			updated = append(updated, id)
		}
	}
	for id := range old {
		if now[id] == "" {
			removed = append(removed, id)
		}
	}
	expected := map[string]any{"previous_bundle": manifest["previous_bundle"], "added": dependencies.ContractSorted(added), "updated": dependencies.ContractSorted(updated), "removed": dependencies.ContractSorted(removed)}
	if !dependencies.ContractSame(changes, expected) {
		return nil, s.Reject("HANDOFF_CHANGES", "版本差异与索引不一致")
	}
	bundle.Changes = changes
	return bundle, nil
}

// Historical source signing gates come only from the validated source policy.
// Current v2 tasks and bounded Plan ownership still load their frozen registry.
func contractSourceSigningRegistry(source *kernel.SemanticSession) {
	published := []any{}
	policy := kernel.SemMap(source.Roles["gate_policy"])
	for _, bucket := range []string{"biological_human", "product_digital_human_with_biological_veto"} {
		for _, id := range kernel.SemStrings(policy[bucket]) {
			published = append(published, map[string]any{"id": id})
		}
	}
	for _, bucket := range []string{"dual_digital_human", "digital_human_review", "check_reviews"} {
		for _, row := range kernel.SemList(policy[bucket]) {
			published = append(published, map[string]any{"id": kernel.SemMap(row)["gate"]})
		}
	}
	source.Registry = map[string]any{"gates": published, "id_policy": map[string]any{"deprecated_ids": []any{}}}
	source.Report.Checks = append(source.Report.Checks, kernel.SemanticCheck{ID: "handoff-source-policy-v1", SourceRef: ".template-spec/agents/digital-human-roles.yaml", Status: "passed"})
}

// The readonly source selector binds the original protocol before loading its
// signing policy. It does not lend historical policy to other governance kinds.
func HandoffSourceAuthorities(s *kernel.SemanticSession, ref string) error {
	handoff, err := s.Doc(ref)
	if err != nil {
		return err
	}
	protocol, valid := kernel.Integer(handoff["schema_version"])
	if valid && protocol == 3 {
		return s.Unavailable("MIGRATION_REQUIRED", "Handoff v3 为前代协议，已不再支持；请使用当前协议重新生成")
	}
	if !valid || protocol < 4 || protocol > 5 {
		return s.Unavailable("CAPABILITY", "未知 Handoff 协议")
	}
	if err = s.ValidateSchema(fmt.Sprintf(".template-spec/process/schemas/strategic-design-handoff-v%d.schema.json", protocol), handoff); err != nil {
		return err
	}
	if err = s.HandoffSourceAuthorities(func(source *kernel.SemanticSession) error {
		return contractHandoffSourcePolicy(source, int(protocol))
	}); err != nil {
		return err
	}
	contractSourceSigningRegistry(s)
	return nil
}

func ContractInspectHandoff(s *kernel.SemanticSession, ref string) (*NativeHandoff, error) {
	h, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	n := kernel.ContractN(h["schema_version"])
	if n == 3 {
		return nil, s.Unavailable("MIGRATION_REQUIRED", "Handoff v3 为前代协议，已不再支持；请使用当前协议重新生成")
	}
	if !kernel.SemHas([]string{"4", "5"}, strconv.Itoa(n)) {
		return nil, s.Unavailable("CAPABILITY", "未知 Handoff 协议")
	}
	if e = s.ValidateSchema(fmt.Sprintf(".template-spec/process/schemas/strategic-design-handoff-v%d.schema.json", n), h); e != nil {
		return nil, e
	}
	if kernel.Text(h["status"]) != "approved" {
		return nil, s.Reject("HANDOFF_APPROVAL", "交接尚未 approved")
	}
	config := kernel.SemMap(h["package_export"])
	exportSchema := ".template-spec/process/schemas/strategic-handoff-export.schema.json"
	if n == 5 {
		exportSchema = ".template-spec/process/schemas/strategic-handoff-export-v2.schema.json"
	}
	if e = s.ValidateSchema(exportSchema, config); e != nil {
		return nil, e
	}
	if n == 5 && config["ui_baseline_kind"] != h["ui_baseline_kind"] {
		return nil, s.Reject("HANDOFF_UI", "交接和导出基线类型冲突")
	}
	context, e := s.ContextSnapshot(kernel.SemMap(h["source_context_snapshot"]))
	if e != nil {
		return nil, e
	}
	terms := map[string]map[string]any{}
	for _, term := range context["business_terms"].([]kernel.Term) {
		raw, _ := schema.Parse(MustMarshalContract(term))
		terms[term.TermRef] = kernel.SemMap(raw)
	}
	used := map[string]bool{}
	for _, op := range []string{"added", "updated", "deprecated"} {
		for _, v := range kernel.SemList(kernel.SemMap(h["context_delta"])[op]) {
			term := kernel.SemMap(v)
			id := kernel.Text(term["term_ref"])
			if used[id] || !kernel.SemHas(kernel.SemMap(h["source_context_snapshot"])["term_refs"], id) || terms[id] == nil {
				return nil, s.Reject("HANDOFF_CONTEXT", "术语增量重复、未引用或不存在")
			}
			used[id] = true
			for _, key := range []string{"term", "meaning", "english_identifier", "context_id", "forbidden_aliases"} {
				if !dependencies.ContractSame(term[key], terms[id][key]) {
					return nil, s.Reject("HANDOFF_CONTEXT", "术语增量与源 Context 冲突")
				}
			}
		}
	}
	source := kernel.SemMap(h["source"])
	strategy, e := s.Doc(kernel.Text(kernel.SemMap(source["domain_strategy_ref"])["persisted_ref"]))
	if e != nil {
		return nil, e
	}
	stage, e := s.Doc(kernel.Text(kernel.SemMap(source["stage_decision_package_ref"])["persisted_ref"]))
	if e != nil {
		return nil, e
	}
	suffix := ""
	if n >= 4 {
		suffix = "-v3"
	}
	for label, doc := range map[string]map[string]any{"domain-strategy": strategy, "stage-decision-package": stage} {
		if e = s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-"+label+suffix+".schema.json", doc); e != nil {
			return nil, e
		}
		if kernel.Text(doc["status"]) != "approved" {
			return nil, s.Reject("HANDOFF_SOURCE_APPROVAL", "来源设计未批准")
		}
		contextSource := s
		key := "domain_strategy_ref"
		if label == "stage-decision-package" {
			key = "stage_decision_package_ref"
		}
		if marker := kernel.SemMap(kernel.SemMap(kernel.SemMap(source[key])["approval_context"])["source_baseline"]); len(marker) > 0 {
			_, inherited, err := VerifySpecBaselineReceipt(s, kernel.Text(marker["receipt_ref"]), false)
			if err != nil {
				return nil, err
			}
			contextSource = inherited
		}
		if _, e = contextSource.ContextSnapshot(kernel.SemMap(doc["context_snapshot"])); e != nil {
			return nil, e
		}
	}
	if strategy["domain_strategy_id"] != kernel.SemMap(source["domain_strategy_ref"])["id"] || strategy["domain_version"] != kernel.SemMap(source["domain_strategy_ref"])["version"] || stage["stage_decision_id"] != kernel.SemMap(source["stage_decision_package_ref"])["id"] || stage["package_version"] != kernel.SemMap(source["stage_decision_package_ref"])["version"] {
		return nil, s.Reject("HANDOFF_SOURCE_IDENTITY", "战略和阶段身份/版本冲突")
	}
	binding := kernel.SemMap(stage["domain_strategy_ref"])
	strategyRef := kernel.SemMap(source["domain_strategy_ref"])["persisted_ref"]
	if marker := kernel.SemMap(kernel.SemMap(kernel.SemMap(source["stage_decision_package_ref"])["approval_context"])["source_baseline"]); len(marker) > 0 {
		strategyRef = kernel.SemMap(kernel.SemMap(kernel.SemMap(source["domain_strategy_ref"])["approval_context"])["source_baseline"])["source_ref"]
	}
	if binding["domain_strategy_id"] != strategy["domain_strategy_id"] || binding["domain_version"] != strategy["domain_version"] || kernel.Text(binding["status"]) != "approved" || binding["persisted_ref"] != strategyRef || kernel.Text(binding["digest"]) != dependencies.ContractDigest(strategy) {
		return nil, s.Reject("HANDOFF_SOURCE_STALE", "阶段包战略来源变化")
	}
	for _, v := range kernel.SemList(stage["unresolved_items"]) {
		if kernel.Text(kernel.SemMap(v)["type"]) == "blocker" {
			return nil, s.Reject("HANDOFF_BLOCKED", "阶段包仍含 blocker")
		}
	}
	if e = contractHandoffRoutes(s, h, stage); e != nil {
		return nil, e
	}
	if e = contractHandoffUIScope(s, h, stage, config); e != nil {
		return nil, e
	}
	rules, scenarios, e := contractHandoffIndexes(s, strategy)
	if e != nil {
		return nil, e
	}
	bundle := &NativeHandoff{Handoff: h, Strategy: strategy, Stage: stage, Config: config, Rules: rules, Scenarios: scenarios}
	if e = contractHandoffApprovals(s, ref, bundle); e != nil {
		return nil, e
	}
	return bundle, nil
}

func contractHandoffUIScope(s *kernel.SemanticSession, h, stage, config map[string]any) error {
	if contractHandoffUIKind(h) == "existing-ui-baseline" && kernel.SemMap(stage["impact_assessment"])["ui"] != false {
		return s.Reject("HANDOFF_UI", "既有 UI 不能消费 UI 影响")
	}
	if contractHandoffUIKind(h) == "not-applicable" {
		impact := kernel.SemMap(stage["impact_assessment"])
		if impact["ui"] != false || impact["frontend"] != false {
			return s.Reject("HANDOFF_UI", "无产品设计基线必须有已批准的无 UI、前端影响依据")
		}
		for _, route := range kernel.SemList(h["consumer_routes"]) {
			if row := kernel.SemMap(route); row["capability"] == "frontend-engineering-design" && row["activation"] != "not-applicable" {
				return s.Reject("HANDOFF_UI", "无产品设计基线不能启用前端消费者")
			}
		}
		for _, key := range []string{"prototype_ref", "visual_baseline_ref", "existing_ui_baseline_ref"} {
			if _, present := kernel.SemMap(h["source"])[key]; present {
				return s.Reject("HANDOFF_UI", "无产品设计基线不能带有 UI 来源")
			}
			if _, present := kernel.SemMap(config["approvals"])[key]; present {
				return s.Reject("HANDOFF_UI", "无产品设计基线不能带有 UI 批准")
			}
		}
		if _, present := config["prototype"]; present {
			return s.Reject("HANDOFF_UI", "无产品设计基线不能带有原型配置")
		}
	}
	return nil
}

func MustMarshalContract(v any) []byte { b, _ := jsonMarshalContract(v); return b }

func jsonMarshalContract(v any) ([]byte, error) {
	str, e := dependencies.ContractJSON(v)
	return []byte(str), e
}

func contractHandoffUIKind(h map[string]any) string {
	if kernel.ContractN(h["schema_version"]) < 5 {
		return "prototype"
	}
	return kernel.Text(h["ui_baseline_kind"])
}

func contractHandoffUIRef(h map[string]any) map[string]any {
	key := "visual_baseline_ref"
	if contractHandoffUIKind(h) == "existing-ui-baseline" {
		key = "existing_ui_baseline_ref"
	}
	return kernel.SemMap(kernel.SemMap(h["source"])[key])
}

func contractHandoffRoutes(s *kernel.SemanticSession, h, stage map[string]any) error {
	if kernel.ContractN(h["schema_version"]) == 3 {
		return nil
	}
	routes := map[string]map[string]any{}
	ids := map[string]bool{}
	for _, v := range kernel.SemList(h["consumer_routes"]) {
		r := kernel.SemMap(v)
		capability, id := kernel.Text(r["capability"]), kernel.Text(r["route_id"])
		if routes[capability] != nil || ids[id] {
			return s.Reject("HANDOFF_ROUTE", "能力或 route_id 重复")
		}
		routes[capability] = r
		ids[id] = true
	}
	caps := []string{"backend-technical-design", "frontend-engineering-design", "delivery-coordination"}
	for _, capability := range caps {
		if routes[capability] == nil {
			return s.Reject("HANDOFF_ROUTE", "消费者路线未完整声明")
		}
	}
	mapping := map[string][]map[string]any{}
	for _, v := range kernel.SemList(stage["downstream_mapping"]) {
		m := kernel.SemMap(v)
		capability := kernel.Text(m["consumer_capability"])
		if !kernel.SemHas(caps, capability) {
			return s.Reject("HANDOFF_ROUTE", "未知消费者能力")
		}
		mapping[capability] = append(mapping[capability], m)
	}
	impact := kernel.SemMap(stage["impact_assessment"])
	for capability, required := range map[string]bool{"backend-technical-design": impact["backend"] == true || impact["api"] == true || impact["data"] == true, "frontend-engineering-design": impact["frontend"] == true || impact["ui"] == true} {
		expected := "not-applicable"
		for _, m := range mapping[capability] {
			if kernel.Text(m["propagation"]) != "not-applicable" {
				expected = "optional"
			}
		}
		if required {
			expected = "required"
		}
		if kernel.Text(routes[capability]["activation"]) != expected {
			return s.Reject("HANDOFF_ROUTE", "路由与批准影响矩阵冲突")
		}
	}
	backend, frontend, coord := routes[caps[0]], routes[caps[1]], routes[caps[2]]
	deps := []string{}
	if kernel.Text(backend["activation"]) != "not-applicable" {
		deps = append(deps, kernel.Text(backend["route_id"]))
	}
	if !dependencies.ContractSame(frontend["dependencies"], deps) {
		return s.Reject("HANDOFF_ROUTE", "前端路由依赖冲突")
	}
	if kernel.Text(frontend["activation"]) != "not-applicable" {
		deps = append(deps, kernel.Text(frontend["route_id"]))
	}
	expected := "not-applicable"
	if len(deps) > 0 {
		expected = "required"
	}
	if kernel.Text(coord["activation"]) != expected || !dependencies.ContractSame(coord["dependencies"], deps) {
		return s.Reject("HANDOFF_ROUTE", "协调路由依赖冲突")
	}
	for capability, rows := range mapping {
		for _, m := range rows {
			if (kernel.Text(m["propagation"]) == "not-applicable") != (kernel.Text(routes[capability]["activation"]) == "not-applicable") {
				return s.Reject("HANDOFF_ROUTE", "downstream mapping 与路线冲突")
			}
		}
	}
	for _, r := range routes {
		for _, dep := range kernel.SemStrings(r["dependencies"]) {
			if !ids[dep] {
				return s.Reject("HANDOFF_ROUTE", "路由依赖悬空")
			}
		}
	}
	return nil
}

func contractHandoffIndexes(s *kernel.SemanticSession, strategy map[string]any) ([]any, []any, error) {
	if kernel.ContractN(strategy["traceability_version"]) != 1 {
		return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "缺少稳定 traceability v1")
	}
	contexts := map[string]bool{}
	for _, v := range kernel.SemList(strategy["contexts"]) {
		contexts[kernel.Text(kernel.SemMap(v)["context_id"])] = true
	}
	rules, scenarios, used := map[string]map[string]any{}, map[string]map[string]any{}, map[string]bool{}
	for _, v := range kernel.SemList(strategy["rule_catalog"]) {
		m := kernel.SemMap(v)
		id := kernel.Text(m["rule_id"])
		if !regexp.MustCompile(`^rule\.[a-z0-9][a-z0-9-]*$`).MatchString(id) || rules[id] != nil || kernel.Text(m["statement"]) == "" || kernel.Text(m["status"]) != "confirmed" || !contexts[kernel.Text(m["responsible_context"])] {
			return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "规则身份、确认或责任区无效")
		}
		row := dependencies.ContractCopy(m)
		row["source_digest"] = dependencies.ContractDigest(m)
		rules[id] = row
	}
	if len(rules) == 0 {
		return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "规则目录为空")
	}
	for _, v := range kernel.SemList(strategy["scenarios"]) {
		m := kernel.SemMap(v)
		id := kernel.Text(m["scenario_id"])
		_, boolean := m["critical"].(bool)
		if !regexp.MustCompile(`^scenario\.[a-z0-9][a-z0-9-]*$`).MatchString(id) || scenarios[id] != nil || kernel.Text(m["status"]) != "confirmed" || !boolean || !contexts[kernel.Text(m["responsible_context"])] || !dependencies.ContractUnique(m["rule_refs"], true) {
			return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "场景身份、确认或规则无效")
		}
		for _, key := range []string{"commands", "success_results", "failure_results"} {
			if !dependencies.ContractUnique(m[key], true) {
				return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "场景缺少成功/失败路径")
			}
		}
		statements := []string{}
		for _, ref := range kernel.SemStrings(m["rule_refs"]) {
			if rules[ref] == nil {
				return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "场景引用悬空规则")
			}
			statements = append(statements, kernel.Text(rules[ref]["statement"]))
			used[ref] = true
		}
		if !dependencies.ContractSame(dependencies.ContractSorted(statements), dependencies.ContractSorted(kernel.SemStrings(m["rules"]))) {
			return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "规则正文与稳定引用冲突")
		}
		row := dependencies.ContractCopy(m)
		row["source_digest"] = dependencies.ContractDigest(m)
		scenarios[id] = row
	}
	if len(scenarios) == 0 {
		return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "场景目录为空")
	}
	for _, v := range kernel.SemList(strategy["invariants"]) {
		m := kernel.SemMap(v)
		ref := kernel.Text(m["rule_ref"])
		if rules[ref] == nil || rules[ref]["statement"] != m["statement"] || !dependencies.ContractUnique(m["scenario_refs"], true) {
			return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "不变量与规则冲突")
		}
		for _, id := range kernel.SemStrings(m["scenario_refs"]) {
			if scenarios[id] == nil || !kernel.SemHas(scenarios[id]["rule_refs"], ref) {
				return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "不变量场景未覆盖规则")
			}
		}
		used[ref] = true
	}
	for id := range rules {
		if !used[id] {
			return nil, nil, s.Reject("HANDOFF_TRACEABILITY", "存在未使用规则")
		}
	}
	outRules, outScenarios := []any{}, []any{}
	for _, id := range dependencies.MapKeysAsAny(rules) {
		outRules = append(outRules, rules[id])
	}
	for _, id := range dependencies.MapKeysAsAny(scenarios) {
		outScenarios = append(outScenarios, scenarios[id])
	}
	return outRules, outScenarios, nil
}

func contractHandoffApprovals(s *kernel.SemanticSession, ref string, b *NativeHandoff) error {
	if e := contractHandoffSourcePolicy(s, kernel.ContractN(b.Handoff["schema_version"])); e != nil {
		return e
	}
	contractNormalizeSourceRoles(s)
	roles := s.Roles
	policy := kernel.SemMap(s.Roles["gate_policy"])
	currentPlan := kernel.ApFind(policy["dual_digital_human"], "gate", "gate.plan-approved") != nil
	explicit := func(g string) bool {
		return kernel.ApFind(policy["digital_human_review"], "gate", g) != nil || kernel.ApFind(policy["dual_digital_human"], "gate", g) != nil || kernel.SemHas(policy["biological_human"], g)
	}
	terminal := "gate.stage-decision-package-approved"
	if currentPlan || explicit("gate.strategic-design-handoff-approved") {
		terminal = "gate.strategic-design-handoff-approved"
	}
	gates := map[string]string{"domain_strategy_ref": "gate.domain-strategy-approved", "stage_decision_package_ref": "gate.stage-decision-package-approved", "spec_ref": "gate.spec-baseline-approved", "prototype_ref": "gate.user-confirmation", "visual_baseline_ref": "gate.user-confirmation", "business_ticket_set_ref": terminal, "existing_ui_baseline_ref": "gate.user-confirmation", "handoff": terminal}
	if currentPlan {
		gates["domain_strategy_ref"], gates["stage_decision_package_ref"], gates["business_ticket_set_ref"] = "gate.plan-approved", "gate.plan-approved", "gate.plan-approved"
		gates["prototype_ref"], gates["visual_baseline_ref"], gates["existing_ui_baseline_ref"] = "gate.product-design-approved", "gate.product-design-approved", "gate.product-design-approved"
	}
	for _, row := range []struct {
		doc     map[string]any
		version any
		gate    string
	}{{b.Strategy, b.Strategy["domain_version"], "check.domain-strategy-approved"}, {b.Stage, b.Stage["package_version"], "check.stage-decision-package-approved"}} {
		key := "domain_strategy_ref"
		if row.gate == "check.stage-decision-package-approved" {
			key = "stage_decision_package_ref"
		}
		if len(kernel.SemMap(kernel.SemMap(kernel.SemMap(kernel.SemMap(b.Handoff["source"])[key])["approval_context"])["source_baseline"])) > 0 {
			continue
		}
		approval := kernel.SemMap(row.doc["approval"])
		if len(approval) > 0 {
			if !dependencies.ContractSame(approval["current_version"], row.version) {
				return s.Reject("HANDOFF_APPROVAL", "来源内置批准版本过期")
			}
			if e := contractSourceApproval(s, row.gate, map[string]any{"ref": approval["persisted_ref"], "approval_ref": approval["approval_ref"], "approval_context": approval["approval_context"]}, kernel.Text(approval["approval_ref"])); e != nil {
				return e
			}
		}
	}
	sources := kernel.SemMap(b.Handoff["source"])
	setSource, e := s.Doc(kernel.Text(kernel.SemMap(sources["business_ticket_set_ref"])["persisted_ref"]))
	if e != nil {
		return e
	}
	capabilities := kernel.SemMap(roles["user_decision_policy"])["required_capabilities"]
	if kernel.Text(setSource["kind"]) == "business-ticket-set" && !kernel.SemHas(capabilities, "business-ticket-approval-v1") {
		return s.Reject("HANDOFF_POLICY", "业务集合缺少来源批准策略能力")
	}
	if kernel.SemHas(capabilities, "business-ticket-approval-v1") {
		gates["business_ticket_set_ref"] = terminal
		business, businessReport, e := dependencies.ContractCollectBusinessTickets(s, kernel.Text(kernel.SemMap(sources["business_ticket_set_ref"])["persisted_ref"]), "formal", "")
		if e != nil {
			return e
		}
		set := kernel.SemMap(sources["business_ticket_set_ref"])
		spec := kernel.SemMap(sources["spec_ref"])
		if business["id"] != set["id"] || business["version"] != set["version"] || kernel.SemMap(business["spec"])["ref"] != spec["persisted_ref"] || kernel.SemMap(business["spec"])["version"] != spec["version"] {
			return s.Reject("HANDOFF_BUSINESS", "业务票集合身份或 Spec 版本冲突")
		}
		b.Business = business
		b.BusinessReport = businessReport
	}
	bindings := dependencies.ContractCopy(sources)
	bindings["handoff"] = map[string]any{"id": b.Handoff["handoff_id"], "version": b.Handoff["handoff_version"], "persisted_ref": ref, "approval_context": kernel.SemMap(kernel.SemMap(b.Config["approvals"])["handoff"])["approval_context"]}
	proofs := []string{}
	for key, v := range bindings {
		source := kernel.SemMap(v)
		approval := kernel.SemMap(kernel.SemMap(b.Config["approvals"])[key])
		if kernel.Text(approval["gate_id"]) != gates[key] {
			return s.Reject("HANDOFF_APPROVAL", "来源批准门禁冲突: "+key)
		}
		assetRef := kernel.Text(source["persisted_ref"])
		actual := ""
		if key == "visual_baseline_ref" || key == "existing_ui_baseline_ref" {
			assetRef = path.Join(assetRef, kernel.Text(source["manifest_ref"]))
			baseline, e := contractHandoffBaseline(s, b.Handoff)
			if e != nil {
				return e
			}
			actual = kernel.Text(kernel.SemMap(baseline["bundle"])["digest"])
			if key == "existing_ui_baseline_ref" {
				if kernel.Text(approval["digest_kind"]) != "sha256-bytes" || !kernel.SemHas(kernel.SemMap(roles["user_decision_policy"])["gates"], gates[key]) {
					return s.Reject("HANDOFF_POLICY", "既有 UI 未绑定真实产品设计决定策略")
				}
				bound, e := s.Bind(assetRef)
				if e != nil {
					return e
				}
				actual = bound.Digest
			}
		} else {
			bytes, e := s.Bytes(assetRef)
			if e != nil {
				return e
			}
			switch kernel.Text(approval["digest_kind"]) {
			case "canonical-json":
				doc, e := s.Doc(assetRef)
				if e != nil {
					return e
				}
				actual = dependencies.ContractDigest(doc)
			case "sha256-bytes":
				actual = "sha256:" + safefs.Digest(bytes)
			default:
				return s.Reject("HANDOFF_APPROVAL", "未知来源摘要算法")
			}
		}
		if key != "handoff" && kernel.Text(source["digest"]) != actual {
			return s.Reject("HANDOFF_SOURCE_STALE", "源资产摘要过期: "+key)
		}
		bound := map[string]any{"ref": assetRef, "approval_context": source["approval_context"]}
		bound["approval_ref"] = approval["record_ref"]
		if e = contractSourceApproval(s, gates[key], bound, kernel.Text(approval["record_ref"])); e != nil {
			return e
		}
		record, e := s.Doc(kernel.Text(approval["record_ref"]))
		if e != nil {
			return e
		}
		record, e = approvaldomain.SelectApprovalRecordSemantic(s, record, gates[key])
		if e != nil {
			return e
		}
		match := false
		for _, v := range kernel.SemList(record["artifact_bindings"]) {
			a := kernel.SemMap(v)
			match = match || a["id"] == kernel.First(kernel.Text(source["id"]), kernel.Text(source["baseline_id"])) && dependencies.ContractSame(a["version"], source["version"]) && kernel.Text(a["digest"]) == actual
		}
		if !match {
			return s.Reject("HANDOFF_APPROVAL", "来源批准未绑定身份、版本和摘要")
		}
		proofs = dependencies.ContractUnion(proofs, []string{kernel.Text(approval["record_ref"]), kernel.Text(record["user_decision_ref"]), kernel.Text(record["continuation_ref"]), kernel.Text(record["decision_reuse_ref"])})
	}
	if b.Business != nil {
		for _, v := range kernel.SemList(b.Business["coverage_deferred"]) {
			if !kernel.SemHas(proofs, kernel.Text(kernel.SemMap(kernel.SemMap(v)["decision"])["ref"])) {
				return s.Reject("BUSINESS_DEFERRED_APPROVAL_UNBOUND", "延期决定不在来源批准链")
			}
		}
	}
	if kernel.SemHas(capabilities, "strategic-decision-reuse-v1") {
		approval := kernel.SemMap(kernel.SemMap(b.Config["approvals"])["handoff"])
		record, e := s.Doc(kernel.Text(approval["record_ref"]))
		if e != nil {
			return e
		}
		record, e = approvaldomain.SelectApprovalRecordSemantic(s, record, gates["handoff"])
		if e != nil {
			return e
		}
		scope, e := s.Doc(kernel.Text(record["subject_ref"]))
		if e != nil {
			return e
		}
		if kernel.ContractN(scope["schema_version"]) != 1 || kernel.Text(scope["kind"]) != "strategic-delivery-scope" {
			return s.Reject("HANDOFF_SCOPE", "战略交付缺少独立批准范围清单")
		}
		delivery, e := s.Doc(kernel.Text(scope["delivery_ref"]))
		if e != nil {
			return e
		}
		if !dependencies.ContractSame(delivery, dependencies.ContractWithout(b.Handoff, "package_export", "status")) {
			return s.Reject("HANDOFF_SCOPE", "交付内容/风险未被批准范围覆盖")
		}
		if e = s.Basis(scope["assets"]); e != nil {
			return e
		}
		for key, v := range sources {
			source := kernel.SemMap(v)
			assetRef := kernel.Text(source["persisted_ref"])
			if key == "visual_baseline_ref" || key == "existing_ui_baseline_ref" {
				assetRef = path.Join(assetRef, kernel.Text(source["manifest_ref"]))
			}
			found := false
			for _, v := range kernel.SemList(scope["assets"]) {
				a := kernel.SemMap(v)
				found = found || kernel.Text(a["ref"]) == assetRef && a["version"] == source["version"] && kernel.Text(a["boundary"]) == gates[key]
			}
			if !found {
				return s.Reject("HANDOFF_SCOPE", "交付范围遗漏当前资产")
			}
		}
	}
	if contractHandoffUIKind(b.Handoff) == "prototype" {
		if e = contractPreview(s, kernel.SemMap(b.Config["prototype"]), kernel.SemStrings(contractHandoffUIRef(b.Handoff)["case_ids"])); e != nil {
			return e
		}
	}
	return nil
}

func contractHandoffSourcePolicy(s *kernel.SemanticSession, protocol int) error {
	version, valid := kernel.Integer(s.Roles["schema_version"])
	gates, gatesPresent := kernel.Object(s.Roles["gate_policy"])
	policy, present := kernel.Object(s.Roles["user_decision_policy"])
	_, gateList := policy["gates"].([]any)
	_, workUnits := kernel.Object(policy["work_units"])
	if !valid || version != 1 || s.Roles["status"] != "active" || !gatesPresent || !present || !gateList || !workUnits || !dependencies.ContractUnique(policy["gates"], false) || !dependencies.ContractUnique(policy["source_kinds"], true) {
		return s.Unavailable("HANDOFF_POLICY_CAPABILITY", "来源批准或真实决定规则缺失、未知或不完整")
	}
	for _, id := range kernel.SemStrings(policy["required_capabilities"]) {
		if !kernel.SemHas([]string{"strategic-decision-reuse-v1", "business-ticket-approval-v1"}, id) {
			return s.Unavailable("HANDOFF_POLICY_CAPABILITY", "不支持的来源决定策略能力: "+id)
		}
	}
	for _, kind := range kernel.SemStrings(policy["source_kinds"]) {
		if !kernel.SemHas([]string{"platform-message", "session-export", "user-confirmation-file"}, kind) {
			return s.Unavailable("HANDOFF_POLICY_CAPABILITY", "未知来源决定输入协议: "+kind)
		}
	}
	if raw, declared := policy["schema_version"]; declared {
		v, valid := kernel.Integer(raw)
		if !valid || v != 1 {
			return s.Unavailable("HANDOFF_POLICY_CAPABILITY", "未知来源决定策略协议")
		}
	} else {
		if protocol != 3 && protocol != 4 || gates["default_if_unlisted"] != "biological-human" || !kernel.SameStringSet(policy["source_kinds"], []string{"platform-message", "session-export", "user-confirmation-file"}) {
			return s.Unavailable("HANDOFF_POLICY_CAPABILITY", "未登记的 versionless 来源策略形状")
		}
		// The published Handoff v3/v4/v5 v1 signing policy predates this
		// field. Its complete original gates/work_units contract is required;
		// a missing policy can never be projected to an empty one.
		s.Report.Checks = append(s.Report.Checks, kernel.SemanticCheck{ID: "handoff-source-policy-versionless-v1", SourceRef: ".template-spec/agents/digital-human-roles.yaml", Status: "passed"})
	}
	for _, bucket := range []string{"biological_human", "product_digital_human_with_biological_veto", "dual_digital_human", "digital_human_review", "check_reviews"} {
		if value, present := gates[bucket]; present {
			if _, valid := value.([]any); !valid {
				return s.Unavailable("HANDOFF_POLICY_CAPABILITY", "来源签署分类必须为数组: "+bucket)
			}
		}
	}
	return nil
}

func contractSourceApproval(s *kernel.SemanticSession, gate string, binding map[string]any, approvalRef string) error {
	if inherited, err := inheritedSourceApproval(s, gate, binding, approvalRef); inherited {
		return err
	}
	record, e := s.Doc(approvalRef)
	if e != nil {
		return e
	}
	record, e = approvaldomain.SelectApprovalRecordSemantic(s, record, gate)
	if e != nil {
		return e
	}
	// The published source protocol selects an internal check from a bundle,
	// while a standalone historical record retains its declared signing gate.
	if kernel.SemHas([]string{"check.domain-strategy-approved", "check.stage-decision-package-approved"}, gate) && kernel.Text(record["gate_id"]) != gate {
		gate = kernel.Text(record["gate_id"])
	}
	savedRoles, savedRegistry := s.Roles, s.Registry
	defer func() { s.Roles, s.Registry = savedRoles, savedRegistry }()
	if kernel.ContractN(record["schema_version"]) == 2 {
		s.Roles, e = s.Doc(".template-spec/agents/digital-human-roles.yaml")
		if e != nil {
			return e
		}
		s.Registry, e = s.Doc(".template-spec/process/lifecycle-registry.yaml")
		if e != nil {
			return e
		}
		if _, e = s.Doc(".template-spec/agents/yss-skill-registry.yaml"); e != nil {
			return e
		}
		task, e := s.Doc(kernel.Text(record["review_task_ref"]))
		if e != nil {
			return e
		}
		for _, ref := range []string{".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/yss-skill-registry.yaml"} {
			b, e := s.Bind(ref)
			if e != nil {
				return e
			}
			found := false
			for _, v := range kernel.SemList(kernel.SemMap(task["review_context"])["basis"]) {
				a := kernel.SemMap(v)
				found = found || kernel.Text(a["ref"]) == ref && strings.TrimPrefix(kernel.Text(a["digest"]), "sha256:") == strings.TrimPrefix(b.Digest, "sha256:")
			}
			if !found {
				return s.Reject("HANDOFF_POLICY", "来源正式任务未冻结实际注册表")
			}
		}
	}
	if gate == "gate.plan-approved" {
		bounded := record["plan_review_binding"] != nil
		for _, v := range kernel.SemList(kernel.SemMap(kernel.SemMap(s.Roles["gate_policy"])["review_execution"])["review_bundles"]) {
			row := kernel.SemMap(v)
			bounded = bounded || kernel.Text(row["aggregate_gate"]) == gate && kernel.Text(row["aggregate_additional_review_task"]) == "forbidden"
		}
		if bounded {
			s.Registry, e = s.Doc(".template-spec/process/lifecycle-registry.yaml")
			if e != nil {
				return e
			}
			expected, err := approvaldomain.ApprovalExpectationForBoundAsset(s, gate, binding, nil, binding["review_package"])
			if err != nil {
				return err
			}
			ownerOptions := map[string]string{"asset-ref": kernel.Text(expected["subject_ref"]), "task-ref": kernel.Text(record["review_task_ref"]), "checkpoint-ref": kernel.Text(binding["checkpoint_ref"]), "checkpoint-digest": kernel.Text(binding["checkpoint_digest"])}
			checkpointRef, err := dependencies.SourcePlanApprovalCheckpoint(s, approvalRef, ownerOptions)
			if err != nil {
				return err
			}
			checkpoint, err := s.Doc(checkpointRef)
			if err != nil {
				return err
			}
			reviewRef := kernel.Text(checkpoint["plan_review_ref"])
			reviewBytes, err := s.Bytes(reviewRef)
			if err != nil {
				return err
			}
			review, err := s.Doc(reviewRef)
			if err != nil {
				return err
			}
			if expected["subject_ref"] != reviewRef || strings.TrimPrefix(kernel.Text(expected["subject_digest"]), "sha256:") != safefs.Digest(reviewBytes) {
				return s.Reject("APPROVAL_CURRENT_INVALID", "源 Plan 当前消费主体不匹配")
			}
			if !kernel.ApSetEqual(expected["approval_scope"], []any{checkpoint["feature_id"]}) || expected["drafter_principal_ref"] != review["drafter_principal_ref"] {
				return s.Reject("APPROVAL_CURRENT_INVALID", "源 Plan 当前消费范围或作者不匹配")
			}
			basis := kernel.ApRows(expected["basis"])
			if len(basis) == 0 {
				return s.Reject("APPROVAL_CURRENT_INVALID", "源 Plan 当前消费依据不匹配")
			}
			for _, asset := range basis {
				matched := false
				for _, current := range kernel.ApRows(review["basis"]) {
					matched = matched || asset["ref"] == current["ref"] && strings.TrimPrefix(kernel.Text(asset["digest"]), "sha256:") == strings.TrimPrefix(kernel.Text(current["digest"]), "sha256:")
				}
				if !matched {
					return s.Reject("APPROVAL_CURRENT_INVALID", "源 Plan 当前消费依据不匹配")
				}
			}
			if err = s.Basis(kernel.ApArray(expected["basis"])); err != nil {
				return err
			}
			if binding["id"] != nil || binding["version"] != nil {
				matched := false
				for _, asset := range kernel.ApRows(record["artifact_bindings"]) {
					matched = matched || (binding["id"] == nil || dependencies.ContractSame(asset["id"], binding["id"])) && (binding["version"] == nil || dependencies.ContractSame(asset["version"], binding["version"])) && kernel.Text(asset["digest"]) == kernel.Text(binding["digest"])
				}
				if !matched {
					return s.Reject("APPROVAL_CURRENT_INVALID", "批准未绑定当前资产身份/版本/摘要")
				}
			}
			// The aggregate reuses the professional task; that task must not gain an extra aggregate boundary.
			return s.Verify("source-plan-approval", approvalRef, ownerOptions)
		}
	}
	if e = dependencies.ContractApproval(s, gate, binding, approvalRef, map[string]string{}); e != nil {
		return fmt.Errorf("来源批准 %s (%s): %w", gate, approvalRef, e)
	}
	return nil
}

func contractHandoffClosure(s *kernel.SemanticSession, ref string, b *NativeHandoff) ([]string, error) {
	queue := []string{ref, "CONTEXT.md", ".template-spec/agents/digital-human-roles.yaml"}
	savedRegistry := s.Registry
	defer func() { s.Registry = savedRegistry }()
	sealedPackages := map[string]bool{}
	enqueueBaseline := func(receiptRef string) error {
		if err := s.Verify("spec-baseline-receipt", receiptRef, nil); err != nil {
			return err
		}
		receipt, err := s.Doc(receiptRef)
		if err != nil {
			return err
		}
		working, err := s.Doc(kernel.Text(receipt["working_set_ref"]))
		if err != nil {
			return err
		}
		packageRef := kernel.Text(receipt["package_ref"])
		sealedPackages[packageRef] = true
		queue = append(queue, receiptRef, packageRef, kernel.Text(receipt["working_set_ref"]))
		for _, asset := range kernel.SemMap(working["assets"]) {
			queue = append(queue, kernel.Text(asset))
		}
		return nil
	}
	if exists, err := s.Exists(".yss.json"); err != nil {
		return nil, err
	} else if exists {
		queue = append(queue, ".yss.json")
	}
	for key, v := range kernel.SemMap(b.Handoff["source"]) {
		queue = append(queue, kernel.Text(kernel.SemMap(v)["persisted_ref"]))
		if marker := kernel.SemMap(kernel.SemMap(kernel.SemMap(v)["approval_context"])["source_baseline"]); len(marker) > 0 {
			gate := "gate.plan-approved"
			if key == "spec_ref" {
				gate = "gate.spec-baseline-approved"
			}
			binding := kernel.SemMap(v)
			if _, err := inheritedSourceApproval(s, gate, map[string]any{"ref": binding["persisted_ref"], "approval_context": binding["approval_context"]}, kernel.Text(kernel.SemMap(kernel.SemMap(b.Config["approvals"])[key])["record_ref"])); err != nil {
				return nil, err
			}
			if err := enqueueBaseline(kernel.Text(marker["receipt_ref"])); err != nil {
				return nil, err
			}
			queue = append(queue, "yss-project.yaml", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", kernel.Text(marker["receipt_ref"]), kernel.Text(marker["context_reconciliation_ref"]))
		}
	}
	for _, v := range kernel.SemMap(b.Config["approvals"]) {
		queue = append(queue, kernel.Text(kernel.SemMap(v)["record_ref"]))
	}
	queue = append(queue, kernel.SemStrings(b.Handoff["evidence_and_version_digests"])...)
	queue = append(queue, kernel.SemStrings(b.Config["additional_files"])...)
	prototype := kernel.SemMap(b.Config["prototype"])
	if len(prototype) > 0 {
		queue = append(queue, kernel.Text(prototype["preview_root"]), kernel.Text(prototype["verification_ref"]))
		if kernel.Text(prototype["profile"]) == "H2" {
			queue = append(queue, kernel.Text(prototype["source_root"]), kernel.Text(prototype["lock_ref"]))
		}
	}
	symbolic := kernel.SemMap(b.Config["reference_map"])
	boundedPlan := false
	for _, value := range kernel.SemList(kernel.SemMap(kernel.SemMap(s.Roles["gate_policy"])["review_execution"])["review_bundles"]) {
		row := kernel.SemMap(value)
		boundedPlan = boundedPlan || row["aggregate_gate"] == "gate.plan-approved" && row["aggregate_additional_review_task"] == "forbidden"
	}
	// Keep SpecBaseline's published closure when it reuses this walker internally.
	protocol := kernel.ContractN(b.Handoff["schema_version"])
	boundedPlan = boundedPlan && (protocol == 3 || protocol == 4 || protocol == 5)
	seen := map[string]bool{}
	total := 0
	enqueue := func(ref string) {
		if mapped := kernel.Text(symbolic[ref]); mapped != "" {
			ref = mapped
		}
		if ref != "" {
			queue = append(queue, ref)
		}
	}
	var refs func(any, string)
	var dependencyErr error
	documentRef := ""
	refs = func(v any, key string) {
		if dependencyErr != nil {
			return
		}
		switch x := v.(type) {
		case []any:
			for _, v := range x {
				if key == "evidence_refs" {
					enqueue(kernel.Text(v))
				} else {
					refs(v, key)
				}
			}
		case map[string]any:
			if len(kernel.SemMap(x["upstream_spec_baseline"])) > 0 {
				s.Registry, dependencyErr = s.Doc(".template-spec/process/lifecycle-registry.yaml")
				if dependencyErr != nil {
					return
				}
				if dependencyErr = s.Verify("inherited-spec-checkpoint", documentRef, nil); dependencyErr != nil {
					return
				}
				if dependencyErr = enqueueBaseline(kernel.Text(kernel.SemMap(x["upstream_spec_baseline"])["receipt_ref"])); dependencyErr != nil {
					return
				}
				queue = append(queue, "yss-project.yaml", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml")
			}
			if marker := kernel.SemMap(x["source_baseline"]); len(marker) > 0 {
				enqueue(kernel.Text(marker["receipt_ref"]))
				enqueue(kernel.Text(marker["context_reconciliation_ref"]))
				return
			}
			if x["kind"] == "spec-baseline-import" {
				enqueue(kernel.Text(x["package_ref"]))
				enqueue(kernel.Text(x["working_set_ref"]))
			}
			if x["plan_review_control"] != nil {
				enqueue("yss-project.yaml")
				enqueue(".template-spec/process/schemas/plan-review-control.schema.json")
				control := kernel.SemMap(x["plan_review_control"])
				enqueue(kernel.Text(kernel.SemMap(control["provenance"])["source_ref"]))
				for _, v := range kernel.SemList(control["attempts"]) {
					for _, key := range []string{"checkpoint_ref", "task_ref", "candidate_ref", "result_ref"} {
						enqueue(kernel.Text(kernel.SemMap(v)[key]))
					}
				}
			}
			if kernel.SemMap(x["review_context"])["plan_review_binding"] != nil {
				enqueue(kernel.Text(x["checkpoint_ref"]))
				for _, ref := range kernel.SemStrings(x["inputs"]) {
					enqueue(ref)
				}
			}
			if kernel.ContractN(x["schema_version"]) == 2 && kernel.Text(x["review_task_ref"]) != "" {
				enqueue(".template-spec/process/lifecycle-registry.yaml")
				enqueue(".template-spec/agents/yss-skill-registry.yaml")
			}
			for k, v := range x {
				str, ok := v.(string)
				if k == "policy_ref" && ok {
					enqueue(strings.Split(str, "#")[0])
				} else if ok && kernel.SemHas([]string{"review_task_ref", "candidate_ref", "registry_ref", "approval_ref", "persisted_ref", "receipt_ref", "package_ref", "working_set_ref", "context_reconciliation_ref", "user_decision_ref", "decision_reuse_ref", "continuation_ref", "plan_continuation_ref", "scope_ref", "subject_ref", "delivery_ref", "review_ref", "ref"}, k) && !regexp.MustCompile(`^[a-z]+://`).MatchString(str) {
					enqueue(str)
				} else {
					refs(v, k)
				}
			}
		}
	}
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
		if mapped := kernel.Text(symbolic[ref]); mapped != "" {
			ref = mapped
		}
		if err := kernel.RejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
		if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
			continue
		}
		if strings.HasPrefix(ref, "evidence.") {
			return nil, s.Reject("HANDOFF_CLOSURE", "未解析 evidence ID")
		}
		if !strings.Contains(ref, "/") && !regexp.MustCompile(`\.(md|yaml|json|html|png|txt|log)$`).MatchString(ref) {
			if ref == "" {
				continue
			}
			exists, e := s.Exists(ref)
			if e != nil {
				return nil, e
			}
			if !exists {
				continue
			}
		}
		if !kernel.ContractPath(ref) {
			return nil, s.Reject("HANDOFF_PATH", "闭包引用非法")
		}
		if seen[ref] {
			continue
		}
		exists, e := s.Exists(ref)
		if e != nil {
			return nil, e
		}
		if !exists {
			return nil, s.Reject("HANDOFF_CLOSURE", "引用不在来源快照: "+ref)
		}
		mapped := s.LocalRef(ref)
		if _, directory := s.Scans[mapped]; directory {
			files, e := s.Scan(ref)
			if e != nil {
				return nil, e
			}
			for _, f := range files {
				if f == "source-context.snapshot.md" {
					f = "CONTEXT.md"
				}
				enqueue(f)
			}
			continue
		}
		bytes, e := s.Bytes(ref)
		if e != nil {
			return nil, e
		}
		if strings.HasSuffix(ref, "/orchestration-contract.yaml") {
			_, parts, _, err := s.OrchestrationDocument(ref)
			if err != nil {
				return nil, err
			}
			for _, part := range parts {
				if part != ref {
					enqueue(part)
				}
			}
		}
		seen[ref] = true
		total += len(bytes)
		if len(seen) > 20000 || total > 512<<20 {
			return nil, s.Reject("HANDOFF_LIMIT", "闭包数量/大小超限")
		}
		sealedSource := false
		for prefix := range sealedPackages {
			sealedSource = sealedSource || strings.HasPrefix(ref, prefix+"/")
		}
		localBaseline := sealedSource || contractHandoffUIKind(b.Handoff) == "existing-ui-baseline" && strings.HasPrefix(ref, kernel.Text(contractHandoffUIRef(b.Handoff)["persisted_ref"])+"/")
		if !localBaseline && (strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml") || strings.HasSuffix(ref, ".json")) {
			doc, e := schema.Parse(bytes)
			if e != nil {
				return nil, e
			}
			value := kernel.SemMap(doc)
			if boundedPlan && value["gate_id"] == "gate.plan-approved" && kernel.Text(value["decision"]) != "" && kernel.Text(value["actor_kind"]) != "" {
				opts := map[string]string{}
				if value["plan_review_binding"] != nil && kernel.Text(value["review_task_ref"]) != "" {
					opts["task-ref"] = kernel.Text(value["review_task_ref"])
				}
				owner, e := dependencies.SourcePlanApprovalCheckpoint(s, ref, opts)
				if e != nil {
					return nil, e
				}
				enqueue(owner)
				if opts["task-ref"] == "" {
					enqueue(kernel.TrackerRef)
				}
				if exists, e := s.Exists(".template-spec/process/harness-profile.yaml"); e != nil {
					return nil, e
				} else if exists {
					enqueue(".template-spec/process/harness-profile.yaml")
				}
			}
			documentRef = ref
			refs(doc, "")
			if dependencyErr != nil {
				return nil, dependencyErr
			}
		}
		if !localBaseline && strings.HasSuffix(ref, ".md") {
			for _, match := range regexp.MustCompile(`!?\[[^\]]*\]\(<?([^\s)>]+)>?(?:\s+[^)]*)?\)`).FindAllStringSubmatch(string(bytes), -1) {
				dep, e := contractLocalDependency(ref, match[1])
				if e != nil {
					return nil, s.Reject("HANDOFF_PATH", e.Error())
				}
				if dep != "" {
					enqueue(dep)
				}
			}
		}
	}
	out := []string{}
	for ref := range seen {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out, nil
}

func contractLocalDependency(from, href string) (string, error) {
	if href == "" || regexp.MustCompile(`(?i)^(#|https?:|mailto:|data:)`).MatchString(href) {
		return "", nil
	}
	if regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:`).MatchString(href) {
		return "", fmt.Errorf("不支持资源协议")
	}
	parts := strings.FieldsFunc(href, func(r rune) bool { return r == '?' || r == '#' })
	if len(parts) == 0 {
		return "", nil
	}
	clean, e := url.PathUnescape(parts[0])
	if e != nil || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("资源须为相对路径")
	}
	ref := path.Join(path.Dir(from), clean)
	if !kernel.ContractPath(ref) {
		return "", fmt.Errorf("资源路径越界")
	}
	return ref, nil
}

// Imported routing belongs to the actual receiving Profile, not a mutable
// self-described target identity in the receipt.
func ContractReceiptConsumer(s *kernel.SemanticSession, receipt map[string]any, capability string) error {
	if kernel.ContractN(receipt["schema_version"]) < 2 {
		return nil
	}
	profile, err := s.Doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	if receipt["target_profile_id"] != profile["profile_id"] || !kernel.SemHas(receipt["selected_consumer_capabilities"], capability) {
		return s.Reject("HANDOFF_CONSUMER", "导入收据未绑定当前 Profile 的消费者能力")
	}
	if declared, present := kernel.SemMap(profile["handoff"])["consumer_capabilities"]; present && !kernel.SemHas(declared, capability) {
		return s.Unavailable("CAPABILITY", "当前 Profile 未登记所需交接消费能力")
	}
	return nil
}

func ContractHandoffConsumption(s *kernel.SemanticSession, data map[string]any, opts map[string]string) error {
	consumer, slice := kernel.First(opts["consumer"], "tactical"), opts["slice"]
	if consumer == "frontend" {
		return s.Reject("HANDOFF_CONSUMER", "前端消费者已从生命周期移除")
	}
	if consumer != "tactical" {
		return s.Reject("HANDOFF_CONSUMER", "未知消费者")
	}
	if consumer == "tactical" && kernel.ContractN(data["schema_version"]) == 2 && !kernel.SemHas([]string{"domain-driven", "layered-mvc"}, kernel.Text(kernel.SemMap(data["architecture"])["family"])) {
		return s.Reject("HANDOFF_CONSUMER", "未知技术设计架构")
	}
	identity, e := s.Doc("yss-project.yaml")
	if e != nil {
		return e
	}
	if kernel.Text(identity["repository_mode"]) != "project-instance" {
		return s.Reject("HANDOFF_CONSUMER", "接收端必须 project-instance")
	}
	if slice != "" && kernel.Text(data["status"]) != "approved" {
		return s.Reject("HANDOFF_CONSUMER", "当前切片不能消费未批准接收合同")
	}
	binding := kernel.SemMap(data["strategic_handoff"])
	if e = dependencies.ContractRequired(s, binding, "import_receipt_ref", "context_reconciliation_ref", "bundle_digest"); e != nil {
		return e
	}
	receipt, e := s.Doc(kernel.Text(binding["import_receipt_ref"]))
	if e != nil {
		return e
	}
	base := path.Join("docs/handoffs", kernel.Text(receipt["bundle_id"]), kernel.Text(receipt["version"]))
	if kernel.Text(binding["import_receipt_ref"]) != base+"/import-receipt.json" || kernel.Text(receipt["package_ref"]) != base+"/package" || binding["bundle_digest"] != receipt["bundle_digest"] {
		return s.Reject("HANDOFF_RECEIPT", "收据路径或包身份冲突")
	}
	if e = ContractReceipt(s, receipt); e != nil {
		return e
	}
	capability := "backend-technical-design"
	if e = ContractReceiptConsumer(s, receipt, capability); e != nil {
		return e
	}
	if kernel.ContractN(receipt["schema_version"]) >= 2 {
		route := kernel.ApFind(receipt["routes"], "capability", capability)
		if route == nil || kernel.Text(route["activation"]) == "not-applicable" || route["route_id"] != binding["route_id"] {
			return s.Reject("HANDOFF_ROUTE", "接收未绑定启用路线")
		}
	}
	if e = s.Verify("context-reconciliation", kernel.Text(binding["context_reconciliation_ref"]), map[string]string{"import-receipt": kernel.Text(binding["import_receipt_ref"])}); e != nil {
		return e
	}
	parent := path.Join("docs/handoffs", kernel.Text(receipt["bundle_id"]))
	names, e := s.List(parent)
	if e != nil {
		return e
	}
	latest := ""
	max := 0
	for _, name := range names {
		if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(name) {
			continue
		}
		exists, e := s.Exists(path.Join(parent, name, "import-receipt.json"))
		if e != nil {
			return e
		}
		if exists {
			n, e := strconv.Atoi(name[1:])
			if e != nil {
				return s.Reject("HANDOFF_VERSION", "版本数值超限")
			}
			if n > max {
				max, latest = n, name
			}
		}
	}
	if latest == "" {
		return s.Reject("HANDOFF_RECEIPT", "缺少最新收据")
	}
	currentReceipt, e := s.Doc(path.Join(parent, latest, "import-receipt.json"))
	if e != nil {
		return e
	}
	if kernel.Text(currentReceipt["version"]) != latest || kernel.Text(currentReceipt["package_ref"]) != path.Join(parent, latest, "package") {
		return s.Reject("HANDOFF_RECEIPT", "最新收据路径无效")
	}
	if e = ContractReceipt(s, currentReceipt); e != nil {
		return e
	}
	if e = ContractReceiptConsumer(s, currentReceipt, capability); e != nil {
		return e
	}
	original, e := ContractOpenHandoff(s, kernel.Text(receipt["package_ref"]))
	if e != nil {
		return e
	}
	current := original
	if latest != kernel.Text(receipt["version"]) {
		current, e = ContractOpenHandoff(s, kernel.Text(currentReceipt["package_ref"]))
		if e != nil {
			return e
		}
	}
	if original.Manifest["bundle_digest"] != receipt["bundle_digest"] || current.Manifest["bundle_id"] != receipt["bundle_id"] || current.Manifest["bundle_digest"] != currentReceipt["bundle_digest"] {
		return s.Reject("HANDOFF_RECEIPT", "收据与当前包冲突")
	}
	context, e := s.ContextContract()
	if e != nil {
		return e
	}
	terms := map[string]kernel.Term{}
	for _, t := range context["business_terms"].([]kernel.Term) {
		terms[t.TermRef] = t
	}
	for _, op := range []string{"added", "updated", "deprecated"} {
		for _, v := range kernel.SemList(kernel.SemMap(current.Handoff["context_delta"])[op]) {
			expected := kernel.SemMap(v)
			term, ok := terms[kernel.Text(expected["term_ref"])]
			if op == "deprecated" {
				if ok {
					return s.Reject("HANDOFF_CONTEXT", "废弃术语仍存在")
				}
				continue
			}
			if !ok {
				return s.Reject("HANDOFF_CONTEXT", "目标术语未对账")
			}
			doc := kernel.SemMap(MustParseContract(MustMarshalContract(term)))
			for _, key := range []string{"term", "meaning", "english_identifier", "context_id", "forbidden_aliases"} {
				if !dependencies.ContractSame(expected[key], doc[key]) {
					return s.Reject("HANDOFF_CONTEXT", "目标术语字段未对账")
				}
			}
		}
	}
	known := map[string]map[string]any{}
	for _, v := range current.Rules {
		m := kernel.SemMap(v)
		known[kernel.Text(m["rule_id"])] = m
	}
	for _, v := range current.Scenarios {
		m := kernel.SemMap(v)
		if m["critical"] == true {
			known[kernel.Text(m["scenario_id"])] = m
		}
	}
	if current.Business != nil {
		for _, v := range kernel.SemList(current.Business["tickets"]) {
			m := kernel.SemMap(v)
			row := dependencies.ContractCopy(m)
			if row["source_digest"] == nil {
				row["source_digest"] = m["digest"]
			}
			known[kernel.Text(m["id"])] = row
		}
	}
	rows := map[string]map[string]any{}
	for _, v := range kernel.SemList(binding["rows"]) {
		row := kernel.SemMap(v)
		id := kernel.Text(row["source_id"])
		if id == "" || rows[id] != nil {
			return s.Reject("HANDOFF_MAPPING", "接收来源重复")
		}
		rows[id] = row
	}
	// 与 Node 的 verifyConsumption 同形：记录每条阻断原因；依赖未知或未声明时阻断整个消费者，否则只阻断依赖它的切片。
	all := false
	blockedSlices := map[string]bool{}
	issues := []any{}
	mark := func(id, reason string, row map[string]any) {
		issues = append(issues, map[string]any{"source_id": id, "reason": reason})
		if kernel.Text(row["dependency_status"]) != "known" || !dependencies.ContractUnique(row["dependent_slice_refs"], true) {
			all = true
			return
		}
		for _, ref := range kernel.SemStrings(row["dependent_slice_refs"]) {
			blockedSlices[ref] = true
		}
	}
	ids, seams, e := dependencies.ContractDesignTargets(s, data)
	if e != nil {
		return e
	}
	for id, source := range known {
		row := rows[id]
		if row == nil {
			mark(id, "missing-mapping", nil)
			continue
		}
		if row["source_digest"] != source["source_digest"] {
			mark(id, "stale-source", row)
			continue
		}
		if !kernel.SemHas([]string{"known", "unknown"}, kernel.Text(row["dependency_status"])) || row["dependent_slice_refs"] == nil {
			return s.Reject("HANDOFF_MAPPING", "缺少切片依赖判定")
		}
		if kernel.Text(row["disposition"]) == "implemented" || kernel.Text(row["disposition"]) == "not-applicable" {
			if e = dependencies.ContractTraceRow(s, row, source, ids, seams, slice); e != nil {
				return e
			}
		} else {
			if kernel.Text(row["disposition"]) == "deferred" {
				if e = dependencies.ContractRequired(s, row, "reason", "risk", "owner", "followup_ticket_ref", "verification_plan", "target_version"); e != nil {
					return e
				}
			} else if !kernel.SemHas([]string{"pending", "conflict"}, kernel.Text(row["disposition"])) {
				return s.Reject("HANDOFF_MAPPING", "非法接收状态")
			}
			mark(id, kernel.Text(row["disposition"]), row)
		}
	}
	for id, row := range rows {
		if known[id] == nil {
			mark(id, "removed-or-unknown-source", row)
		}
	}
	if current.Business != nil {
		for _, value := range kernel.SemList(current.Business["tickets"]) {
			ticket := kernel.SemMap(value)
			row := rows[kernel.Text(ticket["id"])]
			for _, value := range kernel.SemList(ticket["source_refs"]) {
				locator := kernel.Text(kernel.SemMap(value)["locator"])
				if known[locator] == nil {
					continue
				}
				mapped := rows[locator]
				incomplete := row == nil || mapped == nil || kernel.Text(row["dependency_status"]) != "known" || kernel.Text(mapped["dependency_status"]) != "known"
				for _, ref := range kernel.SemStrings(row["dependent_slice_refs"]) {
					incomplete = incomplete || !kernel.SemHas(mapped["dependent_slice_refs"], ref)
				}
				if incomplete {
					mark(kernel.Text(ticket["id"]), "business-rule-scenario-dependency-incomplete", row)
				}
			}
		}
	}
	if current.Business != nil && original.Business != nil && !dependencies.ContractSame(current.Business["coverage_deferred"], original.Business["coverage_deferred"]) {
		all = true
		issues = append(issues, map[string]any{"source_id": "business-ticket-coverage", "reason": "deferred-scope-stale"})
	}
	if current.Business != nil && original.Business != nil {
		prior := map[string]any{}
		for _, value := range kernel.SemList(original.Business["tickets"]) {
			ticket := kernel.SemMap(value)
			prior[kernel.Text(ticket["id"])] = ticket["source_digest"]
		}
		for _, value := range kernel.SemList(current.Business["tickets"]) {
			ticket := kernel.SemMap(value)
			if !dependencies.ContractSame(prior[kernel.Text(ticket["id"])], ticket["source_digest"]) {
				mark(kernel.Text(ticket["id"]), "business-ticket-stale", rows[kernel.Text(ticket["id"])])
			}
		}
	}
	for _, key := range []string{"spec_ref", "business_ticket_set_ref", "prototype_ref", "visual_baseline_ref", "existing_ui_baseline_ref"} {
		if key == "business_ticket_set_ref" && current.Business != nil && original.Business != nil {
			continue
		}
		if kernel.SemMap(kernel.SemMap(current.Handoff["source"])[key])["digest"] != kernel.SemMap(kernel.SemMap(original.Handoff["source"])[key])["digest"] {
			all = true
			issues = append(issues, map[string]any{"source_id": key, "reason": "upstream-asset-stale"})
		}
	}
	strategyBasis := func(v map[string]any) map[string]any {
		out := dependencies.ContractWithout(v, "domain_version", "approval", "rule_catalog", "scenarios", "invariants")
		invariants := []any{}
		for _, v := range kernel.SemList(v["invariants"]) {
			invariants = append(invariants, dependencies.ContractWithout(kernel.SemMap(v), "statement"))
		}
		out["invariants"] = invariants
		return out
	}
	if !dependencies.ContractSame(strategyBasis(current.Strategy), strategyBasis(original.Strategy)) || !dependencies.ContractSame(dependencies.ContractWithout(current.Stage, "package_version", "approval", "domain_strategy_ref"), dependencies.ContractWithout(original.Stage, "package_version", "approval", "domain_strategy_ref")) || contractHandoffUIKind(current.Handoff) != contractHandoffUIKind(original.Handoff) || !dependencies.ContractSame(current.Config["prototype"], original.Config["prototype"]) {
		all = true
		issues = append(issues, map[string]any{"source_id": "strategic-design-basis", "reason": "upstream-design-stale"})
	}
	blockedHere := all || (slice != "" && blockedSlices[slice]) || (slice == "" && len(issues) > 0)
	blockedRefs := []string{}
	for ref := range blockedSlices {
		blockedRefs = append(blockedRefs, ref)
	}
	sort.Strings(blockedRefs)
	complete := true
	for id := range known {
		complete = complete && rows[id] != nil
	}
	scope := slice
	if scope == "" {
		scope = "whole-" + consumer + "-design"
	}
	report := map[string]any{"result": map[bool]string{true: "blocked", false: "verified"}[blockedHere], "bundle_digest": current.Manifest["bundle_digest"], "consumed_bundle_digest": receipt["bundle_digest"], "scope": scope, "coverage_complete": complete, "block_all": all, "blocked_slice_refs": blockedRefs, "issues": issues}
	report["tactical_digest"] = kernel.ApDigest(data)
	if kernel.ContractN(data["schema_version"]) == 2 {
		report["technical_design_digest"] = kernel.ApDigest(data)
	}
	// 命令行薄壳按 Node 的 verify-strategic-handoff-consumption 输出形态读取这份报告。
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	s.Report.Coverage["handoff_consumption"] = report
	if blockedHere {
		return s.Reject("HANDOFF_STALE", "来源变化、缺失/延期承接或未知依赖阻断当前范围")
	}
	return nil
}

func MustParseContract(b []byte) any { v, _ := schema.Parse(b); return v }

func ContractReceipt(s *kernel.SemanticSession, r map[string]any) error {
	switch kernel.ContractN(r["schema_version"]) {
	case 1:
		if e := dependencies.ContractRequired(s, r, "bundle_id", "version", "bundle_digest", "package_ref", "target_context_digest", "status"); e != nil {
			return e
		}
	case 2:
		if e := s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-import-receipt.schema.json", r); e != nil {
			return e
		}
	case 3:
		if e := s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-import-receipt-v3.schema.json", r); e != nil {
			return e
		}
		b, e := s.Bind(kernel.Text(r["source_delivery_record_ref"]))
		if e != nil {
			return e
		}
		if b.Digest != kernel.Text(r["source_delivery_record_sha256"]) {
			return s.Reject("HANDOFF_RECEIPT", "来源交付记录摘要冲突")
		}
	default:
		return s.Unavailable("CAPABILITY", "未知导入收据协议")
	}
	bundle, e := ContractOpenHandoff(s, kernel.Text(r["package_ref"]))
	if e != nil {
		return e
	}
	if r["bundle_id"] != bundle.Manifest["bundle_id"] || r["version"] != bundle.Manifest["version"] || r["bundle_digest"] != bundle.Manifest["bundle_digest"] {
		return s.Reject("HANDOFF_RECEIPT", "收据与当前包身份不一致")
	}
	if kernel.ContractN(bundle.Handoff["schema_version"]) == 5 && kernel.ContractN(r["schema_version"]) != 3 {
		return s.Reject("HANDOFF_RECEIPT", "Handoff v5必须使用绑定真实交付记录的receipt v3")
	}
	if kernel.ContractN(r["schema_version"]) == 3 {
		base := path.Join("docs/handoffs", kernel.Text(r["bundle_id"]), kernel.Text(r["version"]))
		if r["source_delivery_record_ref"] != base+"/source-delivery-record.json" || r["package_ref"] != base+"/package" {
			return s.Reject("HANDOFF_RECEIPT", "来源交付记录或包路径非法")
		}
		record, e := s.Doc(kernel.Text(r["source_delivery_record_ref"]))
		if e != nil {
			return e
		}
		if e = s.ValidateSchema(".template-spec/process/schemas/strategic-handoff-delivery.schema.json", record); e != nil {
			return e
		}
		if e = contractDeliveryRecordIdentity(s, bundle, record); e != nil {
			return e
		}
	}
	return nil
}

func contractHandoffBaseline(s *kernel.SemanticSession, h map[string]any) (map[string]any, error) {
	if contractHandoffUIKind(h) == "not-applicable" {
		return map[string]any{"cases": []any{}}, nil
	}
	binding := contractHandoffUIRef(h)
	ref := path.Join(kernel.Text(binding["persisted_ref"]), kernel.Text(binding["manifest_ref"]))
	var baseline map[string]any
	var e error
	if contractHandoffUIKind(h) == "existing-ui-baseline" {
		baseline, e = dependencies.ContractExistingUI(s, ref)
	} else {
		baseline, e = dependencies.ContractVisualBaseline(s, ref)
	}
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, v := range kernel.SemList(baseline["cases"]) {
		ids = append(ids, kernel.Text(kernel.SemMap(v)["case_id"]))
	}
	if baseline["baseline_id"] != binding["baseline_id"] || baseline["version"] != binding["version"] || !kernel.SameStringSet(ids, binding["case_ids"]) {
		return nil, s.Reject("HANDOFF_UI", "基线身份/版本/用例冲突")
	}
	return baseline, nil
}

func contractPreview(s *kernel.SemanticSession, config map[string]any, cases []string) error {
	if !kernel.SemHas([]string{"H1", "H2"}, kernel.Text(config["profile"])) || !dependencies.ContractWithin(kernel.Text(config["entry_ref"]), kernel.Text(config["preview_root"])) || kernel.Text(config["entry_ref"]) == kernel.Text(config["preview_root"]) {
		return s.Reject("HANDOFF_PREVIEW", "预览类型或入口范围非法")
	}
	digest, files, e := dependencies.ContractTree(s, kernel.Text(config["preview_root"]))
	if e != nil {
		return e
	}
	if len(files) == 0 {
		return s.Reject("HANDOFF_PREVIEW", "预览目录为空")
	}
	for _, ref := range files {
		if !strings.HasSuffix(strings.ToLower(ref), ".html") && !strings.HasSuffix(strings.ToLower(ref), ".css") {
			continue
		}
		bytes, e := s.Bytes(ref)
		if e != nil {
			return e
		}
		matches := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']([^"']+)["']`).FindAllStringSubmatch(string(bytes), -1)
		matches = append(matches, regexp.MustCompile(`(?i)url\(\s*["']?([^\s)'";]+)["']?\s*\)`).FindAllStringSubmatch(string(bytes), -1)...)
		for _, match := range matches {
			if regexp.MustCompile(`(?i)^(?:https?:)?//`).MatchString(match[1]) {
				return s.Reject("HANDOFF_PREVIEW", "离线预览包含远程资源")
			}
			dep, e := contractLocalDependency(ref, match[1])
			if e != nil {
				return s.Reject("HANDOFF_PREVIEW", e.Error())
			}
			if dep != "" && !kernel.SemHas(files, dep) {
				return s.Reject("HANDOFF_PREVIEW", "离线资源未打包")
			}
		}
	}
	if kernel.Text(config["profile"]) == "H2" {
		sourceDigest, sourceFiles, e := dependencies.ContractTree(s, kernel.Text(config["source_root"]))
		if e != nil {
			return e
		}
		if sourceDigest != kernel.Text(config["source_digest"]) || !strings.HasPrefix(kernel.Text(config["lock_ref"]), kernel.Text(config["source_root"])+"/") {
			return s.Reject("HANDOFF_PREVIEW", "H2 源码或锁文件批准摘要变化")
		}
		if _, e = s.Bytes(kernel.Text(config["lock_ref"])); e != nil {
			return e
		}
		for _, ref := range sourceFiles {
			for _, part := range strings.Split(ref, "/") {
				if part == "node_modules" || part == ".git" {
					return s.Reject("HANDOFF_PREVIEW", "H2 源包含依赖缓存")
				}
			}
		}
	}
	receipt, e := dependencies.ContractBoundDoc(s, map[string]any{"ref": config["verification_ref"], "digest": config["verification_digest"]})
	if e != nil {
		return e
	}
	exit, ok := kernel.Integer(receipt["exit_code"])
	if kernel.Text(receipt["network_mode"]) != "offline" || !ok || exit != 0 || kernel.Text(receipt["command"]) == "" || !dependencies.ContractDate(receipt["executed_at"]) || kernel.Text(receipt["preview_digest"]) != digest || !dependencies.ContractUnique(receipt["case_ids"], true) {
		return s.Reject("HANDOFF_PREVIEW", "真实离线浏览记录未绑定当前预览")
	}
	for _, id := range cases {
		if !kernel.SemHas(receipt["case_ids"], id) {
			return s.Reject("HANDOFF_PREVIEW", "离线验证未覆盖视觉用例")
		}
	}
	return dependencies.ContractEvidence(s, receipt["evidence_refs"])
}

func contractNormalizeSourceRoles(s *kernel.SemanticSession) {
	roles := dependencies.ContractCopy(s.Roles)
	policy := dependencies.ContractCopy(kernel.SemMap(roles["gate_policy"]))
	review := kernel.SemList(policy["digital_human_review"])
	for _, v := range kernel.SemList(policy["check_reviews"]) {
		m := kernel.SemMap(v)
		exists := false
		for _, r := range review {
			exists = exists || kernel.SemMap(r)["gate"] == m["gate"]
		}
		for _, r := range kernel.SemList(policy["dual_digital_human"]) {
			exists = exists || kernel.SemMap(r)["gate"] == m["gate"]
		}
		if !exists {
			review = append(review, v)
		}
	}
	policy["digital_human_review"] = review
	veto := kernel.SemStrings(policy["product_digital_human_with_biological_veto"])
	for _, gate := range veto {
		if kernel.SemHas(policy["biological_human"], gate) || kernel.ApFind(policy["digital_human_review"], "gate", gate) != nil || kernel.ApFind(policy["dual_digital_human"], "gate", gate) != nil {
			continue
		}
		policy["digital_human_review"] = append(kernel.ApArray(policy["digital_human_review"]), map[string]any{"gate": gate, "countersigners": []string{"role.product-manager"}})
	}
	roles["gate_policy"] = policy
	s.Roles = roles
}

func sourceClosureBindings(s *kernel.SemanticSession, refs []string) ([]any, error) {
	rows := []any{}
	for _, ref := range refs {
		binding, e := s.Bind(ref)
		if e != nil {
			return nil, e
		}
		rows = append(rows, map[string]any{"ref": ref, "digest": binding.Digest})
	}
	return rows, nil
}
func handoffSourceCoverage(report, source *kernel.SemanticSession, ref string, h *NativeHandoff) error {
	refs, e := contractHandoffClosure(source, ref, h)
	if e != nil {
		return e
	}
	closure, e := sourceClosureBindings(source, refs)
	if e != nil {
		return e
	}
	kernel.SemanticCoverage(report, "handoff_source", map[string]any{"handoff": h.Handoff, "config": h.Config, "indexes": map[string]any{"rules": h.Rules, "scenarios": h.Scenarios}, "strategy": h.Strategy, "stage": h.Stage, "business": h.BusinessReport, "closure": closure})
	return nil
}
func handoffPackageCoverage(s *kernel.SemanticSession, h *NativeHandoff) error {
	prefix := "payload/files"
	for _, entry := range kernel.SemList(h.Manifest["files"]) {
		row := kernel.SemMap(entry)
		if row["original_ref"] == h.Manifest["handoff_ref"] && row["path"] == "handoff.yaml" {
			prefix = "."
		}
	}
	kernel.SemanticCoverage(s, "handoff_package", map[string]any{"bundle_id": h.Manifest["bundle_id"], "version": h.Manifest["version"], "bundle_digest": h.Manifest["bundle_digest"], "rules": len(h.Rules), "scenarios": len(h.Scenarios), "manifest": h.Manifest, "changes": h.Changes, "source_prefix": prefix})
	return handoffSourceCoverage(s, h.Source, kernel.Text(h.Manifest["handoff_ref"]), h)
}
func verifyStrategicDeliveryPublic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	recordRef := path.Join(ref, "delivery-record.json")
	h, record, e := ContractOpenStrategicDelivery(s, recordRef)
	if e != nil {
		return e
	}
	verification, e := s.Doc(path.Join(ref, kernel.Text(record["verification_ref"])))
	if e != nil {
		return e
	}
	bound, e := s.Bind(recordRef)
	if e != nil {
		return e
	}
	if e = handoffPackageCoverage(s, h); e != nil {
		return e
	}
	kernel.SemanticCoverage(s, "handoff_delivery", map[string]any{"record": record, "verification": verification, "record_ref": recordRef, "record_digest": bound.Digest, "package_ref": path.Join(ref, kernel.Text(record["package_ref"]))})
	return nil
}
func verifySourceApprovalPublic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	binding, e := s.Doc(ref)
	if e != nil {
		return e
	}
	gate := opts["gate"]
	approvalRef := kernel.Text(binding["approval_ref"])
	if gate == "" || approvalRef == "" {
		return s.Unavailable("ARGUMENT", "来源批准需要 gate 与 binding.approval_ref")
	}
	saved := s.Roles
	defer func() { s.Roles = saved }()
	contractNormalizeSourceRoles(s)
	if e = contractSourceApproval(s, gate, binding, approvalRef); e != nil {
		return e
	}
	kernel.SemanticCoverage(s, "source_approval", map[string]any{"result": "passed"})
	return nil
}
