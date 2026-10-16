package contracts

import approvaldomain "github.com/iloveZzz/yss-cli/internal/governance/approval"

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	handoffdomain "github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/governance/reading"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	gmtext "github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"math"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func contractPlatformRecipe(profile, entry map[string]any) string {
	recipe := ContractWithout(profile, "candidate_blockers", "recommended")
	component := entry["components"]
	if component == nil {
		component = map[string]any{}
	}
	artifacts := entry["platform_artifact_bindings"]
	if artifacts == nil {
		artifacts = map[string]any{}
	}
	snapshots, external := entry["external_snapshot_bindings"], entry["external_artifact_bindings"]
	if snapshots == nil {
		snapshots = []any{}
	}
	if external == nil {
		external = []any{}
	}
	return ContractDigest(map[string]any{"profile": recipe, "entry": map[string]any{"id": entry["id"], "profile_id": entry["profile_id"], "spring_boot_version": entry["spring_boot_version"], "parent": entry["parent"], "bom": entry["bom"], "components": component, "platform_artifact_bindings": artifacts, "external_snapshot_bindings": snapshots, "external_artifact_bindings": external}})
}

func contractPlatformFingerprint(s *kernel.SemanticSession, family string) (map[string]any, error) {
	tool, e := s.ToolSourceSession()
	if e != nil {
		return nil, e
	}
	s = tool
	skill := ""
	switch family {
	case "domain-driven":
		skill = "yss-ddd-scaffold-generator"
	case "layered-mvc":
		skill = "yss-layered-mvc-scaffold-generator"
	default:
		return nil, s.Reject("PLATFORM_SOURCE", "未知脚手架架构")
	}
	base := ".agents/skills/" + skill
	generator := []string{base + "/scripts/generate_scaffold.mjs", base + "/assets", "scripts/lib/backend-platform.mjs", "scripts/lib/scaffold-local-database.mjs"}
	if present, err := s.Exists("scripts/lib/standalone-backend-scaffold.mjs"); err != nil {
		return nil, err
	} else if present {
		generator = append(generator, "scripts/lib/standalone-backend-scaffold.mjs")
	}
	generator = append(generator, "scripts/lib/scaffold-reference-example.mjs", ".agents/skills/yss-dto/references/openapi-wire-profile.yaml")
	if family == "layered-mvc" {
		generator = append(generator, ".agents/skills/yss-ddd-scaffold-generator/assets/wrapper", ".agents/skills/yss-ddd-scaffold-generator/assets/reference")
	}
	verifier := []string{"scripts/lib/backend-platform-provenance.mjs", "scripts/lib/backend-platform-verification.mjs", "scripts/lib/command-runner.mjs", "scripts/vendor/xml.mjs", ".agents/skills/yss-ddd-scaffold-generator/scripts/run_scaffold_verification.mjs"}
	fileMap := func(refs []string) (string, error) {
		files := map[string]map[string]any{}
		for _, ref := range refs {
			exists, e := s.Exists(ref)
			if e != nil {
				return "", e
			}
			if !exists {
				return "", s.Unavailable("CAPABILITY", "固定工具provenance字节缺失，需显式--tool-root: "+ref)
			}
			inputs := []string{ref}
			if _, dir := s.Scans[s.LocalRef(ref)]; dir {
				inputs, e = s.Scan(ref)
				if e != nil {
					return "", e
				}
			}
			for _, ref := range inputs {
				b, e := s.Bytes(ref)
				if e != nil {
					return "", e
				}
				d, e := s.V.Watch(s.LocalRef(ref))
				if e != nil {
					return "", e
				}
				files[ref] = map[string]any{"digest": "sha256:" + safefs.Digest(b), "executable": d.Mode&0111 != 0}
			}
		}
		keys := []string{}
		for k := range files {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		collator := collate.New(language.English)
		sort.SliceStable(keys, func(i, j int) bool { return collator.CompareString(keys[i], keys[j]) < 0 })
		var b strings.Builder
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			key, _ := ContractJSON(k)
			value, e := ContractJSON(files[k])
			if e != nil {
				return "", e
			}
			b.WriteString(key)
			b.WriteByte(':')
			b.WriteString(value)
		}
		b.WriteByte('}')
		return "sha256:" + safefs.Digest([]byte(b.String())), nil
	}
	g, e := fileMap(generator)
	if e != nil {
		return nil, e
	}
	v, e := fileMap(verifier)
	if e != nil {
		return nil, e
	}
	return map[string]any{"schema_version": 1, "generator_digest": g, "verifier_digest": v}, nil
}

func contractPlatform(s *kernel.SemanticSession, binding map[string]any, verified bool) (map[string]any, map[string]any, []map[string]any, error) {
	c, e := contractPlatformCatalog(s)
	if e != nil {
		return nil, nil, nil, e
	}
	if kernel.ContractN(binding["schema_version"]) != 2 {
		return nil, nil, nil, s.Reject("PLATFORM_BINDING", "当前运行需要平台配置v2")
	}
	matches := []map[string]any{}
	for _, v := range kernel.SemList(c["profiles"]) {
		p := kernel.SemMap(v)
		if p["id"] == binding["profile_id"] && p["spring_boot_version"] == binding["spring_boot_version"] {
			matches = append(matches, p)
		}
	}
	if len(matches) != 1 {
		return nil, nil, nil, s.Reject("PLATFORM_BINDING", "平台配置必须选择唯一精确Boot patch")
	}
	profile := matches[0]
	entry := kernel.ApFind(c["compatibility"], "id", kernel.Text(binding["compatibility_id"]))
	if entry == nil || entry["profile_id"] != profile["id"] || entry["spring_boot_version"] != profile["spring_boot_version"] {
		return nil, nil, nil, s.Reject("PLATFORM_BINDING", "缺少精确YSS兼容条目")
	}
	expected := map[string]any{"schema_version": 2, "profile_id": profile["id"], "spring_boot_version": profile["spring_boot_version"], "java_version": profile["java_version"], "parent": entry["parent"], "bom": entry["bom"], "compatibility_id": entry["id"], "compatibility_digest": contractPlatformRecipe(profile, entry)}
	if line := kernel.Text(profile["component_platform_line"]); line == "boot2-java8" || line == "boot3-java17" {
		expected["component_platform_line"] = line
	}
	if !ContractSame(binding, expected) {
		return nil, nil, nil, s.Reject("PLATFORM_BINDING", "平台坐标、Java版本或兼容摘要漂移")
	}
	reports := []map[string]any{}
	if !verified {
		return profile, entry, reports, nil
	}
	if kernel.Text(entry["status"]) != "verified" || len(kernel.SemList(entry["evidence"])) == 0 {
		return nil, nil, nil, s.Reject("PLATFORM_UNVERIFIED", "YSS组合尚未验证")
	}
	for _, v := range kernel.SemList(entry["evidence"]) {
		ev := kernel.SemMap(v)
		report, e := ContractBoundDoc(s, ev)
		if e != nil {
			return nil, nil, nil, e
		}
		family := kernel.Text(report["architecture_family"])
		if kernel.Text(report["verification_scope"]) != "empty-scaffold" || kernel.Text(report["recipe_digest"]) != contractPlatformRecipe(profile, entry) || !ContractSHA(report["generated_tree_digest"]) || kernel.Text(report["status"]) != "passed" || report["spring_boot_version"] != profile["spring_boot_version"] || report["java_version"] != profile["java_version"] || report["architecture_family"] != ev["architecture_family"] || !ContractSame(report["parent"], entry["parent"]) || !ContractSame(report["bom"], entry["bom"]) || kernel.Text(report["dependency_check"]) != "passed" || kernel.Text(report["startup_check"]) != "passed" || kernel.Text(kernel.SemMap(report["integration_tests"])["status"]) != "passed" {
			return nil, nil, nil, s.Reject("PLATFORM_EVIDENCE", "平台qualification身份或集成检查不完整")
		}
		fingerprint, e := contractPlatformFingerprint(s, family)
		if e != nil {
			return nil, nil, nil, e
		}
		if !ContractSame(report["source_fingerprint"], fingerprint) {
			return nil, nil, nil, s.Reject("PLATFORM_SOURCE", "固定generator/verifier源码变化")
		}
		for _, phase := range []string{"validate", "test", "package"} {
			found := false
			for _, v := range kernel.SemList(report["commands"]) {
				row := kernel.SemMap(v)
				if kernel.Text(row["command"]) != "./mvnw "+phase && !(kernel.Text(row["phase"]) == phase && strings.HasPrefix(kernel.Text(row["command"]), "./mvnw "+phase+" ") && !strings.Contains(kernel.Text(row["command"]), "skipTests") && !strings.Contains(kernel.Text(row["command"]), "maven.test.skip")) {
					continue
				}
				exit, ok := kernel.Integer(row["exit_code"])
				// The published qualification protocol records a nonempty timestamp;
				// historical reports do not define an RFC3339 restriction.
				if !ok || exit != 0 || kernel.Text(row["executed_at"]) == "" || kernel.Text(row["stdout_ref"]) == "" || kernel.Text(row["stderr_ref"]) == "" {
					return nil, nil, nil, s.Reject("PLATFORM_EVIDENCE", "Maven真实执行证据不完整")
				}
				found = true
			}
			if !found {
				return nil, nil, nil, s.Reject("PLATFORM_EVIDENCE", "平台缺少实际Maven阶段")
			}
		}
		required := []string{"effective-pom.xml", "dependency-trees.json", "platform-tests.xml", "boot-bom-effective.xml", "runtime-jar-entries.log", "startup.stdout.log", "startup.stderr.log"}
		for _, phase := range []string{"validate", "test", "package"} {
			required = append(required, "mvnw-"+phase+".stdout.log", "mvnw-"+phase+".stderr.log")
		}
		artifacts := kernel.SemList(report["evidence_artifacts"])
		if len(artifacts) == 0 {
			return nil, nil, nil, s.Reject("PLATFORM_EVIDENCE", "平台可移植实际证据缺失")
		}
		for _, name := range required {
			if kernel.ApFind(artifacts, "ref", name) == nil {
				return nil, nil, nil, s.Reject("PLATFORM_EVIDENCE", "平台缺少可移植证据: "+name)
			}
		}
		for _, v := range artifacts {
			a := kernel.SemMap(v)
			if !kernel.ContractPath(kernel.Text(a["ref"])) {
				return nil, nil, nil, s.Reject("PATH", "平台证据路径非法")
			}
			ref := path.Join(path.Dir(kernel.Text(ev["ref"])), kernel.Text(a["ref"]))
			if _, e = contractBinding(s, map[string]any{"ref": ref, "digest": a["digest"]}); e != nil {
				return nil, nil, nil, e
			}
		}
		reports = append(reports, report)
	}
	return profile, entry, reports, nil
}

func ContractScaffoldUserDecision(s *kernel.SemanticSession, decision map[string]any) error {
	c, e := contractPlatformCatalog(s)
	if e != nil {
		return e
	}
	var profile map[string]any
	for _, v := range kernel.SemList(c["profiles"]) {
		p := kernel.SemMap(v)
		if p["id"] == decision["platform_profile"] && (kernel.SemMap(decision["platform_configuration"])["spring_boot_version"] == nil || p["spring_boot_version"] == kernel.SemMap(decision["platform_configuration"])["spring_boot_version"]) {
			if profile != nil {
				return s.Reject("SCAFFOLD_DECISION", "架构决定平台patch不唯一")
			}
			profile = p
		}
	}
	if profile == nil {
		return s.Reject("SCAFFOLD_DECISION", "架构决定平台未知")
	}
	confirmation := kernel.SemMap(decision["user_confirmation"])
	requirement := map[string]any{"boundary": "gate.backend-architecture-platform-approved", "subject_ref": confirmation["decision_subject_ref"], "scope": []any{decision["project_id"], decision["confirmed_architecture"], fmt.Sprintf("%s@%s/java%d", kernel.Text(profile["id"]), kernel.Text(profile["spring_boot_version"]), kernel.ContractN(profile["java_version"])), decision["decision_inputs_digest"]}, "user_decision_ref": confirmation["user_decision_ref"]}
	if _, e = approvaldomain.AssertUserDecisionRequirementSemantic(s, requirement); e != nil {
		return e
	}
	subject, e := s.Doc(kernel.Text(requirement["subject_ref"]))
	if e != nil {
		return e
	}
	snapshot := ContractWithout(decision, "user_confirmation", "status", "decision_inputs_digest")
	snapshot["platform_selection"] = map[string]any{"profile_id": profile["id"], "spring_boot_version": profile["spring_boot_version"], "java_version": profile["java_version"]}
	if !ContractSame(subject, snapshot) {
		return s.Reject("user-decision-stale", "脚手架输入与用户所见快照不一致")
	}
	return nil
}

func contractExactVersion(v any) bool {
	return kernel.Text(v) != "" && !regexp.MustCompile(`(?i)[\[\](),*+]|\bx\b|latest`).MatchString(kernel.Text(v))
}

func ContractSHA(v any) bool {
	return regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(kernel.Text(v))
}

func contractGitTree(v any) bool {
	return regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(kernel.Text(v))
}

func contractPlatformGeneration(profile map[string]any) (string, int) {
	v := strings.Split(kernel.Text(profile["spring_boot_version"]), ".")[0]
	switch v {
	case "2":
		return "boot2-java8", 2
	case "3":
		return "boot3-java17", 3
	default:
		return "boot4", 0
	}
}

func contractRetirement(v any) bool {
	required := map[string]bool{"organization-code-search": true, "maven-download-metrics": true, "runtime-bean-and-configuration-scan": true, "30-90-day-zero-usage-window": true}
	for _, v := range kernel.SemList(v) {
		m := kernel.SemMap(v)
		if kernel.Text(m["status"]) != "zero" || kernel.Text(m["ref"]) == "" {
			return false
		}
		delete(required, kernel.Text(m["kind"]))
	}
	return len(required) == 0
}
func contractPlatformArtifact(s *kernel.SemanticSession, b map[string]any, role string, expected map[string]any, snapshot, resolved bool) error {
	status := kernel.Text(b["status"])
	if role == "" || kernel.Text(b["role"]) != role || status != "candidate" && !(resolved && status == "resolved") && !(expected != nil && status == "resolved-pom") || kernel.Text(b["group_id"]) == "" || kernel.Text(b["artifact_id"]) == "" || !contractExactVersion(b["declared_version"]) || snapshot != strings.HasSuffix(kernel.Text(b["declared_version"]), "-SNAPSHOT") {
		return s.Reject("PLATFORM_ARTIFACT", "平台候选产物坐标或角色冲突")
	}
	if expected != nil && (b["group_id"] != expected["group_id"] || b["artifact_id"] != expected["artifact_id"] || b["declared_version"] != expected["version"]) {
		return s.Reject("PLATFORM_ARTIFACT", "候选产物与兼容坐标冲突")
	}
	if status == "candidate" {
		for _, field := range []string{"resolved_version", "published_at", "pom_sha256", "jar_sha256", "sources_jar_sha256", "source_tree"} {
			value, exists := b[field]
			if !exists || value != nil {
				return s.Reject("PLATFORM_ARTIFACT", "候选产物不得伪造已解析证据")
			}
		}
		if !kernel.ApIsArray(b["evidence"]) || len(kernel.SemList(b["evidence"])) != 0 || len(kernel.SemList(b["blockers"])) == 0 {
			return s.Reject("PLATFORM_ARTIFACT", "候选产物必须保持有阻断且未解析")
		}
	} else if status == "resolved-pom" {
		if (role != "parent" && role != "bom") || !snapshot || !regexp.MustCompile(`^\d+\.\d+\.\d+-\d{8}\.\d{6}-\d+$`).MatchString(kernel.Text(b["resolved_version"])) || !ContractSHA(b["pom_sha256"]) || !contractGitTree(b["source_tree"]) || b["jar_sha256"] != nil || b["sources_jar_sha256"] != nil || len(kernel.SemList(b["evidence"])) == 0 || !kernel.ApIsArray(b["blockers"]) || len(kernel.SemList(b["blockers"])) != 0 {
			return s.Reject("PLATFORM_ARTIFACT", "平台 POM 缺少精确 SNAPSHOT 及源码证据")
		}
	} else {
		if snapshot || b["resolved_version"] != b["declared_version"] || !ContractSHA(b["pom_sha256"]) || !ContractSHA(b["jar_sha256"]) || !ContractSHA(b["sources_jar_sha256"]) || b["published_at"] != nil && kernel.Text(b["published_at"]) == "" || b["source_tree"] != nil && !contractGitTree(b["source_tree"]) || !kernel.ApIsArray(b["evidence"]) || !kernel.ApIsArray(b["blockers"]) {
			return s.Reject("PLATFORM_ARTIFACT", "外部发布产物缺少不可变证据")
		}
	}
	return nil
}

func contractPlatformCatalog(s *kernel.SemanticSession) (map[string]any, error) {
	c, e := s.Doc(".template-spec/engineering/backend-platforms.json")
	if e != nil {
		return nil, e
	}
	if kernel.ContractN(c["schema_version"]) != 2 || kernel.Text(c["kind"]) != "backend-platform-catalog" || !kernel.ApIsArray(c["profiles"]) || !kernel.ApIsArray(c["compatibility"]) || c["component_capabilities"] != nil || c["component_capability_ids"] != nil {
		return nil, s.Reject("PLATFORM_CATALOG", "平台目录协议无效")
	}
	profiles, entries := map[string]bool{}, map[string]bool{}
	for _, v := range kernel.SemList(c["profiles"]) {
		p := kernel.SemMap(v)
		key := kernel.Text(p["id"]) + ":" + kernel.Text(p["spring_boot_version"]) + ":" + fmt.Sprint(p["java_version"])
		line, _ := contractPlatformGeneration(p)
		if profiles[key] || !regexp.MustCompile(`^\d+[.]\d+[.]\d+$`).MatchString(kernel.Text(p["spring_boot_version"])) || line != "boot4" && kernel.Text(p["component_platform_line"]) != line || line == "boot4" && p["component_platform_line"] != nil {
			return nil, s.Reject("PLATFORM_CATALOG", "重复平台或代际冲突")
		}
		profiles[key] = true
	}
	for _, v := range kernel.SemList(c["compatibility"]) {
		entry := kernel.SemMap(v)
		id := kernel.Text(entry["id"])
		if entries[id] || id == "" {
			return nil, s.Reject("PLATFORM_CATALOG", "兼容条目ID重复或为空")
		}
		entries[id] = true
		var profile map[string]any
		for _, v := range kernel.SemList(c["profiles"]) {
			p := kernel.SemMap(v)
			if p["id"] == entry["profile_id"] && p["spring_boot_version"] == entry["spring_boot_version"] {
				profile = p
			}
		}
		line, major := contractPlatformGeneration(profile)
		if profile == nil || line == "boot4" || kernel.Text(profile["component_platform_line"]) != line {
			return nil, s.Reject("PLATFORM_CATALOG", "兼容条目平台代际缺失")
		}
		if raw := entry["platform_artifact_bindings"]; raw != nil {
			artifacts := kernel.SemMap(raw)
			if len(artifacts) != 2 {
				return nil, s.Reject("PLATFORM_ARTIFACT", "平台候选必须完整包含parent和bom")
			}
			for _, role := range []string{"parent", "bom"} {
				if e = contractPlatformArtifact(s, kernel.SemMap(artifacts[role]), role, kernel.SemMap(entry[role]), true, false); e != nil {
					return nil, e
				}
			}
		}
		for _, field := range []string{"external_snapshot_bindings", "external_artifact_bindings"} {
			if entry[field] == nil {
				continue
			}
			if !kernel.ApIsArray(entry[field]) {
				return nil, s.Reject("PLATFORM_ARTIFACT", "外部绑定不是数组")
			}
			coordinates := map[string]bool{}
			for _, v := range kernel.SemList(entry[field]) {
				b := kernel.SemMap(v)
				key := kernel.Text(b["group_id"]) + ":" + kernel.Text(b["artifact_id"])
				if coordinates[key] {
					return nil, s.Reject("PLATFORM_ARTIFACT", "外部产物坐标重复")
				}
				coordinates[key] = true
				if e = contractPlatformArtifact(s, b, kernel.Text(b["role"]), nil, field == "external_snapshot_bindings", field == "external_artifact_bindings"); e != nil {
					return nil, e
				}
			}
		}
		resolutions := map[string]map[string]any{}
		if raw := entry["artifact_resolution_evidence"]; raw != nil {
			b := kernel.SemMap(raw)
			report, e := ContractBoundDoc(s, b)
			if e != nil {
				return nil, e
			}
			if kernel.ContractN(report["schema_version"]) != 1 || kernel.Text(report["kind"]) != "backend-component-artifact-resolution" || report["report_id"] != b["report_id"] || kernel.SemMap(report["repository"])["id"] != b["repository_id"] || !kernel.ApIsArray(report["artifacts"]) {
				return nil, s.Reject("PLATFORM_ARTIFACT", "产物解析证据身份冲突")
			}
			for _, v := range kernel.SemList(report["artifacts"]) {
				a := kernel.SemMap(v)
				resolutions[kernel.Text(a["group_id"])+":"+kernel.Text(a["artifact_id"])+":"+kernel.Text(a["declared_version"])] = a
			}
		}
		for _, value := range kernel.SemMap(entry["platform_artifact_bindings"]) {
			b := kernel.SemMap(value)
			if kernel.Text(b["status"]) != "resolved-pom" {
				continue
			}
			a := resolutions[kernel.Text(b["group_id"])+":"+kernel.Text(b["artifact_id"])+":"+kernel.Text(b["declared_version"])]
			if a == nil || b["resolved_version"] != a["resolved_version"] || kernel.Text(b["pom_sha256"]) != "sha256:"+kernel.Text(a["pom_sha256"]) || b["source_tree"] != a["source_tree"] || a["pom_matches_source_tree"] != true {
				return nil, s.Reject("PLATFORM_ARTIFACT", "平台 POM 与解析证据冲突")
			}
			for _, evidence := range kernel.SemList(b["evidence"]) {
				if _, err := ContractBoundDoc(s, kernel.SemMap(evidence)); err != nil {
					return nil, err
				}
			}
		}
		if entry["component_capabilities"] == nil {
			continue
		}
		components := kernel.SemMap(entry["component_capabilities"])
		if components == nil {
			return nil, s.Reject("PLATFORM_COMPONENT", "组件能力不是映射")
		}
		for id, raw := range components {
			item := kernel.SemMap(raw)
			if !strings.Contains(id, ".") || !kernel.SemHas([]string{"candidate", "verified", "blocked"}, kernel.Text(item["status"])) || !kernel.ApIsArray(item["verified_architectures"]) || !kernel.ApIsArray(item["artifacts"]) || !kernel.ApIsArray(item["evidence"]) || !kernel.ApIsArray(item["blockers"]) || item["provider_kind"] != nil && !kernel.SemHas([]string{"yss-component", "platform-managed"}, kernel.Text(item["provider_kind"])) || !kernel.SemHas([]string{"active", "maintenance", "deprecated", "retired"}, kernel.First(kernel.Text(item["lifecycle"]), "active")) || !kernel.SemHas([]string{"allowed", "forbidden"}, kernel.First(kernel.Text(item["adoption_policy"]), "allowed")) {
				return nil, s.Reject("PLATFORM_COMPONENT", "组件能力结构或状态无效")
			}
			if kernel.Text(item["lifecycle"]) == "retired" && !contractRetirement(item["retirement_evidence"]) {
				return nil, s.Reject("PLATFORM_COMPONENT", "退役缺少组织零使用证据")
			}
			if !ContractUnique(item["verified_architectures"], false) {
				return nil, s.Reject("PLATFORM_COMPONENT", "验证架构重复或无效")
			}
			for _, family := range kernel.SemStrings(item["verified_architectures"]) {
				if !kernel.SemHas([]string{"domain-driven", "layered-mvc"}, family) {
					return nil, s.Reject("PLATFORM_COMPONENT", "未知验证架构")
				}
			}
			coordinates := map[string]bool{}
			for _, v := range kernel.SemList(item["artifacts"]) {
				a := kernel.SemMap(v)
				key := kernel.Text(a["group_id"]) + ":" + kernel.Text(a["artifact_id"])
				if kernel.Text(a["group_id"]) == "" || kernel.Text(a["artifact_id"]) == "" || !contractExactVersion(a["declared_version"]) || coordinates[key] || a["resolved_version"] != nil && !contractExactVersion(a["resolved_version"]) || strings.HasSuffix(kernel.Text(a["declared_version"]), "-SNAPSHOT") && a["resolved_version"] != nil && (a["resolved_version"] == a["declared_version"] || strings.HasSuffix(kernel.Text(a["resolved_version"]), "-SNAPSHOT")) {
					return nil, s.Reject("PLATFORM_COMPONENT", "组件精确坐标冲突")
				}
				coordinates[key] = true
				if kernel.Text(item["provider_kind"]) == "platform-managed" && (id != "contract.request-validation" || len(kernel.SemList(item["artifacts"])) != 1 || kernel.Text(a["group_id"]) != "org.springframework.boot" || kernel.Text(a["artifact_id"]) != "spring-boot-starter-validation" || a["declared_version"] != profile["spring_boot_version"]) {
					return nil, s.Reject("PLATFORM_COMPONENT", "平台托管validation坐标冲突")
				}
				if entry["artifact_resolution_evidence"] != nil {
					r := resolutions[key+":"+kernel.Text(a["declared_version"])]
					if r == nil {
						return nil, s.Reject("PLATFORM_COMPONENT", "组件没有实际解析条目")
					}
					var pom, jar, tree any
					if kernel.Text(r["pom_sha256"]) != "" {
						pom = "sha256:" + kernel.Text(r["pom_sha256"])
					}
					if kernel.Text(r["jar_sha256"]) != "" {
						jar = "sha256:" + kernel.Text(r["jar_sha256"])
					}
					if r["sources_match_source_tree"] == true && r["pom_matches_source_tree"] == true {
						tree = r["source_tree"]
					}
					if !ContractSame(a["resolved_version"], r["resolved_version"]) || !ContractSame(a["pom_sha256"], pom) || !ContractSame(a["jar_sha256"], jar) || !ContractSame(a["source_tree"], tree) {
						return nil, s.Reject("PLATFORM_COMPONENT", "组件绑定与解析证据漂移")
					}
				}
				if kernel.Text(item["status"]) == "verified" && (!contractExactVersion(a["resolved_version"]) || !ContractSHA(a["pom_sha256"]) || !ContractSHA(a["jar_sha256"]) || !contractGitTree(a["source_tree"]) || !strings.HasPrefix(kernel.Text(a["declared_version"]), strconv.Itoa(major)+".")) {
					return nil, s.Reject("PLATFORM_COMPONENT", "verified组件缺少精确代际证据")
				}
			}
			recipe := ContractWithout(item, "capability_id", "component_digest", "status", "evidence", "blockers")
			recipe["capability_id"] = id
			if !ContractSHA(item["component_digest"]) || kernel.Text(item["component_digest"]) != ContractDigest(recipe) {
				return nil, s.Reject("PLATFORM_COMPONENT", "组件配方摘要变化")
			}
			if kernel.Text(item["status"]) == "verified" {
				if kernel.Text(entry["status"]) != "verified" || len(kernel.SemList(item["verified_architectures"])) == 0 || len(kernel.SemList(item["artifacts"])) == 0 || len(kernel.SemList(item["evidence"])) == 0 || len(kernel.SemList(item["blockers"])) != 0 {
					return nil, s.Reject("PLATFORM_COMPONENT", "verified组件与平台状态或证据冲突")
				}
				for _, v := range kernel.SemList(item["evidence"]) {
					ev := kernel.SemMap(v)
					if !kernel.ContractPath(kernel.Text(ev["ref"])) || !ContractSHA(ev["digest"]) || !kernel.SemHas(item["verified_architectures"], kernel.Text(ev["architecture_family"])) {
						return nil, s.Reject("PLATFORM_COMPONENT", "组件证据不可移植或架构不匹配")
					}
				}
			} else if len(kernel.SemList(item["blockers"])) == 0 {
				return nil, s.Reject("PLATFORM_COMPONENT", "未验证能力必须记录阻断")
			}
		}
		if line == "boot3-java17" {
			if kernel.Text(profile["spring_cloud_version"]) != "2025.0.3" || kernel.Text(profile["spring_cloud_alibaba_version"]) != "2025.0.0.0" || kernel.Text(profile["validation_namespace"]) != "jakarta" || kernel.ContractN(profile["jackson_major"]) != 2 || kernel.Text(profile["auto_configuration_imports_path"]) != "META-INF/spring/org.springframework.boot.autoconfigure.AutoConfiguration.imports" || entry["platform_artifact_bindings"] == nil || !kernel.ApIsArray(entry["external_snapshot_bindings"]) || !kernel.ApIsArray(entry["external_artifact_bindings"]) || len(kernel.SemList(entry["external_snapshot_bindings"])) != 0 {
				return nil, s.Reject("PLATFORM_COMPONENT", "Boot3固定平台政策变化")
			}
			expected := map[string]string{"contract.request-validation": "org.springframework.boot:spring-boot-starter-validation:" + kernel.Text(profile["spring_boot_version"]), "component.cache": "com.yss.cloud:yss-component-cache-starter:3.1.0-SNAPSHOT", "component.distributed-id": "com.yss.cloud:yss-component-distributed-id:3.1.0-SNAPSHOT", "component.excel-import-export": "com.yss.cloud:yss-component-excel-mvc:3.0.0-SNAPSHOT"}
			for id, coordinate := range expected {
				if id != "contract.request-validation" && kernel.Text(kernel.SemMap(entry["bom"])["version"]) != "3.0.0-SNAPSHOT" {
					continue
				}
				a := kernel.SemList(kernel.SemMap(components[id])["artifacts"])
				if len(a) != 1 {
					return nil, s.Reject("PLATFORM_COMPONENT", "Boot3组件坐标缺失")
				}
				b := kernel.SemMap(a[0])
				if kernel.Text(b["group_id"])+":"+kernel.Text(b["artifact_id"])+":"+kernel.Text(b["declared_version"]) != coordinate {
					return nil, s.Reject("PLATFORM_COMPONENT", "Boot3组件固定坐标变化")
				}
			}
			if kernel.Text(kernel.SemMap(entry["bom"])["version"]) != "3.0.0-SNAPSHOT" {
				continue
			}
			for _, raw := range components {
				item := kernel.SemMap(raw)
				if kernel.Text(item["status"]) != "blocked" || len(kernel.SemList(item["verified_architectures"])) != 0 || len(kernel.SemList(item["evidence"])) != 0 {
					return nil, s.Reject("PLATFORM_COMPONENT", "Boot3未发布组件必须保持blocked")
				}
				for _, v := range kernel.SemList(item["artifacts"]) {
					a := kernel.SemMap(v)
					if kernel.Text(a["artifact_id"]) == "yss-component-excel-starter" {
						return nil, s.Reject("PLATFORM_COMPONENT", "移除的Excel starter重新出现")
					}
					for _, key := range []string{"resolved_version", "pom_sha256", "jar_sha256", "source_tree"} {
						value, exists := a[key]
						if !exists || value != nil {
							return nil, s.Reject("PLATFORM_COMPONENT", "Boot3未发布组件不得解析")
						}
					}
				}
			}
			external := kernel.SemList(entry["external_artifact_bindings"])
			if len(external) != 1 {
				return nil, s.Reject("PLATFORM_COMPONENT", "Boot3 Fesod外部发布绑定缺失")
			}
			a := kernel.SemMap(external[0])
			if kernel.Text(a["group_id"])+":"+kernel.Text(a["artifact_id"])+":"+kernel.Text(a["declared_version"]) != "org.apache.fesod:fesod-sheet:2.0.2-incubating" || kernel.Text(a["status"]) != "resolved" {
				return nil, s.Reject("PLATFORM_COMPONENT", "Boot3 Fesod固定发布坐标变化")
			}
		}
	}
	return c, nil
}

func init() {
	kernel.RegisterSemanticValidator("slice", verifySliceSemantic)
	kernel.RegisterSemanticValidator("scaffold", verifyScaffoldSemantic)
	kernel.RegisterSemanticValidator("scaffold-inputs", verifyScaffoldInputsSemantic)
	kernel.RegisterSemanticValidator("business-checkpoint", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		cp, e := s.Doc(ref)
		if e != nil {
			return e
		}
		return contractBusinessCheckpoint(s, cp, opts["required"] == "true")
	})
	kernel.RegisterSemanticValidator("openapi-validation", verifyOpenAPIValidationSemantic)
	kernel.RegisterSemanticValidator("scaffold-decisions", verifyScaffoldDecisionsSemantic)
	kernel.RegisterSemanticValidator("slice-preflight", verifySlicePreflightSemantic)
	kernel.RegisterSemanticValidator("business-tickets", verifyBusinessTicketsSemantic)
	kernel.RegisterSemanticValidator("technical-completion", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		doc, e := s.Doc(ref)
		if e != nil {
			return e
		}
		return contractTechnicalDesign(s, ref, doc, nil, opts)
	})
	kernel.RegisterSemanticValidator("repository-ready", verifyRepositoryReadySemantic)
	kernel.RegisterSemanticValidator("technical-transition", contractTechnicalTransition)
	kernel.RegisterSemanticValidator("service-transition", contractServiceTransition)
}

func contractTechnicalTransition(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	state, e := s.TransitionState(ref, opts["task-ref"])
	if e != nil {
		return e
	}
	if result, ok := kernel.Object(state["technical_analysis_result"]); ok {
		resultRef := kernel.Text(state["technical_analysis_result_ref"])
		persisted, e := s.Doc(resultRef)
		if e != nil {
			return e
		}
		for _, d := range []map[string]any{result, persisted} {
			if kernel.Text(d["result_schema"]) != "workflow-execution-result-v1" || kernel.Text(d["work_unit"]) != "work-unit.technical-analysis" || kernel.Text(d["result"]) != "completed" || d["current_version"] != true {
				return s.Reject("TECHNICAL_TRANSITION", "缺少当前已完成技术分析执行结果")
			}
		}
		if !kernel.SemHas(result["evidence_refs"], resultRef) {
			return s.Reject("TECHNICAL_TRANSITION", "技术分析证据没有持久化结果")
		}
		if e = ContractEvidence(s, result["evidence_refs"]); e != nil {
			return e
		}
		reconciliation := kernel.SemMap(result["context_reconciliation"])
		if kernel.Text(reconciliation["status"]) != "reconciled" || kernel.Text(reconciliation["ref"]) == "" {
			return s.Reject("TECHNICAL_TRANSITION", "技术分析Context未对账")
		}
		if e = contractTransitionContext(s, reconciliation, opts); e != nil {
			return e
		}
		api := kernel.SemMap(result["api_contract_decision"])
		if !ContractSame(api, kernel.SemMap(persisted["api_contract_decision"])) || !kernel.SemHas(result["evidence_refs"], kernel.Text(api["ref"])) {
			return s.Reject("TECHNICAL_TRANSITION", "技术分析当前API决定绑定不一致")
		}
		_, e = contractAPIDecision(s, api)
		return e
	}
	reconciliation := kernel.SemMap(state["context_reconciliation"])
	if kernel.Text(reconciliation["status"]) != "reconciled" || kernel.Text(reconciliation["ref"]) == "" {
		return s.Reject("TECHNICAL_TRANSITION", "后端技术Context未对账")
	}
	if e = contractTransitionContext(s, reconciliation, opts); e != nil {
		return e
	}
	handoff, spec := kernel.SemMap(state["strategic_handoff_consumption"]), kernel.SemMap(state["approved_spec"])
	if kernel.Text(handoff["result"]) == "inputs-verified" && handoff["ready_for_agent"] == false && kernel.Text(handoff["ref"]) != "" {
		consumed, e := s.Doc(kernel.Text(handoff["ref"]))
		if e != nil {
			return e
		}
		if e = handoffdomain.ContractHandoffConsumption(s, consumed, map[string]string{"consumer": "tactical", "inputs-only": "true"}); e != nil {
			return e
		}
	} else if kernel.Text(spec["status"]) == "approved" && spec["current_version"] == true && kernel.Text(spec["ref"]) != "" {
		if _, e = s.Bytes(kernel.Text(spec["ref"])); e != nil {
			return e
		}
	} else {
		return s.Reject("TECHNICAL_TRANSITION", "缺少当前批准Spec或战略接收")
	}
	prereqs := map[string]any{"technical_design": state["technical_design"], "data_architecture_decision": state["data_architecture_decision"], "api_contract_decision": state["api_contract_decision"], "engineering_contract_approval_ref": state["engineering_contract_approval_ref"]}
	for _, key := range []string{"technical_design", "data_architecture_decision", "api_contract_decision"} {
		binding := kernel.SemMap(prereqs[key])
		if kernel.Text(binding["status"]) != "approved" || binding["current_version"] != true || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(kernel.Text(binding["version"])) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(kernel.Text(binding["digest"])) {
			return s.Reject("TECHNICAL_TRANSITION", "缺少当前正式批准技术绑定: "+key)
		}
	}
	transitionOpts := kernel.SemanticOptions(opts)
	transitionOpts["project-id"] = kernel.First(kernel.Text(state["project_id"]), "backend")
	return contractBackendPrerequisites(s, prereqs, nil, transitionOpts)
}

func contractTransitionContext(s *kernel.SemanticSession, reconciliation map[string]any, opts map[string]string) error {
	if kernel.Text(reconciliation["status"]) != "reconciled" {
		return s.Reject("CONTEXT", "流转 Context 未对账")
	}
	if kernel.Text(reconciliation["ref"]) == "CONTEXT.md" {
		current, e := s.ContextContract()
		if e != nil {
			return e
		}
		if snapshot, ok := kernel.Object(reconciliation["snapshot"]); ok {
			_, e = s.ContextSnapshot(snapshot)
			return e
		}
		if digest := kernel.Text(reconciliation["document_digest"]); digest != "" && digest != kernel.Text(current["document_digest"]) {
			return s.Reject("CONTEXT_DRIFT", "流转 Context 摘要变化")
		}
		return nil
	}
	return s.Verify("context-reconciliation", kernel.Text(reconciliation["ref"]), opts)
}
func contractServiceTransition(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	return s.Reject("SCAFFOLD_RETIRED", "mvc-data-analysis-v1 仅允许历史只读审计；使用通用 MVC 后按需 setup-yss-harness")
}

func ContractJSON(v any) (string, error) {
	var b strings.Builder
	var write func(any) error
	write = func(v any) error {
		switch x := v.(type) {
		case nil:
			b.WriteString("null")
		case bool:
			b.WriteString(strconv.FormatBool(x))
		case string:
			kernel.WriteContextJSONString(&b, x)
		case json.Number:
			f, e := strconv.ParseFloat(string(x), 64)
			if e != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return fmt.Errorf("非有限数值")
			}
			if f == 0 {
				b.WriteByte('0')
			} else {
				raw := strconv.FormatFloat(f, 'g', -1, 64)
				if f >= 1e-6 && f < 1e21 || f <= -1e-6 && f > -1e21 {
					raw = strconv.FormatFloat(f, 'f', -1, 64)
				}
				raw = regexp.MustCompile(`e([+-])0+`).ReplaceAllString(raw, "e$1")
				b.WriteString(raw)
			}
		case int:
			b.WriteString(strconv.Itoa(x))
		case int64:
			b.WriteString(strconv.FormatInt(x, 10))
		case float64:
			return write(json.Number(strconv.FormatFloat(x, 'g', -1, 64)))
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool {
				a, ea := strconv.ParseUint(keys[i], 10, 32)
				z, ez := strconv.ParseUint(keys[j], 10, 32)
				ia := ea == nil && a < 4294967295 && strconv.FormatUint(a, 10) == keys[i]
				iz := ez == nil && z < 4294967295 && strconv.FormatUint(z, 10) == keys[j]
				if ia != iz {
					return ia
				}
				if ia {
					return a < z
				}
				return kernel.Utf16Less(keys[i], keys[j])
			})
			b.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					b.WriteByte(',')
				}
				kernel.WriteContextJSONString(&b, k)
				b.WriteByte(':')
				if e := write(x[k]); e != nil {
					return e
				}
			}
			b.WriteByte('}')
		case []any:
			b.WriteByte('[')
			for i, y := range x {
				if i > 0 {
					b.WriteByte(',')
				}
				if e := write(y); e != nil {
					return e
				}
			}
			b.WriteByte(']')
		case []string:
			a := make([]any, len(x))
			for i, y := range x {
				a[i] = y
			}
			return write(a)
		default:
			raw, e := json.Marshal(x)
			if e != nil {
				return e
			}
			y, e := schema.Parse(raw)
			if e != nil {
				return e
			}
			return write(y)
		}
		return nil
	}
	err := write(v)
	return b.String(), err
}

func ContractDigest(v any) string {
	b, e := ContractJSON(v)
	if e != nil {
		return ""
	}
	return "sha256:" + safefs.Digest([]byte(b))
}

func ContractSame(a, b any) bool {
	x, e := ContractJSON(a)
	y, f := ContractJSON(b)
	return e == nil && f == nil && x == y
}

func ContractCopy(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		out[k] = x
	}
	return out
}

func ContractWithout(v map[string]any, keys ...string) map[string]any {
	out := ContractCopy(v)
	for _, k := range keys {
		delete(out, k)
	}
	return out
}

func contractVersion(v any) string {
	if x, ok := v.(string); ok {
		return x
	}
	return fmt.Sprint(v)
}

func ContractRequired(s *kernel.SemanticSession, m map[string]any, keys ...string) error {
	for _, k := range keys {
		if v, ok := m[k]; !ok || v == nil || v == "" {
			return s.Reject("CONTRACT_INVALID", "缺少 "+k)
		}
	}
	return nil
}

func ContractUnique(v any, nonempty bool) bool {
	a := kernel.SemStrings(v)
	if _, ok := v.([]any); !ok {
		if _, ok = v.([]string); !ok {
			return false
		}
	}
	if nonempty && len(a) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		if strings.TrimSpace(x) == "" || seen[x] {
			return false
		}
		seen[x] = true
	}
	return true
}

func ContractWithin(candidate, parent string) bool {
	if candidate == "" || parent == "" {
		return false
	}
	r, e := filepath.Rel(filepath.Clean(parent), filepath.Clean(candidate))
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}

func contractHarnessProjectRoot(value string) bool {
	clean := strings.TrimSuffix(value, "/")
	if !kernel.ContractPath(clean) || kernel.SemHas(strings.Split(clean, "/"), ".") || regexp.MustCompile(`[\x00-\x1f]`).MatchString(clean) {
		return false
	}
	parts := strings.Split(clean, "/")
	if len(parts) >= 2 && parts[0] == "apps" && kernel.SemHas([]string{"backend", "frontend"}, parts[1]) {
		return len(parts) >= 3 && regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(parts[2])
	}
	return true
}

func ContractDate(v any) bool { _, e := time.Parse(time.RFC3339Nano, kernel.Text(v)); return e == nil }

func ContractUnion(groups ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range groups {
		for _, x := range a {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	return out
}

func contractBinding(s *kernel.SemanticSession, v any) (map[string]any, error) {
	m, ok := kernel.Object(v)
	if !ok {
		return nil, s.Reject("BINDING_INVALID", "依据须为 ref/digest 对象")
	}
	ref, d := kernel.Text(m["ref"]), kernel.Text(m["digest"])
	if ref == "" || !strings.HasPrefix(d, "sha256:") || !kernel.IsSHA256(strings.TrimPrefix(d, "sha256:")) {
		return nil, s.Reject("BINDING_INVALID", "依据引用或摘要非法")
	}
	observedRef := ref
	if filepath.IsAbs(ref) {
		var e error
		observedRef, e = dependencies.ContractVerificationRef(s, ref)
		if e != nil {
			return nil, e
		}
	}
	b, e := s.Bytes(observedRef)
	if e != nil {
		return nil, e
	}
	if "sha256:"+safefs.Digest(b) != d {
		return nil, s.Reject("CONTRACT_STALE", "依据原始字节变化: "+ref)
	}
	return m, nil
}

func ContractBoundDoc(s *kernel.SemanticSession, v any) (map[string]any, error) {
	m, e := contractBinding(s, v)
	if e != nil {
		return nil, e
	}
	ref := kernel.Text(m["ref"])
	if filepath.IsAbs(ref) {
		ref, e = dependencies.ContractVerificationRef(s, ref)
		if e != nil {
			return nil, e
		}
	}
	return s.Doc(ref)
}

func ContractEvidence(s *kernel.SemanticSession, refs any) error {
	if !ContractUnique(refs, true) {
		return s.Reject("EVIDENCE_REQUIRED", "证据引用须非空、唯一")
	}
	for _, ref := range kernel.SemStrings(refs) {
		if filepath.IsAbs(ref) {
			var e error
			ref, e = dependencies.ContractVerificationRef(s, ref)
			if e != nil {
				return e
			}
		}
		if _, e := s.Bytes(ref); e != nil {
			return fmt.Errorf("evidence %s: %w", ref, e)
		}
	}
	return nil
}

func contractBasis(s *kernel.SemanticSession, raw map[string]any) (map[string]map[string]any, error) {
	basis := kernel.SemMap(raw["basis"])
	result := map[string]map[string]any{}
	var resolve func(string, map[string]bool) (map[string]any, error)
	resolve = func(key string, stack map[string]bool) (map[string]any, error) {
		if stack[key] {
			return nil, s.Reject("REFERENCE_CYCLE", "依据别名循环: "+key)
		}
		if m := result[key]; m != nil {
			return m, nil
		}
		v, ok := basis[key]
		if !ok {
			return nil, s.Reject("CONTRACT_INVALID", "缺少依据: "+key)
		}
		stack[key] = true
		defer delete(stack, key)
		if alias, ok := v.(string); ok {
			m, e := resolve(alias, stack)
			if e != nil {
				return nil, e
			}
			result[key] = m
			return m, nil
		}
		m, e := contractBinding(s, v)
		if e != nil {
			return nil, e
		}
		result[key] = m
		return m, nil
	}
	for k := range basis {
		if _, e := resolve(k, map[string]bool{}); e != nil {
			return nil, e
		}
	}
	return result, nil
}

func contractNeedBasis(s *kernel.SemanticSession, b map[string]map[string]any, keys ...string) error {
	for _, k := range keys {
		if b[k] == nil {
			return s.Reject("CONTRACT_INVALID", "缺少依据: "+k)
		}
	}
	return nil
}

type ContractPlanEntry = reading.PlanEntry

func ContractPlan(s *kernel.SemanticSession, ref string) ([]ContractPlanEntry, bool, error) {
	raw, e := s.Bytes(ref)
	if e != nil {
		return nil, false, e
	}
	fm, body, e := kernel.Frontmatter(raw)
	if e != nil {
		return nil, false, e
	}
	v, e := schema.Parse(fm)
	if e != nil {
		return nil, false, e
	}
	supported := kernel.Text(kernel.SemMap(v)["content_profile"]) == "plan-spec-v1"
	source := []byte(body)
	tree := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(gmtext.NewReader(source))
	kind, depth, exampleDepth := "", 0, 0
	entries := []ContractPlanEntry{}
	names := map[string]string{"功能需求": "FR", "Functional Requirements": "FR", "非功能需求": "NFR", "Non-functional Requirements": "NFR", "验收标准": "AC", "Acceptance Criteria": "AC", "未决项": "Q", "Open Questions": "Q", "成功标准": "SC"}
	for n := tree.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok {
			title := strings.TrimSpace(string(h.Text(source)))
			if exampleDepth > 0 && h.Level <= exampleDepth {
				exampleDepth = 0
			}
			if regexp.MustCompile(`(?i)示例|虚构|example`).MatchString(title) {
				exampleDepth = h.Level
			}
			if exampleDepth > 0 {
				kind = ""
				continue
			}
			name := regexp.MustCompile(`^\d+[.、]\s*`).ReplaceAllString(title, "")
			if x := names[name]; x != "" {
				kind = x
				depth = h.Level
			} else if h.Level <= depth {
				kind = ""
			}
			continue
		}
		if exampleDepth > 0 {
			continue
		}
		t, ok := n.(*extast.Table)
		if !ok || kind == "" {
			continue
		}
		header := t.FirstChild()
		if header == nil {
			continue
		}
		headers := []string{}
		for cell := header.FirstChild(); cell != nil; cell = cell.NextSibling() {
			headers = append(headers, strings.TrimSpace(string(cell.Text(source))))
		}
		if len(headers) == 0 || headers[0] != "ID" {
			continue
		}
		for row := header.NextSibling(); row != nil; row = row.NextSibling() {
			values := []string{}
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				values = append(values, strings.TrimSpace(string(cell.Text(source))))
			}
			fields := map[string]string{}
			for i, h := range headers {
				if i < len(values) {
					fields[h] = values[i]
				} else {
					fields[h] = ""
				}
			}
			entries = append(entries, ContractPlanEntry{fields["ID"], kind, fields})
		}
	}
	return entries, supported, nil
}

func contractLocate(s *kernel.SemanticSession, ref, locator string) error {
	b, e := s.Bytes(ref)
	if e != nil {
		return e
	}
	if strings.HasPrefix(locator, "pointer:") {
		v, e := schema.Parse(b)
		if e != nil {
			return e
		}
		pointer := strings.TrimPrefix(locator, "pointer:")
		if pointer != "" && !strings.HasPrefix(pointer, "/") {
			return s.Reject("LOCATOR_INVALID", locator)
		}
		for _, k := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			k = strings.ReplaceAll(strings.ReplaceAll(k, "~1", "/"), "~0", "~")
			switch x := v.(type) {
			case map[string]any:
				var ok bool
				v, ok = x[k]
				if !ok {
					return s.Reject("LOCATOR_INVALID", locator)
				}
			case []any:
				i, err := strconv.Atoi(k)
				if err != nil || i < 0 || i >= len(x) {
					return s.Reject("LOCATOR_INVALID", locator)
				}
				v = x[i]
			default:
				return s.Reject("LOCATOR_INVALID", locator)
			}
		}
		return nil
	}
	if m := regexp.MustCompile(`^lines:(\d+)-(\d+)$`).FindStringSubmatch(locator); m != nil {
		a, _ := strconv.Atoi(m[1])
		z, _ := strconv.Atoi(m[2])
		if a < 1 || z < a || z > len(strings.Split(string(b), "\n")) {
			return s.Reject("LOCATOR_INVALID", locator)
		}
		return nil
	}
	if regexp.MustCompile(`^AC-[A-Za-z0-9][A-Za-z0-9._-]*$`).MatchString(locator) {
		entries, supported, e := ContractPlan(s, ref)
		if e != nil {
			return e
		}
		if supported {
			count := 0
			for _, entry := range entries {
				if entry.Kind == "AC" && entry.ID == locator {
					count++
				}
			}
			if count != 1 {
				return s.Reject("LOCATOR_INVALID", "验收 ID 缺失或不唯一: "+locator)
			}
			return nil
		}
	}
	body := string(b)
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		var rawBody []byte
		_, rawBody, e = kernel.Frontmatter(b)
		body = string(rawBody)
		if e != nil {
			return e
		}
	}
	count := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, locator) {
			count++
		}
	}
	if locator == "" || count != 1 {
		return s.Reject("LOCATOR_INVALID", "定位缺失或不唯一: "+locator)
	}
	return nil
}

type NativeSlice struct {
	Raw, Normalized map[string]any
	Basis           map[string]map[string]any
	Ref             string
	Repositories    map[string]map[string]any
}

func LoadNativeSlice(s *kernel.SemanticSession, ref string) (*NativeSlice, error) {
	doc, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	raw := doc
	if m, ok := kernel.Object(doc["slice_contract"]); ok {
		raw = m
	}
	n := kernel.ContractN(raw["schema_version"])
	if n != 2 && n != 3 {
		return nil, s.Reject("CONTRACT_SCHEMA", "Slice schema 仅支持 v2/v3")
	}
	out := &NativeSlice{Raw: raw, Normalized: ContractCopy(raw), Ref: ref, Basis: map[string]map[string]any{}, Repositories: map[string]map[string]any{}}
	if n == 2 {
		version, e := contractBusinessVersion(s)
		if e != nil {
			return nil, e
		}
		if version == 1 {
			return nil, s.Reject("BUSINESS_SLICE_V3_REQUIRED", "当前业务协议要求 v3")
		}
		ticket := kernel.Text(kernel.SemMap(raw["lifecycle_refs"])["ticket"])
		if ticket != "" {
			if e = contractImplementationTicket(s, ticket); e != nil {
				return nil, e
			}
		}
		compiler, e := s.Doc(".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml")
		if e != nil {
			return nil, e
		}
		for section, fields := range kernel.SemMap(compiler["slice_contract_required"]) {
			target := raw
			if section != "root" {
				target = kernel.SemMap(raw[section])
			}
			for _, field := range kernel.SemStrings(fields) {
				if _, ok := target[field]; !ok {
					return nil, s.Reject("CONTRACT_INVALID", "缺少 "+section+"."+field)
				}
			}
		}
		for _, section := range []string{"common", "resolution", "backend"} {
			if _, ok := kernel.SemMap(raw[section])["required_skills"].([]any); !ok {
				return nil, s.Reject("CONTRACT_INVALID", section+".required_skills 须为数组")
			}
		}
		units := kernel.SemList(raw["work_units"])
		if len(units) == 0 {
			return nil, s.Reject("CONTRACT_INVALID", "缺少工作单元")
		}
		for _, v := range units {
			u := kernel.SemMap(v)
			if !ContractSame(u["contract_id"], raw["contract_id"]) || !ContractSame(u["contract_version"], raw["contract_version"]) || kernel.Text(kernel.SemMap(u["work_unit"])["primary_skill"]) == "" {
				return nil, s.Reject("CONTRACT_INVALID", "工作单元合同身份不一致")
			}
		}
		return out, nil
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/slice-implementation-contract-v3.schema.json", raw); e != nil {
		return nil, e
	}
	basis, e := contractBasis(s, raw)
	if e != nil {
		return nil, e
	}
	out.Basis = basis
	if e = contractNeedBasis(s, basis, "spec", "ticket", "engineering_baseline", "implementation_repository", "build_architecture_checklist"); e != nil {
		return nil, e
	}
	if e = contractImplementationTicket(s, kernel.Text(basis["ticket"]["ref"])); e != nil {
		return nil, e
	}
	if e = contractSliceBusiness(s, raw, basis); e != nil {
		return nil, e
	}
	scope, resolution, extension, app := kernel.SemMap(raw["scope"]), ContractCopy(kernel.SemMap(raw["resolution"])), kernel.SemMap(raw["extensions"]), kernel.SemMap(raw["applicability"])
	if app["checks"] == nil {
		if e = contractNeedBasis(s, basis, "architecture_review"); e != nil {
			return nil, e
		}
	} else {
		if e = contractNeedBasis(s, basis, "lifecycle_registry", "process_tailoring"); e != nil {
			return nil, e
		}
		reg, e := s.Doc(kernel.Text(basis["lifecycle_registry"]["ref"]))
		if e != nil {
			return nil, e
		}
		check := kernel.ApFind(reg["checks"], "id", "check.architecture-reviewed")
		if kernel.Text(check["trigger"]) != "高风险或跨边界变化。" {
			return nil, s.Unavailable("CAPABILITY", "未知架构审查触发规则")
		}
		required := kernel.Text(scope["risk_level"]) == "high" || len(kernel.SemStrings(scope["project_roots"])) > 1
		for _, impact := range []string{"high-risk", "architecture", "cross-repo", "cross-boundary"} {
			required = required || kernel.SemHas(scope["impacted_areas"], impact)
		}
		if !kernel.SemHas([]string{"low", "medium", "high"}, kernel.Text(scope["risk_level"])) {
			return nil, s.Reject("APPLICABILITY", "未知风险等级")
		}
		for _, impact := range kernel.SemStrings(scope["impacted_areas"]) {
			if !kernel.SemHas([]string{"ui", "frontend", "backend", "api", "data", "persistence", "domain", "architecture", "cross-repo", "cross-boundary", "high-risk"}, impact) {
				return nil, s.Reject("APPLICABILITY", "未知影响")
			}
		}
		reason := fmt.Sprintf("%s 本切片风险 %s，影响 %s，工程数 %d。", kernel.Text(check["trigger"]), kernel.Text(scope["risk_level"]), kernel.First(strings.Join(kernel.SemStrings(scope["impacted_areas"]), ", "), "无"), len(kernel.SemStrings(scope["project_roots"])))
		expected := map[string]any{"check.architecture-reviewed": map[string]any{"status": "not-applicable", "reason": reason}}
		if required {
			expected = map[string]any{"check.architecture-reviewed": map[string]any{"status": "required", "reason": reason}}
			if e = contractNeedBasis(s, basis, "architecture_review"); e != nil {
				return nil, e
			}
		}
		if !ContractSame(app["checks"], expected) {
			return nil, s.Reject("APPLICABILITY", "材料适用性与生命周期规则冲突")
		}
	}
	for _, key := range []string{"frontend", "backend", "api", "cross_repo"} {
		a := kernel.SemMap(app[key])
		if kernel.Text(a["status"]) == "required" {
			if extension[key] == nil {
				return nil, s.Reject("APPLICABILITY", key+" 缺少子合同")
			}
		} else if kernel.Text(a["reason"]) == "" || extension[key] != nil {
			return nil, s.Reject("APPLICABILITY", key+" 不适用理由或子合同冲突")
		}
	}
	for impact, key := range map[string]string{"frontend": "frontend", "ui": "frontend", "backend": "backend", "api": "api", "cross-repo": "cross_repo"} {
		if kernel.SemHas(scope["impacted_areas"], impact) && kernel.Text(kernel.SemMap(app[key])["status"]) != "required" {
			return nil, s.Reject("APPLICABILITY", impact+" 影响与适用性冲突")
		}
	}
	if kernel.SemHas(scope["impacted_areas"], "data") || kernel.SemHas(scope["impacted_areas"], "persistence") {
		if e = contractNeedBasis(s, basis, "data_architecture"); e != nil {
			return nil, e
		}
	}
	if raw["ticket_policy"] != nil && kernel.Text(basis["ticket"]["version"]) == "" {
		return nil, s.Reject("CONTRACT_INVALID", "冻结 Ticket 缺少版本")
	}
	if raw["ticket_policy"] != nil && len(kernel.SemStrings(scope["project_roots"])) > 1 && kernel.SemMap(extension["cross_repo"])["repository_bindings"] == nil {
		return nil, s.Reject("REPOSITORY", "跨仓合同缺少逐仓登记")
	}
	if e = contractSliceRepositories(s, out); e != nil {
		return nil, e
	}
	if extension["frontend"] != nil {
		return nil, s.Reject("CONTRACT_INVALID", "前端实现已从生命周期移除；Slice 合同不能声明 frontend 子合同")
	}
	if extension["backend"] != nil {
		if e = contractNeedBasis(s, basis, "technical_design", "repository_registration", "manifest", "backend_repository", "maven_wrapper"); e != nil {
			return nil, e
		}
		if resolution["architecture_identity"] == nil && len(out.Repositories) == 0 {
			return nil, s.Reject("ARCHITECTURE", "后端缺少架构身份")
		}
		if kernel.Text(basis["technical_design"]["version"]) == "" {
			return nil, s.Reject("TECHNICAL_DESIGN", "技术设计版本缺失")
		}
		for _, locator := range kernel.SemStrings(kernel.SemMap(extension["backend"])["design_refs"]) {
			if e = contractLocate(s, kernel.Text(basis["technical_design"]["ref"]), locator); e != nil {
				return nil, e
			}
		}
	}
	apiKey, other := "no_api_impact_record", "openapi_freeze"
	if extension["api"] != nil {
		apiKey, other = other, apiKey
	}
	if e = contractNeedBasis(s, basis, apiKey); e != nil {
		return nil, e
	}
	if basis[other] != nil {
		return nil, s.Reject("APPLICABILITY", "API 影响结论冲突")
	}
	for _, p := range kernel.SemStrings(scope["allowed_write_paths"]) {
		if !kernel.ContractPath(p) {
			return nil, s.Reject("PATH", "写范围须为明确相对路径: "+p)
		}
	}
	if kernel.Text(scope["implementation_path_policy"]) != "external-repository-native" {
		for _, p := range kernel.SemStrings(scope["project_roots"]) {
			if !contractHarnessProjectRoot(p) {
				return nil, s.Reject("PATH", "Harness 工程需已登记的具体项目相对路径")
			}
		}
	}
	acceptance, verification := kernel.SemMap(raw["acceptance"]), kernel.SemMap(raw["verification"])
	for id, v := range acceptance {
		a := kernel.SemMap(v)
		src := basis[kernel.Text(a["source"])]
		if src == nil {
			return nil, s.Reject("ACCEPTANCE", "验收来源缺失: "+id)
		}
		if e = contractLocate(s, kernel.Text(src["ref"]), kernel.Text(a["locator"])); e != nil {
			return nil, e
		}
	}
	for id, v := range verification {
		check := kernel.SemMap(v)
		registered := false
		for _, root := range kernel.SemStrings(scope["project_roots"]) {
			registered = registered || ContractWithin(kernel.Text(check["cwd"]), root)
		}
		if !registered {
			return nil, s.Reject("VERIFICATION", "cwd 未登记: "+id)
		}
		for _, root := range kernel.SemStrings(check["dependency_roots"]) {
			if out.Repositories[root] == nil {
				return nil, s.Reject("REPOSITORY", "验证依赖根未登记")
			}
		}
		if !ContractUnique(check["acceptance_refs"], true) {
			return nil, s.Reject("ACCEPTANCE", "验证验收引用非法")
		}
		for _, a := range kernel.SemStrings(check["acceptance_refs"]) {
			if acceptance[a] == nil {
				return nil, s.Reject("ACCEPTANCE", "未知验收引用: "+a)
			}
		}
	}
	units := []any{}
	seen, used, covered := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, v := range kernel.SemList(raw["work_units"]) {
		u := ContractCopy(kernel.SemMap(v))
		id := kernel.Text(u["id"])
		if seen[id] {
			return nil, s.Reject("WORK_UNIT", "工作单元重复")
		}
		seen[id] = true
		allowed := u["allowed_write_paths"]
		if allowed == nil {
			allowed = scope["allowed_write_paths"]
		}
		for _, p := range kernel.SemStrings(allowed) {
			within := false
			for _, parent := range kernel.SemStrings(scope["allowed_write_paths"]) {
				within = within || ContractWithin(p, parent)
			}
			if !kernel.ContractPath(p) || !within {
				return nil, s.Reject("PATH", "工作单元写范围越界")
			}
		}
		root := kernel.Text(u["project_root"])
		if root == "" && len(kernel.SemStrings(scope["project_roots"])) == 1 {
			root = kernel.SemStrings(scope["project_roots"])[0]
		}
		if !kernel.SemHas(scope["project_roots"], root) {
			return nil, s.Reject("REPOSITORY", "工作单元须选择工程根")
		}
		ids := []string{}
		for id, v := range verification {
			if kernel.SemMap(v)["required_for_all"] == true {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		ids = ContractUnion(ids, kernel.SemStrings(u["verification_refs"]))
		checks := []any{}
		commands, evidence := []string{}, []string{}
		for _, id := range ids {
			v := verification[id]
			if v == nil || !ContractWithin(kernel.Text(kernel.SemMap(v)["cwd"]), root) {
				return nil, s.Reject("VERIFICATION", "未知验证或执行工程冲突")
			}
			c := ContractCopy(kernel.SemMap(v))
			c["id"] = id
			checks = append(checks, c)
			commands = append(commands, kernel.Text(c["command"]))
			evidence = ContractUnion(evidence, kernel.SemStrings(c["expected_evidence"]))
			used[id] = true
		}
		for _, skill := range append([]string{kernel.Text(u["primary_skill"])}, kernel.SemStrings(u["supporting_skills"])...) {
			if !kernel.SemHas(resolution["required_skills"], skill) {
				return nil, s.Reject("SKILL_CLOSURE", "工作单元 Skill 未冻结")
			}
		}
		for _, id := range kernel.SemStrings(u["acceptance_refs"]) {
			valid := false
			for _, v := range checks {
				valid = valid || kernel.SemHas(kernel.SemMap(v)["acceptance_refs"], id)
			}
			if acceptance[id] == nil || !valid {
				return nil, s.Reject("ACCEPTANCE", "工作单元验收缺少验证覆盖")
			}
			covered[id] = true
		}
		if kernel.Text(u["tdd_mode"]) == "controlled-generation" {
			if e = ContractRequired(s, kernel.SemMap(u["controlled_generation"]), "exception_reason", "generator", "generator_inputs", "expected_files", "verification_commands", "behavior_tests_after_generation"); e != nil {
				return nil, e
			}
		} else if u["controlled_generation"] != nil {
			return nil, s.Reject("TDD", "行为测试不得携带受控生成例外")
		}
		u["allowed_write_paths"], u["project_root"], u["verification"], u["verification_commands"], u["expected_evidence"] = allowed, root, checks, commands, evidence
		u["supporting_skills"] = kernel.SemStrings(u["supporting_skills"])
		u["forbidden_patterns"] = ContractUnion(kernel.SemStrings(scope["forbidden_patterns"]), kernel.SemStrings(u["forbidden_patterns"]))
		row := ContractCopy(u)
		row["contract_id"], row["contract_version"], row["work_unit"] = raw["contract_id"], raw["contract_version"], u
		units = append(units, row)
	}
	for id := range acceptance {
		if !covered[id] {
			return nil, s.Reject("ACCEPTANCE", "未分配验收: "+id)
		}
	}
	for id := range verification {
		if !used[id] {
			return nil, s.Reject("VERIFICATION", "未分配验证: "+id)
		}
	}
	for _, id := range kernel.SemStrings(kernel.SemMap(extension["cross_repo"])["integration_verification"]) {
		if len(kernel.SemStrings(kernel.SemMap(verification[id])["dependency_roots"])) == 0 {
			return nil, s.Reject("VERIFICATION", "联合验证须显式依赖根")
		}
	}
	refs := map[string]any{}
	for k, m := range basis {
		refs[k] = m["ref"]
	}
	refs["openapi_freeze_or_no_impact"] = basis[apiKey]["ref"]
	resolution["freshness"] = "current"
	if resolution["architecture_identity"] != nil {
		resolution["architecture_identity_digest"] = strings.TrimPrefix(ContractDigest(resolution["architecture_identity"]), "sha256:")
	}
	if resolution["architecture_identity"] != nil {
		selected := map[string]any{}
		for key, value := range basis {
			selected[key] = value
		}
		evidence, e := contractSliceArchitectureEvidence(s, resolution["architecture_identity"], selected)
		if e != nil {
			return nil, e
		}
		resolution["architecture_evidence"] = evidence
	}
	if basis["technical_design"] != nil {
		resolution["technical_design"] = basis["technical_design"]
	}
	if basis["frontend_delivery"] != nil {
		resolution["frontend_delivery"] = map[string]any{"acceptance_ref": basis["frontend_delivery"]["ref"], "digest": basis["frontend_delivery"]["digest"]}
	}
	common := ContractCopy(scope)
	common["required_capabilities"], common["required_skills"], common["quality_baseline_ref"] = resolution["required_capabilities"], resolution["required_skills"], basis["engineering_baseline"]["ref"]
	allCommands, allEvidence := []string{}, []string{}
	for _, v := range verification {
		c := kernel.SemMap(v)
		allCommands = append(allCommands, kernel.Text(c["command"]))
		allEvidence = ContractUnion(allEvidence, kernel.SemStrings(c["expected_evidence"]))
	}
	common["verification_commands"], common["expected_evidence_files"] = allCommands, allEvidence
	out.Normalized["common"], out.Normalized["resolution"], out.Normalized["lifecycle_refs"], out.Normalized["work_units"] = common, resolution, refs, units
	out.Normalized["readiness"] = map[string]any{"blockers": []any{}, "stale_inputs": []any{}}
	for _, key := range []string{"backend", "frontend"} {
		m := ContractCopy(kernel.SemMap(extension[key]))
		m["status"] = "required"
		if extension[key] == nil {
			m = map[string]any{"status": "not-applicable"}
		}
		if key == "frontend" && resolution["frontend_delivery"] != nil {
			m["delivery"] = resolution["frontend_delivery"]
		}
		out.Normalized[key] = m
	}
	out.Normalized["repositories"] = out.Repositories
	return out, nil
}

func contractBusinessVersion(s *kernel.SemanticSession) (int, error) {
	identity, e := s.Doc("yss-project.yaml")
	if e != nil {
		return 0, e
	}
	if kernel.Text(identity["repository_mode"]) == "template-source" {
		return 0, nil
	}
	exists, e := s.Exists(".template-spec/agents/issue-tracker.md")
	if e != nil || !exists {
		return 0, e
	}
	tracker, e := kernel.ContractTicketMetadata(s, ".template-spec/agents/issue-tracker.md")
	if e != nil {
		return 0, e
	}
	value, present := kernel.SemMap(tracker["tracker"])["business_ticket_version"]
	if present && kernel.ContractN(value) != 1 {
		return 0, s.Reject("BUSINESS_TICKET_VERSION_UNSUPPORTED", "未知业务 Ticket 协议版本")
	}
	return kernel.ContractN(value), nil
}

func contractImplementationTicket(s *kernel.SemanticSession, ref string) error {
	if regexp.MustCompile(`(^|/)(work-items|business-tickets)/`).MatchString(ref) {
		return s.Reject("IMPLEMENTATION_TICKET", "阶段或业务 Ticket 不能授予实现资格")
	}
	doc, e := kernel.ContractTicketMetadata(s, ref)
	if e != nil {
		return e
	}
	if kernel.SemHas([]string{"stage-work-item", "business-ticket", "business-ticket-set"}, kernel.Text(doc["kind"])) {
		return s.Reject("IMPLEMENTATION_TICKET", "非实现 Ticket")
	}
	return nil
}

func verifySliceSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	c, e := LoadNativeSlice(s, ref)
	if e != nil {
		return e
	}
	bound := map[string]string{}
	for key, value := range opts {
		bound[key] = value
	}
	approval := kernel.First(opts["approval-ref"], opts["approval_ref"])
	if approval == "" && opts["checkpoint"] != "" {
		cp, err := s.Doc(opts["checkpoint"])
		if err != nil {
			return err
		}
		gate := kernel.SemMap(kernel.SemMap(cp["gates"])["gate.slice-contract-approved"])
		if gate["status"] != "approved" || gate["subject_ref"] != c.Ref {
			return s.Reject("SLICE_APPROVAL_REQUIRED", "当前 checkpoint 未绑定已批准的 Slice")
		}
		approval = kernel.First(kernel.Text(gate["approval_ref"]), opts["checkpoint"])
	}
	if approval == "" {
		return s.Reject("SLICE_APPROVAL_REQUIRED", "Slice verify 需要独立当前 checkpoint 或批准引用")
	}
	bound["approval-ref"] = approval
	if len(c.Repositories) > 0 && bound["unit"] == "" {
		units := kernel.SemList(c.Normalized["work_units"])
		if len(units) == 0 {
			return s.Reject("WORK_UNIT", "跨仓合同未登记工作单元")
		}
		for _, value := range units {
			unit := kernel.Text(kernel.SemMap(value)["id"])
			if unit == "" {
				return s.Reject("WORK_UNIT", "跨仓合同工作单元缺少身份")
			}
			bound["unit"] = unit
			if err := ContractSliceFresh(s, c, bound); err != nil {
				return err
			}
		}
		return nil
	}
	return ContractSliceFresh(s, c, bound)
}

// A consumer selects the expected subject from its current asset or checkpoint.
// Approval contents may never supply their own expected subject, scope or author.
func ContractApproval(s *kernel.SemanticSession, gate string, binding map[string]any, approvalRef string, opts map[string]string) error {
	assetRef := kernel.Text(binding["ref"])
	raw, e := s.Bytes(assetRef)
	if e != nil {
		return e
	}
	asset := map[string]any{}
	state := kernel.SemMap(binding["approval_context"])
	if len(state) == 0 {
		asset, e = s.Doc(assetRef)
		if e != nil {
			return e
		}
	}
	if digest := kernel.Text(binding["digest"]); digest != "" {
		if strings.TrimPrefix(digest, "sha256:") != safefs.Digest(raw) {
			return s.Reject("APPROVAL_CURRENT_INVALID", "当前资产批准摘要过期")
		}
	}
	if len(state) == 0 {
		state = kernel.SemMap(asset["approval_context"])
	}
	if len(state) == 0 && opts["checkpoint"] != "" {
		cp, e := s.Doc(opts["checkpoint"])
		if e != nil {
			return e
		}
		for _, bucket := range []string{"gates", "checks"} {
			g := kernel.SemMap(kernel.SemMap(cp[bucket])[gate])
			state = kernel.SemMap(g["approval_context"])
			if len(state) == 0 && kernel.Text(g["subject_ref"]) != "" {
				state = ContractCopy(g)
				state["review_package"] = false
			}
			if len(state) > 0 {
				break
			}
		}
	}
	if len(state) == 0 {
		scope := asset["approval_scope"]
		if scope == nil {
			scope = asset["scope"]
		}
		state = map[string]any{"subject_ref": assetRef, "approval_scope": scope, "basis": asset["basis"], "drafter_principal_ref": asset["drafter_principal_ref"], "review_package": strings.HasPrefix(gate, "gate.")}
	}
	if value, present := binding["review_package"]; present {
		state = ContractCopy(state)
		state["review_package"] = value
	}
	expected, e := approvaldomain.ApprovalExpectationFromState(s, gate, state)
	if e != nil {
		return e
	}
	if kernel.Text(expected["subject_ref"]) != assetRef {
		basis := kernel.ApArray(expected["basis"])
		found := false
		for _, v := range basis {
			m := kernel.SemMap(v)
			if kernel.Text(m["ref"]) == assetRef {
				found = true
			}
		}
		if !found {
			b, e := s.Bind(assetRef)
			if e != nil {
				return e
			}
			basis = append(basis, map[string]any{"ref": b.Ref, "digest": b.Digest})
		}
		expected["basis"] = basis
	}
	record, e := s.Doc(approvalRef)
	if e != nil {
		return e
	}
	record, e = approvaldomain.SelectApprovalRecordSemantic(s, record, gate)
	if e != nil {
		return e
	}
	if e = approvaldomain.AssertCurrentApprovalSemantic(s, record, expected); e != nil {
		return e
	}
	if binding["id"] != nil || binding["version"] != nil {
		matched := false
		for _, v := range kernel.SemList(record["artifact_bindings"]) {
			m := kernel.SemMap(v)
			idMatches := binding["id"] == nil || ContractSame(m["id"], binding["id"])
			versionMatches := binding["version"] == nil || ContractSame(m["version"], binding["version"])
			matched = matched || idMatches && versionMatches && kernel.Text(m["digest"]) == kernel.Text(binding["digest"])
		}
		if !matched {
			return s.Reject("APPROVAL_CURRENT_INVALID", "批准未绑定当前资产身份/版本/摘要")
		}
	}
	return nil
}

func contractSliceApproval(s *kernel.SemanticSession, c *NativeSlice, opts map[string]string) error {
	return ContractSliceApprovalBinding(s, c, opts, nil)
}

func ContractSliceApprovalBinding(s *kernel.SemanticSession, c *NativeSlice, opts map[string]string, consumerBinding map[string]any) error {
	raw := c.Raw
	approval := kernel.First(opts["approval-ref"], opts["approval_ref"])
	if approval == "" {
		return s.Reject("SLICE_APPROVAL_REQUIRED", "缺少 Slice 批准引用")
	}
	if kernel.Text(raw["status"]) != "approved" {
		return s.Reject("SLICE_APPROVAL_REQUIRED", "Slice 尚未 approved")
	}
	binding, e := s.Bind(c.Ref)
	if e != nil {
		return e
	}
	b := map[string]any{"ref": c.Ref, "id": raw["contract_id"], "version": raw["contract_version"], "digest": binding.Digest, "review_package": false}
	if raw["approval_context"] != nil {
		b["approval_context"] = raw["approval_context"]
	}
	if consumerBinding != nil && consumerBinding["approval_context"] != nil {
		b["approval_context"] = consumerBinding["approval_context"]
	}
	policy := kernel.SemMap(s.Roles["gate_policy"])
	orchestrator := kernel.SemHas(policy["orchestrator"], "gate.slice-contract-approved")
	for _, bucket := range []string{"dual_digital_human", "digital_human_review", "check_reviews"} {
		if kernel.ApFind(policy[bucket], "gate", "gate.slice-contract-approved") != nil {
			orchestrator = false
		}
	}
	if orchestrator {
		cp, e := s.Doc(approval)
		if e != nil {
			return e
		}
		if cp["gate_id"] != nil || kernel.Text(cp["repository_mode"]) != "project-instance" || kernel.Text(cp["status"]) == "blocked" || len(kernel.SemList(cp["blockers"])) > 0 {
			return s.Reject("SLICE_APPROVAL_REQUIRED", "Slice 编排批准不是当前 checkpoint")
		}
		if e = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); e != nil {
			return e
		}
		gate := kernel.SemMap(kernel.SemMap(cp["gates"])["gate.slice-contract-approved"])
		implementation := kernel.SemMap(kernel.SemMap(cp["human_review"])["implementation"])
		if kernel.Text(gate["status"]) != "approved" || kernel.Text(gate["subject_ref"]) != c.Ref || kernel.Text(implementation["slice_contract_ref"]) != c.Ref {
			return s.Reject("SLICE_APPROVAL_REQUIRED", "checkpoint 未批准当前 Slice")
		}
		implementation = ContractCopy(implementation)
		implementation["user_decisions"] = kernel.SemMap(cp["human_review"])["user_decisions"]
		if e = AssertImplementationDecisionSemantic(s, implementation); e != nil {
			return e
		}
	} else if e = ContractApproval(s, "gate.slice-contract-approved", b, approval, opts); e != nil {
		return e
	}
	if kernel.ContractN(raw["schema_version"]) == 3 {
		container, e := s.Doc(approval)
		if e != nil {
			return e
		}
		refs := kernel.SemStrings(container["evidence_refs"])
		if gates := kernel.SemMap(container["gates"]); gates != nil {
			refs = kernel.SemStrings(kernel.SemMap(gates["gate.slice-contract-approved"])["evidence_refs"])
		}
		found := false
		for _, reviewRef := range refs {
			if !regexp.MustCompile(`[.](yaml|yml|json)$`).MatchString(reviewRef) {
				continue
			}
			review, e := s.Doc(reviewRef)
			if e != nil {
				return e
			}
			rows := []map[string]any{review}
			if kernel.Text(review["kind"]) == "review-bundle" {
				rows, e = approvaldomain.ReviewBundleRowsSemantic(s, review)
				if e != nil {
					return e
				}
			}
			var r map[string]any
			for _, row := range rows {
				if kernel.Text(row["gate_id"]) == "check.design-reviewed" && kernel.Text(row["subject_ref"]) == c.Ref {
					r = row
				}
			}
			if r == nil {
				continue
			}
			if kernel.Text(r["subject_ref"]) != c.Ref || strings.TrimPrefix(kernel.Text(r["subject_digest"]), "sha256:") != strings.TrimPrefix(binding.Digest, "sha256:") || kernel.Text(r["principal_ref"]) == kernel.Text(r["drafter_principal_ref"]) {
				return s.Reject("DESIGN_REVIEW_REQUIRED", "设计审查未独立绑定当前 Slice")
			}
			reviewOpts := map[string]string{"checkpoint": approval}
			if e = ContractApproval(s, "check.design-reviewed", b, reviewRef, reviewOpts); e != nil {
				return e
			}
			for _, v := range kernel.SemList(r["findings"]) {
				f := kernel.SemMap(v)
				if kernel.Text(f["kind"]) == "suggestion" && kernel.Text(f["follow_up"]) == "" {
					return s.Reject("DESIGN_REVIEW_REQUIRED", "建议缺少 follow_up")
				}
				if kernel.Text(f["kind"]) != "suggestion" && (!kernel.SemHas([]string{"requirement-violation", "missing-evidence", "important-risk"}, kernel.Text(f["kind"])) || kernel.Text(f["status"]) != "resolved") {
					return s.Reject("DESIGN_REVIEW_REQUIRED", "设计审查阻断项未解决")
				}
			}
			found = true
		}
		if !found {
			return s.Reject("DESIGN_REVIEW_REQUIRED", "未登记 check.design-reviewed")
		}
	}
	return nil
}

// 选出本次校验的工程与工作单元：本地合同选唯一单元，跨仓合同按工作单元的工程重建依据与解析。
// 公开入口 contract verify --kind slice-preflight：执行前检查报告，与 Node 的 `scripts/slice-contract verify` 同形
// （read_only、ref/id/version/digest、checks、approval_validity、execution_allowed、blockers）。可读性与结构、批准、新鲜度是三层不同的证据；
// 有阻断原因时报告仍写进 coverage.slice_preflight，同时以拒绝收尾（退出码非 0，与 Node 一致）。
func verifySlicePreflightSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	c, e := LoadNativeSlice(s, ref)
	if e != nil {
		return e
	}
	binding, e := s.Bind(c.Ref)
	if e != nil {
		return e
	}
	checks := map[string]any{"readable": "passed", "structure": "passed", "approval": "not-checked", "freshness": "not-checked"}
	blockers := []any{}
	compiler, e := s.Doc(".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml")
	if e != nil {
		return e
	}
	if kernel.ContractN(c.Raw["schema_version"]) == 2 {
		required := kernel.SemMap(compiler["slice_contract_required"])
		sections := make([]string, 0, len(required))
		for section := range required {
			sections = append(sections, section)
		}
		sort.Strings(sections)
		for _, section := range sections {
			target := c.Normalized
			if section != "root" {
				target = kernel.SemMap(c.Normalized[section])
			}
			for _, field := range kernel.SemStrings(required[section]) {
				if _, ok := target[field]; target == nil || !ok {
					blockers = append(blockers, "missing:"+section+"."+field)
				}
			}
		}
		for _, section := range []string{"common", "resolution", "backend"} {
			if _, ok := kernel.SemMap(c.Normalized[section])["required_skills"].([]any); !ok {
				blockers = append(blockers, "invalid:"+section+".required_skills")
			}
		}
		units := kernel.SemList(c.Normalized["work_units"])
		if len(units) == 0 {
			blockers = append(blockers, "slice-contract-work-units-missing")
		}
		for _, v := range units {
			unit := kernel.SemMap(v)
			id := kernel.Text(unit["id"])
			if unit["contract_id"] != c.Normalized["contract_id"] {
				blockers = append(blockers, "work-unit-contract-id-mismatch:"+id)
			}
			if unit["contract_version"] != c.Normalized["contract_version"] {
				blockers = append(blockers, "work-unit-contract-version-mismatch:"+id)
			}
			if kernel.Text(kernel.SemMap(unit["work_unit"])["primary_skill"]) == "" {
				blockers = append(blockers, "work-unit-primary-skill-missing:"+id)
			}
		}
		if len(blockers) > 0 {
			checks["structure"] = "failed"
		}
	}
	approval := kernel.First(opts["approval-ref"], opts["approval_ref"])
	if approval != "" && len(blockers) == 0 {
		bound := map[string]string{}
		for key, value := range opts {
			bound[key] = value
		}
		bound["approval-ref"] = approval
		if e := contractSliceApproval(s, c, bound); e != nil {
			checks["approval"] = "failed"
			blockers = append(blockers, kernel.SemanticCode(e))
		} else if code := sliceExecutionContractCode(s, c); code != "" {
			checks["approval"] = "failed"
			blockers = append(blockers, code)
		} else {
			checks["approval"] = "passed"
			reasons, e := slicePreflightFreshness(s, c, bound)
			if e != nil {
				return e
			}
			checks["freshness"] = "passed"
			if len(reasons) > 0 {
				checks["freshness"] = "failed"
			}
			for _, reason := range reasons {
				blockers = append(blockers, reason)
			}
		}
	}
	allowed := approval != "" && len(blockers) == 0 && checks["freshness"] == "passed"
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	s.Report.Coverage["slice_preflight"] = map[string]any{"read_only": true, "ref": c.Ref, "id": c.Raw["contract_id"], "version": c.Raw["contract_version"], "digest": binding.Digest,
		"checks": checks, "approval_validity": checks["approval"], "execution_allowed": allowed, "blockers": blockers}
	if len(blockers) > 0 {
		return s.Reject("SLICE_PREFLIGHT_BLOCKED", "Slice 执行前检查有阻断原因")
	}
	return nil
}

// Node 的 createApprovedExecutionContext 在批准阶段还核对合同自身：写范围、就绪条件、Skill/Compiler 冻结摘要、resolution 当前性、架构身份摘要。
// 返回 Node 的原因码（空串表示通过）；这些属于「批准」层，不是「新鲜度」层。
func sliceExecutionContractCode(s *kernel.SemanticSession, c *NativeSlice) string {
	n := c.Normalized
	if !ContractUnique(kernel.SemMap(n["common"])["allowed_write_paths"], true) {
		return "EXECUTION_SCOPE_MISSING"
	}
	readiness := kernel.SemMap(n["readiness"])
	if len(kernel.SemList(readiness["blockers"])) > 0 || len(kernel.SemList(readiness["stale_inputs"])) > 0 {
		return "EXECUTION_CONTRACT_STALE"
	}
	registry, e := s.Doc(".template-spec/agents/yss-skill-registry.yaml")
	if e != nil {
		return "EXECUTION_CONTRACT_STALE"
	}
	compiler, e := s.Doc(".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml")
	if e != nil {
		return "EXECUTION_CONTRACT_STALE"
	}
	r := kernel.SemMap(n["resolution"])
	if kernel.Text(r["registry_digest"]) != strings.TrimPrefix(ContractDigest(registry), "sha256:") || kernel.Text(r["compiler_contract_digest"]) != strings.TrimPrefix(ContractDigest(compiler), "sha256:") || kernel.Text(r["freshness"]) != "current" {
		return "EXECUTION_CONTRACT_STALE"
	}
	if identity := kernel.SemMap(r["architecture_identity"]); len(identity) > 0 && kernel.Text(r["architecture_identity_digest"]) != strings.TrimPrefix(ContractDigest(identity), "sha256:") {
		return "EXECUTION_CONTRACT_STALE"
	}
	return ""
}

// 逐项独立评估新鲜度，原因码与 Node 的 evaluateContractFreshness 一致（顺序也一致）。
func slicePreflightFreshness(s *kernel.SemanticSession, c *NativeSlice, opts map[string]string) ([]string, error) {
	registry, e := s.Doc(".template-spec/agents/yss-skill-registry.yaml")
	if e != nil {
		return nil, e
	}
	compiler, e := s.Doc(".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml")
	if e != nil {
		return nil, e
	}
	if kernel.ContractN(registry["schema_version"]) != 3 || kernel.ContractN(compiler["schema_version"]) != 2 {
		return nil, s.Unavailable("CAPABILITY", "未知 Skill/Compiler 协议")
	}
	ctx, n, r, e := contractSliceSelect(s, c, opts)
	if e != nil {
		return []string{"EXECUTION_APPROVAL_INVALID"}, nil
	}
	reasons := []string{}
	if design := kernel.SemMap(r["technical_design"]); len(design) > 0 {
		doc, e := ContractBoundDoc(s, design)
		if e == nil && (kernel.Text(design["version"]) == "" || kernel.Text(doc["version"]) != kernel.Text(design["version"])) {
			e = s.Reject("TECHNICAL_DESIGN_STALE", "技术设计版本变化")
		}
		if e == nil {
			e = contractTechnicalDesign(s, kernel.Text(design["ref"]), doc, ctx, opts)
		}
		if e != nil {
			reasons = append(reasons, "technical-design-stale-or-unavailable")
		}
	} else if kernel.Text(kernel.SemMap(n["backend"])["status"]) == "required" {
		reasons = append(reasons, "technical-design-stale-or-unavailable")
	}
	if kernel.Text(r["registry_digest"]) != strings.TrimPrefix(ContractDigest(registry), "sha256:") {
		reasons = append(reasons, "registry-digest-changed")
	}
	if kernel.Text(r["compiler_contract_digest"]) != strings.TrimPrefix(ContractDigest(compiler), "sha256:") {
		reasons = append(reasons, "compiler-contract-digest-changed")
	}
	if identity := kernel.SemMap(r["architecture_identity"]); len(identity) > 0 {
		if e := contractArchitecture(s, identity, ctx, registry, opts); e != nil {
			switch kernel.SemanticCode(e) {
			case "ARCH_IDENTITY_SHAPE", "ARCH_PROFILE_UNSUPPORTED", "ARCH_SOURCE_UNSUPPORTED":
				reasons = append(reasons, "architecture-identity-invalid")
			default:
				reasons = append(reasons, "architecture-evidence-stale-or-unavailable")
			}
		}
		if kernel.Text(r["architecture_identity_digest"]) != strings.TrimPrefix(ContractDigest(identity), "sha256:") {
			reasons = append(reasons, "architecture-identity-digest-changed")
		}
	}
	if e := contractComponents(s, r, registry); e != nil {
		reasons = append(reasons, "component-binding-drift")
	}
	return reasons, nil
}

func contractSliceSelect(s *kernel.SemanticSession, c *NativeSlice, opts map[string]string) (*NativeSlice, map[string]any, map[string]any, error) {
	n := c.Normalized
	r := kernel.SemMap(n["resolution"])
	var e error
	unit := opts["unit"]
	selectedContext := c
	if len(c.Repositories) == 0 {
		selectedContext, e = ContractSelectLocalUnit(s, c, unit)
		if e != nil {
			return nil, nil, nil, e
		}
		n = selectedContext.Normalized
		r = kernel.SemMap(n["resolution"])
	}
	if len(c.Repositories) > 0 {
		if unit == "" && len(kernel.SemList(n["work_units"])) == 1 {
			unit = kernel.Text(kernel.SemMap(kernel.SemList(n["work_units"])[0])["id"])
		}
		if unit == "" {
			return nil, nil, nil, s.Reject("WORK_UNIT", "跨仓校验须显式工作单元")
		}
		found := false
		for _, v := range kernel.SemList(n["work_units"]) {
			u := kernel.SemMap(v)
			if kernel.Text(u["id"]) == unit {
				repo := c.Repositories[kernel.Text(u["project_root"])]
				if repo == nil {
					return nil, nil, nil, s.Reject("REPOSITORY", "工作单元未登记")
				}
				r = kernel.SemMap(repo["resolution"])
				copyContext := *c
				copyContext.Basis = map[string]map[string]any{}
				for key, value := range c.Basis {
					copyContext.Basis[key] = value
				}
				for key, value := range kernel.SemMap(repo["basis"]) {
					copyContext.Basis[key] = kernel.SemMap(value)
				}
				copyContext.Normalized = ContractCopy(n)
				common := ContractCopy(kernel.SemMap(n["common"]))
				common["allowed_write_paths"] = u["allowed_write_paths"]
				common["project_roots"] = []any{u["project_root"]}
				copyContext.Normalized["common"] = common
				copyContext.Normalized["resolution"] = r
				selectedContext = &copyContext
				found = true
				break
			}
		}
		if !found {
			return nil, nil, nil, s.Reject("WORK_UNIT", "工作单元未唯一选择工程")
		}
	}
	return selectedContext, n, r, nil
}

func ContractSliceFresh(s *kernel.SemanticSession, c *NativeSlice, opts map[string]string) error {
	if e := contractSliceApproval(s, c, opts); e != nil {
		return e
	}
	n := c.Normalized
	registry, e := s.Doc(".template-spec/agents/yss-skill-registry.yaml")
	if e != nil {
		return e
	}
	compiler, e := s.Doc(".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml")
	if e != nil {
		return e
	}
	if kernel.ContractN(registry["schema_version"]) != 3 || kernel.ContractN(compiler["schema_version"]) != 2 {
		return s.Unavailable("CAPABILITY", "未知 Skill/Compiler 协议")
	}
	r := kernel.SemMap(n["resolution"])
	if kernel.Text(r["freshness"]) != "current" || len(kernel.SemList(kernel.SemMap(n["readiness"])["blockers"])) > 0 || len(kernel.SemList(kernel.SemMap(n["readiness"])["stale_inputs"])) > 0 {
		return s.Reject("CONTRACT_STALE", "合同 readiness/freshness 不允许执行")
	}
	if kernel.Text(r["registry_digest"]) != strings.TrimPrefix(ContractDigest(registry), "sha256:") || kernel.Text(r["compiler_contract_digest"]) != strings.TrimPrefix(ContractDigest(compiler), "sha256:") {
		return s.Reject("CONTRACT_STALE", "Skill/Compiler 冻结摘要变化")
	}
	if !ContractUnique(kernel.SemMap(n["common"])["allowed_write_paths"], true) {
		return s.Reject("PATH", "合同缺少写范围")
	}
	selectedContext, n, r, e := contractSliceSelect(s, c, opts)
	if e != nil {
		return e
	}
	identity := kernel.SemMap(r["architecture_identity"])
	if len(identity) > 0 {
		if kernel.Text(r["architecture_identity_digest"]) != strings.TrimPrefix(ContractDigest(identity), "sha256:") {
			return s.Reject("ARCHITECTURE_STALE", "架构身份摘要变化")
		}
		if e = contractArchitecture(s, identity, selectedContext, registry, opts); e != nil {
			return e
		}
	}
	if e = contractComponents(s, r, registry); e != nil {
		return e
	}
	if design := kernel.SemMap(r["technical_design"]); len(design) > 0 {
		doc, e := ContractBoundDoc(s, design)
		if e != nil {
			return e
		}
		if kernel.Text(design["version"]) == "" || kernel.Text(doc["version"]) != kernel.Text(design["version"]) {
			return s.Reject("TECHNICAL_DESIGN_STALE", "技术设计版本变化")
		}
		if e = contractTechnicalDesign(s, kernel.Text(design["ref"]), doc, selectedContext, opts); e != nil {
			return e
		}
	} else if kernel.Text(kernel.SemMap(n["backend"])["status"]) == "required" {
		return s.Reject("TECHNICAL_DESIGN_REQUIRED", "后端 Slice 缺少当前技术设计")
	}
	s.Report.ExecutionAuthorization = "not-evaluated"
	return nil
}

// A responsibility view is derived only after the entire source Slice has
// been normalized and approved. It never changes that source or its basis.
func ContractSelectLocalUnit(s *kernel.SemanticSession, c *NativeSlice, unit string) (*NativeSlice, error) {
	if kernel.ContractN(c.Raw["schema_version"]) != 3 || unit == "" || len(c.Repositories) != 0 {
		return c, nil
	}
	u := kernel.ApFind(c.Normalized["work_units"], "id", unit)
	if u == nil {
		return nil, s.Reject("WORK_UNIT", "未知当前批准工作单元")
	}
	role := kernel.Text(u["role_id"])
	if !kernel.SemHas([]string{"role.backend-engineer", "role.frontend-engineer", "role.backend-agent", "role.frontend-agent"}, role) {
		return nil, s.Reject("WORK_UNIT", "单仓职责选择需要明确后端或前端角色")
	}
	selected := *c
	selected.Normalized = ContractCopy(c.Normalized)
	common := ContractCopy(kernel.SemMap(c.Normalized["common"]))
	common["project_roots"] = []any{u["project_root"]}
	common["allowed_write_paths"] = u["allowed_write_paths"]
	selected.Normalized["common"] = common
	selected.Normalized["work_units"] = []any{u}
	if role == "role.backend-engineer" || role == "role.backend-agent" {
		selected.Normalized["frontend"] = map[string]any{"status": "not-applicable"}
	} else {
		selected.Normalized["backend"] = map[string]any{"status": "not-applicable"}
	}
	return &selected, nil
}

func contractSliceRepositories(s *kernel.SemanticSession, c *NativeSlice) error {
	x := kernel.SemMap(kernel.SemMap(c.Raw["extensions"])["cross_repo"])
	bindings := kernel.SemMap(x["repository_bindings"])
	if len(bindings) == 0 {
		return nil
	}
	if e := contractNeedBasis(s, c.Basis, "repository_preparation"); e != nil {
		return e
	}
	prep, e := s.Doc(kernel.Text(c.Basis["repository_preparation"]["ref"]))
	if e != nil {
		return e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/implementation-repository-preparation-result.schema.json", prep); e != nil {
		return e
	}
	identities, roots := map[string]bool{}, map[string]bool{}
	for root, v := range bindings {
		canonical, e := filepath.Abs(root)
		if e != nil || canonical != root || roots[canonical] {
			return s.Reject("REPOSITORY", "工程根有路径别名")
		}
		roots[canonical] = true
		keys := kernel.SemMap(v)
		selected := map[string]any{}
		for name, key := range keys {
			if name == "recipe_refs" || name == "capability_refs" {
				continue
			}
			b := c.Basis[kernel.Text(key)]
			if b == nil {
				return s.Reject("REPOSITORY", "缺少逐仓 basis."+kernel.Text(key))
			}
			selected[name] = b
		}
		reg, e := ContractBoundDoc(s, selected["implementation_repository"])
		if e != nil {
			return e
		}
		base, e := ContractBoundDoc(s, selected["engineering_baseline"])
		if e != nil {
			return e
		}
		actual := filepath.Join(kernel.Text(reg["local_worktree"]), kernel.First(kernel.Text(reg["project_root"]), "."))
		if actual != root || kernel.Text(reg["status"]) != "current" || kernel.Text(base["status"]) != "current" || reg["repository_id"] != base["repository_id"] || reg["project_id"] != base["project_id"] || !ContractUnique(reg["allowed_write_paths"], true) {
			return s.Reject("REPOSITORY", "逐仓身份、状态或写范围冲突")
		}
		key := kernel.Text(reg["repository_id"]) + "\x00" + kernel.Text(reg["project_id"])
		if identities[key] {
			return s.Reject("REPOSITORY", "同一仓库工程重复绑定")
		}
		identities[key] = true
		matches := []map[string]any{}
		for _, v := range kernel.SemList(prep["projects"]) {
			p := kernel.SemMap(v)
			if p["project_id"] == reg["project_id"] && kernel.Text(p["repository_ref"]) == kernel.Text(kernel.SemMap(selected["implementation_repository"])["ref"]) {
				matches = append(matches, p)
			}
		}
		if len(matches) != 1 || kernel.Text(matches[0]["status"]) == "not-applicable" || kernel.Text(matches[0]["project_root"]) != root {
			return s.Reject("REPOSITORY", "准备结果没有唯一当前工程")
		}
		project := matches[0]
		if !kernel.SemHas([]string{"existing-and-onboarded", "initialized-and-verified"}, kernel.Text(project["status"])) {
			return s.Reject("REPOSITORY_NOT_READY", "工程准备未就绪")
		}
		for _, k := range []string{"repository_ref", "baseline_ref"} {
			if kernel.Text(project[k]) != "" {
				if _, e = s.Bytes(kernel.Text(project[k])); e != nil {
					return e
				}
			}
		}
		if kernel.Text(project["delivery_role"]) == "backend" {
			p := kernel.SemMap(kernel.SemMap(project["design_prerequisites"])["technical_design"])
			if !ContractSame(p["ref"], kernel.SemMap(selected["technical_design"])["ref"]) || !ContractSame(p["digest"], kernel.SemMap(selected["technical_design"])["digest"]) {
				return s.Reject("REPOSITORY", "准备结果技术设计绑定冲突")
			}
		}
		identity := base["architecture_identity"]
		if identity == nil {
			identity = reg["architecture_identity"]
		}
		if base["architecture_identity"] != nil && reg["architecture_identity"] != nil && !ContractSame(base["architecture_identity"], reg["architecture_identity"]) {
			return s.Reject("ARCHITECTURE", "逐仓架构身份冲突")
		}
		resolution := ContractWithout(kernel.SemMap(c.Raw["resolution"]), "architecture_identity", "architecture_identity_digest")
		if identity != nil {
			resolution["architecture_identity"], resolution["architecture_identity_digest"] = identity, strings.TrimPrefix(ContractDigest(identity), "sha256:")
		}
		if selected["technical_design"] != nil {
			resolution["technical_design"] = selected["technical_design"]
		}
		if selected["frontend_delivery"] != nil {
			b := kernel.SemMap(selected["frontend_delivery"])
			resolution["frontend_delivery"] = map[string]any{"acceptance_ref": b["ref"], "digest": b["digest"]}
		}
		if selected["repository_registration"] == nil {
			selected["repository_registration"] = selected["implementation_repository"]
		}
		if identity != nil {
			evidence, e := contractSliceArchitectureEvidence(s, identity, selected)
			if e != nil {
				return e
			}
			resolution["architecture_evidence"] = evidence
		}
		if e = s.RegisterExternalRoot(root, kernel.Text(kernel.SemMap(selected["implementation_repository"])["ref"])); e != nil {
			return e
		}
		c.Repositories[root] = map[string]any{"registration": reg, "project": project, "basis": selected, "resolution": resolution}
	}
	if !kernel.SameStringSet(kernel.SemMap(c.Raw["scope"])["project_roots"], MapKeysAsAny(c.Repositories)) {
		return s.Reject("REPOSITORY", "逐仓绑定未覆盖全部工程根")
	}
	for _, key := range []string{"delivery_order", "rollback_order"} {
		if !kernel.SameStringSet(x[key], MapKeysAsAny(c.Repositories)) {
			return s.Reject("REPOSITORY", key+" 必须完整且唯一")
		}
	}
	for _, v := range kernel.SemList(c.Raw["work_units"]) {
		u := kernel.SemMap(v)
		repo := c.Repositories[kernel.Text(u["project_root"])]
		if repo == nil {
			return s.Reject("REPOSITORY", "工作单元未选择唯一工程")
		}
		allowed := u["allowed_write_paths"]
		if allowed == nil {
			allowed = kernel.SemMap(c.Raw["scope"])["allowed_write_paths"]
		}
		for _, p := range kernel.SemStrings(allowed) {
			within := false
			for _, parent := range kernel.SemStrings(kernel.SemMap(repo["registration"])["allowed_write_paths"]) {
				within = within || ContractWithin(p, parent)
			}
			if !within {
				return s.Reject("PATH", "超出逐仓写范围")
			}
		}
	}
	return nil
}

// The derived evidence always uses the selected repository's independently
// bound registration and assets, never another repository's global basis.
func contractSliceArchitectureEvidence(s *kernel.SemanticSession, identity any, basis map[string]any) (map[string]any, error) {
	registration, e := ContractBoundDoc(s, basis["repository_registration"])
	if e != nil {
		return nil, e
	}
	keys := []string{"engineering_baseline", "repository_registration", "manifest"}
	if kernel.ContractN(kernel.SemMap(identity)["schema_version"]) != 2 {
		out := map[string]any{}
		for _, key := range keys {
			doc, e := ContractBoundDoc(s, basis[key])
			if e != nil {
				return nil, e
			}
			out[key] = doc
		}
		return out, nil
	}
	out := map[string]any{"repository_registration": basis["repository_registration"]}
	for _, key := range []string{"engineering_baseline", "manifest"} {
		original, bound := kernel.SemMap(kernel.SemMap(registration["architecture_evidence"])[key]), kernel.SemMap(basis[key])
		if original["ref"] != bound["ref"] || original["digest"] != bound["digest"] || kernel.Text(original["ref"]) == "" {
			return nil, s.Reject("ARCHITECTURE", "逐仓登记未绑定当前 "+key)
		}
		if _, e := contractBinding(s, original); e != nil {
			return nil, e
		}
		out[key] = original
	}
	return out, nil
}

func MapKeysAsAny(v map[string]map[string]any) []string {
	out := []string{}
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contractSliceBusiness(s *kernel.SemanticSession, raw map[string]any, basis map[string]map[string]any) error {
	ticket, e := kernel.ContractTicketMetadata(s, kernel.Text(basis["ticket"]["ref"]))
	if e != nil {
		return e
	}
	wanted := kernel.SemStrings(ticket["business_ticket_refs"])
	set := basis["business_ticket_set"]
	version, e := contractBusinessVersion(s)
	if e != nil {
		return e
	}
	if version != 1 && ticket["business_ticket_refs"] == nil && set == nil {
		return nil
	}
	if kernel.Text(ticket["kind"]) != "vertical-slice-ticket" || set == nil {
		return s.Reject("BUSINESS_SLICE_INVALID", "业务票来源要求 vertical-slice-ticket 和冻结集合")
	}
	business, e := contractBusinessTickets(s, kernel.Text(set["ref"]))
	if e != nil {
		return e
	}
	if kernel.Text(ticket["business_ticket_set_ref"]) != "" && kernel.Text(ticket["business_ticket_set_ref"]) != kernel.Text(set["ref"]) {
		return s.Reject("BUSINESS_SLICE_INVALID", "Slice/业务票集合绑定冲突")
	}
	spec := kernel.SemMap(business["spec"])
	if spec["ref"] != basis["spec"]["ref"] || spec["digest"] != basis["spec"]["digest"] {
		return s.Reject("BUSINESS_SLICE_INVALID", "业务票与当前 Spec 冲突")
	}
	if len(wanted) == 0 {
		return s.Reject("BUSINESS_SLICE_INVALID", "未选择业务票")
	}
	known := kernel.SemMap(business["ticket_documents"])
	acs := []string{}
	for _, id := range wanted {
		b := kernel.SemMap(known[id])
		if len(b) == 0 {
			return s.Reject("BUSINESS_SLICE_INVALID", "未知业务票: "+id)
		}
		acs = ContractUnion(acs, kernel.SemStrings(b["acceptance_refs"]))
	}
	ticketAC := kernel.SemStrings(ticket["acceptance_refs"])
	for _, id := range ticketAC {
		if !kernel.SemHas(acs, id) {
			return s.Reject("BUSINESS_SLICE_INVALID", "实现票验收超出业务票范围")
		}
	}
	covered := []string{}
	for _, v := range kernel.SemMap(raw["acceptance"]) {
		a := kernel.SemMap(v)
		if kernel.Text(a["source"]) != "spec" || !kernel.SemHas(ticketAC, kernel.Text(a["locator"])) {
			return s.Reject("BUSINESS_SLICE_INVALID", "Slice 验收未引用当前业务验收")
		}
		covered = ContractUnion(covered, []string{kernel.Text(a["locator"])})
	}
	if !kernel.SameStringSet(ticketAC, covered) {
		return s.Reject("BUSINESS_SLICE_INVALID", "Slice 未完整覆盖实现票验收")
	}
	return nil
}

func contractBusinessTickets(s *kernel.SemanticSession, ref string) (map[string]any, error) {
	return ContractBusinessTicketsMode(s, ref, "formal")
}

func contractBusinessLocator(s *kernel.SemanticSession, binding map[string]any) error {
	ref, locator, kind := kernel.Text(binding["ref"]), kernel.Text(binding["locator"]), kernel.Text(binding["locator_kind"])
	b, e := s.Bytes(ref)
	if e != nil {
		return e
	}
	count := 0
	if regexp.MustCompile(`[.](yaml|yml|json)$`).MatchString(ref) {
		if kind == "heading" {
			return s.Reject("BUSINESS_TICKETS", "结构化来源不能使用 heading 定位")
		}
		value, e := schema.Parse(b)
		if e != nil {
			return s.Unavailable("INPUT", e.Error())
		}
		var walk func(any)
		walk = func(value any) {
			switch v := value.(type) {
			case []any:
				for _, item := range v {
					walk(item)
				}
			case map[string]any:
				for key, item := range v {
					if (key == "id" || strings.HasSuffix(key, "_id")) && kernel.Text(item) == locator {
						count++
					} else {
						walk(item)
					}
				}
			}
		}
		walk(value)
	} else {
		if strings.HasPrefix(string(b), "---\n") || strings.HasPrefix(string(b), "---\r\n") {
			_, body, err := kernel.Frontmatter(b)
			if err != nil {
				return err
			}
			b = body
		}
		tree := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(gmtext.NewReader(b))
		exampleDepth := 0
		for node := tree.FirstChild(); node != nil; node = node.NextSibling() {
			if h, ok := node.(*ast.Heading); ok {
				title := strings.TrimSpace(string(h.Text(b)))
				if exampleDepth != 0 && h.Level <= exampleDepth {
					exampleDepth = 0
				}
				if regexp.MustCompile(`(?i)示例|虚构|example`).MatchString(title) {
					exampleDepth = h.Level
				}
				if exampleDepth == 0 && kind == "heading" && title == locator {
					count++
				}
				continue
			}
			if exampleDepth != 0 || kind == "heading" {
				continue
			}
			if table, ok := node.(*extast.Table); ok {
				for row := table.FirstChild(); row != nil; row = row.NextSibling() {
					if _, header := row.(*extast.TableHeader); header {
						continue
					}
					if cell := row.FirstChild(); cell != nil && strings.TrimSpace(string(cell.Text(b))) == locator {
						count++
					}
				}
			}
		}
	}
	if count != 1 {
		return s.Reject("BUSINESS_TICKETS", "来源稳定定位缺失或不唯一")
	}
	return nil
}

func ContractBusinessTicketsMode(s *kernel.SemanticSession, ref, mode string) (map[string]any, error) {
	out, _, err := ContractCollectBusinessTickets(s, ref, mode, "")
	return out, err
}

func verifyBusinessTicketsSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	source, local, reviewRef, err := handoffdomain.ContractBusinessSource(s, ref, opts["review-ref"])
	mode := kernel.First(opts["mode"], "draft")
	var report map[string]any
	if err == nil {
		_, report, err = ContractCollectBusinessTickets(source, local, mode, reviewRef)
		if source != s {
			prefix, mappingErr := filepath.Rel(s.Root, source.Root)
			if mappingErr != nil {
				return s.Unavailable("PATH", mappingErr.Error())
			}
			mapped := func(ref string) string { return path.Join(filepath.ToSlash(prefix), source.LocalRef(ref)) }
			for _, name := range []string{"inputs", "tickets", "diagnostics"} {
				key := "ref"
				if name == "diagnostics" {
					key = "source_ref"
				}
				for _, value := range kernel.SemList(report[name]) {
					row := kernel.SemMap(value)
					row[key] = mapped(kernel.Text(row[key]))
				}
			}
			for _, name := range []string{"spec", "semantic_review"} {
				if value, ok := kernel.Object(report[name]); ok {
					row := ContractCopy(value)
					row["ref"] = mapped(kernel.Text(row["ref"]))
					report[name] = row
				}
			}
		}
	} else {
		report = contractBusinessReport(mode)
		report["status"] = "blocked"
		report["diagnostics"] = []any{map[string]any{"code": kernel.SemanticCode(err), "message": err.Error(), "source_ref": ref, "locator": nil, "recovery": "核对来源、补齐引用或重新审查当前业务 Ticket 集"}}
	}
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	s.Report.Coverage["business_tickets"] = report
	return err
}

func contractBusinessReport(mode string) map[string]any {
	return map[string]any{"schema_version": 1, "read_only": true, "execution_authorization": "not-evaluated", "mode": mode, "status": "passed", "diagnostics": []any{}, "inputs": []any{}, "tickets": []any{}, "uncovered": []string{}, "unassessed": []string{"business-granularity", "semantic-fidelity", "approval-validity"}}
}

// The report and all strict consumers share these rules. Failed IO stops only
// its dependent branch; independent rules collect diagnostics, never authority.
func ContractCollectBusinessTickets(s *kernel.SemanticSession, ref, mode, reviewRef string) (out, report map[string]any, failure error) {
	report = contractBusinessReport(mode)
	issues, inputs := []any{}, []any{}
	observed := map[string]string{}
	issue := func(code, message, source string, locator any) {
		issues = append(issues, map[string]any{"code": code, "message": message, "source_ref": source, "locator": locator, "recovery": "核对来源、补齐引用或重新审查当前业务 Ticket 集"})
		if failure == nil {
			failure = s.Reject(code, message)
		}
	}
	problem := func(err error, code, source string, locator any) bool {
		if err == nil {
			return false
		}
		if code == "" {
			code = kernel.SemanticCode(err)
		}
		issue(code, err.Error(), source, locator)
		if kernel.SemanticExit(err) == 2 {
			failure = err
		}
		return true
	}
	defer func() {
		report["diagnostics"], report["inputs"] = issues, inputs
		if failure != nil {
			report["status"] = "blocked"
			out = nil
		}
	}()
	read := func(source string) ([]byte, bool) {
		if !kernel.ContractPath(source) {
			issue("BUSINESS_PATH_INVALID", "来源必须为项目内规范相对路径", source, nil)
			return nil, false
		}
		bytes, err := s.Bytes(source)
		if problem(err, "", source, nil) {
			return nil, false
		}
		digest := "sha256:" + safefs.Digest(bytes)
		if previous, ok := observed[source]; ok {
			if previous != digest {
				issue("INPUT_DRIFT", "读取期间来源变化", source, nil)
				return nil, false
			}
		} else {
			observed[source] = digest
			inputs = append(inputs, map[string]any{"ref": source, "digest": digest})
		}
		return bytes, true
	}
	doc := func(source string) (map[string]any, bool) {
		if _, ok := read(source); !ok {
			return nil, false
		}
		value, err := s.Doc(source)
		return value, !problem(err, "", source, nil)
	}
	binding := func(value any) (map[string]any, bool) {
		bound, valid := kernel.Object(value)
		if !valid || kernel.Text(bound["ref"]) == "" || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(kernel.Text(bound["version"])) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(kernel.Text(bound["digest"])) {
			issue("BINDING_INVALID", "需要 ref/version/digest 来源绑定", ref, nil)
			return nil, false
		}
		source := kernel.Text(bound["ref"])
		if _, ok := read(source); !ok {
			return nil, false
		}
		if observed[source] != bound["digest"] {
			issue("SOURCE_STALE", "来源原始字节已变化", source, nil)
		}
		return bound, true
	}
	validate := func(value map[string]any, source string) {
		err := s.ValidateSchema(".template-spec/process/schemas/business-tickets-v1.schema.json", value)
		code := "SCHEMA_INVALID"
		if kernel.SemanticExit(err) == 2 {
			code = "SCHEMA_UNAVAILABLE"
		}
		problem(err, code, source, nil)
	}
	if mode != "draft" && mode != "formal" {
		issue("MODE_INVALID", "业务票模式必须为 draft 或 formal", ref, nil)
		return
	}
	set, ok := doc(ref)
	if !ok {
		return
	}
	validate(set, ref)
	if kernel.ContractN(set["schema_version"]) != 1 || set["kind"] != "business-ticket-set" || strings.TrimSpace(kernel.Text(set["id"])) == "" || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(kernel.Text(set["version"])) || !kernel.SemHas([]string{"draft", "ready-for-human"}, kernel.Text(set["status"])) {
		issue("BUSINESS_SET_INVALID", "业务票集合身份、版本或状态非法", ref, nil)
	}
	report["id"], report["version"], report["spec"] = set["id"], set["version"], set["spec"]
	report["coverage_deferred"] = append([]any{}, kernel.SemList(set["coverage_deferred"])...)
	ids, order := map[string]ContractPlanEntry{}, []string{}
	specRef := kernel.Text(kernel.SemMap(set["spec"])["ref"])
	if bound, valid := binding(set["spec"]); valid {
		specRef = kernel.Text(bound["ref"])
		if _, valid = doc(specRef); valid {
			entries, supported, err := ContractPlan(s, specRef)
			if !problem(err, "", specRef, nil) {
				if !supported {
					issue("SPEC_FORMAT_UNASSESSED", "Spec 需显式采用 plan-spec-v1；旧格式不推断覆盖", specRef, nil)
				}
				hasFR, hasAC := false, false
				for _, entry := range entries {
					if !kernel.SemHas([]string{"FR", "NFR", "AC"}, entry.Kind) {
						continue
					}
					if _, exists := ids[entry.ID]; exists {
						issue("DUPLICATE_ID", "Spec ID 重复: "+entry.ID, specRef, entry.ID)
					} else {
						ids[entry.ID] = entry
						order = append(order, entry.ID)
					}
					hasFR, hasAC = hasFR || entry.Kind == "FR", hasAC || entry.Kind == "AC"
				}
				if !hasFR || !hasAC {
					issue("SPEC_COVERAGE_UNASSESSED", "缺少可机械读取的 FR/AC", specRef, nil)
				}
			}
		}
	}
	docs, covered, seenPaths := map[string]any{}, map[string]bool{}, map[string]bool{}
	reportTickets, ticketOrder := []any{}, []string{}
	if len(kernel.SemList(set["tickets"])) == 0 {
		issue("BUSINESS_TICKETS_REQUIRED", "业务票集合必须包含票据", ref, nil)
	}
	for _, value := range kernel.SemList(set["tickets"]) {
		bound, valid := binding(value)
		if !valid {
			continue
		}
		ticketRef, id := kernel.Text(bound["ref"]), kernel.Text(bound["id"])
		ticket, valid := doc(ticketRef)
		if !valid {
			continue
		}
		validate(ticket, ticketRef)
		if docs[id] != nil || seenPaths[ticketRef] {
			issue("DUPLICATE_ID", "Ticket ID 或路径重复: "+id, ticketRef, nil)
		}
		seenPaths[ticketRef] = true
		if kernel.ContractN(ticket["schema_version"]) != 1 || ticket["kind"] != "business-ticket" || !regexp.MustCompile(`^BT-[A-Za-z0-9][A-Za-z0-9._-]*$`).MatchString(kernel.Text(ticket["id"])) || !ContractSame(ticket["id"], bound["id"]) || !ContractSame(ticket["version"], bound["version"]) {
			issue("BUSINESS_TICKET_INVALID", "类型、稳定 ID 或版本不匹配", ticketRef, nil)
		}
		if !kernel.SemHas([]string{"draft", "ready-for-human"}, kernel.Text(ticket["status"])) {
			issue("BUSINESS_READINESS_FORBIDDEN", "业务票不得声明实现就绪", ticketRef, nil)
		}
		if !strings.HasPrefix(ticketRef, path.Join(path.Dir(ref), "business-tickets")+"/") || !strings.HasSuffix(ticketRef, ".md") {
			issue("BUSINESS_TICKET_LOCATION", "业务票需独立放在 business-tickets/", ticketRef, nil)
		}
		if !ContractSame(ticket["spec"], set["spec"]) {
			issue("SPEC_BINDING_MISMATCH", "票据与集合的 Spec 依据不一致", ticketRef, nil)
		}
		for _, field := range []string{"requirement_refs", "acceptance_refs", "dependencies"} {
			if !ContractUnique(ticket[field], field != "dependencies") {
				issue("REQUIRED_FIELD", "缺少或重复 "+field, ticketRef, nil)
			}
		}
		if len(kernel.SemStrings(ticket["requirement_refs"])) == 0 || len(kernel.SemStrings(ticket["acceptance_refs"])) == 0 {
			issue("COVERAGE_REQUIRED", "需要需求与验收引用", ticketRef, nil)
		}
		for _, requirement := range kernel.SemStrings(ticket["requirement_refs"]) {
			entry, exists := ids[requirement]
			if !exists || !kernel.SemHas([]string{"FR", "NFR"}, entry.Kind) {
				issue("DANGLING_REFERENCE", "无效引用 "+requirement, ticketRef, nil)
			} else {
				covered[requirement] = true
			}
		}
		for _, ac := range kernel.SemStrings(ticket["acceptance_refs"]) {
			entry := ids[ac]
			if entry.Kind != "AC" {
				issue("DANGLING_REFERENCE", "无效引用 "+ac, ticketRef, nil)
			} else {
				covered[ac] = true
			}
			requirements := regexp.MustCompile(`\b(?:FR|NFR)-[A-Za-z0-9][A-Za-z0-9._-]*\b`).FindAllString(entry.Fields["需求引用"], -1)
			mismatch := len(requirements) == 0
			for _, requirement := range requirements {
				mismatch = mismatch || !kernel.SemHas(ticket["requirement_refs"], requirement)
			}
			if mismatch {
				issue("AC_REQUIREMENT_MISMATCH", "验收 "+ac+" 与本票需求来源不一致", ticketRef, ac)
			}
		}
		for _, value := range kernel.SemList(ticket["source_refs"]) {
			bound, valid := binding(value)
			if valid && kernel.Text(bound["locator"]) != "" {
				problem(contractBusinessLocator(s, bound), "DANGLING_REFERENCE", kernel.Text(bound["ref"]), bound["locator"])
			}
		}
		raw, err := s.Bytes(ticketRef)
		if !problem(err, "", ticketRef, nil) {
			for _, heading := range []string{"业务结果", "范围", "非目标", "验收", "风险"} {
				if !regexp.MustCompile(`(?m)^##\s+` + heading + `\s*$`).Match(raw) {
					issue("REQUIRED_SECTION", "缺少 "+heading, ticketRef, nil)
				}
			}
		}
		for _, value := range kernel.SemList(ticket["open_questions"]) {
			q := kernel.SemMap(value)
			complete := true
			for _, field := range []string{"id", "question", "owner", "resolve_by"} {
				complete = complete && strings.TrimSpace(kernel.Text(q[field])) != ""
			}
			if _, valid := q["blocking"].(bool); !valid || !complete {
				issue("QUESTION_INCOMPLETE", "未决项需问题、责任人、时点及阻断判断", ticketRef, nil)
			}
			if mode == "formal" && q["blocking"] == true {
				issue("OPEN_BLOCKER", kernel.Text(q["question"]), ticketRef, nil)
			}
		}
		if mode == "formal" && ticket["status"] != "ready-for-human" {
			issue("TICKET_DRAFT", "正式化仍有草案票", ticketRef, nil)
		}
		if docs[id] == nil {
			ticketOrder = append(ticketOrder, id)
		}
		docs[id] = ticket
		reportTickets = append(reportTickets, map[string]any{"id": id, "ref": ticketRef, "version": bound["version"], "source_digest": bound["digest"], "source_refs": ticket["source_refs"], "requirement_refs": ticket["requirement_refs"], "acceptance_refs": ticket["acceptance_refs"], "dependencies": ticket["dependencies"]})
	}
	report["tickets"] = reportTickets
	active, done := map[string]bool{}, map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if active[id] {
			issue("DEPENDENCY_CYCLE", "依赖环包含 "+id, ref, nil)
			return
		}
		if done[id] {
			return
		}
		active[id] = true
		for _, dep := range kernel.SemStrings(kernel.SemMap(docs[id])["dependencies"]) {
			if docs[dep] == nil {
				issue("DANGLING_DEPENDENCY", id+" 依赖未知票 "+dep, ref, nil)
			} else {
				visit(dep)
			}
		}
		delete(active, id)
		done[id] = true
	}
	for _, id := range ticketOrder {
		visit(id)
	}
	deferred := map[string]bool{}
	for _, value := range kernel.SemList(set["coverage_deferred"]) {
		row := kernel.SemMap(value)
		id := kernel.First(kernel.Text(row["source_id"]), kernel.Text(row["id"]))
		if _, exists := ids[id]; !exists || covered[id] || deferred[id] {
			issue("DEFERRED_COVERAGE_INVALID", "延期引用无效或重复: "+id, ref, nil)
		}
		complete := true
		for _, field := range []string{"reason", "risk", "owner", "target_version", "followup_ticket_ref", "verification_plan"} {
			complete = complete && strings.TrimSpace(kernel.Text(row[field])) != ""
		}
		if !complete {
			issue("DEFERRED_INCOMPLETE", "延期缺少责任或处理计划: "+id, ref, nil)
		}
		if mode == "formal" {
			if row["decision"] == nil {
				issue("DEFERRED_DECISION_REQUIRED", "延期缺少既有决定绑定: "+id, ref, nil)
			} else {
				binding(row["decision"])
			}
		}
		deferred[id] = true
	}
	uncovered := []string{}
	for _, id := range order {
		if !covered[id] && !deferred[id] {
			uncovered = append(uncovered, id)
			issue("COVERAGE_MISSING", "未覆盖 "+id, specRef, id)
		}
	}
	report["uncovered"] = uncovered
	if mode == "formal" {
		if set["status"] != "ready-for-human" {
			issue("SET_DRAFT", "业务集合尚未正式化", ref, nil)
		}
		reviewRef = kernel.First(reviewRef, kernel.Text(kernel.SemMap(set["review"])["ref"]), kernel.Text(set["review_ref"]))
		if review, valid := doc(reviewRef); valid {
			validate(review, reviewRef)
			if kernel.ContractN(review["schema_version"]) != 1 || review["kind"] != "business-ticket-review" || review["result"] != "passed" || strings.TrimSpace(kernel.Text(review["reviewer"])) == "" || strings.TrimSpace(kernel.Text(review["drafter"])) == "" || ContractSame(review["reviewer"], review["drafter"]) || review["subject_ref"] != ref || review["subject_digest"] != observed[ref] || len(kernel.SemList(review["evidence"])) == 0 {
				issue("REVIEW_REQUIRED", "需要独立且绑定当前集合的专业审查", reviewRef, nil)
			}
			// Keep the existing approval-evidence boundary while collecting business bindings.
			problem(s.Basis(review["evidence"]), "", reviewRef, nil)
			for _, evidence := range kernel.SemList(review["evidence"]) {
				binding(evidence)
			}
			report["semantic_review"] = map[string]any{"status": "recorded", "ref": reviewRef, "result": review["result"], "subject_digest": review["subject_digest"]}
			report["unassessed"] = []string{"approval-validity", "semantic-review-truth"}
		}
	}
	out = ContractCopy(set)
	out["ticket_documents"], out["tickets"] = docs, reportTickets
	return
}

func contractBusinessCheckpoint(s *kernel.SemanticSession, cp map[string]any, required bool) error {
	identity, e := s.Doc("yss-project.yaml")
	if e != nil {
		return e
	}
	if kernel.Text(identity["repository_mode"]) == "template-source" {
		return nil
	}
	version, e := contractBusinessVersion(s)
	if e != nil {
		return e
	}
	if version != 1 {
		return nil
	}
	profile, e := s.Doc(".template-spec/process/harness-profile.yaml")
	if e != nil {
		return e
	}
	if kernel.SemHas([]string{"harness.backend-delivery", "harness.frontend-delivery"}, kernel.Text(profile["profile_id"])) {
		return nil
	}
	stage, next := kernel.Text(cp["stage"]), kernel.Text(cp["next_work_unit"])
	artifact := kernel.SemMap(kernel.SemMap(cp["artifacts"])["artifact.business-ticket-set"])
	formal := kernel.SemHas([]string{"work-unit.technical-analysis", "work-unit.strategic-design-handoff", "work-unit.implementation-repository-preparation", "work-unit.ticket-decomposition", "work-unit.slice-implementation"}, next) || kernel.SemHas([]string{"stage.system-data-engineering", "stage.vertical-slice-implementation", "stage.verification-release-retrospective"}, stage) || stage == "stage.ticket-formalization" && next != "work-unit.business-ticket-formalization"
	past := kernel.SemHas([]string{"stage.product-design", "stage.system-data-engineering", "stage.ticket-formalization", "stage.vertical-slice-implementation", "stage.verification-release-retrospective"}, stage)
	completed := kernel.SemHas([]string{"work-unit.prototype-design", "work-unit.prototype-design-v2", "work-unit.business-ticket-formalization"}, next) || kernel.Text(kernel.SemMap(cp["stage_trace"])["completed_work_unit"]) == "work-unit.spec-synthesis" || stage == "stage.spec-architecture" && kernel.SemHas([]string{"completed", "paused-human-gate"}, kernel.Text(cp["status"]))
	ref := kernel.First(kernel.Text(artifact["ref"]), kernel.Text(cp["business_ticket_set_ref"]))
	if !required && !formal && !past && !completed && ref == "" {
		return nil
	}
	if ref == "" {
		return s.Reject("BUSINESS_CHECKPOINT_BLOCKED", "缺少业务票集合")
	}
	var set map[string]any
	if formal || required {
		set, e = contractBusinessTickets(s, ref)
	} else {
		set, e = ContractBusinessTicketsMode(s, ref, "draft")
	}
	if e != nil {
		return e
	}
	_ = set
	b, e := s.Bind(ref)
	if e != nil {
		return e
	}
	if kernel.Text(artifact["digest"]) != "" && kernel.Text(artifact["digest"]) != b.Digest {
		return s.Reject("BUSINESS_SYNC_STALE", "checkpoint 业务票摘要过期")
	}
	for _, name := range []string{"map.md", "parent-ticket.md"} {
		target := path.Join(path.Dir(ref), name)
		exists, e := s.Exists(target)
		if e != nil {
			return e
		}
		if !exists {
			continue
		}
		doc, e := s.Doc(target)
		if e != nil {
			return e
		}
		if kernel.Text(doc["business_ticket_set_ref"]) != "" && (kernel.Text(doc["business_ticket_set_ref"]) != ref || kernel.Text(doc["business_ticket_set_digest"]) != b.Digest) {
			return s.Reject("BUSINESS_SYNC_STALE", "业务票阅读视图同步过期")
		}
	}
	return nil
}

func verifyRepositoryReadySemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	doc, e := s.Doc(ref)
	if e != nil {
		return e
	}
	prep := kernel.SemMap(doc["implementation_repository_preparation"])
	if len(prep) == 0 {
		prep = doc
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/implementation-repository-preparation-result.schema.json", prep); e != nil {
		return e
	}
	if kernel.ContractN(prep["schema_version"]) != 2 || kernel.Text(prep["result"]) != "completed" || prep["current_version"] != true || doc["implementation_repositories_stale"] == true {
		return s.Reject("REPOSITORY_NOT_READY", "多项目准备不是当前 completed")
	}
	if e = ContractEvidence(s, prep["evidence_refs"]); e != nil {
		return e
	}
	impacts := kernel.SemMap(doc["delivery_impacts"])
	for _, role := range []string{"backend", "frontend"} {
		found := false
		for _, v := range kernel.SemList(prep["projects"]) {
			p := kernel.SemMap(v)
			if kernel.Text(p["delivery_role"]) != role {
				continue
			}
			found = true
			if kernel.Text(p["status"]) == "not-applicable" {
				if impacts[role] == true || kernel.Text(p["reason"]) == "" {
					return s.Reject("REPOSITORY_NOT_READY", "不适用与影响冲突")
				}
				continue
			}
			if e = ContractRequired(s, p, "project_id", "repository_ref", "project_root", "repository_scope"); e != nil {
				return e
			}
			if role == "backend" {
				if e = contractBackendPrerequisites(s, kernel.SemMap(p["design_prerequisites"]), nil, opts); e != nil {
					return e
				}
			}
			switch kernel.Text(p["status"]) {
			case "existing-and-onboarded":
				r := kernel.SemMap(p["onboarding_result"])
				if kernel.Text(r["status"]) != "completed" {
					return s.Reject("REPOSITORY_NOT_READY", "接管未完成")
				}
				if _, e = s.Bytes(kernel.Text(r["ref"])); e != nil {
					return e
				}
			case "initialized-and-verified":
				c := kernel.SemMap(p["scaffold_contract"])
				if kernel.Text(c["status"]) != "approved" || c["persisted"] != true || c["current_version"] != true || (kernel.ContractN(c["schema_version"]) != 4 && kernel.ContractN(c["schema_version"]) != 5) {
					return s.Reject("SCAFFOLD_CONTRACT", "非当前批准的 v4 脚手架合同")
				}
				manifest, e := s.Doc(kernel.Text(p["scaffold_manifest_ref"]))
				if e != nil {
					return e
				}
				if role == "backend" && (kernel.ContractN(manifest["schema_version"]) != kernel.ContractN(c["schema_version"]) || kernel.Text(manifest["architecture_profile"]) == "mvc-data-analysis-v1" || kernel.Text(manifest["completion_level"]) != "empty-scaffold-verified" || !ContractSame(manifest["design_prerequisites"], p["design_prerequisites"])) {
					return s.Reject("SCAFFOLD_MANIFEST", "生成资产与准备依据冲突")
				}
				verification := kernel.SemMap(p["scaffold_verification"])
				if kernel.Text(verification["status"]) != "passed" {
					return s.Reject("SCAFFOLD_VERIFICATION", "脚手架验证未通过")
				}
				if e = s.Verify("verification", kernel.Text(verification["ref"]), opts); e != nil {
					return e
				}
				if role == "frontend" && p["template_available"] != true {
					return s.Reject("SCAFFOLD_TEMPLATE", "前端模板未验证")
				}
			default:
				return s.Reject("REPOSITORY_NOT_READY", "不支持的准备状态")
			}
		}
		if !found {
			return s.Reject("REPOSITORY_NOT_READY", role+" 缺少工程或不适用决定")
		}
	}
	return nil
}

// 公开入口 contract verify --kind scaffold-decisions [--allow-template]：脚手架架构选择记录本身的校验
// （Node 入口 scripts/verify-scaffold-architecture-decisions 的唯一实现）。模板记录只跳过「必须是正式记录」与用户决定核验。
func verifyScaffoldDecisionsSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	record, e := s.Doc(ref)
	if e != nil {
		return e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/scaffold-architecture-decisions.schema.json", record); e != nil {
		return e
	}
	template := opts["allow-template"] == "true"
	if !template && record["template"] != false {
		return s.Reject("SCAFFOLD_DECISION", "正式决策记录必须设置 template: false")
	}
	catalog, e := contractPlatformCatalog(s)
	if e != nil {
		return e
	}
	registry, e := s.Doc(".template-spec/agents/yss-skill-registry.yaml")
	if e != nil {
		return e
	}
	ids, projects := map[string]bool{}, map[string]map[string]any{}
	decisions := kernel.SemList(record["decisions"])
	reject := func(message string) error { return s.Reject("SCAFFOLD_DECISION", message) }
	for _, v := range decisions {
		decision := kernel.SemMap(v)
		id, project := kernel.Text(decision["decision_id"]), kernel.Text(decision["project_id"])
		if ids[id] || projects[project] != nil {
			return reject("decision_id 与 project_id 必须唯一")
		}
		ids[id], projects[project] = true, decision
		configuration := kernel.SemMap(decision["platform_configuration"])
		if len(configuration) > 0 {
			var matches []map[string]any
			for _, p := range kernel.SemList(catalog["profiles"]) {
				profile := kernel.SemMap(p)
				if profile["id"] == decision["platform_profile"] && (configuration["spring_boot_version"] == nil || profile["spring_boot_version"] == configuration["spring_boot_version"]) {
					matches = append(matches, profile)
				}
			}
			if len(matches) != 1 {
				return reject("平台选择未知或不唯一: " + kernel.Text(decision["platform_profile"]))
			}
			if configuration["profile_id"] != matches[0]["id"] || configuration["spring_boot_version"] != matches[0]["spring_boot_version"] || configuration["java_version"] != matches[0]["java_version"] {
				return reject("platform_configuration 与平台选择不一致")
			}
		} else if decision["platform_profile"] != "spring-boot-2.7-jdk8" {
			return reject("新平台缺少 platform_configuration")
		}
		status := kernel.Text(decision["status"])
		confirmed := kernel.SemHas([]string{"user-confirmed", "lifecycle-approved", "consumed"}, status)
		if confirmed {
			complete := kernel.Text(decision["confirmed_architecture"]) != ""
			for _, item := range kernel.SemMap(decision["user_confirmation"]) {
				if item == nil || kernel.Text(item) == "" {
					complete = false
				}
			}
			if !complete {
				return reject(project + " 已确认状态必须提供完整用户确认引用")
			}
		}
		if !confirmed && status != "stale" && decision["confirmed_architecture"] != nil {
			return reject(project + " 未确认状态不得预填 confirmed_architecture")
		}
		if decision["confirmed_architecture"] != nil && kernel.Text(decision["confirmed_architecture"]) != "" && decision["confirmed_architecture"] != decision["recommended_architecture"] && kernel.Text(decision["override_reason"]) == "" {
			return reject(project + " 覆盖 Agent 推荐时必须说明 override_reason")
		}
		mode := kernel.Text(decision["inheritance_mode"])
		if mode == "root-default" && (decision["parent_project_id"] != nil || decision["inherited_from"] != nil) {
			return reject(project + " 根默认不得声明父项目")
		}
		if (mode == "inherit" || mode == "override") && (kernel.Text(decision["parent_project_id"]) == "" || decision["inherited_from"] != decision["parent_project_id"]) {
			return reject(project + " 子项目继承引用不完整")
		}
		if confirmed && record["template"] == false {
			if e = ContractScaffoldUserDecision(s, decision); e != nil {
				return e
			}
		}
		identity := map[string]any{}
		for key, value := range decision {
			identity[key] = value
		}
		identity["architecture_family"] = decision["confirmed_architecture"]
		if kernel.Text(decision["confirmed_architecture"]) == "" {
			identity["architecture_family"] = decision["recommended_architecture"]
		}
		identity["generator_skill"] = kernel.SemMap(kernel.SemMap(registry["architecture_profiles"])[kernel.Text(decision["architecture_profile"])])["generator_skill"]
		identity["contract_digest"] = decision["decision_inputs_digest"]
		if e = contractArchitecture(s, identity, nil, registry, opts); e != nil {
			return e
		}
	}
	for _, v := range decisions {
		decision := kernel.SemMap(v)
		if decision["parent_project_id"] == nil {
			continue
		}
		parent := projects[kernel.Text(decision["parent_project_id"])]
		project := kernel.Text(decision["project_id"])
		if parent == nil {
			return reject(project + " 引用了不存在的父项目")
		}
		if decision["inheritance_mode"] == "inherit" && kernel.Text(decision["confirmed_architecture"]) != "" && kernel.Text(parent["confirmed_architecture"]) != "" && decision["confirmed_architecture"] != parent["confirmed_architecture"] {
			return reject(project + " 继承值与父项目不一致；需要改为 override")
		}
	}
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	s.Report.Coverage["scaffold_decisions"] = map[string]any{"template": template, "decisions": len(decisions)}
	return nil
}

func contractArchitecture(s *kernel.SemanticSession, identity map[string]any, c *NativeSlice, registry map[string]any, opts map[string]string) error {
	if kernel.ContractN(identity["schema_version"]) == 2 {
		return contractExistingArchitecture(s, identity, c, registry, opts)
	}
	if identity["schema_version"] != nil || identity["source_kind"] != nil {
		return s.Reject("ARCH_SOURCE_UNSUPPORTED", "未知架构身份来源")
	}
	profile := kernel.SemMap(kernel.SemMap(registry["architecture_profiles"])[kernel.Text(identity["architecture_profile"])])
	if kernel.Text(profile["maturity"]) == "deprecated" {
		return s.Reject("SCAFFOLD_RETIRED", "退役架构仅供历史审计，不能编译当前实现合同")
	}
	if len(profile) == 0 || profile["architecture_family"] != identity["architecture_family"] || profile["generator_skill"] != identity["generator_skill"] || kernel.Text(identity["verification_database"]) != "h2" || kernel.Text(identity["production_database"]) != "not-bound" || !ContractUnique(identity["requested_capabilities"], false) {
		return s.Reject("ARCH_IDENTITY_SHAPE", "脚手架架构/Profile/验证数据库不一致")
	}
	modules := kernel.SemStrings(profile["core_modules"])
	for _, capability := range kernel.SemStrings(identity["requested_capabilities"]) {
		add := kernel.SemMap(profile["capability_modules"])[capability]
		if add == nil {
			return s.Reject("ARCH_PROFILE_UNSUPPORTED", "未知架构能力")
		}
		modules = ContractUnion(modules, kernel.SemStrings(add))
	}
	if !kernel.SameStringSet(modules, identity["resolved_modules"]) || !kernel.IsSHA256(strings.TrimPrefix(kernel.Text(identity["contract_digest"]), "sha256:")) {
		return s.Reject("ARCH_IDENTITY_SHAPE", "模块闭包或合同摘要非法")
	}
	if c != nil {
		for _, key := range []string{"engineering_baseline", "repository_registration", "manifest"} {
			basis := c.Basis[key]
			if basis == nil {
				return s.Reject("ARCH_EVIDENCE_REF", "缺少架构证据: "+key)
			}
			doc, e := ContractBoundDoc(s, basis)
			if e != nil {
				return e
			}
			if !ContractSame(identity, doc["architecture_identity"]) {
				return s.Reject("ARCH_IDENTITY_CONFLICT", key+" 架构身份变化")
			}
		}
	}
	return nil
}

type contractPOM struct {
	Artifact              string
	Dependencies, Modules []string
}

func contractParsePOM(b []byte) (contractPOM, error) {
	p := contractPOM{}
	decoder := xml.NewDecoder(strings.NewReader(string(b)))
	stack := []string{}
	for {
		token, e := decoder.Token()
		if e != nil {
			if e.Error() == "EOF" {
				break
			}
			return p, e
		}
		switch t := token.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			value := strings.TrimSpace(string(t))
			if value == "" {
				continue
			}
			trace := strings.Join(stack, "/")
			if trace == "project/artifactId" {
				p.Artifact += value
			}
			if trace == "project/dependencies/dependency/artifactId" || trace == "project/profiles/profile/dependencies/dependency/artifactId" {
				p.Dependencies = append(p.Dependencies, value)
			}
			if strings.HasSuffix(trace, "/modules/module") {
				p.Modules = append(p.Modules, value)
			}
		}
	}
	return p, nil
}

func contractExistingArchitecture(s *kernel.SemanticSession, identity map[string]any, c *NativeSlice, registry map[string]any, opts map[string]string) error {
	if c == nil {
		return s.Reject("ARCH_EVIDENCE_REF", "既有架构须由冻结依据消费")
	}
	if kernel.Text(identity["source_kind"]) != "existing-registration" {
		return s.Reject("ARCH_SOURCE_UNSUPPORTED", "未知既有架构来源")
	}
	fields := []string{"schema_version", "source_kind", "architecture_family", "architecture_profile", "repository_id", "project_id", "source_digest", "build_units_digest"}
	if e := ContractRequired(s, identity, fields...); e != nil {
		return e
	}
	for key := range identity {
		if !kernel.SemHas(append(fields, "platform_configuration"), key) {
			return s.Reject("ARCH_IDENTITY_SHAPE", "既有身份混入生成事实")
		}
	}
	profile := kernel.SemMap(kernel.SemMap(registry["existing_project_profiles"])[kernel.Text(identity["architecture_profile"])])
	if kernel.Text(profile["source_kind"]) != "existing-registration" || kernel.Text(profile["build_system"]) != "maven" || profile["architecture_family"] != identity["architecture_family"] {
		return s.Reject("ARCH_PROFILE_UNSUPPORTED", "既有工程 Profile 不支持")
	}
	if e := contractNeedBasis(s, c.Basis, "engineering_baseline", "repository_registration", "manifest"); e != nil {
		return e
	}
	base, e := ContractBoundDoc(s, c.Basis["engineering_baseline"])
	if e != nil {
		return e
	}
	reg, e := ContractBoundDoc(s, c.Basis["repository_registration"])
	if e != nil {
		return e
	}
	manifest, e := ContractBoundDoc(s, c.Basis["manifest"])
	if e != nil {
		return e
	}
	for key, doc := range map[string]map[string]any{"baseline": base, "registration": reg, "manifest": manifest} {
		if !ContractSame(identity, doc["architecture_identity"]) || doc["repository_id"] != identity["repository_id"] || doc["project_id"] != identity["project_id"] {
			return s.Reject("ARCH_IDENTITY_CONFLICT", key+" 三方身份冲突")
		}
	}
	if kernel.Text(base["kind"]) != "existing-engineering-baseline" || kernel.ContractN(base["schema_version"]) != 1 || kernel.Text(manifest["kind"]) != "existing-project-observation" || kernel.ContractN(manifest["schema_version"]) != 1 || kernel.Text(base["status"]) != "current" || kernel.Text(reg["status"]) != "current" {
		return s.Reject("ARCH_EVIDENCE_STALE", "工程基线/观测来源或状态非法")
	}
	if e = ContractRequired(s, reg, "local_worktree", "project_root", "owner", "repository_url"); e != nil {
		return e
	}
	for _, key := range []string{"engineering_baseline", "manifest"} {
		registered := kernel.SemMap(kernel.SemMap(reg["architecture_evidence"])[key])
		if registered["ref"] != c.Basis[key]["ref"] || registered["digest"] != c.Basis[key]["digest"] {
			return s.Reject("ARCH_EVIDENCE_CONFLICT", "登记未绑定当前 "+key)
		}
	}
	repoRoot, root := kernel.Text(reg["local_worktree"]), filepath.Join(kernel.Text(reg["local_worktree"]), kernel.Text(reg["project_root"]))
	if e = s.RegisterExternalRoot(repoRoot, kernel.Text(c.Basis["repository_registration"]["ref"])); e != nil {
		return e
	}
	if root != repoRoot {
		if e = s.RegisterExternalRoot(root, kernel.Text(c.Basis["repository_registration"]["ref"])); e != nil {
			return e
		}
	}
	source := kernel.SemMap(manifest["source"])
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(kernel.Text(source["base_commit"])) || !ContractUnique(source["roots"], true) || len(kernel.SemList(source["files"])) == 0 || !ContractSame(source, base["source"]) || ContractDigest(source) != kernel.Text(identity["source_digest"]) {
		return s.Reject("ARCH_SOURCE_MISSING", "固定源码范围或摘要不一致")
	}
	queries := [][]string{{"rev-parse", "--show-toplevel"}, {"rev-parse", kernel.Text(source["base_commit"]) + "^{commit}"}, {"config", "--local", "--get", "remote.origin.url"}, {"rev-parse", "HEAD"}}
	expect := []string{repoRoot, kernel.Text(source["base_commit"]), kernel.Text(reg["repository_url"]), kernel.Text(source["base_commit"])}
	for i, q := range queries {
		out, e := s.Git(repoRoot, q...)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(out)) != expect[i] {
			if i != 3 {
				return s.Reject("ARCH_SOURCE_STALE", "Git 根/origin/提交与登记冲突")
			}
			allowed, err := CandidateIntentAllowlist(s, repoRoot, c.Ref)
			if err != nil {
				return err
			}
			differences, err := s.Git(repoRoot, "diff", "--no-renames", "--name-only", "-z", kernel.Text(source["base_commit"]), "HEAD")
			if err != nil {
				return err
			}
			if err = candidateOnlyIntentPaths(s, differences, allowed, "ARCH_SOURCE_STALE"); err != nil {
				return err
			}
		}
	}
	treeBytes, e := s.Git(repoRoot, "ls-tree", "-r", "-z", kernel.Text(source["base_commit"]))
	if e != nil {
		return e
	}
	tree := map[string]string{}
	prefix := kernel.Text(reg["project_root"])
	if prefix == "." {
		prefix = ""
	} else {
		prefix += "/"
	}
	for _, row := range strings.Split(string(treeBytes), "\x00") {
		parts := strings.SplitN(row, "\t", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], prefix) {
			continue
		}
		ref := strings.TrimPrefix(parts[1], prefix)
		skip := false
		for _, part := range strings.Split(ref, "/") {
			skip = skip || kernel.SemHas([]string{".git", "target", "node_modules"}, part)
		}
		if skip {
			continue
		}
		beneath := false
		for _, scope := range kernel.SemStrings(source["roots"]) {
			beneath = beneath || ContractWithin(ref, scope)
		}
		if !beneath {
			continue
		}
		meta := strings.Fields(parts[0])
		if len(meta) != 3 || !kernel.SemHas([]string{"100644", "100755"}, meta[0]) {
			return s.Reject("ARCH_SOURCE_INVALID", "源码范围有 gitlink/symlink")
		}
		tree[ref] = meta[2]
	}
	intentAllowed, err := CandidateIntentAllowlist(s, repoRoot, c.Ref)
	if err != nil {
		return err
	}
	seen, changes := map[string]bool{}, []string{}
	for _, v := range kernel.SemList(source["files"]) {
		f := kernel.SemMap(v)
		ref := kernel.Text(f["path"])
		if !kernel.ContractPath(ref) || seen[ref] || kernel.Text(f["base_blob"]) != tree[ref] || !strings.HasPrefix(kernel.Text(f["sha256"]), "sha256:") {
			return s.Reject("ARCH_SOURCE_INVALID", "固定源码路径/blob 非法")
		}
		seen[ref] = true
		if intentAllowed[prefix+ref] {
			continue
		} // Original frozen blob/identity stays intact; intent current bytes are independently verified.
		b, e := s.ExternalBytes(root, ref)
		if e != nil {
			view := s.ExternalViews[root]
			d, x := view.Watch(ref)
			if x != nil || d.Type != "missing" {
				return e
			}
			changes = append(changes, ref)
			continue
		}
		digest := "sha256:" + safefs.Digest(b)
		if digest != kernel.Text(f["sha256"]) {
			changes = append(changes, ref)
			continue
		}
		h := sha1.New()
		fmt.Fprintf(h, "blob %d\x00", len(b))
		_, _ = h.Write(b)
		blob := hex.EncodeToString(h.Sum(nil))
		if blob != tree[ref] {
			found := false
			for _, p := range kernel.SemList(source["patches"]) {
				patch := kernel.SemMap(p)
				found = found || kernel.Text(patch["path"]) == ref && kernel.Text(patch["base_blob"]) == tree[ref] && kernel.Text(patch["sha256"]) == digest
			}
			if !found {
				return s.Reject("ARCH_SOURCE_UNDECLARED", "未登记基线补丁")
			}
		}
	}
	if len(seen) != len(tree) {
		return s.Reject("ARCH_SOURCE_INCOMPLETE", "冻结源码清单不完整")
	}
	external := kernel.NewSemanticSession(s.Ctx, root, s.Args)
	external.V = s.ExternalViews[root]
	s.Children = append(s.Children, external)
	for _, scope := range kernel.SemStrings(source["roots"]) {
		files, e := external.Scan(scope)
		if e != nil {
			return e
		}
		for _, ref := range files {
			skip := false
			for _, part := range strings.Split(ref, "/") {
				skip = skip || kernel.SemHas([]string{".git", "target", "node_modules"}, part)
			}
			if !skip && !seen[ref] && !intentAllowed[prefix+ref] {
				changes = append(changes, ref)
			}
		}
	}
	units := kernel.SemList(manifest["build_units"])
	if len(units) == 0 || !ContractSame(units, base["build_units"]) || ContractDigest(units) != kernel.Text(identity["build_units_digest"]) {
		return s.Reject("ARCH_BUILD_STALE", "构建单元摘要变化")
	}
	unitByPom, artifactMap, poms := map[string]map[string]any{}, map[string]string{}, map[string]contractPOM{}
	for _, v := range units {
		u := kernel.SemMap(v)
		id, pom := kernel.Text(u["id"]), kernel.Text(u["pom_ref"])
		if id == "" || unitByPom[pom] != nil || !seen[pom] {
			return s.Reject("ARCH_BUILD_CONFLICT", "构建单元重复或 POM 未冻结")
		}
		b, e := s.ExternalBytes(root, pom)
		if e != nil {
			return e
		}
		p, e := contractParsePOM(b)
		if e != nil {
			return s.Reject("ARCH_BUILD_INVALID", e.Error())
		}
		if kernel.Text(u["artifact_id"]) != p.Artifact || artifactMap[p.Artifact] != "" {
			return s.Reject("ARCH_BUILD_CONFLICT", "artifactId 冲突")
		}
		artifactMap[p.Artifact] = id
		unitByPom[pom], poms[id] = u, p
	}
	for _, v := range units {
		u := kernel.SemMap(v)
		p := poms[kernel.Text(u["id"])]
		for _, module := range p.Modules {
			child := path.Join(path.Dir(kernel.Text(u["pom_ref"])), module, "pom.xml")
			if strings.Contains(module, "${") || !kernel.ContractPath(child) || unitByPom[child] == nil {
				return s.Reject("ARCH_BUILD_INCOMPLETE", "实际 reactor 未登记")
			}
		}
		deps := []string{}
		for _, artifact := range p.Dependencies {
			if artifactMap[artifact] != "" {
				deps = ContractUnion(deps, []string{artifactMap[artifact]})
			}
		}
		if !ContractSame(ContractSorted(deps), ContractSorted(kernel.SemStrings(u["depends_on"]))) {
			return s.Reject("ARCH_BUILD_CONFLICT", "实际构建依赖变化")
		}
		for _, role := range kernel.SemStrings(u["roles"]) {
			refs := kernel.SemStrings(kernel.SemMap(u["role_paths"])[role])
			if len(refs) == 0 {
				return s.Reject("ARCH_BOUNDARY_MISSING", "责任源码映射缺失")
			}
			for _, ref := range refs {
				frozen, boundary := false, false
				for p := range seen {
					frozen = frozen || ContractWithin(p, ref)
				}
				for _, scope := range kernel.SemStrings(base["boundary_scope"]) {
					boundary = boundary || ContractWithin(ref, scope)
				}
				if !kernel.ContractPath(ref) || !frozen || !boundary {
					return s.Reject("ARCH_BOUNDARY_CONFLICT", "责任源码越界")
				}
			}
		}
	}
	for _, role := range kernel.SemStrings(profile["required_roles"]) {
		found := false
		for _, v := range units {
			found = found || kernel.SemHas(kernel.SemMap(v)["roles"], role)
		}
		if !found {
			return s.Reject("ARCH_BOUNDARY_MISSING", "架构责任未覆盖")
		}
	}
	if !ContractUnique(reg["allowed_write_paths"], true) || !ContractUnique(base["boundary_scope"], true) || !ContractUnique(base["verification_commands"], true) || !ContractSame(base["verification_commands"], reg["verification_commands"]) {
		return s.Reject("ARCH_REGISTRATION_MISSING", "写范围/业务边界/验证入口无效")
	}
	review, e := ContractBoundDoc(s, base["boundary_review"])
	if e != nil {
		return e
	}
	gate := kernel.Text(review["gate_id"])
	if !kernel.SemHas([]string{"check.architecture-reviewed", "gate.technical-design-approved"}, gate) {
		return s.Reject("ARCH_REVIEW_MISSING", "边界审查门禁无效")
	}
	if e = ContractApproval(s, gate, c.Basis["manifest"], kernel.Text(kernel.SemMap(base["boundary_review"])["ref"]), opts); e != nil {
		return e
	}
	found := false
	for _, v := range kernel.SemList(review["artifact_bindings"]) {
		b := kernel.SemMap(v)
		found = found || b["id"] == base["id"] && b["version"] == base["version"] && kernel.Text(b["digest"]) == ContractDigest(ContractWithout(base, "boundary_review"))
	}
	if !found || kernel.Text(base["author"]) == "" || kernel.Text(review["principal_ref"]) == kernel.Text(base["author"]) {
		return s.Reject("ARCH_REVIEW_STALE", "边界审查未独立绑定基线")
	}
	if e = ContractEvidence(s, review["evidence_refs"]); e != nil {
		return e
	}
	databases := kernel.SemMap(base["databases"])
	if databases["verification"] == nil || databases["production"] == nil {
		return s.Reject("ARCH_DATABASE_MISSING", "验证/生产数据库须分别声明")
	}
	for _, v := range databases {
		db := kernel.SemMap(v)
		if !kernel.SemHas([]string{"declared", "verified", "unknown", "not-applicable"}, kernel.Text(db["status"])) {
			return s.Reject("ARCH_DATABASE_INVALID", "数据库状态非法")
		}
		if kernel.Text(db["status"]) == "verified" {
			report, e := ContractBoundDoc(s, db["verification"])
			if e != nil {
				return e
			}
			for _, k := range []string{"engine", "version", "environment_id"} {
				if kernel.Text(db[k]) == "" || !ContractSame(db[k], report[k]) {
					return s.Reject("ARCH_DATABASE_STALE", "数据库环境/版本不一致")
				}
			}
			if report["source_digest"] != identity["source_digest"] {
				return s.Reject("ARCH_DATABASE_STALE", "数据库验证源码变化")
			}
			if e = ContractRecordedResults(s, report["results"], true); e != nil {
				return e
			}
		}
	}
	for _, ref := range changes {
		allowed, registered := false, false
		for _, p := range kernel.SemStrings(kernel.SemMap(c.Normalized["common"])["allowed_write_paths"]) {
			if filepath.IsAbs(p) {
				p, _ = filepath.Rel(root, p)
			}
			allowed = allowed || ContractWithin(ref, p)
		}
		for _, p := range kernel.SemStrings(reg["allowed_write_paths"]) {
			registered = registered || ContractWithin(ref, p)
		}
		if !allowed || !registered {
			return s.Reject("ARCH_SOURCE_OUT_OF_SCOPE", "当前源码增量超出已批准范围")
		}
	}
	return nil
}

func ContractSorted(a []string) []string { x := append([]string(nil), a...); sort.Strings(x); return x }

func ContractRecordedResults(s *kernel.SemanticSession, v any, bound bool) error {
	rows := kernel.SemList(v)
	if len(rows) == 0 {
		return s.Reject("VERIFICATION_REQUIRED", "缺少真实验证记录")
	}
	for _, v := range rows {
		r := kernel.SemMap(v)
		exit, ok := kernel.Integer(r["exit_code"])
		if kernel.Text(r["command"]) == "" || !ok || exit != 0 || !ContractDate(r["executed_at"]) {
			return s.Reject("VERIFICATION_FAILED", "实际命令、退出码或时间非法")
		}
		if bound {
			if e := s.Basis(r["evidence"]); e != nil {
				return e
			}
		}
	}
	return nil
}

func contractComponents(s *kernel.SemanticSession, resolution, registry map[string]any) error {
	frozen := kernel.SemList(resolution["component_bindings"])
	required := []string{}
	skills := kernel.SemStrings(resolution["required_skills"])
	for _, value := range kernel.SemList(resolution["non_expanding_dependencies"]) {
		dependency := kernel.SemMap(value)
		if kernel.Text(dependency["type"]) == "component-dependency" {
			skills = append(skills, kernel.Text(dependency["skill"]))
		}
	}
	for _, v := range kernel.SemList(registry["capabilities"]) {
		c := kernel.SemMap(v)
		provider := kernel.SemMap(c["provider"])
		id := kernel.Text(c["id"])
		if kernel.Text(provider["kind"]) == "yss-component" && (kernel.SemHas(resolution["required_capabilities"], id) || kernel.SemHas(skills, kernel.Text(c["primary_skill"]))) {
			bindingID := kernel.Text(provider["binding_id"])
			if bindingID == "" {
				return s.Reject("COMPONENT_CAPABILITY_INVALID", "组件 provider 缺少 binding_id")
			}
			if !kernel.SemHas(required, bindingID) {
				required = append(required, bindingID)
			}
		}
	}
	if len(required) == 0 && len(frozen) == 0 {
		return nil
	}
	if len(frozen) == 0 {
		return s.Reject("COMPONENT_CAPABILITY_MISSING", "冻结能力缺少平台证据")
	}
	catalog, e := contractPlatformCatalog(s)
	if e != nil {
		return e
	}
	if kernel.ContractN(catalog["schema_version"]) != 2 {
		return s.Unavailable("CAPABILITY", "未知后端平台目录协议")
	}
	seen := map[string]bool{}
	contexts := map[string]bool{}
	coordinates := map[string]map[string]string{}
	currentBindings := []any{}
	for _, v := range frozen {
		binding := kernel.SemMap(v)
		id := kernel.Text(binding["capability_id"])
		contextKey := ContractDigest(map[string]any{"platform_configuration": binding["platform_configuration"], "architecture_family": binding["architecture_family"]})
		if contexts[contextKey+id] || id == "" || !kernel.SemHas([]string{"domain-driven", "layered-mvc"}, kernel.Text(binding["architecture_family"])) {
			return s.Reject("COMPONENT_CAPABILITY_INVALID", "组件绑定缺失或重复")
		}
		seen[id] = true
		contexts[contextKey+id] = true
		platform := kernel.SemMap(binding["platform_configuration"])
		profile, compat, _, e := contractPlatform(s, platform, true)
		if e != nil {
			return e
		}
		_ = catalog
		item := kernel.SemMap(kernel.SemMap(compat["component_capabilities"])[id])
		if len(item) == 0 {
			return s.Reject("COMPONENT_CAPABILITY_MISSING", "组件不在平台闭包")
		}
		if kernel.Text(item["status"]) != "verified" || kernel.Text(compat["status"]) != "verified" || !kernel.SemHas(item["verified_architectures"], kernel.Text(binding["architecture_family"])) || len(kernel.SemList(item["blockers"])) > 0 {
			return s.Reject("COMPONENT_CAPABILITY_UNVERIFIED", "组件/平台/架构组合未验证")
		}
		if kernel.Text(item["adoption_policy"]) == "forbidden" {
			return s.Reject("COMPONENT_NEW_ADOPTION_FORBIDDEN", "组件已禁止新采纳")
		}
		if coordinates[contextKey] == nil {
			coordinates[contextKey] = map[string]string{}
		}
		for _, value := range kernel.SemList(item["artifacts"]) {
			artifact := kernel.SemMap(value)
			key := kernel.Text(artifact["group_id"]) + ":" + kernel.Text(artifact["artifact_id"])
			version := kernel.Text(artifact["declared_version"]) + "->" + kernel.Text(artifact["resolved_version"])
			if previous := coordinates[contextKey][key]; previous != "" && previous != version {
				return s.Reject("COMPONENT_ARTIFACT_COORDINATE_CONFLICT", "同平台组件坐标解析版本冲突: "+key)
			}
			coordinates[contextKey][key] = version
		}
		currentEvidence := []any{}
		for _, v := range kernel.SemList(item["evidence"]) {
			ev := kernel.SemMap(v)
			if ev["architecture_family"] == binding["architecture_family"] {
				currentEvidence = append(currentEvidence, ev)
			}
		}
		if len(currentEvidence) == 0 || kernel.Text(binding["component_digest"]) != kernel.Text(item["component_digest"]) || !ContractSame(binding["artifacts"], item["artifacts"]) || !ContractSame(binding["evidence"], currentEvidence) || !ContractSame(binding["verified_architectures"], item["verified_architectures"]) || kernel.Text(binding["status"]) != kernel.Text(item["status"]) {
			return s.Reject("COMPONENT_CAPABILITY_STALE", "组件冻结绑定变化")
		}
		currentBindings = append(currentBindings, map[string]any{"capability_id": id, "status": item["status"], "verified_architectures": item["verified_architectures"], "artifacts": item["artifacts"], "evidence": currentEvidence, "component_digest": item["component_digest"], "architecture_family": binding["architecture_family"], "platform_configuration": binding["platform_configuration"]})
		for _, v := range kernel.SemList(binding["evidence"]) {
			ev := kernel.SemMap(v)
			doc, e := ContractBoundDoc(s, ev)
			if e != nil {
				return e
			}
			if kernel.ContractN(doc["schema_version"]) != 1 || kernel.Text(doc["kind"]) != "backend-component-capability-evidence" || kernel.Text(doc["status"]) != "passed" || doc["capability_id"] != binding["capability_id"] || doc["component_digest"] != binding["component_digest"] || doc["architecture_family"] != binding["architecture_family"] || doc["compatibility_id"] != compat["id"] || doc["profile_id"] != profile["id"] || doc["spring_boot_version"] != profile["spring_boot_version"] || !ContractSame(doc["artifacts"], binding["artifacts"]) {
				return s.Reject("COMPONENT_CAPABILITY_STALE", "组件真实证据来源不一致")
			}
		}
	}
	for _, id := range required {
		if !seen[id] {
			return s.Reject("COMPONENT_CAPABILITY_MISSING", "所需组件未冻结")
		}
	}
	if len(frozen) > 0 && (!ContractSame(frozen, currentBindings) || kernel.Text(resolution["component_bindings_digest"]) != strings.TrimPrefix(ContractDigest(currentBindings), "sha256:")) {
		return s.Reject("COMPONENT_CAPABILITY_STALE", "冻结组件闭包摘要变化")
	}
	return nil
}

func ContractDesignTargets(s *kernel.SemanticSession, data map[string]any) (map[string]map[string]any, map[string]map[string]any, error) {
	design := data
	if kernel.ContractN(data["schema_version"]) == 2 {
		design = kernel.SemMap(data["design"])
	}
	ids, seams := map[string]map[string]any{}, map[string]map[string]any{}
	keys := map[string]string{"aggregate_catalog": "aggregate_id", "entity_catalog": "entity_id", "value_object_catalog": "value_object_id", "behavior_catalog": "behavior_id", "invariant_catalog": "invariant_id", "state_transition_catalog": "transition_id", "domain_event_catalog": "event_id", "gateway_catalog": "gateway_id", "module_catalog": "module_id", "use_case_catalog": "use_case_id", "rule_catalog": "rule_id", "integration_catalog": "integration_id", "component_catalog": "component_id"}
	for catalog, key := range keys {
		for _, v := range kernel.SemList(design[catalog]) {
			item := kernel.SemMap(v)
			id := kernel.Text(item[key])
			if id == "" {
				id = kernel.Text(item["id"])
			}
			if id == "" || ids[id] != nil {
				return nil, nil, s.Reject("TECHNICAL_DESIGN_INVALID", "设计 ID 缺失或重复")
			}
			ids[id] = item
		}
	}
	for _, v := range kernel.SemList(design["persistence_mapping"]) {
		item := kernel.SemMap(v)
		id := kernel.Text(item["mapping_id"])
		if id == "" {
			id = kernel.Text(item["id"])
		}
		if id != "" {
			if ids[id] != nil {
				return nil, nil, s.Reject("TECHNICAL_DESIGN_INVALID", "持久化设计 ID 重复")
			}
			ids[id] = item
		}
	}
	for _, v := range kernel.SemList(design["test_seams"]) {
		item := kernel.SemMap(v)
		id := kernel.First(kernel.Text(item["seam_id"]), kernel.Text(item["id"]))
		if id == "" || seams[id] != nil {
			return nil, nil, s.Reject("TECHNICAL_DESIGN_INVALID", "seam ID 缺失或重复")
		}
		seams[id] = item
	}
	return ids, seams, nil
}

func ContractTraceRow(s *kernel.SemanticSession, row, source map[string]any, ids, seams map[string]map[string]any, slice string) error {
	switch kernel.Text(row["disposition"]) {
	case "implemented":
		if !ContractUnique(row["tactical_refs"], true) || !ContractUnique(row["test_seam_refs"], true) {
			return s.Reject("TECHNICAL_TRACEABILITY", "设计落点或 seam 缺失")
		}
		for _, ref := range kernel.SemStrings(row["tactical_refs"]) {
			if ids[ref] == nil {
				return s.Reject("TECHNICAL_TRACEABILITY", "设计落点悬空")
			}
		}
		for _, ref := range kernel.SemStrings(row["test_seam_refs"]) {
			if seams[ref] == nil || !kernel.SemHas(row["tactical_refs"], kernel.Text(seams[ref]["subject_ref"])) {
				return s.Reject("TECHNICAL_TRACEABILITY", "seam 未关联设计落点")
			}
		}
		if (kernel.Text(source["kind"]) == "scenario" || strings.HasPrefix(kernel.Text(row["source_id"]), "scenario.")) && source["critical"] == true {
			for _, outcome := range []string{"success", "failure"} {
				found := false
				for _, v := range kernel.SemList(row["scenario_tests"]) {
					test := kernel.SemMap(v)
					found = found || kernel.Text(test["outcome"]) == outcome && kernel.SemHas(row["test_seam_refs"], kernel.Text(test["seam_ref"]))
				}
				if !found {
					return s.Reject("TECHNICAL_TRACEABILITY", "关键场景缺少成功/失败测试")
				}
			}
		}
		return ContractEvidence(s, row["evidence_refs"])
	case "not-applicable":
		if kernel.Text(row["reason"]) == "" {
			return s.Reject("TECHNICAL_TRACEABILITY", "不适用缺少理由")
		}
		return ContractEvidence(s, row["evidence_refs"])
	case "deferred":
		if e := ContractRequired(s, row, "reason", "risk", "owner", "followup_ticket_ref", "verification_plan", "target_version"); e != nil {
			return e
		}
		fallthrough
	case "pending", "conflict":
		if slice == "" || kernel.Text(row["dependency_status"]) != "known" || !ContractUnique(row["dependent_slice_refs"], true) || kernel.SemHas(row["dependent_slice_refs"], slice) {
			return s.Reject("TECHNICAL_TRACEABILITY", "未闭合承接阻断当前切片")
		}
		return nil
	default:
		return s.Reject("TECHNICAL_TRACEABILITY", "未知承接状态")
	}
}

func contractTechnicalDesign(s *kernel.SemanticSession, ref string, doc map[string]any, c *NativeSlice, opts map[string]string) error {
	if kernel.ContractN(doc["schema_version"]) != 2 {
		if kernel.ContractN(doc["schema_version"]) != 1 || opts["legacy-ddd"] != "true" {
			return s.Reject("TECHNICAL_LEGACY_REQUIRED", "历史Tactical v1须显式legacy-ddd只读适配")
		}
		if e := contractTactical(s, doc); e != nil {
			return e
		}
		if (c != nil || opts["slice"] != "") && kernel.Text(doc["status"]) != "approved" {
			return s.Reject("TECHNICAL_DESIGN_STALE", "Slice仅消费approved历史DDD")
		}
		if doc["strategic_handoff"] != nil || doc["strategic_context_import_ref"] != nil || kernel.SemMap(doc["upstream_impact"])["source_kind"] == "strategic-handoff" {
			return handoffdomain.ContractHandoffConsumption(s, doc, map[string]string{"consumer": "tactical", "slice": opts["slice"]})
		}
		s.Report.Checks = append(s.Report.Checks, kernel.SemanticCheck{ID: "legacy-ddd-read-only", SourceRef: ref, Status: "passed"})
		return nil
	}
	if e := s.ValidateSchema(".agents/skills/yss-technical-design/references/technical-design.schema.json", doc); e != nil {
		return e
	}
	if kernel.Text(doc["digest"]) != ContractDigest(ContractWithout(doc, "digest")) {
		return s.Reject("TECHNICAL_DESIGN_STALE", "技术设计摘要不一致")
	}
	if kernel.SemHas([]string{"stale", "blocked", "drift", "new_impacts"}, kernel.Text(doc["status"])) || c != nil && kernel.Text(doc["status"]) != "approved" {
		return s.Reject("TECHNICAL_DESIGN_STALE", "技术设计状态不能消费")
	}
	architecture := kernel.SemMap(doc["architecture"])
	binding := map[string]any{"ref": architecture["decision_ref"], "digest": architecture["decision_digest"]}
	record, e := ContractBoundDoc(s, binding)
	if e != nil {
		return e
	}
	if kernel.Text(architecture["source_kind"]) == "scaffold-decision" {
		decision := kernel.ApFind(record["decisions"], "project_id", kernel.Text(architecture["project_id"]))
		if record["template"] != false || kernel.Text(record["status"]) != "current" || !kernel.SemHas([]string{"user-confirmed", "lifecycle-approved", "consumed"}, kernel.Text(decision["status"])) || decision["confirmed_architecture"] != architecture["family"] {
			return s.Reject("TECHNICAL_ARCHITECTURE", "项目架构未正式确认")
		}
		if e = ContractScaffoldUserDecision(s, decision); e != nil {
			return e
		}
	} else {
		entry := kernel.ApFind(record["repositories"], "project_id", kernel.Text(architecture["project_id"]))
		if len(entry) == 0 {
			entry = record
		}
		if entry["project_id"] != architecture["project_id"] || kernel.SemMap(entry["architecture_identity"])["architecture_family"] != architecture["family"] || kernel.SemHas([]string{"stale", "blocked"}, kernel.Text(entry["status"])) {
			return s.Reject("TECHNICAL_ARCHITECTURE", "既有登记身份变化")
		}
		if kernel.ContractN(kernel.SemMap(entry["architecture_identity"])["schema_version"]) == 2 {
			registry, e := s.Doc(".template-spec/agents/yss-skill-registry.yaml")
			if e != nil {
				return e
			}
			architectureContext := c
			if architectureContext == nil {
				evidence := kernel.SemMap(entry["architecture_evidence"])
				basis := map[string]map[string]any{
					"repository_registration": {"ref": architecture["decision_ref"], "digest": architecture["decision_digest"]},
					"engineering_baseline":    kernel.SemMap(evidence["engineering_baseline"]),
					"manifest":                kernel.SemMap(evidence["manifest"]),
				}
				architectureContext = &NativeSlice{Raw: map[string]any{}, Normalized: map[string]any{"common": map[string]any{"allowed_write_paths": []any{}}}, Basis: basis}
			}
			if e = contractExistingArchitecture(s, kernel.SemMap(entry["architecture_identity"]), architectureContext, registry, opts); e != nil {
				return e
			}
		}
	}
	context, spec, strategic := false, false, false
	for _, v := range kernel.SemList(doc["inputs"]) {
		b := kernel.SemMap(v)
		if _, e = contractBinding(s, b); e != nil {
			return e
		}
		context = context || kernel.Text(b["kind"]) == "context" && kernel.Text(b["ref"]) == "CONTEXT.md"
		spec = spec || kernel.Text(b["kind"]) == "spec"
		strategic = strategic || kernel.Text(b["kind"]) == "strategic"
	}
	if !context || !spec {
		return s.Reject("TECHNICAL_DESIGN_INPUT", "缺少当前 Context/Spec")
	}
	if kernel.Text(doc["status"]) == "approved" {
		if e = ContractEvidence(s, doc["evidence_refs"]); e != nil {
			return e
		}
	}
	design := kernel.SemMap(doc["design"])
	if kernel.Text(doc["design_scope"]) == "engineering-only" {
		if e = contractEngineeringDesign(s, doc, record); e != nil {
			return e
		}
	} else if kernel.Text(architecture["family"]) == "layered-mvc" {
		if e = contractMVC(s, design); e != nil {
			return e
		}
	} else {
		if !strategic {
			return s.Reject("TECHNICAL_DESIGN_INPUT", "DDD 缺少战略输入")
		}
		if design["strategic_handoff"] != nil {
			return s.Reject("TECHNICAL_DESIGN_INVALID", "DDD 不得另存战略绑定")
		}
		if e = contractTactical(s, design); e != nil {
			return e
		}
	}
	ids, seams, e := ContractDesignTargets(s, doc)
	if e != nil {
		return e
	}
	for _, seam := range seams {
		if ids[kernel.Text(seam["subject_ref"])] == nil {
			return s.Reject("TECHNICAL_DESIGN_INVALID", "seam 主体悬空")
		}
	}
	sources, rows := map[string]map[string]any{}, map[string]map[string]any{}
	for _, v := range kernel.SemList(doc["source_items"]) {
		source := kernel.SemMap(v)
		id := kernel.Text(source["source_id"])
		if sources[id] != nil {
			return s.Reject("TECHNICAL_TRACEABILITY", "来源重复")
		}
		found := false
		for _, v := range kernel.SemList(doc["inputs"]) {
			found = found || kernel.SemMap(v)["ref"] == source["source_ref"]
		}
		if !found {
			return s.Reject("TECHNICAL_TRACEABILITY", "来源不在冻结输入")
		}
		sources[id] = source
	}
	if len(sources) == 0 && doc["strategic_handoff"] == nil {
		return s.Reject("TECHNICAL_TRACEABILITY", "无战略包须列本地规则/场景")
	}
	for _, v := range kernel.SemList(doc["traceability"]) {
		row := kernel.SemMap(v)
		id := kernel.Text(row["source_id"])
		if rows[id] != nil || sources[id] == nil {
			return s.Reject("TECHNICAL_TRACEABILITY", "承接重复或来源未知")
		}
		rows[id] = row
	}
	slice := opts["slice"]
	if c != nil {
		slice = kernel.Text(c.Raw["slice_id"])
	}
	for id, source := range sources {
		if rows[id] == nil {
			return s.Reject("TECHNICAL_TRACEABILITY", "缺少逐条承接")
		}
		if e = ContractTraceRow(s, rows[id], source, ids, seams, slice); e != nil {
			return e
		}
	}
	if doc["strategic_handoff"] != nil {
		return handoffdomain.ContractHandoffConsumption(s, doc, map[string]string{"consumer": "tactical", "slice": slice})
	}
	return nil
}

func contractEngineeringDesign(s *kernel.SemanticSession, data, registration map[string]any) error {
	identity := kernel.SemMap(registration["architecture_identity"])
	if kernel.ContractN(identity["schema_version"]) != 2 || kernel.Text(identity["source_kind"]) != "existing-registration" {
		return s.Reject("ENGINEERING_DESIGN_INVALID", "工程分支必须消费既有工程身份 v2")
	}
	binding := kernel.SemMap(kernel.SemMap(registration["architecture_evidence"])["engineering_baseline"])
	baseline, e := ContractBoundDoc(s, binding)
	if e != nil {
		return e
	}
	boundEngineering, boundAPI := false, false
	for _, v := range kernel.SemList(data["inputs"]) {
		b := kernel.SemMap(v)
		boundEngineering = boundEngineering || kernel.Text(b["kind"]) == "engineering" && b["ref"] == binding["ref"] && b["digest"] == binding["digest"]
		boundAPI = boundAPI || kernel.Text(b["kind"]) == "api"
	}
	if !boundEngineering || !boundAPI {
		return s.Reject("ENGINEERING_DESIGN_INVALID", "工程分支缺少固定基线/API输入")
	}
	d := kernel.SemMap(data["design"])
	components := map[string]map[string]any{}
	for _, v := range kernel.SemList(d["component_catalog"]) {
		c := kernel.SemMap(v)
		id := kernel.Text(c["component_id"])
		if components[id] != nil || id == "" {
			return s.Reject("ENGINEERING_DESIGN_INVALID", "组件ID缺失或重复")
		}
		components[id] = c
	}
	paths := []string{}
	for _, c := range components {
		unit := kernel.ApFind(baseline["build_units"], "id", kernel.Text(c["build_unit_ref"]))
		if unit == nil {
			return s.Reject("ENGINEERING_DESIGN_INVALID", "实际构建单元悬空")
		}
		for _, id := range kernel.SemStrings(c["depends_on"]) {
			if components[id] == nil {
				return s.Reject("ENGINEERING_DESIGN_INVALID", "工程组件依赖悬空")
			}
		}
		for _, ref := range kernel.SemStrings(c["write_paths"]) {
			if !kernel.ContractPath(ref) {
				return s.Reject("PATH", "工程组件路径非法")
			}
			allowed := false
			for _, scope := range kernel.SemStrings(registration["allowed_write_paths"]) {
				allowed = allowed || ContractWithin(ref, scope)
			}
			if !allowed {
				return s.Reject("ENGINEERING_DESIGN_INVALID", "工程写范围超出登记")
			}
			for _, scope := range kernel.SemStrings(baseline["boundary_scope"]) {
				if ContractWithin(ref, scope) || ContractWithin(scope, ref) {
					return s.Reject("ENGINEERING_DESIGN_INVALID", "工程分支触碰已登记业务边界")
				}
			}
			unitRoot := path.Dir(kernel.Text(unit["pom_ref"]))
			if unitRoot != "." && !ContractWithin(ref, unitRoot) {
				return s.Reject("ENGINEERING_DESIGN_INVALID", "工程写范围与构建单元冲突")
			}
			paths = ContractUnion(paths, []string{ref})
		}
	}
	if !kernel.SameStringSet(d["allowed_write_paths"], paths) || len(kernel.SemList(data["source_items"])) == 0 {
		return s.Reject("ENGINEERING_DESIGN_INVALID", "工程写范围闭包或来源缺失")
	}
	for _, v := range kernel.SemList(data["traceability"]) {
		if kernel.Text(kernel.SemMap(v)["disposition"]) != "implemented" {
			return s.Reject("ENGINEERING_DESIGN_INVALID", "工程设计来源必须逐条承接")
		}
	}
	for _, v := range kernel.SemList(d["read_dependencies"]) {
		input := kernel.SemMap(v)
		found := false
		for _, v := range kernel.SemList(data["inputs"]) {
			b := kernel.SemMap(v)
			found = found || b["ref"] == input["ref"] && b["digest"] == input["digest"]
		}
		if !found {
			return s.Reject("ENGINEERING_DESIGN_INVALID", "工程只读依赖未冻结")
		}
	}
	return nil
}

func contractMVC(s *kernel.SemanticSession, design map[string]any) error {
	modules, cases, rules := map[string]map[string]any{}, map[string]map[string]any{}, map[string]map[string]any{}
	for _, v := range kernel.SemList(design["module_catalog"]) {
		m := kernel.SemMap(v)
		modules[kernel.Text(m["module_id"])] = m
	}
	for _, v := range kernel.SemList(design["use_case_catalog"]) {
		m := kernel.SemMap(v)
		cases[kernel.Text(m["use_case_id"])] = m
	}
	for _, v := range kernel.SemList(design["rule_catalog"]) {
		m := kernel.SemMap(v)
		rules[kernel.Text(m["rule_id"])] = m
	}
	permitted := map[string][]string{"server": {"service", "core", "client"}, "service": {"repository", "adapter"}, "core": {"repository", "adapter"}, "repository": {}, "adapter": {"client", "feign-client"}, "client": {}, "feign-client": {"client"}}
	for _, m := range modules {
		for _, ref := range kernel.SemStrings(m["depends_on"]) {
			if modules[ref] == nil || !kernel.SemHas(permitted[kernel.Text(m["layer"])], kernel.Text(modules[ref]["layer"])) {
				return s.Reject("TECHNICAL_DESIGN_INVALID", "MVC 模块依赖越界")
			}
		}
	}
	for _, item := range cases {
		if !kernel.SemHas([]string{"service", "core"}, kernel.Text(modules[kernel.Text(item["module_ref"])]["layer"])) {
			return s.Reject("TECHNICAL_DESIGN_INVALID", "MVC 用例须属于 service/core")
		}
		for _, ref := range kernel.SemStrings(item["rule_refs"]) {
			if rules[ref] == nil {
				return s.Reject("TECHNICAL_DESIGN_INVALID", "MVC 未声明规则")
			}
		}
	}
	for id, rule := range rules {
		for _, ref := range kernel.SemStrings(rule["use_case_refs"]) {
			if cases[ref] == nil || !kernel.SemHas(cases[ref]["rule_refs"], id) {
				return s.Reject("TECHNICAL_DESIGN_INVALID", "规则/用例未双向关联")
			}
		}
	}
	for _, v := range kernel.SemList(design["state_transition_catalog"]) {
		if cases[kernel.Text(kernel.SemMap(v)["use_case_ref"])] == nil {
			return s.Reject("TECHNICAL_DESIGN_INVALID", "状态转换用例未知")
		}
	}
	if len(kernel.SemList(design["state_transition_catalog"])) == 0 && kernel.Text(design["state_not_applicable_reason"]) == "" {
		return s.Reject("TECHNICAL_DESIGN_INVALID", "无状态变化须说明")
	}
	for _, v := range kernel.SemList(design["persistence_mapping"]) {
		m := kernel.SemMap(v)
		if cases[kernel.Text(m["use_case_ref"])] == nil || kernel.Text(modules[kernel.Text(m["repository_ref"])]["layer"]) != "repository" {
			return s.Reject("TECHNICAL_DESIGN_INVALID", "持久化用例/Repository 无效")
		}
	}
	for _, v := range kernel.SemList(design["integration_catalog"]) {
		if kernel.Text(modules[kernel.Text(kernel.SemMap(v)["adapter_ref"])]["layer"]) != "adapter" {
			return s.Reject("TECHNICAL_DESIGN_INVALID", "集成未关联 Adapter")
		}
	}
	if len(kernel.SemList(design["integration_catalog"])) == 0 && kernel.Text(design["integration_not_applicable_reason"]) == "" {
		return s.Reject("TECHNICAL_DESIGN_INVALID", "无集成须说明")
	}
	return nil
}

func contractTactical(s *kernel.SemanticSession, d map[string]any) error {
	if e := ContractRequired(s, d, "schema_version", "tactical_design_id", "tactical_version", "status", "context_ref", "aggregate_catalog", "entity_catalog", "value_object_catalog", "behavior_catalog", "invariant_catalog", "state_transition_catalog", "consistency_policy", "domain_event_catalog", "gateway_catalog", "persistence_mapping", "test_seams", "adr_candidates", "upstream_impact", "version", "digest", "evidence_refs"); e != nil {
		return e
	}
	if kernel.ContractN(d["schema_version"]) != 1 || !regexp.MustCompile("^tactical-design[.][a-z0-9][a-z0-9-]*$").MatchString(kernel.Text(d["tactical_design_id"])) || !regexp.MustCompile("^v[0-9]+$").MatchString(kernel.Text(d["version"])) || !regexp.MustCompile("^v[0-9]+$").MatchString(kernel.Text(d["tactical_version"])) || !regexp.MustCompile("^sha256:[A-Fa-f0-9]{64}$").MatchString(kernel.Text(d["digest"])) || !kernel.SemHas([]string{"draft", "ready-for-human", "approved", "blocked", "stale", "drift", "new_impacts", "not-applicable"}, kernel.Text(d["status"])) {
		return s.Reject("TACTICAL_DESIGN_INVALID", "战术身份、版本、状态或摘要格式非法")
	}
	requireStrings := func(m map[string]any, keys ...string) error {
		for _, key := range keys {
			if strings.TrimSpace(kernel.Text(m[key])) == "" {
				return s.Reject("TACTICAL_DESIGN_INVALID", "战术字段须为非空字符串: "+key)
			}
		}
		return nil
	}
	requireArray := func(m map[string]any, key string, minimum int) error {
		a, ok := m[key].([]any)
		if !ok || len(a) < minimum {
			return s.Reject("TACTICAL_DESIGN_INVALID", "战术字段缺少数组或项: "+key)
		}
		return nil
	}
	for _, key := range []string{"aggregate_catalog", "entity_catalog", "behavior_catalog", "invariant_catalog", "state_transition_catalog", "gateway_catalog", "persistence_mapping", "test_seams"} {
		if e := requireArray(d, key, 1); e != nil {
			return e
		}
	}
	for _, key := range []string{"value_object_catalog", "domain_event_catalog", "adr_candidates", "evidence_refs"} {
		if e := requireArray(d, key, 0); e != nil {
			return e
		}
	}
	policy, ok := kernel.Object(d["consistency_policy"])
	if !ok {
		return s.Reject("TACTICAL_DESIGN_INVALID", "一致性政策须为对象")
	}
	if e := requireStrings(policy, "transaction_boundary", "concurrency", "idempotency", "cross_aggregate_strategy"); e != nil {
		return e
	}
	upstream, ok := kernel.Object(d["upstream_impact"])
	if !ok {
		return s.Reject("TACTICAL_DESIGN_INVALID", "上游影响须为对象")
	}
	if e := requireStrings(upstream, "spec_ref", "strategic_ref", "api_ref", "data_ref"); e != nil {
		return e
	}
	if upstream["upstream_current"] == false && kernel.Text(d["status"]) != "stale" {
		return s.Reject("TACTICAL_DESIGN_INVALID", "上游已过期须声明stale")
	}
	keys := map[string]string{"aggregate_catalog": "aggregate_id", "entity_catalog": "entity_id", "value_object_catalog": "value_object_id", "behavior_catalog": "behavior_id", "invariant_catalog": "invariant_id", "state_transition_catalog": "transition_id", "domain_event_catalog": "event_id", "gateway_catalog": "gateway_id", "test_seams": "seam_id"}
	prefixes := map[string]string{"aggregate_catalog": "aggregate", "entity_catalog": "entity", "value_object_catalog": "value-object", "behavior_catalog": "behavior", "invariant_catalog": "invariant", "state_transition_catalog": "transition", "domain_event_catalog": "domain-event", "gateway_catalog": "gateway", "test_seams": "test-seam"}
	catalogs := map[string]map[string]map[string]any{}
	for catalog, key := range keys {
		catalogs[catalog] = map[string]map[string]any{}
		for _, v := range kernel.SemList(d[catalog]) {
			m, ok := kernel.Object(v)
			id := kernel.Text(m[key])
			if !ok || !regexp.MustCompile("^"+prefixes[catalog]+"[.][a-z0-9][a-z0-9-]*$").MatchString(id) || catalogs[catalog][id] != nil {
				return s.Reject("TACTICAL_DESIGN_INVALID", "目录ID缺失、非法或重复")
			}
			catalogs[catalog][id] = m
		}
	}
	refsExist := func(values any, catalog string) error {
		for _, v := range kernel.SemList(values) {
			ref, ok := v.(string)
			if !ok || catalogs[catalog][ref] == nil {
				return s.Reject("TACTICAL_DESIGN_INVALID", "领域引用悬空")
			}
		}
		return nil
	}
	for id, a := range catalogs["aggregate_catalog"] {
		if e := requireStrings(a, "aggregate_id", "name", "context_ref", "root_entity", "consistency_boundary"); e != nil {
			return e
		}
		if a["model_origin"] == "strategic-concept" || a["api_exposure"] != "internal-only" || catalogs["entity_catalog"][kernel.Text(a["root_entity"])] == nil || catalogs["entity_catalog"][kernel.Text(a["root_entity"])]["aggregate_id"] != id {
			return s.Reject("TACTICAL_DESIGN_INVALID", "聚合根、来源或暴露冲突")
		}
		for key, catalog := range map[string]string{"invariant_refs": "invariant_catalog", "behavior_refs": "behavior_catalog"} {
			if e := requireArray(a, key, 1); e != nil {
				return e
			}
			if e := refsExist(a[key], catalog); e != nil {
				return e
			}
		}
	}
	fields := map[string][]string{"entity_catalog": {"entity_id", "name", "aggregate_id", "identity", "lifecycle"}, "behavior_catalog": {"behavior_id", "name", "aggregate_id", "command"}, "invariant_catalog": {"invariant_id", "aggregate_id", "statement"}, "state_transition_catalog": {"transition_id", "aggregate_id", "from", "to", "behavior_id"}, "domain_event_catalog": {"event_id", "name", "aggregate_id", "delivery"}}
	for catalog, required := range fields {
		for _, m := range catalogs[catalog] {
			if e := requireStrings(m, required...); e != nil {
				return e
			}
			if catalogs["aggregate_catalog"][kernel.Text(m["aggregate_id"])] == nil {
				return s.Reject("TACTICAL_DESIGN_INVALID", "聚合归属悬空")
			}
			if catalog == "state_transition_catalog" && catalogs["behavior_catalog"][kernel.Text(m["behavior_id"])] == nil {
				return s.Reject("TACTICAL_DESIGN_INVALID", "状态转换行为悬空")
			}
			if catalog == "behavior_catalog" {
				for _, key := range []string{"preconditions", "invariant_refs", "postconditions", "event_refs"} {
					minimum := 0
					if key == "invariant_refs" {
						minimum = 1
					}
					if e := requireArray(m, key, minimum); e != nil {
						return e
					}
				}
				if e := refsExist(m["invariant_refs"], "invariant_catalog"); e != nil {
					return e
				}
				if e := refsExist(m["event_refs"], "domain_event_catalog"); e != nil {
					return e
				}
			}
			if catalog == "domain_event_catalog" {
				if e := requireArray(m, "consumers", 0); e != nil {
					return e
				}
			}
		}
	}
	for _, m := range catalogs["value_object_catalog"] {
		if e := ContractRequired(s, m, "value_object_id", "name", "aggregate_id", "immutable"); e != nil {
			return e
		}
		if m["immutable"] != true || catalogs["aggregate_catalog"][kernel.Text(m["aggregate_id"])] == nil {
			return s.Reject("TACTICAL_DESIGN_INVALID", "值对象归属或immutable无效")
		}
	}
	for _, m := range catalogs["gateway_catalog"] {
		if e := requireStrings(m, "gateway_id", "name", "context_ref", "layer"); e != nil {
			return e
		}
		if m["layer"] != "Domain" {
			return s.Reject("TACTICAL_DESIGN_INVALID", "Gateway必须属于Domain")
		}
		if e := requireArray(m, "capabilities", 1); e != nil {
			return e
		}
	}
	for _, v := range kernel.SemList(d["persistence_mapping"]) {
		m, ok := kernel.Object(v)
		if !ok {
			return s.Reject("TACTICAL_DESIGN_INVALID", "存储映射须为对象")
		}
		if e := requireStrings(m, "aggregate_id", "storage_model", "notes"); e != nil {
			return e
		}
		if catalogs["aggregate_catalog"][kernel.Text(m["aggregate_id"])] == nil {
			return s.Reject("TACTICAL_DESIGN_INVALID", "存储聚合悬空")
		}
	}
	for _, m := range catalogs["test_seams"] {
		if e := requireStrings(m, "seam_id", "kind", "subject_ref", "assertion"); e != nil {
			return e
		}
		found := false
		for _, catalog := range []string{"aggregate_catalog", "entity_catalog", "value_object_catalog", "behavior_catalog", "invariant_catalog", "state_transition_catalog", "gateway_catalog"} {
			found = found || catalogs[catalog][kernel.Text(m["subject_ref"])] != nil
		}
		if !found {
			return s.Reject("TACTICAL_DESIGN_INVALID", "seam主体悬空")
		}
	}
	if kernel.SemMap(d["complexity"])["escalate_to_standalone"] == true && strings.TrimSpace(kernel.Text(kernel.SemMap(d["complexity"])["standalone_ref"])) == "" {
		return s.Reject("TACTICAL_DESIGN_INVALID", "复杂度升级缺少独立设计")
	}
	return nil
}

// verifyScaffoldCommon is the contract-level part shared by every scaffold
// verification: JSON form, schema, approval state, generation policy and the
// fixed write/evidence declarations.
func verifyScaffoldCommon(s *kernel.SemanticSession, ref string) (map[string]any, error) {
	bytes, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	if !json.Valid(bytes) {
		return nil, s.Reject("SCAFFOLD_FORMAT", "脚手架合同必须为 JSON 对象")
	}
	doc, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/project-scaffold-contract.schema.json", doc); e != nil {
		return nil, e
	}
	if (kernel.ContractN(doc["schema_version"]) != 4 && kernel.ContractN(doc["schema_version"]) != 5) || kernel.Text(doc["status"]) != "approved" || doc["current_version"] != true || kernel.Text(doc["persisted_ref"]) == "" {
		return nil, s.Reject("SCAFFOLD_CONTRACT", "脚手架合同必须已批准、持久化且当前")
	}
	policy := kernel.SemMap(doc["generation_policy"])
	for key, value := range map[string]string{"mode": "initialize-only", "existing_target": "unsupported", "old_project_migration": "unsupported", "template_upgrade": "unsupported"} {
		if kernel.Text(policy[key]) != value {
			return nil, s.Reject("SCAFFOLD_POLICY", "不支持的生成政策")
		}
	}
	if !ContractUnique(doc["allowed_write_paths"], true) || !ContractUnique(doc["verification_commands"], true) || !kernel.SemHas(doc["expected_evidence_files"], ".yss/scaffold-generation.json") {
		return nil, s.Reject("SCAFFOLD_CONTRACT", "写范围/固定验证/Manifest 证据缺失")
	}
	return doc, nil
}

func verifyScaffoldSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	doc, e := verifyScaffoldCommon(s, ref)
	if e != nil {
		return e
	}
	if kernel.Text(doc["delivery_role"]) == "frontend" {
		boundOpts := kernel.SemanticOptions(opts)
		boundOpts["contract-ref"] = ref
		return contractFrontendScaffold(s, doc, boundOpts)
	}
	decision, e := scaffoldBackendInputs(s, ref, doc, opts)
	if e != nil {
		return e
	}
	return scaffoldPlatformQualification(s, doc, decision)
}

// verifyScaffoldInputsSemantic checks everything a backend generator consumes
// except platform qualification, which the generator evaluates itself because it
// may be run against a candidate catalog.
func verifyScaffoldInputsSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	doc, e := verifyScaffoldCommon(s, ref)
	if e != nil {
		return e
	}
	if kernel.Text(doc["delivery_role"]) != "backend" {
		return s.Reject("SCAFFOLD_CONTRACT", "后端脚手架身份无效")
	}
	_, e = scaffoldBackendInputs(s, ref, doc, opts)
	return e
}

func scaffoldBackendInputs(s *kernel.SemanticSession, ref string, doc map[string]any, opts map[string]string) (map[string]any, error) {
	work := kernel.SemMap(doc["work_unit"])
	if work["primary_skill"] != doc["generator_skill"] || kernel.Text(work["tdd_mode"]) != "controlled-generation" || work["controlled_generation"] != true || !ContractSame(work["allowed_write_paths"], doc["allowed_write_paths"]) || !ContractSame(work["verification_commands"], doc["verification_commands"]) {
		return nil, s.Reject("SCAFFOLD_WORK_UNIT", "受控生成工作单元与合同冲突")
	}
	if kernel.Text(doc["delivery_role"]) != "backend" || kernel.Text(doc["scaffold_status"]) != "required" {
		return nil, s.Reject("SCAFFOLD_CONTRACT", "后端脚手架身份无效")
	}
	family, skill := kernel.Text(doc["architecture_family"]), kernel.Text(doc["generator_skill"])
	if family == "domain-driven" && skill != "yss-ddd-scaffold-generator" || family == "layered-mvc" && skill != "yss-layered-mvc-scaffold-generator" || !kernel.SemHas([]string{"domain-driven", "layered-mvc"}, family) {
		return nil, s.Reject("SCAFFOLD_ARCHITECTURE", "生成器和架构族冲突")
	}
	if !ContractSame(doc["verification_commands"], []string{"./mvnw validate", "./mvnw test", "./mvnw package"}) {
		return nil, s.Reject("SCAFFOLD_VERIFICATION", "Maven 验证入口须为固定三条命令")
	}
	approval := kernel.SemMap(doc["approval"])
	if approval["approval_ref"] != doc["lifecycle_approval_ref"] || approval["persisted_ref"] != doc["persisted_ref"] || approval["current_version"] != doc["contract_version"] {
		return nil, s.Reject("SCAFFOLD_APPROVAL", "批准指针或版本冲突")
	}
	coordinates := kernel.SemMap(doc["maven_coordinates"])
	values := []any{coordinates["group_id"], coordinates["project_version"], kernel.SemMap(coordinates["parent"])["group_id"], kernel.SemMap(coordinates["parent"])["artifact_id"], kernel.SemMap(coordinates["parent"])["version"], coordinates["yss_components_version"]}
	for _, v := range values {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(kernel.Text(v)) {
			return nil, s.Reject("SCAFFOLD_COORDINATES", "Maven 坐标非法")
		}
	}
	decisionRef := kernel.Text(doc["decision_ref"])
	if !filepath.IsAbs(decisionRef) {
		decisionRef = path.Join(path.Dir(ref), decisionRef)
	}
	if filepath.IsAbs(decisionRef) {
		r, e := filepath.Rel(s.Root, decisionRef)
		if e != nil || strings.HasPrefix(r, "..") {
			return nil, s.Reject("PATH", "架构决定越出治理 root")
		}
		decisionRef = filepath.ToSlash(r)
	}
	record, e := ContractBoundDoc(s, map[string]any{"ref": decisionRef, "digest": doc["decision_digest"]})
	if e != nil {
		return nil, e
	}
	decision := kernel.ApFind(record["decisions"], "decision_id", kernel.Text(doc["decision_id"]))
	if kernel.Text(record["kind"]) != "scaffold-architecture-decisions" || record["template"] != false || kernel.Text(record["status"]) != "current" || kernel.Text(decision["status"]) != "lifecycle-approved" || decision["confirmed_architecture"] != doc["architecture_family"] || decision["project_id"] != doc["project_name"] {
		return nil, s.Reject("SCAFFOLD_DECISION", "架构决定不是当前项目正式批准")
	}
	profiles, modules := kernel.SemMap(doc["profiles"]), kernel.SemMap(doc["module_profile"])
	if decision["platform_profile"] != profiles["platform"] || decision["architecture_profile"] != doc["architecture_profile"] || kernel.Text(decision["verification_database"]) != "h2" || kernel.Text(decision["production_database"]) != "not-bound" || decision["database_profile"] != nil || !ContractSame(decision["requested_capabilities"], modules["requested_capabilities"]) || !ContractSame(decision["resolved_modules"], modules["resolved_modules"]) {
		return nil, s.Reject("SCAFFOLD_DECISION", "架构 Profile/模块闭包冲突")
	}
	if e = ContractScaffoldUserDecision(s, decision); e != nil {
		return nil, e
	}
	if kernel.Text(doc["architecture_profile"]) == "mvc-data-analysis-v1" {
		return nil, s.Reject("SCAFFOLD_RETIRED", "数据分析合同仅允许历史只读审计")
	}
	if family == "layered-mvc" && doc["init_git"] != false {
		return nil, s.Reject("SCAFFOLD_POLICY", "通用 MVC 不允许初始化 Git")
	}
	if kernel.ContractN(doc["schema_version"]) == 5 {
		if e = contractReferenceExample(s, kernel.SemMap(doc["reference_example"])); e != nil {
			return nil, e
		}
	}
	if e = contractBackendPrerequisites(s, kernel.SemMap(doc["design_prerequisites"]), doc, opts); e != nil {
		return nil, e
	}
	return decision, nil
}

func scaffoldPlatformQualification(s *kernel.SemanticSession, doc, decision map[string]any) error {
	profiles, modules := kernel.SemMap(doc["profiles"]), kernel.SemMap(doc["module_profile"])
	coordinates := kernel.SemMap(doc["maven_coordinates"])
	profile, entry, reports, e := contractPlatform(s, kernel.SemMap(doc["platform_configuration"]), true)
	if e != nil {
		return e
	}
	bom := kernel.SemMap(kernel.SemMap(doc["platform_configuration"])["bom"])
	if profiles["platform"] != profile["id"] || profiles["validation_namespace"] != profile["validation_namespace"] || !ContractSame(coordinates["parent"], kernel.SemMap(doc["platform_configuration"])["parent"]) || coordinates["yss_components_version"] != bom["version"] || bom["group_id"] != "com.yss.cloud" || bom["artifact_id"] != "yss-components-bom" || !ContractSame(decision["platform_configuration"], doc["platform_configuration"]) {
		return s.Reject("SCAFFOLD_PLATFORM", "架构决定/合同平台或Maven坐标变化")
	}
	for _, capability := range kernel.SemStrings(modules["requested_capabilities"]) {
		if !kernel.SemHas(entry["capabilities"], capability) {
			return s.Reject("SCAFFOLD_PLATFORM", "平台未验证架构能力")
		}
	}
	found := false
	for _, report := range reports {
		if report["architecture_family"] != doc["architecture_family"] {
			continue
		}
		all := true
		for _, capability := range kernel.SemStrings(modules["requested_capabilities"]) {
			all = all && kernel.SemHas(report["verified_capabilities"], capability)
		}
		found = found || all
	}
	if !found {
		return s.Reject("SCAFFOLD_PLATFORM", "架构/能力组合缺少qualification证据")
	}
	return nil
}

func contractFrontendScaffold(s *kernel.SemanticSession, doc map[string]any, opts map[string]string) error {
	if kernel.Text(doc["scaffold_kind"]) != "frontend-yss-vue3" || kernel.Text(doc["generator_skill"]) != "yss-frontend-scaffold-generator" {
		return s.Reject("SCAFFOLD_CONTRACT", "前端生成器身份错误")
	}
	front := kernel.SemMap(doc["frontend"])
	template := kernel.SemMap(front["template"])
	if kernel.Text(template["kind"]) == "bundled" {
		for _, key := range []string{"app_name", "microapp_name"} {
			if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(kernel.Text(front[key])) {
				return s.Reject("SCAFFOLD_NAME", "应用名称非法")
			}
		}
		if !regexp.MustCompile(`^/(?:[a-zA-Z0-9_-]+/?)*$`).MatchString(kernel.Text(front["base_route"])) || len(kernel.SemMap(front["replacements"])) > 0 {
			return s.Reject("SCAFFOLD_TEMPLATE", "内置基线不接受任意替换或非法 route")
		}
		manifestRef := ".agents/skills/yss-frontend-scaffold-generator/references/data-quality-v1.manifest.json"
		b, e := s.Bind(manifestRef)
		if e != nil {
			return e
		}
		if b.Digest != kernel.Text(template["manifest_digest"]) {
			return s.Reject("SCAFFOLD_TEMPLATE", "内置 manifest 摘要冲突")
		}
		manifest, e := s.Doc(manifestRef)
		if e != nil {
			return e
		}
		if manifest["baseline_id"] != template["baseline_id"] {
			return s.Reject("SCAFFOLD_TEMPLATE", "基线身份冲突")
		}
		prefix := ".agents/skills/yss-frontend-scaffold-generator/assets/data-quality-v1"
		files, e := s.Scan(prefix)
		if e != nil {
			return e
		}
		expected := kernel.SemMap(manifest["files"])
		if len(files) != len(expected) {
			return s.Reject("SCAFFOLD_TEMPLATE", "基线文件清单变化")
		}
		for _, file := range files {
			raw, e := s.Bytes(file)
			if e != nil {
				return e
			}
			ref := strings.TrimPrefix(file, prefix+"/")
			if strings.TrimPrefix(kernel.Text(expected[ref]), "sha256:") != safefs.Digest(raw) {
				return s.Reject("SCAFFOLD_TEMPLATE", "基线源字节变化")
			}
		}
	} else {
		if e := contractFrontendGitTemplate(s, doc, template, opts); e != nil {
			return e
		}
	}
	if kernel.Text(front["openapi_impact"]) == "frozen" {
		if _, e := contractBinding(s, map[string]any{"ref": front["openapi_json_ref"], "digest": front["openapi_json_digest"]}); e != nil {
			return e
		}
	} else if kernel.Text(front["openapi_impact"]) != "not-applicable" || kernel.Text(front["openapi_not_applicable_reason"]) == "" {
		return s.Reject("SCAFFOLD_API", "API 不适用须说明理由")
	}
	return nil
}

func contractFrontendGitTemplate(s *kernel.SemanticSession, doc, template map[string]any, opts map[string]string) error {
	checkout := kernel.First(opts["template-checkout"], s.Args["template-checkout"])
	if checkout == "" {
		return s.Unavailable("CAPABILITY", "外部Git模板需要显式template-checkout")
	}
	if !filepath.IsAbs(checkout) || filepath.Clean(checkout) != checkout {
		return s.Reject("SCAFFOLD_TEMPLATE", "模板checkout必须为规范绝对目录")
	}
	if kernel.Text(template["repository"]) == "" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(kernel.Text(template["commit"])) {
		return s.Reject("SCAFFOLD_TEMPLATE", "模板须绑定仓库及完整固定commit")
	}
	if e := s.RegisterExternalRoot(checkout, opts["contract-ref"]); e != nil {
		return e
	}
	top, e := s.Git(checkout, "rev-parse", "--show-toplevel")
	if e != nil {
		return e
	}
	if filepath.Clean(strings.TrimSpace(string(top))) != checkout {
		return s.Reject("SCAFFOLD_TEMPLATE", "模板checkout不是Git工作树")
	}
	head, e := s.Git(checkout, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(head)) != kernel.Text(template["commit"]) {
		return s.Reject("SCAFFOLD_TEMPLATE", "模板HEAD与批准commit不一致")
	}
	origin, e := s.Git(checkout, "config", "--local", "--get", "remote.origin.url")
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(origin)) != kernel.Text(template["repository"]) {
		return s.Reject("SCAFFOLD_TEMPLATE", "模板origin与批准仓库不一致")
	}
	// Read the approved immutable tree. Uncommitted checkout edits do not become
	// template authority, matching the old producer's fixed-commit archive.
	tree, e := s.Git(checkout, "ls-tree", "-rz", "--full-tree", kernel.Text(template["commit"]))
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	total := 0
	for _, line := range strings.Split(string(tree), "\x00") {
		if line == "" {
			continue
		}
		header, ref, ok := strings.Cut(line, "\t")
		parts := strings.Fields(header)
		if !ok || len(parts) != 3 || !kernel.ContractPath(ref) || seen[ref] || parts[1] != "blob" || !kernel.SemHas([]string{"100644", "100755", "120000"}, parts[0]) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(parts[2]) {
			return s.Reject("SCAFFOLD_TEMPLATE", "模板Git树路径、类型或对象非法")
		}
		seen[ref] = true
		bytes, e := s.Git(checkout, "cat-file", "blob", parts[2])
		if e != nil {
			return e
		}
		total += len(bytes)
		if total > 256<<20 {
			return s.Unavailable("CAPABILITY", "模板固定树超过256MiB观测上限")
		}
	}
	return nil
}

func contractBackendPrerequisites(s *kernel.SemanticSession, p, scaffold map[string]any, opts map[string]string) error {
	technical, e := ContractBoundDoc(s, p["technical_design"])
	if e != nil {
		return e
	}
	if kernel.ContractN(technical["schema_version"]) != 2 || kernel.Text(technical["status"]) != "approved" || technical["version"] != kernel.SemMap(p["technical_design"])["version"] {
		return s.Reject("SCAFFOLD_TECHNICAL", "技术设计不是当前 approved v2")
	}
	if scaffold != nil {
		a := kernel.SemMap(technical["architecture"])
		if a["family"] != scaffold["architecture_family"] || a["project_id"] != scaffold["project_name"] || a["decision_digest"] != scaffold["decision_digest"] {
			return s.Reject("SCAFFOLD_TECHNICAL", "技术设计与脚手架架构决定冲突")
		}
	}
	if e = contractTechnicalDesign(s, kernel.Text(kernel.SemMap(p["technical_design"])["ref"]), technical, nil, opts); e != nil {
		return e
	}
	data, e := ContractBoundDoc(s, p["data_architecture_decision"])
	if e != nil {
		return e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/data-architecture-decision.schema.json", data); e != nil {
		return e
	}
	if data["decision_version"] != kernel.SemMap(p["data_architecture_decision"])["version"] {
		return s.Reject("DATA_ARCHITECTURE_STALE", "数据架构决定版本不匹配")
	}
	if kernel.Text(data["status"]) != "approved" || data["current_version"] != true || data["impact"] != kernel.SemMap(p["data_architecture_decision"])["impact"] {
		return s.Reject("DATA_ARCHITECTURE_STALE", "数据架构决定不是当前批准影响结论")
	}
	if _, e = contractBinding(s, map[string]any{"ref": data["assessment_ref"], "digest": data["assessment_digest"]}); e != nil {
		return e
	}
	if e = ContractEvidence(s, data["evidence_refs"]); e != nil {
		return e
	}
	if kernel.Text(data["impact"]) == "required" {
		if _, e = contractBinding(s, map[string]any{"ref": data["data_architecture_ref"], "digest": data["data_architecture_digest"]}); e != nil {
			return e
		}
		if e = ContractEvidence(s, data["review_evidence_refs"]); e != nil {
			return e
		}
	}
	api, e := contractAPIDecision(s, kernel.SemMap(p["api_contract_decision"]))
	if e != nil {
		return e
	}
	approvalRef := kernel.Text(p["engineering_contract_approval_ref"])
	if scaffold != nil && (approvalRef != kernel.Text(scaffold["lifecycle_approval_ref"]) || approvalRef != kernel.Text(kernel.SemMap(scaffold["approval"])["approval_ref"])) {
		return s.Reject("SCAFFOLD_APPROVAL", "工程批准指针冲突")
	}
	if e = ContractApproval(s, "gate.engineering-contract-approved", kernel.SemMap(p["technical_design"]), approvalRef, opts); e != nil {
		return e
	}
	approval, e := s.Doc(approvalRef)
	if e != nil {
		return e
	}
	approval, e = approvaldomain.SelectApprovalRecordSemantic(s, approval, "gate.engineering-contract-approved")
	if e != nil {
		return e
	}
	packageDoc, e := s.Doc(kernel.Text(approval["subject_ref"]))
	if e != nil {
		return e
	}
	if kernel.ContractN(packageDoc["schema_version"]) != 1 || kernel.Text(packageDoc["kind"]) != "engineering-contract-package" {
		return s.Reject("ENGINEERING_PACKAGE", "缺少工程合同原子批准包")
	}
	if scaffold != nil && (packageDoc["project_id"] != scaffold["project_name"] || !kernel.SameStringSet(approval["approval_scope"], []any{scaffold["contract_id"], scaffold["project_name"]})) {
		return s.Reject("ENGINEERING_PACKAGE", "工程合同包项目或范围冲突")
	}
	if project := opts["project-id"]; project != "" && (kernel.Text(packageDoc["project_id"]) != project || !kernel.SemHas(approval["approval_scope"], project)) {
		return s.Reject("ENGINEERING_PACKAGE", "工程包项目或批准范围与当前消费者不一致")
	}
	for key, doc := range map[string]map[string]any{"technical_design": technical, "data_architecture_decision": data, "api_contract_decision": api} {
		expected := ContractCopy(kernel.SemMap(p[key]))
		version := doc["decision_version"]
		if key == "technical_design" {
			version = doc["version"]
		}
		expected["version"] = version
		if key != "technical_design" {
			expected["impact"] = doc["impact"]
		}
		actual := kernel.SemMap(packageDoc[key])
		for _, field := range []string{"ref", "version", "digest"} {
			if !ContractSame(actual[field], expected[field]) {
				return s.Reject("ENGINEERING_PACKAGE", "工程包未原子绑定当前资产")
			}
		}
		if key != "technical_design" && actual["impact"] != doc["impact"] {
			return s.Reject("ENGINEERING_PACKAGE", "工程包影响结论冲突")
		}
		id := doc["decision_id"]
		if key == "technical_design" {
			id = doc["technical_design_id"]
		}
		match := false
		for _, v := range kernel.SemList(approval["artifact_bindings"]) {
			b := kernel.SemMap(v)
			match = match || b["id"] == id && b["version"] == version && b["digest"] == expected["digest"]
		}
		if !match {
			return s.Reject("ENGINEERING_PACKAGE", "批准记录未绑定当前资产版本和字节")
		}
	}
	if kernel.Text(api["impact"]) == "required" {
		if !ContractSame(packageDoc["frozen_openapi"], api["openapi"]) {
			return s.Reject("ENGINEERING_PACKAGE", "工程包未原子冻结 OpenAPI")
		}
	} else if packageDoc["frozen_openapi"] != nil {
		return s.Reject("ENGINEERING_PACKAGE", "无 API 影响不得携带 frozen_openapi")
	}
	return nil
}

func contractAPIDecision(s *kernel.SemanticSession, binding map[string]any) (map[string]any, error) {
	doc, e := ContractBoundDoc(s, binding)
	if e != nil {
		return nil, e
	}
	suffix := ""
	if kernel.ContractN(doc["schema_version"]) == 2 {
		suffix = "-v2"
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/api-contract-decision"+suffix+".schema.json", doc); e != nil {
		return nil, e
	}
	if kernel.Text(doc["status"]) != "approved" || doc["decision_version"] != binding["version"] || doc["impact"] != binding["impact"] {
		return nil, s.Reject("API_DECISION_STALE", "API 决定版本/影响/批准状态冲突")
	}
	if _, e = contractBinding(s, map[string]any{"ref": doc["assessment_ref"], "digest": doc["assessment_digest"]}); e != nil {
		return nil, e
	}
	if e = ContractEvidence(s, doc["evidence_refs"]); e != nil {
		return nil, e
	}
	if kernel.Text(doc["impact"]) == "not-applicable" {
		return doc, nil
	}
	openapi, e := ContractBoundDoc(s, doc["openapi"])
	if e != nil {
		return nil, e
	}
	_ = openapi
	validation, e := ContractBoundDoc(s, doc["validation_record"])
	if e != nil {
		return nil, e
	}
	review, e := ContractBoundDoc(s, doc["draft_review"])
	if e != nil {
		return nil, e
	}
	bindingAPI := kernel.SemMap(doc["openapi"])
	if kernel.Text(review["result"]) != "approved" || review["blocking_findings"] == nil || len(kernel.SemList(review["blocking_findings"])) > 0 || review["draft_ref"] != bindingAPI["ref"] || review["draft_digest"] != bindingAPI["digest"] {
		return nil, s.Reject("OPENAPI_REVIEW", "OpenAPI 实际审查没有批准当前 Draft")
	}
	if e = ContractEvidence(s, review["evidence_refs"]); e != nil {
		return nil, e
	}
	if e = contractOpenAPIValidation(s, validation); e != nil {
		return nil, e
	}
	draft := kernel.SemMap(validation["draft"])
	if draft["ref"] != bindingAPI["ref"] || draft["sha256"] != bindingAPI["digest"] || kernel.SemMap(doc["freeze"])["version"] != bindingAPI["version"] {
		return nil, s.Reject("OPENAPI_FREEZE", "冻结没有绑定当前接口版本")
	}
	if kernel.ContractN(doc["schema_version"]) == 1 {
		if kernel.SemMap(doc["freeze"])["draft_ref"] != bindingAPI["ref"] || kernel.SemMap(doc["freeze"])["draft_digest"] != bindingAPI["digest"] || kernel.SemMap(doc["draft_review"])["draft_ref"] != bindingAPI["ref"] || kernel.SemMap(doc["draft_review"])["draft_digest"] != bindingAPI["digest"] {
			return nil, s.Reject("OPENAPI_FREEZE", "冻结 Draft 引用冲突")
		}
	}
	return doc, nil
}

// 单独校验 OpenAPI Draft validation record（Node 入口 scripts/verify-openapi-draft-validation-record 的唯一实现）。
// --allow-template 只接受模板记录，仅做 Schema 与模板标记检查；正式记录走与冻结校验同一份 contractOpenAPIValidation。
func verifyOpenAPIValidationSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	record, e := s.Doc(ref)
	if e != nil {
		return e
	}
	template := opts["allow-template"] == "true"
	if template {
		if e = s.ValidateSchema(".template-spec/process/schemas/openapi-draft-validation-record.schema.json", record); e != nil {
			return e
		}
		if record["template"] != true {
			return s.Reject("OPENAPI_VALIDATION", "--allow-template 只接受模板记录")
		}
	} else if e = contractOpenAPIValidation(s, record); e != nil {
		return e
	}
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	draft := kernel.SemMap(record["draft"])
	s.Report.Coverage["openapi_validation"] = map[string]any{"template": template, "draft_ref": draft["ref"], "draft_sha256": draft["sha256"]}
	return nil
}

func contractOpenAPIValidation(s *kernel.SemanticSession, record map[string]any) error {
	if e := s.ValidateSchema(".template-spec/process/schemas/openapi-draft-validation-record.schema.json", record); e != nil {
		return e
	}
	draft, tool := kernel.SemMap(record["draft"]), kernel.SemMap(record["toolchain"])
	layout, err := kernel.ViewWorkLayout(s.V)
	if err != nil {
		return err
	}
	feature, err := layout.FeatureOf(kernel.Text(draft["ref"]))
	if err != nil {
		return err
	}
	base, _ := layout.FeatureRoot(feature)
	if !strings.HasPrefix(kernel.Text(draft["ref"]), base+"/api/") {
		return s.Reject("OPENAPI_VALIDATION", "Draft 必须位于配置的功能包根的 api 目录")
	}
	exit, ok := kernel.Integer(tool["exit_code"])
	if record["template"] != false || kernel.Text(record["status"]) != "passed" || !ok || exit != 0 || kernel.Text(tool["command"]) != "pnpm exec redocly lint "+kernel.Text(draft["ref"]) {
		return s.Reject("OPENAPI_VALIDATION", "实际 lint 未通过或未指向当前 Draft")
	}
	for _, v := range kernel.SemMap(record["checks"]) {
		if kernel.Text(v) != "passed" {
			return s.Reject("OPENAPI_VALIDATION", "结构/lint 检查未通过")
		}
	}
	doc, e := ContractBoundDoc(s, map[string]any{"ref": draft["ref"], "digest": draft["sha256"]})
	if e != nil {
		return e
	}
	if kernel.Text(doc["openapi"]) != "3.1.0" || draft["oas_version"] != doc["openapi"] {
		return s.Reject("OPENAPI_VALIDATION", "必须 OAS 3.1.0")
	}
	lock, e := s.Bytes(kernel.Text(tool["lockfile_ref"]))
	if e != nil {
		return e
	}
	if _, e = s.Bytes(kernel.Text(tool["evidence_ref"])); e != nil {
		return e
	}
	version := regexp.QuoteMeta(kernel.Text(tool["version"]))
	if !regexp.MustCompile(`['"]?@redocly/cli@`+version+`(?:['":\s(]|$)`).Match(lock) && !regexp.MustCompile(`['"]?@redocly/cli['"]?:[\s\S]{0,240}?\n\s+version:\s*['"]?`+version+`(?:\(|['"\s]|$)`).Match(lock) {
		return s.Reject("OPENAPI_VALIDATION", "锁文件未绑定当前 Redocly 版本")
	}
	return contractOpenAPIReferences(s, kernel.Text(draft["ref"]), doc)
}

func contractOpenAPIReferences(s *kernel.SemanticSession, draftRef string, doc map[string]any) error {
	apiRoot := path.Dir(draftRef)
	cache := map[string]map[string]any{draftRef: doc}
	active := map[string]bool{}
	var load func(string, string) (any, string, error)
	load = func(ref, owner string) (any, string, error) {
		if regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`).MatchString(ref) || strings.HasPrefix(ref, "/") || strings.Contains(ref, "\\") {
			return nil, "", s.Reject("OPENAPI_REF", "禁止远程或绝对引用")
		}
		parts := strings.SplitN(ref, "#", 2)
		file := owner
		if parts[0] != "" {
			file = path.Join(path.Dir(owner), parts[0])
		}
		if !kernel.ContractPath(file) || !ContractWithin(file, apiRoot) {
			return nil, "", s.Reject("OPENAPI_REF", "引用越出 feature API 目录")
		}
		// 未缓存的文件必须现读：nil map 装进 any 后不等于 nil，不能用 any(cache[file]) == nil 判断。
		var value any
		if cached, ok := cache[file]; ok {
			value = cached
		} else {
			parsed, e := s.Doc(file)
			if e != nil {
				return nil, "", e
			}
			cache[file] = parsed
			value = parsed
		}
		if len(parts) > 1 && parts[1] != "" {
			if !strings.HasPrefix(parts[1], "/") {
				return nil, "", s.Reject("OPENAPI_REF", "仅支持 JSON Pointer")
			}
			for _, key := range strings.Split(parts[1][1:], "/") {
				decoded, e := url.PathUnescape(key)
				if e != nil {
					return nil, "", s.Reject("OPENAPI_REF", "非法 fragment")
				}
				key = strings.ReplaceAll(strings.ReplaceAll(decoded, "~1", "/"), "~0", "~")
				m, ok := kernel.Object(value)
				if ok {
					var exists bool
					value, exists = m[key]
					if !exists {
						return nil, "", s.Reject("OPENAPI_REF", "引用不可解析")
					}
				} else if a, ok := value.([]any); ok {
					i, e := strconv.Atoi(key)
					if e != nil || i < 0 || i >= len(a) {
						return nil, "", s.Reject("OPENAPI_REF", "引用数组越界")
					}
					value = a[i]
				} else {
					return nil, "", s.Reject("OPENAPI_REF", "引用不可解析")
				}
			}
		}
		return value, file, nil
	}
	var walk func(any, string) error
	walk = func(value any, owner string) error {
		switch v := value.(type) {
		case map[string]any:
			if ref := kernel.Text(v["$ref"]); ref != "" {
				key := owner + "|" + ref
				if !active[key] {
					active[key] = true
					resolved, file, e := load(ref, owner)
					if e != nil {
						return e
					}
					if e = walk(resolved, file); e != nil {
						return e
					}
					delete(active, key)
				}
			}
			for _, child := range v {
				if e := walk(child, owner); e != nil {
					return e
				}
			}
		case []any:
			for _, child := range v {
				if e := walk(child, owner); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e := walk(doc, draftRef); e != nil {
		return e
	}
	ids := map[string]bool{}
	for route, v := range kernel.SemMap(doc["paths"]) {
		item := kernel.SemMap(v)
		for method, v := range item {
			if !kernel.SemHas([]string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}, method) {
				continue
			}
			op := kernel.SemMap(v)
			id := kernel.Text(op["operationId"])
			if id == "" || ids[id] {
				return s.Reject("OPENAPI_OPERATION", "operationId 缺失或重复")
			}
			ids[id] = true
			parameters := append(append([]any{}, kernel.SemList(item["parameters"])...), kernel.SemList(op["parameters"])...)
			for _, placeholder := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(route, -1) {
				found := false
				for _, v := range parameters {
					parameter := kernel.SemMap(v)
					if kernel.Text(parameter["$ref"]) != "" {
						resolved, _, e := load(kernel.Text(parameter["$ref"]), draftRef)
						if e != nil {
							return e
						}
						parameter = kernel.SemMap(resolved)
					}
					found = found || kernel.Text(parameter["in"]) == "path" && kernel.Text(parameter["name"]) == placeholder[1] && parameter["required"] == true
				}
				if !found {
					return s.Reject("OPENAPI_OPERATION", "required path parameter 缺失")
				}
			}
		}
	}
	return nil
}

func ContractTree(s *kernel.SemanticSession, prefix string) (string, []string, error) {
	files, e := s.Scan(prefix)
	if e != nil {
		return "", nil, e
	}
	rows := []any{}
	for _, ref := range files {
		b, e := s.Bytes(ref)
		if e != nil {
			return "", nil, e
		}
		rows = append(rows, map[string]any{"path": strings.TrimPrefix(ref, prefix+"/"), "sha256": "sha256:" + safefs.Digest(b)})
	}
	return ContractDigest(rows), files, nil
}

func contractPNG(s *kernel.SemanticSession, b []byte, viewport map[string]any) error {
	if len(b) < 24 || len(b) > 5<<20 || string(b[:8]) != "\x89PNG\r\n\x1a\n" || string(b[12:16]) != "IHDR" || int(binary.BigEndian.Uint32(b[16:20])) != kernel.ContractN(viewport["width"]) || int(binary.BigEndian.Uint32(b[20:24])) != kernel.ContractN(viewport["height"]) {
		return s.Reject("UI_BASELINE_IMAGE", "PNG 尺寸/格式/大小与 viewport 不一致")
	}
	return nil
}

func ContractExistingUI(s *kernel.SemanticSession, ref string) (map[string]any, error) {
	data, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/existing-ui-baseline.schema.json", data); e != nil {
		return nil, e
	}
	prefix := path.Dir(ref)
	files, e := s.Scan(prefix)
	if e != nil {
		return nil, e
	}
	if len(files) > 20000 {
		return nil, s.Reject("UI_BASELINE_LIMIT", "文件数量超限")
	}
	consumed := map[string]bool{path.Base(ref): true}
	total := 0
	for _, file := range files {
		b, e := s.Bytes(file)
		if e != nil {
			return nil, e
		}
		total += len(b)
	}
	if total > 100<<20 {
		return nil, s.Reject("UI_BASELINE_LIMIT", "包超过 100MiB")
	}
	bound := func(v any) ([]byte, error) {
		b := kernel.SemMap(v)
		rel := kernel.Text(b["ref"])
		if !kernel.ContractPath(rel) {
			return nil, s.Reject("UI_BASELINE_PATH", "基线证据须为安全相对路径")
		}
		bytes, e := s.Bytes(path.Join(prefix, rel))
		if e != nil {
			return nil, e
		}
		consumed[rel] = true
		if len(bytes) == 0 || "sha256:"+safefs.Digest(bytes) != kernel.Text(b["digest"]) {
			return nil, s.Reject("UI_BASELINE_STALE", "原始证据摘要不一致")
		}
		return bytes, nil
	}
	document := func(v any) (map[string]any, error) {
		b, e := bound(v)
		if e != nil {
			return nil, e
		}
		value, e := schema.Parse(b)
		if e != nil {
			return nil, e
		}
		m, ok := kernel.Object(value)
		if !ok {
			return nil, s.Reject("UI_BASELINE_FORMAT", "证据须为对象")
		}
		return m, nil
	}
	source := kernel.SemMap(data["source"])
	manifest, e := document(source["manifest"])
	if e != nil {
		return nil, e
	}
	digest, sourceFiles, e := ContractTree(s, path.Join(prefix, kernel.Text(source["root_ref"])))
	if e != nil {
		return nil, e
	}
	if digest != kernel.Text(source["digest"]) {
		return nil, s.Reject("UI_BASELINE_STALE", "固定源码摘要不一致")
	}
	if _, e = bound(source["lock"]); e != nil {
		return nil, e
	}
	if !strings.HasPrefix(kernel.Text(kernel.SemMap(source["lock"])["ref"]), kernel.Text(source["root_ref"])+"/") {
		return nil, s.Reject("UI_BASELINE_LOCK", "锁文件不属于固定源码")
	}
	if kernel.ContractN(manifest["schema_version"]) != 1 || kernel.Text(manifest["kind"]) != "existing-ui-source-observation" {
		return nil, s.Reject("UI_BASELINE_SOURCE", "源码观测 manifest 类型无效")
	}
	for _, key := range []string{"repository_id", "project_id", "source_commit"} {
		if manifest[key] != source[key] {
			return nil, s.Reject("UI_BASELINE_SOURCE", "源码身份冲突")
		}
	}
	rows := []any{}
	sourcePaths := map[string]bool{}
	for _, file := range sourceFiles {
		rel := strings.TrimPrefix(file, prefix+"/")
		consumed[rel] = true
		sourcePaths[rel] = true
		b, e := s.Bytes(file)
		if e != nil {
			return nil, e
		}
		rows = append(rows, map[string]any{"path": strings.TrimPrefix(file, path.Join(prefix, kernel.Text(source["root_ref"]))+"/"), "digest": "sha256:" + safefs.Digest(b)})
	}
	if manifest["source_digest"] != source["digest"] || !ContractSame(contractSortRows(kernel.SemList(manifest["files"]), "path"), contractSortRows(rows, "path")) {
		return nil, s.Reject("UI_BASELINE_SOURCE", "观测文件集与源码不一致")
	}
	routePrefix := ""
	if data["api_route_mapping"] != nil {
		mapping := kernel.SemMap(data["api_route_mapping"])
		for _, key := range []string{"config", "environment"} {
			if !sourcePaths[kernel.Text(kernel.SemMap(mapping[key])["ref"])] {
				return nil, s.Reject("UI_BASELINE_PROXY", "代理映射未绑定固定源码")
			}
		}
		config, e := bound(mapping["config"])
		if e != nil {
			return nil, e
		}
		environment, e := bound(mapping["environment"])
		if e != nil {
			return nil, e
		}
		if !regexp.MustCompile(`(?m)^\s*const apiBase = env[.]VITE_API_BASE_URL \|\| ['"]/api['"]\s*;?\s*$`).Match(config) || !strings.Contains(string(config), "path.replace(new RegExp(`^${apiBase}`), '')") {
			return nil, s.Reject("UI_BASELINE_PROXY", "未支持的固定 Vite prefix rewrite")
		}
		values := []string{}
		for _, line := range strings.Split(string(environment), "\n") {
			if regexp.MustCompile(`^\s*VITE_API_BASE_URL\s*=`).MatchString(line) {
				values = append(values, strings.TrimSpace(strings.SplitN(line, "=", 2)[1]))
			}
		}
		if len(values) != 1 || values[0] != kernel.Text(mapping["prefix"]) {
			return nil, s.Reject("UI_BASELINE_PROXY", "环境 prefix 不一致")
		}
		routePrefix = kernel.Text(mapping["prefix"])
	}
	build, e := document(data["build"])
	if e != nil {
		return nil, e
	}
	capture, e := document(data["capture"])
	if e != nil {
		return nil, e
	}
	openapi, e := document(data["openapi"])
	if e != nil {
		return nil, e
	}
	if _, e = bound(data["replay"]); e != nil {
		return nil, e
	}
	if !regexp.MustCompile(`^3[.]1[.][0-9]+$`).MatchString(kernel.Text(openapi["openapi"])) || openapi["paths"] == nil {
		return nil, s.Reject("UI_BASELINE_API", "需 OpenAPI 3.1 原始接口")
	}
	for _, record := range []map[string]any{build, capture} {
		if kernel.ContractN(record["schema_version"]) != 1 || record["source_commit"] != source["source_commit"] || record["source_digest"] != source["digest"] || kernel.Text(record["command"]) == "" || !ContractDate(record["executed_at"]) {
			return nil, s.Reject("UI_BASELINE_EXECUTION", "构建/截图未绑定当前源码和实际执行")
		}
		exit, ok := kernel.Integer(record["exit_code"])
		if !ok || exit != 0 || len(kernel.SemList(record["evidence"])) == 0 {
			return nil, s.Reject("UI_BASELINE_EXECUTION", "构建/截图退出码或原始日志缺失")
		}
		for _, evidence := range kernel.SemList(record["evidence"]) {
			if _, e = bound(evidence); e != nil {
				return nil, e
			}
		}
	}
	output := kernel.SemMap(build["output"])
	outputDigest, outputs, e := ContractTree(s, path.Join(prefix, kernel.Text(output["ref"])))
	if e != nil {
		return nil, e
	}
	if outputDigest != kernel.Text(output["digest"]) || build["lock_digest"] != kernel.SemMap(source["lock"])["digest"] {
		return nil, s.Reject("UI_BASELINE_BUILD", "构建输出或锁文件变化")
	}
	for _, ref := range outputs {
		consumed[strings.TrimPrefix(ref, prefix+"/")] = true
	}
	if capture["build_digest"] != kernel.SemMap(data["build"])["digest"] || capture["openapi_digest"] != kernel.SemMap(data["openapi"])["digest"] || kernel.Text(capture["ui_change"]) != "none" {
		return nil, s.Reject("UI_BASELINE_CAPTURE", "截图来源或 UI 改动冲突")
	}
	ids := map[string]bool{}
	for _, v := range kernel.SemList(data["cases"]) {
		item := kernel.SemMap(v)
		id := kernel.Text(item["case_id"])
		if ids[id] {
			return nil, s.Reject("UI_BASELINE_CASE", "用例重复")
		}
		ids[id] = true
		for _, ref := range kernel.SemStrings(item["source_refs"]) {
			if !sourcePaths[ref] {
				return nil, s.Reject("UI_BASELINE_CASE", "用例源码悬空")
			}
		}
		if _, e = bound(item["actions"]); e != nil {
			return nil, e
		}
		api, e := document(item["api"])
		if e != nil {
			return nil, e
		}
		image, e := bound(item["image"])
		if e != nil {
			return nil, e
		}
		if e = contractPNG(s, image, kernel.SemMap(item["viewport"])); e != nil {
			return nil, e
		}
		if kernel.ContractN(api["schema_version"]) != 1 || api["case_id"] != item["case_id"] || api["openapi_digest"] != kernel.SemMap(data["openapi"])["digest"] || api["source_digest"] != source["digest"] || api["build_digest"] != kernel.SemMap(data["build"])["digest"] || len(kernel.SemList(api["exchanges"])) == 0 {
			return nil, s.Reject("UI_BASELINE_API", "真实 API 证据来源冲突")
		}
		for _, v := range kernel.SemList(api["exchanges"]) {
			exchange := kernel.SemMap(v)
			status, ok := kernel.Integer(exchange["status"])
			observed, e := url.Parse(kernel.Text(exchange["url"]))
			if e != nil || !kernel.SemHas([]string{"http", "https"}, observed.Scheme) || kernel.Text(exchange["operation_id"]) == "" || kernel.Text(exchange["method"]) == "" || !ContractDate(exchange["executed_at"]) || !ok || status < 100 || status > 599 {
				return nil, s.Reject("UI_BASELINE_API", "API 交换记录非法")
			}
			routes := []string{}
			for route, v := range kernel.SemMap(openapi["paths"]) {
				for method, v := range kernel.SemMap(v) {
					if strings.EqualFold(method, kernel.Text(exchange["method"])) && kernel.SemMap(v)["operationId"] == exchange["operation_id"] {
						routes = append(routes, route)
					}
				}
			}
			if len(routes) != 1 {
				return nil, s.Reject("UI_BASELINE_API", "API operation 与冻结接口不一致")
			}
			parts := []string{}
			for _, part := range strings.Split(routes[0], "/") {
				if regexp.MustCompile(`^\{[^}]+\}$`).MatchString(part) {
					parts = append(parts, "[^/]+")
				} else {
					parts = append(parts, regexp.QuoteMeta(part))
				}
			}
			observedPath := observed.Path
			if routePrefix != "" {
				if !strings.HasPrefix(observedPath, routePrefix+"/") {
					return nil, s.Reject("UI_BASELINE_PROXY", "API URL prefix 不一致")
				}
				observedPath = strings.TrimPrefix(observedPath, routePrefix)
			}
			if !regexp.MustCompile("^" + strings.Join(parts, "/") + "$").MatchString(observedPath) {
				return nil, s.Reject("UI_BASELINE_API", "API URL 路径不一致")
			}
			for _, key := range []string{"request", "response"} {
				if _, e = bound(exchange[key]); e != nil {
					return nil, e
				}
			}
		}
		matches := []map[string]any{}
		for _, v := range kernel.SemList(capture["cases"]) {
			c := kernel.SemMap(v)
			if c["case_id"] == item["case_id"] {
				matches = append(matches, c)
			}
		}
		if len(matches) != 1 {
			return nil, s.Reject("UI_BASELINE_CAPTURE", "capture 用例缺失或重复")
		}
		for _, key := range []string{"route", "state", "viewport", "source_refs", "actions", "image", "api"} {
			if !ContractSame(matches[0][key], item[key]) {
				return nil, s.Reject("UI_BASELINE_CAPTURE", "截图动作绑定冲突")
			}
		}
	}
	if len(kernel.SemList(capture["cases"])) != len(ids) {
		return nil, s.Reject("UI_BASELINE_CAPTURE", "capture 有未登记用例")
	}
	for _, file := range files {
		if !consumed[strings.TrimPrefix(file, prefix+"/")] {
			return nil, s.Reject("UI_BASELINE_PAYLOAD", "包含未绑定原始文件")
		}
	}
	return data, nil
}

func contractSortRows(rows []any, key string) []any {
	out := append([]any(nil), rows...)
	sort.Slice(out, func(i, j int) bool {
		return kernel.Text(kernel.SemMap(out[i])[key]) < kernel.Text(kernel.SemMap(out[j])[key])
	})
	return out
}

func ContractVisualBaseline(s *kernel.SemanticSession, ref string) (map[string]any, error) {
	data, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if e = ContractRequired(s, data, "schema_version", "baseline_id", "feature", "version", "status", "bundle", "source", "capture_environment", "cases"); e != nil {
		return nil, e
	}
	bundle, source, env := kernel.SemMap(data["bundle"]), kernel.SemMap(data["source"]), kernel.SemMap(data["capture_environment"])
	if kernel.ContractN(data["schema_version"]) != 1 || kernel.Text(data["status"]) != "approved" || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(kernel.Text(data["feature"])) || kernel.Text(data["baseline_id"]) != "visual-baseline."+kernel.Text(data["feature"]) || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(kernel.Text(data["version"])) || kernel.Text(bundle["format"]) != "portable-directory" || kernel.ContractN(bundle["max_image_bytes"]) != 5<<20 || kernel.ContractN(bundle["max_bundle_bytes"]) != 100<<20 {
		return nil, s.Reject("VISUAL_BASELINE", "基线身份、状态或包限制错误")
	}
	if e = ContractRequired(s, env, "browser_version", "operating_system", "fonts_digest", "locale", "timezone", "capture_script_ref", "capture_script_digest", "capture_result_ref", "capture_result_digest"); e != nil {
		return nil, e
	}
	if kernel.Text(env["browser"]) != "chromium" || kernel.ContractN(env["device_scale_factor"]) != 1 || kernel.Text(env["color_space"]) != "srgb" || env["animations_disabled"] != true || env["cursor_hidden"] != true {
		return nil, s.Reject("VISUAL_BASELINE_CAPTURE", "截图环境未满足固定条件")
	}
	prefix := path.Dir(ref)
	consumed := map[string]bool{}
	total := 0
	bound := func(ref, digest string, expectSize int) ([]byte, error) {
		if !kernel.ContractPath(ref) || !kernel.IsSHA256(strings.TrimPrefix(digest, "sha256:")) {
			return nil, s.Reject("VISUAL_BASELINE_PATH", "基线文件或摘要非法")
		}
		bytes, e := s.Bytes(path.Join(prefix, ref))
		if e != nil {
			return nil, e
		}
		if "sha256:"+safefs.Digest(bytes) != digest || expectSize >= 0 && len(bytes) != expectSize {
			return nil, s.Reject("VISUAL_BASELINE_STALE", "基线原始字节变化")
		}
		if !consumed[ref] {
			total += len(bytes)
			consumed[ref] = true
		}
		return bytes, nil
	}
	for _, key := range []string{"prototype", "interaction_spec", "state_matrix"} {
		ref := kernel.Text(source[key+"_ref"])
		if !strings.HasPrefix(ref, "sources/") {
			return nil, s.Reject("VISUAL_BASELINE_PATH", "业务源快照须在 sources/")
		}
		if _, e = bound(ref, kernel.Text(source[key+"_digest"]), -1); e != nil {
			return nil, e
		}
	}
	for _, key := range []string{"capture_script", "capture_result"} {
		ref := kernel.Text(env[key+"_ref"])
		if !strings.HasPrefix(ref, "capture/") {
			return nil, s.Reject("VISUAL_BASELINE_PATH", "截图证据须在 capture/")
		}
		if _, e = bound(ref, kernel.Text(env[key+"_digest"]), -1); e != nil {
			return nil, e
		}
	}
	ids, viewports := map[string]bool{}, map[string]bool{}
	for _, v := range kernel.SemList(data["cases"]) {
		item := kernel.SemMap(v)
		if e = ContractRequired(s, item, "case_id", "route", "page", "state", "viewport", "theme", "locale", "data_scenario", "image_ref", "image_digest", "image_size_bytes", "mask_ref", "mask_digest", "mask_size_bytes", "semantic_refs", "allowed_differences", "result"); e != nil {
			return nil, e
		}
		id := kernel.Text(item["case_id"])
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(id) || ids[id] || kernel.Text(item["result"]) != "passed" {
			return nil, s.Reject("VISUAL_BASELINE_CASE", "用例 ID/状态错误")
		}
		ids[id] = true
		viewport := kernel.SemMap(item["viewport"])
		if e = ContractRequired(s, viewport, "name", "width", "height", "scroll_mode", "scroll_position"); e != nil {
			return nil, e
		}
		if kernel.ContractN(viewport["width"]) <= 0 || kernel.ContractN(viewport["height"]) <= 0 || !kernel.SemHas([]string{"viewport", "segment"}, kernel.Text(viewport["scroll_mode"])) || kernel.Text(viewport["scroll_mode"]) == "viewport" && kernel.ContractN(viewport["scroll_position"]) != 0 {
			return nil, s.Reject("VISUAL_BASELINE_VIEWPORT", "viewport 无效")
		}
		viewports[fmt.Sprintf("%dx%d", kernel.ContractN(viewport["width"]), kernel.ContractN(viewport["height"]))] = true
		for _, ref := range kernel.SemStrings(item["semantic_refs"]) {
			if !kernel.SemHas([]any{source["prototype_ref"], source["interaction_spec_ref"], source["state_matrix_ref"]}, ref) {
				return nil, s.Reject("VISUAL_BASELINE_SEMANTIC", "语义引用未绑定业务快照")
			}
		}
		if !ContractUnique(item["semantic_refs"], true) {
			return nil, s.Reject("VISUAL_BASELINE_SEMANTIC", "缺少语义引用")
		}
		imageRef := kernel.Text(item["image_ref"])
		if !strings.HasPrefix(imageRef, "images/") || !strings.HasSuffix(imageRef, ".png") {
			return nil, s.Reject("VISUAL_BASELINE_IMAGE", "图片路径非法")
		}
		image, e := bound(imageRef, kernel.Text(item["image_digest"]), kernel.ContractN(item["image_size_bytes"]))
		if e != nil {
			return nil, e
		}
		if e = contractPNG(s, image, viewport); e != nil {
			return nil, e
		}
		if kernel.Text(item["mask_ref"]) == "not-applicable" {
			if kernel.Text(item["mask_digest"]) != "not-applicable" || kernel.ContractN(item["mask_size_bytes"]) != 0 {
				return nil, s.Reject("VISUAL_BASELINE_MASK", "无 mask 占位合同错误")
			}
		} else {
			maskRef := kernel.Text(item["mask_ref"])
			if !strings.HasPrefix(maskRef, "masks/") || !strings.HasSuffix(maskRef, ".png") {
				return nil, s.Reject("VISUAL_BASELINE_MASK", "mask 路径非法")
			}
			mask, e := bound(maskRef, kernel.Text(item["mask_digest"]), kernel.ContractN(item["mask_size_bytes"]))
			if e != nil {
				return nil, e
			}
			if e = contractPNG(s, mask, viewport); e != nil {
				return nil, e
			}
		}
	}
	if len(ids) == 0 || !viewports["1440x900"] || !viewports["390x844"] || total != kernel.ContractN(bundle["size_bytes"]) || total > 100<<20 {
		return nil, s.Reject("VISUAL_BASELINE_LIMIT", "缺少规定 viewport 或实际包大小不一致")
	}
	files, e := s.Scan(prefix)
	if e != nil {
		return nil, e
	}
	for _, file := range files {
		rel := strings.TrimPrefix(file, prefix+"/")
		if rel != "visual-baseline.yaml" && !consumed[rel] {
			return nil, s.Reject("VISUAL_BASELINE_PAYLOAD", "存在未绑定 payload")
		}
	}
	raw, e := s.Bytes(ref)
	if e != nil {
		return nil, e
	}
	digest, e := contractVisualDigest(raw)
	if e != nil {
		return nil, s.Unavailable("INPUT", e.Error())
	}
	if digest != kernel.Text(bundle["digest"]) {
		return nil, s.Reject("VISUAL_BASELINE_STALE", "manifest 插入序摘要不一致")
	}
	return data, nil
}

// Visual Baseline v1 intentionally hashes a fixed field order, while nested
// viewport objects retain YAML/JSON source order. Do not replace it with a
// sorted-map digest: that would silently change existing approvals.
func contractVisualDigest(raw []byte) (string, error) {
	var root yaml.Node
	if e := yaml.Unmarshal(raw, &root); e != nil {
		return "", e
	}
	if len(root.Content) != 1 {
		return "", fmt.Errorf("单文档要求")
	}
	node := root.Content[0]
	lookup := func(n *yaml.Node, key string) *yaml.Node {
		if n == nil {
			return nil
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
		return nil
	}
	var emit func(*yaml.Node) (string, error)
	emit = func(n *yaml.Node) (string, error) {
		if n == nil {
			return "null", nil
		}
		switch n.Kind {
		case yaml.MappingNode:
			parts := []string{}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, _ := ContractJSON(n.Content[i].Value)
				v, e := emit(n.Content[i+1])
				if e != nil {
					return "", e
				}
				parts = append(parts, k+":"+v)
			}
			return "{" + strings.Join(parts, ",") + "}", nil
		case yaml.SequenceNode:
			parts := []string{}
			for _, n := range n.Content {
				v, e := emit(n)
				if e != nil {
					return "", e
				}
				parts = append(parts, v)
			}
			return "[" + strings.Join(parts, ",") + "]", nil
		case yaml.ScalarNode:
			var value any
			if e := n.Decode(&value); e != nil {
				return "", e
			}
			return ContractJSON(value)
		default:
			return "", fmt.Errorf("不支持 alias")
		}
	}
	source, env := lookup(node, "source"), lookup(node, "capture_environment")
	parts := []string{}
	put := func(key string, n *yaml.Node) error {
		if n == nil {
			return nil
		}
		k, _ := ContractJSON(key)
		v, e := emit(n)
		if e != nil {
			return e
		}
		parts = append(parts, k+":"+v)
		return nil
	}
	for _, key := range []string{"schema_version", "baseline_id", "feature", "version"} {
		if e := put(key, lookup(node, key)); e != nil {
			return "", e
		}
	}
	for _, key := range []string{"prototype_ref", "prototype_digest", "interaction_spec_ref", "interaction_spec_digest", "state_matrix_ref", "state_matrix_digest"} {
		if e := put(key, lookup(source, key)); e != nil {
			return "", e
		}
	}
	for _, key := range []string{"capture_script_ref", "capture_script_digest", "capture_result_ref", "capture_result_digest"} {
		if e := put(key, lookup(env, key)); e != nil {
			return "", e
		}
	}
	cases := []string{}
	if n := lookup(node, "cases"); n != nil {
		for _, c := range n.Content {
			row := []string{}
			for _, key := range []string{"case_id", "route", "page", "state", "viewport", "theme", "locale", "data_scenario", "image_ref", "image_digest", "image_size_bytes", "mask_ref", "mask_digest", "mask_size_bytes", "semantic_refs", "allowed_differences"} {
				if n := lookup(c, key); n != nil {
					k, _ := ContractJSON(key)
					v, e := emit(n)
					if e != nil {
						return "", e
					}
					row = append(row, k+":"+v)
				}
			}
			cases = append(cases, "{"+strings.Join(row, ",")+"}")
		}
	}
	parts = append(parts, `"cases":[`+strings.Join(cases, ",")+"]")
	return "sha256:" + safefs.Digest([]byte("{"+strings.Join(parts, ",")+"}")), nil
}

// Reference assets are owned by the same fixed tool source as the generators.
func contractReferenceExample(s *kernel.SemanticSession, value map[string]any) error {
	tool, err := s.ToolSourceSession()
	if err != nil {
		return err
	}
	prefix := ".agents/skills/yss-ddd-scaffold-generator/assets/reference"
	refs, err := tool.Scan(prefix)
	if err != nil {
		return err
	}
	sort.Strings(refs)
	var bytes []byte
	for _, ref := range refs {
		content, e := tool.Bytes(ref)
		if e != nil {
			return e
		}
		bytes = append(bytes, []byte(strings.TrimPrefix(ref, prefix+"/"))...)
		bytes = append(bytes, 0)
		bytes = append(bytes, content...)
		bytes = append(bytes, 0)
	}
	wireRef := ".agents/skills/yss-dto/references/openapi-wire-profile.yaml"
	wire, err := tool.Bytes(wireRef)
	if err != nil {
		return err
	}
	bytes = append(bytes, []byte(wireRef)...)
	bytes = append(bytes, 0)
	bytes = append(bytes, wire...)
	bytes = append(bytes, 0)
	if len(refs) == 0 || kernel.Text(value["template_digest"]) != "sha256:"+safefs.Digest(bytes) {
		return s.Reject("SCAFFOLD_REFERENCE", "参考模板来源摘要变化，须重新编译合同")
	}
	return nil
}
