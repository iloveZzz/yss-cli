package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SemanticValidator func(*SemanticSession, string, map[string]string) error

var SemanticValidators = map[string]SemanticValidator{}

type SemanticCheck struct {
	ID         string `json:"id"`
	SourceRoot string `json:"source_root,omitempty"`
	SourceRef  string `json:"source_ref"`
	Status     string `json:"status"`
	Code       string `json:"code,omitempty"`
}

type SemanticDiagnostic struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	SourceRef string `json:"source_ref,omitempty"`
	Recovery  string `json:"recovery"`
}

type SemanticInput struct {
	Root       string            `json:"root"`
	Ref        string            `json:"ref"`
	Descriptor domain.Descriptor `json:"descriptor"`
}

type SemanticGitInput struct {
	Root      string   `json:"root"`
	Arguments []string `json:"arguments"`
	Digest    string   `json:"digest"`
	ExitCode  int      `json:"exit_code"`
}

type SemanticReport struct {
	SchemaVersion          int                  `json:"schema_version"`
	Kind                   string               `json:"kind"`
	Status                 string               `json:"status"`
	ReadOnly               bool                 `json:"read_only"`
	Scope                  string               `json:"scope"`
	Profile                string               `json:"profile,omitempty"`
	ApprovalCreated        bool                 `json:"approval_created"`
	ExecutionAuthorization string               `json:"execution_authorization"`
	Checks                 []SemanticCheck      `json:"checks"`
	Diagnostics            []SemanticDiagnostic `json:"diagnostics"`
	Inputs                 []SemanticInput      `json:"inputs"`
	Applicability          []map[string]any     `json:"applicability"`
	Coverage               map[string]any       `json:"coverage,omitempty"`
	GitInputs              []SemanticGitInput   `json:"git_inputs"`
}

type SemanticSession struct {
	Ctx           context.Context     `json:"-" yaml:"-"`
	Root          string              `json:"-" yaml:"-"`
	V             *View               `json:"-" yaml:"-"`
	Args          map[string]string   `json:"-" yaml:"-"`
	CheckpointRef string              `json:"-" yaml:"-"`
	Registry      map[string]any      `json:"-" yaml:"-"`
	Roles         map[string]any      `json:"-" yaml:"-"`
	Report        *SemanticReport     `json:"-" yaml:"-"`
	Scans         map[string][]string `json:"-" yaml:"-"`
	active        map[string]bool
	ExternalViews map[string]*View  `json:"-" yaml:"-"`
	Aliases       map[string]string `json:"-" yaml:"-"`
	// All source sessions share these observations, including immutable Handoff payloads.
	Children          []*SemanticSession `json:"-" yaml:"-"`
	gitInputs         map[string]semanticGitObservation
	ContextInventory  []string                  `json:"-" yaml:"-"`
	IntakeInputs      map[string]map[string]any `json:"-" yaml:"-"`
	ProfileInput      map[string]any            `json:"-" yaml:"-"` // Verified explicit Profile receipt, for readonly coordination.
	RuleSession       *SemanticSession          `json:"-" yaml:"-"`
	toolSource        *SemanticSession
	finalObservations map[string][]func() error
}

func NewSemanticSession(ctx context.Context, root string, args map[string]string) *SemanticSession {
	if args == nil {
		args = map[string]string{}
	}
	v := NewView(root)
	v.bindIdentity = true
	return &SemanticSession{Ctx: ctx, Root: root, V: v, Args: args, CheckpointRef: args["checkpoint"],
		Report: &SemanticReport{SchemaVersion: 1, Kind: "governance-semantic-verification", Status: "passed", ReadOnly: true,
			Scope: "current-native-governance", ApprovalCreated: false, ExecutionAuthorization: "not-evaluated",
			Checks: []SemanticCheck{}, Diagnostics: []SemanticDiagnostic{}, Inputs: []SemanticInput{}, Applicability: []map[string]any{}, GitInputs: []SemanticGitInput{}},
		Scans: map[string][]string{}, active: map[string]bool{}, ExternalViews: map[string]*View{}, Aliases: map[string]string{}, gitInputs: map[string]semanticGitObservation{}, IntakeInputs: map[string]map[string]any{}}
}

func (s *SemanticSession) Reject(code, message string) error {
	return &domain.Error{Code: code, Message: message, Exit: 1}
}

func (s *SemanticSession) Unavailable(code, message string) error {
	return &domain.Error{Code: code, Message: message, Exit: 2}
}

func (s *SemanticSession) Guard() error {
	if s.Ctx == nil {
		return s.Unavailable("ARGUMENT", "取消上下文不可为空")
	}
	if err := s.Ctx.Err(); err != nil {
		return s.Unavailable("CANCELLED", err.Error())
	}
	return nil
}

func (s *SemanticSession) LocalRef(ref string) string {
	if mapped, ok := s.Aliases[ref]; ok {
		return mapped
	}
	return ref
}

func (s *SemanticSession) Bytes(ref string) ([]byte, error) {
	if err := s.Guard(); err != nil {
		return nil, err
	}
	v, local, err := s.ReferenceView(ref)
	if err != nil {
		var d *domain.Error
		if errors.As(err, &d) && d.Exit == 2 {
			return nil, err
		}
		return nil, s.Unavailable("PATH", err.Error())
	}
	b, err := v.Read(local)
	if err != nil {
		var d *domain.Error
		if errors.As(err, &d) && d.Code == "INPUT_DRIFT" {
			return nil, s.Unavailable("INPUT_DRIFT", err.Error())
		}
		return nil, s.Unavailable("INPUT", err.Error())
	}
	return b, nil
}

func (s *SemanticSession) Doc(ref string) (map[string]any, error) {
	b, err := s.Bytes(ref)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(ref, ".md") {
		b, _, err = Frontmatter(b)
		if err != nil {
			return nil, s.Unavailable("INPUT", err.Error())
		}
	}
	v, err := schema.Parse(b)
	if err != nil {
		return nil, s.Unavailable("INPUT", ref+": "+err.Error())
	}
	m, ok := Object(v)
	if !ok {
		return nil, s.Unavailable("INPUT", ref+": 必须是对象")
	}
	return m, nil
}

func (s *SemanticSession) Exists(ref string) (bool, error) {
	if err := s.Guard(); err != nil {
		return false, err
	}
	v, local, err := s.ReferenceView(ref)
	if err != nil {
		return false, err
	}
	if v.Virtual != nil {
		d, e := v.Watch(local)
		return e == nil && d.Type != "missing", e
	}
	p, err := safefs.Path(v.Root, local)
	if err != nil {
		return false, s.Unavailable("PATH", err.Error())
	}
	st, err := os.Lstat(p)
	if os.IsNotExist(err) {
		_, e := v.Watch(local)
		return false, e
	}
	if err != nil {
		return false, s.Unavailable("INPUT", err.Error())
	}
	if st.IsDir() {
		inspector := s
		if v != s.V {
			inspector = NewSemanticSession(s.Ctx, v.Root, s.Args)
			s.Children = append(s.Children, inspector)
		}
		_, err = inspector.Scan(local)
		return err == nil, err
	}
	_, err = v.Watch(local)
	return err == nil, err
}

func (s *SemanticSession) Bind(ref string) (Binding, error) {
	b, err := s.Bytes(ref)
	if err != nil {
		return Binding{}, err
	}
	return Binding{Ref: ref, Digest: "sha256:" + safefs.Digest(b)}, nil
}

func (s *SemanticSession) ValidateSchema(ref string, value any) error {
	if s.RuleSession != nil {
		// The enclosing wrapper/Receipt belongs to its receiver. A frozen
		// source registry and checkpoint belong to the source Profile already
		// identified by authorities, even through several nested packages.
		if s.Report.Profile != "" && (ref == ".template-spec/process/schemas/lifecycle-registry.schema.json" || ref == ".template-spec/process/schemas/lifecycle-checkpoint.schema.json") {
			return s.validateImmutableSourceSchema(ref, value)
		}
		return s.RuleSession.ValidateSchema(ref, value)
	}
	return s.validateSchemaInView(ref, value)
}

func (s *SemanticSession) validateSchemaInView(ref string, value any) error {
	if err := s.Guard(); err != nil {
		return err
	}
	path, err := safefs.Path(s.Root, s.LocalRef(ref))
	if err != nil {
		return s.Unavailable("PATH", err.Error())
	}
	issues, err := schema.ValidateValueWithReader(path, value, func(file string) ([]byte, error) {
		local, e := filepath.Rel(s.Root, file)
		if e != nil {
			return nil, e
		}
		return s.Bytes(filepath.ToSlash(local))
	})
	if err != nil {
		return s.Unavailable("CAPABILITY", ref+": "+err.Error())
	}
	if len(issues) > 0 {
		return s.Reject("SCHEMA", fmt.Sprintf("%s: %v", ref, issues))
	}
	return s.Guard()
}

// Historical source snapshots need not contain schemas. Their original
// registry, roles, approvals and replies remain the authority; the supported
// CLI Bundle supplies only the corresponding Profile's strict syntax. A
// captured schema is observed and validated too, and can never replace this
// canonical constraint with a permissive or unsupported schema.
func (s *SemanticSession) validateImmutableSourceSchema(ref string, value any) error {
	if err := s.Guard(); err != nil {
		return err
	}
	present, err := s.Exists(ref)
	if err != nil {
		return err
	}
	if present {
		if err = s.validateSchemaInView(ref, value); err != nil {
			return err
		}
	}
	b, err := bundle.Load(s.Report.Profile)
	if err != nil {
		return s.Unavailable("CAPABILITY", "不支持冻结来源 Profile 的结构校验："+err.Error())
	}
	p, err := safefs.Path(s.Root, ref)
	if err != nil {
		return s.Unavailable("PATH", err.Error())
	}
	issues, err := schema.ValidateValueWithReader(p, value, func(file string) ([]byte, error) {
		local, e := filepath.Rel(s.Root, file)
		if e != nil || !strings.HasPrefix(filepath.ToSlash(local), ".template-spec/process/schemas/") {
			return nil, fmt.Errorf("来源结构 schema 依赖越界：%s", file)
		}
		f, ok := b.Files[filepath.ToSlash(local)]
		if !ok {
			return nil, fmt.Errorf("受信 %s Bundle 缺少结构 schema：%s", s.Report.Profile, local)
		}
		return f.Render(nil)
	})
	if err != nil {
		return s.Unavailable("CAPABILITY", ref+": "+err.Error())
	}
	if len(issues) > 0 {
		return s.Reject("SCHEMA", fmt.Sprintf("%s/%s: %v", s.Report.Profile, ref, issues))
	}
	return s.Guard()
}

func (s *SemanticSession) Basis(value any) error {
	rows, ok := value.([]any)
	if !ok || len(rows) == 0 {
		return s.Reject("EVIDENCE_REQUIRED", "缺少当前依据")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		m, ok := Object(row)
		if !ok {
			return s.Reject("EVIDENCE_INVALID", "依据必须为对象")
		}
		ref := Text(m["ref"])
		if err := RejectProgressionEvidence(s, ref); err != nil {
			return err
		}
		digest := strings.TrimPrefix(Text(m["digest"]), "sha256:")
		if ref == "" || seen[ref] || !IsSHA256(digest) {
			return s.Reject("EVIDENCE_INVALID", "依据引用或摘要缺失、重复或非法")
		}
		seen[ref] = true
		b, err := s.Bytes(ref)
		if err != nil {
			return err
		}
		if safefs.Digest(b) != digest {
			return s.Reject("EVIDENCE_DRIFT", "依据过期: "+ref)
		}
	}
	return nil
}

func IsSHA256(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func SemMap(v any) map[string]any {
	m, _ := Object(v)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func SemList(v any) []any { a, _ := v.([]any); return a }

func SemStrings(v any) []string {
	a := []string{}
	switch v := v.(type) {
	case []any:
		for _, x := range v {
			a = append(a, Text(x))
		}
	case []string:
		a = append(a, v...)
	}
	return a
}

func SemHas(v any, w string) bool {
	for _, x := range SemStrings(v) {
		if x == w {
			return true
		}
	}
	return false
}

// scan records directories as well as files: even additions to an empty subtree
// invalidate a claimed current result. Files are also observed by bytes and mode.
func (s *SemanticSession) Scan(ref string) ([]string, error) {
	if err := s.Guard(); err != nil {
		return nil, err
	}
	logicalRef := ref
	ref = s.LocalRef(ref)
	entries, err := s.ScanFresh(ref, true)
	if err != nil {
		return nil, err
	}
	if old, ok := s.Scans[ref]; ok && !EqualStrings(old, entries) {
		return nil, s.Unavailable("INPUT_DRIFT", "扫描集合变化: "+ref)
	}
	s.Scans[ref] = entries
	files := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e, "file:") {
			file := strings.TrimPrefix(e, "file:")
			if ref != logicalRef {
				mapped := false
				for original, stored := range s.Aliases {
					if stored == file {
						file = original
						mapped = true
						break
					}
				}
				if !mapped {
					return nil, s.Reject("SOURCE_MANIFEST", "来源目录含未登记文件: "+file)
				}
			}
			files = append(files, file)
		}
	}
	return files, nil
}

func EqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *SemanticSession) ScanFresh(ref string, observe bool) ([]string, error) {
	if s.V.Virtual != nil {
		return s.scanVirtual(ref, observe)
	}
	p, err := safefs.Path(s.Root, ref)
	if err != nil {
		return nil, s.Unavailable("PATH", err.Error())
	}
	if _, err = os.Lstat(p); os.IsNotExist(err) {
		err = nil
		if observe {
			_, err = s.V.Watch(ref)
		}
		return []string{"missing:" + ref}, err
	} else if err != nil {
		return nil, err
	}
	entries := []string{}
	count := 0
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = s.Guard(); e != nil {
			return e
		}
		count++
		if count > 20000 {
			return s.Unavailable("CI_SCOPE", "扫描超过20000项")
		}
		local, e := filepath.Rel(s.Root, path)
		if e != nil {
			return e
		}
		local = filepath.ToSlash(local)
		if _, e = safefs.Path(s.Root, local); e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if d.IsDir() {
			entries = append(entries, fmt.Sprintf("dir:%s:%o", local, info.Mode().Perm()))
			return nil
		}
		if !info.Mode().IsRegular() {
			return s.Unavailable("PATH", "扫描包含不支持的文件类型: "+local)
		}
		entries = append(entries, "file:"+local)
		if observe {
			_, e = s.V.Watch(local)
		}
		return e
	})
	if err != nil {
		return nil, s.Unavailable("INPUT", err.Error())
	}
	sort.Strings(entries)
	return entries, nil
}

func (s *SemanticSession) List(ref string) ([]string, error) {
	_, err := s.Scan(ref)
	if err != nil {
		return nil, err
	}
	if s.V.Virtual != nil {
		local := s.LocalRef(ref)
		set := map[string]bool{}
		for name := range s.V.Virtual {
			if strings.HasPrefix(name, local+"/") {
				set[strings.SplitN(strings.TrimPrefix(name, local+"/"), "/", 2)[0]] = true
			}
		}
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}
	p, err := safefs.Path(s.Root, s.LocalRef(ref))
	if err != nil {
		return nil, err
	}
	rows, err := os.ReadDir(p)
	if err != nil {
		return nil, s.Unavailable("INPUT", err.Error())
	}
	names := []string{}
	for _, r := range rows {
		names = append(names, r.Name())
	}
	return names, nil
}

func (s *SemanticSession) Verify(kind, ref string, opts map[string]string) error {
	if err := s.Guard(); err != nil {
		return err
	}
	if opts == nil {
		opts = map[string]string{}
	}
	encoded, _ := json.Marshal(opts)
	key := kind + "\x00" + ref + "\x00" + string(encoded)
	if s.active[key] {
		return s.Reject("REFERENCE_CYCLE", "未完成的语义校验形成循环: "+kind+"/"+ref)
	}
	fn, ok := SemanticValidators[kind]
	if !ok {
		return s.Unavailable("CAPABILITY", "尚缺原生语义校验能力: "+kind)
	}
	s.active[key] = true
	err := fn(s, ref, opts)
	delete(s.active, key)
	row := SemanticCheck{ID: kind, SourceRef: ref, Status: "passed"}
	if err != nil {
		row.Status = "failed"
		row.Code = SemanticCode(err)
	}
	s.Report.Checks = append(s.Report.Checks, row)
	return err
}

func SemanticCode(err error) string {
	var d *domain.Error
	if errors.As(err, &d) {
		return d.Code
	}
	return "INTERNAL"
}

func SemanticExit(err error) int {
	var d *domain.Error
	if errors.As(err, &d) && d.Exit != 0 {
		return d.Exit
	}
	return 2
}

func (s *SemanticSession) Diagnostic(ref string, err error) {
	if err == nil {
		return
	}
	s.Report.Diagnostics = append(s.Report.Diagnostics, SemanticDiagnostic{Code: SemanticCode(err), Message: err.Error(), SourceRef: ref, Recovery: "修复当前输入、能力或证据后重新校验"})
	if SemanticExit(err) == 2 {
		s.Report.Status = "error"
	} else if s.Report.Status != "error" {
		s.Report.Status = "failed"
	}
}

func (s *SemanticSession) Finish() error {
	if err := s.reobserveOnline(); err != nil {
		return err
	}
	return s.FinishInputs()
}

// Recheck live facts before any final file/scan checks, including child views.
// A live probe cannot change a parent file after its final observation.
func (s *SemanticSession) reobserveOnline() error {
	if err := s.Guard(); err != nil {
		return err
	}
	keys := make([]string, 0, len(s.finalObservations))
	for key := range s.finalObservations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := s.Guard(); err != nil {
			return err
		}
		for _, validate := range s.finalObservations[key] {
			if err := validate(); err != nil {
				return err
			}
		}
	}
	for _, child := range s.Children {
		if err := child.reobserveOnline(); err != nil {
			return err
		}
	}
	return s.Guard()
}

// Only fixed native validators register these callbacks. Never dispatch a
// recorded command. key must contain a non-secret identity and expected digest.
func (s *SemanticSession) ObserveOnline(key string, validate func() error) error {
	if key == "" || validate == nil {
		return s.Unavailable("INPUT", "在线观测缺少身份或原生校验器")
	}
	if err := s.Guard(); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	if s.finalObservations == nil {
		s.finalObservations = map[string][]func() error{}
	}
	s.finalObservations[key] = append(s.finalObservations[key], validate)
	return s.Guard()
}

func (s *SemanticSession) FinishInputs() error {
	if err := s.Guard(); err != nil {
		return err
	}
	if s.ContextInventory != nil {
		now, err := s.ContextFiles()
		if err != nil {
			return err
		}
		if !EqualStrings(s.ContextInventory, now) {
			return s.Unavailable("INPUT_DRIFT", "Context 扫描集合变化")
		}
	}
	for root, old := range s.IntakeInputs {
		now, err := s.IntakeSnapshot(root, 0)
		if err != nil {
			return err
		}
		if !ApEqual(now, old) {
			return s.Unavailable("INPUT_DRIFT", "只读分诊仓库观测变化: "+root)
		}
	}
	for _, old := range s.gitInputs {
		now, err := s.gitFresh(old.Root, old.Args)
		if err != nil {
			return err
		}
		if now.Output != old.Output || now.Exit != old.Exit {
			return s.Unavailable("INPUT_DRIFT", "核验结束时 Git 输入变化: "+old.Root+"/"+strings.Join(old.Args, " "))
		}
	}
	for _, v := range s.views() {
		root := v.Root
		for ref, old := range v.Observed {
			now, err := v.Watch(ref)
			if err != nil || now != old {
				return s.Unavailable("INPUT_DRIFT", "核验结束时输入变化: "+root+"/"+ref)
			}
		}
	}
	for ref, old := range s.Scans {
		now, err := s.ScanFresh(ref, false)
		if err != nil {
			return err
		}
		if !EqualStrings(old, now) {
			return s.Unavailable("INPUT_DRIFT", "核验结束时扫描集合变化: "+ref)
		}
	}
	for _, child := range s.Children {
		if err := child.FinishInputs(); err != nil {
			return err
		}
	}
	return s.Guard()
}

func (s *SemanticSession) views() []*View {
	// Roots can coincide (for example an explicitly registered scaffold root).
	// Never let a later view hide an earlier observation of the same path.
	all := []*View{s.V}
	for _, v := range s.ExternalViews {
		if v != s.V {
			all = append(all, v)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Root < all[j].Root })
	return all
}

func (s *SemanticSession) Inputs() []SemanticInput {
	rows := []SemanticInput{}
	for key := range s.finalObservations {
		rows = append(rows, SemanticInput{Root: s.Root, Ref: "online:" + key, Descriptor: domain.Descriptor{Type: "online"}})
	}
	for _, v := range s.views() {
		root := v.Root
		for ref, d := range v.Observed {
			rows = append(rows, SemanticInput{Root: root, Ref: ref, Descriptor: d})
		}
	}
	for _, child := range s.Children {
		rows = append(rows, child.Inputs()...)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Root == rows[j].Root {
			return rows[i].Ref < rows[j].Ref
		}
		return rows[i].Root < rows[j].Root
	})
	return rows
}

func (s *SemanticSession) CollectedChecks() []SemanticCheck {
	rows := make([]SemanticCheck, 0, len(s.Report.Checks))
	for _, row := range s.Report.Checks {
		if row.SourceRoot == "" {
			row.SourceRoot = s.Root
		}
		rows = append(rows, row)
	}
	for _, child := range s.Children {
		rows = append(rows, child.CollectedChecks()...)
	}
	return rows
}

func (s *SemanticSession) Authorities() error {
	for _, ref := range []string{".yss/asset-transactions/active.json", ".yss/asset-transactions/recovery.lock"} {
		d, err := s.V.Watch(s.LocalRef(ref))
		if err != nil {
			return err
		}
		if d.Type != "missing" {
			return s.Reject("ASSET_TRANSACTION_PENDING", "存在未完成资产事务: "+ref)
		}
	}
	if _, err := s.Scan(".yss/transactions"); err != nil {
		return err
	}
	if s.V.Virtual != nil || s.RuleSession != nil {
		exists, err := s.Exists(".yss/transactions")
		if err != nil {
			return err
		}
		if exists {
			return s.Unavailable("CAPABILITY", "归档来源含运行事务状态，不能由物理运行存储补齐")
		}
	} else {
		status, err := transaction.Status(s.Root)
		if err != nil {
			return s.Unavailable("INPUT", err.Error())
		}
		if len(status.Pending) > 0 || len(status.Preparations) > 0 {
			return s.Reject("TRANSACTION_PENDING", "存在未完成 Go 事务，先恢复")
		}
	}
	family := ""
	for name, profile := range domain.Profiles {
		d, err := s.V.Watch(s.LocalRef(profile.Metadata))
		if err != nil {
			return s.Unavailable("IDENTITY", err.Error())
		}
		if d.Type != "missing" {
			if family != "" {
				return s.Unavailable("IDENTITY", "发现多个旧 CLI 家族 metadata")
			}
			meta, err := s.Doc(profile.Metadata)
			if err != nil {
				return err
			}
			version, valid := Integer(meta["metadataSchemaVersion"])
			expected := int64(2)
			if name == "spec" {
				expected = 3
			}
			if !valid || version != expected || meta["templateSource"] != profile.TemplateSource || name != "spec" && meta["profileId"] != profile.ID {
				return s.Unavailable("IDENTITY", "未知旧 metadata 版本或来源身份矛盾")
			}
			if !CiCommit.MatchString(Text(meta["templateCommit"])) {
				return s.Unavailable("IDENTITY", "旧 metadata 缺少固定模板来源")
			}
			if _, valid := Object(meta["managedFiles"]); !valid {
				return s.Unavailable("IDENTITY", "旧 metadata 缺少受管基线")
			}
			if name != "spec" {
				raw, err := s.Bytes(profile.Metadata)
				if err != nil {
					return err
				}
				var fields map[string]json.RawMessage
				if err = json.Unmarshal(raw, &fields); err != nil {
					return s.Unavailable("IDENTITY", err.Error())
				}
				var compact bytes.Buffer
				if err = json.Compact(&compact, fields["managedFiles"]); err != nil || safefs.Digest(compact.Bytes()) != Text(meta["baselineDigest"]) {
					return s.Unavailable("BASELINE", "旧 metadata 基线摘要损坏")
				}
			}
			family = name
		}
	}
	native, err := s.V.Watch(s.LocalRef(domain.MetadataFile))
	if err != nil {
		return s.Unavailable("IDENTITY", err.Error())
	}
	if native.Type != "missing" {
		if _, err := s.Doc(domain.MetadataFile); err != nil {
			return err
		}
		raw, err := s.Bytes(domain.MetadataFile)
		if err != nil {
			return err
		}
		meta, err := identitymeta.ValidateNative(s.Root, raw, family)
		if err != nil {
			code := SemanticCode(err)
			if code == "INTERNAL" {
				code = "IDENTITY"
			}
			return s.Unavailable(code, err.Error())
		}
		family = meta.Profile
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	n, ok := Integer(identity["schema_version"])
	if !ok || n != 1 || !SemHas([]string{"template-source", "project-instance"}, Text(identity["repository_mode"])) {
		return s.Unavailable("IDENTITY", "未知仓库身份")
	}
	// 模板源仓根没有 Profile 身份文件；只有它自己（repository_mode: template-source）可以省略，实例缺失仍是错误。
	hasProfile, err := s.Exists(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	if hasProfile || Text(identity["repository_mode"]) != "template-source" {
		profile, err := s.Doc(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return err
		}
		version, valid := Integer(profile["schema_version"])
		if !valid || version < 1 || version > 2 {
			return s.Unavailable("IDENTITY", "未知 Profile schema")
		}
		matched := ""
		for name, p := range domain.Profiles {
			if p.ID == profile["profile_id"] {
				matched = name
			}
		}
		if matched == "" || family != "" && family != matched || s.Args["profile"] != "" && s.Args["profile"] != matched {
			return s.Unavailable("IDENTITY", "本地 Profile 与 metadata 或消费 Profile 矛盾")
		}
		s.Report.Profile = matched
	}
	s.Registry, err = s.Doc(".template-spec/process/lifecycle-registry.yaml")
	if err != nil {
		return err
	}
	s.Roles, err = s.Doc(".template-spec/agents/digital-human-roles.yaml")
	if err != nil {
		return err
	}
	for _, authority := range []map[string]any{s.Registry, s.Roles} {
		version, ok := Integer(authority["schema_version"])
		if !ok || version != 1 || authority["status"] != "active" {
			return s.Unavailable("CAPABILITY", "未知注册表/角色规则版本或非active状态")
		}
	}
	decisionPolicy, present := Object(s.Roles["user_decision_policy"])
	policyVersion, policyVersionValid := Integer(decisionPolicy["schema_version"])
	_, gatesValid := decisionPolicy["gates"].([]any)
	_, unitsValid := Object(decisionPolicy["work_units"])
	gatePolicy, gatePolicyPresent := Object(s.Roles["gate_policy"])
	if !present || !policyVersionValid || policyVersion != 1 || !gatesValid || !unitsValid || !gatePolicyPresent || gatePolicy["default_if_unlisted"] != "reject-unlisted" {
		return s.Unavailable("CAPABILITY", "缺失、未知或不完整的本地批准/用户决定规则，不能降为无需批准")
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/lifecycle-registry.schema.json", s.Registry); err != nil {
		return err
	}
	for _, bucket := range []string{"stages", "gates", "checks", "artifacts", "work_units", "evidence"} {
		ids := map[string]bool{}
		for _, row := range SemList(s.Registry[bucket]) {
			id := Text(SemMap(row)["id"])
			if id == "" || ids[id] {
				return s.Unavailable("CAPABILITY", "注册表稳定 ID 缺失或重复: "+bucket+"/"+id)
			}
			ids[id] = true
		}
	}
	return err
}

// A source session is used only for immutable payloads selected by a validated
// Handoff manifest. It shares cancellation and contributes to final input checks.
func (s *SemanticSession) sourceSession(prefix string) (*SemanticSession, error) {
	return s.sourceSessionBindings(prefix, map[string]string{"CONTEXT.md": "source-context.snapshot.md"})
}

// The caller supplies aliases only after verifying the immutable manifest's
// source inventory. Both sides remain confined to the source-session root.
func (s *SemanticSession) sourceSessionBindings(prefix string, bindings map[string]string) (*SemanticSession, error) {
	child, err := s.SourceSnapshotSession(prefix, bindings)
	if err != nil {
		return nil, err
	}
	if err = child.Authorities(); err != nil {
		return nil, err
	}
	return child, nil
}

// Handoff v3/v1 approval snapshots can carry only their source signing policy.
// The domain adapter must verify manifest/schema and source policy before using
// this unloaded view; no receiving or embedded authority is injected here.
func (s *SemanticSession) SourceSnapshotSession(prefix string, bindings map[string]string) (*SemanticSession, error) {
	storedPrefix := s.LocalRef(prefix)
	p, err := safefs.Path(s.Root, storedPrefix)
	if err != nil {
		return nil, err
	}
	if _, err = s.Scan(prefix); err != nil {
		return nil, err
	}
	// A receiving Profile/checkpoint describes the consumer, never the package
	// source. Retain only explicit tool/runtime IO roots for source validation.
	sourceArgs := map[string]string{}
	for _, key := range []string{"tool-root", "home", "run-dir"} {
		if value, ok := s.Args[key]; ok {
			sourceArgs[key] = value
		}
	}
	child := NewSemanticSession(s.Ctx, p, sourceArgs)
	if s.V.Virtual != nil {
		child.V.Virtual = map[string]semanticArchiveFile{}
		for ref, file := range s.V.Virtual {
			if strings.HasPrefix(ref, storedPrefix+"/") {
				child.V.Virtual[strings.TrimPrefix(ref, storedPrefix+"/")] = file
			}
		}
	}
	child.RuleSession = s
	storedAliases := map[string]string{}
	for original, stored := range bindings {
		if err = safefs.ValidateRef(original); err != nil {
			return nil, err
		}
		if _, err = safefs.Path(p, stored); err != nil {
			return nil, err
		}
		if old, exists := storedAliases[stored]; exists && old != original {
			return nil, s.Reject("SOURCE_MANIFEST", "来源映射存在歧义: "+stored)
		}
		storedAliases[stored] = original
		child.Aliases[original] = stored
	}
	s.Children = append(s.Children, child)
	return child, nil
}

// Registration semantics are checked by the Slice validator before this seam.
// This function additionally binds the registration and refuses unregistered IO.
func (s *SemanticSession) RegisterExternalRoot(root, registrationRef string) error {
	if s.V.Virtual != nil {
		return s.Reject("REPOSITORY", "归档来源不能授予物理工程访问权限")
	}
	if _, err := s.Bytes(registrationRef); err != nil {
		return err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if absolute != root {
		return s.Reject("REPOSITORY", "登记工程根须是规范绝对路径")
	}
	if _, err = safefs.Path(root, "yss-project.yaml"); err != nil {
		return err
	}
	if s.ExternalViews[root] == nil {
		s.ExternalViews[root] = NewView(root)
		s.ExternalViews[root].bindIdentity = true
	}
	return nil
}

func (s *SemanticSession) ExternalBytes(root, ref string) ([]byte, error) {
	if err := s.Guard(); err != nil {
		return nil, err
	}
	v := s.ExternalViews[root]
	if v == nil {
		return nil, s.Reject("REPOSITORY", "工程根未登记: "+root)
	}
	b, err := v.Read(ref)
	if err != nil {
		var d *domain.Error
		if errors.As(err, &d) && d.Code == "INPUT_DRIFT" {
			return nil, s.Unavailable("INPUT_DRIFT", err.Error())
		}
		return nil, s.Unavailable("INPUT", err.Error())
	}
	return b, nil
}

func (s *SemanticSession) externalDoc(root, ref string) (map[string]any, error) {
	b, err := s.ExternalBytes(root, ref)
	if err != nil {
		return nil, err
	}
	m, err := schema.Parse(b)
	if err != nil {
		return nil, err
	}
	out, ok := Object(m)
	if !ok {
		return nil, s.Reject("INPUT", "工程资产须是对象")
	}
	return out, nil
}
