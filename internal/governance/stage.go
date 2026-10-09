package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"go.yaml.in/yaml/v3"
)

const trackerRef = ".template-spec/agents/issue-tracker.md"
const trackingSchemaRef = ".template-spec/process/schemas/stage-tracking.schema.json"

// This is the same consumer mapping as the retained stage-tracking contract, including its design override.
var trackedUnits = map[string]string{
	"work-unit.plan-opportunity": "stage.plan", "work-unit.plan-requirements": "stage.plan",
	"work-unit.domain-strategy-design": "stage.plan", "work-unit.stage-decision": "stage.plan",
	"work-unit.spec-synthesis": "stage.spec-architecture", "work-unit.prototype-design-v2": "stage.product-design",
	"work-unit.prototype-design": "stage.product-design", "work-unit.business-ticket-formalization": "stage.product-design",
	"work-unit.strategic-design-handoff": "stage.product-design",
}

type Binding struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}
type Completion struct {
	Criterion    string    `json:"criterion"`
	EvidenceRefs []Binding `json:"evidence_refs"`
}
type Deferral struct {
	Owner            string `json:"owner"`
	ResolveBy        string `json:"resolve_by"`
	Receiver         string `json:"receiver"`
	VerificationPlan string `json:"verification_plan"`
	DecisionRef      string `json:"decision_ref"`
	Risk             string `json:"risk"`
	FollowUpRef      string `json:"follow_up_ref"`
}
type WorkItem struct {
	ID                 string       `json:"id"`
	Kind               string       `json:"kind"`
	Title              string       `json:"title"`
	Stage              string       `json:"stage"`
	WorkUnit           string       `json:"work_unit"`
	Owner              string       `json:"owner"`
	Scope              string       `json:"scope"`
	Acceptance         []string     `json:"acceptance"`
	Dependencies       []string     `json:"dependencies"`
	SourceRefs         []Binding    `json:"source_refs"`
	Progress           string       `json:"progress"`
	SplitReasons       []string     `json:"split_reasons"`
	Completion         []Completion `json:"completion"`
	DefinitionRef      string       `json:"definition_ref,omitempty"`
	DefinitionDigest   string       `json:"definition_digest,omitempty"`
	RecheckRequired    bool         `json:"recheck_required,omitempty"`
	CancellationReason string       `json:"cancellation_reason,omitempty"`
	Deferred           *Deferral    `json:"deferred,omitempty"`
}
type TrackingEntry struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}
type StageTracking struct {
	SchemaVersion int           `json:"schema_version"`
	FeatureID     string        `json:"feature_id"`
	CheckpointRef string        `json:"checkpoint_ref"`
	EntryStage    string        `json:"entry_stage"`
	Entry         TrackingEntry `json:"entry"`
	Items         []WorkItem    `json:"items"`
}
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

type view struct {
	root         string
	overlay      map[string][]byte
	observed     map[string]domain.Descriptor
	virtual      map[string]semanticArchiveFile
	bindIdentity bool
	identities   map[string]os.FileInfo
}

func newView(root string) *view {
	return &view{root: root, overlay: map[string][]byte{}, observed: map[string]domain.Descriptor{}}
}
func (v *view) watch(ref string) (domain.Descriptor, error) {
	var identity os.FileInfo
	if v.bindIdentity && v.virtual == nil {
		p, err := safefs.Path(v.root, ref)
		if err != nil {
			return domain.Descriptor{}, err
		}
		identity, err = os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return domain.Descriptor{}, err
		}
		if old, seen := v.identities[ref]; seen && !sameObservedIdentity(old, identity) {
			return domain.Descriptor{}, domain.Fail("INPUT_DRIFT", "核验阶段文件身份变化: "+ref)
		}
	}
	d, err := v.describe(ref)
	if err != nil {
		return d, err
	}
	if old, ok := v.observed[ref]; ok && old != d {
		return d, domain.Fail("INPUT_DRIFT", "核验阶段输入变化: "+ref)
	}
	if v.bindIdentity && v.virtual == nil {
		p, err := safefs.Path(v.root, ref)
		if err != nil {
			return d, err
		}
		after, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return d, err
		}
		if !sameObservedIdentity(identity, after) {
			return d, domain.Fail("INPUT_DRIFT", "读取期间文件身份变化: "+ref)
		}
		if v.identities == nil {
			v.identities = map[string]os.FileInfo{}
		}
		v.identities[ref] = identity
	}
	v.observed[ref] = d
	return d, nil
}

func sameObservedIdentity(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func (v *view) read(ref string) ([]byte, error) {
	if b, ok := v.overlay[ref]; ok {
		return b, nil
	}
	d, err := v.watch(ref)
	if err != nil {
		return nil, err
	}
	if d.Type != "file" {
		return nil, domain.Fail("INPUT", "输入不可读: "+ref)
	}
	if v.virtual != nil {
		return append([]byte(nil), v.virtual[ref].Data...), nil
	}
	p, err := safefs.Path(v.root, ref)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err == nil && safefs.Digest(b) != d.Digest {
		return nil, domain.Fail("INPUT_DRIFT", "读取期间输入变化: "+ref)
	}
	if err == nil && v.bindIdentity {
		after, statErr := os.Lstat(p)
		if statErr != nil || !sameObservedIdentity(v.identities[ref], after) {
			return nil, domain.Fail("INPUT_DRIFT", "读取返回前文件身份变化: "+ref)
		}
	}
	return b, err
}
func (v *view) document(ref string) (map[string]any, error) {
	b, err := v.read(ref)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(ref, ".md") {
		b, _, err = frontmatter(b)
		if err != nil {
			return nil, err
		}
	}
	data, err := schema.Parse(b)
	if err != nil {
		return nil, err
	}
	m, ok := object(data)
	if !ok {
		return nil, domain.Fail("INPUT", "治理资产必须是对象: "+ref)
	}
	return m, nil
}
func (v *view) bind(ref string) (Binding, error) {
	b, err := v.read(ref)
	if err != nil {
		return Binding{}, err
	}
	return Binding{ref, "sha256:" + safefs.Digest(b)}, nil
}
func (v *view) set(ref string, data []byte) error {
	if v.virtual != nil {
		return domain.Fail("READ_ONLY", "归档来源视图不允许写入")
	}
	if _, err := v.watch(ref); err != nil {
		return err
	}
	v.overlay[ref] = data
	return nil
}
func (v *view) ops() ([]transaction.Operation, error) {
	refs := []string{}
	for ref := range v.overlay {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	ops := []transaction.Operation{}
	for _, ref := range refs {
		d := v.observed[ref]
		b := v.overlay[ref]
		if d.Type == "file" && d.Digest == safefs.Digest(b) {
			continue
		}
		var before *domain.Descriptor
		mode := uint32(0644)
		if d.Type == "file" {
			copy := d
			before = &copy
			mode = d.Mode
		}
		ops = append(ops, transaction.Operation{Path: ref, Data: b, Mode: mode, Before: before})
	}
	return ops, nil
}

func frontmatter(b []byte) ([]byte, []byte, error) {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, nil, domain.Fail("TRACKER", "治理 Markdown 必须有 frontmatter")
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return nil, nil, domain.Fail("TRACKER", "frontmatter 不完整")
	}
	end += 4
	tail := end + 4
	if tail < len(s) && s[tail] != '\n' {
		return nil, nil, domain.Fail("TRACKER", "frontmatter 终止符非法")
	}
	return []byte(s[4:end]), []byte(s[tail:]), nil
}
func tracker(v *view, sessions ...*semanticSession) (map[string]any, error) {
	read := v.document
	if len(sessions) > 0 {
		read = sessions[0].doc
	}
	m, err := read(trackerRef)
	if err != nil {
		return nil, err
	}
	t, ok := object(m["tracker"])
	if !ok || !(text(t["platform"]) == "local-markdown" || text(t["platform"]) == "github" || text(t["platform"]) == "gitlab") {
		return nil, domain.Fail("TRACKER", "主 tracker 配置无效")
	}
	if version, exists := t["lifecycle_tracking_version"]; exists {
		n, ok := integer(version)
		if !ok || n != 1 {
			return nil, domain.Fail("TRACKER", "未知阶段追踪版本")
		}
	}
	if text(t["root"]) == "" {
		return nil, domain.Fail("TRACKER", "tracker.root 缺失")
	}
	if _, err = safefs.Path(v.root, strings.TrimSuffix(text(t["root"]), "/")); err != nil {
		return nil, err
	}
	return t, nil
}
func projectIdentity(v *view, sessions ...*semanticSession) error {
	read := v.document
	if len(sessions) > 0 {
		read = sessions[0].doc
	}
	m, err := read("yss-project.yaml")
	if err != nil {
		return err
	}
	version, ok := integer(m["schema_version"])
	if !ok || version != 1 || text(m["repository_mode"]) != "project-instance" {
		return domain.Fail("IDENTITY", "stage/project-ci 仅适用于 project-instance")
	}
	return nil
}
func designProfile(v *view, sessions ...*semanticSession) (bool, []string, error) {
	ref := ".template-spec/process/harness-profile.yaml"
	read := v.document
	if len(sessions) > 0 {
		ref = sessions[0].localRef(ref)
		read = sessions[0].doc
	}
	d, err := v.watch(ref)
	if err != nil {
		return false, nil, err
	}
	if d.Type == "missing" {
		return false, nil, nil
	}
	m, err := read(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return false, nil, err
	}
	allowed := m["allowed_work_units"]
	if lifecycle, ok := object(m["lifecycle"]); ok && lifecycle["allowed_work_units"] != nil {
		allowed = lifecycle["allowed_work_units"]
	}
	list := []string(nil)
	if allowed != nil {
		values, ok := allowed.([]any)
		if !ok {
			return false, nil, domain.Fail("PROFILE", "allowed_work_units 必须是数组")
		}
		list = []string{}
		for _, id := range values {
			s, ok := id.(string)
			if !ok {
				return false, nil, domain.Fail("PROFILE", "allowed_work_units 必须是字符串数组")
			}
			list = append(list, s)
		}
	}
	return text(m["profile_id"]) == "harness.business-ddd-strategy-handoff", list, nil
}
func viewWorkLayout(v *view) (*worklayout.Layout, error) {
	config, e := tracker(v)
	if e != nil {
		return nil, e
	}
	return worklayout.New(v.root, config)
}
func checkpointFeature(v *view, ref string) (string, error) {
	layout, e := viewWorkLayout(v)
	if e != nil {
		return "", e
	}
	return layout.CheckpointFeature(ref)
}

// A registered canonical feature ID and its directory are separate facts. Older
// instances retain their existing path identity until they adopt this policy.
func stageFeatureBinding(s *semanticSession, ref string, cp map[string]any) (string, string, error) {
	if err := s.guard(); err != nil {
		return "", "", err
	}
	if strings.HasPrefix(text(cp["feature_id"]), "feature.") {
		present, err := s.exists(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return "", "", err
		}
		if present {
			profileDoc, err := s.doc(".template-spec/process/harness-profile.yaml")
			if err != nil {
				return "", "", err
			}
			profile := ""
			for name, known := range domain.Profiles {
				if profileDoc["profile_id"] == known.ID {
					profile = name
				}
			}
			if profile == "" {
				return "", "", s.unavailable("IDENTITY", "未知功能登记 Profile")
			}
			contractRef := guidanceContractRef(profile)
			present, err = s.exists(contractRef)
			if err != nil {
				return "", "", err
			}
			if present {
				contract, err := s.doc(contractRef)
				if err != nil {
					return "", "", err
				}
				if _, declared := contract["progression_target"]; declared {
					if _, _, err = progressionPolicy(s); err != nil {
						return "", "", err
					}
					configRef, registered, err := progressionLocation(s, ref)
					if err != nil {
						return "", "", err
					}
					if registered["feature_id"] != cp["feature_id"] {
						return "", "", s.reject("TRACKING_FEATURE", "当前 checkpoint 功能身份与登记不一致")
					}
					return text(cp["feature_id"]), path.Dir(configRef), nil
				}
			}
		}
	}
	config, err := tracker(s.v, s)
	if err != nil {
		return "", "", err
	}
	layout, err := worklayout.New(s.root, config)
	if err != nil {
		return "", "", err
	}
	feature, err := layout.CheckpointFeature(ref)
	if err != nil {
		return "", "", err
	}
	if text(cp["feature_id"]) != feature {
		return "", "", s.reject("TRACKING_FEATURE", "checkpoint 功能身份与路径不一致")
	}
	base, err := layout.FeatureRoot(feature)
	return feature, base, err
}
func asTracking(cp map[string]any) (*StageTracking, error) {
	if cp["stage_tracking"] == nil {
		return nil, nil
	}
	b, err := json.Marshal(cp["stage_tracking"])
	if err != nil {
		return nil, err
	}
	var t StageTracking
	if err = json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
func contains(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}
func jsonBytes(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
func pendingOldTransaction(v *view) error {
	for _, ref := range []string{".yss/asset-transactions/active.json", ".yss/asset-transactions/recovery.lock"} {
		d, err := v.watch(ref)
		if err != nil {
			return err
		}
		if d.Type != "missing" {
			return domain.Fail("UNPORTED", "存在旧资产事务，需要原运行时恢复后再迁移: "+ref)
		}
	}
	status, err := transaction.Status(v.root)
	if err != nil {
		return err
	}
	if len(status.Pending) > 0 {
		return domain.Fail("INTERRUPTED", "存在未完成 Go 事务，先 recover")
	}
	return nil
}

func checkStage(v *view, cp map[string]any, ref string, sessions ...*semanticSession) (map[string]any, error) {
	s := newSemanticSession(context.Background(), v.root, nil)
	s.v = v
	if len(sessions) > 0 {
		s = sessions[0]
	}
	if err := projectIdentity(v, s); err != nil {
		return nil, err
	}
	config, err := tracker(v, s)
	if err != nil {
		return nil, err
	}
	feature, featureRoot, err := stageFeatureBinding(s, ref, cp)
	if err != nil {
		return nil, err
	}
	t, err := asTracking(cp)
	if err != nil {
		return nil, err
	}
	if t == nil {
		enabled, _ := integer(config["lifecycle_tracking_version"])
		if enabled == 1 && (text(cp["stage"]) == "stage.plan" || text(cp["stage"]) == "stage.spec-architecture" || text(cp["stage"]) == "stage.product-design") {
			return nil, domain.Fail("TRACKING_REQUIRED", "当前阶段缺少 stage_tracking")
		}
		return map[string]any{"status": "not-applicable", "stale_item_ids": []string{}, "scope": "stage-work-only", "read_only": true, "approval_created": false}, nil
	}
	sp, err := safefs.Path(v.root, trackingSchemaRef)
	if err != nil {
		return nil, err
	}
	if _, err = s.bytes(trackingSchemaRef); err != nil {
		return nil, err
	}
	// The current stage Schema is standalone. Refuse a future external closure until every
	// referenced Schema can be included in the write plan's locked input guards.
	stageSchema, err := s.doc(trackingSchemaRef)
	if err != nil {
		return nil, err
	}
	var externalRefs func(any) bool
	externalRefs = func(value any) bool {
		if m, ok := object(value); ok {
			for key, child := range m {
				if key == "$ref" || key == "$dynamicRef" {
					if s, ok := child.(string); ok && !strings.HasPrefix(s, "#") {
						return true
					}
				}
				if externalRefs(child) {
					return true
				}
			}
		} else if list, ok := value.([]any); ok {
			for _, child := range list {
				if externalRefs(child) {
					return true
				}
			}
		}
		return false
	}
	if externalRefs(stageSchema) {
		return nil, domain.Fail("UNPORTED", "stage Schema 的外部引用尚未绑定事务输入闭包")
	}
	issues, err := schema.ValidateValueWithReader(sp, cp["stage_tracking"], func(file string) ([]byte, error) {
		ref, err := filepath.Rel(s.root, file)
		if err != nil {
			return nil, err
		}
		return s.bytes(filepath.ToSlash(ref))
	})
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, domain.Fail("TRACKING_SCHEMA", fmt.Sprintf("阶段追踪 Schema 不通过: %v", issues))
	}
	if t.SchemaVersion != 1 || t.FeatureID != feature || t.CheckpointRef != ref {
		return nil, domain.Fail("TRACKING_IDENTITY", "stage_tracking 身份、版本或 checkpoint 引用不一致")
	}
	design, allowed, err := designProfile(v, s)
	if err != nil {
		return nil, err
	}
	base := featureRoot + "/"
	if design {
		if t.Entry.Kind != "checkpoint" || t.Entry.Ref != ref {
			return nil, domain.Fail("TRACKING_ENTRY", "design Profile 必须使用 checkpoint 入口")
		}
	} else if t.Entry.Kind != "parent-ticket" || t.Entry.Ref != base+"parent-ticket.md" {
		return nil, domain.Fail("TRACKING_ENTRY", "阶段追踪必须引用本功能 parent-ticket")
	}
	entry, err := s.bytes(t.Entry.Ref)
	if err != nil {
		return nil, err
	}
	if t.Entry.Kind == "parent-ticket" && !strings.Contains(string(entry), ref) {
		return nil, domain.Fail("TRACKING_ENTRY", "parent-ticket 缺少 checkpoint 引用")
	}
	r, err := s.doc(".template-spec/process/lifecycle-registry.yaml")
	if err != nil {
		return nil, err
	}
	kind, _, ok := findRegistry(r, t.EntryStage)
	if !ok || kind != "stages" {
		return nil, domain.Fail("TRACKING_STAGE", "入口阶段未登记")
	}
	index := map[string]*WorkItem{}
	stale := map[string]bool{}
	for i := range t.Items {
		item := &t.Items[i]
		if index[item.ID] != nil {
			return nil, domain.Fail("TRACKING_ID", "工作项 ID 重复: "+item.ID)
		}
		index[item.ID] = item
		kind, _, ok = findRegistry(r, item.Stage)
		unitKind, _, unitOK := findRegistry(r, item.WorkUnit)
		expected := trackedUnits[item.WorkUnit]
		if design && (item.WorkUnit == "work-unit.business-ticket-formalization" || item.WorkUnit == "work-unit.strategic-design-handoff") {
			expected = "stage.ticket-formalization"
		}
		if !ok || kind != "stages" || !unitOK || unitKind != "work_units" || expected != item.Stage {
			return nil, domain.Fail("TRACKING_ROUTE", "工作项阶段与工作单元不匹配: "+item.ID)
		}
		if allowed != nil && !contains(allowed, item.WorkUnit) {
			return nil, domain.Fail("TRACKING_PROFILE", "Profile 禁止当前工作单元: "+item.ID)
		}
		if len(item.SplitReasons) > 0 && item.DefinitionRef == "" {
			return nil, domain.Fail("TRACKING_DEFINITION", "拆分工作项必须持有独立定义: "+item.ID)
		}
		if item.DefinitionRef != "" {
			if item.DefinitionRef != base+"work-items/"+item.ID+".md" {
				return nil, domain.Fail("TRACKING_DEFINITION", "独立定义路径非法")
			}
			b, err := s.bytes(item.DefinitionRef)
			if err != nil {
				return nil, err
			}
			s := string(b)
			if !strings.Contains(s, "kind: stage-work-item") || !strings.Contains(s, "id: "+item.ID+"\n") || !strings.Contains(s, ref) || regexp.MustCompile(`ready-for-agent|(?m)^Status:|^progress:`).MatchString(s) {
				return nil, domain.Fail("TRACKING_DEFINITION", "独立定义包含非法状态或缺少身份")
			}
			if item.DefinitionDigest != "sha256:"+safefs.Digest(b) {
				return nil, domain.Fail("TRACKING_DEFINITION", "独立定义摘要漂移")
			}
		}
		if item.Deferred != nil {
			if !contains(item.SplitReasons, "cross-stage-deferral") || item.DefinitionRef == "" || item.Progress == "completed" || item.Progress == "cancelled" {
				return nil, domain.Fail("TRACKING_DEFERRAL", "延期工作项必须独立，且不能标记完成或取消")
			}
			at, err := time.Parse(time.RFC3339Nano, item.Deferred.ResolveBy)
			if err != nil || !at.After(time.Now()) {
				return nil, domain.Fail("TRACKING_DEFERRAL", "延期已过期或日期非法")
			}
			if _, err = s.bytes(item.Deferred.DecisionRef); err != nil {
				return nil, err
			}
		}
		if item.Progress == "cancelled" && strings.TrimSpace(item.CancellationReason) == "" {
			return nil, domain.Fail("TRACKING_CANCEL", "取消工作项必须记录原因")
		}
		if item.Progress == "completed" {
			if len(item.SourceRefs) == 0 || item.RecheckRequired {
				return nil, domain.Fail("TRACKING_COMPLETION", "完成工作项缺少来源或仍要求重验")
			}
			for _, criterion := range item.Acceptance {
				found := false
				for _, c := range item.Completion {
					if c.Criterion == criterion && len(c.EvidenceRefs) > 0 {
						found = true
					}
				}
				if !found {
					return nil, domain.Fail("TRACKING_COMPLETION", "验收准则缺少证据: "+criterion)
				}
			}
			for _, c := range item.Completion {
				if !contains(item.Acceptance, c.Criterion) {
					return nil, domain.Fail("TRACKING_COMPLETION", "未登记的验收准则")
				}
			}
		}
		bindings := append([]Binding{}, item.SourceRefs...)
		for _, c := range item.Completion {
			bindings = append(bindings, c.EvidenceRefs...)
		}
		for _, binding := range bindings {
			if _, err := safefs.Path(v.root, binding.Ref); err != nil {
				return nil, err
			}
			raw, err := s.bytes(binding.Ref)
			if err != nil || "sha256:"+safefs.Digest(raw) != binding.Digest {
				stale[item.ID] = true
			}
		}
	}
	visited, visiting := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return domain.Fail("TRACKING_DEPENDENCY", "工作项依赖成环")
		}
		if visited[id] {
			return nil
		}
		item := index[id]
		if item == nil {
			return domain.Fail("TRACKING_DEPENDENCY", "依赖未登记: "+id)
		}
		visiting[id] = true
		for _, dep := range item.Dependencies {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range index {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	changed := true
	for changed {
		changed = false
		for _, item := range t.Items {
			for _, dep := range item.Dependencies {
				if stale[dep] && !stale[item.ID] {
					stale[item.ID] = true
					changed = true
				}
			}
		}
	}
	for _, item := range t.Items {
		dependent := false
		for _, other := range t.Items {
			if contains(other.Dependencies, item.ID) {
				dependent = true
			}
		}
		if len(t.Items) > 0 && (item.Owner != t.Items[0].Owner || dependent) && item.DefinitionRef == "" {
			return nil, domain.Fail("TRACKING_DEFINITION", "跨负责人或阻塞其他工作的项必须独立")
		}
		if item.Progress == "running" || item.Progress == "completed" {
			for _, dep := range item.Dependencies {
				if index[dep].Progress != "completed" {
					return nil, domain.Fail("TRACKING_DEPENDENCY", "前置工作尚未完成: "+item.ID)
				}
			}
			if stale[item.ID] {
				return nil, domain.Fail("TRACKING_STALE", "来源或证据漂移，拒绝运行/完成: "+item.ID)
			}
		}
	}
	staleIDs := []string{}
	for id := range stale {
		staleIDs = append(staleIDs, id)
	}
	sort.Strings(staleIDs)
	return map[string]any{"status": "valid", "stage_tracking": t, "stale_item_ids": staleIDs, "scope": "stage-work-only", "read_only": true, "approval_created": false, "execution_authorization": "not-evaluated"}, nil
}

func nativePlanDigest(plan WritePlan) (string, error) {
	plan.PlanDigest = ""
	b, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	return safefs.Digest(b), nil
}
func stageDefinition(item WorkItem, cp string) []byte {
	return []byte(fmt.Sprintf("---\nkind: stage-work-item\nid: %s\ncheckpoint_ref: %s\n---\n# %s\n\n负责人：%s\n\n%s\n\n## 验收\n\n- %s\n\n当前进度、依赖与完成证据以 %s 中的 %s 为准。\n", item.ID, cp, item.Title, item.Owner, item.Scope, strings.Join(item.Acceptance, "\n- "), cp, item.ID))
}
func enableTracker(b []byte) ([]byte, error) {
	fm, body, err := frontmatter(b)
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
	s := newSemanticSession(ctx, root, nil)
	v := s.v
	if err := projectIdentity(v); err != nil {
		return plan, err
	}
	if err := pendingOldTransaction(v); err != nil {
		return plan, err
	}
	if _, err := v.read("CONTEXT.md"); err != nil {
		return plan, err
	}
	if _, err := contextContract(root); err != nil {
		return plan, err
	}
	if _, err := v.watch("CONTEXT.md"); err != nil {
		return plan, err
	}
	if !strings.HasSuffix(cpRef, ".json") {
		return plan, domain.Fail("UNPORTED", "阶段写入要求显式 JSON checkpoint；YAML 仅只读兼容")
	}
	cp, err := v.document(cpRef)
	if err != nil {
		return plan, domain.Fail("UNPORTED", "需要既有 checkpoint，不从模板推断当前阶段")
	}
	feature, featureRoot, err := stageFeatureBinding(s, cpRef, cp)
	if err != nil {
		return plan, err
	}
	config, err := tracker(v)
	if err != nil {
		return plan, err
	}
	design, _, err := designProfile(v)
	if err != nil {
		return plan, err
	}
	tracking, err := asTracking(cp)
	if err != nil {
		return plan, err
	}
	if tracking != nil {
		if _, err = checkStage(v, cp, cpRef, s); err != nil {
			return plan, err
		}
	}
	if tracking == nil {
		entry := TrackingEntry{"parent-ticket", featureRoot + "/parent-ticket.md"}
		if design {
			entry = TrackingEntry{"checkpoint", cpRef}
		}
		tracking = &StageTracking{SchemaVersion: 1, FeatureID: feature, CheckpointRef: cpRef, EntryStage: text(cp["stage"]), Entry: entry, Items: []WorkItem{}}
	}
	input, err := v.read(inputRef)
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
			m, ok := object(seed)
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
			id := text(m["id"])
			var existing *WorkItem
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
			if p := text(m["progress"]); p != "" && p != "pending" {
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
			refs := []Binding{}
			if sources, present := m["source_refs"]; present {
				list, ok := sources.([]any)
				if !ok {
					return plan, domain.Fail("INPUT", "source_refs 必须是数组")
				}
				for _, source := range list {
					ref := text(source)
					if obj, ok := object(source); ok {
						ref = text(obj["ref"])
					}
					binding, err := v.bind(ref)
					if err != nil {
						return plan, err
					}
					refs = append(refs, binding)
				}
			}
			copy["source_refs"] = refs
			b, _ := json.Marshal(copy)
			var item WorkItem
			if err = json.Unmarshal(b, &item); err != nil {
				return plan, err
			}
			if item.Deferred != nil && !contains(item.SplitReasons, "cross-stage-deferral") {
				item.SplitReasons = append(item.SplitReasons, "cross-stage-deferral")
			}
			tracking.Items = append(tracking.Items, item)
		}
	} else if action == "update" {
		patch, ok := object(value)
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
		id := text(patch["id"])
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
		return plan, domain.Fail("UNPORTED", "stage 写入动作尚未迁移")
	}
	for i := range tracking.Items {
		item := &tracking.Items[i]
		for _, other := range tracking.Items {
			if contains(other.Dependencies, item.ID) && !contains(item.SplitReasons, "blocks-other-work") {
				item.SplitReasons = append(item.SplitReasons, "blocks-other-work")
			}
		}
		if len(tracking.Items) > 0 && item.Owner != tracking.Items[0].Owner && !contains(item.SplitReasons, "different-owner") {
			item.SplitReasons = append(item.SplitReasons, "different-owner")
		}
		if len(item.SplitReasons) > 0 && item.DefinitionRef == "" {
			ref := featureRoot + "/work-items/" + item.ID + ".md"
			d, err := v.watch(ref)
			if err != nil {
				return plan, err
			}
			if d.Type != "missing" {
				return plan, domain.Fail("TRACKING_CONFLICT", "独立定义是人工文件，不覆盖: "+ref)
			}
			data := stageDefinition(*item, cpRef)
			item.DefinitionRef = ref
			item.DefinitionDigest = "sha256:" + safefs.Digest(data)
			if err = v.set(ref, data); err != nil {
				return plan, err
			}
		}
	}
	if !design {
		entry := tracking.Entry.Ref
		d, err := v.watch(entry)
		if err != nil {
			return plan, err
		}
		link := "\n阶段工作进度与证据：" + cpRef + "\n"
		if d.Type == "missing" {
			content := "# " + feature + "\n\nStatus: needs-triage\n" + link
			if text(config["platform"]) != "local-markdown" {
				content += "\npublication: pending\npending_publication_to: " + text(config["platform"]) + "\n"
			}
			if err = v.set(entry, []byte(content)); err != nil {
				return plan, err
			}
		} else {
			b, err := v.read(entry)
			if err != nil {
				return plan, err
			}
			if !strings.Contains(string(b), cpRef) {
				if err = v.set(entry, append(b, []byte(link)...)); err != nil {
					return plan, err
				}
			}
		}
	}
	mapRef := featureRoot + "/map.md"
	d, err := v.watch(mapRef)
	if err != nil {
		return plan, err
	}
	if d.Type == "missing" {
		if err = v.set(mapRef, []byte("---\ncheckpoint_ref: "+cpRef+"\n---\n# "+feature+"\n\n阶段工作与证据见 "+cpRef+"。\n")); err != nil {
			return plan, err
		}
	} else {
		b, err := v.read(mapRef)
		if err != nil {
			return plan, err
		}
		if !strings.Contains(string(b), cpRef) {
			if err = v.set(mapRef, append(b, []byte("\n阶段工作与证据："+cpRef+"\n")...)); err != nil {
				return plan, err
			}
		}
	}
	cp["stage_tracking"] = tracking
	b, err := jsonBytes(cp)
	if err != nil {
		return plan, err
	}
	if err = v.set(cpRef, b); err != nil {
		return plan, err
	}
	trackerBytes, err := v.read(trackerRef)
	if err != nil {
		return plan, err
	}
	enabled, err := enableTracker(trackerBytes)
	if err != nil {
		return plan, err
	}
	if err = v.set(trackerRef, enabled); err != nil {
		return plan, err
	}
	// Normalize typed values before calling the same Schema compiler as file validation.
	normalized, err := schema.Parse(b)
	if err != nil {
		return plan, err
	}
	cp, _ = object(normalized)
	if _, err = checkStage(v, cp, cpRef, s); err != nil {
		return plan, err
	}
	if err = s.finish(); err != nil {
		return plan, err
	}
	for ref, descriptor := range v.observed {
		// The transaction manager owns the journal and pending exclusion under
		// its lock; its preparation must not invalidate the stage plan itself.
		if ref != ".yss/transactions" && !strings.HasPrefix(ref, ".yss/transactions/") {
			plan.Observed[ref] = descriptor
		}
	}
	plan.Operations, err = v.ops()
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
		s := newSemanticSession(ctx, root, nil)
		files, err := s.scanFresh(scan.Ref, false)
		if err != nil {
			return err
		}
		if !equalStrings(filter(files), filter(scan.Files)) {
			return domain.Fail("INPUT_DRIFT", "阶段写入期间功能登记文件集合变化")
		}
	}
	return nil
}

func stageRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	ref := first(args["checkpoint"], args["file"], args["arg0"])
	if action == "query" || action == "status" || action == "check" {
		if args["stage"] != "" || args["work-unit"] != "" {
			return nil, domain.Fail("UNPORTED", "checkpoint 工作项查询不支持 --stage/--work-unit 过滤")
		}
		s := newSemanticSession(ctx, root, args)
		v := s.v
		if err := pendingOldTransaction(v); err != nil {
			return nil, err
		}
		cp, err := s.doc(ref)
		if err != nil {
			return nil, err
		}
		result, err := checkStage(v, cp, ref, s)
		if err != nil {
			return nil, err
		}
		if err = s.finish(); err != nil {
			return nil, err
		}
		if id := args["id"]; id != "" {
			tracking, err := asTracking(cp)
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
		return nil, domain.Fail("UNPORTED", "stage 动作未迁移: "+action)
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
		return map[string]any{"status": tx.Status, "transaction": tx, "scope": "stage-work-only", "approval_created": false, "ready_for_agent_created": false, "execution_authorization": "not-evaluated"}, nil
	}
	if args["refresh"] != "" {
		return nil, domain.Fail("UNPORTED", "refresh 保留历史并重置执行态的语义尚未迁移")
	}
	if action == "plan" {
		action = "register"
	}
	input := first(args["items"], args["item"])
	if input == "" {
		return nil, domain.Fail("INPUT", "register/update 需要 --items/--item")
	}
	return buildStagePlan(root, action, ref, input, ctx)
}
