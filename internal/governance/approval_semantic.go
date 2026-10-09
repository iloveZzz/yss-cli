package governance

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const approvalRolesRef = ".template-spec/agents/digital-human-roles.yaml"
const approvalRegistryRef = ".template-spec/process/lifecycle-registry.yaml"
const approvalSkillsRef = ".template-spec/agents/yss-skill-registry.yaml"
const approvalSchemaRef = ".template-spec/process/schemas/approval-record.schema.json"
const approvalTaskSchemaRef = ".template-spec/process/schemas/digital-human-task-package.schema.json"

func apMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func apArray(v any) []any {
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

func apIsArray(v any) bool {
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
func apText(v any) string { s, _ := v.(string); return s }
func apNumber(v any) int {
	switch x := v.(type) {
	case json.Number:
		n, valid := integer(x)
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
func apStrings(v any) []string {
	a := apArray(v)
	out := make([]string, len(a))
	for i, x := range a {
		out[i] = apText(x)
	}
	return out
}
func apContains(v any, want string) bool {
	for _, s := range apStrings(v) {
		if s == want {
			return true
		}
	}
	return false
}
func apUnique(v any) bool {
	a := apArray(v)
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
func apSetEqual(a, b any) bool {
	if !apUnique(a) || !apUnique(b) || len(apArray(a)) != len(apArray(b)) {
		return false
	}
	for _, s := range apStrings(a) {
		if !apContains(b, s) {
			return false
		}
	}
	return true
}
func apArraysEqual(a, b any) bool { return apEqual(a, b) }
func apHex(v any) string          { return strings.TrimPrefix(apText(v), "sha256:") }
func apHashValid(v any, prefix bool) bool {
	s := apText(v)
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
func apFind(v any, key, want string) map[string]any {
	for _, x := range apArray(v) {
		m := apMap(x)
		if apText(m[key]) == want {
			return m
		}
	}
	return nil
}
func apCopy(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
func apTruthy(v any) bool {
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
func apCanonical(v any) []byte {
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
			emit(apArray(x))
		case []map[string]any:
			emit(apArray(x))
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
func apEqual(a, b any) bool { return bytes.Equal(apCanonical(a), apCanonical(b)) }
func apDigest(v any) string {
	if b, ok := v.([]byte); ok {
		return "sha256:" + safefs.Digest(b)
	}
	if s, ok := v.(string); ok {
		return "sha256:" + safefs.Digest([]byte(s))
	}
	return "sha256:" + safefs.Digest(apCanonical(v))
}
func semanticDocumentDigest(value any) (string, error) {
	if _, err := json.Marshal(value); err != nil {
		return "", err
	}
	return safefs.Digest(apCanonical(value)), nil
}
func apFail(s *semanticSession, code, message string) error { return s.reject(code, message) }
func apBasis(s *semanticSession, v any, label, code string) ([]map[string]any, error) {
	a := apArray(v)
	if len(a) == 0 {
		return nil, apFail(s, code, label+" 缺少当前证据摘要")
	}
	refs := map[string]bool{}
	out := []map[string]any{}
	for _, x := range a {
		m := apMap(x)
		ref := apText(m["ref"])
		if err := rejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ref) == "" || refs[ref] || !apHashValid(m["digest"], true) {
			return nil, apFail(s, code, label+" 证据引用缺失、重复或摘要非法")
		}
		refs[ref] = true
		b, e := s.bytes(ref)
		if e != nil {
			return nil, e
		}
		if safefs.Digest(b) != apHex(m["digest"]) {
			return nil, apFail(s, code, label+" 证据过期: "+ref)
		}
		out = append(out, map[string]any{"ref": ref, "digest": apHex(m["digest"])})
	}
	return out, nil
}
func apCovers(basis []map[string]any, row map[string]any) bool {
	for _, b := range basis {
		if apText(b["ref"]) == apText(row["ref"]) && apHex(b["digest"]) == apHex(row["digest"]) {
			return true
		}
	}
	return false
}
func apRows(v any) []map[string]any {
	a := apArray(v)
	out := make([]map[string]any, len(a))
	for i, x := range a {
		out[i] = apMap(x)
	}
	return out
}

func init() { registerSemanticValidator("approval", verifyApprovalSemantic) }

func approvalRule(s *semanticSession, boundary string) (map[string]any, error) {
	p := apMap(s.roles["gate_policy"])
	for _, bucket := range []string{"check_reviews", "dual_digital_human", "digital_human_review"} {
		if row := apFind(p[bucket], "gate", boundary); row != nil {
			rule := apCopy(row)
			rule["bucket"] = bucket
			return rule, nil
		}
	}
	if apContains(p["product_digital_human_with_biological_veto"], boundary) {
		return map[string]any{"bucket": "product_digital_human_with_biological_veto", "countersigners": []string{"role.product-manager"}}, nil
	}
	if apContains(p["biological_human"], boundary) {
		return map[string]any{"bucket": "biological_human", "countersigners": []string{"role.biological-human"}}, nil
	}
	for _, bucket := range []string{"evidence_only", "orchestrator", "automatic_checks"} {
		if apContains(p[bucket], boundary) {
			return nil, nil
		}
	}
	return nil, apFail(s, "GATE_POLICY_REQUIRED", "未分类门禁或检查: "+boundary)
}

func assertApprovalSignerSemantic(s *semanticSession, record map[string]any) (map[string]any, error) {
	if e := s.validateSchema(approvalSchemaRef, record); e != nil {
		return nil, e
	}
	gate := apText(record["gate_id"])
	if apContains(apMap(s.registry["id_policy"])["deprecated_ids"], gate) {
		return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "未知或已退役门禁: "+gate)
	}
	if apFind(s.roles["runtimes"], "id", apText(record["runtime_id"])) == nil {
		return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "未知 runtime_id")
	}
	if apText(record["actor_kind"]) == "orchestrator" {
		return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "编排器不能关闭会签门禁")
	}
	rule, e := approvalRule(s, gate)
	if e != nil {
		return nil, e
	}
	if record["continuation_ref"] != nil && apText(record["actor_kind"]) == "digital-human" {
		reviewers := apMap(apMap(s.roles["gate_policy"])["continuation_reviews"])[gate]
		if reviewers != nil {
			rule = map[string]any{"bucket": "digital_human_review", "countersigners": reviewers}
		}
	}
	if rule == nil || apText(record["decision"]) != "approved" || record["biological_veto"] == true {
		return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "会签规则、approved 结论或生物人否决不允许关闭门禁")
	}
	if apText(rule["bucket"]) == "biological_human" {
		if apText(record["actor_kind"]) != "biological-human" || apText(record["role_id"]) != "role.biological-human" {
			return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "必须由生物人会签")
		}
	} else {
		if apText(record["actor_kind"]) != "digital-human" {
			return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "必须由数字人会签")
		}
		if !(apNumber(record["schema_version"]) == 2 && len(apArray(rule["capability_ids"])) > 0) && !apContains(rule["countersigners"], apText(record["role_id"])) {
			return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "会签角色未获当前门禁政策授权")
		}
		drafter := apText(rule["drafter"])
		if apNumber(record["schema_version"]) == 1 && drafter != "" && (apText(record["role_id"]) == drafter || apContains(record["countersigner_role_ids"], drafter)) {
			return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "起草角色不得会签自己")
		}
		if drafter != "" && apText(record["drafter_role_id"]) != "" && apText(record["drafter_role_id"]) != drafter {
			return nil, apFail(s, "APPROVAL_CURRENT_INVALID", "drafter_role_id 与门禁政策不符")
		}
	}
	return rule, nil
}

func approvalExpectationFromState(s *semanticSession, boundary string, state map[string]any) (map[string]any, error) {
	ref := apText(state["subject_ref"])
	for _, ref := range []string{ref, apText(state["approval_ref"])} {
		if err := rejectProgressionEvidence(s, ref); err != nil {
			return nil, err
		}
	}
	if ref == "" || !apUnique(state["approval_scope"]) {
		return nil, apFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前消费边界缺少主体或批准范围")
	}
	b, e := s.bytes(ref)
	if e != nil {
		return nil, e
	}
	digest := state["subject_digest"]
	if !apTruthy(digest) {
		for _, x := range apRows(state["basis"]) {
			if apText(x["ref"]) == ref {
				digest = x["digest"]
				break
			}
		}
	}
	if !apTruthy(digest) {
		digest = safefs.Digest(b)
	}
	basis := []map[string]any{}
	for _, x := range apRows(state["basis"]) {
		if apText(x["ref"]) != ref && apText(x["ref"]) != apText(state["approval_ref"]) {
			basis = append(basis, x)
		}
	}
	drafter := state["drafter_principal_ref"]
	if !apTruthy(drafter) {
		subject, _ := s.doc(ref)
		drafter = subject["drafter_principal_ref"]
	}
	reviewPackage := any(strings.HasPrefix(boundary, "gate."))
	if state["review_package"] != nil {
		reviewPackage = state["review_package"]
	}
	return map[string]any{"boundary": boundary, "subject_ref": ref, "subject_digest": digest, "approval_scope": state["approval_scope"], "basis": basis, "drafter_principal_ref": drafter, "review_package": reviewPackage, "review_context": state["review_context"], "implementer_principal_ref": state["implementer_principal_ref"], "subject_id": state["subject_id"], "subject_version": state["subject_version"]}, nil
}

func reviewBundleRowsSemantic(s *semanticSession, bundle map[string]any) ([]map[string]any, error) {
	version := apNumber(bundle["schema_version"])
	rows := apRows(bundle["reviews"])
	if (version != 1 && version != 2) || apText(bundle["kind"]) != "review-bundle" || len(rows) == 0 {
		return nil, apFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查身份无效或为空")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		gate := apText(row["gate_id"])
		if gate == "" || seen[gate] {
			return nil, apFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查 gate_id 缺失或重复")
		}
		seen[gate] = true
		for _, field := range []string{"schema_version", "role_id", "runtime_id", "principal_ref"} {
			if !apEqual(row[field], bundle[field]) {
				return nil, apFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查身份不同: "+field)
			}
		}
		if row["review_session_id"] != nil && !apEqual(row["review_session_id"], bundle["review_session_id"]) {
			return nil, apFail(s, "APPROVAL_BUNDLE_INVALID", "review_session_id 不一致")
		}
		if version == 2 {
			for _, field := range []string{"review_task_ref", "review_task_digest"} {
				if !apEqual(row[field], bundle[field]) {
					return nil, apFail(s, "APPROVAL_BUNDLE_INVALID", field+" 不一致")
				}
			}
			if !apSetEqual(row["capability_ids"], bundle["capability_ids"]) {
				return nil, apFail(s, "APPROVAL_BUNDLE_INVALID", "capability_ids 不一致")
			}
			for _, asset := range apRows(row["basis"]) {
				found := false
				for _, bound := range apRows(bundle["basis"]) {
					if apEqual(asset, bound) {
						found = true
					}
				}
				if !found {
					return nil, apFail(s, "APPROVAL_BUNDLE_INVALID", "组合依据没有覆盖逐项 basis")
				}
			}
		}
	}
	return rows, nil
}

func selectApprovalRecordSemantic(s *semanticSession, record map[string]any, gate string) (map[string]any, error) {
	if apText(record["kind"]) != "review-bundle" {
		return record, nil
	}
	rows, e := reviewBundleRowsSemantic(s, record)
	if e != nil {
		return nil, e
	}
	row := apFind(rows, "gate_id", gate)
	if row == nil {
		return nil, apFail(s, "APPROVAL_BUNDLE_INVALID", "组合审查缺少明确当前边界")
	}
	out := apCopy(row)
	for _, pair := range [][2]string{{"review_bundle_id", "bundle_id"}, {"review_task_id", "task_id"}, {"review_work_unit_id", "work_unit_id"}, {"review_session_id", "review_session_id"}} {
		out[pair[0]] = record[pair[1]]
	}
	if apNumber(record["schema_version"]) == 2 {
		out["review_bundle_basis"] = record["basis"]
	}
	if record["plan_review_binding"] != nil {
		out["plan_review_binding"] = record["plan_review_binding"]
	}
	return out, nil
}

// verifyApprovalSemantic consumes only independently selected checkpoint/task
// context. History performs structure checks and never current authorization.
func verifyApprovalSemantic(s *semanticSession, ref string, opts map[string]string) error {
	opts = semanticOptions(opts)
	if opts["gate"] == "" {
		opts["gate"] = opts["boundary"]
	}
	record, e := s.doc(ref)
	if e != nil {
		return e
	}
	if apText(record["kind"]) == "review-bundle" {
		if e = s.validateSchema(".template-spec/process/schemas/review-bundle.schema.json", record); e != nil {
			return e
		}
	} else {
		if e = s.validateSchema(approvalSchemaRef, record); e != nil {
			return e
		}
	}
	if opts["history"] == "true" {
		if opts["checkpoint"] != "" || s.checkpointRef != "" || opts["task"] != "" || opts["require-approved"] == "true" {
			return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "history 不得用于当前批准校验")
		}
		if apText(record["kind"]) == "review-bundle" {
			if _, e = reviewBundleRowsSemantic(s, record); e != nil {
				return e
			}
		}
		s.report.Applicability = append(s.report.Applicability, map[string]any{"kind": "approval", "mode": "history-only", "execution_authorization": "not-evaluated"})
		return nil
	}
	if err := rejectProgressionEvidence(s, ref); err != nil {
		return err
	}
	if err := currentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	checkpointRef := opts["checkpoint"]
	if checkpointRef == "" {
		checkpointRef = s.checkpointRef
	}
	var checkpoint map[string]any
	if checkpointRef != "" {
		checkpoint, e = s.doc(checkpointRef)
		if e != nil {
			return e
		}
		if e = s.validateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", checkpoint); e != nil {
			return e
		}
	}
	rows := []map[string]any{record}
	if apText(record["kind"]) == "review-bundle" {
		rows, e = reviewBundleRowsSemantic(s, record)
		if e != nil {
			return e
		}
	}
	for _, row := range rows {
		gate := apText(row["gate_id"])
		if opts["gate"] != "" && gate != opts["gate"] {
			continue
		}
		selected, e := selectApprovalRecordSemantic(s, record, gate)
		if e != nil {
			return e
		}
		if gate == "gate.plan-approved" {
			if checkpointRef == "" {
				return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "Plan 聚合批准须完整当前 checkpoint")
			}
			if e = s.verify("plan-aggregate", checkpointRef, map[string]string{"approval": ref, "gate": gate}); e != nil {
				return e
			}
			continue
		}
		var expected map[string]any
		if checkpoint != nil {
			state := apMap(apMap(checkpoint["gates"])[gate])
			if state == nil {
				state = apMap(apMap(checkpoint["checks"])[gate])
			}
			expected, e = approvalExpectationFromState(s, gate, state)
		} else if opts["task"] != "" {
			task, e2 := s.doc(opts["task"])
			if e2 != nil {
				return e2
			}
			bound, e2 := s.bind(opts["task"])
			if e2 != nil {
				return e2
			}
			expected, e = approvalExpectedFromTaskSemantic(s, opts["task"], task, gate, strings.TrimPrefix(bound.Digest, "sha256:"), nil)
		} else {
			return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前批准必须由 checkpoint 或显式 task 提供期望")
		}
		if e != nil {
			return e
		}
		if e = assertCurrentApprovalSemantic(s, selected, expected); e != nil {
			return e
		}
	}
	if opts["gate"] != "" && apFind(rows, "gate_id", opts["gate"]) == nil {
		return apFail(s, "APPROVAL_BUNDLE_INVALID", "批准资产不包含消费者 gate")
	}
	s.report.Applicability = append(s.report.Applicability, map[string]any{"kind": "approval", "mode": "current-binding", "execution_authorization": "not-evaluated"})
	return nil
}

// The current consumer excludes intent from its existing reference fields. Pure
// historical schema inspection deliberately does not call this proof boundary.
func currentApprovalReferencesSemantic(s *semanticSession, record map[string]any) error {
	refs := []string{apText(record["subject_ref"]), apText(record["review_task_ref"]), apText(record["continuation_ref"])}
	refs = append(refs, semStrings(record["evidence_refs"])...)
	for _, field := range []string{"basis", "review_bundle_basis"} {
		for _, asset := range apRows(record[field]) {
			refs = append(refs, apText(asset["ref"]))
		}
	}
	for _, ref := range refs {
		if err := rejectProgressionEvidence(s, ref); err != nil {
			return err
		}
	}
	return nil
}

func assertCurrentApprovalSemantic(s *semanticSession, record, expected map[string]any) error {
	if err := currentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	if err := rejectProgressionEvidence(s, apText(expected["subject_ref"])); err != nil {
		return err
	}
	_, e := assertApprovalSignerSemantic(s, record)
	if e != nil {
		return e
	}
	gate := apText(expected["boundary"])
	if gate == "" || apText(expected["subject_ref"]) == "" || !apUnique(expected["approval_scope"]) || len(apArray(expected["basis"])) == 0 {
		return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前批准缺少独立边界、范围及依据")
	}
	if apNumber(record["schema_version"]) == 2 {
		consumerReview := apMap(expected["review_context"])
		taskRef := apText(consumerReview["review_task_ref"])
		taskDigest := apText(consumerReview["review_task_digest"])
		if taskRef == "" || !apHashValid(taskDigest, false) {
			return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "v2 批准须由消费者独立指定当前正式审查任务和摘要")
		}
		if record["review_task_ref"] != taskRef || record["review_task_digest"] != taskDigest {
			return apFail(s, "REVIEW_BINDING_STALE", "会签未绑定消费者当前审查任务")
		}
		task, e := s.doc(taskRef)
		if e != nil {
			return e
		}
		issued, e := approvalExpectedFromTaskSemantic(s, taskRef, task, gate, taskDigest, consumerReview)
		if e != nil {
			return e
		}
		if apText(record["review_task_id"]) != "" && record["review_task_id"] != task["task_id"] || apText(record["review_work_unit_id"]) != "" && record["review_work_unit_id"] != task["work_unit_id"] {
			return apFail(s, "APPROVAL_CURRENT_INVALID", "组合包与正式任务身份不匹配")
		}
		if record["review_bundle_basis"] != nil {
			bundle := apRows(record["review_bundle_basis"])
			checked := bundle
			if record["plan_review_binding"] != nil {
				checked = nil
				for _, b := range bundle {
					if apCovers(apRows(issued["basis"]), b) {
						checked = append(checked, b)
					}
				}
			}
			if _, e = apBasis(s, checked, "组合包", "APPROVAL_CURRENT_INVALID"); e != nil {
				return e
			}
			taskBasis := apRows(apMap(task["review_context"])["basis"])
			if len(bundle) != len(taskBasis) {
				return apFail(s, "APPROVAL_CURRENT_INVALID", "组合包依据数量与正式任务不匹配")
			}
			for _, b := range bundle {
				if !apCovers(taskBasis, b) {
					return apFail(s, "APPROVAL_CURRENT_INVALID", "组合包依据与正式任务不匹配")
				}
			}
		}
		if issued["subject_ref"] != expected["subject_ref"] || !apSetEqual(issued["approval_scope"], expected["approval_scope"]) {
			return apFail(s, "APPROVAL_CURRENT_INVALID", "当前消费上下文与正式任务范围不匹配")
		}
		for _, b := range apRows(expected["basis"]) {
			if !apCovers(apRows(issued["basis"]), b) {
				return apFail(s, "APPROVAL_CURRENT_INVALID", "消费上下文依据不属于正式任务")
			}
		}
		if apText(expected["drafter_principal_ref"]) != "" && expected["drafter_principal_ref"] != issued["drafter_principal_ref"] {
			return apFail(s, "APPROVAL_CURRENT_INVALID", "正式任务作者与消费上下文不匹配")
		}
		expected = apCopy(expected)
		expected["drafter_principal_ref"] = issued["drafter_principal_ref"]
	}
	drafter := apText(expected["drafter_principal_ref"])
	if strings.TrimSpace(drafter) == "" {
		return apFail(s, "APPROVAL_CONTEXT_REQUIRED", "当前消费上下文缺少独立起草者来源")
	}
	if record["gate_id"] != expected["boundary"] || record["subject_ref"] != expected["subject_ref"] || !apSetEqual(record["approval_scope"], expected["approval_scope"]) {
		return apFail(s, "APPROVAL_CURRENT_INVALID", "审查对象或范围不匹配")
	}
	if strings.TrimSpace(apText(record["drafter_principal_ref"])) == "" || record["drafter_principal_ref"] == record["principal_ref"] || record["drafter_principal_ref"] != drafter || apTruthy(expected["implementer_principal_ref"]) && record["principal_ref"] == expected["implementer_principal_ref"] {
		return apFail(s, "APPROVAL_CURRENT_INVALID", "独立作者/实施者/审查身份不匹配")
	}
	b, e := s.bytes(apText(expected["subject_ref"]))
	if e != nil {
		return e
	}
	if !apHashValid(record["subject_digest"], true) || safefs.Digest(b) != apHex(record["subject_digest"]) || apTruthy(expected["subject_digest"]) && safefs.Digest(b) != apHex(expected["subject_digest"]) {
		return apFail(s, "APPROVAL_CURRENT_INVALID", "审查主体或消费边界摘要过期")
	}
	subject, _ := s.doc(apText(expected["subject_ref"]))
	for _, pair := range [][3]string{{"subject_id", "id", "contract_id"}, {"subject_version", "version", "contract_version"}} {
		if apTruthy(expected[pair[0]]) && expected[pair[0]] != subject[pair[1]] && expected[pair[0]] != subject[pair[2]] {
			return apFail(s, "APPROVAL_CURRENT_INVALID", "主体ID或版本不匹配")
		}
	}
	reviewPackage := strings.HasPrefix(gate, "gate.")
	if expected["review_package"] != nil {
		reviewPackage = expected["review_package"] == true
	}
	if reviewPackage && (subject["gate_id"] != gate || len(apArray(subject["basis"])) == 0) {
		return apFail(s, "APPROVAL_CURRENT_INVALID", "审阅包身份或依据缺失")
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
			return apFail(s, "APPROVAL_CURRENT_INVALID", "批准范围未覆盖证据: "+apText(b["ref"]))
		}
	}
	if apNumber(record["schema_version"]) == 2 {
		if e = assertReviewCapabilityBindingSemantic(s, record, apMap(expected["review_context"])); e != nil {
			return e
		}
	}
	if apContains(apMap(s.roles["user_decision_policy"])["gates"], gate) {
		result, e := assertApprovalUserDecisionSemantic(s, record)
		if e != nil {
			return e
		}
		if apText(record["actor_kind"]) == "biological-human" {
			for _, v := range result.validated {
				if v.principal != apText(record["principal_ref"]) {
					return apFail(s, "APPROVAL_CURRENT_INVALID", "生物人会签者与原始回复者不一致")
				}
			}
		}
	}
	return nil
}
