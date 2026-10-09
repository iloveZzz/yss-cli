package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type semanticValidator func(*semanticSession, string, map[string]string) error

var semanticValidators = map[string]semanticValidator{}

func registerSemanticValidator(kind string, fn semanticValidator) {
	if kind == "" || fn == nil || semanticValidators[kind] != nil {
		panic("duplicate or invalid semantic validator: " + kind)
	}
	semanticValidators[kind] = fn
}

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
type semanticFailure struct {
	cause  error
	report *SemanticReport
}

func (e *semanticFailure) Error() string    { return e.cause.Error() }
func (e *semanticFailure) Unwrap() error    { return e.cause }
func (e *semanticFailure) ErrorResult() any { return e.report }

// InputFailureReport keeps the readonly contract even before a project view
// can be opened (for example, malformed arguments or conflicting identity).
func InputFailureReport(ctx context.Context, root, kind string, err error) *SemanticReport {
	s := newSemanticSession(ctx, root, nil)
	s.report.Kind = kind
	s.diagnostic("inputs", err)
	s.report.Status = "error"
	return s.report
}

type semanticSession struct {
	ctx           context.Context
	root          string
	v             *view
	args          map[string]string
	checkpointRef string
	registry      map[string]any
	roles         map[string]any
	report        *SemanticReport
	scans         map[string][]string
	active        map[string]bool
	externalViews map[string]*view
	aliases       map[string]string
	// All source sessions share these observations, including immutable Handoff payloads.
	children          []*semanticSession
	gitInputs         map[string]semanticGitObservation
	contextInventory  []string
	intakeInputs      map[string]map[string]any
	profileInput      map[string]any // Verified explicit Profile receipt, for readonly coordination.
	ruleSession       *semanticSession
	toolSource        *semanticSession
	finalObservations map[string][]func() error
}

func newSemanticSession(ctx context.Context, root string, args map[string]string) *semanticSession {
	if args == nil {
		args = map[string]string{}
	}
	v := newView(root)
	v.bindIdentity = true
	return &semanticSession{ctx: ctx, root: root, v: v, args: args, checkpointRef: args["checkpoint"],
		report: &SemanticReport{SchemaVersion: 1, Kind: "governance-semantic-verification", Status: "passed", ReadOnly: true,
			Scope: "current-native-governance", ApprovalCreated: false, ExecutionAuthorization: "not-evaluated",
			Checks: []SemanticCheck{}, Diagnostics: []SemanticDiagnostic{}, Inputs: []SemanticInput{}, Applicability: []map[string]any{}, GitInputs: []SemanticGitInput{}},
		scans: map[string][]string{}, active: map[string]bool{}, externalViews: map[string]*view{}, aliases: map[string]string{}, gitInputs: map[string]semanticGitObservation{}, intakeInputs: map[string]map[string]any{}}
}
func (s *semanticSession) reject(code, message string) error {
	return &domain.Error{Code: code, Message: message, Exit: 1}
}
func (s *semanticSession) unavailable(code, message string) error {
	return &domain.Error{Code: code, Message: message, Exit: 2}
}
func (s *semanticSession) guard() error {
	if s.ctx == nil {
		return s.unavailable("ARGUMENT", "取消上下文不可为空")
	}
	if err := s.ctx.Err(); err != nil {
		return s.unavailable("CANCELLED", err.Error())
	}
	return nil
}
func (s *semanticSession) localRef(ref string) string {
	if mapped, ok := s.aliases[ref]; ok {
		return mapped
	}
	return ref
}
func (s *semanticSession) bytes(ref string) ([]byte, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	v, local, err := s.referenceView(ref)
	if err != nil {
		var d *domain.Error
		if errors.As(err, &d) && d.Exit == 2 {
			return nil, err
		}
		return nil, s.unavailable("PATH", err.Error())
	}
	b, err := v.read(local)
	if err != nil {
		var d *domain.Error
		if errors.As(err, &d) && d.Code == "INPUT_DRIFT" {
			return nil, s.unavailable("INPUT_DRIFT", err.Error())
		}
		return nil, s.unavailable("INPUT", err.Error())
	}
	return b, nil
}
func (s *semanticSession) doc(ref string) (map[string]any, error) {
	b, err := s.bytes(ref)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(ref, ".md") {
		b, _, err = frontmatter(b)
		if err != nil {
			return nil, s.unavailable("INPUT", err.Error())
		}
	}
	v, err := schema.Parse(b)
	if err != nil {
		return nil, s.unavailable("INPUT", ref+": "+err.Error())
	}
	m, ok := object(v)
	if !ok {
		return nil, s.unavailable("INPUT", ref+": 必须是对象")
	}
	return m, nil
}
func (s *semanticSession) exists(ref string) (bool, error) {
	if err := s.guard(); err != nil {
		return false, err
	}
	v, local, err := s.referenceView(ref)
	if err != nil {
		return false, err
	}
	if v.virtual != nil {
		d, e := v.watch(local)
		return e == nil && d.Type != "missing", e
	}
	p, err := safefs.Path(v.root, local)
	if err != nil {
		return false, s.unavailable("PATH", err.Error())
	}
	st, err := os.Lstat(p)
	if os.IsNotExist(err) {
		_, e := v.watch(local)
		return false, e
	}
	if err != nil {
		return false, s.unavailable("INPUT", err.Error())
	}
	if st.IsDir() {
		inspector := s
		if v != s.v {
			inspector = newSemanticSession(s.ctx, v.root, s.args)
			s.children = append(s.children, inspector)
		}
		_, err = inspector.scan(local)
		return err == nil, err
	}
	_, err = v.watch(local)
	return err == nil, err
}
func (s *semanticSession) bind(ref string) (Binding, error) {
	b, err := s.bytes(ref)
	if err != nil {
		return Binding{}, err
	}
	return Binding{Ref: ref, Digest: "sha256:" + safefs.Digest(b)}, nil
}
func (s *semanticSession) validateSchema(ref string, value any) error {
	if s.ruleSession != nil {
		// The enclosing wrapper/Receipt belongs to its receiver. A frozen
		// source registry and checkpoint belong to the source Profile already
		// identified by authorities, even through several nested packages.
		if s.report.Profile != "" && (ref == ".template-spec/process/schemas/lifecycle-registry.schema.json" || ref == ".template-spec/process/schemas/lifecycle-checkpoint.schema.json") {
			return s.validateImmutableSourceSchema(ref, value)
		}
		return s.ruleSession.validateSchema(ref, value)
	}
	return s.validateSchemaInView(ref, value)
}

func (s *semanticSession) validateSchemaInView(ref string, value any) error {
	if err := s.guard(); err != nil {
		return err
	}
	path, err := safefs.Path(s.root, s.localRef(ref))
	if err != nil {
		return s.unavailable("PATH", err.Error())
	}
	issues, err := schema.ValidateValueWithReader(path, value, func(file string) ([]byte, error) {
		local, e := filepath.Rel(s.root, file)
		if e != nil {
			return nil, e
		}
		return s.bytes(filepath.ToSlash(local))
	})
	if err != nil {
		return s.unavailable("CAPABILITY", ref+": "+err.Error())
	}
	if len(issues) > 0 {
		return s.reject("SCHEMA", fmt.Sprintf("%s: %v", ref, issues))
	}
	return s.guard()
}

// Historical source snapshots need not contain schemas. Their original
// registry, roles, approvals and replies remain the authority; the supported
// CLI Bundle supplies only the corresponding Profile's strict syntax. A
// captured schema is observed and validated too, and can never replace this
// canonical constraint with a permissive or unsupported schema.
func (s *semanticSession) validateImmutableSourceSchema(ref string, value any) error {
	if err := s.guard(); err != nil {
		return err
	}
	present, err := s.exists(ref)
	if err != nil {
		return err
	}
	if present {
		if err = s.validateSchemaInView(ref, value); err != nil {
			return err
		}
	}
	b, err := bundle.Load(s.report.Profile)
	if err != nil {
		return s.unavailable("CAPABILITY", "不支持冻结来源 Profile 的结构校验："+err.Error())
	}
	p, err := safefs.Path(s.root, ref)
	if err != nil {
		return s.unavailable("PATH", err.Error())
	}
	issues, err := schema.ValidateValueWithReader(p, value, func(file string) ([]byte, error) {
		local, e := filepath.Rel(s.root, file)
		if e != nil || !strings.HasPrefix(filepath.ToSlash(local), ".template-spec/process/schemas/") {
			return nil, fmt.Errorf("来源结构 schema 依赖越界：%s", file)
		}
		f, ok := b.Files[filepath.ToSlash(local)]
		if !ok {
			return nil, fmt.Errorf("受信 %s Bundle 缺少结构 schema：%s", s.report.Profile, local)
		}
		return f.Render(nil)
	})
	if err != nil {
		return s.unavailable("CAPABILITY", ref+": "+err.Error())
	}
	if len(issues) > 0 {
		return s.reject("SCHEMA", fmt.Sprintf("%s/%s: %v", s.report.Profile, ref, issues))
	}
	return s.guard()
}
func (s *semanticSession) basis(value any) error {
	rows, ok := value.([]any)
	if !ok || len(rows) == 0 {
		return s.reject("EVIDENCE_REQUIRED", "缺少当前依据")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		m, ok := object(row)
		if !ok {
			return s.reject("EVIDENCE_INVALID", "依据必须为对象")
		}
		ref := text(m["ref"])
		if err := rejectProgressionEvidence(s, ref); err != nil {
			return err
		}
		digest := strings.TrimPrefix(text(m["digest"]), "sha256:")
		if ref == "" || seen[ref] || !isSHA256(digest) {
			return s.reject("EVIDENCE_INVALID", "依据引用或摘要缺失、重复或非法")
		}
		seen[ref] = true
		b, err := s.bytes(ref)
		if err != nil {
			return err
		}
		if safefs.Digest(b) != digest {
			return s.reject("EVIDENCE_DRIFT", "依据过期: "+ref)
		}
	}
	return nil
}
func isSHA256(v string) bool {
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
func semMap(v any) map[string]any {
	m, _ := object(v)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func semList(v any) []any { a, _ := v.([]any); return a }
func semStrings(v any) []string {
	a := []string{}
	switch v := v.(type) {
	case []any:
		for _, x := range v {
			a = append(a, text(x))
		}
	case []string:
		a = append(a, v...)
	}
	return a
}
func semHas(v any, w string) bool {
	for _, x := range semStrings(v) {
		if x == w {
			return true
		}
	}
	return false
}
func semSameSet(a, b any) bool {
	x, y := semStrings(a), semStrings(b)
	if len(x) == 0 || len(x) != len(y) {
		return false
	}
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] == "" || x[i] != y[i] || i > 0 && x[i] == x[i-1] {
			return false
		}
	}
	return true
}
func semanticOptions(args map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range args {
		out[k] = v
	}
	return out
}

// scan records directories as well as files: even additions to an empty subtree
// invalidate a claimed current result. Files are also observed by bytes and mode.
func (s *semanticSession) scan(ref string) ([]string, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	logicalRef := ref
	ref = s.localRef(ref)
	entries, err := s.scanFresh(ref, true)
	if err != nil {
		return nil, err
	}
	if old, ok := s.scans[ref]; ok && !equalStrings(old, entries) {
		return nil, s.unavailable("INPUT_DRIFT", "扫描集合变化: "+ref)
	}
	s.scans[ref] = entries
	files := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e, "file:") {
			file := strings.TrimPrefix(e, "file:")
			if ref != logicalRef {
				mapped := false
				for original, stored := range s.aliases {
					if stored == file {
						file = original
						mapped = true
						break
					}
				}
				if !mapped {
					return nil, s.reject("SOURCE_MANIFEST", "来源目录含未登记文件: "+file)
				}
			}
			files = append(files, file)
		}
	}
	return files, nil
}
func equalStrings(a, b []string) bool {
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
func (s *semanticSession) scanFresh(ref string, observe bool) ([]string, error) {
	if s.v.virtual != nil {
		return s.scanVirtual(ref, observe)
	}
	p, err := safefs.Path(s.root, ref)
	if err != nil {
		return nil, s.unavailable("PATH", err.Error())
	}
	if _, err = os.Lstat(p); os.IsNotExist(err) {
		err = nil
		if observe {
			_, err = s.v.watch(ref)
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
		if e = s.guard(); e != nil {
			return e
		}
		count++
		if count > 20000 {
			return s.unavailable("CI_SCOPE", "扫描超过20000项")
		}
		local, e := filepath.Rel(s.root, path)
		if e != nil {
			return e
		}
		local = filepath.ToSlash(local)
		if _, e = safefs.Path(s.root, local); e != nil {
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
			return s.unavailable("PATH", "扫描包含不支持的文件类型: "+local)
		}
		entries = append(entries, "file:"+local)
		if observe {
			_, e = s.v.watch(local)
		}
		return e
	})
	if err != nil {
		return nil, s.unavailable("INPUT", err.Error())
	}
	sort.Strings(entries)
	return entries, nil
}
func (s *semanticSession) list(ref string) ([]string, error) {
	_, err := s.scan(ref)
	if err != nil {
		return nil, err
	}
	if s.v.virtual != nil {
		local := s.localRef(ref)
		set := map[string]bool{}
		for name := range s.v.virtual {
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
	p, err := safefs.Path(s.root, s.localRef(ref))
	if err != nil {
		return nil, err
	}
	rows, err := os.ReadDir(p)
	if err != nil {
		return nil, s.unavailable("INPUT", err.Error())
	}
	names := []string{}
	for _, r := range rows {
		names = append(names, r.Name())
	}
	return names, nil
}
func (s *semanticSession) verify(kind, ref string, opts map[string]string) error {
	if err := s.guard(); err != nil {
		return err
	}
	if opts == nil {
		opts = map[string]string{}
	}
	encoded, _ := json.Marshal(opts)
	key := kind + "\x00" + ref + "\x00" + string(encoded)
	if s.active[key] {
		return s.reject("REFERENCE_CYCLE", "未完成的语义校验形成循环: "+kind+"/"+ref)
	}
	fn, ok := semanticValidators[kind]
	if !ok {
		return s.unavailable("CAPABILITY", "尚缺原生语义校验能力: "+kind)
	}
	s.active[key] = true
	err := fn(s, ref, opts)
	delete(s.active, key)
	row := SemanticCheck{ID: kind, SourceRef: ref, Status: "passed"}
	if err != nil {
		row.Status = "failed"
		row.Code = semanticCode(err)
	}
	s.report.Checks = append(s.report.Checks, row)
	return err
}
func semanticCode(err error) string {
	var d *domain.Error
	if errors.As(err, &d) {
		return d.Code
	}
	return "INTERNAL"
}
func semanticExit(err error) int {
	var d *domain.Error
	if errors.As(err, &d) && d.Exit != 0 {
		return d.Exit
	}
	return 2
}
func (s *semanticSession) diagnostic(ref string, err error) {
	if err == nil {
		return
	}
	s.report.Diagnostics = append(s.report.Diagnostics, SemanticDiagnostic{Code: semanticCode(err), Message: err.Error(), SourceRef: ref, Recovery: "修复当前输入、能力或证据后重新校验"})
	if semanticExit(err) == 2 {
		s.report.Status = "error"
	} else if s.report.Status != "error" {
		s.report.Status = "failed"
	}
}
func (s *semanticSession) finish() error {
	if err := s.reobserveOnline(); err != nil {
		return err
	}
	return s.finishInputs()
}

// Recheck live facts before any final file/scan checks, including child views.
// A live probe cannot change a parent file after its final observation.
func (s *semanticSession) reobserveOnline() error {
	if err := s.guard(); err != nil {
		return err
	}
	keys := make([]string, 0, len(s.finalObservations))
	for key := range s.finalObservations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := s.guard(); err != nil {
			return err
		}
		for _, validate := range s.finalObservations[key] {
			if err := validate(); err != nil {
				return err
			}
		}
	}
	for _, child := range s.children {
		if err := child.reobserveOnline(); err != nil {
			return err
		}
	}
	return s.guard()
}

// Only fixed native validators register these callbacks. Never dispatch a
// recorded command. key must contain a non-secret identity and expected digest.
func (s *semanticSession) observeOnline(key string, validate func() error) error {
	if key == "" || validate == nil {
		return s.unavailable("INPUT", "在线观测缺少身份或原生校验器")
	}
	if err := s.guard(); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	if s.finalObservations == nil {
		s.finalObservations = map[string][]func() error{}
	}
	s.finalObservations[key] = append(s.finalObservations[key], validate)
	return s.guard()
}

func (s *semanticSession) finishInputs() error {
	if err := s.guard(); err != nil {
		return err
	}
	if s.contextInventory != nil {
		now, err := s.contextFiles()
		if err != nil {
			return err
		}
		if !equalStrings(s.contextInventory, now) {
			return s.unavailable("INPUT_DRIFT", "Context 扫描集合变化")
		}
	}
	for root, old := range s.intakeInputs {
		now, err := s.intakeSnapshot(root, 0)
		if err != nil {
			return err
		}
		if !apEqual(now, old) {
			return s.unavailable("INPUT_DRIFT", "只读分诊仓库观测变化: "+root)
		}
	}
	for _, old := range s.gitInputs {
		now, err := s.gitFresh(old.Root, old.Args)
		if err != nil {
			return err
		}
		if now.Output != old.Output || now.Exit != old.Exit {
			return s.unavailable("INPUT_DRIFT", "核验结束时 Git 输入变化: "+old.Root+"/"+strings.Join(old.Args, " "))
		}
	}
	for _, v := range s.views() {
		root := v.root
		for ref, old := range v.observed {
			now, err := v.watch(ref)
			if err != nil || now != old {
				return s.unavailable("INPUT_DRIFT", "核验结束时输入变化: "+root+"/"+ref)
			}
		}
	}
	for ref, old := range s.scans {
		now, err := s.scanFresh(ref, false)
		if err != nil {
			return err
		}
		if !equalStrings(old, now) {
			return s.unavailable("INPUT_DRIFT", "核验结束时扫描集合变化: "+ref)
		}
	}
	for _, child := range s.children {
		if err := child.finishInputs(); err != nil {
			return err
		}
	}
	return s.guard()
}
func (s *semanticSession) views() []*view {
	// Roots can coincide (for example an explicitly registered scaffold root).
	// Never let a later view hide an earlier observation of the same path.
	all := []*view{s.v}
	for _, v := range s.externalViews {
		if v != s.v {
			all = append(all, v)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].root < all[j].root })
	return all
}
func (s *semanticSession) inputs() []SemanticInput {
	rows := []SemanticInput{}
	for key := range s.finalObservations {
		rows = append(rows, SemanticInput{Root: s.root, Ref: "online:" + key, Descriptor: domain.Descriptor{Type: "online"}})
	}
	for _, v := range s.views() {
		root := v.root
		for ref, d := range v.observed {
			rows = append(rows, SemanticInput{Root: root, Ref: ref, Descriptor: d})
		}
	}
	for _, child := range s.children {
		rows = append(rows, child.inputs()...)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Root == rows[j].Root {
			return rows[i].Ref < rows[j].Ref
		}
		return rows[i].Root < rows[j].Root
	})
	return rows
}

func (s *semanticSession) collectedChecks() []SemanticCheck {
	rows := make([]SemanticCheck, 0, len(s.report.Checks))
	for _, row := range s.report.Checks {
		if row.SourceRoot == "" {
			row.SourceRoot = s.root
		}
		rows = append(rows, row)
	}
	for _, child := range s.children {
		rows = append(rows, child.collectedChecks()...)
	}
	return rows
}
func (s *semanticSession) authorities() error {
	for _, ref := range []string{".yss/asset-transactions/active.json", ".yss/asset-transactions/recovery.lock"} {
		d, err := s.v.watch(s.localRef(ref))
		if err != nil {
			return err
		}
		if d.Type != "missing" {
			return s.reject("ASSET_TRANSACTION_PENDING", "存在未完成资产事务: "+ref)
		}
	}
	if _, err := s.scan(".yss/transactions"); err != nil {
		return err
	}
	if s.v.virtual != nil || s.ruleSession != nil {
		exists, err := s.exists(".yss/transactions")
		if err != nil {
			return err
		}
		if exists {
			return s.unavailable("CAPABILITY", "归档来源含运行事务状态，不能由物理运行存储补齐")
		}
	} else {
		status, err := transaction.Status(s.root)
		if err != nil {
			return s.unavailable("INPUT", err.Error())
		}
		if len(status.Pending) > 0 || len(status.Preparations) > 0 {
			return s.reject("TRANSACTION_PENDING", "存在未完成 Go 事务，先恢复")
		}
	}
	family := ""
	for name, profile := range domain.Profiles {
		d, err := s.v.watch(s.localRef(profile.Metadata))
		if err != nil {
			return s.unavailable("IDENTITY", err.Error())
		}
		if d.Type != "missing" {
			if family != "" {
				return s.unavailable("IDENTITY", "发现多个旧 CLI 家族 metadata")
			}
			meta, err := s.doc(profile.Metadata)
			if err != nil {
				return err
			}
			version, valid := integer(meta["metadataSchemaVersion"])
			expected := int64(2)
			if name == "spec" {
				expected = 3
			}
			if !valid || version != expected || meta["templateSource"] != profile.TemplateSource || name != "spec" && meta["profileId"] != profile.ID {
				return s.unavailable("IDENTITY", "未知旧 metadata 版本或来源身份矛盾")
			}
			if !ciCommit.MatchString(text(meta["templateCommit"])) {
				return s.unavailable("IDENTITY", "旧 metadata 缺少固定模板来源")
			}
			if _, valid := object(meta["managedFiles"]); !valid {
				return s.unavailable("IDENTITY", "旧 metadata 缺少受管基线")
			}
			if name != "spec" {
				raw, err := s.bytes(profile.Metadata)
				if err != nil {
					return err
				}
				var fields map[string]json.RawMessage
				if err = json.Unmarshal(raw, &fields); err != nil {
					return s.unavailable("IDENTITY", err.Error())
				}
				var compact bytes.Buffer
				if err = json.Compact(&compact, fields["managedFiles"]); err != nil || safefs.Digest(compact.Bytes()) != text(meta["baselineDigest"]) {
					return s.unavailable("BASELINE", "旧 metadata 基线摘要损坏")
				}
			}
			family = name
		}
	}
	native, err := s.v.watch(s.localRef(domain.MetadataFile))
	if err != nil {
		return s.unavailable("IDENTITY", err.Error())
	}
	if native.Type != "missing" {
		if _, err := s.doc(domain.MetadataFile); err != nil {
			return err
		}
		raw, err := s.bytes(domain.MetadataFile)
		if err != nil {
			return err
		}
		meta, err := identitymeta.ValidateNative(s.root, raw, family)
		if err != nil {
			code := semanticCode(err)
			if code == "INTERNAL" {
				code = "IDENTITY"
			}
			return s.unavailable(code, err.Error())
		}
		family = meta.Profile
	}
	profile, err := s.doc(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	version, valid := integer(profile["schema_version"])
	if !valid || version < 1 || version > 2 {
		return s.unavailable("IDENTITY", "未知 Profile schema")
	}
	matched := ""
	for name, p := range domain.Profiles {
		if p.ID == profile["profile_id"] {
			matched = name
		}
	}
	if matched == "" || family != "" && family != matched || s.args["profile"] != "" && s.args["profile"] != matched {
		return s.unavailable("IDENTITY", "本地 Profile 与 metadata 或消费 Profile 矛盾")
	}
	s.report.Profile = matched
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return err
	}
	n, ok := integer(identity["schema_version"])
	if !ok || n != 1 || !semHas([]string{"template-source", "project-instance"}, text(identity["repository_mode"])) {
		return s.unavailable("IDENTITY", "未知仓库身份")
	}
	s.registry, err = s.doc(".template-spec/process/lifecycle-registry.yaml")
	if err != nil {
		return err
	}
	s.roles, err = s.doc(".template-spec/agents/digital-human-roles.yaml")
	if err != nil {
		return err
	}
	for _, authority := range []map[string]any{s.registry, s.roles} {
		version, ok := integer(authority["schema_version"])
		if !ok || version != 1 || authority["status"] != "active" {
			return s.unavailable("CAPABILITY", "未知注册表/角色规则版本或非active状态")
		}
	}
	decisionPolicy, present := object(s.roles["user_decision_policy"])
	policyVersion, policyVersionValid := integer(decisionPolicy["schema_version"])
	_, gatesValid := decisionPolicy["gates"].([]any)
	_, unitsValid := object(decisionPolicy["work_units"])
	gatePolicy, gatePolicyPresent := object(s.roles["gate_policy"])
	if !present || !policyVersionValid || policyVersion != 1 || !gatesValid || !unitsValid || !gatePolicyPresent || gatePolicy["default_if_unlisted"] != "reject-unlisted" {
		return s.unavailable("CAPABILITY", "缺失、未知或不完整的本地批准/用户决定规则，不能降为无需批准")
	}
	if err = s.validateSchema(".template-spec/process/schemas/lifecycle-registry.schema.json", s.registry); err != nil {
		return err
	}
	for _, bucket := range []string{"stages", "gates", "checks", "artifacts", "work_units", "evidence"} {
		ids := map[string]bool{}
		for _, row := range semList(s.registry[bucket]) {
			id := text(semMap(row)["id"])
			if id == "" || ids[id] {
				return s.unavailable("CAPABILITY", "注册表稳定 ID 缺失或重复: "+bucket+"/"+id)
			}
			ids[id] = true
		}
	}
	return err
}

// A source session is used only for immutable payloads selected by a validated
// Handoff manifest. It shares cancellation and contributes to final input checks.
func (s *semanticSession) sourceSession(prefix string) (*semanticSession, error) {
	return s.sourceSessionBindings(prefix, map[string]string{"CONTEXT.md": "source-context.snapshot.md"})
}

// The caller supplies aliases only after verifying the immutable manifest's
// source inventory. Both sides remain confined to the source-session root.
func (s *semanticSession) sourceSessionBindings(prefix string, bindings map[string]string) (*semanticSession, error) {
	child, err := s.sourceSnapshotSession(prefix, bindings)
	if err != nil {
		return nil, err
	}
	if err = child.authorities(); err != nil {
		return nil, err
	}
	return child, nil
}

// Handoff v3/v1 approval snapshots can carry only their source signing policy.
// The domain adapter must verify manifest/schema and source policy before using
// this unloaded view; no receiving or embedded authority is injected here.
func (s *semanticSession) sourceSnapshotSession(prefix string, bindings map[string]string) (*semanticSession, error) {
	storedPrefix := s.localRef(prefix)
	p, err := safefs.Path(s.root, storedPrefix)
	if err != nil {
		return nil, err
	}
	if _, err = s.scan(prefix); err != nil {
		return nil, err
	}
	// A receiving Profile/checkpoint describes the consumer, never the package
	// source. Retain only explicit tool/runtime IO roots for source validation.
	sourceArgs := map[string]string{}
	for _, key := range []string{"tool-root", "home", "run-dir"} {
		if value, ok := s.args[key]; ok {
			sourceArgs[key] = value
		}
	}
	child := newSemanticSession(s.ctx, p, sourceArgs)
	if s.v.virtual != nil {
		child.v.virtual = map[string]semanticArchiveFile{}
		for ref, file := range s.v.virtual {
			if strings.HasPrefix(ref, storedPrefix+"/") {
				child.v.virtual[strings.TrimPrefix(ref, storedPrefix+"/")] = file
			}
		}
	}
	child.ruleSession = s
	storedAliases := map[string]string{}
	for original, stored := range bindings {
		if err = safefs.ValidateRef(original); err != nil {
			return nil, err
		}
		if _, err = safefs.Path(p, stored); err != nil {
			return nil, err
		}
		if old, exists := storedAliases[stored]; exists && old != original {
			return nil, s.reject("SOURCE_MANIFEST", "来源映射存在歧义: "+stored)
		}
		storedAliases[stored] = original
		child.aliases[original] = stored
	}
	s.children = append(s.children, child)
	return child, nil
}

// Registration semantics are checked by the Slice validator before this seam.
// This function additionally binds the registration and refuses unregistered IO.
func (s *semanticSession) registerExternalRoot(root, registrationRef string) error {
	if s.v.virtual != nil {
		return s.reject("REPOSITORY", "归档来源不能授予物理工程访问权限")
	}
	if _, err := s.bytes(registrationRef); err != nil {
		return err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if absolute != root {
		return s.reject("REPOSITORY", "登记工程根须是规范绝对路径")
	}
	if _, err = safefs.Path(root, "yss-project.yaml"); err != nil {
		return err
	}
	if s.externalViews[root] == nil {
		s.externalViews[root] = newView(root)
		s.externalViews[root].bindIdentity = true
	}
	return nil
}
func (s *semanticSession) externalBytes(root, ref string) ([]byte, error) {
	if err := s.guard(); err != nil {
		return nil, err
	}
	v := s.externalViews[root]
	if v == nil {
		return nil, s.reject("REPOSITORY", "工程根未登记: "+root)
	}
	b, err := v.read(ref)
	if err != nil {
		var d *domain.Error
		if errors.As(err, &d) && d.Code == "INPUT_DRIFT" {
			return nil, s.unavailable("INPUT_DRIFT", err.Error())
		}
		return nil, s.unavailable("INPUT", err.Error())
	}
	return b, nil
}
func (s *semanticSession) externalDoc(root, ref string) (map[string]any, error) {
	b, err := s.externalBytes(root, ref)
	if err != nil {
		return nil, err
	}
	m, err := schema.Parse(b)
	if err != nil {
		return nil, err
	}
	out, ok := object(m)
	if !ok {
		return nil, s.reject("INPUT", "工程资产须是对象")
	}
	return out, nil
}

func semanticRun(ctx context.Context, group, action, root string, args map[string]string) (any, error) {
	s := newSemanticSession(ctx, root, args)
	ref := args["file"]
	if ref == "" {
		ref = args["arg0"]
	}
	if group == "lifecycle" {
		ref = args["checkpoint"]
		if ref == "" {
			ref = args["file"]
		}
	}
	if group == "handoff" && args["kind"] == "package" {
		ref = args["package"]
	}
	kind := args["kind"]
	switch group {
	case "lifecycle":
		kind = "checkpoint"
	case "handoff":
		kind = "handoff-" + kind
	}
	err := s.guard()
	if err == nil {
		allowed := map[string][]string{"contract": {"slice", "scaffold", "task", "frontend-delivery"}, "evidence": {"approval", "user-decision", "verification"}, "handoff": {"package", "consumption"}}
		if kinds, ok := allowed[group]; ok && !semHas(kinds, args["kind"]) {
			err = s.unavailable("ARGUMENT", "未知或不适用于该接口的领域类型: "+args["kind"])
		}
	}
	if err == nil {
		allowedFlags := map[string]bool{}
		for _, flag := range []string{"root", "target-dir", "profile", "json", "kind", "file", "arg0", "checkpoint", "home", "run-dir", "tool-root", "template-checkout"} {
			allowedFlags[flag] = true
		}
		for _, flag := range map[string][]string{
			"slice": {"approval-ref", "unit"}, "verification": {"approval-ref", "task"},
			"frontend-delivery": {"slice", "phase", "unit"},
			"task":              {"history"}, "approval": {"require-approved", "history", "gate", "boundary", "task"},
			"user-decision":   {"requirements", "continuation", "task"},
			"handoff-package": {"package"}, "handoff-consumption": {"consumer", "slice"}, "checkpoint": {"history"},
		}[kind] {
			allowedFlags[flag] = true
		}
		for flag := range args {
			if !allowedFlags[flag] {
				err = s.unavailable("ARGUMENT", "该公开治理接口不接受参数: --"+flag)
				break
			}
		}
	}
	if err == nil {
		for flag, kinds := range map[string][]string{"requirements": {"user-decision"}, "continuation": {"user-decision"}, "gate": {"approval"}, "boundary": {"approval"}, "task": {"approval", "user-decision", "verification"}, "consumer": {"handoff-consumption"}, "package": {"handoff-package"}, "require-approved": {"approval"}, "history": {"approval", "task", "checkpoint"}} {
			if _, provided := args[flag]; provided && !semHas(kinds, kind) {
				err = s.unavailable("ARGUMENT", "该领域类型不接受参数: --"+flag)
				break
			}
		}
	}
	if err == nil {
		selectors := []string{"file", "arg0"}
		if group == "lifecycle" {
			selectors = append(selectors, "checkpoint")
		}
		if kind == "handoff-package" {
			selectors = append(selectors, "package")
		}
		selected := ""
		for _, key := range selectors {
			value := args[key]
			if value != "" {
				if selected != "" && selected != value {
					err = s.unavailable("ARGUMENT", "同一资产的路径参数矛盾")
					break
				}
				selected = value
			}
		}
	}
	if err == nil && args["gate"] != "" && args["boundary"] != "" && args["gate"] != args["boundary"] {
		err = s.unavailable("ARGUMENT", "gate 与 boundary 参数矛盾")
	}
	if err == nil && args["schema"] != "" {
		err = s.unavailable("ARGUMENT", "verify 不允许替换领域 Schema")
	}
	if err == nil && ref == "" {
		err = s.unavailable("ARGUMENT", "语义校验需要明确资产路径")
	}
	if err == nil {
		err = s.authorities()
	}
	if err == nil {
		_, err = s.contextContract()
	}
	if err == nil {
		err = s.verify(kind, ref, args)
	}
	if err != nil {
		s.diagnostic(ref, err)
	}
	if final := s.finish(); final != nil {
		s.diagnostic(ref, final)
		err = final
	}
	s.report.Inputs = s.inputs()
	s.report.GitInputs = s.gitBindings()
	s.report.Checks = s.collectedChecks()
	if err != nil {
		return s.report, &semanticFailure{cause: err, report: s.report}
	}
	if kind == "frontend-delivery" && s.report.Coverage["delivery_mode"] == "local-approved-assets" {
		s.report.Coverage["inputs_current"] = true
	}
	return s.report, nil
}
