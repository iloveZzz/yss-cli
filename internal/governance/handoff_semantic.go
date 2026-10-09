package governance

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"golang.org/x/text/cases"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func init() {
	registerSemanticValidator("handoff-package", verifyHandoffPackageSemantic)
	registerSemanticValidator("handoff-source", func(s *semanticSession, ref string, opts map[string]string) error {
		_, err := contractInspectHandoff(s, ref)
		return err
	})
	registerSemanticValidator("strategic-delivery", func(s *semanticSession, ref string, opts map[string]string) error {
		_, _, err := contractOpenStrategicDelivery(s, ref)
		return err
	})
	registerSemanticValidator("handoff-consumption", func(s *semanticSession, ref string, opts map[string]string) error {
		doc, e := s.doc(ref)
		if e != nil {
			return e
		}
		consumer := first(opts["consumer"], "tactical")
		version, ok := integer(doc["schema_version"])
		if !ok || !((consumer == "tactical" && (version == 1 || version == 2)) || (consumer == "frontend" && (version == 1 || version == 2 || version == 3))) {
			return s.unavailable("CAPABILITY", "未知 Handoff 消费合同类型或版本")
		}
		return contractHandoffConsumption(s, doc, opts)
	})
}

type nativeHandoff struct {
	Manifest, Handoff, Strategy, Stage, Config, Changes, Business map[string]any
	Rules, Scenarios                                              []any
	Source                                                        *semanticSession
	Prefix                                                        string
}

func verifyHandoffPackageSemantic(s *semanticSession, ref string, opts map[string]string) error {
	_, e := contractOpenHandoff(s, ref)
	return e
}

// Consume the published v1 strategic delivery wrapper as an observed directory.
// Neither its persisted command nor its optional archive is executed/extracted.
func contractOpenStrategicDelivery(s *semanticSession, recordRef string) (*nativeHandoff, map[string]any, error) {
	if path.Base(recordRef) != "delivery-record.json" || path.Dir(recordRef) == "." || !contractPath(recordRef) {
		return nil, nil, s.reject("HANDOFF_DELIVERY", "战略交付必须登记独立目录中的 delivery-record.json")
	}
	record, e := s.doc(recordRef)
	if e != nil {
		return nil, nil, e
	}
	if e = s.validateSchema(".template-spec/process/schemas/strategic-handoff-delivery.schema.json", record); e != nil {
		return nil, nil, e
	}
	prefix := path.Dir(recordRef)
	report, e := s.doc(path.Join(prefix, text(record["verification_ref"])))
	if e != nil {
		return nil, nil, e
	}
	if e = s.validateSchema(".template-spec/process/schemas/strategic-handoff-delivery-verification.schema.json", report); e != nil {
		return nil, nil, e
	}
	zip, hasZIP := object(record["zip"])
	if hasZIP {
		if _, e = backendBound(s, map[string]any{"ref": path.Join(prefix, text(zip["ref"])), "digest": zip["sha256"]}); e != nil {
			return nil, nil, e
		}
	}
	files, e := s.scan(prefix)
	if e != nil {
		return nil, nil, e
	}
	allowed := map[string]bool{recordRef: true, path.Join(prefix, "verification.json"): true}
	if hasZIP {
		allowed[path.Join(prefix, "package.zip")] = true
	}
	for _, ref := range files {
		if !strings.HasPrefix(ref, path.Join(prefix, "package")+"/") && !allowed[ref] {
			return nil, nil, s.reject("HANDOFF_DELIVERY", "战略交付目录包含未登记文件: "+ref)
		}
	}
	bundle, e := contractOpenHandoff(s, path.Join(prefix, text(record["package_ref"])))
	if e != nil {
		return nil, nil, e
	}
	if report["bundle_digest"] != bundle.Manifest["bundle_digest"] || report["package_ref"] != record["package_ref"] || report["manifest_ref"] != record["manifest_ref"] {
		return nil, nil, s.reject("HANDOFF_DELIVERY", "交付验证与当前包身份矛盾")
	}
	if e = contractDeliveryRecordIdentity(s, bundle, record); e != nil {
		return nil, nil, e
	}
	return bundle, record, nil
}
func contractDeliveryRecordIdentity(s *semanticSession, bundle *nativeHandoff, record map[string]any) error {
	h := semMap(record["handoff"])
	if h["id"] != bundle.Manifest["bundle_id"] || h["version"] != bundle.Manifest["version"] || h["schema_version"] != bundle.Handoff["schema_version"] || record["bundle_digest"] != bundle.Manifest["bundle_digest"] {
		return s.reject("HANDOFF_DELIVERY", "交付记录、验证与当前包身份矛盾")
	}
	found := false
	for _, item := range semList(bundle.Manifest["files"]) {
		row := semMap(item)
		found = found || row["original_ref"] == h["ref"] && row["sha256"] == h["sha256"]
	}
	if !found {
		return s.reject("HANDOFF_DELIVERY", "交付记录缺少 Handoff 原始字节绑定")
	}
	assets, keys := []any{}, []string{}
	for key := range semMap(bundle.Handoff["source"]) {
		keys = append(keys, key)
	}
	order := collate.New(language.English)
	sort.SliceStable(keys, func(i, j int) bool { return order.CompareString(keys[i], keys[j]) < 0 })
	for _, key := range keys {
		v := semMap(semMap(bundle.Handoff["source"])[key])
		id := v["id"]
		if id == nil {
			id = v["baseline_id"]
		}
		assets = append(assets, map[string]any{"source_key": key, "id": id, "version": v["version"], "ref": v["persisted_ref"], "digest": v["digest"]})
	}
	routes := []any{}
	for _, item := range semList(bundle.Handoff["consumer_routes"]) {
		v := semMap(item)
		routes = append(routes, map[string]any{"route_id": v["route_id"], "capability": v["capability"], "activation": v["activation"]})
	}
	var previous any
	if prior, present := object(bundle.Manifest["previous_bundle"]); present {
		previous = map[string]any{"handoff_id": prior["bundle_id"], "version": prior["version"], "bundle_digest": prior["digest"]}
	}
	if !contractSame(assets, record["source_assets"]) || !contractSame(routes, record["consumer_routes"]) || !contractSame(previous, record["previous_delivery"]) {
		return s.reject("HANDOFF_DELIVERY", "交付记录来源、路由或上一版本与批准包不一致")
	}
	return nil
}

func contractOpenHandoff(s *semanticSession, prefix string) (*nativeHandoff, error) {
	if strings.EqualFold(path.Ext(prefix), ".zip") {
		archive, root, e := s.zipSourceSession(prefix)
		if e != nil {
			return nil, e
		}
		return contractOpenHandoff(archive, root)
	}
	manifest, e := s.doc(path.Join(prefix, "manifest.json"))
	if e != nil {
		return nil, e
	}
	if e = s.validateSchema(".template-spec/process/schemas/strategic-handoff-package.schema.json", manifest); e != nil {
		return nil, e
	}
	if text(manifest["bundle_digest"]) != contractDigest(contractWithout(manifest, "bundle_digest")) {
		return nil, s.reject("HANDOFF_MANIFEST", "包清单摘要不一致")
	}
	names, originals := []string{"manifest.json"}, map[string]bool{}
	folds := map[string]bool{"manifest.json": true}
	fold := cases.Fold()
	total := 0
	for _, v := range semList(manifest["files"]) {
		file := semMap(v)
		ref := text(file["path"])
		for _, candidate := range []string{ref, text(file["original_ref"])} {
			if e = rejectProgressionEvidence(s, candidate); e != nil {
				return nil, e
			}
		}
		if !contractPath(ref) || folds[fold.String(ref)] {
			return nil, s.reject("HANDOFF_PATH", "包路径非法或大小写冲突")
		}
		folds[fold.String(ref)] = true
		names = append(names, ref)
		b, e := s.bytes(path.Join(prefix, ref))
		if e != nil {
			return nil, e
		}
		if len(b) != contractN(file["size_bytes"]) || "sha256:"+safefs.Digest(b) != text(file["sha256"]) {
			return nil, s.reject("HANDOFF_FILE_STALE", "文件摘要或大小不一致: "+ref)
		}
		total += len(b)
		if original := text(file["original_ref"]); original != "" {
			if !contractPath(original) || originals[fold.String(original)] {
				return nil, s.reject("HANDOFF_PATH", "原始路径非法或重复")
			}
			expected := "payload/files/" + original
			if original == "CONTEXT.md" {
				expected = "payload/files/source-context.snapshot.md"
			}
			legacyExpected := "payload/files/" + original
			if original == "CONTEXT.md" {
				legacyExpected = "payload/source-context.snapshot.md"
			}
			if original == text(manifest["handoff_ref"]) {
				legacyExpected = "handoff.yaml"
			}
			if ref != expected && ref != legacyExpected {
				return nil, s.unavailable("READONLY_SOURCE_LAYOUT_REQUIRED", "包需要只读来源布局")
			}
			originals[fold.String(original)] = true
		}
	}
	if total > 512<<20 || len(names) > 20001 {
		return nil, s.reject("HANDOFF_LIMIT", "包大小或数量超限")
	}
	files, e := s.scan(prefix)
	if e != nil {
		return nil, e
	}
	actual := []string{}
	for _, f := range files {
		actual = append(actual, strings.TrimPrefix(f, prefix+"/"))
	}
	if !contractSame(contractSorted(names), contractSorted(actual)) {
		return nil, s.reject("HANDOFF_MANIFEST", "包文件未登记、缺失或额外")
	}
	aliases := map[string]string{}
	sourcePrefix := path.Join(prefix, "payload/files")
	legacy := false
	for _, entry := range semList(manifest["files"]) {
		file := semMap(entry)
		if text(file["original_ref"]) == text(manifest["handoff_ref"]) && text(file["path"]) == "handoff.yaml" {
			legacy = true
		}
	}
	if legacy {
		sourcePrefix = prefix
	}
	for _, entry := range semList(manifest["files"]) {
		file := semMap(entry)
		if original := text(file["original_ref"]); original != "" {
			stored := strings.TrimPrefix(text(file["path"]), "payload/files/")
			if legacy {
				stored = text(file["path"])
			}
			aliases[original] = stored
			if original != "CONTEXT.md" && !(legacy && original == text(manifest["handoff_ref"])) {
				for a, b := path.Dir(original), path.Dir(stored); a != "." && b != "."; a, b = path.Dir(a), path.Dir(b) {
					if previous, ok := aliases[a]; ok && previous != b {
						return nil, s.reject("HANDOFF_PATH", "目录映射冲突")
					}
					aliases[a] = b
				}
			}
		}
	}
	if legacy && aliases["CONTEXT.md"] != "payload/source-context.snapshot.md" || !legacy && aliases["CONTEXT.md"] != "source-context.snapshot.md" {
		return nil, s.reject("HANDOFF_PATH", "来源布局混合或缺少根词汇快照")
	}
	source, e := s.sourceSnapshotSession(sourcePrefix, aliases)
	if e != nil {
		return nil, e
	}
	source.roles, e = source.doc(".template-spec/agents/digital-human-roles.yaml")
	if e != nil {
		return nil, e
	}
	if len(semMap(source.roles["gate_policy"])) == 0 {
		return nil, source.reject("HANDOFF_POLICY", "来源角色策略缺少 gate_policy")
	}
	published := []any{}
	policy := semMap(source.roles["gate_policy"])
	for _, bucket := range []string{"biological_human", "product_digital_human_with_biological_veto"} {
		for _, id := range semStrings(policy[bucket]) {
			published = append(published, map[string]any{"id": id})
		}
	}
	for _, bucket := range []string{"dual_digital_human", "digital_human_review", "check_reviews"} {
		for _, row := range semList(policy[bucket]) {
			published = append(published, map[string]any{"id": semMap(row)["gate"]})
		}
	}
	source.registry = map[string]any{"gates": published, "id_policy": map[string]any{"deprecated_ids": []any{}}}
	source.report.Checks = append(source.report.Checks, SemanticCheck{ID: "handoff-source-policy-v1", SourceRef: ".template-spec/agents/digital-human-roles.yaml", Status: "passed"})
	handoff, e := source.doc(text(manifest["handoff_ref"]))
	if e != nil {
		return nil, e
	}
	if contractN(handoff["schema_version"]) == 5 {
		for _, entry := range semList(manifest["files"]) {
			file := semMap(entry)
			if original := text(file["original_ref"]); original != "" {
				expected := "payload/files/" + original
				if original == "CONTEXT.md" {
					expected = "payload/files/source-context.snapshot.md"
				}
				if text(file["path"]) != expected {
					return nil, s.reject("HANDOFF_PATH", "Handoff v5 必须保留固定 payload/files 来源布局")
				}
			}
		}
	}
	bundle, e := contractInspectHandoff(source, text(manifest["handoff_ref"]))
	if e != nil {
		return nil, e
	}
	bundle.Manifest, bundle.Source, bundle.Prefix = manifest, source, prefix
	if bundle.Handoff["handoff_id"] != manifest["bundle_id"] || bundle.Handoff["handoff_version"] != manifest["version"] {
		return nil, s.reject("HANDOFF_IDENTITY", "包与 Handoff 身份不一致")
	}
	for kind, expected := range map[string][]any{"rules": bundle.Rules, "scenarios": bundle.Scenarios} {
		b, e := s.bytes(path.Join(prefix, "indexes", kind+".json"))
		if e != nil {
			return nil, e
		}
		parsed, e := schema.Parse(b)
		if e != nil {
			return nil, s.unavailable("INPUT", e.Error())
		}
		if !contractSame(parsed, expected) {
			return nil, s.reject("HANDOFF_INDEX", "索引与来源不一致")
		}
	}
	closure, e := contractHandoffClosure(source, text(manifest["handoff_ref"]), bundle)
	if e != nil {
		return nil, e
	}
	expectedOriginals := []string{}
	for _, v := range semList(manifest["files"]) {
		if ref := text(semMap(v)["original_ref"]); ref != "" {
			expectedOriginals = append(expectedOriginals, ref)
		}
	}
	if !contractSame(contractSorted(closure), contractSorted(expectedOriginals)) {
		return nil, s.reject("HANDOFF_CLOSURE", "源快照闭包不一致")
	}
	changes, e := s.doc(path.Join(prefix, "indexes/changes.json"))
	if e != nil {
		return nil, e
	}
	if !contractSame(changes["previous_bundle"], manifest["previous_bundle"]) {
		return nil, s.reject("HANDOFF_PREVIOUS", "差异前版绑定不一致")
	}
	old := map[string]string{}
	if manifest["previous_bundle"] != nil {
		previous, e := s.doc(path.Join(prefix, "indexes/previous-manifest.json"))
		if e != nil {
			return nil, e
		}
		prior := semMap(manifest["previous_bundle"])
		if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(text(previous["version"])) || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(text(manifest["version"])) {
			return nil, s.reject("HANDOFF_PREVIOUS", "前版版本格式非法")
		}
		pv, _ := strconv.Atoi(text(previous["version"])[1:])
		cv, _ := strconv.Atoi(text(manifest["version"])[1:])
		if text(previous["bundle_digest"]) != contractDigest(contractWithout(previous, "bundle_digest")) || previous["bundle_digest"] != prior["digest"] || previous["bundle_id"] != manifest["bundle_id"] || previous["version"] != prior["version"] || pv >= cv {
			return nil, s.reject("HANDOFF_PREVIOUS", "前版清单身份或摘要无效")
		}
		for _, kind := range []string{"rules", "scenarios"} {
			b, e := s.bytes(path.Join(prefix, "indexes/previous-"+kind+".json"))
			if e != nil {
				return nil, e
			}
			entry := apFind(previous["files"], "path", "indexes/"+kind+".json")
			if entry == nil || contractN(entry["size_bytes"]) != len(b) || text(entry["sha256"]) != "sha256:"+safefs.Digest(b) {
				return nil, s.reject("HANDOFF_PREVIOUS", "前版索引字节不一致")
			}
			value, e := schema.Parse(b)
			if e != nil {
				return nil, e
			}
			for _, v := range semList(value) {
				m := semMap(v)
				old[first(text(m["rule_id"]), text(m["scenario_id"]))] = text(m["source_digest"])
			}
		}
	}
	now := map[string]string{}
	for _, v := range append(append([]any{}, bundle.Rules...), bundle.Scenarios...) {
		m := semMap(v)
		now[first(text(m["rule_id"]), text(m["scenario_id"]))] = text(m["source_digest"])
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
	expected := map[string]any{"previous_bundle": manifest["previous_bundle"], "added": contractSorted(added), "updated": contractSorted(updated), "removed": contractSorted(removed)}
	if !contractSame(changes, expected) {
		return nil, s.reject("HANDOFF_CHANGES", "版本差异与索引不一致")
	}
	bundle.Changes = changes
	return bundle, nil
}
func contractInspectHandoff(s *semanticSession, ref string) (*nativeHandoff, error) {
	h, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	n := contractN(h["schema_version"])
	if !semHas([]string{"3", "4", "5"}, strconv.Itoa(n)) {
		return nil, s.unavailable("CAPABILITY", "未知 Handoff 协议")
	}
	if e = s.validateSchema(fmt.Sprintf(".template-spec/process/schemas/strategic-design-handoff-v%d.schema.json", n), h); e != nil {
		return nil, e
	}
	if text(h["status"]) != "approved" {
		return nil, s.reject("HANDOFF_APPROVAL", "交接尚未 approved")
	}
	config := semMap(h["package_export"])
	exportSchema := ".template-spec/process/schemas/strategic-handoff-export.schema.json"
	if n == 5 {
		exportSchema = ".template-spec/process/schemas/strategic-handoff-export-v2.schema.json"
	}
	if e = s.validateSchema(exportSchema, config); e != nil {
		return nil, e
	}
	if n == 5 && config["ui_baseline_kind"] != h["ui_baseline_kind"] {
		return nil, s.reject("HANDOFF_UI", "交接和导出基线类型冲突")
	}
	context, e := s.contextSnapshot(semMap(h["source_context_snapshot"]))
	if e != nil {
		return nil, e
	}
	terms := map[string]map[string]any{}
	for _, term := range context["business_terms"].([]Term) {
		raw, _ := schema.Parse(mustMarshalContract(term))
		terms[term.TermRef] = semMap(raw)
	}
	used := map[string]bool{}
	for _, op := range []string{"added", "updated", "deprecated"} {
		for _, v := range semList(semMap(h["context_delta"])[op]) {
			term := semMap(v)
			id := text(term["term_ref"])
			if used[id] || !semHas(semMap(h["source_context_snapshot"])["term_refs"], id) || terms[id] == nil {
				return nil, s.reject("HANDOFF_CONTEXT", "术语增量重复、未引用或不存在")
			}
			used[id] = true
			for _, key := range []string{"term", "meaning", "english_identifier", "context_id", "forbidden_aliases"} {
				if !contractSame(term[key], terms[id][key]) {
					return nil, s.reject("HANDOFF_CONTEXT", "术语增量与源 Context 冲突")
				}
			}
		}
	}
	source := semMap(h["source"])
	strategy, e := s.doc(text(semMap(source["domain_strategy_ref"])["persisted_ref"]))
	if e != nil {
		return nil, e
	}
	stage, e := s.doc(text(semMap(source["stage_decision_package_ref"])["persisted_ref"]))
	if e != nil {
		return nil, e
	}
	suffix := ""
	if n >= 4 {
		suffix = "-v3"
	}
	for label, doc := range map[string]map[string]any{"domain-strategy": strategy, "stage-decision-package": stage} {
		if e = s.validateSchema(".template-spec/process/schemas/strategic-handoff-"+label+suffix+".schema.json", doc); e != nil {
			return nil, e
		}
		if text(doc["status"]) != "approved" {
			return nil, s.reject("HANDOFF_SOURCE_APPROVAL", "来源设计未批准")
		}
		contextSource := s
		key := "domain_strategy_ref"
		if label == "stage-decision-package" {
			key = "stage_decision_package_ref"
		}
		if marker := semMap(semMap(semMap(source[key])["approval_context"])["source_baseline"]); len(marker) > 0 {
			_, inherited, err := verifySpecBaselineReceipt(s, text(marker["receipt_ref"]), false)
			if err != nil {
				return nil, err
			}
			contextSource = inherited
		}
		if _, e = contextSource.contextSnapshot(semMap(doc["context_snapshot"])); e != nil {
			return nil, e
		}
	}
	if strategy["domain_strategy_id"] != semMap(source["domain_strategy_ref"])["id"] || strategy["domain_version"] != semMap(source["domain_strategy_ref"])["version"] || stage["stage_decision_id"] != semMap(source["stage_decision_package_ref"])["id"] || stage["package_version"] != semMap(source["stage_decision_package_ref"])["version"] {
		return nil, s.reject("HANDOFF_SOURCE_IDENTITY", "战略和阶段身份/版本冲突")
	}
	binding := semMap(stage["domain_strategy_ref"])
	strategyRef := semMap(source["domain_strategy_ref"])["persisted_ref"]
	if marker := semMap(semMap(semMap(source["stage_decision_package_ref"])["approval_context"])["source_baseline"]); len(marker) > 0 {
		strategyRef = semMap(semMap(semMap(source["domain_strategy_ref"])["approval_context"])["source_baseline"])["source_ref"]
	}
	if binding["domain_strategy_id"] != strategy["domain_strategy_id"] || binding["domain_version"] != strategy["domain_version"] || text(binding["status"]) != "approved" || binding["persisted_ref"] != strategyRef || text(binding["digest"]) != contractDigest(strategy) {
		return nil, s.reject("HANDOFF_SOURCE_STALE", "阶段包战略来源变化")
	}
	for _, v := range semList(stage["unresolved_items"]) {
		if text(semMap(v)["type"]) == "blocker" {
			return nil, s.reject("HANDOFF_BLOCKED", "阶段包仍含 blocker")
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
	bundle := &nativeHandoff{Handoff: h, Strategy: strategy, Stage: stage, Config: config, Rules: rules, Scenarios: scenarios}
	if e = contractHandoffApprovals(s, ref, bundle); e != nil {
		return nil, e
	}
	return bundle, nil
}

func contractHandoffUIScope(s *semanticSession, h, stage, config map[string]any) error {
	if contractHandoffUIKind(h) == "existing-ui-baseline" && semMap(stage["impact_assessment"])["ui"] != false {
		return s.reject("HANDOFF_UI", "既有 UI 不能消费 UI 影响")
	}
	if contractHandoffUIKind(h) == "not-applicable" {
		impact := semMap(stage["impact_assessment"])
		if impact["ui"] != false || impact["frontend"] != false {
			return s.reject("HANDOFF_UI", "无产品设计基线必须有已批准的无 UI、前端影响依据")
		}
		for _, route := range semList(h["consumer_routes"]) {
			if row := semMap(route); row["capability"] == "frontend-engineering-design" && row["activation"] != "not-applicable" {
				return s.reject("HANDOFF_UI", "无产品设计基线不能启用前端消费者")
			}
		}
		for _, key := range []string{"prototype_ref", "visual_baseline_ref", "existing_ui_baseline_ref"} {
			if _, present := semMap(h["source"])[key]; present {
				return s.reject("HANDOFF_UI", "无产品设计基线不能带有 UI 来源")
			}
			if _, present := semMap(config["approvals"])[key]; present {
				return s.reject("HANDOFF_UI", "无产品设计基线不能带有 UI 批准")
			}
		}
		if _, present := config["prototype"]; present {
			return s.reject("HANDOFF_UI", "无产品设计基线不能带有原型配置")
		}
	}
	return nil
}
func mustMarshalContract(v any) []byte          { b, _ := jsonMarshalContract(v); return b }
func jsonMarshalContract(v any) ([]byte, error) { str, e := contractJSON(v); return []byte(str), e }
func contractHandoffUIKind(h map[string]any) string {
	if contractN(h["schema_version"]) < 5 {
		return "prototype"
	}
	return text(h["ui_baseline_kind"])
}
func contractHandoffUIRef(h map[string]any) map[string]any {
	key := "visual_baseline_ref"
	if contractHandoffUIKind(h) == "existing-ui-baseline" {
		key = "existing_ui_baseline_ref"
	}
	return semMap(semMap(h["source"])[key])
}
func contractHandoffRoutes(s *semanticSession, h, stage map[string]any) error {
	if contractN(h["schema_version"]) == 3 {
		return nil
	}
	routes := map[string]map[string]any{}
	ids := map[string]bool{}
	for _, v := range semList(h["consumer_routes"]) {
		r := semMap(v)
		capability, id := text(r["capability"]), text(r["route_id"])
		if routes[capability] != nil || ids[id] {
			return s.reject("HANDOFF_ROUTE", "能力或 route_id 重复")
		}
		routes[capability] = r
		ids[id] = true
	}
	caps := []string{"backend-technical-design", "frontend-engineering-design", "delivery-coordination"}
	for _, capability := range caps {
		if routes[capability] == nil {
			return s.reject("HANDOFF_ROUTE", "消费者路线未完整声明")
		}
	}
	mapping := map[string][]map[string]any{}
	for _, v := range semList(stage["downstream_mapping"]) {
		m := semMap(v)
		capability := text(m["consumer_capability"])
		if !semHas(caps, capability) {
			return s.reject("HANDOFF_ROUTE", "未知消费者能力")
		}
		mapping[capability] = append(mapping[capability], m)
	}
	impact := semMap(stage["impact_assessment"])
	for capability, required := range map[string]bool{"backend-technical-design": impact["backend"] == true || impact["api"] == true || impact["data"] == true, "frontend-engineering-design": impact["frontend"] == true || impact["ui"] == true} {
		expected := "not-applicable"
		for _, m := range mapping[capability] {
			if text(m["propagation"]) != "not-applicable" {
				expected = "optional"
			}
		}
		if required {
			expected = "required"
		}
		if text(routes[capability]["activation"]) != expected {
			return s.reject("HANDOFF_ROUTE", "路由与批准影响矩阵冲突")
		}
	}
	backend, frontend, coord := routes[caps[0]], routes[caps[1]], routes[caps[2]]
	deps := []string{}
	if text(backend["activation"]) != "not-applicable" {
		deps = append(deps, text(backend["route_id"]))
	}
	if !contractSame(frontend["dependencies"], deps) {
		return s.reject("HANDOFF_ROUTE", "前端路由依赖冲突")
	}
	if text(frontend["activation"]) != "not-applicable" {
		deps = append(deps, text(frontend["route_id"]))
	}
	expected := "not-applicable"
	if len(deps) > 0 {
		expected = "required"
	}
	if text(coord["activation"]) != expected || !contractSame(coord["dependencies"], deps) {
		return s.reject("HANDOFF_ROUTE", "协调路由依赖冲突")
	}
	for capability, rows := range mapping {
		for _, m := range rows {
			if (text(m["propagation"]) == "not-applicable") != (text(routes[capability]["activation"]) == "not-applicable") {
				return s.reject("HANDOFF_ROUTE", "downstream mapping 与路线冲突")
			}
		}
	}
	for _, r := range routes {
		for _, dep := range semStrings(r["dependencies"]) {
			if !ids[dep] {
				return s.reject("HANDOFF_ROUTE", "路由依赖悬空")
			}
		}
	}
	return nil
}
func contractHandoffIndexes(s *semanticSession, strategy map[string]any) ([]any, []any, error) {
	if contractN(strategy["traceability_version"]) != 1 {
		return nil, nil, s.reject("HANDOFF_TRACEABILITY", "缺少稳定 traceability v1")
	}
	contexts := map[string]bool{}
	for _, v := range semList(strategy["contexts"]) {
		contexts[text(semMap(v)["context_id"])] = true
	}
	rules, scenarios, used := map[string]map[string]any{}, map[string]map[string]any{}, map[string]bool{}
	for _, v := range semList(strategy["rule_catalog"]) {
		m := semMap(v)
		id := text(m["rule_id"])
		if !regexp.MustCompile(`^rule\.[a-z0-9][a-z0-9-]*$`).MatchString(id) || rules[id] != nil || text(m["statement"]) == "" || text(m["status"]) != "confirmed" || !contexts[text(m["responsible_context"])] {
			return nil, nil, s.reject("HANDOFF_TRACEABILITY", "规则身份、确认或责任区无效")
		}
		row := contractCopy(m)
		row["source_digest"] = contractDigest(m)
		rules[id] = row
	}
	if len(rules) == 0 {
		return nil, nil, s.reject("HANDOFF_TRACEABILITY", "规则目录为空")
	}
	for _, v := range semList(strategy["scenarios"]) {
		m := semMap(v)
		id := text(m["scenario_id"])
		_, boolean := m["critical"].(bool)
		if !regexp.MustCompile(`^scenario\.[a-z0-9][a-z0-9-]*$`).MatchString(id) || scenarios[id] != nil || text(m["status"]) != "confirmed" || !boolean || !contexts[text(m["responsible_context"])] || !contractUnique(m["rule_refs"], true) {
			return nil, nil, s.reject("HANDOFF_TRACEABILITY", "场景身份、确认或规则无效")
		}
		for _, key := range []string{"commands", "success_results", "failure_results"} {
			if !contractUnique(m[key], true) {
				return nil, nil, s.reject("HANDOFF_TRACEABILITY", "场景缺少成功/失败路径")
			}
		}
		statements := []string{}
		for _, ref := range semStrings(m["rule_refs"]) {
			if rules[ref] == nil {
				return nil, nil, s.reject("HANDOFF_TRACEABILITY", "场景引用悬空规则")
			}
			statements = append(statements, text(rules[ref]["statement"]))
			used[ref] = true
		}
		if !contractSame(contractSorted(statements), contractSorted(semStrings(m["rules"]))) {
			return nil, nil, s.reject("HANDOFF_TRACEABILITY", "规则正文与稳定引用冲突")
		}
		row := contractCopy(m)
		row["source_digest"] = contractDigest(m)
		scenarios[id] = row
	}
	if len(scenarios) == 0 {
		return nil, nil, s.reject("HANDOFF_TRACEABILITY", "场景目录为空")
	}
	for _, v := range semList(strategy["invariants"]) {
		m := semMap(v)
		ref := text(m["rule_ref"])
		if rules[ref] == nil || rules[ref]["statement"] != m["statement"] || !contractUnique(m["scenario_refs"], true) {
			return nil, nil, s.reject("HANDOFF_TRACEABILITY", "不变量与规则冲突")
		}
		for _, id := range semStrings(m["scenario_refs"]) {
			if scenarios[id] == nil || !semHas(scenarios[id]["rule_refs"], ref) {
				return nil, nil, s.reject("HANDOFF_TRACEABILITY", "不变量场景未覆盖规则")
			}
		}
		used[ref] = true
	}
	for id := range rules {
		if !used[id] {
			return nil, nil, s.reject("HANDOFF_TRACEABILITY", "存在未使用规则")
		}
	}
	outRules, outScenarios := []any{}, []any{}
	for _, id := range mapKeysAsAny(rules) {
		outRules = append(outRules, rules[id])
	}
	for _, id := range mapKeysAsAny(scenarios) {
		outScenarios = append(outScenarios, scenarios[id])
	}
	return outRules, outScenarios, nil
}
func contractHandoffApprovals(s *semanticSession, ref string, b *nativeHandoff) error {
	if e := contractHandoffSourcePolicy(s, contractN(b.Handoff["schema_version"])); e != nil {
		return e
	}
	roles := contractCopy(s.roles)
	decisionPolicy := semMap(roles["user_decision_policy"])
	for _, id := range semStrings(decisionPolicy["required_capabilities"]) {
		if !semHas([]string{"strategic-decision-reuse-v1", "business-ticket-approval-v1"}, id) {
			return s.unavailable("HANDOFF_POLICY_CAPABILITY", "不支持的来源决定策略能力: "+id)
		}
	}
	policy := contractCopy(semMap(roles["gate_policy"]))
	review := semList(policy["digital_human_review"])
	for _, v := range semList(policy["check_reviews"]) {
		m := semMap(v)
		exists := false
		for _, r := range review {
			exists = exists || semMap(r)["gate"] == m["gate"]
		}
		for _, r := range semList(policy["dual_digital_human"]) {
			exists = exists || semMap(r)["gate"] == m["gate"]
		}
		if !exists {
			review = append(review, v)
		}
	}
	policy["digital_human_review"] = review
	veto := semStrings(policy["product_digital_human_with_biological_veto"])
	for _, gate := range veto {
		if semHas(policy["biological_human"], gate) || apFind(policy["digital_human_review"], "gate", gate) != nil || apFind(policy["dual_digital_human"], "gate", gate) != nil {
			continue
		}
		policy["digital_human_review"] = append(apArray(policy["digital_human_review"]), map[string]any{"gate": gate, "countersigners": []string{"role.product-manager"}})
	}
	roles["gate_policy"] = policy
	s.roles = roles
	currentPlan := apFind(policy["dual_digital_human"], "gate", "gate.plan-approved") != nil
	explicit := func(g string) bool {
		return apFind(policy["digital_human_review"], "gate", g) != nil || apFind(policy["dual_digital_human"], "gate", g) != nil || semHas(policy["biological_human"], g)
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
		if len(semMap(semMap(semMap(semMap(b.Handoff["source"])[key])["approval_context"])["source_baseline"])) > 0 {
			continue
		}
		approval := semMap(row.doc["approval"])
		if len(approval) > 0 {
			if !contractSame(approval["current_version"], row.version) {
				return s.reject("HANDOFF_APPROVAL", "来源内置批准版本过期")
			}
			if e := contractSourceApproval(s, row.gate, map[string]any{"ref": approval["persisted_ref"], "approval_ref": approval["approval_ref"], "approval_context": approval["approval_context"]}, text(approval["approval_ref"])); e != nil {
				return e
			}
		}
	}
	sources := semMap(b.Handoff["source"])
	setSource, e := s.doc(text(semMap(sources["business_ticket_set_ref"])["persisted_ref"]))
	if e != nil {
		return e
	}
	capabilities := semMap(roles["user_decision_policy"])["required_capabilities"]
	if text(setSource["kind"]) == "business-ticket-set" && !semHas(capabilities, "business-ticket-approval-v1") {
		return s.reject("HANDOFF_POLICY", "业务集合缺少来源批准策略能力")
	}
	if semHas(capabilities, "business-ticket-approval-v1") {
		gates["business_ticket_set_ref"] = terminal
		business, e := contractBusinessTickets(s, text(semMap(sources["business_ticket_set_ref"])["persisted_ref"]))
		if e != nil {
			return e
		}
		set := semMap(sources["business_ticket_set_ref"])
		spec := semMap(sources["spec_ref"])
		if business["id"] != set["id"] || business["version"] != set["version"] || semMap(business["spec"])["ref"] != spec["persisted_ref"] || semMap(business["spec"])["version"] != spec["version"] {
			return s.reject("HANDOFF_BUSINESS", "业务票集合身份或 Spec 版本冲突")
		}
		b.Business = business
	}
	bindings := contractCopy(sources)
	bindings["handoff"] = map[string]any{"id": b.Handoff["handoff_id"], "version": b.Handoff["handoff_version"], "persisted_ref": ref, "approval_context": semMap(semMap(b.Config["approvals"])["handoff"])["approval_context"]}
	proofs := []string{}
	for key, v := range bindings {
		source := semMap(v)
		approval := semMap(semMap(b.Config["approvals"])[key])
		if text(approval["gate_id"]) != gates[key] {
			return s.reject("HANDOFF_APPROVAL", "来源批准门禁冲突: "+key)
		}
		assetRef := text(source["persisted_ref"])
		actual := ""
		if key == "visual_baseline_ref" || key == "existing_ui_baseline_ref" {
			assetRef = path.Join(assetRef, text(source["manifest_ref"]))
			baseline, e := contractHandoffBaseline(s, b.Handoff)
			if e != nil {
				return e
			}
			actual = text(semMap(baseline["bundle"])["digest"])
			if key == "existing_ui_baseline_ref" {
				if text(approval["digest_kind"]) != "sha256-bytes" || !semHas(semMap(roles["user_decision_policy"])["gates"], gates[key]) {
					return s.reject("HANDOFF_POLICY", "既有 UI 未绑定真实产品设计决定策略")
				}
				bound, e := s.bind(assetRef)
				if e != nil {
					return e
				}
				actual = bound.Digest
			}
		} else {
			bytes, e := s.bytes(assetRef)
			if e != nil {
				return e
			}
			switch text(approval["digest_kind"]) {
			case "canonical-json":
				doc, e := s.doc(assetRef)
				if e != nil {
					return e
				}
				actual = contractDigest(doc)
			case "sha256-bytes":
				actual = "sha256:" + safefs.Digest(bytes)
			default:
				return s.reject("HANDOFF_APPROVAL", "未知来源摘要算法")
			}
		}
		if key != "handoff" && text(source["digest"]) != actual {
			return s.reject("HANDOFF_SOURCE_STALE", "源资产摘要过期: "+key)
		}
		bound := map[string]any{"ref": assetRef, "approval_context": source["approval_context"]}
		bound["approval_ref"] = approval["record_ref"]
		if e = contractSourceApproval(s, gates[key], bound, text(approval["record_ref"])); e != nil {
			return e
		}
		record, e := s.doc(text(approval["record_ref"]))
		if e != nil {
			return e
		}
		record, e = selectApprovalRecordSemantic(s, record, gates[key])
		if e != nil {
			return e
		}
		match := false
		for _, v := range semList(record["artifact_bindings"]) {
			a := semMap(v)
			match = match || a["id"] == first(text(source["id"]), text(source["baseline_id"])) && contractSame(a["version"], source["version"]) && text(a["digest"]) == actual
		}
		if !match {
			return s.reject("HANDOFF_APPROVAL", "来源批准未绑定身份、版本和摘要")
		}
		proofs = contractUnion(proofs, []string{text(approval["record_ref"]), text(record["user_decision_ref"]), text(record["continuation_ref"]), text(record["decision_reuse_ref"])})
	}
	if b.Business != nil {
		for _, v := range semList(b.Business["coverage_deferred"]) {
			if !semHas(proofs, text(semMap(semMap(v)["decision"])["ref"])) {
				return s.reject("BUSINESS_DEFERRED_APPROVAL_UNBOUND", "延期决定不在来源批准链")
			}
		}
	}
	if semHas(capabilities, "strategic-decision-reuse-v1") {
		approval := semMap(semMap(b.Config["approvals"])["handoff"])
		record, e := s.doc(text(approval["record_ref"]))
		if e != nil {
			return e
		}
		record, e = selectApprovalRecordSemantic(s, record, gates["handoff"])
		if e != nil {
			return e
		}
		scope, e := s.doc(text(record["subject_ref"]))
		if e != nil {
			return e
		}
		if contractN(scope["schema_version"]) != 1 || text(scope["kind"]) != "strategic-delivery-scope" {
			return s.reject("HANDOFF_SCOPE", "战略交付缺少独立批准范围清单")
		}
		delivery, e := s.doc(text(scope["delivery_ref"]))
		if e != nil {
			return e
		}
		if !contractSame(delivery, contractWithout(b.Handoff, "package_export", "status")) {
			return s.reject("HANDOFF_SCOPE", "交付内容/风险未被批准范围覆盖")
		}
		if e = s.basis(scope["assets"]); e != nil {
			return e
		}
		for key, v := range sources {
			source := semMap(v)
			assetRef := text(source["persisted_ref"])
			if key == "visual_baseline_ref" || key == "existing_ui_baseline_ref" {
				assetRef = path.Join(assetRef, text(source["manifest_ref"]))
			}
			found := false
			for _, v := range semList(scope["assets"]) {
				a := semMap(v)
				found = found || text(a["ref"]) == assetRef && a["version"] == source["version"] && text(a["boundary"]) == gates[key]
			}
			if !found {
				return s.reject("HANDOFF_SCOPE", "交付范围遗漏当前资产")
			}
		}
	}
	if contractHandoffUIKind(b.Handoff) == "prototype" {
		if e = contractPreview(s, semMap(b.Config["prototype"]), semStrings(contractHandoffUIRef(b.Handoff)["case_ids"])); e != nil {
			return e
		}
	}
	return nil
}

func contractHandoffSourcePolicy(s *semanticSession, protocol int) error {
	version, valid := integer(s.roles["schema_version"])
	gates, gatesPresent := object(s.roles["gate_policy"])
	policy, present := object(s.roles["user_decision_policy"])
	_, gateList := policy["gates"].([]any)
	_, workUnits := object(policy["work_units"])
	if !valid || version != 1 || s.roles["status"] != "active" || !gatesPresent || !present || !gateList || !workUnits || !contractUnique(policy["gates"], false) || !contractUnique(policy["source_kinds"], true) {
		return s.unavailable("HANDOFF_POLICY_CAPABILITY", "来源批准或真实决定规则缺失、未知或不完整")
	}
	for _, kind := range semStrings(policy["source_kinds"]) {
		if !semHas([]string{"platform-message", "session-export", "user-confirmation-file"}, kind) {
			return s.unavailable("HANDOFF_POLICY_CAPABILITY", "未知来源决定输入协议: "+kind)
		}
	}
	if raw, declared := policy["schema_version"]; declared {
		v, valid := integer(raw)
		if !valid || v != 1 {
			return s.unavailable("HANDOFF_POLICY_CAPABILITY", "未知来源决定策略协议")
		}
	} else {
		if protocol != 3 && protocol != 4 || gates["default_if_unlisted"] != "biological-human" || !semSameSet(policy["source_kinds"], []string{"platform-message", "session-export", "user-confirmation-file"}) {
			return s.unavailable("HANDOFF_POLICY_CAPABILITY", "未登记的 versionless 来源策略形状")
		}
		// The published Handoff v3/v4/v5 v1 signing policy predates this
		// field. Its complete original gates/work_units contract is required;
		// a missing policy can never be projected to an empty one.
		s.report.Checks = append(s.report.Checks, SemanticCheck{ID: "handoff-source-policy-versionless-v1", SourceRef: ".template-spec/agents/digital-human-roles.yaml", Status: "passed"})
	}
	for _, bucket := range []string{"biological_human", "product_digital_human_with_biological_veto", "dual_digital_human", "digital_human_review", "check_reviews"} {
		if value, present := gates[bucket]; present {
			if _, valid := value.([]any); !valid {
				return s.unavailable("HANDOFF_POLICY_CAPABILITY", "来源签署分类必须为数组: "+bucket)
			}
		}
	}
	return nil
}
func contractSourceApproval(s *semanticSession, gate string, binding map[string]any, approvalRef string) error {
	if inherited, err := inheritedSourceApproval(s, gate, binding, approvalRef); inherited {
		return err
	}
	record, e := s.doc(approvalRef)
	if e != nil {
		return e
	}
	record, e = selectApprovalRecordSemantic(s, record, gate)
	if e != nil {
		return e
	}
	// The published source protocol selects an internal check from a bundle,
	// while a standalone historical record retains its declared signing gate.
	if semHas([]string{"check.domain-strategy-approved", "check.stage-decision-package-approved"}, gate) && text(record["gate_id"]) != gate {
		gate = text(record["gate_id"])
	}
	savedRoles, savedRegistry := s.roles, s.registry
	defer func() { s.roles, s.registry = savedRoles, savedRegistry }()
	if contractN(record["schema_version"]) == 2 {
		s.roles, e = s.doc(".template-spec/agents/digital-human-roles.yaml")
		if e != nil {
			return e
		}
		s.registry, e = s.doc(".template-spec/process/lifecycle-registry.yaml")
		if e != nil {
			return e
		}
		if _, e = s.doc(".template-spec/agents/yss-skill-registry.yaml"); e != nil {
			return e
		}
		task, e := s.doc(text(record["review_task_ref"]))
		if e != nil {
			return e
		}
		for _, ref := range []string{".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/yss-skill-registry.yaml"} {
			b, e := s.bind(ref)
			if e != nil {
				return e
			}
			found := false
			for _, v := range semList(semMap(task["review_context"])["basis"]) {
				a := semMap(v)
				found = found || text(a["ref"]) == ref && strings.TrimPrefix(text(a["digest"]), "sha256:") == strings.TrimPrefix(b.Digest, "sha256:")
			}
			if !found {
				return s.reject("HANDOFF_POLICY", "来源正式任务未冻结实际注册表")
			}
		}
	}
	if gate == "gate.plan-approved" {
		bounded := record["plan_review_binding"] != nil
		for _, v := range semList(semMap(semMap(s.roles["gate_policy"])["review_execution"])["review_bundles"]) {
			row := semMap(v)
			bounded = bounded || text(row["aggregate_gate"]) == gate && text(row["aggregate_additional_review_task"]) == "forbidden"
		}
		if bounded {
			s.registry, e = s.doc(".template-spec/process/lifecycle-registry.yaml")
			if e != nil {
				return e
			}
			context := semMap(binding["approval_context"])
			assetRef := first(text(context["subject_ref"]), text(binding["ref"]))
			if e = s.verify("source-plan-approval", approvalRef, map[string]string{"asset-ref": assetRef, "task-ref": text(record["review_task_ref"])}); e != nil {
				return e
			}
		}
	}
	if e = contractApproval(s, gate, binding, approvalRef, map[string]string{}); e != nil {
		return fmt.Errorf("来源批准 %s (%s): %w", gate, approvalRef, e)
	}
	return nil
}
func contractHandoffClosure(s *semanticSession, ref string, b *nativeHandoff) ([]string, error) {
	queue := []string{ref, "CONTEXT.md", ".template-spec/agents/digital-human-roles.yaml"}
	savedRegistry := s.registry
	defer func() { s.registry = savedRegistry }()
	sealedPackages := map[string]bool{}
	enqueueBaseline := func(receiptRef string) error {
		if _, _, err := verifySpecBaselineReceipt(s, receiptRef, false); err != nil {
			return err
		}
		receipt, err := s.doc(receiptRef)
		if err != nil {
			return err
		}
		working, err := s.doc(text(receipt["working_set_ref"]))
		if err != nil {
			return err
		}
		packageRef := text(receipt["package_ref"])
		sealedPackages[packageRef] = true
		queue = append(queue, receiptRef, packageRef, text(receipt["working_set_ref"]))
		for _, asset := range semMap(working["assets"]) {
			queue = append(queue, text(asset))
		}
		return nil
	}
	if exists, err := s.exists(".yss.json"); err != nil {
		return nil, err
	} else if exists {
		queue = append(queue, ".yss.json")
	}
	for key, v := range semMap(b.Handoff["source"]) {
		queue = append(queue, text(semMap(v)["persisted_ref"]))
		if marker := semMap(semMap(semMap(v)["approval_context"])["source_baseline"]); len(marker) > 0 {
			gate := "gate.plan-approved"
			if key == "spec_ref" {
				gate = "gate.spec-baseline-approved"
			}
			binding := semMap(v)
			if _, err := inheritedSourceApproval(s, gate, map[string]any{"ref": binding["persisted_ref"], "approval_context": binding["approval_context"]}, text(semMap(semMap(b.Config["approvals"])[key])["record_ref"])); err != nil {
				return nil, err
			}
			if err := enqueueBaseline(text(marker["receipt_ref"])); err != nil {
				return nil, err
			}
			queue = append(queue, "yss-project.yaml", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", text(marker["receipt_ref"]), text(marker["context_reconciliation_ref"]))
		}
	}
	for _, v := range semMap(b.Config["approvals"]) {
		queue = append(queue, text(semMap(v)["record_ref"]))
	}
	queue = append(queue, semStrings(b.Handoff["evidence_and_version_digests"])...)
	queue = append(queue, semStrings(b.Config["additional_files"])...)
	prototype := semMap(b.Config["prototype"])
	if len(prototype) > 0 {
		queue = append(queue, text(prototype["preview_root"]), text(prototype["verification_ref"]))
		if text(prototype["profile"]) == "H2" {
			queue = append(queue, text(prototype["source_root"]), text(prototype["lock_ref"]))
		}
	}
	symbolic := semMap(b.Config["reference_map"])
	boundedPlan := false
	for _, value := range semList(semMap(semMap(s.roles["gate_policy"])["review_execution"])["review_bundles"]) {
		row := semMap(value)
		boundedPlan = boundedPlan || row["aggregate_gate"] == "gate.plan-approved" && row["aggregate_additional_review_task"] == "forbidden"
	}
	// Keep SpecBaseline's published closure when it reuses this walker internally.
	protocol := contractN(b.Handoff["schema_version"])
	boundedPlan = boundedPlan && (protocol == 3 || protocol == 4 || protocol == 5)
	seen := map[string]bool{}
	total := 0
	enqueue := func(ref string) {
		if mapped := text(symbolic[ref]); mapped != "" {
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
					enqueue(text(v))
				} else {
					refs(v, key)
				}
			}
		case map[string]any:
			if len(semMap(x["upstream_spec_baseline"])) > 0 {
				s.registry, dependencyErr = s.doc(".template-spec/process/lifecycle-registry.yaml")
				if dependencyErr != nil {
					return
				}
				if dependencyErr = verifyInheritedSpecCheckpoint(s, documentRef, x); dependencyErr != nil {
					return
				}
				if dependencyErr = enqueueBaseline(text(semMap(x["upstream_spec_baseline"])["receipt_ref"])); dependencyErr != nil {
					return
				}
				queue = append(queue, "yss-project.yaml", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml")
			}
			if marker := semMap(x["source_baseline"]); len(marker) > 0 {
				enqueue(text(marker["receipt_ref"]))
				enqueue(text(marker["context_reconciliation_ref"]))
				return
			}
			if x["kind"] == "spec-baseline-import" {
				enqueue(text(x["package_ref"]))
				enqueue(text(x["working_set_ref"]))
			}
			if x["plan_review_control"] != nil {
				enqueue("yss-project.yaml")
				enqueue(".template-spec/process/schemas/plan-review-control.schema.json")
				control := semMap(x["plan_review_control"])
				enqueue(text(semMap(control["provenance"])["source_ref"]))
				for _, v := range semList(control["attempts"]) {
					for _, key := range []string{"checkpoint_ref", "task_ref", "candidate_ref", "result_ref"} {
						enqueue(text(semMap(v)[key]))
					}
				}
			}
			if semMap(x["review_context"])["plan_review_binding"] != nil {
				enqueue(text(x["checkpoint_ref"]))
				for _, ref := range semStrings(x["inputs"]) {
					enqueue(ref)
				}
			}
			if contractN(x["schema_version"]) == 2 && text(x["review_task_ref"]) != "" {
				enqueue(".template-spec/process/lifecycle-registry.yaml")
				enqueue(".template-spec/agents/yss-skill-registry.yaml")
			}
			for k, v := range x {
				str, ok := v.(string)
				if k == "policy_ref" && ok {
					enqueue(strings.Split(str, "#")[0])
				} else if ok && semHas([]string{"review_task_ref", "candidate_ref", "registry_ref", "approval_ref", "persisted_ref", "receipt_ref", "package_ref", "working_set_ref", "context_reconciliation_ref", "user_decision_ref", "decision_reuse_ref", "continuation_ref", "plan_continuation_ref", "scope_ref", "subject_ref", "delivery_ref", "review_ref", "ref"}, k) && !regexp.MustCompile(`^[a-z]+://`).MatchString(str) {
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
		if err := rejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
		if mapped := text(symbolic[ref]); mapped != "" {
			ref = mapped
		}
		if err := rejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
		if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
			continue
		}
		if strings.HasPrefix(ref, "evidence.") {
			return nil, s.reject("HANDOFF_CLOSURE", "未解析 evidence ID")
		}
		if !strings.Contains(ref, "/") && !regexp.MustCompile(`\.(md|yaml|json|html|png|txt|log)$`).MatchString(ref) {
			if ref == "" {
				continue
			}
			exists, e := s.exists(ref)
			if e != nil {
				return nil, e
			}
			if !exists {
				continue
			}
		}
		if !contractPath(ref) {
			return nil, s.reject("HANDOFF_PATH", "闭包引用非法")
		}
		if seen[ref] {
			continue
		}
		exists, e := s.exists(ref)
		if e != nil {
			return nil, e
		}
		if !exists {
			return nil, s.reject("HANDOFF_CLOSURE", "引用不在来源快照: "+ref)
		}
		mapped := s.localRef(ref)
		if _, directory := s.scans[mapped]; directory {
			files, e := s.scan(ref)
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
		bytes, e := s.bytes(ref)
		if e != nil {
			return nil, e
		}
		seen[ref] = true
		total += len(bytes)
		if len(seen) > 20000 || total > 512<<20 {
			return nil, s.reject("HANDOFF_LIMIT", "闭包数量/大小超限")
		}
		sealedSource := false
		for prefix := range sealedPackages {
			sealedSource = sealedSource || strings.HasPrefix(ref, prefix+"/")
		}
		localBaseline := sealedSource || contractHandoffUIKind(b.Handoff) == "existing-ui-baseline" && strings.HasPrefix(ref, text(contractHandoffUIRef(b.Handoff)["persisted_ref"])+"/")
		if !localBaseline && (strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml") || strings.HasSuffix(ref, ".json")) {
			doc, e := schema.Parse(bytes)
			if e != nil {
				return nil, e
			}
			value := semMap(doc)
			if boundedPlan && value["gate_id"] == "gate.plan-approved" && text(value["decision"]) != "" && text(value["actor_kind"]) != "" {
				opts := map[string]string{}
				if value["plan_review_binding"] != nil && text(value["review_task_ref"]) != "" {
					opts["task-ref"] = text(value["review_task_ref"])
				}
				owner, e := sourcePlanApprovalCheckpoint(s, ref, opts)
				if e != nil {
					return nil, e
				}
				enqueue(owner)
				if opts["task-ref"] == "" {
					enqueue(trackerRef)
				}
				if exists, e := s.exists(".template-spec/process/harness-profile.yaml"); e != nil {
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
					return nil, s.reject("HANDOFF_PATH", e.Error())
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
	if !contractPath(ref) {
		return "", fmt.Errorf("资源路径越界")
	}
	return ref, nil
}

// Imported routing belongs to the actual receiving Profile, not a mutable
// self-described target identity in the receipt.
func contractReceiptConsumer(s *semanticSession, receipt map[string]any, capability string) error {
	if contractN(receipt["schema_version"]) < 2 {
		return nil
	}
	profile, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	if receipt["target_profile_id"] != profile["profile_id"] || !semHas(receipt["selected_consumer_capabilities"], capability) {
		return s.reject("HANDOFF_CONSUMER", "导入收据未绑定当前 Profile 的消费者能力")
	}
	if declared, present := semMap(profile["handoff"])["consumer_capabilities"]; present && !semHas(declared, capability) {
		return s.unavailable("CAPABILITY", "当前 Profile 未登记所需交接消费能力")
	}
	return nil
}
func contractHandoffConsumption(s *semanticSession, data map[string]any, opts map[string]string) error {
	consumer, slice := first(opts["consumer"], "tactical"), opts["slice"]
	if !semHas([]string{"tactical", "frontend"}, consumer) {
		return s.reject("HANDOFF_CONSUMER", "未知消费者")
	}
	if consumer == "tactical" && contractN(data["schema_version"]) == 2 && !semHas([]string{"domain-driven", "layered-mvc"}, text(semMap(data["architecture"])["family"])) {
		return s.reject("HANDOFF_CONSUMER", "未知技术设计架构")
	}
	identity, e := s.doc("yss-project.yaml")
	if e != nil {
		return e
	}
	if text(identity["repository_mode"]) != "project-instance" {
		return s.reject("HANDOFF_CONSUMER", "接收端必须 project-instance")
	}
	if slice != "" && text(data["status"]) != map[string]string{"tactical": "approved", "frontend": "accepted"}[consumer] {
		return s.reject("HANDOFF_CONSUMER", "当前切片不能消费未批准接收合同")
	}
	binding := semMap(data["strategic_handoff"])
	if e = contractRequired(s, binding, "import_receipt_ref", "context_reconciliation_ref", "bundle_digest"); e != nil {
		return e
	}
	receipt, e := s.doc(text(binding["import_receipt_ref"]))
	if e != nil {
		return e
	}
	base := path.Join("docs/handoffs", text(receipt["bundle_id"]), text(receipt["version"]))
	if text(binding["import_receipt_ref"]) != base+"/import-receipt.json" || text(receipt["package_ref"]) != base+"/package" || binding["bundle_digest"] != receipt["bundle_digest"] {
		return s.reject("HANDOFF_RECEIPT", "收据路径或包身份冲突")
	}
	if e = contractReceipt(s, receipt); e != nil {
		return e
	}
	capability := map[string]string{"tactical": "backend-technical-design", "frontend": "frontend-engineering-design"}[consumer]
	if e = contractReceiptConsumer(s, receipt, capability); e != nil {
		return e
	}
	if contractN(receipt["schema_version"]) >= 2 {
		route := apFind(receipt["routes"], "capability", capability)
		if route == nil || text(route["activation"]) == "not-applicable" || route["route_id"] != binding["route_id"] {
			return s.reject("HANDOFF_ROUTE", "接收未绑定启用路线")
		}
	}
	if e = s.verify("context-reconciliation", text(binding["context_reconciliation_ref"]), map[string]string{"import-receipt": text(binding["import_receipt_ref"])}); e != nil {
		return e
	}
	parent := path.Join("docs/handoffs", text(receipt["bundle_id"]))
	names, e := s.list(parent)
	if e != nil {
		return e
	}
	latest := ""
	max := 0
	for _, name := range names {
		if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(name) {
			continue
		}
		exists, e := s.exists(path.Join(parent, name, "import-receipt.json"))
		if e != nil {
			return e
		}
		if exists {
			n, e := strconv.Atoi(name[1:])
			if e != nil {
				return s.reject("HANDOFF_VERSION", "版本数值超限")
			}
			if n > max {
				max, latest = n, name
			}
		}
	}
	if latest == "" {
		return s.reject("HANDOFF_RECEIPT", "缺少最新收据")
	}
	currentReceipt, e := s.doc(path.Join(parent, latest, "import-receipt.json"))
	if e != nil {
		return e
	}
	if text(currentReceipt["version"]) != latest || text(currentReceipt["package_ref"]) != path.Join(parent, latest, "package") {
		return s.reject("HANDOFF_RECEIPT", "最新收据路径无效")
	}
	if e = contractReceipt(s, currentReceipt); e != nil {
		return e
	}
	if e = contractReceiptConsumer(s, currentReceipt, capability); e != nil {
		return e
	}
	original, e := contractOpenHandoff(s, text(receipt["package_ref"]))
	if e != nil {
		return e
	}
	current := original
	if latest != text(receipt["version"]) {
		current, e = contractOpenHandoff(s, text(currentReceipt["package_ref"]))
		if e != nil {
			return e
		}
	}
	if original.Manifest["bundle_digest"] != receipt["bundle_digest"] || current.Manifest["bundle_id"] != receipt["bundle_id"] || current.Manifest["bundle_digest"] != currentReceipt["bundle_digest"] {
		return s.reject("HANDOFF_RECEIPT", "收据与当前包冲突")
	}
	context, e := s.contextContract()
	if e != nil {
		return e
	}
	terms := map[string]Term{}
	for _, t := range context["business_terms"].([]Term) {
		terms[t.TermRef] = t
	}
	for _, op := range []string{"added", "updated", "deprecated"} {
		for _, v := range semList(semMap(current.Handoff["context_delta"])[op]) {
			expected := semMap(v)
			term, ok := terms[text(expected["term_ref"])]
			if op == "deprecated" {
				if ok {
					return s.reject("HANDOFF_CONTEXT", "废弃术语仍存在")
				}
				continue
			}
			if !ok {
				return s.reject("HANDOFF_CONTEXT", "目标术语未对账")
			}
			doc := semMap(mustParseContract(mustMarshalContract(term)))
			for _, key := range []string{"term", "meaning", "english_identifier", "context_id", "forbidden_aliases"} {
				if !contractSame(expected[key], doc[key]) {
					return s.reject("HANDOFF_CONTEXT", "目标术语字段未对账")
				}
			}
		}
	}
	known := map[string]map[string]any{}
	for _, v := range current.Rules {
		m := semMap(v)
		known[text(m["rule_id"])] = m
	}
	for _, v := range current.Scenarios {
		m := semMap(v)
		if m["critical"] == true {
			known[text(m["scenario_id"])] = m
		}
	}
	if current.Business != nil {
		for _, v := range semList(current.Business["tickets"]) {
			m := semMap(v)
			row := contractCopy(m)
			if row["source_digest"] == nil {
				row["source_digest"] = m["digest"]
			}
			known[text(m["id"])] = row
		}
	}
	rows := map[string]map[string]any{}
	for _, v := range semList(binding["rows"]) {
		row := semMap(v)
		id := text(row["source_id"])
		if id == "" || rows[id] != nil {
			return s.reject("HANDOFF_MAPPING", "接收来源重复")
		}
		rows[id] = row
	}
	blocked := false
	mark := func(row map[string]any) {
		blocked = blocked || slice == "" || text(row["dependency_status"]) != "known" || !contractUnique(row["dependent_slice_refs"], true) || semHas(row["dependent_slice_refs"], slice)
	}
	ids, seams, e := contractDesignTargets(s, data)
	if e != nil {
		return e
	}
	cases := map[string]map[string]any{}
	for _, v := range semList(data["frontend_cases"]) {
		m := semMap(v)
		id := text(m["case_id"])
		if cases[id] != nil {
			return s.reject("HANDOFF_MAPPING", "前端用例重复")
		}
		cases[id] = m
	}
	for id, source := range known {
		row := rows[id]
		if row == nil || row["source_digest"] != source["source_digest"] {
			mark(row)
			continue
		}
		if !semHas([]string{"known", "unknown"}, text(row["dependency_status"])) || row["dependent_slice_refs"] == nil {
			return s.reject("HANDOFF_MAPPING", "缺少切片依赖判定")
		}
		if consumer == "frontend" && text(row["disposition"]) == "mapped" {
			if !contractUnique(row["frontend_case_refs"], true) {
				return s.reject("HANDOFF_MAPPING", "前端落点缺失")
			}
			for _, ref := range semStrings(row["frontend_case_refs"]) {
				if cases[ref] == nil || !semHas(cases[ref]["source_ids"], id) {
					return s.reject("HANDOFF_MAPPING", "前端用例未承接源规则")
				}
			}
			if strings.HasPrefix(id, "scenario.") {
				for _, outcome := range []string{"success", "failure"} {
					found := false
					for _, ref := range semStrings(row["frontend_case_refs"]) {
						found = found || text(cases[ref]["outcome"]) == outcome
					}
					if !found {
						return s.reject("HANDOFF_MAPPING", "关键场景缺少前端成功/失败用例")
					}
				}
			}
			if e = contractEvidence(s, row["evidence_refs"]); e != nil {
				return e
			}
		} else if text(row["disposition"]) == "implemented" && consumer == "tactical" || text(row["disposition"]) == "not-applicable" {
			if e = contractTraceRow(s, row, source, ids, seams, slice); e != nil {
				return e
			}
		} else {
			if text(row["disposition"]) == "deferred" {
				if e = contractRequired(s, row, "reason", "risk", "owner", "followup_ticket_ref", "verification_plan", "target_version"); e != nil {
					return e
				}
			} else if !semHas([]string{"pending", "conflict"}, text(row["disposition"])) {
				return s.reject("HANDOFF_MAPPING", "非法接收状态")
			}
			mark(row)
		}
	}
	for id, row := range rows {
		if known[id] == nil {
			mark(row)
		}
	}
	if current.Business != nil {
		for _, value := range semList(current.Business["tickets"]) {
			ticket := semMap(value)
			row := rows[text(ticket["id"])]
			for _, value := range semList(ticket["source_refs"]) {
				locator := text(semMap(value)["locator"])
				if known[locator] == nil {
					continue
				}
				mapped := rows[locator]
				incomplete := row == nil || mapped == nil || text(row["dependency_status"]) != "known" || text(mapped["dependency_status"]) != "known"
				for _, ref := range semStrings(row["dependent_slice_refs"]) {
					incomplete = incomplete || !semHas(mapped["dependent_slice_refs"], ref)
				}
				if incomplete {
					mark(row)
				}
			}
		}
	}
	if current.Business != nil && original.Business != nil && !contractSame(current.Business["coverage_deferred"], original.Business["coverage_deferred"]) {
		blocked = true
	}
	if current.Business != nil && original.Business != nil {
		prior := map[string]any{}
		for _, value := range semList(original.Business["tickets"]) {
			ticket := semMap(value)
			prior[text(ticket["id"])] = ticket["source_digest"]
		}
		for _, value := range semList(current.Business["tickets"]) {
			ticket := semMap(value)
			if !contractSame(prior[text(ticket["id"])], ticket["source_digest"]) {
				mark(rows[text(ticket["id"])])
			}
		}
	}
	for _, key := range []string{"spec_ref", "business_ticket_set_ref", "prototype_ref", "visual_baseline_ref", "existing_ui_baseline_ref"} {
		if key == "business_ticket_set_ref" && current.Business != nil && original.Business != nil {
			continue
		}
		if semMap(semMap(current.Handoff["source"])[key])["digest"] != semMap(semMap(original.Handoff["source"])[key])["digest"] {
			blocked = true
		}
	}
	strategyBasis := func(v map[string]any) map[string]any {
		out := contractWithout(v, "domain_version", "approval", "rule_catalog", "scenarios", "invariants")
		invariants := []any{}
		for _, v := range semList(v["invariants"]) {
			invariants = append(invariants, contractWithout(semMap(v), "statement"))
		}
		out["invariants"] = invariants
		return out
	}
	if !contractSame(strategyBasis(current.Strategy), strategyBasis(original.Strategy)) || !contractSame(contractWithout(current.Stage, "package_version", "approval", "domain_strategy_ref"), contractWithout(original.Stage, "package_version", "approval", "domain_strategy_ref")) || contractHandoffUIKind(current.Handoff) != contractHandoffUIKind(original.Handoff) || !contractSame(current.Config["prototype"], original.Config["prototype"]) {
		blocked = true
	}
	if blocked {
		return s.reject("HANDOFF_STALE", "来源变化、缺失/延期承接或未知依赖阻断当前范围")
	}
	return nil
}
func mustParseContract(b []byte) any { v, _ := schema.Parse(b); return v }
func contractReceipt(s *semanticSession, r map[string]any) error {
	switch contractN(r["schema_version"]) {
	case 1:
		if e := contractRequired(s, r, "bundle_id", "version", "bundle_digest", "package_ref", "target_context_digest", "status"); e != nil {
			return e
		}
	case 2:
		if e := s.validateSchema(".template-spec/process/schemas/strategic-handoff-import-receipt.schema.json", r); e != nil {
			return e
		}
	case 3:
		if e := s.validateSchema(".template-spec/process/schemas/strategic-handoff-import-receipt-v3.schema.json", r); e != nil {
			return e
		}
		b, e := s.bind(text(r["source_delivery_record_ref"]))
		if e != nil {
			return e
		}
		if b.Digest != text(r["source_delivery_record_sha256"]) {
			return s.reject("HANDOFF_RECEIPT", "来源交付记录摘要冲突")
		}
	default:
		return s.unavailable("CAPABILITY", "未知导入收据协议")
	}
	bundle, e := contractOpenHandoff(s, text(r["package_ref"]))
	if e != nil {
		return e
	}
	if r["bundle_id"] != bundle.Manifest["bundle_id"] || r["version"] != bundle.Manifest["version"] || r["bundle_digest"] != bundle.Manifest["bundle_digest"] {
		return s.reject("HANDOFF_RECEIPT", "收据与当前包身份不一致")
	}
	if contractN(bundle.Handoff["schema_version"]) == 5 && contractN(r["schema_version"]) != 3 {
		return s.reject("HANDOFF_RECEIPT", "Handoff v5必须使用绑定真实交付记录的receipt v3")
	}
	if contractN(r["schema_version"]) == 3 {
		base := path.Join("docs/handoffs", text(r["bundle_id"]), text(r["version"]))
		if r["source_delivery_record_ref"] != base+"/source-delivery-record.json" || r["package_ref"] != base+"/package" {
			return s.reject("HANDOFF_RECEIPT", "来源交付记录或包路径非法")
		}
		record, e := s.doc(text(r["source_delivery_record_ref"]))
		if e != nil {
			return e
		}
		if e = s.validateSchema(".template-spec/process/schemas/strategic-handoff-delivery.schema.json", record); e != nil {
			return e
		}
		if e = contractDeliveryRecordIdentity(s, bundle, record); e != nil {
			return e
		}
	}
	return nil
}
func contractHandoffBaseline(s *semanticSession, h map[string]any) (map[string]any, error) {
	if contractHandoffUIKind(h) == "not-applicable" {
		return map[string]any{"cases": []any{}}, nil
	}
	binding := contractHandoffUIRef(h)
	ref := path.Join(text(binding["persisted_ref"]), text(binding["manifest_ref"]))
	var baseline map[string]any
	var e error
	if contractHandoffUIKind(h) == "existing-ui-baseline" {
		baseline, e = contractExistingUI(s, ref)
	} else {
		baseline, e = contractVisualBaseline(s, ref)
	}
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, v := range semList(baseline["cases"]) {
		ids = append(ids, text(semMap(v)["case_id"]))
	}
	if baseline["baseline_id"] != binding["baseline_id"] || baseline["version"] != binding["version"] || !semSameSet(ids, binding["case_ids"]) {
		return nil, s.reject("HANDOFF_UI", "基线身份/版本/用例冲突")
	}
	return baseline, nil
}
func contractPreview(s *semanticSession, config map[string]any, cases []string) error {
	if !semHas([]string{"H1", "H2"}, text(config["profile"])) || !contractWithin(text(config["entry_ref"]), text(config["preview_root"])) || text(config["entry_ref"]) == text(config["preview_root"]) {
		return s.reject("HANDOFF_PREVIEW", "预览类型或入口范围非法")
	}
	digest, files, e := contractTree(s, text(config["preview_root"]))
	if e != nil {
		return e
	}
	if len(files) == 0 {
		return s.reject("HANDOFF_PREVIEW", "预览目录为空")
	}
	for _, ref := range files {
		if !strings.HasSuffix(strings.ToLower(ref), ".html") && !strings.HasSuffix(strings.ToLower(ref), ".css") {
			continue
		}
		bytes, e := s.bytes(ref)
		if e != nil {
			return e
		}
		matches := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']([^"']+)["']`).FindAllStringSubmatch(string(bytes), -1)
		matches = append(matches, regexp.MustCompile(`(?i)url\(\s*["']?([^\s)'";]+)["']?\s*\)`).FindAllStringSubmatch(string(bytes), -1)...)
		for _, match := range matches {
			if regexp.MustCompile(`(?i)^(?:https?:)?//`).MatchString(match[1]) {
				return s.reject("HANDOFF_PREVIEW", "离线预览包含远程资源")
			}
			dep, e := contractLocalDependency(ref, match[1])
			if e != nil {
				return s.reject("HANDOFF_PREVIEW", e.Error())
			}
			if dep != "" && !semHas(files, dep) {
				return s.reject("HANDOFF_PREVIEW", "离线资源未打包")
			}
		}
	}
	if text(config["profile"]) == "H2" {
		sourceDigest, sourceFiles, e := contractTree(s, text(config["source_root"]))
		if e != nil {
			return e
		}
		if sourceDigest != text(config["source_digest"]) || !strings.HasPrefix(text(config["lock_ref"]), text(config["source_root"])+"/") {
			return s.reject("HANDOFF_PREVIEW", "H2 源码或锁文件批准摘要变化")
		}
		if _, e = s.bytes(text(config["lock_ref"])); e != nil {
			return e
		}
		for _, ref := range sourceFiles {
			for _, part := range strings.Split(ref, "/") {
				if part == "node_modules" || part == ".git" {
					return s.reject("HANDOFF_PREVIEW", "H2 源包含依赖缓存")
				}
			}
		}
	}
	receipt, e := contractBoundDoc(s, map[string]any{"ref": config["verification_ref"], "digest": config["verification_digest"]})
	if e != nil {
		return e
	}
	exit, ok := integer(receipt["exit_code"])
	if text(receipt["network_mode"]) != "offline" || !ok || exit != 0 || text(receipt["command"]) == "" || !contractDate(receipt["executed_at"]) || text(receipt["preview_digest"]) != digest || !contractUnique(receipt["case_ids"], true) {
		return s.reject("HANDOFF_PREVIEW", "真实离线浏览记录未绑定当前预览")
	}
	for _, id := range cases {
		if !semHas(receipt["case_ids"], id) {
			return s.reject("HANDOFF_PREVIEW", "离线验证未覆盖视觉用例")
		}
	}
	return contractEvidence(s, receipt["evidence_refs"])
}
