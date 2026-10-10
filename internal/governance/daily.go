package governance

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const dailyPolicyRef = ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"
const dailyMarker = "<!-- yss-task-evidence -->"

type DailyReport struct {
	*SemanticReport
	DeliveryPath      string           `json:"delivery_path"`
	TaskID            string           `json:"task_id,omitempty"`
	CandidateDigest   string           `json:"candidate_digest,omitempty"`
	DiffDigest        string           `json:"diff_digest,omitempty"`
	ChangedFiles      []string         `json:"changed_files"`
	APIContractDigest string           `json:"api_digest,omitempty"`
	TestsExecuted     bool             `json:"tests_executed"`
	Reasons           []string         `json:"reasons"`
	RequiredChecks    []map[string]any `json:"required_checks"`
}
type dailyFailure struct {
	cause  error
	report *DailyReport
}

func (e *dailyFailure) Error() string    { return e.cause.Error() }
func (e *dailyFailure) Unwrap() error    { return e.cause }
func (e *dailyFailure) ErrorResult() any { return e.report }

type dailySession struct {
	s                   *semanticSession
	report              *DailyReport
	record              map[string]any
	body                string
	taskRef, repo, base string
	basis               map[string]string
	policy              map[string]any
	wireApplicable      bool
}

func dailyRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	s := newSemanticSession(ctx, root, args)
	s.report.Kind = "lifecycle." + action
	s.report.Scope = "daily-record-and-current-git-only"
	d := &dailySession{s: s, report: &DailyReport{SemanticReport: s.report, DeliveryPath: "needs-info", ChangedFiles: []string{}}, basis: map[string]string{}}
	err := d.run(action, args)
	if freshErr := s.finish(); freshErr != nil {
		err = freshErr
	}
	s.report.Inputs = s.inputs()
	s.report.GitInputs = s.gitBindings()
	if err != nil {
		s.diagnostic(d.taskRef, err)
		return d.report, &dailyFailure{err, d.report}
	}
	return d.report, nil
}
func (d *dailySession) info(message string) error {
	d.report.DeliveryPath = "needs-info"
	return d.s.unavailable("NEEDS_INFO", message)
}
func (d *dailySession) rejected(message string) error { return d.s.reject("DAILY_EVIDENCE", message) }
func (d *dailySession) govern(action, message string) error {
	d.report.DeliveryPath = "governed"
	d.report.Reasons = append(d.report.Reasons, message)
	d.report.Diagnostics = append(d.report.Diagnostics, SemanticDiagnostic{Code: "GOVERNED_REQUIRED", Message: message, SourceRef: d.taskRef, Recovery: "保留已有编辑和证据，恢复最近可信正式阶段"})
	if action == "verify-daily" {
		return d.s.reject("GOVERNED_REQUIRED", message)
	}
	return nil
}
func (d *dailySession) run(action string, args map[string]string) error {
	if action != "route" && action != "verify-daily" {
		return d.s.unavailable("UNPORTED", "不支持的日常动作")
	}
	for _, key := range []string{"root", "task", "implementation-root", "base"} {
		if strings.TrimSpace(args[key]) == "" {
			return d.s.unavailable("ARGUMENT", "需要显式 --"+key)
		}
	}
	if !ciCommit.MatchString(args["base"]) {
		return d.s.unavailable("ARGUMENT", "--base 必须是完整小写 Git SHA")
	}
	d.taskRef = args["task"]
	d.base = args["base"]
	if !strings.HasSuffix(d.taskRef, ".md") {
		return d.info("日常任务必须是单一 Markdown 记录")
	}
	if err := d.identity(args["profile"]); err != nil {
		return err
	}
	if err := d.readPolicy(); err != nil {
		return err
	}
	repo, err := filepath.Abs(args["implementation-root"])
	if err != nil {
		return d.s.unavailable("ROOT", err.Error())
	}
	if filepath.Clean(repo) != repo {
		return d.s.unavailable("ROOT", "实现仓路径必须规范")
	}
	if _, err = safefs.Path(repo, ".yss-daily-observer"); err != nil {
		return d.s.unavailable("PATH", err.Error())
	}
	d.repo = repo
	d.s.externalViews[repo] = newView(repo)
	top, err := d.s.git(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(top)) != repo {
		return d.s.unavailable("ROOT", "--implementation-root 必须是真实 Git 根")
	}
	commit, err := d.s.git(repo, "rev-parse", "--verify", d.base+"^{commit}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(commit)) != d.base {
		return d.s.unavailable("BASELINE", "--base 不是当前可读的完整 commit")
	}
	if _, err = d.basisRef("CONTEXT.md", ""); err != nil {
		return err
	}
	if _, err = d.basisRef("AGENTS.md", ""); err != nil {
		return err
	}
	raw, err := d.s.bytes(d.taskRef)
	if err != nil {
		return err
	}
	d.record, d.body, err = dailyRecord(raw)
	if err != nil {
		return d.info(err.Error())
	}
	d.report.TaskID = text(d.record["task_id"])
	if d.report.TaskID == "" {
		return d.info("记录缺少 task_id")
	}
	if governed, err := d.formalBinding(); err != nil {
		return err
	} else if governed {
		return d.govern(action, "当前或历史正式任务绑定不可通过 daily 标签降级")
	}
	if text(d.record["delivery_path"]) != "daily" {
		return d.govern(action, "记录没有选择日常交付路线")
	}
	for _, key := range []string{"goal_ref", "acceptance_ref"} {
		if _, err = d.basisRef(text(d.record[key]), ""); err != nil {
			return d.info("缺少可读的目标或验收依据: " + key)
		}
	}
	repository, ok := object(d.record["repository"])
	if !ok {
		return d.info("缺少实现仓与工程基线")
	}
	if text(repository["root"]) != repo || text(repository["baseline_sha"]) != d.base {
		return d.s.unavailable("BASELINE", "记录仓库或 baseline_sha 与显式参数不一致")
	}
	for _, key := range []string{"baseline_ref", "rollback_ref"} {
		if _, err = d.basisRef(text(repository[key]), ""); err != nil {
			return d.info("缺少可读的工程基线或回滚依据: " + key)
		}
	}
	scope, ok := object(d.record["scope"])
	if !ok {
		return d.info("缺少写范围与风险依据")
	}
	paths, ok := dailyStrings(scope["paths"])
	if !ok || len(paths) == 0 {
		return d.info("scope.paths 需要非空相对路径集合")
	}
	for _, p := range paths {
		if err = safefs.ValidateRef(strings.TrimSuffix(p, "/")); err != nil {
			return d.s.unavailable("PATH", err.Error())
		}
	}
	if _, err = d.basisRef(text(scope["risk_ref"]), ""); err != nil {
		return d.info("风险依据不可读")
	}
	impacts, ok := dailyStrings(scope["impacts"])
	if !ok {
		return d.info("scope.impacts 必须明确登记，未知风险先调查")
	}
	excluded, _ := dailyStrings(d.policy["excluded_impacts"])
	for _, impact := range impacts {
		if semHas(excluded, impact) || !semHas([]string{"no-api", "ui", "backend", "frontend", "compatible-additive-api"}, impact) {
			return d.govern(action, "风险影响需要正式治理: "+impact)
		}
	}
	if d.report.Profile != "spec" {
		if err := d.specialistInput(action, impacts); err != nil {
			return err
		}
		if d.report.DeliveryPath == "governed" {
			return nil
		}
	}
	for _, key := range []string{"skills", "inputs"} {
		rows, ok := d.record[key].([]any)
		if !ok || key == "skills" && len(rows) == 0 {
			return d.info("缺少明确的 " + key)
		}
		for _, v := range rows {
			row, ok := object(v)
			if !ok || text(row["digest"]) == "" {
				return d.info(key + " 引用缺少原字节摘要")
			}
			if _, err = d.basisRef(text(row["ref"]), text(row["digest"])); err != nil {
				return err
			}
		}
	}
	commands, ok := d.record["commands"].([]any)
	if !ok || len(commands) == 0 {
		return d.info("缺少工程验证命令")
	}
	for _, v := range commands {
		m, ok := object(v)
		argv, valid := dailyStrings(m["argv"])
		if !ok || !valid || len(argv) == 0 || text(m["cwd"]) != "." {
			return d.info("验证命令需要非空 argv 与当前实现仓 cwd: .")
		}
	}
	if err = d.observeDiff(paths); err != nil {
		return err
	}
	if d.report.Profile != "spec" {
		if err = d.specialistPaths(action, d.report.ChangedFiles); err != nil {
			return err
		}
		if d.report.DeliveryPath == "governed" {
			return nil
		}
	}
	if err = d.checkAPI(action); err != nil {
		return err
	}
	if d.report.DeliveryPath == "governed" {
		return nil
	}
	d.report.CandidateDigest = d.candidateDigest()
	d.report.DeliveryPath = "daily"
	d.report.Reasons = append(d.report.Reasons, "当前目标、范围、工程基线、技能与已知风险符合日常路线；路由不授予实现、批准或发布资格")
	completion, _ := dailyStrings(d.policy["completion_requires"])
	eligibility, _ := dailyStrings(d.policy["eligible_requirements"])
	for _, key := range eligibility {
		d.report.Checks = append(d.report.Checks, SemanticCheck{ID: "eligibility." + key, SourceRef: d.taskRef, Status: "passed"})
	}
	for _, key := range completion {
		d.report.RequiredChecks = append(d.report.RequiredChecks, map[string]any{"id": key, "status": "pending-verification"})
	}
	if action == "route" {
		return nil
	}
	implementation, ok := object(d.record["implementation"])
	if !ok || text(implementation["actor_id"]) == "" {
		return d.info("缺少实现者身份")
	}
	if text(implementation["diff_digest"]) != d.report.DiffDigest {
		return d.rejected("实现 diff 摘要陈旧")
	}
	files, ok := dailyStrings(implementation["changed_files"])
	if !ok || !equalStrings(files, d.report.ChangedFiles) {
		return d.rejected("实现文件清单不等于实际 diff")
	}
	if err = d.verifyTests(d.record["tests"], true); err != nil {
		return err
	}
	if err = d.verifyReview(d.record["review"], text(implementation["actor_id"])); err != nil {
		return err
	}
	api, _ := object(d.record["api"])
	if text(api["mode"]) == "compatible-additive" {
		if err = d.verifyAPIEvidence(api, text(implementation["actor_id"])); err != nil {
			return err
		}
	}
	for _, row := range d.report.RequiredChecks {
		row["status"] = "passed"
	}
	for _, row := range d.report.RequiredChecks {
		d.report.Checks = append(d.report.Checks, SemanticCheck{ID: text(row["id"]), SourceRef: d.taskRef, Status: "passed"})
	}
	return nil
}

func (d *dailySession) identity(requested string) error {
	m, err := d.s.doc("yss-project.yaml")
	if err != nil {
		return err
	}
	v, ok := integer(m["schema_version"])
	if !ok || v != 1 || text(m["repository_mode"]) != "project-instance" {
		return d.s.unavailable("UNPORTED", "日常路线仅适用于当前 project-instance")
	}
	family := ""
	for name, p := range domain.Profiles {
		exists, err := d.s.exists(p.Metadata)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if family != "" {
			return d.s.unavailable("IDENTITY", "发现多个 Profile metadata")
		}
		meta, err := d.s.doc(p.Metadata)
		if err != nil {
			return err
		}
		version, valid := integer(meta["metadataSchemaVersion"])
		want := int64(2)
		if name == "spec" {
			want = 3
		}
		if !valid || version != want || text(meta["templateSource"]) != p.TemplateSource {
			return d.s.unavailable("UNPORTED", "旧 metadata 不支持当前路线")
		}
		family = name
	}
	exists, err := d.s.exists(domain.MetadataFile)
	if err != nil {
		return err
	}
	if exists {
		raw, err := d.s.bytes(domain.MetadataFile)
		if err != nil {
			return err
		}
		meta, err := identitymeta.ValidateNative(d.s.root, raw, family)
		if err != nil {
			return d.s.unavailable("IDENTITY", err.Error())
		}
		family = meta.Profile
	}
	if family == "" {
		return d.s.unavailable("UNPORTED", "缺少可核验的 Profile metadata")
	}
	if !semHas([]string{"spec", "backend", "frontend"}, family) {
		return d.s.unavailable("UNPORTED", "此 Profile 尚未提供日常路线")
	}
	if requested != "" && requested != family {
		return d.s.unavailable("IDENTITY", "消费 Profile 与项目身份矛盾")
	}
	d.report.Profile = family
	exists, err = d.s.exists(".template-spec/process/harness-profile.yaml")
	if err != nil {
		return err
	}
	if exists {
		p, err := d.s.doc(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return err
		}
		if text(p["profile_id"]) != domain.Profiles[family].ID {
			return d.s.unavailable("IDENTITY", "Profile 合同与 metadata 矛盾")
		}
	}
	return nil
}
func (d *dailySession) readPolicy() error {
	m, err := d.s.doc(guidanceContractRef(d.report.Profile))
	if err != nil {
		return d.s.unavailable("UNPORTED", "缺少日常路线能力策略")
	}
	triage, _ := object(m["request_triage"])
	p, ok := object(triage["delivery_path"])
	v, valid := integer(p["version"])
	if !ok || !valid || v != 1 {
		return d.s.unavailable("UNPORTED", "未知日常路线策略版本")
	}
	enabled, valid := dailyStrings(p["enabled_profiles"])
	if !valid || len(enabled) == 0 || !semHas(enabled, d.report.Profile) {
		return d.s.unavailable("UNPORTED", "此 Profile 的日常政策尚未迁移")
	}
	for _, profile := range enabled {
		if !semHas([]string{"spec", "backend", "frontend"}, profile) {
			return d.s.unavailable("UNPORTED", "未知日常 Profile 能力")
		}
	}
	required := map[string][]string{"eligible_requirements": {"goal", "acceptance", "single_repository", "current_baseline", "skills", "commands", "rollback", "known_risk"}, "excluded_impacts": {"data-migration", "cross-repository", "platform-conversion", "architecture-conversion", "permission-change", "deployment", "release", "external-approval", "breaking-api", "unknown-api"}, "read_only_capabilities": {"lifecycle.route", "lifecycle.verify-daily"}, "completion_requires": {"current-scope", "passing-actual-tests", "independent-review", "no-open-blocking-findings", "current-api-evidence-if-applicable"}}
	for key, want := range required {
		got, ok := dailyStrings(p[key])
		if !ok || !equalStringSets(got, want) {
			return d.s.unavailable("UNPORTED", "日常策略能力或条件尚未迁移: "+key)
		}
	}
	for key, want := range map[string]string{"active_task_bindings": "preserve-governed", "single_record": "markdown-with-embedded-evidence", "compatible_api": "conservative-additive"} {
		if text(p[key]) != want {
			return d.s.unavailable("UNPORTED", "日常策略不支持: "+key)
		}
	}
	d.policy = p
	raw, _ := json.Marshal(p)
	d.basis["delivery-policy"] = "sha256:" + safefs.Digest(raw)
	return nil
}
func dailyRecord(b []byte) (map[string]any, string, error) {
	s := string(b)
	if strings.Count(s, dailyMarker) != 1 {
		return nil, "", fmt.Errorf("记录缺少唯一 yss-task-evidence block，旧记录需补事实")
	}
	body, footer, _ := strings.Cut(s, dailyMarker)
	footer = strings.TrimSpace(footer)
	if !strings.HasPrefix(footer, "```json\n") {
		return nil, "", fmt.Errorf("证据必须是 fenced JSON")
	}
	payload, tail, ok := strings.Cut(strings.TrimPrefix(footer, "```json\n"), "\n```")
	if !ok || strings.TrimSpace(tail) != "" {
		return nil, "", fmt.Errorf("证据 block 未闭合或存在额外内容")
	}
	v, err := schema.Parse([]byte(payload))
	if err != nil {
		return nil, "", err
	}
	m, ok := object(v)
	if !ok {
		return nil, "", fmt.Errorf("记录证据必须为对象")
	}
	return m, body, nil
}
func dailyStrings(v any) ([]string, bool) {
	a, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := []string{}
	seen := map[string]bool{}
	for _, x := range a {
		s, ok := x.(string)
		if !ok || strings.TrimSpace(s) == "" || seen[s] {
			return nil, false
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, true
}
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return equalStrings(a, b)
}
func (d *dailySession) recordRef(ref string) ([]byte, error) {
	if strings.HasPrefix(ref, "#") {
		name := strings.TrimPrefix(ref, "#")
		lines := strings.SplitAfter(d.body, "\n")
		var out strings.Builder
		found := false
		finished := false
		level := 0
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			hashes := len(trim) - len(strings.TrimLeft(trim, "#"))
			heading := hashes > 0 && hashes <= 6 && len(trim) > hashes && trim[hashes] == ' '
			if heading {
				title := strings.TrimSpace(trim[hashes:])
				if found && hashes <= level {
					finished = true
				}
				if title == name {
					if found {
						return nil, d.info("章节引用重复: " + ref)
					}
					found = true
					level = hashes
					continue
				}
			}
			if found && !finished {
				out.WriteString(line)
			}
		}
		if !found || strings.TrimSpace(out.String()) == "" {
			return nil, d.info("章节不存在或为空: " + ref)
		}
		return []byte(out.String()), nil
	}
	if ref == "" {
		return nil, d.info("证据引用缺失")
	}
	if ref == d.taskRef {
		return nil, d.info("请引用 Ticket 具体章节以避免自摘要")
	}
	return d.s.bytes(ref)
}
func (d *dailySession) basisRef(ref, digest string) ([]byte, error) {
	b, err := d.recordRef(ref)
	if err != nil {
		return nil, err
	}
	current := "sha256:" + safefs.Digest(b)
	if digest != "" && current != digest {
		return nil, d.rejected("输入原字节摘要不匹配: " + ref)
	}
	d.basis[ref] = current
	return b, nil
}
func (d *dailySession) evidenceRef(ref, digest string) error {
	if digest == "" {
		return d.info("证据缺少原字节摘要: " + ref)
	}
	b, err := d.recordRef(ref)
	if err != nil {
		return err
	}
	if "sha256:"+safefs.Digest(b) != digest {
		return d.rejected("证据日志或审查原字节摘要不匹配: " + ref)
	}
	return nil
}

func (d *dailySession) formalBinding() (bool, error) {
	rows, ok := d.record["formal_bindings"].([]any)
	if !ok {
		return false, d.info("必须声明 formal_bindings；缺失不能证明没有正式绑定")
	}
	for _, v := range rows {
		m, ok := object(v)
		if !ok {
			return false, d.info("formal_bindings 需要可读资产引用")
		}
		ref := text(m["ref"])
		raw, err := d.s.bytes(ref)
		if err != nil {
			return false, err
		}
		if strings.HasSuffix(ref, ".md") {
			raw, _, err = frontmatter(raw)
			if err != nil {
				return false, d.info("正式绑定 Markdown 缺少可解析 metadata")
			}
		}
		parsed, err := schema.Parse(raw)
		if err != nil {
			return false, d.info("正式绑定不可解析")
		}
		asset, _ := object(parsed)
		if asset["stage"] != nil || asset["slice_id"] != nil {
			return true, nil
		}
		if d.relatedFormal(asset) {
			return true, nil
		}
	}
	layout, err := viewWorkLayout(d.s.v)
	if err != nil {
		return false, err
	}
	for _, dir := range append(layout.ScanRoots, "docs/tasks", ".template-spec/implementation") {
		files, err := d.s.scan(dir)
		if err != nil {
			return false, err
		}
		for _, ref := range files {
			if ref == d.taskRef || !(strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml") || strings.HasSuffix(ref, ".json") || strings.HasSuffix(ref, ".md")) {
				continue
			}
			raw, err := d.s.bytes(ref)
			if err != nil {
				return false, err
			}
			if !strings.Contains(string(raw), d.report.TaskID) && !strings.Contains(string(raw), d.taskRef) {
				continue
			}
			if strings.HasSuffix(ref, ".md") {
				raw, _, err = frontmatter(raw)
				if err != nil {
					return false, d.info("相关正式记录不可解析，不能证明可以降级")
				}
			}
			parsed, err := schema.Parse(raw)
			if err != nil {
				return false, d.info("相关正式记录不可解析，不能证明可以降级")
			}
			asset, _ := object(parsed)
			if d.relatedFormal(asset) {
				return true, nil
			}
		}
	}
	return d.formalHistory()
}

func (d *dailySession) formalHistory() (bool, error) {
	if _, err := os.Lstat(filepath.Join(d.s.root, ".git")); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, d.s.unavailable("INPUT", err.Error())
	}
	layout, err := viewWorkLayout(d.s.v)
	if err != nil {
		return false, err
	}
	roots := append(append([]string{}, layout.ScanRoots...), "docs/tasks", ".template-spec/implementation", d.taskRef)
	commits, err := d.s.git(d.s.root, append([]string{"log", "--all", "--format=%H", "--"}, roots...)...)
	if err != nil {
		return false, err
	}
	ids := strings.Fields(string(commits))
	if len(ids) > 1000 {
		return false, d.info("正式任务历史超出当前只读审计范围，不能证明可降级")
	}
	seen := map[string]bool{}
	for _, sha := range ids {
		tree, err := d.s.git(d.s.root, append([]string{"ls-tree", "-r", "-z", sha, "--"}, roots...)...)
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(string(tree), "\x00") {
			head, ref, ok := strings.Cut(line, "\t")
			fields := strings.Fields(head)
			if !ok || len(fields) != 3 || fields[1] != "blob" {
				continue
			}
			identity := ref + "\x00" + fields[2]
			if seen[identity] {
				continue
			}
			seen[identity] = true
			if !(dailyStructured(ref) || strings.HasSuffix(ref, ".md")) {
				continue
			}
			raw, err := d.s.git(d.s.root, "cat-file", "blob", fields[2])
			if err != nil {
				return false, err
			}
			if !strings.Contains(string(raw), d.report.TaskID) && !strings.Contains(string(raw), d.taskRef) && ref != d.taskRef {
				continue
			}
			if strings.HasSuffix(ref, ".md") && strings.Contains(string(raw), dailyMarker) {
				m, _, e := dailyRecord(raw)
				if e != nil {
					return false, d.info("相关 Ticket 历史不可解析")
				}
				bindings, _ := m["formal_bindings"].([]any)
				if ref == d.taskRef && (text(m["delivery_path"]) == "governed" || len(bindings) > 0) {
					return true, nil
				}
				continue
			}
			if strings.HasSuffix(ref, ".md") {
				raw, _, err = frontmatter(raw)
				if err != nil {
					return false, d.info("相关正式历史记录不可解析")
				}
			}
			parsed, err := schema.Parse(raw)
			if err != nil {
				return false, d.info("相关正式历史记录不可解析")
			}
			m, _ := object(parsed)
			if d.relatedFormal(m) {
				return true, nil
			}
		}
	}
	return false, nil
}
func dailySameTask(m map[string]any, id string) bool {
	if m == nil {
		return false
	}
	matched := false
	for _, key := range []string{"task_id", "ticket_id", "parent_ticket_id", "feature_id"} {
		if text(m[key]) == id {
			matched = true
		}
	}
	if !matched {
		return false
	}
	contract, _ := object(m["contract"])
	kind := text(contract["kind"])
	return kind != "" && kind != "read-only-intake" || m["checkpoint"] != nil || m["stage"] != nil || m["slice_contract_ref"] != nil || m["governed_task_binding"] != nil
}

func (d *dailySession) observeDiff(paths []string) error {
	tree, err := d.s.git(d.repo, "ls-tree", "-r", "-z", d.base)
	if err != nil {
		return err
	}
	index, err := d.s.git(d.repo, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	untracked, err := d.s.git(d.repo, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	baseline := map[string]map[string]string{}
	staged := map[string][]map[string]string{}
	names := map[string]bool{}
	for _, line := range strings.Split(string(tree), "\x00") {
		if line == "" {
			continue
		}
		head, name, ok := strings.Cut(line, "\t")
		fields := strings.Fields(head)
		if !ok || len(fields) != 3 {
			return d.s.unavailable("INPUT", "Git baseline tree 不可解析")
		}
		baseline[name] = map[string]string{"mode": fields[0], "oid": fields[2], "kind": fields[1]}
		names[name] = true
	}
	for _, line := range strings.Split(string(index), "\x00") {
		if line == "" {
			continue
		}
		head, name, ok := strings.Cut(line, "\t")
		fields := strings.Fields(head)
		if !ok || len(fields) != 3 {
			return d.s.unavailable("INPUT", "Git index 不可解析")
		}
		staged[name] = append(staged[name], map[string]string{"mode": fields[0], "oid": fields[1], "stage": fields[2]})
		names[name] = true
	}
	for _, name := range strings.Split(string(untracked), "\x00") {
		if name != "" {
			names[name] = true
		}
	}
	excluded := ""
	if local, e := filepath.Rel(d.repo, filepath.Join(d.s.root, d.taskRef)); e == nil && !strings.HasPrefix(local, "../") {
		excluded = filepath.ToSlash(local)
	}
	rows := []map[string]any{}
	for name := range names {
		if name == excluded {
			continue
		}
		if err = safefs.ValidateRef(name); err != nil {
			return d.s.unavailable("PATH", err.Error())
		}
		if baseline[name]["kind"] == "commit" {
			return d.s.unavailable("UNPORTED", "实现仓包含 gitlink，单仓日常范围无法证明")
		}
		descriptor, e := d.s.externalViews[d.repo].watch(name)
		if e != nil {
			return d.s.unavailable("INPUT", e.Error())
		}
		if descriptor.Type != "file" && descriptor.Type != "missing" {
			return d.s.unavailable("PATH", "实现输入必须是普通文件或已删除文件")
		}
		oid, mode := "", ""
		if descriptor.Type == "file" {
			raw, e := d.s.externalViews[d.repo].read(name)
			if e != nil {
				return d.s.unavailable("INPUT", e.Error())
			}
			h := sha1.New()
			fmt.Fprintf(h, "blob %d%c", len(raw), 0)
			h.Write(raw)
			oid = fmt.Sprintf("%x", h.Sum(nil))
			mode = "100644"
			if descriptor.Mode&0111 != 0 {
				mode = "100755"
			}
		}
		idx := staged[name]
		sameIndex := len(idx) == 1 && idx[0]["stage"] == "0" && idx[0]["oid"] == baseline[name]["oid"] && idx[0]["mode"] == baseline[name]["mode"]
		if baseline[name] == nil && len(idx) == 0 {
			sameIndex = true
		}
		if oid == baseline[name]["oid"] && mode == baseline[name]["mode"] && sameIndex {
			continue
		}
		allowed := false
		for _, p := range paths {
			if name == strings.TrimSuffix(p, "/") || strings.HasPrefix(name, strings.TrimSuffix(p, "/")+"/") {
				allowed = true
			}
		}
		if !allowed {
			return d.rejected("实际工作树或 index diff 超出当前写范围: " + name)
		}
		if len(idx) > 1 || len(idx) == 1 && idx[0]["stage"] != "0" {
			return d.info("实现仓存在尚未解决的 index 冲突")
		}
		if !sameIndex && (len(idx) == 0 && oid != "" || len(idx) == 1 && idx[0]["oid"] != oid) {
			return d.rejected("已暂存候选与当前验证工作树不一致")
		}
		rows = append(rows, map[string]any{"ref": name, "baseline": baseline[name], "index": idx, "current": descriptor})
		d.report.ChangedFiles = append(d.report.ChangedFiles, name)
	}
	sort.Strings(d.report.ChangedFiles)
	sort.Slice(rows, func(i, j int) bool { return text(rows[i]["ref"]) < text(rows[j]["ref"]) })
	raw, _ := json.Marshal(map[string]any{"base": d.base, "root": d.repo, "files": rows})
	d.report.DiffDigest = "sha256:" + safefs.Digest(raw)
	return nil
}
func (d *dailySession) candidateDigest() string {
	stable := map[string]any{}
	for _, key := range []string{"delivery_path", "task_id", "goal_ref", "acceptance_ref", "repository", "scope", "formal_bindings", "skills", "commands", "inputs"} {
		stable[key] = d.record[key]
	}
	api, _ := object(d.record["api"])
	a := map[string]any{}
	for _, key := range []string{"mode", "reason", "baseline", "candidate", "new_operations", "tools"} {
		if api[key] != nil {
			a[key] = api[key]
		}
	}
	stable["api"] = a
	raw, _ := json.Marshal(map[string]any{"record": stable, "inputs": d.basis, "diff": d.report.DiffDigest, "api_contract": d.report.APIContractDigest})
	return "sha256:" + safefs.Digest(raw)
}
func (d *dailySession) verifyTests(v any, coverAll bool) error {
	rows, ok := v.([]any)
	if !ok || len(rows) == 0 {
		return d.info("缺少实际测试结果")
	}
	commands, _ := d.record["commands"].([]any)
	seen := map[int64]bool{}
	for _, v := range rows {
		m, ok := object(v)
		index, valid := integer(m["command_index"])
		exit, exitValid := integer(m["exit_code"])
		if !ok || !valid || index < 0 || index >= int64(len(commands)) || !exitValid {
			return d.info("测试证据需要有效 command_index 与实际 exit_code")
		}
		if exit != 0 {
			return d.rejected("实际测试没有通过")
		}
		if text(m["candidate_digest"]) != d.report.CandidateDigest {
			return d.rejected("实际测试没有绑定当前输入与 diff")
		}
		command, _ := object(commands[index])
		if err := d.executionLog(m, command["argv"], d.repo, "candidate_digest", d.report.CandidateDigest); err != nil {
			return err
		}
		seen[index] = true
	}
	if coverAll && len(seen) != len(commands) {
		return d.info("尚有工程验证命令没有实际结果")
	}
	return nil
}
func (d *dailySession) verifyReview(v any, actor string) error {
	return d.verifyBoundReview(v, actor, "candidate_digest", d.report.CandidateDigest)
}
func (d *dailySession) verifyBoundReview(v any, actor, bindingField, currentDigest string) error {
	m, ok := object(v)
	if !ok || text(m["reviewer_id"]) == "" {
		return d.info("缺少独立审查身份与结论")
	}
	if text(m["reviewer_id"]) == actor {
		return d.rejected("实现者不能审查自己的资产")
	}
	findings, ok := m["blocking_findings"].([]any)
	if !ok {
		return d.info("独立审查必须明确登记 blocking_findings")
	}
	if len(findings) > 0 {
		return d.rejected("独立审查仍有开放阻塞 finding")
	}
	if text(m["result"]) != "passed" {
		return d.rejected("独立审查尚未通过或存在阻塞 finding")
	}
	if text(m[bindingField]) != currentDigest {
		return d.rejected("独立审查没有绑定当前输入与 diff")
	}
	if err := d.evidenceRef(text(m["ref"]), text(m["digest"])); err != nil {
		return err
	}
	actual, err := d.factRecord(text(m["ref"]))
	if err != nil {
		return err
	}
	for _, key := range []string{"reviewer_id", "result", "blocking_findings", bindingField} {
		if !apEqual(actual[key], m[key]) {
			return d.rejected("独立审查原始记录与登记身份、结论、阻塞或当前摘要不一致: " + key)
		}
	}
	return nil
}

func (d *dailySession) executionLog(row map[string]any, argv any, cwd, bindingField, currentDigest string) error {
	if err := d.evidenceRef(text(row["log_ref"]), text(row["log_digest"])); err != nil {
		return err
	}
	log, err := d.factRecord(text(row["log_ref"]))
	if err != nil {
		return err
	}
	exit, valid := integer(log["exit_code"])
	expected, expectedValid := integer(row["exit_code"])
	when, timeErr := time.Parse(time.RFC3339Nano, text(log["executed_at"]))
	if !valid || !expectedValid || timeErr != nil || when.After(time.Now().Add(time.Minute)) {
		return d.info("执行日志缺少有效实际退出码或执行时间")
	}
	if !apEqual(log["argv"], argv) || text(log["cwd"]) != cwd || exit != expected {
		return d.rejected("执行日志与登记命令、实现根或实际退出码自相矛盾")
	}
	if text(log[bindingField]) == "" {
		return d.info("执行日志原始记录缺少当次候选摘要: " + bindingField)
	}
	if text(log[bindingField]) != currentDigest || !apEqual(log[bindingField], row[bindingField]) {
		return d.rejected("执行日志原始记录未绑定当前候选，不能只重新填写登记摘要")
	}
	return nil
}

func (d *dailySession) factRecord(ref string) (map[string]any, error) {
	raw, err := d.recordRef(ref)
	if err != nil {
		return nil, err
	}
	trim := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trim, "```json\n") && strings.HasSuffix(trim, "\n```") {
		trim = strings.TrimSuffix(strings.TrimPrefix(trim, "```json\n"), "\n```")
	}
	value, err := schema.Parse([]byte(trim))
	if err != nil {
		return nil, d.info("原始测试或审查章节需要可解析的事实 JSON，不能仅重新填写登记摘要")
	}
	m, ok := object(value)
	if !ok {
		return nil, d.info("原始测试或审查事实记录必须是对象")
	}
	return m, nil
}

func (d *dailySession) relatedFormal(m map[string]any) bool {
	if dailySameTask(m, d.report.TaskID) {
		return true
	}
	scope, _ := object(m["scope"])
	for _, value := range []any{scope["ticket_ref"], m["ticket_ref"], m["parent_ticket_ref"], m["feature_ref"]} {
		if text(value) == d.taskRef || text(d.record["ticket_ref"]) != "" && text(value) == text(d.record["ticket_ref"]) {
			if m["slice_id"] != nil || m["stage"] != nil || m["contract"] != nil {
				return true
			}
		}
	}
	return false
}

// Analysis authority never expands a specialist's implementation side.
func (d *dailySession) specialistInput(action string, impacts []string) error {
	side := d.report.Profile
	opposite := "frontend"
	if side == "frontend" {
		opposite = "backend"
	}
	scope, _ := object(d.record["scope"])
	paths, _ := dailyStrings(scope["paths"])
	repository, _ := object(d.record["repository"])
	paths = append(paths, filepath.ToSlash(text(repository["root"])))
	if declared := text(repository["side"]); declared != "" && declared != side {
		return d.govern(action, "实现仓登记侧别与本端 Profile 不一致")
	}
	if err := d.specialistPaths(action, paths); err != nil || d.report.DeliveryPath == "governed" {
		return err
	}
	if semHas(impacts, opposite) {
		return d.govern(action, "本端 Profile 不允许另一端实现，需协调交付")
	}
	input, ok := object(d.record["business_input"])
	if !ok || text(input["side"]) != side || !semHas([]string{"standalone", "upstream"}, text(input["mode"])) {
		return d.info("本端需要明确 standalone/upstream 业务输入及 side")
	}
	conflicts, ok := dailyStrings(input["conflicts"])
	if !ok {
		return d.info("必须明确登记业务输入冲突")
	}
	if len(conflicts) > 0 {
		return d.govern(action, "上游/本地规则冲突需由权威方确认")
	}
	if text(input["digest"]) == "" {
		return d.info("业务输入缺少当前字节摘要")
	}
	if _, err := d.basisRef(text(input["ref"]), text(input["digest"])); err != nil {
		return err
	}
	if side == "frontend" {
		dependency, ok := object(d.record["backend_dependency"])
		if !ok || !semHas([]string{"not-applicable", "aligned"}, text(dependency["mode"])) {
			return d.info("前端需要后端依赖对齐证据或不适用依据")
		}
		api, _ := object(d.record["api"])
		if text(dependency["mode"]) == "not-applicable" && (text(api["mode"]) != "none" || semHas(impacts, "compatible-additive-api")) {
			return d.govern(action, "真实 API 依赖不能声明后端不适用")
		}
		if text(dependency["reason"]) == "" || text(dependency["digest"]) == "" {
			return d.info("后端依赖缺少原因和当前依据摘要")
		}
		if _, err := d.basisRef(text(dependency["ref"]), text(dependency["digest"])); err != nil {
			return err
		}
	}
	return nil
}

func (d *dailySession) specialistPaths(action string, paths []string) error {
	opposite := "frontend"
	if d.report.Profile == "frontend" {
		opposite = "backend"
	}
	for _, ref := range paths {
		ref = "/" + strings.Trim(filepath.ToSlash(ref), "/") + "/"
		if strings.Contains(ref, "/apps/"+opposite+"/") || strings.Contains(ref, "/app/"+opposite+"/") {
			return d.govern(action, "本端 Profile 不允许另一端工程写范围")
		}
	}
	return nil
}
