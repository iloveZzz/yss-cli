package kernel

import (
	"bytes"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const ApprovalRolesRef = ".template-spec/agents/digital-human-roles.yaml"

const ApprovalRegistryRef = ".template-spec/process/lifecycle-registry.yaml"

const ApprovalSkillsRef = ".template-spec/agents/yss-skill-registry.yaml"

const ApprovalSchemaRef = ".template-spec/process/schemas/approval-record.schema.json"

const ApprovalTaskSchemaRef = ".template-spec/process/schemas/digital-human-task-package.schema.json"

func ApMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

func ApArray(v any) []any {
	switch a := v.(type) {
	case []any:
		return a
	case []string:
		out := make([]any, len(a))
		for i, x := range a {
			out[i] = x
		}
		return out
	case []map[string]any:
		out := make([]any, len(a))
		for i, x := range a {
			out[i] = x
		}
		return out
	}
	return nil
}

func ApIsArray(v any) bool {
	switch v.(type) {
	case []any, []string, []map[string]any:
		return true
	}
	return false
}

// ECMAScript canonical JSON converts every JSON number to IEEE-754 first.
// Go preserves json.Number spellings, which would otherwise change digests.
func apJSONNumber(value string) string {
	n, err := strconv.ParseFloat(value, 64)
	if err != nil && !math.IsInf(n, 0) || math.IsNaN(n) || math.IsInf(n, 0) {
		return "null"
	}
	if n == 0 {
		return "0"
	}
	abs := math.Abs(n)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	s := strconv.FormatFloat(n, 'e', -1, 64)
	parts := strings.Split(s, "e")
	exponent, _ := strconv.Atoi(parts[1])
	sign := ""
	if exponent >= 0 {
		sign = "+"
	}
	return parts[0] + "e" + sign + strconv.Itoa(exponent)
}

func ApText(v any) string { s, _ := v.(string); return s }

func ApNumber(v any) int {
	switch x := v.(type) {
	case json.Number:
		n, valid := Integer(x)
		if !valid || n > int64(math.MaxInt) || n < int64(math.MinInt) {
			return -1
		}
		return int(n)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || x != math.Trunc(x) || x >= float64(math.MaxInt) || x < float64(math.MinInt) {
			return -1
		}
		return int(x)
	case int:
		return x
	case int64:
		if x > int64(math.MaxInt) || x < int64(math.MinInt) {
			return -1
		}
		return int(x)
	}
	return -1
}

func ApStrings(v any) []string {
	a := ApArray(v)
	out := make([]string, len(a))
	for i, x := range a {
		out[i] = ApText(x)
	}
	return out
}

func ApContains(v any, want string) bool {
	for _, s := range ApStrings(v) {
		if s == want {
			return true
		}
	}
	return false
}

func apUnique(v any) bool {
	a := ApArray(v)
	if len(a) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		s, ok := x.(string)
		if !ok || strings.TrimSpace(s) == "" || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

func ApSetEqual(a, b any) bool {
	if !apUnique(a) || !apUnique(b) || len(ApArray(a)) != len(ApArray(b)) {
		return false
	}
	for _, s := range ApStrings(a) {
		if !ApContains(b, s) {
			return false
		}
	}
	return true
}

func ApArraysEqual(a, b any) bool { return ApEqual(a, b) }

func apHex(v any) string { return strings.TrimPrefix(ApText(v), "sha256:") }

func apHashValid(v any, prefix bool) bool {
	s := ApText(v)
	if prefix {
		s = strings.TrimPrefix(s, "sha256:")
	}
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func ApFind(v any, key, want string) map[string]any {
	for _, x := range ApArray(v) {
		m := ApMap(x)
		if ApText(m[key]) == want {
			return m
		}
	}
	return nil
}

func ApCopy(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func ApTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		return x != "0"
	case int:
		return x != 0
	}
	return true
}

// apCanonical matches JSON.stringify(canonical(value)): integer property keys
// precede UTF-16-sorted remaining keys; JSON strings retain HTML and U+2028/2029.
// Arrays preserve order. Digests never depend on Go's map iteration order.
func ApCanonical(v any) []byte {
	var out bytes.Buffer
	var emit func(any)
	quote := func(s string) {
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		_ = e.Encode(s)
		raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
		raw = bytes.ReplaceAll(raw, []byte(`\u2028`), []byte("\u2028"))
		raw = bytes.ReplaceAll(raw, []byte(`\u2029`), []byte("\u2029"))
		out.Write(raw)
	}
	less := func(a, b string) bool {
		x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
		for i := 0; i < len(x) && i < len(y); i++ {
			if x[i] != y[i] {
				return x[i] < y[i]
			}
		}
		return len(x) < len(y)
	}
	emit = func(value any) {
		switch x := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			index := func(k string) (uint64, bool) {
				n, e := strconv.ParseUint(k, 10, 32)
				return n, e == nil && n < 4294967295 && strconv.FormatUint(n, 10) == k
			}
			sort.Slice(keys, func(i, j int) bool {
				a, ia := index(keys[i])
				b, ib := index(keys[j])
				if ia && ib {
					return a < b
				}
				if ia != ib {
					return ia
				}
				return less(keys[i], keys[j])
			})
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				quote(k)
				out.WriteByte(':')
				emit(x[k])
			}
			out.WriteByte('}')
		case []any:
			out.WriteByte('[')
			for i, a := range x {
				if i > 0 {
					out.WriteByte(',')
				}
				emit(a)
			}
			out.WriteByte(']')
		case []string:
			emit(ApArray(x))
		case []map[string]any:
			emit(ApArray(x))
		case string:
			quote(x)
		case json.Number:
			out.WriteString(apJSONNumber(string(x)))
		case float64:
			out.WriteString(apJSONNumber(strconv.FormatFloat(x, 'g', -1, 64)))
		case float32:
			out.WriteString(apJSONNumber(strconv.FormatFloat(float64(x), 'g', -1, 64)))
		case int:
			out.WriteString(apJSONNumber(strconv.Itoa(x)))
		case int64:
			out.WriteString(apJSONNumber(strconv.FormatInt(x, 10)))
		default:
			b, e := json.Marshal(value)
			if e != nil {
				out.WriteString("null")
			} else {
				out.Write(b)
			}
		}
	}
	emit(v)
	return out.Bytes()
}

func ApEqual(a, b any) bool { return bytes.Equal(ApCanonical(a), ApCanonical(b)) }

func ApDigest(v any) string {
	if b, ok := v.([]byte); ok {
		return "sha256:" + safefs.Digest(b)
	}
	if s, ok := v.(string); ok {
		return "sha256:" + safefs.Digest([]byte(s))
	}
	return "sha256:" + safefs.Digest(ApCanonical(v))
}

func SemanticDocumentDigest(value any) (string, error) {
	if _, err := json.Marshal(value); err != nil {
		return "", err
	}
	return safefs.Digest(ApCanonical(value)), nil
}

func ApFail(s *SemanticSession, code, message string) error { return s.Reject(code, message) }

func apBasis(s *SemanticSession, v any, label, code string) ([]map[string]any, error) {
	a := ApArray(v)
	if len(a) == 0 {
		return nil, ApFail(s, code, label+" 缺少当前证据摘要")
	}
	refs := map[string]bool{}
	out := []map[string]any{}
	for _, x := range a {
		m := ApMap(x)
		ref := ApText(m["ref"])
		if err := RejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ref) == "" || refs[ref] || !apHashValid(m["digest"], true) {
			return nil, ApFail(s, code, label+" 证据引用缺失、重复或摘要非法")
		}
		refs[ref] = true
		b, e := s.Bytes(ref)
		if e != nil {
			return nil, e
		}
		if safefs.Digest(b) != apHex(m["digest"]) {
			return nil, ApFail(s, code, label+" 证据过期: "+ref)
		}
		out = append(out, map[string]any{"ref": ref, "digest": apHex(m["digest"])})
	}
	return out, nil
}

func apCovers(basis []map[string]any, row map[string]any) bool {
	for _, b := range basis {
		if ApText(b["ref"]) == ApText(row["ref"]) && apHex(b["digest"]) == apHex(row["digest"]) {
			return true
		}
	}
	return false
}

func ApRows(v any) []map[string]any {
	a := ApArray(v)
	out := make([]map[string]any, len(a))
	for i, x := range a {
		out[i] = ApMap(x)
	}
	return out
}

func approvalRule(s *SemanticSession, boundary string) (map[string]any, error) {
	p := ApMap(s.Roles["gate_policy"])
	for _, bucket := range []string{"check_reviews", "dual_digital_human", "digital_human_review"} {
		if row := ApFind(p[bucket], "gate", boundary); row != nil {
			rule := ApCopy(row)
			rule["bucket"] = bucket
			return rule, nil
		}
	}
	if ApContains(p["product_digital_human_with_biological_veto"], boundary) {
		return map[string]any{"bucket": "product_digital_human_with_biological_veto", "countersigners": []string{"role.product-manager"}}, nil
	}
	if ApContains(p["biological_human"], boundary) {
		return map[string]any{"bucket": "biological_human", "countersigners": []string{"role.biological-human"}}, nil
	}
	for _, bucket := range []string{"evidence_only", "orchestrator", "automatic_checks"} {
		if ApContains(p[bucket], boundary) {
			return nil, nil
		}
	}
	return nil, ApFail(s, "GATE_POLICY_REQUIRED", "未分类门禁或检查: "+boundary)
}

func assertApprovalSignerSemantic(s *SemanticSession, record map[string]any) (map[string]any, error) {
	if e := s.ValidateSchema(ApprovalSchemaRef, record); e != nil {
		return nil, e
	}
	gate := ApText(record["gate_id"])
	if ApContains(ApMap(s.Registry["id_policy"])["deprecated_ids"], gate) {
		return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "未知或已退役门禁: "+gate)
	}
	if ApFind(s.Roles["runtimes"], "id", ApText(record["runtime_id"])) == nil {
		return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "未知 runtime_id")
	}
	if ApText(record["actor_kind"]) == "orchestrator" {
		return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "编排器不能关闭会签门禁")
	}
	rule, e := approvalRule(s, gate)
	if e != nil {
		return nil, e
	}
	if record["continuation_ref"] != nil && ApText(record["actor_kind"]) == "digital-human" {
		reviewers := ApMap(ApMap(s.Roles["gate_policy"])["continuation_reviews"])[gate]
		if reviewers != nil {
			rule = map[string]any{"bucket": "digital_human_review", "countersigners": reviewers}
		}
	}
	if rule == nil || ApText(record["decision"]) != "approved" || record["biological_veto"] == true {
		return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "会签规则、approved 结论或生物人否决不允许关闭门禁")
	}
	if ApText(rule["bucket"]) == "biological_human" {
		if ApText(record["actor_kind"]) != "biological-human" || ApText(record["role_id"]) != "role.biological-human" {
			return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "必须由生物人会签")
		}
	} else {
		if ApText(record["actor_kind"]) != "digital-human" {
			return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "必须由数字人会签")
		}
		if !(ApNumber(record["schema_version"]) == 2 && len(ApArray(rule["capability_ids"])) > 0) && !ApContains(rule["countersigners"], ApText(record["role_id"])) {
			return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "会签角色未获当前门禁政策授权")
		}
		drafter := ApText(rule["drafter"])
		if ApNumber(record["schema_version"]) == 1 && drafter != "" && (ApText(record["role_id"]) == drafter || ApContains(record["countersigner_role_ids"], drafter)) {
			return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "起草角色不得会签自己")
		}
		if drafter != "" && ApText(record["drafter_role_id"]) != "" && ApText(record["drafter_role_id"]) != drafter {
			return nil, ApFail(s, "APPROVAL_CURRENT_INVALID", "drafter_role_id 与门禁政策不符")
		}
	}
	return rule, nil
}

func ApprovalExpectationFromState(s *SemanticSession, boundary string, state map[string]any) (map[string]any, error) {
	ref := ApText(state["subject_ref"])
	for _, ref := range []string{ref, ApText(state["approval_ref"])} {
		if err := RejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
	}
	if ref == "" || !apUnique(state["approval_scope"]) {
		return nil, ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前消费边界缺少主体或批准范围")
	}
	b, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	digest := state["subject_digest"]
	if !ApTruthy(digest) {
		for _, x := range ApRows(state["basis"]) {
			if ApText(x["ref"]) == ref {
				digest = x["digest"]
				break
			}
		}
	}
	if !ApTruthy(digest) {
		digest = safefs.Digest(b)
	}
	basis := []map[string]any{}
	for _, x := range ApRows(state["basis"]) {
		if ApText(x["ref"]) != ref && ApText(x["ref"]) != ApText(state["approval_ref"]) {
			basis = append(basis, x)
		}
	}
	drafter := state["drafter_principal_ref"]
	if !ApTruthy(drafter) {
		subject, _ := s.Doc(ref)
		drafter = subject["drafter_principal_ref"]
	}
	reviewPackage := any(strings.HasPrefix(boundary, "gate."))
	if state["review_package"] != nil {
		reviewPackage = state["review_package"]
	}
	return map[string]any{"boundary": boundary, "subject_ref": ref, "subject_digest": digest, "approval_scope": state["approval_scope"], "basis": basis, "drafter_principal_ref": drafter, "review_package": reviewPackage, "review_context": state["review_context"], "implementer_principal_ref": state["implementer_principal_ref"], "subject_id": state["subject_id"], "subject_version": state["subject_version"]}, nil
}

func ReviewBundleRowsSemantic(s *SemanticSession, bundle map[string]any) ([]map[string]any, error) {
	version := ApNumber(bundle["schema_version"])
	rows := ApRows(bundle["reviews"])
	if (version != 1 && version != 2) || ApText(bundle["kind"]) != "review-bundle" || len(rows) == 0 {
		return nil, ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查身份无效或为空")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		gate := ApText(row["gate_id"])
		if gate == "" || seen[gate] {
			return nil, ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查 gate_id 缺失或重复")
		}
		seen[gate] = true
		for _, field := range []string{"schema_version", "role_id", "runtime_id", "principal_ref"} {
			if !ApEqual(row[field], bundle[field]) {
				return nil, ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查身份不同: "+field)
			}
		}
		if row["review_session_id"] != nil && !ApEqual(row["review_session_id"], bundle["review_session_id"]) {
			return nil, ApFail(s, "APPROVAL_BUNDLE_INVALID", "review_session_id 不一致")
		}
		if version == 2 {
			for _, field := range []string{"review_task_ref", "review_task_digest"} {
				if !ApEqual(row[field], bundle[field]) {
					return nil, ApFail(s, "APPROVAL_BUNDLE_INVALID", field+" 不一致")
				}
			}
			if !ApSetEqual(row["capability_ids"], bundle["capability_ids"]) {
				return nil, ApFail(s, "APPROVAL_BUNDLE_INVALID", "capability_ids 不一致")
			}
			for _, asset := range ApRows(row["basis"]) {
				found := false
				for _, bound := range ApRows(bundle["basis"]) {
					if ApEqual(asset, bound) {
						found = true
					}
				}
				if !found {
					return nil, ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合依据没有覆盖逐项 basis")
				}
			}
		}
	}
	return rows, nil
}

func SelectApprovalRecordSemantic(s *SemanticSession, record map[string]any, gate string) (map[string]any, error) {
	if ApText(record["kind"]) != "review-bundle" {
		return record, nil
	}
	rows, e := ReviewBundleRowsSemantic(s, record)
	if e != nil {
		return nil, e
	}
	row := ApFind(rows, "gate_id", gate)
	if row == nil {
		return nil, ApFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查缺少明确当前边界")
	}
	out := ApCopy(row)
	for _, pair := range [][2]string{{"review_bundle_id", "bundle_id"}, {"review_task_id", "task_id"}, {"review_work_unit_id", "work_unit_id"}, {"review_session_id", "review_session_id"}} {
		out[pair[0]] = record[pair[1]]
	}
	if ApNumber(record["schema_version"]) == 2 {
		out["review_bundle_basis"] = record["basis"]
	}
	if record["plan_review_binding"] != nil {
		out["plan_review_binding"] = record["plan_review_binding"]
	}
	return out, nil
}

// The current consumer excludes intent from its existing reference fields. Pure
// historical schema inspection deliberately does not call this proof boundary.
func CurrentApprovalReferencesSemantic(s *SemanticSession, record map[string]any) error {
	refs := []string{ApText(record["subject_ref"]), ApText(record["review_task_ref"]), ApText(record["continuation_ref"])}
	refs = append(refs, SemStrings(record["evidence_refs"])...)
	for _, field := range []string{"basis", "review_bundle_basis"} {
		for _, asset := range ApRows(record[field]) {
			refs = append(refs, ApText(asset["ref"]))
		}
	}
	for _, ref := range refs {
		if err := RejectProgressionEvidence(s, ref); err != nil {
			return err
		}
	}
	return nil
}

func AssertCurrentApprovalSemantic(s *SemanticSession, record, expected map[string]any) error {
	if err := CurrentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	if err := RejectProgressionEvidence(s, ApText(expected["subject_ref"])); err != nil {
		return err
	}
	_, e := assertApprovalSignerSemantic(s, record)
	if e != nil {
		return e
	}
	gate := ApText(expected["boundary"])
	if gate == "" || ApText(expected["subject_ref"]) == "" || !apUnique(expected["approval_scope"]) || len(ApArray(expected["basis"])) == 0 {
		return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前批准缺少独立边界、范围及依据")
	}
	if ApNumber(record["schema_version"]) == 2 {
		consumerReview := ApMap(expected["review_context"])
		taskRef := ApText(consumerReview["review_task_ref"])
		taskDigest := ApText(consumerReview["review_task_digest"])
		if taskRef == "" || !apHashValid(taskDigest, false) {
			return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "v2 批准须由消费者独立指定当前正式审查任务和摘要")
		}
		if record["review_task_ref"] != taskRef || record["review_task_digest"] != taskDigest {
			return ApFail(s, "REVIEW_BINDING_STALE", "会签未绑定消费者当前审查任务")
		}
		task, e := s.Doc(taskRef)
		if e != nil {
			return e
		}
		issued, e := ApprovalExpectedFromTaskSemantic(s, taskRef, task, gate, taskDigest, consumerReview)
		if e != nil {
			return e
		}
		if ApText(record["review_task_id"]) != "" && record["review_task_id"] != task["task_id"] || ApText(record["review_work_unit_id"]) != "" && record["review_work_unit_id"] != task["work_unit_id"] {
			return ApFail(s, "APPROVAL_CURRENT_INVALID", "组合包与正式任务身份不匹配")
		}
		if record["review_bundle_basis"] != nil {
			bundle := ApRows(record["review_bundle_basis"])
			checked := bundle
			if record["plan_review_binding"] != nil {
				checked = nil
				for _, b := range bundle {
					if apCovers(ApRows(issued["basis"]), b) {
						checked = append(checked, b)
					}
				}
			}
			if _, e = apBasis(s, checked, "组合包", "APPROVAL_CURRENT_INVALID"); e != nil {
				return e
			}
			taskBasis := ApRows(ApMap(task["review_context"])["basis"])
			if len(bundle) != len(taskBasis) {
				return ApFail(s, "APPROVAL_CURRENT_INVALID", "组合包依据数量与正式任务不匹配")
			}
			for _, b := range bundle {
				if !apCovers(taskBasis, b) {
					return ApFail(s, "APPROVAL_CURRENT_INVALID", "组合包依据与正式任务不匹配")
				}
			}
		}
		if issued["subject_ref"] != expected["subject_ref"] || !ApSetEqual(issued["approval_scope"], expected["approval_scope"]) {
			return ApFail(s, "APPROVAL_CURRENT_INVALID", "当前消费上下文与正式任务范围不匹配")
		}
		for _, b := range ApRows(expected["basis"]) {
			if !apCovers(ApRows(issued["basis"]), b) {
				return ApFail(s, "APPROVAL_CURRENT_INVALID", "消费上下文依据不属于正式任务")
			}
		}
		if ApText(expected["drafter_principal_ref"]) != "" && expected["drafter_principal_ref"] != issued["drafter_principal_ref"] {
			return ApFail(s, "APPROVAL_CURRENT_INVALID", "正式任务作者与消费上下文不匹配")
		}
		expected = ApCopy(expected)
		expected["drafter_principal_ref"] = issued["drafter_principal_ref"]
	}
	drafter := ApText(expected["drafter_principal_ref"])
	if strings.TrimSpace(drafter) == "" {
		return ApFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前消费上下文缺少独立起草者来源")
	}
	if record["gate_id"] != expected["boundary"] || record["subject_ref"] != expected["subject_ref"] || !ApSetEqual(record["approval_scope"], expected["approval_scope"]) {
		return ApFail(s, "APPROVAL_CURRENT_INVALID", "审查对象或范围不匹配")
	}
	if strings.TrimSpace(ApText(record["drafter_principal_ref"])) == "" || record["drafter_principal_ref"] == record["principal_ref"] || record["drafter_principal_ref"] != drafter || ApTruthy(expected["implementer_principal_ref"]) && record["principal_ref"] == expected["implementer_principal_ref"] {
		return ApFail(s, "APPROVAL_CURRENT_INVALID", "独立作者/实施者/审查身份不匹配")
	}
	b, e := s.Bytes(ApText(expected["subject_ref"]))
	if e != nil {
		return e
	}
	if !apHashValid(record["subject_digest"], true) || safefs.Digest(b) != apHex(record["subject_digest"]) || ApTruthy(expected["subject_digest"]) && safefs.Digest(b) != apHex(expected["subject_digest"]) {
		return ApFail(s, "APPROVAL_CURRENT_INVALID", "审查主体或消费边界摘要过期")
	}
	subject, _ := s.Doc(ApText(expected["subject_ref"]))
	for _, pair := range [][3]string{{"subject_id", "id", "contract_id"}, {"subject_version", "version", "contract_version"}} {
		if ApTruthy(expected[pair[0]]) && expected[pair[0]] != subject[pair[1]] && expected[pair[0]] != subject[pair[2]] {
			return ApFail(s, "APPROVAL_CURRENT_INVALID", "主体ID或版本不匹配")
		}
	}
	reviewPackage := strings.HasPrefix(gate, "gate.")
	if expected["review_package"] != nil {
		reviewPackage = expected["review_package"] == true
	}
	if reviewPackage && (subject["gate_id"] != gate || len(ApArray(subject["basis"])) == 0) {
		return ApFail(s, "APPROVAL_CURRENT_INVALID", "审阅包身份或依据缺失")
	}
	required, e := apBasis(s, expected["basis"], "当前消费上下文", "APPROVAL_CURRENT_INVALID")
	if e != nil {
		return e
	}
	recordBasis := record["basis"]
	if recordBasis == nil {
		recordBasis = subject["basis"]
	}
	bound, e := apBasis(s, recordBasis, "批准记录", "APPROVAL_CURRENT_INVALID")
	if e != nil {
		return e
	}
	for _, b := range required {
		if !apCovers(bound, b) {
			return ApFail(s, "APPROVAL_CURRENT_INVALID", "批准范围未覆盖证据: "+ApText(b["ref"]))
		}
	}
	if ApNumber(record["schema_version"]) == 2 {
		if e = assertReviewCapabilityBindingSemantic(s, record, ApMap(expected["review_context"])); e != nil {
			return e
		}
	}
	if ApContains(ApMap(s.Roles["user_decision_policy"])["gates"], gate) {
		result, e := assertApprovalUserDecisionSemantic(s, record)
		if e != nil {
			return e
		}
		if ApText(record["actor_kind"]) == "biological-human" {
			for _, v := range result.validated {
				if v.principal != ApText(record["principal_ref"]) {
					return ApFail(s, "APPROVAL_CURRENT_INVALID", "生物人会签者与原始回复者不一致")
				}
			}
		}
	}
	return nil
}
