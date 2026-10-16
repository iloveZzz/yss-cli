package scaffold

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

const contractSchemaRef = ".template-spec/process/schemas/project-scaffold-contract.schema.json"

// formalState holds what the approved-contract path derives for the manifest.
type formalState struct {
	contract       *OMap
	contractText   string
	contractFile   string
	digestHex      string
	prerequisites  *OMap
	platformStatus string // "verified" or "candidate"
	catalog        *Catalog
}

// present mirrors isPresent: not undefined/null/"" and not an empty array.
func present(value any, ok bool) bool {
	if !ok || value == nil {
		return false
	}
	if text, isText := value.(string); isText && text == "" {
		return false
	}
	if list, isList := value.([]any); isList && len(list) == 0 {
		return false
	}
	return true
}

func presentField(m *OMap, key string) bool {
	value, ok := m.Get(key)
	return present(value, ok)
}

func numberEquals(value any, want int) bool {
	number, ok := value.(Num)
	if !ok {
		return false
	}
	f, err := strconv.ParseFloat(string(number), 64)
	return err == nil && f == float64(want)
}

// sameJSON is JSON.stringify(a) === JSON.stringify(b), treating two absent values as equal.
func sameJSON(a any, aok bool, b any, bok bool) bool {
	if !aok || !bok {
		return !aok && !bok
	}
	return JSON(a, "") == JSON(b, "")
}

func within(parent, target string) bool {
	rel, err := filepath.Rel(parent, target)
	return err == nil && (rel == "." || !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// findGovernanceRoot walks up from the contract to the nearest project identity.
func findGovernanceRoot(contractFile string) string {
	current := filepath.Dir(contractFile)
	fallback := current
	for {
		if pathExists(filepath.Join(current, "yss-project.yaml")) {
			return current
		}
		next := filepath.Dir(current)
		if next == current {
			return fallback
		}
		current = next
	}
}

// fromPlain converts schema.Parse output into ordered values.
func fromPlain(value any) any {
	switch v := value.(type) {
	case map[string]any:
		m := NewOMap()
		for _, key := range sortedKeys(v) {
			m.Set(key, fromPlain(v[key]))
		}
		return m
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = fromPlain(item)
		}
		return out
	case json.Number:
		return Num(v.String())
	}
	return value
}

func (o *Options) contractVersionNumber() (int, bool) {
	n, err := strconv.Atoi(o.ContractVersion)
	return n, err == nil
}

// formalFlow is the validation shared by both generators, expressed per family.
type formalFlow struct {
	src      Source
	o        *Options
	family   string // "domain-driven" or "layered-mvc"
	skill    string
	kind     string // scaffold_kind
	profile  string // architecture_profile
	name     string // project name
	pkg      string
	outputAt string
}

func (f *formalFlow) validate(ctx context.Context) (*formalState, *OMap, error) {
	o := f.o
	data, err := os.ReadFile(o.ContractFile)
	if err != nil {
		if f.family == "domain-driven" {
			return nil, nil, fail("必须提供已持久化的结构化脚手架合同 JSON 文件: --contract-file")
		}
		return nil, nil, failf("脚手架合同不可读取: %s", o.ContractFile)
	}
	text := string(data)
	parsed, err := ParseJSON(data)
	if err != nil {
		if f.family == "domain-driven" {
			return nil, nil, failf("脚手架合同文件无法读取或不是合法 JSON: %s", o.ContractFile)
		}
		return nil, nil, fail("脚手架合同必须是 JSON 对象")
	}
	contract := asMap(parsed)
	if contract == nil {
		return nil, nil, fail("脚手架合同必须是 JSON 对象")
	}
	version, _ := contract.Get("schema_version")
	if !numberEquals(version, 4) && !numberEquals(version, 5) {
		return nil, nil, failf("unsupported: 新生成只接受 Project Scaffold Contract v4，收到 v%s", fieldStringOrUndefined(contract, "schema_version"))
	}
	if f.family == "layered-mvc" && mustGet(contract, "architecture_profile") == "mvc-data-analysis-v1" {
		return nil, nil, fail("retired: mvc-data-analysis-v1 仅允许历史只读审计；使用通用 MVC 后按需单独运行 setup-yss-harness")
	}
	if mustGet(contract, "kind") != "project-scaffold-contract" || mustGet(contract, "delivery_role") != "backend" || mustGet(contract, "scaffold_kind") != f.kind {
		return nil, nil, failf("schema v4/v5 合同必须绑定 backend/%s", f.kind)
	}
	if err = f.assertContractExample(contract, numberEquals(version, 5)); err != nil {
		return nil, nil, err
	}
	if err = f.checkMetadata(contract); err != nil {
		return nil, nil, err
	}
	if err = f.checkIdentity(contract); err != nil {
		return nil, nil, err
	}
	if err = f.checkProfiles(contract); err != nil {
		return nil, nil, err
	}
	if err = f.checkCoordinates(contract); err != nil {
		return nil, nil, err
	}

	// Governance: schema, architecture decision, user decision, design prerequisites.
	root := findGovernanceRoot(o.ContractFile)
	rel, err := filepath.Rel(root, o.ContractFile)
	if err != nil {
		return nil, nil, err
	}
	if err = governance.VerifyScaffoldInputs(ctx, root, filepath.ToSlash(rel)); err != nil {
		return nil, nil, failf("脚手架合同校验未通过: %s", err.Error())
	}

	decision, err := f.loadDecision(contract)
	if err != nil {
		return nil, nil, err
	}
	state := &formalState{contract: contract, contractText: text, contractFile: o.ContractFile, digestHex: sha256Hex(data)}
	if err = f.checkArchitectureIdentity(contract, state); err != nil {
		return nil, nil, err
	}
	state.prerequisites = designPrerequisites(contract)

	catalog, err := f.catalog()
	if err != nil {
		return nil, nil, err
	}
	state.catalog = catalog
	platform, err := assertContractPlatform(f.src, catalog, contract, decision, !o.AllowCandidate)
	if err != nil {
		return nil, nil, err
	}
	state.platformStatus = "verified"
	if o.AllowCandidate {
		state.platformStatus = "candidate"
	}
	policy := asMap(mustGet(contract, "generation_policy"))
	if policy == nil || mustGet(policy, "mode") != "initialize-only" || mustGet(policy, "existing_target") != "unsupported" || mustGet(policy, "old_project_migration") != "unsupported" || mustGet(policy, "template_upgrade") != "unsupported" {
		return nil, nil, fail("脚手架合同 generation_policy 必须声明 initialize-only，且 existing_target、old_project_migration、template_upgrade 均为 unsupported")
	}
	return state, platform, nil
}

func fieldStringOrUndefined(m *OMap, key string) string {
	if !m.Has(key) {
		return "undefined"
	}
	return fieldString(m, key)
}

func (f *formalFlow) catalog() (*Catalog, error) {
	if f.o.PlatformCatalog != nil {
		return parseCatalog(f.o.PlatformCatalog)
	}
	return loadCatalog(f.src)
}

func (f *formalFlow) assertContractExample(contract *OMap, v5 bool) error {
	reference, hasReference := contract.Get("reference_example")
	if v5 {
		value := asMap(reference)
		enabled, isBool := mustGet(value, "enabled").(bool)
		if value == nil || !isBool {
			return fail("reference_example 模板、路径或摘要无效/漂移")
		}
		expected, err := referenceExample(f.src, enabled)
		if err != nil {
			return err
		}
		if JSON(stable(value), "") != JSON(stable(expected), "") {
			return fail("reference_example 模板、路径或摘要无效/漂移")
		}
		if f.o.WithExample != nil && *f.o.WithExample != enabled {
			return fail("示例参数与批准合同不一致")
		}
		return nil
	}
	if f.o.WithExample != nil && *f.o.WithExample || hasReference {
		return fail("历史 v4 合同不允许追加参考工程；请重新编译 v5 合同")
	}
	return nil
}

func (f *formalFlow) checkMetadata(contract *OMap) error {
	o := f.o
	type flag struct{ name, value string }
	var missing []string
	for _, item := range []flag{{"--contract-id", o.ContractID}, {"--contract-version", o.ContractVersion}, {"--approval-ref", o.ApprovalRef}, {"--compiler-draft-ref", o.CompilerDraftRef}, {"--persisted-ref", o.PersistedRef}} {
		if item.value == "" {
			missing = append(missing, item.name)
		}
	}
	if len(missing) > 0 {
		return failf("生成项目必须提供当前已批准脚手架合同的完整元数据: %s", strings.Join(missing, ", "))
	}
	required := []string{"schema_version", "contract_id", "contract_version", "scaffold_request_id", "status", "compiler_draft_ref", "lifecycle_approval_ref", "persisted_ref", "current_version", "implementation_repository", "backend_repository", "scaffold_status", "project_name", "target_output_dir", "base_package", "architecture_family", "generator_skill", "decision_ref", "decision_id", "decision_digest", "maven_coordinates", "profiles", "module_profile", "allowed_write_paths", "expected_evidence_files", "verification_commands", "approval", "work_unit", "generation_policy"}
	var fields []string
	for _, key := range required {
		if !presentField(contract, key) {
			fields = append(fields, key)
		}
	}
	if len(fields) > 0 {
		return failf("脚手架合同缺少结构化字段: %s", strings.Join(fields, ", "))
	}
	return nil
}

func (f *formalFlow) checkIdentity(contract *OMap) error {
	o := f.o
	if mustGet(contract, "status") != "approved" || mustGet(contract, "current_version") != true {
		return fail("脚手架合同必须已批准且为当前版本")
	}
	number, ok := o.contractVersionNumber()
	if mustGet(contract, "contract_id") != o.ContractID {
		return fail("--contract-id 与脚手架合同不一致")
	}
	if !ok || !numberEquals(mustGet(contract, "contract_version"), number) {
		return fail("--contract-version 与脚手架合同不一致")
	}
	if mustGet(contract, "compiler_draft_ref") != o.CompilerDraftRef {
		return fail("--compiler-draft-ref 与脚手架合同不一致")
	}
	if mustGet(contract, "persisted_ref") != o.PersistedRef {
		return fail("--persisted-ref 与脚手架合同不一致")
	}
	if mustGet(contract, "lifecycle_approval_ref") != o.ApprovalRef {
		return fail("--approval-ref 与脚手架合同不一致")
	}
	if mustGet(contract, "scaffold_status") != "required" {
		return fail("脚手架生成器只接受 scaffold_status=required")
	}
	if mustGet(contract, "architecture_family") != f.family || mustGet(contract, "generator_skill") != f.skill {
		return fail("脚手架合同必须绑定 " + f.family + " 与本生成器")
	}
	if mustGet(contract, "project_name") != o.ProjectName {
		return fail("--project-name 与脚手架合同不一致")
	}
	if mustGet(contract, "base_package") != o.BasePackage {
		return fail("--base-package 与脚手架合同不一致")
	}
	for _, key := range []string{"allowed_write_paths", "expected_evidence_files", "verification_commands"} {
		if list := asList(mustGet(contract, key)); len(list) == 0 {
			return fail("脚手架合同的 allowed_write_paths、expected_evidence_files、verification_commands 必须非空")
		}
	}
	evidence := stringList(mustGet(contract, "expected_evidence_files"))
	if f.family == "domain-driven" && !strings.Contains(strings.Join(evidence, " "), manifestRef) || f.family == "layered-mvc" && !containsString(evidence, manifestRef) {
		return fail("脚手架合同 expected_evidence_files 必须包含 " + manifestRef)
	}
	target, err := filepath.Abs(fieldString(contract, "target_output_dir"))
	if err != nil || target != o.OutputDir {
		return fail("脚手架合同 target_output_dir 与 --output-dir 不一致")
	}
	final := filepath.Join(o.OutputDir, o.ProjectName)
	allowed := false
	for _, item := range stringList(mustGet(contract, "allowed_write_paths")) {
		root, _ := filepath.Abs(filepath.Join(o.OutputDir, item))
		allowed = allowed || within(root, final)
	}
	if !allowed {
		return fail("实际项目根不在脚手架合同 allowed_write_paths 内")
	}
	if JSON(mustGet(contract, "verification_commands"), "") != JSON(commandList(), "") {
		return fail("脚手架合同验证命令必须固定为三条项目根目录 ./mvnw 命令")
	}
	approval := asMap(mustGet(contract, "approval"))
	for _, key := range []string{"approval_ref", "approver", "persisted_ref", "current_version"} {
		if approval == nil || !presentField(approval, key) {
			return fail("脚手架合同 approval 记录不完整")
		}
	}
	if mustGet(approval, "approval_ref") != o.ApprovalRef || mustGet(approval, "persisted_ref") != o.PersistedRef {
		return fail("脚手架合同 approval 引用与命令参数不一致")
	}
	if number, ok := o.contractVersionNumber(); !ok || !numberEquals(mustGet(approval, "current_version"), number) {
		return fail("脚手架合同 approval 不是当前版本")
	}
	work := asMap(mustGet(contract, "work_unit"))
	for _, key := range []string{"id", "behavior", "primary_skill", "supporting_skills", "tdd_mode", "allowed_write_paths", "expected_evidence", "verification_commands", "controlled_generation"} {
		if work == nil || !presentField(work, key) {
			return fail("脚手架合同 work_unit 结构不完整")
		}
	}
	if mustGet(work, "primary_skill") != f.skill || mustGet(work, "tdd_mode") != "controlled-generation" || mustGet(work, "controlled_generation") != true {
		return fail("脚手架合同 work_unit 必须绑定本 skill 和 controlled-generation")
	}
	if JSON(mustGet(work, "verification_commands"), "") != JSON(mustGet(contract, "verification_commands"), "") || JSON(mustGet(work, "allowed_write_paths"), "") != JSON(mustGet(contract, "allowed_write_paths"), "") {
		return fail("脚手架合同 work_unit 与根级验证/写路径约束不一致")
	}
	return nil
}

var supportedProfiles = [][2]string{
	{"architecture", "target-domain-model"}, {"persistence", "mybatis-plus"}, {"verification_database", "h2"},
	{"production_database", "not-bound"}, {"dto_placement", "web"}, {"repository", "yss-internal"},
}

func (f *formalFlow) checkProfiles(contract *OMap) error {
	profiles := asMap(mustGet(contract, "profiles"))
	if f.family == "domain-driven" {
		if profiles == nil {
			return fail("脚手架合同 profiles 结构不完整")
		}
		for _, item := range supportedProfiles {
			if !presentField(profiles, item[0]) {
				return failf("脚手架合同 profiles 缺少 %s", item[0])
			}
			if mustGet(profiles, item[0]) != item[1] {
				return failf("unsupported %s profile: %s；只支持 %s", item[0], fieldString(profiles, item[0]), item[1])
			}
		}
	} else {
		for _, item := range [][2]string{{"architecture", "layered-mvc"}, {"persistence", "mybatis-plus"}, {"repository", "yss-internal"}} {
			if mustGet(profiles, item[0]) != item[1] {
				return failf("unsupported profile %s: %s", item[0], fieldStringOrUndefined(profiles, item[0]))
			}
		}
	}
	if mustGet(profiles, "verification_database") != "h2" || mustGet(profiles, "production_database") != "not-bound" || profiles.Has("database") {
		return fail("新脚手架必须声明 verification_database=h2、production_database=not-bound；旧 database Profile 必须重新批准，不作静默迁移")
	}
	if mustGet(contract, "architecture_profile") != f.profile {
		return failf("合同必须显式绑定 %s Profile", f.profile)
	}
	module := asMap(mustGet(contract, "module_profile"))
	if f.family == "domain-driven" {
		if !numberEquals(mustGet(module, "resolution_version"), 1) || JSON(mustGet(module, "requested_capabilities"), "") != "[]" || JSON(mustGet(module, "resolved_modules"), "") != JSON(stringsToAny(standaloneModules("domain-driven")), "") {
			return fail("DDD module_profile 必须冻结固定模块闭包与 resolution_version=1")
		}
		return nil
	}
	if mustGet(contract, "init_git") != false {
		return fail("通用 layered-mvc-service 脚手架不得初始化 Git")
	}
	requested, ok := mustGet(module, "requested_capabilities").([]any)
	if !ok {
		return fail("module_profile.requested_capabilities 必须是数组")
	}
	resolved, err := orderedModules(stringList(requested))
	if err != nil {
		return err
	}
	if !numberEquals(mustGet(module, "resolution_version"), 1) || JSON(mustGet(module, "resolved_modules"), "") != JSON(stringsToAny(resolved), "") {
		return fail("MVC 能力闭包与 resolution_version=1 不一致")
	}
	return nil
}

func (f *formalFlow) checkCoordinates(contract *OMap) error {
	o := f.o
	options := []string{o.GroupID, o.ProjectVersion, o.ParentGroupID, o.ParentArtifactID, o.ParentVersion, o.YssComponentsVersion}
	provided := 0
	for _, value := range options {
		if value != "" {
			provided++
		}
	}
	if f.family == "domain-driven" && provided != 0 && provided != len(options) {
		return fail("显式 Maven 坐标必须完整提供 groupId、项目版本、父 POM 坐标和 YSS Components 版本")
	}
	coordinates := asMap(mustGet(contract, "maven_coordinates"))
	parent := asMap(mustGet(coordinates, "parent"))
	values := []any{mustGet(coordinates, "group_id"), mustGet(coordinates, "project_version"), mustGet(parent, "group_id"), mustGet(parent, "artifact_id"), mustGet(parent, "version"), mustGet(coordinates, "yss_components_version")}
	for _, value := range values {
		if !present(value, value != nil) {
			if f.family == "domain-driven" {
				return fail("脚手架合同 maven_coordinates 必须包含项目 groupId/version、父 POM GAV 和 YSS Components 版本")
			}
			return fail("Maven 坐标与合同不一致")
		}
	}
	if f.family == "domain-driven" && provided != len(options) {
		return fail("合同包含 maven_coordinates 时必须传入完整 Maven 坐标参数")
	}
	for i, value := range values {
		if jsString(value) != options[i] {
			if f.family == "domain-driven" {
				return fail("命令行 Maven 坐标与已批准脚手架合同不一致")
			}
			return fail("Maven 坐标与合同不一致")
		}
	}
	for _, value := range values {
		if !mavenCoordinate.MatchString(jsString(value)) {
			return fail("Maven 坐标只能包含字母、数字、点、下划线和连字符")
		}
	}
	return nil
}

// loadDecision reads the architecture decision record the contract points at.
func (f *formalFlow) loadDecision(contract *OMap) (*OMap, error) {
	ref := fieldString(contract, "decision_ref")
	file := ref
	if !filepath.IsAbs(ref) {
		file = filepath.Join(filepath.Dir(f.o.ContractFile), ref)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, failf("架构决策文件不可读取: %s", file)
	}
	if sha256Ref(data) != mustGet(contract, "decision_digest") {
		return nil, fail("架构决策文件 digest 与合同不一致")
	}
	parsed, err := schema.Parse(data)
	if err != nil {
		return nil, failf("架构决策 YAML 无效: %s", err.Error())
	}
	set := asMap(fromPlain(parsed))
	if set == nil || mustGet(set, "kind") != "scaffold-architecture-decisions" || mustGet(set, "template") != false || mustGet(set, "status") != "current" {
		return nil, fail("架构决策文件必须是 current 的正式 scaffold-architecture-decisions 记录")
	}
	for _, item := range asList(mustGet(set, "decisions")) {
		if decision := asMap(item); decision != nil && mustGet(decision, "decision_id") == mustGet(contract, "decision_id") {
			return decision, nil
		}
	}
	return nil, failf("架构决策未批准、项目不匹配或不是 %s", f.family)
}

// checkArchitectureIdentity validates the scaffold architecture identity the
// manifest records (the part of validateArchitectureIdentity the generators use).
func (f *formalFlow) checkArchitectureIdentity(contract *OMap, state *formalState) error {
	identity := scaffoldArchitectureIdentity(contract, f.digestFor(state))
	if mustGet(identity, "architecture_family") != f.family || mustGet(identity, "generator_skill") != f.skill || mustGet(identity, "architecture_profile") != f.profile {
		return fail("architecture_identity 的架构族、Profile 或生成器不匹配")
	}
	return nil
}

func (f *formalFlow) digestFor(state *formalState) string {
	if f.family == "domain-driven" {
		return state.digestHex
	}
	return "sha256:" + state.digestHex
}

func scaffoldArchitectureIdentity(contract *OMap, digest string) *OMap {
	identity := NewOMap()
	if binding, ok := contract.Get("platform_configuration"); ok && binding != nil {
		identity.Set("platform_configuration", binding)
	}
	module := asMap(mustGet(contract, "module_profile"))
	identity.Set("architecture_family", mustGet(contract, "architecture_family"))
	identity.Set("architecture_profile", mustGet(contract, "architecture_profile"))
	identity.Set("generator_skill", mustGet(contract, "generator_skill"))
	identity.Set("requested_capabilities", mustGet(module, "requested_capabilities"))
	identity.Set("resolved_modules", mustGet(module, "resolved_modules"))
	identity.Set("contract_digest", digest)
	identity.Set("verification_database", "h2")
	identity.Set("production_database", "not-bound")
	return identity
}

// designPrerequisites is the manifest's design_prerequisites record. The
// governance check has already established that the bound versions and impacts
// are the ones in the approved documents.
func designPrerequisites(contract *OMap) *OMap {
	p := asMap(mustGet(contract, "design_prerequisites"))
	technical := asMap(mustGet(p, "technical_design"))
	data := asMap(mustGet(p, "data_architecture_decision"))
	api := asMap(mustGet(p, "api_contract_decision"))
	out := newMap("technical_design", newMap("ref", mustGet(technical, "ref"), "version", mustGet(technical, "version"), "digest", mustGet(technical, "digest")))
	if context, ok := technical.Get("approval_context"); ok && truthyValue(context) {
		asMap(mustGet(out, "technical_design")).Set("approval_context", cloneValue(context))
	}
	out.Set("data_architecture_decision", newMap("ref", mustGet(data, "ref"), "version", mustGet(data, "version"), "digest", mustGet(data, "digest"), "impact", mustGet(data, "impact")))
	out.Set("api_contract_decision", newMap("ref", mustGet(api, "ref"), "version", mustGet(api, "version"), "digest", mustGet(api, "digest"), "impact", mustGet(api, "impact")))
	out.Set("engineering_contract_approval_ref", mustGet(p, "engineering_contract_approval_ref"))
	return out
}
