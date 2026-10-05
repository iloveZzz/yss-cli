package governance

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

func object(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }
func text(v any) string                   { s, _ := v.(string); return s }

var integerNumberSyntax = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// integer compares exact JSON numeric values without rounding through float64.
// Scale is bounded before expanding digits, including hostile huge exponents.
func integer(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok || !integerNumberSyntax.MatchString(string(n)) {
		return 0, false
	}
	raw := string(n)
	negative := strings.HasPrefix(raw, "-")
	if negative {
		raw = raw[1:]
	}
	mantissa, exponent := raw, "0"
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		mantissa, exponent = raw[:i], raw[i+1:]
	}
	fraction := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		fraction = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return 0, true
	}
	expNegative := strings.HasPrefix(exponent, "-")
	exponent = strings.TrimPrefix(strings.TrimPrefix(exponent, "+"), "-")
	exponent = strings.TrimLeft(exponent, "0")
	if exponent == "" {
		exponent = "0"
	}
	if expNegative {
		exponent = "-" + exponent
	}
	exp, err := strconv.ParseInt(exponent, 10, 64)
	bound := int64(len(raw)) + 20
	if err != nil || exp > bound || exp < -bound {
		return 0, false
	}
	scale := exp - int64(fraction)
	if scale < 0 {
		remove := -scale
		if remove >= int64(len(digits)) {
			return 0, false
		}
		tail := digits[len(digits)-int(remove):]
		if strings.Trim(tail, "0") != "" {
			return 0, false
		}
		digits = digits[:len(digits)-int(remove)]
	} else {
		if int64(len(digits))+scale > 19 {
			return 0, false
		}
		digits += strings.Repeat("0", int(scale))
	}
	if negative {
		digits = "-" + digits
	}
	result, err := strconv.ParseInt(digits, 10, 64)
	return result, err == nil
}

func load(root, ref string) (map[string]any, error) {
	p, err := safefs.Path(root, ref)
	if err != nil {
		return nil, err
	}
	v, err := schema.LoadFile(p)
	if err != nil {
		return nil, err
	}
	m, ok := object(v)
	if !ok {
		return nil, domain.Fail("INPUT", "治理输入必须是对象: "+ref)
	}
	return m, nil
}

func registry(root string) (map[string]any, string, error) {
	const ref = ".template-spec/process/lifecycle-registry.yaml"
	m, err := load(root, ref)
	if err != nil {
		return nil, "", err
	}
	version, ok := integer(m["schema_version"])
	if !ok || version != 1 {
		return nil, "", domain.Fail("LIFECYCLE", "未知生命周期注册表 schema")
	}
	seen := map[string]bool{}
	stableID := regexp.MustCompile(`^(stage|gate|check|artifact|work-unit|evidence)\.[a-z0-9][a-z0-9-]*$`)
	prefixes := map[string]string{"stages": "stage.", "work_units": "work-unit.", "gates": "gate.", "artifacts": "artifact.", "evidence": "evidence.", "checks": "check."}
	for _, kind := range []string{"stages", "work_units", "gates", "artifacts", "evidence", "checks"} {
		if _, present := m[kind]; !present {
			continue
		}
		rows, ok := m[kind].([]any)
		if !ok {
			return nil, "", domain.Fail("LIFECYCLE", "注册表集合必须是数组: "+kind)
		}
		for _, v := range rows {
			row, ok := object(v)
			if !ok || text(row["id"]) == "" {
				return nil, "", domain.Fail("LIFECYCLE", "注册表条目缺少稳定 ID")
			}
			id := text(row["id"])
			if !stableID.MatchString(id) || len(id) <= len(prefixes[kind]) || id[:len(prefixes[kind])] != prefixes[kind] {
				return nil, "", domain.Fail("LIFECYCLE_ID", "稳定 ID 格式或类别非法: "+id)
			}
			if seen[id] {
				return nil, "", domain.Fail("LIFECYCLE", "稳定 ID 重复: "+id)
			}
			seen[id] = true
		}
	}
	p, _ := safefs.Path(root, ref)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	return m, "sha256:" + safefs.Digest(b), nil
}

func findRegistry(m map[string]any, id string) (string, map[string]any, bool) {
	for _, kind := range []string{"stages", "work_units", "gates", "artifacts", "evidence", "checks"} {
		rows, _ := m[kind].([]any)
		for _, v := range rows {
			row, _ := object(v)
			if text(row["id"]) == id {
				return kind, row, true
			}
		}
	}
	return "", nil, false
}

func lifecycleRun(group, action, root string, args map[string]string) (any, error) {
	if action != "query" && action != "status" {
		return nil, domain.Fail("UNPORTED", "生命周期动作尚未迁移: "+action)
	}
	m, digest, err := registry(root)
	if err != nil {
		return nil, err
	}
	base := map[string]any{"registry_ref": ".template-spec/process/lifecycle-registry.yaml", "registry_digest": digest, "read_only": true, "execution_authorization": "not-evaluated", "scope": "registered-state-only", "approval_created": false}
	if action == "query" {
		id := args["id"]
		if id == "" {
			id = args["work-unit"]
		}
		if id == "" {
			id = args["stage"]
		}
		if id == "" {
			id = args["arg0"]
		}
		if id != "" {
			kind, entry, ok := findRegistry(m, id)
			if !ok {
				return nil, domain.Fail("LIFECYCLE_ID", "未登记稳定 ID: "+id)
			}
			if group == "stage" && kind != "stages" {
				return nil, domain.Fail("LIFECYCLE_ID", "stage query 只接受阶段 ID")
			}
			base["kind"] = kind
			base["entry"] = entry
			return base, nil
		}
		if group == "stage" {
			base["entries"] = m["stages"]
		} else {
			base["registry"] = m
		}
		return base, nil
	}
	identity, err := load(root, "yss-project.yaml")
	if err != nil {
		return nil, err
	}
	version, ok := integer(identity["schema_version"])
	if !ok || version != 1 || text(identity["repository_mode"]) != "project-instance" {
		return nil, domain.Fail("IDENTITY", "生命周期状态仅适用于 schema v1 project-instance")
	}
	ref := args["checkpoint"]
	if ref == "" {
		ref = args["file"]
	}
	if ref == "" {
		ref = args["arg0"]
	}
	if ref == "" {
		return nil, domain.Fail("INPUT", "status 需要显式 --checkpoint")
	}
	cp, err := load(root, ref)
	if err != nil {
		return nil, err
	}
	version, ok = integer(cp["schema_version"])
	if !ok || version != 1 || text(cp["repository_mode"]) != "project-instance" {
		return nil, domain.Fail("LIFECYCLE", "checkpoint 身份或 schema 不支持")
	}
	stageID := text(cp["stage"])
	kind, stage, found := findRegistry(m, stageID)
	if !found || kind != "stages" {
		return nil, domain.Fail("LIFECYCLE_ID", "checkpoint 阶段未登记: "+stageID)
	}
	base["checkpoint_ref"] = ref
	base["current_stage"] = stage
	base["next_work_unit"] = cp["next_work_unit"]
	base["blockers"] = cp["blockers"]
	base["owner"] = "未登记"
	base["complete_verification"] = false
	diagnostics := []map[string]any{}
	blockers, hasBlockers := cp["blockers"].([]any)
	if !hasBlockers {
		diagnostics = append(diagnostics, map[string]any{"code": "registered-blockers-invalid", "message": "checkpoint 缺少有效 blockers，不能判定无阻塞"})
	} else if len(blockers) > 0 {
		diagnostics = append(diagnostics, map[string]any{"code": "registered-blocker", "message": "存在已登记阻塞"})
	}
	nextID := text(cp["next_work_unit"])
	nextKind, work, known := findRegistry(m, nextID)
	known = known && nextKind == "work_units"
	nextStages := map[string]bool{}
	if nextID != "" && !known {
		diagnostics = append(diagnostics, map[string]any{"code": "next-work-unit-unregistered", "message": "下一工作单元未登记"})
	}
	if known && text(work["stage"]) != "" {
		nextStages[text(work["stage"])] = true
	}
	if tracking, ok := object(cp["stage_tracking"]); ok {
		if items, ok := tracking["items"].([]any); ok {
			for _, v := range items {
				item, ok := object(v)
				if ok && text(item["work_unit"]) == nextID && text(item["progress"]) != "cancelled" {
					if text(item["stage"]) != "" {
						nextStages[text(item["stage"])] = true
					}
					if base["owner"] == "未登记" && text(item["progress"]) != "completed" && text(item["owner"]) != "" {
						base["owner"] = item["owner"]
					}
				}
			}
		}
	}
	if pause, ok := object(cp["pause"]); ok && text(pause["owner_or_authority"]) != "" {
		base["owner"] = pause["owner_or_authority"]
	}
	base["next_stage"] = nil
	ids := []string{}
	for id := range nextStages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 1 {
		kind, entry, ok := findRegistry(m, ids[0])
		if ok && kind == "stages" {
			base["next_stage"] = entry
		} else {
			diagnostics = append(diagnostics, map[string]any{"code": "next-stage-unverifiable", "message": "已登记下一阶段未在注册表中识别"})
		}
	} else if len(ids) > 1 {
		diagnostics = append(diagnostics, map[string]any{"code": "next-stage-conflict", "message": "下一阶段登记冲突"})
	} else {
		diagnostics = append(diagnostics, map[string]any{"code": "next-stage-unregistered", "message": "未登记下一阶段；不能按阶段顺序推断"})
	}
	base["diagnostics"] = diagnostics
	base["next_stage_candidates"] = ids
	return base, nil
}

func assetCheck(group, action, root string, args map[string]string) (any, error) {
	if action != "check" && action != "verify" {
		return nil, domain.Fail("UNPORTED", group+" 动作尚未迁移: "+action)
	}
	// Full verify remains blocked until each domain's approval, evidence and byte binding is ported.
	if action == "verify" {
		return nil, domain.Fail("UNPORTED", group+" 完整 verify 的语义门禁尚未迁移；使用 check 仅执行显式 Schema 结构校验")
	}
	schemaRef := args["schema"]
	fileRef := args["file"]
	if fileRef == "" {
		fileRef = args["arg0"]
	}
	if schemaRef == "" || fileRef == "" {
		return nil, domain.Fail("INPUT", "结构校验需要 --schema 和 --file")
	}
	sp, err := safefs.Path(root, schemaRef)
	if err != nil {
		return nil, err
	}
	fp, err := safefs.Path(root, fileRef)
	if err != nil {
		return nil, err
	}
	issues, err := schema.Validate(sp, fp)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return map[string]any{"schema_validation": "failed", "issues": issues, "execution_authorization": "not-evaluated"}, domain.Fail("SCHEMA", "Schema 结构校验失败")
	}
	b, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	sb, err := os.ReadFile(sp)
	if err != nil {
		return nil, err
	}
	return map[string]any{"schema_validation": "passed", "semantic_validation": "UNPORTED", "scope": "schema-structure-only", "file": fileRef, "file_digest": "sha256:" + safefs.Digest(b), "schema": schemaRef, "schema_digest": "sha256:" + safefs.Digest(sb), "read_only": true, "execution_authorization": "not-evaluated", "approval_created": false}, nil
}

func rejectUnportedArgs(args map[string]string, allowed ...string) error {
	set := map[string]bool{}
	for _, s := range allowed {
		set[s] = true
	}
	keys := []string{}
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !set[k] {
			return domain.Fail("UNPORTED", "参数语义尚未迁移: --"+k)
		}
	}
	return nil
}
