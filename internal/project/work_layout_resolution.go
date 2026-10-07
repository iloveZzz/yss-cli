package project

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// Reuse saved-plan resolution binding; candidates supply actual new evidence.
// This planner never synthesizes approval, decisions or verification results.
func BuildWorkLayoutWithOptions(root, profile string, o PlanningOptions) (*Plan, error) {
	p, e := BuildWorkLayout(root, profile)
	if e != nil {
		return nil, e
	}
	if o.BaseBundlePath != "" {
		return nil, domain.Fail("ARGUMENT", "目录迁移不升级模板")
	}
	if o.ResolutionFile == "" && len(o.Resolutions) == 0 {
		return p, nil
	}
	origin, resolutions := o.ResolvedFrom, o.Resolutions
	if o.ResolutionFile != "" {
		if o.Original == nil || o.Original.MigrationKind != "work-layout" || o.Original.Digest != planDigest(o.Original) || o.Original.ResolvedFrom != "" {
			return nil, domain.Fail("PLAN_REQUIRED", "决议必须绑定原始目录迁移计划")
		}
		origin = o.Original.Digest
		file, e := ReadResolutionFile(o.ResolutionFile)
		if e != nil {
			return nil, e
		}
		if file.PlanDigest != origin {
			return nil, domain.Fail("RESOLUTION_STALE", "决议计划摘要改变")
		}
		resolutions = file.Decisions
	}
	if origin != p.Digest {
		return nil, domain.Fail("RESOLUTION_STALE", "计划输入或目录映射改变；重新规划")
	}
	assets := map[string]AssetResult{}
	for _, a := range p.Assets {
		assets[a.Path] = a
	}
	seen := map[string]bool{}
	for i, r := range resolutions {
		a, ok := assets[r.Path]
		if !ok || seen[r.Path] {
			return nil, domain.Fail("RESOLUTION", "路径重复或不在迁移范围")
		}
		seen[r.Path] = true
		if e = validateResolution(a, r); e != nil {
			return nil, e
		}
		raw, e := resolutionBytes(r)
		if e != nil {
			return nil, e
		}
		r.CandidateData = base64.StdEncoding.EncodeToString(raw)
		resolutions[i] = r
	}
	sort.Slice(resolutions, func(i, j int) bool { return resolutions[i].Path < resolutions[j].Path })
	return buildWorkLayout(root, profile, resolutions, origin)
}

func workRebindingBytes(ref string, before, after []byte) error {
	old, e := parseWorkDocument(before, filepath.Ext(ref))
	if e != nil {
		return e
	}
	if old == nil && workProtectedPath(ref) {
		return domain.Fail("IMMUTABLE", "原始消息/批准正文不可改写；须使用新的结构化当前证据")
	}
	if old == nil || !workProtected(old.value) && !workProtectedPath(ref) {
		return nil
	}
	current, e := parseWorkDocument(after, filepath.Ext(ref))
	if e != nil {
		return e
	}
	if current == nil || bytes.Equal(before, after) {
		return domain.Fail("APPROVAL_REBIND_REQUIRED", "不可复用原批准字节作为新当前记录")
	}
	a, _ := old.value.(map[string]any)
	b, _ := current.value.(map[string]any)
	if a["kind"] == "user-decision" {
		oldRequest, _ := a["request"].(map[string]any)
		newRequest, _ := b["request"].(map[string]any)
		if newRequest["id"] == nil || newRequest["id"] == oldRequest["id"] {
			return domain.Fail("APPROVAL_REBIND_REQUIRED", "用户决定必须来自新的展示请求和真实回复")
		}
	}
	if a["gate_id"] != nil {
		if b["gate_id"] != a["gate_id"] || b["decision"] != "approved" {
			return domain.Fail("APPROVAL_REBIND_REQUIRED", "当前会签须绑定同一门禁与实际新证据")
		}
		if b["user_decision_ref"] == a["user_decision_ref"] && b["review_task_ref"] == a["review_task_ref"] && b["continuation_ref"] == a["continuation_ref"] {
			return domain.Fail("APPROVAL_REBIND_REQUIRED", "仅修改 subject 路径或摘要不能延续批准")
		}
	}
	if workContainsCurrentSource(current.value, "") || strings.Contains(string(current.body), "docs/.scratch/") {
		return domain.Fail("APPROVAL_REBIND_REQUIRED", "新证据仍引用旧当前工作包")
	}
	return nil
}

func workContainsCurrentSource(value any, key string) bool {
	if workHistoricalKey(key) {
		return false
	}
	switch v := value.(type) {
	case string:
		return strings.Contains(v, "docs/.scratch/")
	case []any:
		for _, x := range v {
			if workContainsCurrentSource(x, key) {
				return true
			}
		}
	case map[string]any:
		for k, x := range v {
			if workContainsCurrentSource(x, k) {
				return true
			}
		}
	}
	return false
}
