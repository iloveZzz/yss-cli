package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"go.yaml.in/yaml/v3"
	"os"
	"path"
	"sort"
	"strings"
)

type WritePlan struct {
	SchemaVersion   int                          `json:"schema_version"`
	ProtocolVersion int                          `json:"protocol_version"`
	Kind            string                       `json:"kind"`
	Root            string                       `json:"root"`
	Action          string                       `json:"action"`
	CheckpointRef   string                       `json:"checkpoint_ref,omitempty"`
	InputRef        string                       `json:"input_ref,omitempty"`
	Options         map[string]string            `json:"options,omitempty"`
	Observed        map[string]domain.Descriptor `json:"observed"`
	Operations      []transaction.Operation      `json:"operations"`
	Scope           string                       `json:"scope"`
	ApprovalCreated bool                         `json:"approval_created"`
	PlanDigest      string                       `json:"plan_digest"`
}

func checkpointFeature(v *kernel.View, ref string) (string, error) {
	layout, e := kernel.ViewWorkLayout(v)
	if e != nil {
		return "", e
	}
	return layout.CheckpointFeature(ref)
}

func jsonBytes(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func pendingOldTransaction(v *kernel.View) error {
	for _, ref := range []string{".yss/asset-transactions/active.json", ".yss/asset-transactions/recovery.lock"} {
		d, err := v.Watch(ref)
		if err != nil {
			return err
		}
		if d.Type != "missing" {
			return domain.Fail("UNPORTED", "存在旧资产事务，需要原运行时恢复后再迁移: "+ref)
		}
	}
	status, err := transaction.Status(v.Root)
	if err != nil {
		return err
	}
	if len(status.Pending) > 0 {
		return domain.Fail("INTERRUPTED", "存在未完成 Go 事务，先 recover")
	}
	return nil
}

func nativePlanDigest(plan WritePlan) (string, error) {
	plan.PlanDigest = ""
	b, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	return safefs.Digest(b), nil
}

func stageDefinition(item kernel.WorkItem, cp string) []byte {
	return []byte(fmt.Sprintf("---\nkind: stage-work-item\nid: %s\ncheckpoint_ref: %s\n---\n# %s\n\n负责人：%s\n\n%s\n\n## 验收\n\n- %s\n\n当前进度、依赖与完成证据以 %s 中的 %s 为准。\n", item.ID, cp, item.Title, item.Owner, item.Scope, strings.Join(item.Acceptance, "\n- "), cp, item.ID))
}

func enableTracker(b []byte) ([]byte, error) {
	fm, body, err := kernel.Frontmatter(b)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err = yaml.Unmarshal(fm, &node); err != nil {
		return nil, err
	}
	if len(node.Content) != 1 {
		return nil, domain.Fail("TRACKER", "tracker frontmatter 根无效")
	}
	top := node.Content[0]
	var t *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "tracker" {
			t = top.Content[i+1]
		}
	}
	if t == nil || t.Kind != yaml.MappingNode {
		return nil, domain.Fail("TRACKER", "tracker 配置缺失")
	}
	found := false
	for i := 0; i+1 < len(t.Content); i += 2 {
		if t.Content[i].Value == "lifecycle_tracking_version" {
			t.Content[i+1].Tag = "!!int"
			t.Content[i+1].Value = "1"
			found = true
		}
	}
	if !found {
		t.Content = append(t.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "lifecycle_tracking_version"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	}
	out, err := yaml.Marshal(&node)
	if err != nil {
		return nil, err
	}
	result := append([]byte("---\n"), out...)
	result = append(result, []byte("---")...)
	return append(result, body...), nil
}

func buildStagePlan(root, action, cpRef, inputRef string, contexts ...context.Context) (WritePlan, error) {
	plan := WritePlan{SchemaVersion: 1, ProtocolVersion: 1, Kind: "stage-tracking-go-plan", Root: root, Action: action, CheckpointRef: cpRef, InputRef: inputRef, Scope: "stage-work-only", Observed: map[string]domain.Descriptor{}, Operations: []transaction.Operation{}}
	ctx := context.Background()
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	s := kernel.NewSemanticSession(ctx, root, nil)
	v := s.V
	if err := kernel.ProjectIdentity(v); err != nil {
		return plan, err
	}
	if err := pendingOldTransaction(v); err != nil {
		return plan, err
	}
	if _, err := v.Read("CONTEXT.md"); err != nil {
		return plan, err
	}
	if _, err := contextContract(root); err != nil {
		return plan, err
	}
	if _, err := v.Watch("CONTEXT.md"); err != nil {
		return plan, err
	}
	if !strings.HasSuffix(cpRef, ".json") {
		return plan, domain.Fail("UNPORTED", "阶段写入要求显式 JSON checkpoint；YAML 仅只读兼容")
	}
	cp, err := v.Document(cpRef)
	if err != nil {
		return plan, domain.Fail("UNPORTED", "需要既有 checkpoint，不从模板推断当前阶段")
	}
	feature, featureRoot, err := kernel.StageFeatureBinding(s, cpRef, cp)
	if err != nil {
		return plan, err
	}
	config, err := kernel.Tracker(v)
	if err != nil {
		return plan, err
	}
	design, _, err := kernel.DesignProfile(v)
	if err != nil {
		return plan, err
	}
	tracking, err := kernel.AsTracking(cp)
	if err != nil {
		return plan, err
	}
	if tracking != nil {
		if _, err = kernel.CheckStage(v, cp, cpRef, s); err != nil {
			return plan, err
		}
	}
	if tracking == nil {
		entry := kernel.TrackingEntry{"parent-ticket", featureRoot + "/parent-ticket.md"}
		if design {
			entry = kernel.TrackingEntry{"checkpoint", cpRef}
		}
		tracking = &kernel.StageTracking{SchemaVersion: 1, FeatureID: feature, CheckpointRef: cpRef, EntryStage: kernel.Text(cp["stage"]), Entry: entry, Items: []kernel.WorkItem{}}
	}
	input, err := v.Read(inputRef)
	if err != nil {
		return plan, err
	}
	value, err := schema.Parse(input)
	if err != nil {
		return plan, err
	}
	if action == "register" {
		seeds, ok := value.([]any)
		if !ok {
			return plan, domain.Fail("INPUT", "register --items 必须是数组")
		}
		for _, seed := range seeds {
			m, ok := kernel.Object(seed)
			if !ok {
				return plan, domain.Fail("INPUT", "工作项 seed 必须是对象")
			}
			allowed := map[string]bool{}
			for _, key := range []string{"id", "title", "owner", "scope", "stage", "work_unit", "acceptance", "dependencies", "source_refs", "progress", "split_reasons", "deferred"} {
				allowed[key] = true
			}
			for key := range m {
				if !allowed[key] {
					return plan, domain.Fail("UNPORTED", "register seed 字段尚未支持: "+key)
				}
			}
			id := kernel.Text(m["id"])
			var existing *kernel.WorkItem
			for i := range tracking.Items {
				if tracking.Items[i].ID == id {
					existing = &tracking.Items[i]
				}
			}
			if existing != nil {
				b, _ := json.Marshal(existing)
				var current map[string]any
				_ = json.Unmarshal(b, &current)
				for _, key := range []string{"title", "owner", "scope", "stage", "work_unit", "acceptance", "dependencies", "split_reasons"} {
					if value, exists := m[key]; exists {
						a, _ := json.Marshal(value)
						b, _ := json.Marshal(current[key])
						if string(a) != string(b) {
							return plan, domain.Fail("TRACKING_CONFLICT", "既有工作项定义不能由旧 seed 覆盖: "+id+"."+key)
						}
					}
				}
				continue
			}
			if p := kernel.Text(m["progress"]); p != "" && p != "pending" {
				return plan, domain.Fail("TRACKING_PROGRESS", "register 不得推断运行或完成")
			}
			copy := map[string]any{}
			for key, value := range m {
				if key != "source_refs" {
					copy[key] = value
				}
			}
			copy["kind"] = "stage-work-item"
			copy["progress"] = "pending"
			copy["completion"] = []any{}
			if copy["dependencies"] == nil {
				copy["dependencies"] = []any{}
			}
			if copy["split_reasons"] == nil {
				copy["split_reasons"] = []any{}
			}
			refs := []kernel.Binding{}
			if sources, present := m["source_refs"]; present {
				list, ok := sources.([]any)
				if !ok {
					return plan, domain.Fail("INPUT", "source_refs 必须是数组")
				}
				for _, source := range list {
					ref := kernel.Text(source)
					if obj, ok := kernel.Object(source); ok {
						ref = kernel.Text(obj["ref"])
					}
					binding, err := v.Bind(ref)
					if err != nil {
						return plan, err
					}
					refs = append(refs, binding)
				}
			}
			copy["source_refs"] = refs
			b, _ := json.Marshal(copy)
			var item kernel.WorkItem
			if err = json.Unmarshal(b, &item); err != nil {
				return plan, err
			}
			if item.Deferred != nil && !kernel.Contains(item.SplitReasons, "cross-stage-deferral") {
				item.SplitReasons = append(item.SplitReasons, "cross-stage-deferral")
			}
			tracking.Items = append(tracking.Items, item)
		}
	} else if action == "update" {
		patch, ok := kernel.Object(value)
		if !ok {
			return plan, domain.Fail("INPUT", "update --item 必须是对象")
		}
		allowed := map[string]bool{}
		for _, key := range []string{"id", "progress", "completion", "cancellation_reason", "source_refs", "recheck_required"} {
			allowed[key] = true
		}
		for key := range patch {
			if !allowed[key] {
				return plan, domain.Fail("UNPORTED", "update 只修改执行状态与明确证据，字段未迁移: "+key)
			}
		}
		id := kernel.Text(patch["id"])
		index := -1
		for i := range tracking.Items {
			if tracking.Items[i].ID == id {
				index = i
			}
		}
		if index < 0 {
			return plan, domain.Fail("TRACKING_ID", "更新工作项未登记: "+id)
		}
		b, _ := json.Marshal(tracking.Items[index])
		var merged map[string]any
		_ = json.Unmarshal(b, &merged)
		for key, value := range patch {
			merged[key] = value
		}
		b, err = json.Marshal(merged)
		if err != nil {
			return plan, err
		}
		if err = json.Unmarshal(b, &tracking.Items[index]); err != nil {
			return plan, err
		}
	} else {
		return plan, domain.Fail("UNPORTED", "不支持的 stage 写入动作")
	}
	for i := range tracking.Items {
		item := &tracking.Items[i]
		for _, other := range tracking.Items {
			if kernel.Contains(other.Dependencies, item.ID) && !kernel.Contains(item.SplitReasons, "blocks-other-work") {
				item.SplitReasons = append(item.SplitReasons, "blocks-other-work")
			}
		}
		if len(tracking.Items) > 0 && item.Owner != tracking.Items[0].Owner && !kernel.Contains(item.SplitReasons, "different-owner") {
			item.SplitReasons = append(item.SplitReasons, "different-owner")
		}
		if len(item.SplitReasons) > 0 && item.DefinitionRef == "" {
			ref := featureRoot + "/work-items/" + item.ID + ".md"
			d, err := v.Watch(ref)
			if err != nil {
				return plan, err
			}
			if d.Type != "missing" {
				return plan, domain.Fail("TRACKING_CONFLICT", "独立定义是人工文件，不覆盖: "+ref)
			}
			data := stageDefinition(*item, cpRef)
			item.DefinitionRef = ref
			item.DefinitionDigest = "sha256:" + safefs.Digest(data)
			if err = v.Set(ref, data); err != nil {
				return plan, err
			}
		}
	}
	if !design {
		entry := tracking.Entry.Ref
		d, err := v.Watch(entry)
		if err != nil {
			return plan, err
		}
		link := "\n阶段工作进度与证据：" + cpRef + "\n"
		if d.Type == "missing" {
			content := "# " + feature + "\n\nStatus: needs-triage\n" + link
			if kernel.Text(config["platform"]) != "local-markdown" {
				content += "\npublication: pending\npending_publication_to: " + kernel.Text(config["platform"]) + "\n"
			}
			if err = v.Set(entry, []byte(content)); err != nil {
				return plan, err
			}
		} else {
			b, err := v.Read(entry)
			if err != nil {
				return plan, err
			}
			if !strings.Contains(string(b), cpRef) {
				if err = v.Set(entry, append(b, []byte(link)...)); err != nil {
					return plan, err
				}
			}
		}
	}
	mapRef := featureRoot + "/map.md"
	d, err := v.Watch(mapRef)
	if err != nil {
		return plan, err
	}
	if d.Type == "missing" {
		if err = v.Set(mapRef, []byte("---\ncheckpoint_ref: "+cpRef+"\n---\n# "+feature+"\n\n阶段工作与证据见 "+cpRef+"。\n")); err != nil {
			return plan, err
		}
	} else {
		b, err := v.Read(mapRef)
		if err != nil {
			return plan, err
		}
		if !strings.Contains(string(b), cpRef) {
			if err = v.Set(mapRef, append(b, []byte("\n阶段工作与证据："+cpRef+"\n")...)); err != nil {
				return plan, err
			}
		}
	}
	cp["stage_tracking"] = tracking
	b, err := jsonBytes(cp)
	if err != nil {
		return plan, err
	}
	if err = v.Set(cpRef, b); err != nil {
		return plan, err
	}
	trackerBytes, err := v.Read(kernel.TrackerRef)
	if err != nil {
		return plan, err
	}
	enabled, err := enableTracker(trackerBytes)
	if err != nil {
		return plan, err
	}
	if err = v.Set(kernel.TrackerRef, enabled); err != nil {
		return plan, err
	}
	// Normalize typed values before calling the same Schema compiler as file validation.
	normalized, err := schema.Parse(b)
	if err != nil {
		return plan, err
	}
	cp, _ = kernel.Object(normalized)
	if _, err = kernel.CheckStage(v, cp, cpRef, s); err != nil {
		return plan, err
	}
	if err = s.Finish(); err != nil {
		return plan, err
	}
	for ref, descriptor := range v.Observed {
		// The transaction manager owns the journal and pending exclusion under
		// its lock; its preparation must not invalidate the stage plan itself.
		if ref != ".yss/transactions" && !strings.HasPrefix(ref, ".yss/transactions/") {
			plan.Observed[ref] = descriptor
		}
	}
	plan.Operations, err = v.Ops()
	if err != nil {
		return plan, err
	}
	if scans := progressionScans(s, root, ""); len(scans) > 0 {
		data, err := json.Marshal(scans)
		if err != nil {
			return plan, err
		}
		plan.Options = map[string]string{"stage_scan_inputs": string(data)}
	}
	plan.PlanDigest, err = nativePlanDigest(plan)
	return plan, err
}

// Transaction postconditions permit only their own files and newly created
// parent directories. Every other registration member remains a locked input.
func validateStagePlanScans(ctx context.Context, root string, plan WritePlan) error {
	if plan.Options["stage_scan_inputs"] == "" {
		return nil
	}
	var scans []progressionScanInput
	if err := json.Unmarshal([]byte(plan.Options["stage_scan_inputs"]), &scans); err != nil {
		return err
	}
	for _, scan := range scans {
		if scan.Root != root {
			return domain.Fail("PLAN", "阶段登记扫描不属于当前工程")
		}
		beforeDirs := map[string]bool{}
		for _, member := range scan.Files {
			if strings.HasPrefix(member, "dir:") {
				body := strings.TrimPrefix(member, "dir:")
				beforeDirs[body[:strings.LastIndex(body, ":")]] = true
			}
		}
		written, newParents := map[string]bool{}, map[string]bool{}
		for _, op := range plan.Operations {
			written["file:"+op.Path] = true
			for dir := path.Dir(op.Path); dir != "."; dir = path.Dir(dir) {
				if !beforeDirs[dir] {
					newParents[dir] = true
				}
			}
		}
		filter := func(files []string) []string {
			result := []string{}
			for _, member := range files {
				if written[member] {
					continue
				}
				if strings.HasPrefix(member, "dir:") {
					body := strings.TrimPrefix(member, "dir:")
					if newParents[body[:strings.LastIndex(body, ":")]] {
						continue
					}
				}
				result = append(result, member)
			}
			return result
		}
		s := kernel.NewSemanticSession(ctx, root, nil)
		files, err := s.ScanFresh(scan.Ref, false)
		if err != nil {
			return err
		}
		if !kernel.EqualStrings(filter(files), filter(scan.Files)) {
			return domain.Fail("INPUT_DRIFT", "阶段写入期间功能登记文件集合变化")
		}
	}
	return nil
}

func stageRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	ref := kernel.First(args["checkpoint"], args["file"], args["arg0"])
	if action == "query" || action == "status" || action == "check" {
		if args["stage"] != "" || args["work-unit"] != "" {
			return nil, domain.Fail("UNPORTED", "checkpoint 工作项查询不支持 --stage/--work-unit 过滤")
		}
		s := kernel.NewSemanticSession(ctx, root, args)
		v := s.V
		if err := pendingOldTransaction(v); err != nil {
			return nil, err
		}
		cp, err := s.Doc(ref)
		if err != nil {
			return nil, err
		}
		result, err := kernel.CheckStage(v, cp, ref, s)
		if err != nil {
			return nil, err
		}
		if err = s.Finish(); err != nil {
			return nil, err
		}
		if id := args["id"]; id != "" {
			tracking, err := kernel.AsTracking(cp)
			if err != nil {
				return nil, err
			}
			if tracking != nil {
				for _, item := range tracking.Items {
					if item.ID == id {
						result["item"] = item
						return result, nil
					}
				}
			}
			return nil, domain.Fail("TRACKING_ID", "阶段工作项未登记: "+id)
		}
		return result, nil
	}
	if action != "register" && action != "update" && action != "plan" && action != "apply" {
		return nil, domain.Fail("UNPORTED", "不支持的 stage 动作: "+action)
	}
	if action == "apply" || args["apply"] == "true" {
		if args["refresh"] != "" {
			return nil, domain.Fail("UNPORTED", "refresh 不能随 apply 忽略")
		}
		planRef := args["plan-file"]
		if planRef == "" {
			return nil, domain.Fail("PLAN_REQUIRED", "写入必须消费 --plan-file")
		}
		p, err := safefs.Path(root, planRef)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if _, err = schema.Parse(b); err != nil {
			return nil, err
		}
		var plan WritePlan
		if err = json.Unmarshal(b, &plan); err != nil {
			return nil, err
		}
		if plan.Kind != "stage-tracking-go-plan" || plan.SchemaVersion != 1 || plan.ProtocolVersion != 1 || plan.Root != root {
			return nil, domain.Fail("UNPORTED", "旧或未知 stage 计划不能由 Go 直接应用")
		}
		if action != "apply" && action != plan.Action {
			return nil, domain.Fail("PLAN", "调用动作与计划不一致")
		}
		digest, err := nativePlanDigest(plan)
		if err != nil || digest != plan.PlanDigest {
			return nil, domain.Fail("PLAN", "计划摘要不一致")
		}
		fresh, err := buildStagePlan(root, plan.Action, plan.CheckpointRef, plan.InputRef, ctx)
		if err != nil {
			return nil, err
		}
		if fresh.PlanDigest != plan.PlanDigest {
			changed := []string{}
			for ref, before := range plan.Observed {
				if now, ok := fresh.Observed[ref]; !ok || before != now {
					changed = append(changed, ref)
				}
			}
			for ref := range fresh.Observed {
				if _, ok := plan.Observed[ref]; !ok {
					changed = append(changed, ref)
				}
			}
			sort.Strings(changed)
			return nil, domain.Explain(domain.Fail("INPUT_DRIFT", "计划已过期，重新登记计划"), "SAVED_STAGE_PLAN_CHANGED", "阶段计划绑定的输入已变化，需重新登记并保存计划。", map[string]any{"root": root, "checkpoint": plan.CheckpointRef, "file": plan.InputRef, "planAction": plan.Action, "affectedInputs": changed})
		}
		var validate func() error
		if fresh.Options["stage_scan_inputs"] != "" {
			validate = func() error { return validateStagePlanScans(ctx, root, fresh) }
			if err = validate(); err != nil {
				return nil, err
			}
		}
		tx, err := transaction.ApplyContextWithValidation(ctx, root, "stage-"+plan.Action, plan.Operations, plan.Observed, nil, validate)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": tx.Status, "source_operation": tx.Status, "reading_update": stageReadingUpdate(ctx, root, plan.CheckpointRef), "transaction": tx, "scope": "stage-work-only", "approval_created": false, "ready_for_agent_created": false, "execution_authorization": "not-evaluated"}, nil
	}
	if args["refresh"] != "" {
		return nil, domain.Fail("UNPORTED", "refresh 保留历史并重置执行态的语义尚未迁移")
	}
	if action == "plan" {
		action = "register"
	}
	input := kernel.First(args["items"], args["item"])
	if input == "" {
		return nil, domain.Fail("INPUT", "register/update 需要 --items/--item")
	}
	return buildStagePlan(root, action, ref, input, ctx)
}
