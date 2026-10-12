package governance

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
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
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func contractPlatformRecipe(profile, entry map[string]any) string {
	recipe := contractWithout(profile, "candidate_blockers", "recommended")
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
	return contractDigest(map[string]any{"profile": recipe, "entry": map[string]any{"id": entry["id"], "profile_id": entry["profile_id"], "spring_boot_version": entry["spring_boot_version"], "parent": entry["parent"], "bom": entry["bom"], "components": component, "platform_artifact_bindings": artifacts, "external_snapshot_bindings": snapshots, "external_artifact_bindings": external}})
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
	if family == "layered-mvc" {
		generator = append(generator, ".agents/skills/yss-ddd-scaffold-generator/assets/wrapper")
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
			key, _ := contractJSON(k)
			value, e := contractJSON(files[k])
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
	if !contractSame(binding, expected) {
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
		report, e := contractBoundDoc(s, ev)
		if e != nil {
			return nil, nil, nil, e
		}
		family := kernel.Text(report["architecture_family"])
		if kernel.Text(report["verification_scope"]) != "empty-scaffold" || kernel.Text(report["recipe_digest"]) != contractPlatformRecipe(profile, entry) || !contractSHA(report["generated_tree_digest"]) || kernel.Text(report["status"]) != "passed" || report["spring_boot_version"] != profile["spring_boot_version"] || report["java_version"] != profile["java_version"] || report["architecture_family"] != ev["architecture_family"] || !contractSame(report["parent"], entry["parent"]) || !contractSame(report["bom"], entry["bom"]) || kernel.Text(report["dependency_check"]) != "passed" || kernel.Text(report["startup_check"]) != "passed" || kernel.Text(kernel.SemMap(report["integration_tests"])["status"]) != "passed" {
			return nil, nil, nil, s.Reject("PLATFORM_EVIDENCE", "平台qualification身份或集成检查不完整")
		}
		fingerprint, e := contractPlatformFingerprint(s, family)
		if e != nil {
			return nil, nil, nil, e
		}
		if !contractSame(report["source_fingerprint"], fingerprint) {
			return nil, nil, nil, s.Reject("PLATFORM_SOURCE", "固定generator/verifier源码变化")
		}
		for _, phase := range []string{"validate", "test", "package"} {
			found := false
			for _, v := range kernel.SemList(report["commands"]) {
				row := kernel.SemMap(v)
				if kernel.Text(row["command"]) != "./mvnw "+phase {
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

func contractScaffoldUserDecision(s *kernel.SemanticSession, decision map[string]any) error {
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
	if _, e = kernel.AssertUserDecisionRequirementSemantic(s, requirement); e != nil {
		return e
	}
	subject, e := s.Doc(kernel.Text(requirement["subject_ref"]))
	if e != nil {
		return e
	}
	snapshot := contractWithout(decision, "user_confirmation", "status", "decision_inputs_digest")
	snapshot["platform_selection"] = map[string]any{"profile_id": profile["id"], "spring_boot_version": profile["spring_boot_version"], "java_version": profile["java_version"]}
	if !contractSame(subject, snapshot) {
		return s.Reject("user-decision-stale", "脚手架输入与用户所见快照不一致")
	}
	return nil
}

func contractExactVersion(v any) bool {
	return kernel.Text(v) != "" && !regexp.MustCompile(`(?i)[\[\](),*+]|\bx\b|latest`).MatchString(kernel.Text(v))
}

func contractSHA(v any) bool {
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
	if role == "" || kernel.Text(b["role"]) != role || status != "candidate" && !(resolved && status == "resolved") || kernel.Text(b["group_id"]) == "" || kernel.Text(b["artifact_id"]) == "" || !contractExactVersion(b["declared_version"]) || snapshot != strings.HasSuffix(kernel.Text(b["declared_version"]), "-SNAPSHOT") {
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
	} else {
		if snapshot || b["resolved_version"] != b["declared_version"] || !contractSHA(b["pom_sha256"]) || !contractSHA(b["jar_sha256"]) || !contractSHA(b["sources_jar_sha256"]) || b["published_at"] != nil && kernel.Text(b["published_at"]) == "" || b["source_tree"] != nil && !contractGitTree(b["source_tree"]) || !kernel.ApIsArray(b["evidence"]) || !kernel.ApIsArray(b["blockers"]) {
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
			report, e := contractBoundDoc(s, b)
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
			if !contractUnique(item["verified_architectures"], false) {
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
					if !contractSame(a["resolved_version"], r["resolved_version"]) || !contractSame(a["pom_sha256"], pom) || !contractSame(a["jar_sha256"], jar) || !contractSame(a["source_tree"], tree) {
						return nil, s.Reject("PLATFORM_COMPONENT", "组件绑定与解析证据漂移")
					}
				}
				if kernel.Text(item["status"]) == "verified" && (!contractExactVersion(a["resolved_version"]) || !contractSHA(a["pom_sha256"]) || !contractSHA(a["jar_sha256"]) || !contractGitTree(a["source_tree"]) || !strings.HasPrefix(kernel.Text(a["declared_version"]), strconv.Itoa(major)+".")) {
					return nil, s.Reject("PLATFORM_COMPONENT", "verified组件缺少精确代际证据")
				}
			}
			recipe := contractWithout(item, "capability_id", "component_digest", "status", "evidence", "blockers")
			recipe["capability_id"] = id
			if !contractSHA(item["component_digest"]) || kernel.Text(item["component_digest"]) != contractDigest(recipe) {
				return nil, s.Reject("PLATFORM_COMPONENT", "组件配方摘要变化")
			}
			if kernel.Text(item["status"]) == "verified" {
				if kernel.Text(entry["status"]) != "verified" || len(kernel.SemList(item["verified_architectures"])) == 0 || len(kernel.SemList(item["artifacts"])) == 0 || len(kernel.SemList(item["evidence"])) == 0 || len(kernel.SemList(item["blockers"])) != 0 {
					return nil, s.Reject("PLATFORM_COMPONENT", "verified组件与平台状态或证据冲突")
				}
				for _, v := range kernel.SemList(item["evidence"]) {
					ev := kernel.SemMap(v)
					if !kernel.ContractPath(kernel.Text(ev["ref"])) || !contractSHA(ev["digest"]) || !kernel.SemHas(item["verified_architectures"], kernel.Text(ev["architecture_family"])) {
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
				a := kernel.SemList(kernel.SemMap(components[id])["artifacts"])
				if len(a) != 1 {
					return nil, s.Reject("PLATFORM_COMPONENT", "Boot3组件坐标缺失")
				}
				b := kernel.SemMap(a[0])
				if kernel.Text(b["group_id"])+":"+kernel.Text(b["artifact_id"])+":"+kernel.Text(b["declared_version"]) != coordinate {
					return nil, s.Reject("PLATFORM_COMPONENT", "Boot3组件固定坐标变化")
				}
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
	registerSemanticValidator("slice", verifySliceSemantic)
	registerSemanticValidator("scaffold", verifyScaffoldSemantic)
	registerSemanticValidator("business-checkpoint", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		cp, e := s.Doc(ref)
		if e != nil {
			return e
		}
		return contractBusinessCheckpoint(s, cp, opts["required"] == "true")
	})
	registerSemanticValidator("frontend-delivery", verifyFrontendDeliverySemantic)
	registerSemanticValidator("openapi-validation", verifyOpenAPIValidationSemantic)
	registerSemanticValidator("scaffold-decisions", verifyScaffoldDecisionsSemantic)
	registerSemanticValidator("technical-completion", func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		doc, e := s.Doc(ref)
		if e != nil {
			return e
		}
		return contractTechnicalDesign(s, ref, doc, nil, opts)
	})
	registerSemanticValidator("repository-ready", verifyRepositoryReadySemantic)
	registerSemanticValidator("technical-transition", contractTechnicalTransition)
	registerSemanticValidator("service-transition", contractServiceTransition)
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
		if e = contractEvidence(s, result["evidence_refs"]); e != nil {
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
		if !contractSame(api, kernel.SemMap(persisted["api_contract_decision"])) || !kernel.SemHas(result["evidence_refs"], kernel.Text(api["ref"])) {
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
		if e = contractHandoffConsumption(s, consumed, map[string]string{"consumer": "tactical", "inputs-only": "true"}); e != nil {
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
	transitionOpts := semanticOptions(opts)
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
	state, e := s.TransitionState(ref, opts["task-ref"])
	if e != nil {
		return e
	}
	request := kernel.SemMap(state["service_project_initialization"])
	if e = contractRequired(s, request, "project_id", "contract_ref", "contract_digest", "project_root"); e != nil {
		return e
	}
	contractRef, e := contractVerificationRef(s, kernel.Text(request["contract_ref"]))
	if e != nil {
		return e
	}
	contract, e := contractBoundDoc(s, map[string]any{"ref": contractRef, "digest": request["contract_digest"]})
	if e != nil {
		return e
	}
	if e = s.Verify("scaffold", contractRef, opts); e != nil {
		return e
	}
	projectRoot := kernel.Text(request["project_root"])
	if !filepath.IsAbs(projectRoot) || filepath.Clean(projectRoot) != projectRoot || filepath.Dir(projectRoot) != kernel.Text(contract["target_output_dir"]) {
		return s.Reject("SERVICE_INITIALIZATION", "服务目标必须是批准输出目录内的规范绝对工程根")
	}
	if kernel.Text(contract["architecture_profile"]) != "mvc-data-analysis-v1" || kernel.Text(contract["scaffold_kind"]) != "backend-mvc-data-analysis" || kernel.Text(contract["generator_skill"]) != "yss-layered-mvc-scaffold-generator" || contract["init_git"] != true || contract["project_name"] != request["project_id"] || filepath.Join(kernel.Text(contract["target_output_dir"]), kernel.Text(contract["project_name"])) != projectRoot {
		return s.Reject("SERVICE_INITIALIZATION", "服务初始化合同身份或目标不一致")
	}
	handoffRef := path.Join(path.Dir(contractRef), kernel.Text(contract["context_handoff_ref"]))
	if !contractWithin(handoffRef, path.Dir(contractRef)) || handoffRef == path.Dir(contractRef) || filepath.IsAbs(kernel.Text(contract["context_handoff_ref"])) {
		return s.Reject("SERVICE_INITIALIZATION", "CONTEXT交接必须位于合同目录内部")
	}
	handoff, e := s.Bytes(handoffRef)
	if e != nil {
		return e
	}
	digest := "sha256:" + safefs.Digest(handoff)
	if kernel.Text(contract["context_handoff_digest"]) != digest || kernel.Text(request["context_handoff_digest"]) != digest {
		return s.Reject("SERVICE_INITIALIZATION", "CONTEXT交接摘要变化")
	}
	parent := filepath.Dir(projectRoot)
	if opts["completed"] != "true" {
		return s.ObservedExternalAbsence(projectRoot, contractRef)
	}
	if e = s.RegisterExternalRoot(parent, contractRef); e != nil {
		return e
	}
	child := kernel.NewSemanticSession(s.Ctx, parent, s.Args)
	child.V = s.ExternalViews[parent]
	s.Children = append(s.Children, child)
	name := filepath.Base(projectRoot)
	// Read the contract-owned files below individually. A recursive inventory
	// of the project would wrongly treat its independently required .git tree
	// as a governance asset and violate the safe reference policy.
	manifestRef, e := contractVerificationRef(s, kernel.Text(request["manifest_ref"]))
	if e != nil {
		return e
	}
	verificationRef, e := contractVerificationRef(s, kernel.Text(request["verification_ref"]))
	if e != nil {
		return e
	}
	resultRef, e := contractVerificationRef(s, kernel.Text(request["result_ref"]))
	if e != nil {
		return e
	}
	manifest, e := contractVerificationDoc(s, manifestRef)
	if e != nil {
		return e
	}
	verification, e := contractVerificationDoc(s, verificationRef)
	if e != nil {
		return e
	}
	result, e := contractVerificationDoc(s, resultRef)
	if e != nil {
		return e
	}
	if kernel.Text(result["result_schema"]) != "workflow-execution-result-v1" || kernel.Text(result["work_unit"]) != "work-unit.service-project-initialization" || kernel.Text(result["result"]) != "completed" || !kernel.SemHas(result["evidence_refs"], kernel.Text(request["manifest_ref"])) || !kernel.SemHas(result["evidence_refs"], kernel.Text(request["verification_ref"])) {
		return s.Reject("SERVICE_INITIALIZATION", "缺少持久化已完成初始化执行结果")
	}
	if kernel.ContractN(manifest["schema_version"]) != 4 || kernel.Text(manifest["kind"]) != "service-project-initialization" || kernel.Text(manifest["architecture_profile"]) != "mvc-data-analysis-v1" || manifest["project_name"] != request["project_id"] || manifest["contract_digest"] != request["contract_digest"] || kernel.Text(manifest["completion_level"]) != "empty-scaffold-verified" {
		return s.Reject("SERVICE_INITIALIZATION", "服务Manifest不是当前已验证空骨架")
	}
	if kernel.Text(verification["status"]) != "passed" || kernel.Text(verification["completion_level"]) != "empty-scaffold-verified" || kernel.Text(verification["project_root"]) != projectRoot {
		return s.Reject("SERVICE_INITIALIZATION", "服务验证身份或完成度错误")
	}
	for _, phase := range []string{"validate", "test", "package"} {
		found := false
		for _, v := range kernel.SemList(verification["commands"]) {
			row := kernel.SemMap(v)
			if kernel.Text(row["phase"]) != phase {
				continue
			}
			exit, ok := kernel.Integer(row["exit_code"])
			if !ok || exit != 0 || !strings.HasPrefix(kernel.Text(row["command"]), "./mvnw "+phase) {
				return s.Reject("SERVICE_INITIALIZATION", "Maven实际执行结果未通过")
			}
			for _, key := range []string{"stdout_ref", "stderr_ref"} {
				ref, e := contractVerificationRef(s, kernel.Text(row[key]))
				if e != nil {
					return e
				}
				if _, e = contractVerificationBytes(s, ref); e != nil {
					return e
				}
			}
			found = true
		}
		if !found {
			return s.Reject("SERVICE_INITIALIZATION", "缺少Maven阶段: "+phase)
		}
	}
	context, e := child.Bytes(path.Join(name, "CONTEXT.md"))
	if e != nil {
		return e
	}
	if string(context) != string(handoff) {
		return s.Reject("SERVICE_INITIALIZATION", "子项目CONTEXT与批准交接不一致")
	}
	if e = s.RegisterExternalRoot(projectRoot, contractRef); e != nil {
		return e
	}
	top, e := s.Git(projectRoot, "rev-parse", "--show-toplevel")
	if e != nil || filepath.Clean(strings.TrimSpace(string(top))) != projectRoot {
		return s.Reject("SERVICE_INITIALIZATION", "子工程Git身份缺失")
	}
	if _, e = child.Bytes("skillUtils/skills-lock.json"); e != nil {
		return e
	}
	initialization, e := child.Doc(path.Join(name, ".template-spec/process/service-initialization.json"))
	if e != nil {
		return e
	}
	if initialization["contract_id"] != contract["contract_id"] || kernel.Text(initialization["context_handoff_digest"]) != digest {
		return s.Reject("SERVICE_INITIALIZATION", "子工程初始化身份变化")
	}
	return nil
}

// These helpers preserve the old JS digest contract: UTF-16 key ordering,
// JSON.stringify scalars, unescaped Unicode, and no trailing newline.
func contractJSON(v any) (string, error) {
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

func contractDigest(v any) string {
	b, e := contractJSON(v)
	if e != nil {
		return ""
	}
	return "sha256:" + safefs.Digest([]byte(b))
}

func contractSame(a, b any) bool {
	x, e := contractJSON(a)
	y, f := contractJSON(b)
	return e == nil && f == nil && x == y
}

func contractCopy(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		out[k] = x
	}
	return out
}

func contractWithout(v map[string]any, keys ...string) map[string]any {
	out := contractCopy(v)
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

func contractRequired(s *kernel.SemanticSession, m map[string]any, keys ...string) error {
	for _, k := range keys {
		if v, ok := m[k]; !ok || v == nil || v == "" {
			return s.Reject("CONTRACT_INVALID", "缺少 "+k)
		}
	}
	return nil
}

func contractUnique(v any, nonempty bool) bool {
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

func contractWithin(candidate, parent string) bool {
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

func contractDate(v any) bool { _, e := time.Parse(time.RFC3339Nano, kernel.Text(v)); return e == nil }

func contractUnion(groups ...[]string) []string {
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
		observedRef, e = contractVerificationRef(s, ref)
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

func contractBoundDoc(s *kernel.SemanticSession, v any) (map[string]any, error) {
	m, e := contractBinding(s, v)
	if e != nil {
		return nil, e
	}
	ref := kernel.Text(m["ref"])
	if filepath.IsAbs(ref) {
		ref, e = contractVerificationRef(s, ref)
		if e != nil {
			return nil, e
		}
	}
	return s.Doc(ref)
}

func contractEvidence(s *kernel.SemanticSession, refs any) error {
	if !contractUnique(refs, true) {
		return s.Reject("EVIDENCE_REQUIRED", "证据引用须非空、唯一")
	}
	for _, ref := range kernel.SemStrings(refs) {
		if filepath.IsAbs(ref) {
			var e error
			ref, e = contractVerificationRef(s, ref)
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

type contractPlanEntry struct {
	ID, Kind string
	Fields   map[string]string
}

func contractPlan(s *kernel.SemanticSession, ref string) ([]contractPlanEntry, bool, error) {
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
	entries := []contractPlanEntry{}
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
			entries = append(entries, contractPlanEntry{fields["ID"], kind, fields})
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
		entries, supported, e := contractPlan(s, ref)
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

type nativeSlice struct {
	Raw, Normalized map[string]any
	Basis           map[string]map[string]any
	Ref             string
	Repositories    map[string]map[string]any
}

func loadNativeSlice(s *kernel.SemanticSession, ref string) (*nativeSlice, error) {
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
	out := &nativeSlice{Raw: raw, Normalized: contractCopy(raw), Ref: ref, Basis: map[string]map[string]any{}, Repositories: map[string]map[string]any{}}
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
			if !contractSame(u["contract_id"], raw["contract_id"]) || !contractSame(u["contract_version"], raw["contract_version"]) || kernel.Text(kernel.SemMap(u["work_unit"])["primary_skill"]) == "" {
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
	scope, resolution, extension, app := kernel.SemMap(raw["scope"]), contractCopy(kernel.SemMap(raw["resolution"])), kernel.SemMap(raw["extensions"]), kernel.SemMap(raw["applicability"])
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
		if !contractSame(app["checks"], expected) {
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
		if kernel.Text(kernel.SemMap(app["frontend"])["baseline_kind"]) == "existing-ui-baseline" {
			if kernel.Text(kernel.SemMap(app["frontend"])["ui_change"]) != "none" || kernel.SemHas(scope["impacted_areas"], "ui") {
				return nil, s.Reject("APPLICABILITY", "既有 UI 不能承接 UI 变化")
			}
			local, err := hasLocalImplementationInputs(s)
			if err != nil {
				return nil, err
			}
			keys := []string{"existing_ui_baseline", "frontend_delivery"}
			if local {
				keys = []string{"existing_ui_baseline"}
			}
			if e = contractNeedBasis(s, basis, keys...); e != nil {
				return nil, e
			}
			baseline, e := contractExistingUI(s, kernel.Text(basis["existing_ui_baseline"]["ref"]))
			if e != nil {
				return nil, e
			}
			ids := []string{}
			for _, v := range kernel.SemList(baseline["cases"]) {
				ids = append(ids, kernel.Text(kernel.SemMap(v)["case_id"]))
			}
			for _, id := range kernel.SemStrings(kernel.SemMap(extension["frontend"])["visual_baseline_case_ids"]) {
				if !kernel.SemHas(ids, id) {
					return nil, s.Reject("FRONTEND_BASELINE", "未登记既有 UI 用例")
				}
			}
		} else if e = contractNeedBasis(s, basis, "requirement_freeze", "low_fidelity_review", "prototype_review", "prototype_profile_decision", "prototype_deliverable", "prototype_deliverable_verification", "prototype_confirmation", "visual_baseline", "state_matrix"); e != nil {
			return nil, e
		}
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
			registered = registered || contractWithin(kernel.Text(check["cwd"]), root)
		}
		if !registered {
			return nil, s.Reject("VERIFICATION", "cwd 未登记: "+id)
		}
		for _, root := range kernel.SemStrings(check["dependency_roots"]) {
			if out.Repositories[root] == nil {
				return nil, s.Reject("REPOSITORY", "验证依赖根未登记")
			}
		}
		if !contractUnique(check["acceptance_refs"], true) {
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
		u := contractCopy(kernel.SemMap(v))
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
				within = within || contractWithin(p, parent)
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
		ids = contractUnion(ids, kernel.SemStrings(u["verification_refs"]))
		checks := []any{}
		commands, evidence := []string{}, []string{}
		for _, id := range ids {
			v := verification[id]
			if v == nil || !contractWithin(kernel.Text(kernel.SemMap(v)["cwd"]), root) {
				return nil, s.Reject("VERIFICATION", "未知验证或执行工程冲突")
			}
			c := contractCopy(kernel.SemMap(v))
			c["id"] = id
			checks = append(checks, c)
			commands = append(commands, kernel.Text(c["command"]))
			evidence = contractUnion(evidence, kernel.SemStrings(c["expected_evidence"]))
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
			if e = contractRequired(s, kernel.SemMap(u["controlled_generation"]), "exception_reason", "generator", "generator_inputs", "expected_files", "verification_commands", "behavior_tests_after_generation"); e != nil {
				return nil, e
			}
		} else if u["controlled_generation"] != nil {
			return nil, s.Reject("TDD", "行为测试不得携带受控生成例外")
		}
		u["allowed_write_paths"], u["project_root"], u["verification"], u["verification_commands"], u["expected_evidence"] = allowed, root, checks, commands, evidence
		u["supporting_skills"] = kernel.SemStrings(u["supporting_skills"])
		u["forbidden_patterns"] = contractUnion(kernel.SemStrings(scope["forbidden_patterns"]), kernel.SemStrings(u["forbidden_patterns"]))
		row := contractCopy(u)
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
		resolution["architecture_identity_digest"] = strings.TrimPrefix(contractDigest(resolution["architecture_identity"]), "sha256:")
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
	common := contractCopy(scope)
	common["required_capabilities"], common["required_skills"], common["quality_baseline_ref"] = resolution["required_capabilities"], resolution["required_skills"], basis["engineering_baseline"]["ref"]
	allCommands, allEvidence := []string{}, []string{}
	for _, v := range verification {
		c := kernel.SemMap(v)
		allCommands = append(allCommands, kernel.Text(c["command"]))
		allEvidence = contractUnion(allEvidence, kernel.SemStrings(c["expected_evidence"]))
	}
	common["verification_commands"], common["expected_evidence_files"] = allCommands, allEvidence
	out.Normalized["common"], out.Normalized["resolution"], out.Normalized["lifecycle_refs"], out.Normalized["work_units"] = common, resolution, refs, units
	out.Normalized["readiness"] = map[string]any{"blockers": []any{}, "stale_inputs": []any{}}
	for _, key := range []string{"backend", "frontend"} {
		m := contractCopy(kernel.SemMap(extension[key]))
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
	c, e := loadNativeSlice(s, ref)
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
			if err := contractSliceFresh(s, c, bound); err != nil {
				return err
			}
		}
		return nil
	}
	return contractSliceFresh(s, c, bound)
}

// A consumer selects the expected subject from its current asset or checkpoint.
// Approval contents may never supply their own expected subject, scope or author.
func contractApproval(s *kernel.SemanticSession, gate string, binding map[string]any, approvalRef string, opts map[string]string) error {
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
				state = contractCopy(g)
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
		state = contractCopy(state)
		state["review_package"] = value
	}
	expected, e := kernel.ApprovalExpectationFromState(s, gate, state)
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
	record, e = kernel.SelectApprovalRecordSemantic(s, record, gate)
	if e != nil {
		return e
	}
	if e = kernel.AssertCurrentApprovalSemantic(s, record, expected); e != nil {
		return e
	}
	if binding["id"] != nil || binding["version"] != nil {
		matched := false
		for _, v := range kernel.SemList(record["artifact_bindings"]) {
			m := kernel.SemMap(v)
			idMatches := binding["id"] == nil || contractSame(m["id"], binding["id"])
			versionMatches := binding["version"] == nil || contractSame(m["version"], binding["version"])
			matched = matched || idMatches && versionMatches && kernel.Text(m["digest"]) == kernel.Text(binding["digest"])
		}
		if !matched {
			return s.Reject("APPROVAL_CURRENT_INVALID", "批准未绑定当前资产身份/版本/摘要")
		}
	}
	return nil
}

func contractSliceApproval(s *kernel.SemanticSession, c *nativeSlice, opts map[string]string) error {
	return contractSliceApprovalBinding(s, c, opts, nil)
}

func contractSliceApprovalBinding(s *kernel.SemanticSession, c *nativeSlice, opts map[string]string, consumerBinding map[string]any) error {
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
		implementation = contractCopy(implementation)
		implementation["user_decisions"] = kernel.SemMap(cp["human_review"])["user_decisions"]
		if e = assertImplementationDecisionSemantic(s, implementation); e != nil {
			return e
		}
	} else if e = contractApproval(s, "gate.slice-contract-approved", b, approval, opts); e != nil {
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
				rows, e = kernel.ReviewBundleRowsSemantic(s, review)
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
			if e = contractApproval(s, "check.design-reviewed", b, reviewRef, reviewOpts); e != nil {
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

func contractSliceFresh(s *kernel.SemanticSession, c *nativeSlice, opts map[string]string) error {
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
	if kernel.Text(r["registry_digest"]) != strings.TrimPrefix(contractDigest(registry), "sha256:") || kernel.Text(r["compiler_contract_digest"]) != strings.TrimPrefix(contractDigest(compiler), "sha256:") {
		return s.Reject("CONTRACT_STALE", "Skill/Compiler 冻结摘要变化")
	}
	if !contractUnique(kernel.SemMap(n["common"])["allowed_write_paths"], true) {
		return s.Reject("PATH", "合同缺少写范围")
	}
	unit := opts["unit"]
	selectedContext := c
	if len(c.Repositories) == 0 {
		selectedContext, e = contractSelectLocalUnit(s, c, unit)
		if e != nil {
			return e
		}
		n = selectedContext.Normalized
		r = kernel.SemMap(n["resolution"])
	}
	if len(c.Repositories) > 0 {
		if unit == "" && len(kernel.SemList(n["work_units"])) == 1 {
			unit = kernel.Text(kernel.SemMap(kernel.SemList(n["work_units"])[0])["id"])
		}
		if unit == "" {
			return s.Reject("WORK_UNIT", "跨仓校验须显式工作单元")
		}
		found := false
		for _, v := range kernel.SemList(n["work_units"]) {
			u := kernel.SemMap(v)
			if kernel.Text(u["id"]) == unit {
				repo := c.Repositories[kernel.Text(u["project_root"])]
				if repo == nil {
					return s.Reject("REPOSITORY", "工作单元未登记")
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
				copyContext.Normalized = contractCopy(n)
				common := contractCopy(kernel.SemMap(n["common"]))
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
			return s.Reject("WORK_UNIT", "工作单元未唯一选择工程")
		}
	}
	identity := kernel.SemMap(r["architecture_identity"])
	if len(identity) > 0 {
		if kernel.Text(r["architecture_identity_digest"]) != strings.TrimPrefix(contractDigest(identity), "sha256:") {
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
		doc, e := contractBoundDoc(s, design)
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
	if kernel.Text(kernel.SemMap(n["frontend"])["status"]) == "required" {
		delivery := kernel.SemMap(r["frontend_delivery"])
		if len(delivery) == 0 {
			delivery = kernel.SemMap(kernel.SemMap(n["frontend"])["delivery"])
		}
		if len(delivery) == 0 {
			e = contractLocalFrontendInputs(s, selectedContext, opts)
		} else {
			e = contractFrontendDelivery(s, delivery, selectedContext, opts)
		}
		if e != nil {
			return e
		}
	}
	s.Report.ExecutionAuthorization = "not-evaluated"
	return nil
}

// A responsibility view is derived only after the entire source Slice has
// been normalized and approved. It never changes that source or its basis.
func contractSelectLocalUnit(s *kernel.SemanticSession, c *nativeSlice, unit string) (*nativeSlice, error) {
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
	selected.Normalized = contractCopy(c.Normalized)
	common := contractCopy(kernel.SemMap(c.Normalized["common"]))
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

func contractSliceRepositories(s *kernel.SemanticSession, c *nativeSlice) error {
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
		reg, e := contractBoundDoc(s, selected["implementation_repository"])
		if e != nil {
			return e
		}
		base, e := contractBoundDoc(s, selected["engineering_baseline"])
		if e != nil {
			return e
		}
		actual := filepath.Join(kernel.Text(reg["local_worktree"]), kernel.First(kernel.Text(reg["project_root"]), "."))
		if actual != root || kernel.Text(reg["status"]) != "current" || kernel.Text(base["status"]) != "current" || reg["repository_id"] != base["repository_id"] || reg["project_id"] != base["project_id"] || !contractUnique(reg["allowed_write_paths"], true) {
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
			if !contractSame(p["ref"], kernel.SemMap(selected["technical_design"])["ref"]) || !contractSame(p["digest"], kernel.SemMap(selected["technical_design"])["digest"]) {
				return s.Reject("REPOSITORY", "准备结果技术设计绑定冲突")
			}
		}
		identity := base["architecture_identity"]
		if identity == nil {
			identity = reg["architecture_identity"]
		}
		if base["architecture_identity"] != nil && reg["architecture_identity"] != nil && !contractSame(base["architecture_identity"], reg["architecture_identity"]) {
			return s.Reject("ARCHITECTURE", "逐仓架构身份冲突")
		}
		resolution := contractWithout(kernel.SemMap(c.Raw["resolution"]), "architecture_identity", "architecture_identity_digest")
		if identity != nil {
			resolution["architecture_identity"], resolution["architecture_identity_digest"] = identity, strings.TrimPrefix(contractDigest(identity), "sha256:")
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
	if !semSameSet(kernel.SemMap(c.Raw["scope"])["project_roots"], mapKeysAsAny(c.Repositories)) {
		return s.Reject("REPOSITORY", "逐仓绑定未覆盖全部工程根")
	}
	for _, key := range []string{"delivery_order", "rollback_order"} {
		if !semSameSet(x[key], mapKeysAsAny(c.Repositories)) {
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
				within = within || contractWithin(p, parent)
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
	registration, e := contractBoundDoc(s, basis["repository_registration"])
	if e != nil {
		return nil, e
	}
	keys := []string{"engineering_baseline", "repository_registration", "manifest"}
	if kernel.ContractN(kernel.SemMap(identity)["schema_version"]) != 2 {
		out := map[string]any{}
		for _, key := range keys {
			doc, e := contractBoundDoc(s, basis[key])
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

func mapKeysAsAny(v map[string]map[string]any) []string {
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
		acs = contractUnion(acs, kernel.SemStrings(b["acceptance_refs"]))
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
		covered = contractUnion(covered, []string{kernel.Text(a["locator"])})
	}
	if !semSameSet(ticketAC, covered) {
		return s.Reject("BUSINESS_SLICE_INVALID", "Slice 未完整覆盖实现票验收")
	}
	return nil
}

func contractBusinessTickets(s *kernel.SemanticSession, ref string) (map[string]any, error) {
	return contractBusinessTicketsMode(s, ref, "formal")
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

func contractBusinessTicketsMode(s *kernel.SemanticSession, ref, mode string) (map[string]any, error) {
	if mode != "draft" && mode != "formal" {
		return nil, s.Reject("BUSINESS_MODE_INVALID", "业务票模式必须为 draft 或 formal")
	}
	set, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/business-tickets-v1.schema.json", set); e != nil {
		return nil, e
	}
	if kernel.Text(set["kind"]) != "business-ticket-set" || !kernel.SemHas([]string{"draft", "ready-for-human"}, kernel.Text(set["status"])) || mode == "formal" && kernel.Text(set["status"]) != "ready-for-human" {
		return nil, s.Reject("BUSINESS_TICKETS", "业务票集合不是正式待审资产")
	}
	spec, e := contractBoundDoc(s, set["spec"])
	if e != nil {
		return nil, e
	}
	_ = spec
	entries, supported, e := contractPlan(s, kernel.Text(kernel.SemMap(set["spec"])["ref"]))
	if e != nil {
		return nil, e
	}
	if !supported {
		return nil, s.Reject("BUSINESS_TICKETS", "Spec 未声明 plan-spec-v1")
	}
	ids := map[string]contractPlanEntry{}
	hasFR, hasAC := false, false
	for _, entry := range entries {
		if entry.Kind != "FR" && entry.Kind != "NFR" && entry.Kind != "AC" {
			continue
		}
		if _, ok := ids[entry.ID]; ok {
			return nil, s.Reject("BUSINESS_TICKETS", "Spec ID 不唯一")
		}
		ids[entry.ID] = entry
		hasFR = hasFR || entry.Kind == "FR"
		hasAC = hasAC || entry.Kind == "AC"
	}
	if !hasFR || !hasAC {
		return nil, s.Reject("BUSINESS_TICKETS", "Spec 缺少可机械读取的 FR/AC")
	}
	docs, covered := map[string]any{}, map[string]bool{}
	reportTickets := []any{}
	seenPaths := map[string]bool{}
	dependencies := map[string][]string{}
	for _, v := range kernel.SemList(set["tickets"]) {
		binding := kernel.SemMap(v)
		ticket, e := contractBoundDoc(s, binding)
		if e != nil {
			return nil, e
		}
		if e = s.ValidateSchema(".template-spec/process/schemas/business-tickets-v1.schema.json", ticket); e != nil {
			return nil, e
		}
		id := kernel.Text(ticket["id"])
		if id == "" {
			id = kernel.Text(ticket["ticket_id"])
		}
		if docs[id] != nil || seenPaths[kernel.Text(binding["ref"])] || id != kernel.Text(binding["id"]) || kernel.Text(ticket["kind"]) != "business-ticket" || !kernel.SemHas([]string{"draft", "ready-for-human"}, kernel.Text(ticket["status"])) || mode == "formal" && kernel.Text(ticket["status"]) != "ready-for-human" || ticket["version"] != binding["version"] {
			return nil, s.Reject("BUSINESS_TICKETS", "业务票身份、状态或版本非法")
		}
		if !contractSame(ticket["spec"], set["spec"]) {
			return nil, s.Reject("BUSINESS_TICKETS", "业务票 Spec 绑定冲突")
		}
		ticketRef := kernel.Text(binding["ref"])
		seenPaths[ticketRef] = true
		if !strings.HasPrefix(ticketRef, path.Join(path.Dir(ref), "business-tickets")+"/") || !strings.HasSuffix(ticketRef, ".md") {
			return nil, s.Reject("BUSINESS_TICKETS", "业务票不在集合相邻目录")
		}
		raw, e := s.Bytes(ticketRef)
		if e != nil {
			return nil, e
		}
		body := string(raw)
		for _, heading := range []string{"业务结果", "范围", "非目标", "验收", "风险"} {
			if !regexp.MustCompile(`(?m)^##\s+` + heading + `\s*$`).MatchString(body) {
				return nil, s.Reject("BUSINESS_TICKETS", "业务票缺少正文: "+heading)
			}
		}
		if !contractUnique(ticket["requirement_refs"], true) || !contractUnique(ticket["acceptance_refs"], true) {
			return nil, s.Reject("BUSINESS_TICKETS", "业务票需求/验收引用无效")
		}
		for _, id := range kernel.SemStrings(ticket["requirement_refs"]) {
			if _, ok := ids[id]; !ok {
				return nil, s.Reject("BUSINESS_TICKETS", "未知 Spec ID: "+id)
			}
			if !kernel.SemHas([]string{"FR", "NFR"}, ids[id].Kind) {
				return nil, s.Reject("BUSINESS_TICKETS", "需求引用必须为 FR/NFR")
			}
			covered[id] = true
		}
		for _, id := range kernel.SemStrings(ticket["acceptance_refs"]) {
			entry := ids[id]
			if entry.Kind != "AC" {
				return nil, s.Reject("BUSINESS_TICKETS", "验收引用必须为 AC")
			}
			covered[id] = true
			requirements := regexp.MustCompile(`\b(?:FR|NFR)-[A-Za-z0-9][A-Za-z0-9._-]*\b`).FindAllString(entry.Fields["需求引用"], -1)
			if len(requirements) == 0 {
				return nil, s.Reject("BUSINESS_TICKETS", "验收缺少原始需求引用")
			}
			for _, requirement := range requirements {
				if !kernel.SemHas(ticket["requirement_refs"], requirement) {
					return nil, s.Reject("BUSINESS_TICKETS", "业务验收缺少需求覆盖")
				}
			}
		}
		for _, v := range kernel.SemList(ticket["open_questions"]) {
			q := kernel.SemMap(v)
			if e = contractRequired(s, q, "id", "question", "owner", "resolve_by"); e != nil {
				return nil, e
			}
			if _, ok := q["blocking"].(bool); !ok {
				return nil, s.Reject("BUSINESS_TICKETS", "未决项缺少布尔 blocking")
			}
			if mode == "formal" && q["blocking"] == true {
				return nil, s.Reject("BUSINESS_TICKETS", "正式业务票仍有阻断问题")
			}
		}
		for _, v := range kernel.SemList(ticket["source_refs"]) {
			b, e := contractBinding(s, v)
			if e != nil {
				return nil, e
			}
			if kernel.Text(b["locator"]) != "" {
				if e = contractBusinessLocator(s, b); e != nil {
					return nil, e
				}
			}
		}
		dependencies[id] = kernel.SemStrings(ticket["dependencies"])
		docs[id] = ticket
		reportTickets = append(reportTickets, map[string]any{"id": id, "ref": ticketRef, "version": ticket["version"], "source_digest": binding["digest"], "source_refs": ticket["source_refs"], "requirement_refs": ticket["requirement_refs"], "acceptance_refs": ticket["acceptance_refs"], "dependencies": ticket["dependencies"]})
	}
	active, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if active[id] {
			return s.Reject("BUSINESS_TICKETS", "业务票依赖循环")
		}
		if done[id] {
			return nil
		}
		active[id] = true
		for _, dep := range dependencies[id] {
			if docs[dep] == nil {
				return s.Reject("BUSINESS_TICKETS", "未知业务票依赖")
			}
			if e := visit(dep); e != nil {
				return e
			}
		}
		delete(active, id)
		done[id] = true
		return nil
	}
	for id := range docs {
		if e = visit(id); e != nil {
			return nil, e
		}
	}
	deferred := map[string]bool{}
	for _, v := range kernel.SemList(set["coverage_deferred"]) {
		d := kernel.SemMap(v)
		id := kernel.First(kernel.Text(d["source_id"]), kernel.Text(d["id"]))
		if _, ok := ids[id]; !ok || covered[id] || deferred[id] {
			return nil, s.Reject("BUSINESS_TICKETS", "延期覆盖非法")
		}
		if e = contractRequired(s, d, "reason", "risk", "owner", "target_version", "followup_ticket_ref", "verification_plan"); e != nil {
			return nil, e
		}
		if mode == "formal" {
			if _, e = contractBinding(s, d["decision"]); e != nil {
				return nil, e
			}
		}
		deferred[id] = true
	}
	for id := range ids {
		if !covered[id] && !deferred[id] {
			return nil, s.Reject("BUSINESS_TICKETS", "Spec 未被覆盖: "+id)
		}
	}
	if mode == "formal" {
		reviewRef := kernel.First(kernel.Text(kernel.SemMap(set["review"])["ref"]), kernel.Text(set["review_ref"]))
		review, e := s.Doc(reviewRef)
		if e != nil {
			return nil, e
		}
		if kernel.ContractN(review["schema_version"]) != 1 || kernel.Text(review["kind"]) != "business-ticket-review" || kernel.Text(review["result"]) != "passed" || kernel.Text(review["reviewer"]) == "" || kernel.Text(review["drafter"]) == "" || kernel.Text(review["reviewer"]) == kernel.Text(review["drafter"]) {
			return nil, s.Reject("BUSINESS_TICKETS", "业务票独立审查未通过")
		}
		bound, e := s.Bind(ref)
		if e != nil {
			return nil, e
		}
		if kernel.Text(review["subject_ref"]) != ref || kernel.Text(review["subject_digest"]) != bound.Digest {
			return nil, s.Reject("BUSINESS_TICKETS", "业务票独立审查未绑定当前集合")
		}
		if e = s.Basis(review["evidence"]); e != nil {
			return nil, e
		}
		if e = s.ValidateSchema(".template-spec/process/schemas/business-tickets-v1.schema.json", review); e != nil {
			return nil, e
		}
	}
	out := contractCopy(set)
	out["ticket_documents"] = docs
	out["tickets"] = reportTickets
	return out, nil
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
		set, e = contractBusinessTicketsMode(s, ref, "draft")
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
	if e = contractEvidence(s, prep["evidence_refs"]); e != nil {
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
			if e = contractRequired(s, p, "project_id", "repository_ref", "project_root", "repository_scope"); e != nil {
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
				if kernel.Text(c["status"]) != "approved" || c["persisted"] != true || c["current_version"] != true || kernel.ContractN(c["schema_version"]) != 4 {
					return s.Reject("SCAFFOLD_CONTRACT", "非当前批准的 v4 脚手架合同")
				}
				manifest, e := s.Doc(kernel.Text(p["scaffold_manifest_ref"]))
				if e != nil {
					return e
				}
				if role == "backend" && (kernel.ContractN(manifest["schema_version"]) != 4 || kernel.Text(manifest["completion_level"]) != "empty-scaffold-verified" || !contractSame(manifest["design_prerequisites"], p["design_prerequisites"])) {
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
			if e = contractScaffoldUserDecision(s, decision); e != nil {
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

func contractArchitecture(s *kernel.SemanticSession, identity map[string]any, c *nativeSlice, registry map[string]any, opts map[string]string) error {
	if kernel.ContractN(identity["schema_version"]) == 2 {
		return contractExistingArchitecture(s, identity, c, registry, opts)
	}
	if identity["schema_version"] != nil || identity["source_kind"] != nil {
		return s.Reject("ARCH_SOURCE_UNSUPPORTED", "未知架构身份来源")
	}
	profile := kernel.SemMap(kernel.SemMap(registry["architecture_profiles"])[kernel.Text(identity["architecture_profile"])])
	if len(profile) == 0 || profile["architecture_family"] != identity["architecture_family"] || profile["generator_skill"] != identity["generator_skill"] || kernel.Text(identity["verification_database"]) != "h2" || kernel.Text(identity["production_database"]) != "not-bound" || !contractUnique(identity["requested_capabilities"], false) {
		return s.Reject("ARCH_IDENTITY_SHAPE", "脚手架架构/Profile/验证数据库不一致")
	}
	modules := kernel.SemStrings(profile["core_modules"])
	for _, capability := range kernel.SemStrings(identity["requested_capabilities"]) {
		add := kernel.SemMap(profile["capability_modules"])[capability]
		if add == nil {
			return s.Reject("ARCH_PROFILE_UNSUPPORTED", "未知架构能力")
		}
		modules = contractUnion(modules, kernel.SemStrings(add))
	}
	if !semSameSet(modules, identity["resolved_modules"]) || !kernel.IsSHA256(strings.TrimPrefix(kernel.Text(identity["contract_digest"]), "sha256:")) {
		return s.Reject("ARCH_IDENTITY_SHAPE", "模块闭包或合同摘要非法")
	}
	if c != nil {
		for _, key := range []string{"engineering_baseline", "repository_registration", "manifest"} {
			basis := c.Basis[key]
			if basis == nil {
				return s.Reject("ARCH_EVIDENCE_REF", "缺少架构证据: "+key)
			}
			doc, e := contractBoundDoc(s, basis)
			if e != nil {
				return e
			}
			if !contractSame(identity, doc["architecture_identity"]) {
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

func contractExistingArchitecture(s *kernel.SemanticSession, identity map[string]any, c *nativeSlice, registry map[string]any, opts map[string]string) error {
	if c == nil {
		return s.Reject("ARCH_EVIDENCE_REF", "既有架构须由冻结依据消费")
	}
	if kernel.Text(identity["source_kind"]) != "existing-registration" {
		return s.Reject("ARCH_SOURCE_UNSUPPORTED", "未知既有架构来源")
	}
	fields := []string{"schema_version", "source_kind", "architecture_family", "architecture_profile", "repository_id", "project_id", "source_digest", "build_units_digest"}
	if e := contractRequired(s, identity, fields...); e != nil {
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
	base, e := contractBoundDoc(s, c.Basis["engineering_baseline"])
	if e != nil {
		return e
	}
	reg, e := contractBoundDoc(s, c.Basis["repository_registration"])
	if e != nil {
		return e
	}
	manifest, e := contractBoundDoc(s, c.Basis["manifest"])
	if e != nil {
		return e
	}
	for key, doc := range map[string]map[string]any{"baseline": base, "registration": reg, "manifest": manifest} {
		if !contractSame(identity, doc["architecture_identity"]) || doc["repository_id"] != identity["repository_id"] || doc["project_id"] != identity["project_id"] {
			return s.Reject("ARCH_IDENTITY_CONFLICT", key+" 三方身份冲突")
		}
	}
	if kernel.Text(base["kind"]) != "existing-engineering-baseline" || kernel.ContractN(base["schema_version"]) != 1 || kernel.Text(manifest["kind"]) != "existing-project-observation" || kernel.ContractN(manifest["schema_version"]) != 1 || kernel.Text(base["status"]) != "current" || kernel.Text(reg["status"]) != "current" {
		return s.Reject("ARCH_EVIDENCE_STALE", "工程基线/观测来源或状态非法")
	}
	if e = contractRequired(s, reg, "local_worktree", "project_root", "owner", "repository_url"); e != nil {
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
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(kernel.Text(source["base_commit"])) || !contractUnique(source["roots"], true) || len(kernel.SemList(source["files"])) == 0 || !contractSame(source, base["source"]) || contractDigest(source) != kernel.Text(identity["source_digest"]) {
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
			allowed, err := candidateIntentAllowlist(s, repoRoot, c.Ref)
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
			beneath = beneath || contractWithin(ref, scope)
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
	intentAllowed, err := candidateIntentAllowlist(s, repoRoot, c.Ref)
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
	if len(units) == 0 || !contractSame(units, base["build_units"]) || contractDigest(units) != kernel.Text(identity["build_units_digest"]) {
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
				deps = contractUnion(deps, []string{artifactMap[artifact]})
			}
		}
		if !contractSame(contractSorted(deps), contractSorted(kernel.SemStrings(u["depends_on"]))) {
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
					frozen = frozen || contractWithin(p, ref)
				}
				for _, scope := range kernel.SemStrings(base["boundary_scope"]) {
					boundary = boundary || contractWithin(ref, scope)
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
	if !contractUnique(reg["allowed_write_paths"], true) || !contractUnique(base["boundary_scope"], true) || !contractUnique(base["verification_commands"], true) || !contractSame(base["verification_commands"], reg["verification_commands"]) {
		return s.Reject("ARCH_REGISTRATION_MISSING", "写范围/业务边界/验证入口无效")
	}
	review, e := contractBoundDoc(s, base["boundary_review"])
	if e != nil {
		return e
	}
	gate := kernel.Text(review["gate_id"])
	if !kernel.SemHas([]string{"check.architecture-reviewed", "gate.technical-design-approved"}, gate) {
		return s.Reject("ARCH_REVIEW_MISSING", "边界审查门禁无效")
	}
	if e = contractApproval(s, gate, c.Basis["manifest"], kernel.Text(kernel.SemMap(base["boundary_review"])["ref"]), opts); e != nil {
		return e
	}
	found := false
	for _, v := range kernel.SemList(review["artifact_bindings"]) {
		b := kernel.SemMap(v)
		found = found || b["id"] == base["id"] && b["version"] == base["version"] && kernel.Text(b["digest"]) == contractDigest(contractWithout(base, "boundary_review"))
	}
	if !found || kernel.Text(base["author"]) == "" || kernel.Text(review["principal_ref"]) == kernel.Text(base["author"]) {
		return s.Reject("ARCH_REVIEW_STALE", "边界审查未独立绑定基线")
	}
	if e = contractEvidence(s, review["evidence_refs"]); e != nil {
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
			report, e := contractBoundDoc(s, db["verification"])
			if e != nil {
				return e
			}
			for _, k := range []string{"engine", "version", "environment_id"} {
				if kernel.Text(db[k]) == "" || !contractSame(db[k], report[k]) {
					return s.Reject("ARCH_DATABASE_STALE", "数据库环境/版本不一致")
				}
			}
			if report["source_digest"] != identity["source_digest"] {
				return s.Reject("ARCH_DATABASE_STALE", "数据库验证源码变化")
			}
			if e = contractRecordedResults(s, report["results"], true); e != nil {
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
			allowed = allowed || contractWithin(ref, p)
		}
		for _, p := range kernel.SemStrings(reg["allowed_write_paths"]) {
			registered = registered || contractWithin(ref, p)
		}
		if !allowed || !registered {
			return s.Reject("ARCH_SOURCE_OUT_OF_SCOPE", "当前源码增量超出已批准范围")
		}
	}
	return nil
}

func contractSorted(a []string) []string { x := append([]string(nil), a...); sort.Strings(x); return x }

func contractRecordedResults(s *kernel.SemanticSession, v any, bound bool) error {
	rows := kernel.SemList(v)
	if len(rows) == 0 {
		return s.Reject("VERIFICATION_REQUIRED", "缺少真实验证记录")
	}
	for _, v := range rows {
		r := kernel.SemMap(v)
		exit, ok := kernel.Integer(r["exit_code"])
		if kernel.Text(r["command"]) == "" || !ok || exit != 0 || !contractDate(r["executed_at"]) {
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
		contextKey := contractDigest(map[string]any{"platform_configuration": binding["platform_configuration"], "architecture_family": binding["architecture_family"]})
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
		if len(currentEvidence) == 0 || kernel.Text(binding["component_digest"]) != kernel.Text(item["component_digest"]) || !contractSame(binding["artifacts"], item["artifacts"]) || !contractSame(binding["evidence"], currentEvidence) || !contractSame(binding["verified_architectures"], item["verified_architectures"]) || kernel.Text(binding["status"]) != kernel.Text(item["status"]) {
			return s.Reject("COMPONENT_CAPABILITY_STALE", "组件冻结绑定变化")
		}
		currentBindings = append(currentBindings, map[string]any{"capability_id": id, "status": item["status"], "verified_architectures": item["verified_architectures"], "artifacts": item["artifacts"], "evidence": currentEvidence, "component_digest": item["component_digest"], "architecture_family": binding["architecture_family"], "platform_configuration": binding["platform_configuration"]})
		for _, v := range kernel.SemList(binding["evidence"]) {
			ev := kernel.SemMap(v)
			doc, e := contractBoundDoc(s, ev)
			if e != nil {
				return e
			}
			if kernel.ContractN(doc["schema_version"]) != 1 || kernel.Text(doc["kind"]) != "backend-component-capability-evidence" || kernel.Text(doc["status"]) != "passed" || doc["capability_id"] != binding["capability_id"] || doc["component_digest"] != binding["component_digest"] || doc["architecture_family"] != binding["architecture_family"] || doc["compatibility_id"] != compat["id"] || doc["profile_id"] != profile["id"] || doc["spring_boot_version"] != profile["spring_boot_version"] || !contractSame(doc["artifacts"], binding["artifacts"]) {
				return s.Reject("COMPONENT_CAPABILITY_STALE", "组件真实证据来源不一致")
			}
		}
	}
	for _, id := range required {
		if !seen[id] {
			return s.Reject("COMPONENT_CAPABILITY_MISSING", "所需组件未冻结")
		}
	}
	if len(frozen) > 0 && (!contractSame(frozen, currentBindings) || kernel.Text(resolution["component_bindings_digest"]) != strings.TrimPrefix(contractDigest(currentBindings), "sha256:")) {
		return s.Reject("COMPONENT_CAPABILITY_STALE", "冻结组件闭包摘要变化")
	}
	return nil
}

func contractDesignTargets(s *kernel.SemanticSession, data map[string]any) (map[string]map[string]any, map[string]map[string]any, error) {
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

func contractTraceRow(s *kernel.SemanticSession, row, source map[string]any, ids, seams map[string]map[string]any, slice string) error {
	switch kernel.Text(row["disposition"]) {
	case "implemented":
		if !contractUnique(row["tactical_refs"], true) || !contractUnique(row["test_seam_refs"], true) {
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
		return contractEvidence(s, row["evidence_refs"])
	case "not-applicable":
		if kernel.Text(row["reason"]) == "" {
			return s.Reject("TECHNICAL_TRACEABILITY", "不适用缺少理由")
		}
		return contractEvidence(s, row["evidence_refs"])
	case "deferred":
		if e := contractRequired(s, row, "reason", "risk", "owner", "followup_ticket_ref", "verification_plan", "target_version"); e != nil {
			return e
		}
		fallthrough
	case "pending", "conflict":
		if slice == "" || kernel.Text(row["dependency_status"]) != "known" || !contractUnique(row["dependent_slice_refs"], true) || kernel.SemHas(row["dependent_slice_refs"], slice) {
			return s.Reject("TECHNICAL_TRACEABILITY", "未闭合承接阻断当前切片")
		}
		return nil
	default:
		return s.Reject("TECHNICAL_TRACEABILITY", "未知承接状态")
	}
}

func contractTechnicalDesign(s *kernel.SemanticSession, ref string, doc map[string]any, c *nativeSlice, opts map[string]string) error {
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
			return contractHandoffConsumption(s, doc, map[string]string{"consumer": "tactical", "slice": opts["slice"]})
		}
		s.Report.Checks = append(s.Report.Checks, kernel.SemanticCheck{ID: "legacy-ddd-read-only", SourceRef: ref, Status: "passed"})
		return nil
	}
	if e := s.ValidateSchema(".agents/skills/yss-technical-design/references/technical-design.schema.json", doc); e != nil {
		return e
	}
	if kernel.Text(doc["digest"]) != contractDigest(contractWithout(doc, "digest")) {
		return s.Reject("TECHNICAL_DESIGN_STALE", "技术设计摘要不一致")
	}
	if kernel.SemHas([]string{"stale", "blocked", "drift", "new_impacts"}, kernel.Text(doc["status"])) || c != nil && kernel.Text(doc["status"]) != "approved" {
		return s.Reject("TECHNICAL_DESIGN_STALE", "技术设计状态不能消费")
	}
	architecture := kernel.SemMap(doc["architecture"])
	binding := map[string]any{"ref": architecture["decision_ref"], "digest": architecture["decision_digest"]}
	record, e := contractBoundDoc(s, binding)
	if e != nil {
		return e
	}
	if kernel.Text(architecture["source_kind"]) == "scaffold-decision" {
		decision := kernel.ApFind(record["decisions"], "project_id", kernel.Text(architecture["project_id"]))
		if record["template"] != false || kernel.Text(record["status"]) != "current" || !kernel.SemHas([]string{"user-confirmed", "lifecycle-approved", "consumed"}, kernel.Text(decision["status"])) || decision["confirmed_architecture"] != architecture["family"] {
			return s.Reject("TECHNICAL_ARCHITECTURE", "项目架构未正式确认")
		}
		if e = contractScaffoldUserDecision(s, decision); e != nil {
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
				architectureContext = &nativeSlice{Raw: map[string]any{}, Normalized: map[string]any{"common": map[string]any{"allowed_write_paths": []any{}}}, Basis: basis}
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
		if e = contractEvidence(s, doc["evidence_refs"]); e != nil {
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
	ids, seams, e := contractDesignTargets(s, doc)
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
		if e = contractTraceRow(s, rows[id], source, ids, seams, slice); e != nil {
			return e
		}
	}
	if doc["strategic_handoff"] != nil {
		return contractHandoffConsumption(s, doc, map[string]string{"consumer": "tactical", "slice": slice})
	}
	return nil
}

func contractEngineeringDesign(s *kernel.SemanticSession, data, registration map[string]any) error {
	identity := kernel.SemMap(registration["architecture_identity"])
	if kernel.ContractN(identity["schema_version"]) != 2 || kernel.Text(identity["source_kind"]) != "existing-registration" {
		return s.Reject("ENGINEERING_DESIGN_INVALID", "工程分支必须消费既有工程身份 v2")
	}
	binding := kernel.SemMap(kernel.SemMap(registration["architecture_evidence"])["engineering_baseline"])
	baseline, e := contractBoundDoc(s, binding)
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
				allowed = allowed || contractWithin(ref, scope)
			}
			if !allowed {
				return s.Reject("ENGINEERING_DESIGN_INVALID", "工程写范围超出登记")
			}
			for _, scope := range kernel.SemStrings(baseline["boundary_scope"]) {
				if contractWithin(ref, scope) || contractWithin(scope, ref) {
					return s.Reject("ENGINEERING_DESIGN_INVALID", "工程分支触碰已登记业务边界")
				}
			}
			unitRoot := path.Dir(kernel.Text(unit["pom_ref"]))
			if unitRoot != "." && !contractWithin(ref, unitRoot) {
				return s.Reject("ENGINEERING_DESIGN_INVALID", "工程写范围与构建单元冲突")
			}
			paths = contractUnion(paths, []string{ref})
		}
	}
	if !semSameSet(d["allowed_write_paths"], paths) || len(kernel.SemList(data["source_items"])) == 0 {
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
	if e := contractRequired(s, d, "schema_version", "tactical_design_id", "tactical_version", "status", "context_ref", "aggregate_catalog", "entity_catalog", "value_object_catalog", "behavior_catalog", "invariant_catalog", "state_transition_catalog", "consistency_policy", "domain_event_catalog", "gateway_catalog", "persistence_mapping", "test_seams", "adr_candidates", "upstream_impact", "version", "digest", "evidence_refs"); e != nil {
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
		if e := contractRequired(s, m, "value_object_id", "name", "aggregate_id", "immutable"); e != nil {
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

func verifyScaffoldSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	bytes, e := s.Bytes(ref)
	if e != nil {
		return e
	}
	if !json.Valid(bytes) {
		return s.Reject("SCAFFOLD_FORMAT", "脚手架合同必须为 JSON 对象")
	}
	doc, e := s.Doc(ref)
	if e != nil {
		return e
	}
	if e = s.ValidateSchema(".template-spec/process/schemas/project-scaffold-contract.schema.json", doc); e != nil {
		return e
	}
	if kernel.ContractN(doc["schema_version"]) != 4 || kernel.Text(doc["status"]) != "approved" || doc["current_version"] != true || kernel.Text(doc["persisted_ref"]) == "" {
		return s.Reject("SCAFFOLD_CONTRACT", "脚手架合同必须已批准、持久化且当前")
	}
	policy := kernel.SemMap(doc["generation_policy"])
	for key, value := range map[string]string{"mode": "initialize-only", "existing_target": "unsupported", "old_project_migration": "unsupported", "template_upgrade": "unsupported"} {
		if kernel.Text(policy[key]) != value {
			return s.Reject("SCAFFOLD_POLICY", "不支持的生成政策")
		}
	}
	if !contractUnique(doc["allowed_write_paths"], true) || !contractUnique(doc["verification_commands"], true) || !kernel.SemHas(doc["expected_evidence_files"], ".yss/scaffold-generation.json") {
		return s.Reject("SCAFFOLD_CONTRACT", "写范围/固定验证/Manifest 证据缺失")
	}
	if kernel.Text(doc["delivery_role"]) == "frontend" {
		boundOpts := semanticOptions(opts)
		boundOpts["contract-ref"] = ref
		return contractFrontendScaffold(s, doc, boundOpts)
	}
	work := kernel.SemMap(doc["work_unit"])
	if work["primary_skill"] != doc["generator_skill"] || kernel.Text(work["tdd_mode"]) != "controlled-generation" || work["controlled_generation"] != true || !contractSame(work["allowed_write_paths"], doc["allowed_write_paths"]) || !contractSame(work["verification_commands"], doc["verification_commands"]) {
		return s.Reject("SCAFFOLD_WORK_UNIT", "受控生成工作单元与合同冲突")
	}
	if kernel.Text(doc["delivery_role"]) != "backend" || kernel.Text(doc["scaffold_status"]) != "required" {
		return s.Reject("SCAFFOLD_CONTRACT", "后端脚手架身份无效")
	}
	family, skill := kernel.Text(doc["architecture_family"]), kernel.Text(doc["generator_skill"])
	if family == "domain-driven" && skill != "yss-ddd-scaffold-generator" || family == "layered-mvc" && skill != "yss-layered-mvc-scaffold-generator" || !kernel.SemHas([]string{"domain-driven", "layered-mvc"}, family) {
		return s.Reject("SCAFFOLD_ARCHITECTURE", "生成器和架构族冲突")
	}
	if !contractSame(doc["verification_commands"], []string{"./mvnw validate", "./mvnw test", "./mvnw package"}) {
		return s.Reject("SCAFFOLD_VERIFICATION", "Maven 验证入口须为固定三条命令")
	}
	approval := kernel.SemMap(doc["approval"])
	if approval["approval_ref"] != doc["lifecycle_approval_ref"] || approval["persisted_ref"] != doc["persisted_ref"] || approval["current_version"] != doc["contract_version"] {
		return s.Reject("SCAFFOLD_APPROVAL", "批准指针或版本冲突")
	}
	coordinates := kernel.SemMap(doc["maven_coordinates"])
	values := []any{coordinates["group_id"], coordinates["project_version"], kernel.SemMap(coordinates["parent"])["group_id"], kernel.SemMap(coordinates["parent"])["artifact_id"], kernel.SemMap(coordinates["parent"])["version"], coordinates["yss_components_version"]}
	for _, v := range values {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(kernel.Text(v)) {
			return s.Reject("SCAFFOLD_COORDINATES", "Maven 坐标非法")
		}
	}
	decisionRef := kernel.Text(doc["decision_ref"])
	if !filepath.IsAbs(decisionRef) {
		decisionRef = path.Join(path.Dir(ref), decisionRef)
	}
	if filepath.IsAbs(decisionRef) {
		r, e := filepath.Rel(s.Root, decisionRef)
		if e != nil || strings.HasPrefix(r, "..") {
			return s.Reject("PATH", "架构决定越出治理 root")
		}
		decisionRef = filepath.ToSlash(r)
	}
	record, e := contractBoundDoc(s, map[string]any{"ref": decisionRef, "digest": doc["decision_digest"]})
	if e != nil {
		return e
	}
	decision := kernel.ApFind(record["decisions"], "decision_id", kernel.Text(doc["decision_id"]))
	if kernel.Text(record["kind"]) != "scaffold-architecture-decisions" || record["template"] != false || kernel.Text(record["status"]) != "current" || kernel.Text(decision["status"]) != "lifecycle-approved" || decision["confirmed_architecture"] != doc["architecture_family"] || decision["project_id"] != doc["project_name"] {
		return s.Reject("SCAFFOLD_DECISION", "架构决定不是当前项目正式批准")
	}
	profiles, modules := kernel.SemMap(doc["profiles"]), kernel.SemMap(doc["module_profile"])
	if decision["platform_profile"] != profiles["platform"] || decision["architecture_profile"] != doc["architecture_profile"] || kernel.Text(decision["verification_database"]) != "h2" || kernel.Text(decision["production_database"]) != "not-bound" || decision["database_profile"] != nil || !contractSame(decision["requested_capabilities"], modules["requested_capabilities"]) || !contractSame(decision["resolved_modules"], modules["resolved_modules"]) {
		return s.Reject("SCAFFOLD_DECISION", "架构 Profile/模块闭包冲突")
	}
	if e = contractScaffoldUserDecision(s, decision); e != nil {
		return e
	}
	if kernel.Text(doc["architecture_profile"]) == "mvc-data-analysis-v1" {
		if doc["init_git"] != true {
			return s.Reject("SCAFFOLD_POLICY", "数据分析脚手架须显式要求独立 Git 初始化")
		}
		handoffRef := path.Join(path.Dir(ref), kernel.Text(doc["context_handoff_ref"]))
		if !contractWithin(handoffRef, path.Dir(ref)) || handoffRef == path.Dir(ref) || filepath.IsAbs(kernel.Text(doc["context_handoff_ref"])) {
			return s.Reject("SCAFFOLD_CONTEXT", "Context handoff必须在批准合同目录内部")
		}
		context, e := s.Bytes(handoffRef)
		if e != nil {
			return e
		}
		if "sha256:"+safefs.Digest(context) != kernel.Text(doc["context_handoff_digest"]) {
			return s.Reject("SCAFFOLD_CONTEXT", "Context handoff 摘要冲突")
		}
		if _, e = kernel.ContextContractBytes(context); e != nil {
			return s.Reject("SCAFFOLD_CONTEXT", e.Error())
		}
	} else if family == "layered-mvc" && doc["init_git"] != false {
		return s.Reject("SCAFFOLD_POLICY", "通用 MVC 不允许初始化 Git")
	}
	if e = contractBackendPrerequisites(s, kernel.SemMap(doc["design_prerequisites"]), doc, opts); e != nil {
		return e
	}
	profile, entry, reports, e := contractPlatform(s, kernel.SemMap(doc["platform_configuration"]), true)
	if e != nil {
		return e
	}
	bom := kernel.SemMap(kernel.SemMap(doc["platform_configuration"])["bom"])
	if profiles["platform"] != profile["id"] || profiles["validation_namespace"] != profile["validation_namespace"] || !contractSame(coordinates["parent"], kernel.SemMap(doc["platform_configuration"])["parent"]) || coordinates["yss_components_version"] != bom["version"] || bom["group_id"] != "com.yss.cloud" || bom["artifact_id"] != "yss-components-bom" || !contractSame(decision["platform_configuration"], doc["platform_configuration"]) {
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
	technical, e := contractBoundDoc(s, p["technical_design"])
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
	data, e := contractBoundDoc(s, p["data_architecture_decision"])
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
	if e = contractEvidence(s, data["evidence_refs"]); e != nil {
		return e
	}
	if kernel.Text(data["impact"]) == "required" {
		if _, e = contractBinding(s, map[string]any{"ref": data["data_architecture_ref"], "digest": data["data_architecture_digest"]}); e != nil {
			return e
		}
		if e = contractEvidence(s, data["review_evidence_refs"]); e != nil {
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
	if e = contractApproval(s, "gate.engineering-contract-approved", kernel.SemMap(p["technical_design"]), approvalRef, opts); e != nil {
		return e
	}
	approval, e := s.Doc(approvalRef)
	if e != nil {
		return e
	}
	approval, e = kernel.SelectApprovalRecordSemantic(s, approval, "gate.engineering-contract-approved")
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
	if scaffold != nil && (packageDoc["project_id"] != scaffold["project_name"] || !semSameSet(approval["approval_scope"], []any{scaffold["contract_id"], scaffold["project_name"]})) {
		return s.Reject("ENGINEERING_PACKAGE", "工程合同包项目或范围冲突")
	}
	if project := opts["project-id"]; project != "" && (kernel.Text(packageDoc["project_id"]) != project || !kernel.SemHas(approval["approval_scope"], project)) {
		return s.Reject("ENGINEERING_PACKAGE", "工程包项目或批准范围与当前消费者不一致")
	}
	for key, doc := range map[string]map[string]any{"technical_design": technical, "data_architecture_decision": data, "api_contract_decision": api} {
		expected := contractCopy(kernel.SemMap(p[key]))
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
			if !contractSame(actual[field], expected[field]) {
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
		if !contractSame(packageDoc["frozen_openapi"], api["openapi"]) {
			return s.Reject("ENGINEERING_PACKAGE", "工程包未原子冻结 OpenAPI")
		}
	} else if packageDoc["frozen_openapi"] != nil {
		return s.Reject("ENGINEERING_PACKAGE", "无 API 影响不得携带 frozen_openapi")
	}
	return nil
}

func contractAPIDecision(s *kernel.SemanticSession, binding map[string]any) (map[string]any, error) {
	doc, e := contractBoundDoc(s, binding)
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
	if e = contractEvidence(s, doc["evidence_refs"]); e != nil {
		return nil, e
	}
	if kernel.Text(doc["impact"]) == "not-applicable" {
		return doc, nil
	}
	openapi, e := contractBoundDoc(s, doc["openapi"])
	if e != nil {
		return nil, e
	}
	_ = openapi
	validation, e := contractBoundDoc(s, doc["validation_record"])
	if e != nil {
		return nil, e
	}
	review, e := contractBoundDoc(s, doc["draft_review"])
	if e != nil {
		return nil, e
	}
	bindingAPI := kernel.SemMap(doc["openapi"])
	if kernel.Text(review["result"]) != "approved" || review["blocking_findings"] == nil || len(kernel.SemList(review["blocking_findings"])) > 0 || review["draft_ref"] != bindingAPI["ref"] || review["draft_digest"] != bindingAPI["digest"] {
		return nil, s.Reject("OPENAPI_REVIEW", "OpenAPI 实际审查没有批准当前 Draft")
	}
	if e = contractEvidence(s, review["evidence_refs"]); e != nil {
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
	doc, e := contractBoundDoc(s, map[string]any{"ref": draft["ref"], "digest": draft["sha256"]})
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
		if !kernel.ContractPath(file) || !contractWithin(file, apiRoot) {
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

func contractTree(s *kernel.SemanticSession, prefix string) (string, []string, error) {
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
	return contractDigest(rows), files, nil
}

func contractPNG(s *kernel.SemanticSession, b []byte, viewport map[string]any) error {
	if len(b) < 24 || len(b) > 5<<20 || string(b[:8]) != "\x89PNG\r\n\x1a\n" || string(b[12:16]) != "IHDR" || int(binary.BigEndian.Uint32(b[16:20])) != kernel.ContractN(viewport["width"]) || int(binary.BigEndian.Uint32(b[20:24])) != kernel.ContractN(viewport["height"]) {
		return s.Reject("UI_BASELINE_IMAGE", "PNG 尺寸/格式/大小与 viewport 不一致")
	}
	return nil
}

func contractExistingUI(s *kernel.SemanticSession, ref string) (map[string]any, error) {
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
	digest, sourceFiles, e := contractTree(s, path.Join(prefix, kernel.Text(source["root_ref"])))
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
	if manifest["source_digest"] != source["digest"] || !contractSame(contractSortRows(kernel.SemList(manifest["files"]), "path"), contractSortRows(rows, "path")) {
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
		if kernel.ContractN(record["schema_version"]) != 1 || record["source_commit"] != source["source_commit"] || record["source_digest"] != source["digest"] || kernel.Text(record["command"]) == "" || !contractDate(record["executed_at"]) {
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
	outputDigest, outputs, e := contractTree(s, path.Join(prefix, kernel.Text(output["ref"])))
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
			if e != nil || !kernel.SemHas([]string{"http", "https"}, observed.Scheme) || kernel.Text(exchange["operation_id"]) == "" || kernel.Text(exchange["method"]) == "" || !contractDate(exchange["executed_at"]) || !ok || status < 100 || status > 599 {
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
			if !contractSame(matches[0][key], item[key]) {
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

func contractVisualBaseline(s *kernel.SemanticSession, ref string) (map[string]any, error) {
	data, e := s.Doc(ref)
	if e != nil {
		return nil, e
	}
	if e = contractRequired(s, data, "schema_version", "baseline_id", "feature", "version", "status", "bundle", "source", "capture_environment", "cases"); e != nil {
		return nil, e
	}
	bundle, source, env := kernel.SemMap(data["bundle"]), kernel.SemMap(data["source"]), kernel.SemMap(data["capture_environment"])
	if kernel.ContractN(data["schema_version"]) != 1 || kernel.Text(data["status"]) != "approved" || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(kernel.Text(data["feature"])) || kernel.Text(data["baseline_id"]) != "visual-baseline."+kernel.Text(data["feature"]) || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(kernel.Text(data["version"])) || kernel.Text(bundle["format"]) != "portable-directory" || kernel.ContractN(bundle["max_image_bytes"]) != 5<<20 || kernel.ContractN(bundle["max_bundle_bytes"]) != 100<<20 {
		return nil, s.Reject("VISUAL_BASELINE", "基线身份、状态或包限制错误")
	}
	if e = contractRequired(s, env, "browser_version", "operating_system", "fonts_digest", "locale", "timezone", "capture_script_ref", "capture_script_digest", "capture_result_ref", "capture_result_digest"); e != nil {
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
		if e = contractRequired(s, item, "case_id", "route", "page", "state", "viewport", "theme", "locale", "data_scenario", "image_ref", "image_digest", "image_size_bytes", "mask_ref", "mask_digest", "mask_size_bytes", "semantic_refs", "allowed_differences", "result"); e != nil {
			return nil, e
		}
		id := kernel.Text(item["case_id"])
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(id) || ids[id] || kernel.Text(item["result"]) != "passed" {
			return nil, s.Reject("VISUAL_BASELINE_CASE", "用例 ID/状态错误")
		}
		ids[id] = true
		viewport := kernel.SemMap(item["viewport"])
		if e = contractRequired(s, viewport, "name", "width", "height", "scroll_mode", "scroll_position"); e != nil {
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
		if !contractUnique(item["semantic_refs"], true) {
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
				k, _ := contractJSON(n.Content[i].Value)
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
			return contractJSON(value)
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
		k, _ := contractJSON(key)
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
					k, _ := contractJSON(key)
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

func verifyFrontendDeliverySemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	if opts["phase"] != "" && !kernel.SemHas([]string{"preflight", "design", "contract", "inputs", "implementation", "verification"}, opts["phase"]) {
		return s.Reject("FRONTEND_PHASE", "未知前端交付阶段")
	}
	if opts["unit"] != "" && ref != opts["checkpoint"] {
		return s.Reject("FRONTEND_BINDING", "外部接收记录不得伪装本地工作单元输入")
	}
	if opts["checkpoint"] != "" && ref == opts["checkpoint"] {
		return contractLocalFrontendInputs(s, nil, opts)
	}
	binding := map[string]any{"acceptance_ref": ref, "digest": opts["expected-digest"]}
	var c *nativeSlice
	if opts["contract"] != "" {
		var e error
		c, e = loadNativeSlice(s, opts["contract"])
		if e != nil {
			return e
		}
		if kernel.Text(c.Raw["status"]) != "approved" && opts["phase"] != "inputs" {
			return s.Reject("SLICE_APPROVAL_REQUIRED", "前端实现须当前批准 Slice")
		}
	}
	return contractFrontendDelivery(s, binding, c, opts)
}

func contractFrontendDelivery(s *kernel.SemanticSession, binding map[string]any, c *nativeSlice, opts map[string]string) error {
	ref := kernel.First(kernel.Text(binding["acceptance_ref"]), kernel.Text(binding["ref"]))
	if ref == "" {
		return s.Reject("FRONTEND_DELIVERY_REQUIRED", "前端合同缺少接收记录")
	}
	doc, e := s.Doc(ref)
	if e != nil {
		return e
	}
	n := kernel.ContractN(doc["schema_version"])
	schemaRef := map[int]string{1: ".template-spec/process/schemas/frontend-delivery-acceptance-v1.schema.json", 2: ".template-spec/process/schemas/frontend-delivery-acceptance.schema.json", 3: ".template-spec/process/schemas/frontend-delivery-acceptance-v3.schema.json"}[n]
	if schemaRef == "" {
		return s.Unavailable("CAPABILITY", "未知前端接收协议")
	}
	if e = s.ValidateSchema(schemaRef, doc); e != nil {
		return e
	}
	b, e := s.Bind(ref)
	if e != nil {
		return e
	}
	if kernel.Text(binding["digest"]) != "" && kernel.Text(binding["digest"]) != b.Digest {
		return s.Reject("FRONTEND_DELIVERY_STALE", "接收原字节变化")
	}
	slice := opts["slice"]
	if c != nil {
		slice = kernel.Text(c.Raw["slice_id"])
	}
	if slice == "" || kernel.Text(doc["slice_id"]) != slice {
		return s.Reject("FRONTEND_DELIVERY_SLICE", "前端接收与执行切片冲突")
	}
	if n >= 2 {
		preflightBinding := kernel.SemMap(doc["strategic_preflight"])
		preflight, e := contractBoundDoc(s, preflightBinding)
		if e != nil {
			return e
		}
		pn := kernel.ContractN(preflight["schema_version"])
		preflightSchema := ".template-spec/process/schemas/frontend-strategic-preflight.schema.json"
		if pn == 2 {
			preflightSchema = ".template-spec/process/schemas/frontend-strategic-preflight-v2.schema.json"
		}
		if pn != 1 && pn != 2 {
			return s.Unavailable("CAPABILITY", "未知前端战略预检协议")
		}
		if e = s.ValidateSchema(preflightSchema, preflight); e != nil {
			return e
		}
		if kernel.Text(preflight["status"]) != "verified" || (n == 3) != (pn == 2) || pn == 2 && preflight["ui_baseline_kind"] != doc["ui_baseline_kind"] {
			return s.Reject("FRONTEND_PREFLIGHT", "预检状态或协议版本冲突")
		}
		receipt, e := s.Doc(kernel.Text(preflight["import_receipt_ref"]))
		if e != nil {
			return e
		}
		base := path.Join("docs/handoffs", kernel.Text(receipt["bundle_id"]), kernel.Text(receipt["version"]))
		if !kernel.SemHas([]string{"2", "3"}, fmt.Sprint(kernel.ContractN(receipt["schema_version"]))) || kernel.Text(preflight["import_receipt_ref"]) != base+"/import-receipt.json" || kernel.Text(receipt["package_ref"]) != base+"/package" || preflight["bundle_digest"] != receipt["bundle_digest"] || preflight["bundle_digest"] != kernel.SemMap(doc["strategic_handoff"])["bundle_digest"] {
			return s.Reject("FRONTEND_PREFLIGHT", "预检收据身份或战略包版本冲突")
		}
		if e = contractReceipt(s, receipt); e != nil {
			return e
		}
		if e = contractReceiptConsumer(s, receipt, "frontend-engineering-design"); e != nil {
			return e
		}
		if e = s.Verify("context-reconciliation", kernel.Text(preflight["context_reconciliation_ref"]), map[string]string{"import-receipt": kernel.Text(preflight["import_receipt_ref"])}); e != nil {
			return e
		}
		bundle, e := contractOpenHandoff(s, kernel.Text(receipt["package_ref"]))
		if e != nil {
			return e
		}
		route := kernel.ApFind(bundle.Handoff["consumer_routes"], "capability", "frontend-engineering-design")
		backend := kernel.ApFind(bundle.Handoff["consumer_routes"], "capability", "backend-technical-design")
		routeReceipt := kernel.ApFind(receipt["routes"], "capability", "frontend-engineering-design")
		if route == nil || routeReceipt == nil || kernel.Text(route["activation"]) == "not-applicable" || route["route_id"] != preflight["route_id"] || routeReceipt["route_id"] != preflight["route_id"] || (pn == 2) != (kernel.ContractN(bundle.Handoff["schema_version"]) == 5) {
			return s.Reject("FRONTEND_PREFLIGHT", "当前交接路线或协议降级冲突")
		}
		sourceIDs := []string{}
		for _, v := range bundle.Rules {
			sourceIDs = append(sourceIDs, kernel.Text(kernel.SemMap(v)["rule_id"]))
		}
		for _, v := range bundle.Scenarios {
			m := kernel.SemMap(v)
			if m["critical"] == true {
				sourceIDs = append(sourceIDs, kernel.Text(m["scenario_id"]))
			}
		}
		if !semSameSet(sourceIDs, preflight["source_rule_refs"]) {
			return s.Reject("FRONTEND_PREFLIGHT", "未完整绑定来源规则/关键场景")
		}
		visual := contractHandoffUIRef(bundle.Handoff)
		expectedVisual := path.Join(base, "package/payload/files", kernel.Text(visual["persisted_ref"]), kernel.Text(visual["manifest_ref"]))
		key := "visual_baseline_ref"
		if pn == 2 {
			key = "ui_baseline_ref"
		}
		if kernel.Text(preflight[key]) != expectedVisual {
			return s.Reject("FRONTEND_PREFLIGHT", "预检 UI baseline 引用冲突")
		}
		expectedMode := "required"
		if kernel.Text(backend["activation"]) == "not-applicable" {
			expectedMode = "not-applicable"
		}
		dep := kernel.SemMap(preflight["backend_dependency"])
		if kernel.Text(dep["mode"]) != expectedMode || dep["route_id"] != backend["route_id"] || kernel.Text(kernel.SemMap(doc["backend_dependency"])["mode"]) != expectedMode {
			return s.Reject("FRONTEND_PREFLIGHT", "后端依赖模式与当前路线冲突")
		}
		if expectedMode == "not-applicable" {
			for _, key := range []string{"reason", "impact_refs", "evidence_refs"} {
				if !contractSame(dep[key], backend[key]) {
					return s.Reject("FRONTEND_PREFLIGHT", "后端不适用依据冲突")
				}
			}
			if doc["backend_delivery"] != nil {
				return s.Reject("FRONTEND_DELIVERY", "后端不适用不得绑定交付收据")
			}
			if e = contractHandoffConsumption(s, doc, map[string]string{"consumer": "frontend", "slice": slice}); e != nil {
				return e
			}
			ids := map[string]bool{}
			for _, v := range kernel.SemList(doc["frontend_cases"]) {
				item := kernel.SemMap(v)
				id := kernel.Text(item["case_id"])
				if id == "" || ids[id] || len(kernel.SemStrings(item["operation_ids"])) > 0 {
					return s.Reject("FRONTEND_CASE", "用例重复或无 API 分支声明接口")
				}
				ids[id] = true
				for _, source := range kernel.SemStrings(item["source_ids"]) {
					if !kernel.SemHas(sourceIDs, source) {
						return s.Reject("FRONTEND_CASE", "未知来源规则/场景")
					}
				}
				caseKey := "visual_case_ids"
				if n == 3 {
					caseKey = "baseline_case_ids"
				}
				for _, id := range kernel.SemStrings(item[caseKey]) {
					if !kernel.SemHas(visual["case_ids"], id) {
						return s.Reject("FRONTEND_CASE", "未知基线用例")
					}
				}
				evidence, e := s.Bytes(kernel.Text(item["evidence_ref"]))
				if e != nil {
					return e
				}
				if len(evidence) == 0 || "sha256:"+safefs.Digest(evidence) != kernel.Text(item["evidence_digest"]) {
					return s.Reject("FRONTEND_CASE", "前端用例说明为空或摘要变化")
				}
			}
			return nil
		}
	}
	return contractFrontendBackendDelivery(s, doc, slice, opts)
}

func contractFrontendBackendDelivery(s *kernel.SemanticSession, acceptance map[string]any, slice string, opts map[string]string) error {
	binding := kernel.SemMap(acceptance["backend_delivery"])
	receipt, e := s.Doc(kernel.Text(binding["import_receipt_ref"]))
	if e != nil {
		return e
	}
	base := path.Join("docs/backend-deliveries", kernel.Text(receipt["delivery_id"]), kernel.Text(receipt["version"]))
	if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(kernel.Text(receipt["version"])) || kernel.Text(binding["import_receipt_ref"]) != base+"/import-receipt.json" || kernel.Text(receipt["package_ref"]) != base+"/package" || binding["bundle_digest"] != receipt["bundle_digest"] {
		return s.Reject("FRONTEND_DELIVERY", "后端收据路径、身份或摘要冲突")
	}
	names, e := s.List(path.Dir(base))
	if e != nil {
		return e
	}
	latest, greatest := "", 0
	for _, name := range names {
		if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(name) {
			continue
		}
		exists, e := s.Exists(path.Join(path.Dir(base), name, "import-receipt.json"))
		if e != nil {
			return e
		}
		if !exists {
			continue
		}
		version, e := strconv.Atoi(name[1:])
		if e != nil {
			return s.Reject("FRONTEND_DELIVERY", "后端收据版本超限")
		}
		if version > greatest {
			greatest, latest = version, name
		}
	}
	if latest != kernel.Text(receipt["version"]) {
		return s.Reject("FRONTEND_DELIVERY_STALE", "已有更新后端收据，当前前端接收已过期")
	}
	manifest, source, e := backendOpenDelivery(s, kernel.Text(receipt["package_ref"]), opts)
	if e != nil {
		return e
	}
	if manifest["bundle_digest"] != receipt["bundle_digest"] || manifest["delivery_id"] != receipt["delivery_id"] || manifest["version"] != receipt["version"] {
		return s.Reject("FRONTEND_DELIVERY", "后端包与接收收据冲突")
	}
	delivery, e := source.Doc(kernel.Text(manifest["delivery_ref"]))
	if e != nil {
		return e
	}
	if kernel.SemMap(delivery["scope"])["slice_id"] != slice || delivery["strategic_bundle_digest"] != kernel.SemMap(acceptance["strategic_handoff"])["bundle_digest"] {
		return s.Reject("FRONTEND_DELIVERY", "后端、战略和前端切片版本冲突")
	}
	if e = contractHandoffConsumption(s, acceptance, map[string]string{"consumer": "frontend", "slice": slice}); e != nil {
		return e
	}
	bundle, e := backendOpenStrategicInput(source, kernel.Text(delivery["strategic_bundle_ref"]))
	if e != nil {
		return e
	}
	n := kernel.ContractN(acceptance["schema_version"])
	if n != 3 && kernel.ContractN(bundle.Handoff["schema_version"]) == 5 {
		return s.Reject("FRONTEND_DELIVERY", "Handoff v5 必须使用前端接收 v3")
	}
	visual := contractHandoffUIRef(bundle.Handoff)
	seen := map[string]bool{}
	for _, v := range kernel.SemList(acceptance["frontend_cases"]) {
		item := kernel.SemMap(v)
		id := kernel.Text(item["case_id"])
		if id == "" || seen[id] || n >= 2 && len(kernel.SemStrings(item["operation_ids"])) == 0 {
			return s.Reject("FRONTEND_CASE", "有后端依赖的用例须唯一并绑定已交付接口")
		}
		seen[id] = true
		for _, id := range kernel.SemStrings(item["operation_ids"]) {
			if !kernel.SemHas(kernel.SemMap(delivery["scope"])["operation_ids"], id) {
				return s.Reject("FRONTEND_CASE", "前端用例依赖未交付接口")
			}
		}
		for _, id := range kernel.SemStrings(item["source_ids"]) {
			covered := kernel.SemHas(kernel.SemMap(delivery["scope"])["source_ids"], id)
			if !covered && n == 3 && bundle.Business != nil {
				row := kernel.ApFind(bundle.Business["tickets"], "id", id)
				if row != nil {
					ticket, e := bundle.Source.Doc(kernel.Text(row["ref"]))
					if e != nil {
						return e
					}
					related := 0
					covered = true
					for _, v := range kernel.SemList(ticket["source_refs"]) {
						binding := kernel.SemMap(v)
						locator := kernel.Text(binding["locator"])
						if binding["locator_kind"] == "id" && (kernel.ApFind(bundle.Rules, "rule_id", locator) != nil || kernel.ApFind(bundle.Scenarios, "scenario_id", locator) != nil) {
							related++
							if !kernel.SemHas(kernel.SemMap(delivery["scope"])["source_ids"], locator) {
								covered = false
							}
						}
					}
					covered = covered && related > 0
				}
			}
			if !covered {
				return s.Reject("FRONTEND_CASE", "前端用例依赖未交付规则/场景")
			}
		}
		caseKey := "visual_case_ids"
		if n == 3 {
			caseKey = "baseline_case_ids"
		}
		for _, id := range kernel.SemStrings(item[caseKey]) {
			if !kernel.SemHas(visual["case_ids"], id) {
				return s.Reject("FRONTEND_CASE", "前端用例视觉基线悬空")
			}
		}
		b, e := s.Bytes(kernel.Text(item["evidence_ref"]))
		if e != nil {
			return e
		}
		if len(b) == 0 || "sha256:"+safefs.Digest(b) != kernel.Text(item["evidence_digest"]) {
			return s.Reject("FRONTEND_CASE", "前端用例说明为空或摘要漂移")
		}
	}
	for _, id := range kernel.SemStrings(kernel.SemMap(delivery["scope"])["source_ids"]) {
		row := kernel.ApFind(kernel.SemMap(acceptance["strategic_handoff"])["rows"], "source_id", id)
		if kernel.Text(row["disposition"]) != "mapped" || !kernel.SemHas(row["dependent_slice_refs"], slice) {
			return s.Reject("FRONTEND_CASE", "交付规则/场景未映射当前前端切片")
		}
	}
	return contractProbeBackend(s, delivery)
}

func contractProbeBackend(s *kernel.SemanticSession, delivery map[string]any) error {
	environment := kernel.SemMap(delivery["environment"])
	base, e := url.Parse(kernel.Text(environment["base_url"]))
	validURL := func(u *url.URL) bool {
		return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.ContainsAny(u.Host, "\\\r\n")
	}
	if e != nil || !validURL(base) {
		return s.Reject("FRONTEND_PROBE_ENVIRONMENT", "服务环境 URL 无效或包含凭据、查询、片段")
	}
	revision, e := url.Parse(kernel.Text(environment["revision_path"]))
	if e != nil {
		return s.Reject("FRONTEND_PROBE_ENVIRONMENT", "版本探测路径非法")
	}
	target := base.ResolveReference(revision)
	origin := func(u *url.URL) string {
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		return strings.ToLower(u.Scheme+"://"+u.Hostname()) + ":" + port
	}
	if !validURL(target) || origin(target) != origin(base) {
		return s.Reject("FRONTEND_PROBE_ENVIRONMENT", "版本探测必须在登记环境同源内")
	}
	expected := map[string]any{"deployment_id": environment["deployment_id"], "source_commit": kernel.SemMap(delivery["build"])["source_commit"], "openapi_digest": kernel.SemMap(delivery["openapi"])["digest"], "artifact_digest": kernel.SemMap(delivery["build"])["artifact_digest"], "test_data_digest": kernel.SemMap(environment["test_data"])["digest"]}
	pointers := kernel.SemMap(environment["revision_pointers"])
	for key, value := range expected {
		pointer := kernel.Text(pointers[key])
		if kernel.Text(value) == "" || !strings.HasPrefix(pointer, "/") || strings.Contains(pointer, "//") {
			return s.Reject("FRONTEND_PROBE_ENVIRONMENT", "版本身份或 JSON Pointer 缺失: "+key)
		}
	}
	name := kernel.Text(environment["authorization_env"])
	if name != "" && !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
		return s.Reject("FRONTEND_PROBE_ENVIRONMENT", "授权环境变量名称非法")
	}
	authorization := ""
	if name != "" {
		authorization = os.Getenv(name)
		if authorization == "" {
			return s.Unavailable("FRONTEND_PROBE_AUTHORIZATION", "缺少已登记授权环境变量")
		}
	}
	authDigest := safefs.Digest([]byte(authorization))
	publicTarget := (&url.URL{Scheme: target.Scheme, Host: target.Host, Path: target.Path}).String()
	key := publicTarget + ":" + contractDigest(map[string]any{"url": target.String(), "expected": expected, "pointers": pointers, "authorization_env": name, "authorization_digest": authDigest})
	return s.ObserveOnline(key, func() error {
		if e := s.Guard(); e != nil {
			return e
		}
		if name != "" && safefs.Digest([]byte(os.Getenv(name))) != authDigest {
			return s.Unavailable("INPUT_DRIFT", "版本探测授权环境在核验期间变化")
		}
		ctx, cancel := context.WithTimeout(s.Ctx, 5*time.Second)
		defer cancel()
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if e != nil {
			return s.Reject("FRONTEND_PROBE_ENVIRONMENT", "版本探测请求无效")
		}
		request.Header.Set("Accept", "application/json")
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		// The fixed protocol has no environment proxy. Construct a private
		// transport rather than inheriting mutable process defaults or credentials.
		transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 5 * time.Second}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, e := client.Do(request)
		if e != nil {
			if cancelErr := s.Guard(); cancelErr != nil {
				return cancelErr
			}
			return s.Unavailable("FRONTEND_PROBE_UNAVAILABLE", "当前登记服务版本不可读")
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return s.Reject("FRONTEND_PROBE_HTTP", fmt.Sprintf("当前服务版本 HTTP %d", response.StatusCode))
		}
		body, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if e != nil {
			if cancelErr := s.Guard(); cancelErr != nil {
				return cancelErr
			}
			return s.Unavailable("FRONTEND_PROBE_UNAVAILABLE", "服务版本响应读取失败")
		}
		if len(body) > 1<<20 || !json.Valid(body) {
			return s.Reject("FRONTEND_PROBE_RESPONSE", "服务版本响应超限或不是 JSON")
		}
		value, e := schema.Parse(body)
		if e != nil {
			return s.Reject("FRONTEND_PROBE_RESPONSE", "服务版本 JSON 不明确")
		}
		for key, expectedValue := range expected {
			current := value
			for _, segment := range strings.Split(kernel.Text(pointers[key])[1:], "/") {
				segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
				if object, ok := current.(map[string]any); ok {
					current = object[segment]
				} else if array, ok := current.([]any); ok && regexp.MustCompile(`^(0|[1-9][0-9]*)$`).MatchString(segment) {
					index, e := strconv.Atoi(segment)
					if e != nil || index >= len(array) {
						current = nil
					} else {
						current = array[index]
					}
				} else {
					current = nil
				}
			}
			if !contractSame(current, expectedValue) {
				return s.Reject("FRONTEND_PROBE_REVISION", "真实服务版本不匹配: "+key)
			}
		}
		return nil
	})
}
