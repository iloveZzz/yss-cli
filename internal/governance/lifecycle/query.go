package lifecycle

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"os"
	"regexp"
	"sort"
)

func Load(root, ref string) (map[string]any, error) {
	p, err := safefs.Path(root, ref)
	if err != nil {
		return nil, err
	}
	v, err := schema.LoadFile(p)
	if err != nil {
		return nil, err
	}
	m, ok := kernel.Object(v)
	if !ok {
		return nil, domain.Fail("INPUT", "治理输入必须是对象: "+ref)
	}
	return m, nil
}

func Registry(root string) (map[string]any, string, error) {
	const ref = ".template-spec/process/lifecycle-registry.yaml"
	m, err := Load(root, ref)
	if err != nil {
		return nil, "", err
	}
	if err := ValidateRegistry(m); err != nil {
		return nil, "", err
	}
	p, _ := safefs.Path(root, ref)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", err
	}
	return m, "sha256:" + safefs.Digest(b), nil
}

func ValidateRegistry(m map[string]any) error {
	version, ok := kernel.Integer(m["schema_version"])
	if !ok || version != 1 {
		return domain.Fail("LIFECYCLE", "未知生命周期注册表 schema")
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
			return domain.Fail("LIFECYCLE", "注册表集合必须是数组: "+kind)
		}
		for _, v := range rows {
			row, ok := kernel.Object(v)
			if !ok || kernel.Text(row["id"]) == "" {
				return domain.Fail("LIFECYCLE", "注册表条目缺少稳定 ID")
			}
			id := kernel.Text(row["id"])
			if !stableID.MatchString(id) || len(id) <= len(prefixes[kind]) || id[:len(prefixes[kind])] != prefixes[kind] {
				return domain.Fail("LIFECYCLE_ID", "稳定 ID 格式或类别非法: "+id)
			}
			if seen[id] {
				return domain.Fail("LIFECYCLE", "稳定 ID 重复: "+id)
			}
			seen[id] = true
		}
	}
	return nil
}

func Query(ctx context.Context, group, action, root string, args map[string]string, sources QuerySources) (any, error) {
	if action != "query" && action != "status" {
		return nil, domain.Fail("UNPORTED", "不支持的生命周期动作: "+action)
	}
	m, digest, err := Registry(root)
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
			kind, entry, ok := kernel.FindRegistry(m, id)
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
	identity, err := Load(root, "yss-project.yaml")
	if err != nil {
		return nil, err
	}
	version, ok := kernel.Integer(identity["schema_version"])
	if !ok || version != 1 || kernel.Text(identity["repository_mode"]) != "project-instance" {
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
	cp, err := Load(root, ref)
	if err != nil {
		return nil, err
	}
	version, ok = kernel.Integer(cp["schema_version"])
	if !ok || version != 1 || kernel.Text(cp["repository_mode"]) != "project-instance" {
		return nil, domain.Fail("LIFECYCLE", "checkpoint 身份或 schema 不支持")
	}
	state, err := State(m, cp)
	if err != nil {
		return nil, err
	}
	for key, value := range state {
		base[key] = value
	}
	base["checkpoint_ref"] = ref
	base["profile_guidance"] = sources.ProfileGuidance(ctx, root, ref, cp)
	projection, progressionErr := sources.ProgressionRead(ctx, root, ref)
	if progressionErr == nil {
		for _, key := range []string{"progression", "coordination", "next_action"} {
			base[key] = projection[key]
		}
	} else {
		base["progression"] = map[string]any{"enabled": false, "status": "unsupported", "reason": progressionErr.Error(), "read_only": true}
	}
	return base, nil
}

func State(m, cp map[string]any) (map[string]any, error) {
	base := map[string]any{}
	stageID := kernel.Text(cp["stage"])
	kind, stage, found := kernel.FindRegistry(m, stageID)
	if !found || kind != "stages" {
		return nil, domain.Fail("LIFECYCLE_ID", "checkpoint 阶段未登记: "+stageID)
	}
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
	nextID := kernel.Text(cp["next_work_unit"])
	nextKind, work, known := kernel.FindRegistry(m, nextID)
	known = known && nextKind == "work_units"
	nextStages := map[string]bool{}
	if nextID != "" && !known {
		diagnostics = append(diagnostics, map[string]any{"code": "next-work-unit-unregistered", "message": "下一工作单元未登记"})
	}
	if known && kernel.Text(work["stage"]) != "" {
		nextStages[kernel.Text(work["stage"])] = true
	}
	if tracking, ok := kernel.Object(cp["stage_tracking"]); ok {
		if items, ok := tracking["items"].([]any); ok {
			for _, v := range items {
				item, ok := kernel.Object(v)
				if ok && kernel.Text(item["work_unit"]) == nextID && kernel.Text(item["progress"]) != "cancelled" {
					if kernel.Text(item["stage"]) != "" {
						nextStages[kernel.Text(item["stage"])] = true
					}
					if base["owner"] == "未登记" && kernel.Text(item["progress"]) != "completed" && kernel.Text(item["owner"]) != "" {
						base["owner"] = item["owner"]
					}
				}
			}
		}
	}
	if pause, ok := kernel.Object(cp["pause"]); ok && kernel.Text(pause["owner_or_authority"]) != "" {
		base["owner"] = pause["owner_or_authority"]
	}
	base["next_stage"] = nil
	ids := []string{}
	for id := range nextStages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 1 {
		kind, entry, ok := kernel.FindRegistry(m, ids[0])
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

func AssetCheck(group, action, root string, args map[string]string) (any, error) {
	if action != "check" && action != "verify" {
		return nil, domain.Fail("UNPORTED", "不支持的 "+group+" 动作: "+action)
	}
	// verify 在 governance.RunContext 已分派到原生语义校验；此处防御性拒绝，
	// 避免任何新调用路径把 verify 降级为 Schema 结构校验。
	if action == "verify" {
		return nil, domain.Fail("INTERNAL", group+" verify 必须经原生语义校验，结构校验入口不接收")
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

func RejectUnportedArgs(args map[string]string, allowed ...string) error {
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

type QuerySources struct {
	ProfileGuidance func(context.Context, string, string, map[string]any) *ProfileGuidanceResult `json:"-" yaml:"-"`
	ProgressionRead func(context.Context, string, string) (map[string]any, error)                `json:"-" yaml:"-"`
}
