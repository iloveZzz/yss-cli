package kernel

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

const TrackerRef = ".template-spec/agents/issue-tracker.md"

const TrackingSchemaRef = ".template-spec/process/schemas/stage-tracking.schema.json"

// This is the same consumer mapping as the retained stage-tracking contract, including its design override.
var TrackedUnits = map[string]string{
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

type View struct {
	Root                   string                         `json:"-" yaml:"-"`
	Overlay                map[string][]byte              `json:"-" yaml:"-"`
	Observed               map[string]domain.Descriptor   `json:"-" yaml:"-"`
	Virtual                map[string]semanticArchiveFile `json:"-" yaml:"-"`
	bindIdentity           bool
	registrationBoundaries map[string]bool
	Identities             map[string]os.FileInfo `json:"-" yaml:"-"`
}

func NewView(root string) *View {
	return &View{Root: root, Overlay: map[string][]byte{}, Observed: map[string]domain.Descriptor{}}
}

func (v *View) Watch(ref string) (domain.Descriptor, error) {
	var identity os.FileInfo
	if v.bindIdentity && v.Virtual == nil {
		for parent := path.Dir(ref); ; parent = path.Dir(parent) {
			if err := v.ObservePathIdentity(parent); err != nil {
				return domain.Descriptor{}, err
			}
			if parent == "." {
				break
			}
		}
		p, err := safefs.Path(v.Root, ref)
		if err != nil {
			return domain.Descriptor{}, err
		}
		identity, err = os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return domain.Descriptor{}, err
		}
		if old, seen := v.Identities[ref]; seen && !SameObservedIdentity(old, identity) {
			return domain.Descriptor{}, domain.Fail("INPUT_DRIFT", "核验阶段文件身份变化: "+ref)
		}
	}
	d, err := v.describe(ref)
	if err != nil {
		return d, err
	}
	if old, ok := v.Observed[ref]; ok && old != d {
		return d, domain.Fail("INPUT_DRIFT", "核验阶段输入变化: "+ref)
	}
	if v.bindIdentity && v.Virtual == nil {
		p, err := safefs.Path(v.Root, ref)
		if err != nil {
			return d, err
		}
		after, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return d, err
		}
		if !SameObservedIdentity(identity, after) {
			return d, domain.Fail("INPUT_DRIFT", "读取期间文件身份变化: "+ref)
		}
		if v.Identities == nil {
			v.Identities = map[string]os.FileInfo{}
		}
		v.Identities[ref] = identity
	}
	v.Observed[ref] = d
	return d, nil
}

func SameObservedIdentity(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (v *View) Read(ref string) ([]byte, error) {
	if b, ok := v.Overlay[ref]; ok {
		return b, nil
	}
	d, err := v.Watch(ref)
	if err != nil {
		return nil, err
	}
	if d.Type != "file" {
		return nil, domain.Fail("INPUT", "输入不可读: "+ref)
	}
	if v.Virtual != nil {
		return append([]byte(nil), v.Virtual[ref].Data...), nil
	}
	p, err := safefs.Path(v.Root, ref)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err == nil && safefs.Digest(b) != d.Digest {
		return nil, domain.Fail("INPUT_DRIFT", "读取期间输入变化: "+ref)
	}
	if err == nil && v.bindIdentity {
		after, statErr := os.Lstat(p)
		if statErr != nil || !SameObservedIdentity(v.Identities[ref], after) {
			return nil, domain.Fail("INPUT_DRIFT", "读取返回前文件身份变化: "+ref)
		}
	}
	return b, err
}

func (v *View) Document(ref string) (map[string]any, error) {
	if strings.HasSuffix(ref, "/orchestration-contract.yaml") {
		if ref == schema.OrchestrationRef {
			var err error
			ref, err = orchestrationReference(func(name string) (bool, error) { d, err := v.Watch(name); return d.Type != "missing", err }, v.Document)
			if err != nil {
				return nil, err
			}
		}
		value, _, _, err := schema.LoadOrchestration(v.Read, ref)
		return value, err
	}
	b, err := v.Read(ref)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(ref, ".md") {
		b, _, err = Frontmatter(b)
		if err != nil {
			return nil, err
		}
	}
	data, err := schema.Parse(b)
	if err != nil {
		return nil, err
	}
	m, ok := Object(data)
	if !ok {
		return nil, domain.Fail("INPUT", "治理资产必须是对象: "+ref)
	}
	return m, nil
}

func (v *View) Bind(ref string) (Binding, error) {
	b, err := v.Read(ref)
	if err != nil {
		return Binding{}, err
	}
	return Binding{ref, "sha256:" + safefs.Digest(b)}, nil
}

func (v *View) Set(ref string, data []byte) error {
	if v.Virtual != nil {
		return domain.Fail("READ_ONLY", "归档来源视图不允许写入")
	}
	if _, err := v.Watch(ref); err != nil {
		return err
	}
	v.Overlay[ref] = data
	return nil
}

func (v *View) Ops() ([]transaction.Operation, error) {
	refs := []string{}
	for ref := range v.Overlay {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	ops := []transaction.Operation{}
	for _, ref := range refs {
		d := v.Observed[ref]
		b := v.Overlay[ref]
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

func Frontmatter(b []byte) ([]byte, []byte, error) {
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

func Tracker(v *View, sessions ...*SemanticSession) (map[string]any, error) {
	read := v.Document
	if len(sessions) > 0 {
		read = sessions[0].Doc
	}
	m, err := read(TrackerRef)
	if err != nil {
		return nil, err
	}
	t, ok := Object(m["tracker"])
	if !ok || !(Text(t["platform"]) == "local-markdown" || Text(t["platform"]) == "github" || Text(t["platform"]) == "gitlab") {
		return nil, domain.Fail("TRACKER", "主 tracker 配置无效")
	}
	if version, exists := t["lifecycle_tracking_version"]; exists {
		n, ok := Integer(version)
		if !ok || n != 1 {
			return nil, domain.Fail("TRACKER", "未知阶段追踪版本")
		}
	}
	if Text(t["root"]) == "" {
		return nil, domain.Fail("TRACKER", "tracker.root 缺失")
	}
	if _, err = safefs.Path(v.Root, strings.TrimSuffix(Text(t["root"]), "/")); err != nil {
		return nil, err
	}
	return t, nil
}

func ProjectIdentity(v *View, sessions ...*SemanticSession) error {
	read := v.Document
	if len(sessions) > 0 {
		read = sessions[0].Doc
	}
	m, err := read("yss-project.yaml")
	if err != nil {
		return err
	}
	version, ok := Integer(m["schema_version"])
	if !ok || (version != 1 && version != 2) || Text(m["repository_mode"]) != "project-instance" {
		return domain.Fail("IDENTITY", "stage/project-ci 仅适用于 project-instance")
	}
	return nil
}

func DesignProfile(v *View, sessions ...*SemanticSession) (bool, []string, error) {
	ref := ".template-spec/process/harness-profile.yaml"
	read := v.Document
	if len(sessions) > 0 {
		ref = sessions[0].LocalRef(ref)
		read = sessions[0].Doc
	}
	d, err := v.Watch(ref)
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
	if lifecycle, ok := Object(m["lifecycle"]); ok && lifecycle["allowed_work_units"] != nil {
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
	return Text(m["profile_id"]) == "harness.business-ddd-strategy-handoff", list, nil
}

func ViewWorkLayout(v *View) (*worklayout.Layout, error) {
	config, e := Tracker(v)
	if e != nil {
		return nil, e
	}
	return worklayout.New(v.Root, config)
}

// A registered canonical feature ID and its directory are separate facts. Older
// instances retain their existing path identity until they adopt this policy.
func StageFeatureBinding(s *SemanticSession, ref string, cp map[string]any) (string, string, error) {
	if err := s.Guard(); err != nil {
		return "", "", err
	}
	if strings.HasPrefix(Text(cp["feature_id"]), "feature.") {
		present, err := s.Exists(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return "", "", err
		}
		if present {
			profileDoc, err := s.Doc(".template-spec/process/harness-profile.yaml")
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
				return "", "", s.Unavailable("IDENTITY", "未知功能登记 Profile")
			}
			contractRef, err := s.OrchestrationReference()
			if err != nil {
				return "", "", err
			}
			present, err = s.Exists(contractRef)
			if err != nil {
				return "", "", err
			}
			if present {
				contract, err := s.Doc(contractRef)
				if err != nil {
					return "", "", err
				}
				if _, declared := contract["progression_target"]; declared {
					if _, _, err = ProgressionPolicy(s); err != nil {
						return "", "", err
					}
					configRef, registered, err := ProgressionLocation(s, ref)
					if err != nil {
						return "", "", err
					}
					if registered["feature_id"] != cp["feature_id"] {
						return "", "", s.Reject("TRACKING_FEATURE", "当前 checkpoint 功能身份与登记不一致")
					}
					return Text(cp["feature_id"]), path.Dir(configRef), nil
				}
			}
		}
	}
	config, err := Tracker(s.V, s)
	if err != nil {
		return "", "", err
	}
	layout, err := worklayout.New(s.Root, config)
	if err != nil {
		return "", "", err
	}
	feature, err := layout.CheckpointFeature(ref)
	if err != nil {
		return "", "", err
	}
	if Text(cp["feature_id"]) != feature {
		return "", "", s.Reject("TRACKING_FEATURE", "checkpoint 功能身份与路径不一致")
	}
	base, err := layout.FeatureRoot(feature)
	return feature, base, err
}

func AsTracking(cp map[string]any) (*StageTracking, error) {
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

func Contains(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

func CheckStage(v *View, cp map[string]any, ref string, sessions ...*SemanticSession) (map[string]any, error) {
	s := NewSemanticSession(context.Background(), v.Root, nil)
	s.V = v
	if len(sessions) > 0 {
		s = sessions[0]
	}
	if err := ProjectIdentity(v, s); err != nil {
		return nil, err
	}
	config, err := Tracker(v, s)
	if err != nil {
		return nil, err
	}
	// 没有功能身份也没有阶段追踪的 checkpoint（尚未启用追踪的旧实例）不要求登记的功能身份，
	// 与 Node 的 assertStageTracking 一致；任一出现就必须与路径、登记一致。
	var feature, featureRoot string
	if cp["feature_id"] != nil || cp["stage_tracking"] != nil {
		feature, featureRoot, err = StageFeatureBinding(s, ref, cp)
		if err != nil {
			return nil, err
		}
	}
	t, err := AsTracking(cp)
	if err != nil {
		return nil, err
	}
	if t == nil {
		enabled, _ := Integer(config["lifecycle_tracking_version"])
		if enabled == 1 && (Text(cp["stage"]) == "stage.plan" || Text(cp["stage"]) == "stage.spec-architecture" || Text(cp["stage"]) == "stage.product-design") {
			return nil, domain.Fail("TRACKING_REQUIRED", "当前阶段缺少 stage_tracking")
		}
		return map[string]any{"status": "not-applicable", "stale_item_ids": []string{}, "scope": "stage-work-only", "read_only": true, "approval_created": false}, nil
	}
	sp, err := safefs.Path(v.Root, TrackingSchemaRef)
	if err != nil {
		return nil, err
	}
	if _, err = s.Bytes(TrackingSchemaRef); err != nil {
		return nil, err
	}
	// The current stage Schema is standalone. Refuse a future external closure until every
	// referenced Schema can be included in the write plan's locked input guards.
	stageSchema, err := s.Doc(TrackingSchemaRef)
	if err != nil {
		return nil, err
	}
	var externalRefs func(any) bool
	externalRefs = func(value any) bool {
		if m, ok := Object(value); ok {
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
		ref, err := filepath.Rel(s.Root, file)
		if err != nil {
			return nil, err
		}
		return s.Bytes(filepath.ToSlash(ref))
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
	design, allowed, err := DesignProfile(v, s)
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
	entry, err := s.Bytes(t.Entry.Ref)
	if err != nil {
		return nil, err
	}
	if t.Entry.Kind == "parent-ticket" && !strings.Contains(string(entry), ref) {
		return nil, domain.Fail("TRACKING_ENTRY", "parent-ticket 缺少 checkpoint 引用")
	}
	r, err := s.Doc(".template-spec/process/lifecycle-registry.yaml")
	if err != nil {
		return nil, err
	}
	kind, _, ok := FindRegistry(r, t.EntryStage)
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
		kind, _, ok = FindRegistry(r, item.Stage)
		unitKind, _, unitOK := FindRegistry(r, item.WorkUnit)
		expected := TrackedUnits[item.WorkUnit]
		if design && (item.WorkUnit == "work-unit.business-ticket-formalization" || item.WorkUnit == "work-unit.strategic-design-handoff") {
			expected = "stage.ticket-formalization"
		}
		if !ok || kind != "stages" || !unitOK || unitKind != "work_units" || expected != item.Stage {
			return nil, domain.Fail("TRACKING_ROUTE", "工作项阶段与工作单元不匹配: "+item.ID)
		}
		if allowed != nil && !Contains(allowed, item.WorkUnit) {
			return nil, domain.Fail("TRACKING_PROFILE", "Profile 禁止当前工作单元: "+item.ID)
		}
		if len(item.SplitReasons) > 0 && item.DefinitionRef == "" {
			return nil, domain.Fail("TRACKING_DEFINITION", "拆分工作项必须持有独立定义: "+item.ID)
		}
		if item.DefinitionRef != "" {
			if item.DefinitionRef != base+"work-items/"+item.ID+".md" {
				return nil, domain.Fail("TRACKING_DEFINITION", "独立定义路径非法")
			}
			b, err := s.Bytes(item.DefinitionRef)
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
			if !Contains(item.SplitReasons, "cross-stage-deferral") || item.DefinitionRef == "" || item.Progress == "completed" || item.Progress == "cancelled" {
				return nil, domain.Fail("TRACKING_DEFERRAL", "延期工作项必须独立，且不能标记完成或取消")
			}
			at, err := time.Parse(time.RFC3339Nano, item.Deferred.ResolveBy)
			if err != nil || !at.After(time.Now()) {
				return nil, domain.Fail("TRACKING_DEFERRAL", "延期已过期或日期非法")
			}
			if _, err = s.Bytes(item.Deferred.DecisionRef); err != nil {
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
				if !Contains(item.Acceptance, c.Criterion) {
					return nil, domain.Fail("TRACKING_COMPLETION", "未登记的验收准则")
				}
			}
		}
		bindings := append([]Binding{}, item.SourceRefs...)
		for _, c := range item.Completion {
			bindings = append(bindings, c.EvidenceRefs...)
		}
		for _, binding := range bindings {
			if _, err := safefs.Path(v.Root, binding.Ref); err != nil {
				return nil, err
			}
			raw, err := s.Bytes(binding.Ref)
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
			if Contains(other.Dependencies, item.ID) {
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

// Only fields available in the actual platform FileInfo are exported. Go's
// FileMode is not POSIX st_mode; permissions is explicitly a portable bitmask.
func ObservedIdentityReceipt(info os.FileInfo) map[string]any {
	kind := "other"
	if info.Mode().IsRegular() {
		kind = "file"
	} else if info.IsDir() {
		kind = "directory"
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = "link"
	}
	permissions := uint32(info.Mode().Perm())
	if info.Mode()&os.ModeSetuid != 0 {
		permissions |= 04000
	}
	if info.Mode()&os.ModeSetgid != 0 {
		permissions |= 02000
	}
	if info.Mode()&os.ModeSticky != 0 {
		permissions |= 01000
	}
	result := map[string]any{"type": kind, "permissions": permissions}
	system := reflect.ValueOf(info.Sys())
	if system.IsValid() && system.Kind() == reflect.Pointer {
		system = system.Elem()
	}
	if system.IsValid() && system.Kind() == reflect.Struct {
		for key, field := range map[string]string{"ino": "Ino", "dev": "Dev"} {
			value := system.FieldByName(field)
			if value.IsValid() {
				switch value.Kind() {
				case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
					result[key] = fmt.Sprint(value.Uint())
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
					result[key] = fmt.Sprint(value.Int())
				}
			}
		}
	}
	return result
}

// Registration scans may observe the boundary identity of a pruned tree, but
// never use it as a readable asset path or authorize traversal into Git data.
func (v *View) registrationBoundaryPath(ref string) (string, error) {
	name := filepath.Base(ref)
	if name != ".git" && name != "node_modules" {
		return "", domain.Fail("PATH", "未知登记扫描边界")
	}
	parent := filepath.ToSlash(filepath.Dir(ref))
	if parent == "." {
		checked, err := safefs.Path(v.Root, "yss-project.yaml")
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Dir(checked), name), nil
	}
	checked, err := safefs.Path(v.Root, parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(checked, name), nil
}

func (v *View) ObserveRegistrationBoundary(ref string) error {
	if v.Virtual != nil {
		return domain.Fail("PATH", "虚拟来源不能登记物理扫描边界")
	}
	target, err := v.registrationBoundaryPath(ref)
	if err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return domain.Fail("PATH", "登记扫描边界拒绝符号链接或特殊文件: "+ref)
	}
	if v.registrationBoundaries == nil {
		v.registrationBoundaries = map[string]bool{}
	}
	v.registrationBoundaries[ref] = true
	return v.ObservePathIdentity(ref)
}

func (v *View) identityPath(ref string) (string, error) {
	if v.registrationBoundaries[ref] {
		return v.registrationBoundaryPath(ref)
	}
	if ref == "." {
		return v.Root, nil
	}
	return safefs.Path(v.Root, ref)
}

func (v *View) ObservePathIdentity(ref string) error {
	if v.Virtual != nil {
		return nil
	}
	target, err := v.identityPath(ref)
	if err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if v.registrationBoundaries[ref] && (info == nil || info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular())) {
		return domain.Fail("PATH", "登记扫描边界不是普通路径: "+ref)
	}
	if v.Identities == nil {
		v.Identities = map[string]os.FileInfo{}
	}
	if old, ok := v.Identities[ref]; ok && !SameObservedIdentity(old, info) {
		return domain.Fail("INPUT_DRIFT", "目录或路径身份变化: "+ref)
	}
	v.Identities[ref] = info
	return nil
}
func (v *View) CheckPathIdentities() error {
	if v.Virtual != nil {
		return nil
	}
	for ref, old := range v.Identities {
		target, err := v.identityPath(ref)
		if err != nil {
			return err
		}
		now, err := os.Lstat(target)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if !SameObservedIdentity(old, now) {
			return domain.Fail("INPUT_DRIFT", "路径身份变化: "+ref)
		}
	}
	return nil
}

// StageDrift 返回来源或证据绑定漂移的工作项，并沿依赖传递；与 CheckStage 的漂移规则一致，
// 但不因已运行/完成项漂移而拒绝，供 refresh 保留历史并重置执行态。
func StageDrift(s *SemanticSession, t *StageTracking) []string {
	stale := map[string]bool{}
	for _, item := range t.Items {
		bindings := append([]Binding{}, item.SourceRefs...)
		for _, c := range item.Completion {
			bindings = append(bindings, c.EvidenceRefs...)
		}
		for _, binding := range bindings {
			if _, err := safefs.Path(s.V.Root, binding.Ref); err != nil {
				stale[item.ID] = true
				continue
			}
			raw, err := s.Bytes(binding.Ref)
			if err != nil || "sha256:"+safefs.Digest(raw) != binding.Digest {
				stale[item.ID] = true
			}
		}
	}
	for changed := true; changed; {
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
	ids := []string{}
	for id := range stale {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
