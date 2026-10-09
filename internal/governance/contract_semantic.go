package governance

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
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
func contractPlatformFingerprint(s *semanticSession, family string) (map[string]any, error) {
	tool, e := s.toolSourceSession()
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
		return nil, s.reject("PLATFORM_SOURCE", "未知脚手架架构")
	}
	base := ".agents/skills/" + skill
	generator := []string{base + "/scripts/generate_scaffold.mjs", base + "/assets", "scripts/lib/backend-platform.mjs", "scripts/lib/scaffold-local-database.mjs"}
	if present, err := s.exists("scripts/lib/standalone-backend-scaffold.mjs"); err != nil {
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
			exists, e := s.exists(ref)
			if e != nil {
				return "", e
			}
			if !exists {
				return "", s.unavailable("CAPABILITY", "固定工具provenance字节缺失，需显式--tool-root: "+ref)
			}
			inputs := []string{ref}
			if _, dir := s.scans[s.localRef(ref)]; dir {
				inputs, e = s.scan(ref)
				if e != nil {
					return "", e
				}
			}
			for _, ref := range inputs {
				b, e := s.bytes(ref)
				if e != nil {
					return "", e
				}
				d, e := s.v.watch(s.localRef(ref))
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
func contractPlatform(s *semanticSession, binding map[string]any, verified bool) (map[string]any, map[string]any, []map[string]any, error) {
	c, e := contractPlatformCatalog(s)
	if e != nil {
		return nil, nil, nil, e
	}
	if contractN(binding["schema_version"]) != 2 {
		return nil, nil, nil, s.reject("PLATFORM_BINDING", "当前运行需要平台配置v2")
	}
	matches := []map[string]any{}
	for _, v := range semList(c["profiles"]) {
		p := semMap(v)
		if p["id"] == binding["profile_id"] && p["spring_boot_version"] == binding["spring_boot_version"] {
			matches = append(matches, p)
		}
	}
	if len(matches) != 1 {
		return nil, nil, nil, s.reject("PLATFORM_BINDING", "平台配置必须选择唯一精确Boot patch")
	}
	profile := matches[0]
	entry := apFind(c["compatibility"], "id", text(binding["compatibility_id"]))
	if entry == nil || entry["profile_id"] != profile["id"] || entry["spring_boot_version"] != profile["spring_boot_version"] {
		return nil, nil, nil, s.reject("PLATFORM_BINDING", "缺少精确YSS兼容条目")
	}
	expected := map[string]any{"schema_version": 2, "profile_id": profile["id"], "spring_boot_version": profile["spring_boot_version"], "java_version": profile["java_version"], "parent": entry["parent"], "bom": entry["bom"], "compatibility_id": entry["id"], "compatibility_digest": contractPlatformRecipe(profile, entry)}
	if line := text(profile["component_platform_line"]); line == "boot2-java8" || line == "boot3-java17" {
		expected["component_platform_line"] = line
	}
	if !contractSame(binding, expected) {
		return nil, nil, nil, s.reject("PLATFORM_BINDING", "平台坐标、Java版本或兼容摘要漂移")
	}
	reports := []map[string]any{}
	if !verified {
		return profile, entry, reports, nil
	}
	if text(entry["status"]) != "verified" || len(semList(entry["evidence"])) == 0 {
		return nil, nil, nil, s.reject("PLATFORM_UNVERIFIED", "YSS组合尚未验证")
	}
	for _, v := range semList(entry["evidence"]) {
		ev := semMap(v)
		report, e := contractBoundDoc(s, ev)
		if e != nil {
			return nil, nil, nil, e
		}
		family := text(report["architecture_family"])
		if text(report["verification_scope"]) != "empty-scaffold" || text(report["recipe_digest"]) != contractPlatformRecipe(profile, entry) || !contractSHA(report["generated_tree_digest"]) || text(report["status"]) != "passed" || report["spring_boot_version"] != profile["spring_boot_version"] || report["java_version"] != profile["java_version"] || report["architecture_family"] != ev["architecture_family"] || !contractSame(report["parent"], entry["parent"]) || !contractSame(report["bom"], entry["bom"]) || text(report["dependency_check"]) != "passed" || text(report["startup_check"]) != "passed" || text(semMap(report["integration_tests"])["status"]) != "passed" {
			return nil, nil, nil, s.reject("PLATFORM_EVIDENCE", "平台qualification身份或集成检查不完整")
		}
		fingerprint, e := contractPlatformFingerprint(s, family)
		if e != nil {
			return nil, nil, nil, e
		}
		if !contractSame(report["source_fingerprint"], fingerprint) {
			return nil, nil, nil, s.reject("PLATFORM_SOURCE", "固定generator/verifier源码变化")
		}
		for _, phase := range []string{"validate", "test", "package"} {
			found := false
			for _, v := range semList(report["commands"]) {
				row := semMap(v)
				if text(row["command"]) != "./mvnw "+phase {
					continue
				}
				exit, ok := integer(row["exit_code"])
				// The published qualification protocol records a nonempty timestamp;
				// historical reports do not define an RFC3339 restriction.
				if !ok || exit != 0 || text(row["executed_at"]) == "" || text(row["stdout_ref"]) == "" || text(row["stderr_ref"]) == "" {
					return nil, nil, nil, s.reject("PLATFORM_EVIDENCE", "Maven真实执行证据不完整")
				}
				found = true
			}
			if !found {
				return nil, nil, nil, s.reject("PLATFORM_EVIDENCE", "平台缺少实际Maven阶段")
			}
		}
		required := []string{"effective-pom.xml", "dependency-trees.json", "platform-tests.xml", "boot-bom-effective.xml", "runtime-jar-entries.log", "startup.stdout.log", "startup.stderr.log"}
		for _, phase := range []string{"validate", "test", "package"} {
			required = append(required, "mvnw-"+phase+".stdout.log", "mvnw-"+phase+".stderr.log")
		}
		artifacts := semList(report["evidence_artifacts"])
		if len(artifacts) == 0 {
			return nil, nil, nil, s.reject("PLATFORM_EVIDENCE", "平台可移植实际证据缺失")
		}
		for _, name := range required {
			if apFind(artifacts, "ref", name) == nil {
				return nil, nil, nil, s.reject("PLATFORM_EVIDENCE", "平台缺少可移植证据: "+name)
			}
		}
		for _, v := range artifacts {
			a := semMap(v)
			if !contractPath(text(a["ref"])) {
				return nil, nil, nil, s.reject("PATH", "平台证据路径非法")
			}
			ref := path.Join(path.Dir(text(ev["ref"])), text(a["ref"]))
			if _, e = contractBinding(s, map[string]any{"ref": ref, "digest": a["digest"]}); e != nil {
				return nil, nil, nil, e
			}
		}
		reports = append(reports, report)
	}
	return profile, entry, reports, nil
}
func contractScaffoldUserDecision(s *semanticSession, decision map[string]any) error {
	c, e := contractPlatformCatalog(s)
	if e != nil {
		return e
	}
	var profile map[string]any
	for _, v := range semList(c["profiles"]) {
		p := semMap(v)
		if p["id"] == decision["platform_profile"] && (semMap(decision["platform_configuration"])["spring_boot_version"] == nil || p["spring_boot_version"] == semMap(decision["platform_configuration"])["spring_boot_version"]) {
			if profile != nil {
				return s.reject("SCAFFOLD_DECISION", "架构决定平台patch不唯一")
			}
			profile = p
		}
	}
	if profile == nil {
		return s.reject("SCAFFOLD_DECISION", "架构决定平台未知")
	}
	confirmation := semMap(decision["user_confirmation"])
	requirement := map[string]any{"boundary": "gate.backend-architecture-platform-approved", "subject_ref": confirmation["decision_subject_ref"], "scope": []any{decision["project_id"], decision["confirmed_architecture"], fmt.Sprintf("%s@%s/java%d", text(profile["id"]), text(profile["spring_boot_version"]), contractN(profile["java_version"])), decision["decision_inputs_digest"]}, "user_decision_ref": confirmation["user_decision_ref"]}
	if _, e = assertUserDecisionRequirementSemantic(s, requirement); e != nil {
		return e
	}
	subject, e := s.doc(text(requirement["subject_ref"]))
	if e != nil {
		return e
	}
	snapshot := contractWithout(decision, "user_confirmation", "status", "decision_inputs_digest")
	snapshot["platform_selection"] = map[string]any{"profile_id": profile["id"], "spring_boot_version": profile["spring_boot_version"], "java_version": profile["java_version"]}
	if !contractSame(subject, snapshot) {
		return s.reject("user-decision-stale", "脚手架输入与用户所见快照不一致")
	}
	return nil
}
func contractExactVersion(v any) bool {
	return text(v) != "" && !regexp.MustCompile(`(?i)[\[\](),*+]|\bx\b|latest`).MatchString(text(v))
}
func contractSHA(v any) bool     { return regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(text(v)) }
func contractGitTree(v any) bool { return regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(text(v)) }
func contractPlatformGeneration(profile map[string]any) (string, int) {
	v := strings.Split(text(profile["spring_boot_version"]), ".")[0]
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
	for _, v := range semList(v) {
		m := semMap(v)
		if text(m["status"]) != "zero" || text(m["ref"]) == "" {
			return false
		}
		delete(required, text(m["kind"]))
	}
	return len(required) == 0
}
func contractPlatformArtifact(s *semanticSession, b map[string]any, role string, expected map[string]any, snapshot, resolved bool) error {
	status := text(b["status"])
	if role == "" || text(b["role"]) != role || status != "candidate" && !(resolved && status == "resolved") || text(b["group_id"]) == "" || text(b["artifact_id"]) == "" || !contractExactVersion(b["declared_version"]) || snapshot != strings.HasSuffix(text(b["declared_version"]), "-SNAPSHOT") {
		return s.reject("PLATFORM_ARTIFACT", "平台候选产物坐标或角色冲突")
	}
	if expected != nil && (b["group_id"] != expected["group_id"] || b["artifact_id"] != expected["artifact_id"] || b["declared_version"] != expected["version"]) {
		return s.reject("PLATFORM_ARTIFACT", "候选产物与兼容坐标冲突")
	}
	if status == "candidate" {
		for _, field := range []string{"resolved_version", "published_at", "pom_sha256", "jar_sha256", "sources_jar_sha256", "source_tree"} {
			value, exists := b[field]
			if !exists || value != nil {
				return s.reject("PLATFORM_ARTIFACT", "候选产物不得伪造已解析证据")
			}
		}
		if !apIsArray(b["evidence"]) || len(semList(b["evidence"])) != 0 || len(semList(b["blockers"])) == 0 {
			return s.reject("PLATFORM_ARTIFACT", "候选产物必须保持有阻断且未解析")
		}
	} else {
		if snapshot || b["resolved_version"] != b["declared_version"] || !contractSHA(b["pom_sha256"]) || !contractSHA(b["jar_sha256"]) || !contractSHA(b["sources_jar_sha256"]) || b["published_at"] != nil && text(b["published_at"]) == "" || b["source_tree"] != nil && !contractGitTree(b["source_tree"]) || !apIsArray(b["evidence"]) || !apIsArray(b["blockers"]) {
			return s.reject("PLATFORM_ARTIFACT", "外部发布产物缺少不可变证据")
		}
	}
	return nil
}
func contractPlatformCatalog(s *semanticSession) (map[string]any, error) {
	c, e := s.doc(".template-spec/engineering/backend-platforms.json")
	if e != nil {
		return nil, e
	}
	if contractN(c["schema_version"]) != 2 || text(c["kind"]) != "backend-platform-catalog" || !apIsArray(c["profiles"]) || !apIsArray(c["compatibility"]) || c["component_capabilities"] != nil || c["component_capability_ids"] != nil {
		return nil, s.reject("PLATFORM_CATALOG", "平台目录协议无效")
	}
	profiles, entries := map[string]bool{}, map[string]bool{}
	for _, v := range semList(c["profiles"]) {
		p := semMap(v)
		key := text(p["id"]) + ":" + text(p["spring_boot_version"]) + ":" + fmt.Sprint(p["java_version"])
		line, _ := contractPlatformGeneration(p)
		if profiles[key] || !regexp.MustCompile(`^\d+[.]\d+[.]\d+$`).MatchString(text(p["spring_boot_version"])) || line != "boot4" && text(p["component_platform_line"]) != line || line == "boot4" && p["component_platform_line"] != nil {
			return nil, s.reject("PLATFORM_CATALOG", "重复平台或代际冲突")
		}
		profiles[key] = true
	}
	for _, v := range semList(c["compatibility"]) {
		entry := semMap(v)
		id := text(entry["id"])
		if entries[id] || id == "" {
			return nil, s.reject("PLATFORM_CATALOG", "兼容条目ID重复或为空")
		}
		entries[id] = true
		var profile map[string]any
		for _, v := range semList(c["profiles"]) {
			p := semMap(v)
			if p["id"] == entry["profile_id"] && p["spring_boot_version"] == entry["spring_boot_version"] {
				profile = p
			}
		}
		line, major := contractPlatformGeneration(profile)
		if profile == nil || line == "boot4" || text(profile["component_platform_line"]) != line {
			return nil, s.reject("PLATFORM_CATALOG", "兼容条目平台代际缺失")
		}
		if raw := entry["platform_artifact_bindings"]; raw != nil {
			artifacts := semMap(raw)
			if len(artifacts) != 2 {
				return nil, s.reject("PLATFORM_ARTIFACT", "平台候选必须完整包含parent和bom")
			}
			for _, role := range []string{"parent", "bom"} {
				if e = contractPlatformArtifact(s, semMap(artifacts[role]), role, semMap(entry[role]), true, false); e != nil {
					return nil, e
				}
			}
		}
		for _, field := range []string{"external_snapshot_bindings", "external_artifact_bindings"} {
			if entry[field] == nil {
				continue
			}
			if !apIsArray(entry[field]) {
				return nil, s.reject("PLATFORM_ARTIFACT", "外部绑定不是数组")
			}
			coordinates := map[string]bool{}
			for _, v := range semList(entry[field]) {
				b := semMap(v)
				key := text(b["group_id"]) + ":" + text(b["artifact_id"])
				if coordinates[key] {
					return nil, s.reject("PLATFORM_ARTIFACT", "外部产物坐标重复")
				}
				coordinates[key] = true
				if e = contractPlatformArtifact(s, b, text(b["role"]), nil, field == "external_snapshot_bindings", field == "external_artifact_bindings"); e != nil {
					return nil, e
				}
			}
		}
		resolutions := map[string]map[string]any{}
		if raw := entry["artifact_resolution_evidence"]; raw != nil {
			b := semMap(raw)
			report, e := contractBoundDoc(s, b)
			if e != nil {
				return nil, e
			}
			if contractN(report["schema_version"]) != 1 || text(report["kind"]) != "backend-component-artifact-resolution" || report["report_id"] != b["report_id"] || semMap(report["repository"])["id"] != b["repository_id"] || !apIsArray(report["artifacts"]) {
				return nil, s.reject("PLATFORM_ARTIFACT", "产物解析证据身份冲突")
			}
			for _, v := range semList(report["artifacts"]) {
				a := semMap(v)
				resolutions[text(a["group_id"])+":"+text(a["artifact_id"])+":"+text(a["declared_version"])] = a
			}
		}
		if entry["component_capabilities"] == nil {
			continue
		}
		components := semMap(entry["component_capabilities"])
		if components == nil {
			return nil, s.reject("PLATFORM_COMPONENT", "组件能力不是映射")
		}
		for id, raw := range components {
			item := semMap(raw)
			if !strings.Contains(id, ".") || !semHas([]string{"candidate", "verified", "blocked"}, text(item["status"])) || !apIsArray(item["verified_architectures"]) || !apIsArray(item["artifacts"]) || !apIsArray(item["evidence"]) || !apIsArray(item["blockers"]) || item["provider_kind"] != nil && !semHas([]string{"yss-component", "platform-managed"}, text(item["provider_kind"])) || !semHas([]string{"active", "maintenance", "deprecated", "retired"}, first(text(item["lifecycle"]), "active")) || !semHas([]string{"allowed", "forbidden"}, first(text(item["adoption_policy"]), "allowed")) {
				return nil, s.reject("PLATFORM_COMPONENT", "组件能力结构或状态无效")
			}
			if text(item["lifecycle"]) == "retired" && !contractRetirement(item["retirement_evidence"]) {
				return nil, s.reject("PLATFORM_COMPONENT", "退役缺少组织零使用证据")
			}
			if !contractUnique(item["verified_architectures"], false) {
				return nil, s.reject("PLATFORM_COMPONENT", "验证架构重复或无效")
			}
			for _, family := range semStrings(item["verified_architectures"]) {
				if !semHas([]string{"domain-driven", "layered-mvc"}, family) {
					return nil, s.reject("PLATFORM_COMPONENT", "未知验证架构")
				}
			}
			coordinates := map[string]bool{}
			for _, v := range semList(item["artifacts"]) {
				a := semMap(v)
				key := text(a["group_id"]) + ":" + text(a["artifact_id"])
				if text(a["group_id"]) == "" || text(a["artifact_id"]) == "" || !contractExactVersion(a["declared_version"]) || coordinates[key] || a["resolved_version"] != nil && !contractExactVersion(a["resolved_version"]) || strings.HasSuffix(text(a["declared_version"]), "-SNAPSHOT") && a["resolved_version"] != nil && (a["resolved_version"] == a["declared_version"] || strings.HasSuffix(text(a["resolved_version"]), "-SNAPSHOT")) {
					return nil, s.reject("PLATFORM_COMPONENT", "组件精确坐标冲突")
				}
				coordinates[key] = true
				if text(item["provider_kind"]) == "platform-managed" && (id != "contract.request-validation" || len(semList(item["artifacts"])) != 1 || text(a["group_id"]) != "org.springframework.boot" || text(a["artifact_id"]) != "spring-boot-starter-validation" || a["declared_version"] != profile["spring_boot_version"]) {
					return nil, s.reject("PLATFORM_COMPONENT", "平台托管validation坐标冲突")
				}
				if entry["artifact_resolution_evidence"] != nil {
					r := resolutions[key+":"+text(a["declared_version"])]
					if r == nil {
						return nil, s.reject("PLATFORM_COMPONENT", "组件没有实际解析条目")
					}
					var pom, jar, tree any
					if text(r["pom_sha256"]) != "" {
						pom = "sha256:" + text(r["pom_sha256"])
					}
					if text(r["jar_sha256"]) != "" {
						jar = "sha256:" + text(r["jar_sha256"])
					}
					if r["sources_match_source_tree"] == true && r["pom_matches_source_tree"] == true {
						tree = r["source_tree"]
					}
					if !contractSame(a["resolved_version"], r["resolved_version"]) || !contractSame(a["pom_sha256"], pom) || !contractSame(a["jar_sha256"], jar) || !contractSame(a["source_tree"], tree) {
						return nil, s.reject("PLATFORM_COMPONENT", "组件绑定与解析证据漂移")
					}
				}
				if text(item["status"]) == "verified" && (!contractExactVersion(a["resolved_version"]) || !contractSHA(a["pom_sha256"]) || !contractSHA(a["jar_sha256"]) || !contractGitTree(a["source_tree"]) || !strings.HasPrefix(text(a["declared_version"]), strconv.Itoa(major)+".")) {
					return nil, s.reject("PLATFORM_COMPONENT", "verified组件缺少精确代际证据")
				}
			}
			recipe := contractWithout(item, "capability_id", "component_digest", "status", "evidence", "blockers")
			recipe["capability_id"] = id
			if !contractSHA(item["component_digest"]) || text(item["component_digest"]) != contractDigest(recipe) {
				return nil, s.reject("PLATFORM_COMPONENT", "组件配方摘要变化")
			}
			if text(item["status"]) == "verified" {
				if text(entry["status"]) != "verified" || len(semList(item["verified_architectures"])) == 0 || len(semList(item["artifacts"])) == 0 || len(semList(item["evidence"])) == 0 || len(semList(item["blockers"])) != 0 {
					return nil, s.reject("PLATFORM_COMPONENT", "verified组件与平台状态或证据冲突")
				}
				for _, v := range semList(item["evidence"]) {
					ev := semMap(v)
					if !contractPath(text(ev["ref"])) || !contractSHA(ev["digest"]) || !semHas(item["verified_architectures"], text(ev["architecture_family"])) {
						return nil, s.reject("PLATFORM_COMPONENT", "组件证据不可移植或架构不匹配")
					}
				}
			} else if len(semList(item["blockers"])) == 0 {
				return nil, s.reject("PLATFORM_COMPONENT", "未验证能力必须记录阻断")
			}
		}
		if line == "boot3-java17" {
			if text(profile["spring_cloud_version"]) != "2025.0.3" || text(profile["spring_cloud_alibaba_version"]) != "2025.0.0.0" || text(profile["validation_namespace"]) != "jakarta" || contractN(profile["jackson_major"]) != 2 || text(profile["auto_configuration_imports_path"]) != "META-INF/spring/org.springframework.boot.autoconfigure.AutoConfiguration.imports" || entry["platform_artifact_bindings"] == nil || !apIsArray(entry["external_snapshot_bindings"]) || !apIsArray(entry["external_artifact_bindings"]) || len(semList(entry["external_snapshot_bindings"])) != 0 {
				return nil, s.reject("PLATFORM_COMPONENT", "Boot3固定平台政策变化")
			}
			expected := map[string]string{"contract.request-validation": "org.springframework.boot:spring-boot-starter-validation:" + text(profile["spring_boot_version"]), "component.cache": "com.yss.cloud:yss-component-cache-starter:3.1.0-SNAPSHOT", "component.distributed-id": "com.yss.cloud:yss-component-distributed-id:3.1.0-SNAPSHOT", "component.excel-import-export": "com.yss.cloud:yss-component-excel-mvc:3.0.0-SNAPSHOT"}
			for id, coordinate := range expected {
				a := semList(semMap(components[id])["artifacts"])
				if len(a) != 1 {
					return nil, s.reject("PLATFORM_COMPONENT", "Boot3组件坐标缺失")
				}
				b := semMap(a[0])
				if text(b["group_id"])+":"+text(b["artifact_id"])+":"+text(b["declared_version"]) != coordinate {
					return nil, s.reject("PLATFORM_COMPONENT", "Boot3组件固定坐标变化")
				}
			}
			for _, raw := range components {
				item := semMap(raw)
				if text(item["status"]) != "blocked" || len(semList(item["verified_architectures"])) != 0 || len(semList(item["evidence"])) != 0 {
					return nil, s.reject("PLATFORM_COMPONENT", "Boot3未发布组件必须保持blocked")
				}
				for _, v := range semList(item["artifacts"]) {
					a := semMap(v)
					if text(a["artifact_id"]) == "yss-component-excel-starter" {
						return nil, s.reject("PLATFORM_COMPONENT", "移除的Excel starter重新出现")
					}
					for _, key := range []string{"resolved_version", "pom_sha256", "jar_sha256", "source_tree"} {
						value, exists := a[key]
						if !exists || value != nil {
							return nil, s.reject("PLATFORM_COMPONENT", "Boot3未发布组件不得解析")
						}
					}
				}
			}
			external := semList(entry["external_artifact_bindings"])
			if len(external) != 1 {
				return nil, s.reject("PLATFORM_COMPONENT", "Boot3 Fesod外部发布绑定缺失")
			}
			a := semMap(external[0])
			if text(a["group_id"])+":"+text(a["artifact_id"])+":"+text(a["declared_version"]) != "org.apache.fesod:fesod-sheet:2.0.2-incubating" || text(a["status"]) != "resolved" {
				return nil, s.reject("PLATFORM_COMPONENT", "Boot3 Fesod固定发布坐标变化")
			}
		}
	}
	return c, nil
}

func init() {
	registerSemanticValidator("slice", verifySliceSemantic)
	registerSemanticValidator("scaffold", verifyScaffoldSemantic)
	registerSemanticValidator("business-checkpoint", func(s *semanticSession, ref string, opts map[string]string) error {
		cp, e := s.doc(ref)
		if e != nil {
			return e
		}
		return contractBusinessCheckpoint(s, cp, opts["required"] == "true")
	})
	registerSemanticValidator("frontend-delivery", verifyFrontendDeliverySemantic)
	registerSemanticValidator("technical-completion", func(s *semanticSession, ref string, opts map[string]string) error {
		doc, e := s.doc(ref)
		if e != nil {
			return e
		}
		return contractTechnicalDesign(s, ref, doc, nil, opts)
	})
	registerSemanticValidator("repository-ready", verifyRepositoryReadySemantic)
	registerSemanticValidator("technical-transition", contractTechnicalTransition)
	registerSemanticValidator("service-transition", contractServiceTransition)
}
func contractTechnicalTransition(s *semanticSession, ref string, opts map[string]string) error {
	state, e := s.transitionState(ref, opts["task-ref"])
	if e != nil {
		return e
	}
	if result, ok := object(state["technical_analysis_result"]); ok {
		resultRef := text(state["technical_analysis_result_ref"])
		persisted, e := s.doc(resultRef)
		if e != nil {
			return e
		}
		for _, d := range []map[string]any{result, persisted} {
			if text(d["result_schema"]) != "workflow-execution-result-v1" || text(d["work_unit"]) != "work-unit.technical-analysis" || text(d["result"]) != "completed" || d["current_version"] != true {
				return s.reject("TECHNICAL_TRANSITION", "缺少当前已完成技术分析执行结果")
			}
		}
		if !semHas(result["evidence_refs"], resultRef) {
			return s.reject("TECHNICAL_TRANSITION", "技术分析证据没有持久化结果")
		}
		if e = contractEvidence(s, result["evidence_refs"]); e != nil {
			return e
		}
		reconciliation := semMap(result["context_reconciliation"])
		if text(reconciliation["status"]) != "reconciled" || text(reconciliation["ref"]) == "" {
			return s.reject("TECHNICAL_TRANSITION", "技术分析Context未对账")
		}
		if e = contractTransitionContext(s, reconciliation, opts); e != nil {
			return e
		}
		api := semMap(result["api_contract_decision"])
		if !contractSame(api, semMap(persisted["api_contract_decision"])) || !semHas(result["evidence_refs"], text(api["ref"])) {
			return s.reject("TECHNICAL_TRANSITION", "技术分析当前API决定绑定不一致")
		}
		_, e = contractAPIDecision(s, api)
		return e
	}
	reconciliation := semMap(state["context_reconciliation"])
	if text(reconciliation["status"]) != "reconciled" || text(reconciliation["ref"]) == "" {
		return s.reject("TECHNICAL_TRANSITION", "后端技术Context未对账")
	}
	if e = contractTransitionContext(s, reconciliation, opts); e != nil {
		return e
	}
	handoff, spec := semMap(state["strategic_handoff_consumption"]), semMap(state["approved_spec"])
	if text(handoff["result"]) == "inputs-verified" && handoff["ready_for_agent"] == false && text(handoff["ref"]) != "" {
		consumed, e := s.doc(text(handoff["ref"]))
		if e != nil {
			return e
		}
		if e = contractHandoffConsumption(s, consumed, map[string]string{"consumer": "tactical", "inputs-only": "true"}); e != nil {
			return e
		}
	} else if text(spec["status"]) == "approved" && spec["current_version"] == true && text(spec["ref"]) != "" {
		if _, e = s.bytes(text(spec["ref"])); e != nil {
			return e
		}
	} else {
		return s.reject("TECHNICAL_TRANSITION", "缺少当前批准Spec或战略接收")
	}
	prereqs := map[string]any{"technical_design": state["technical_design"], "data_architecture_decision": state["data_architecture_decision"], "api_contract_decision": state["api_contract_decision"], "engineering_contract_approval_ref": state["engineering_contract_approval_ref"]}
	for _, key := range []string{"technical_design", "data_architecture_decision", "api_contract_decision"} {
		binding := semMap(prereqs[key])
		if text(binding["status"]) != "approved" || binding["current_version"] != true || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(text(binding["version"])) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(text(binding["digest"])) {
			return s.reject("TECHNICAL_TRANSITION", "缺少当前正式批准技术绑定: "+key)
		}
	}
	transitionOpts := semanticOptions(opts)
	transitionOpts["project-id"] = first(text(state["project_id"]), "backend")
	return contractBackendPrerequisites(s, prereqs, nil, transitionOpts)
}
func contractTransitionContext(s *semanticSession, reconciliation map[string]any, opts map[string]string) error {
	if text(reconciliation["status"]) != "reconciled" {
		return s.reject("CONTEXT", "流转 Context 未对账")
	}
	if text(reconciliation["ref"]) == "CONTEXT.md" {
		current, e := s.contextContract()
		if e != nil {
			return e
		}
		if snapshot, ok := object(reconciliation["snapshot"]); ok {
			_, e = s.contextSnapshot(snapshot)
			return e
		}
		if digest := text(reconciliation["document_digest"]); digest != "" && digest != text(current["document_digest"]) {
			return s.reject("CONTEXT_DRIFT", "流转 Context 摘要变化")
		}
		return nil
	}
	return s.verify("context-reconciliation", text(reconciliation["ref"]), opts)
}
func contractServiceTransition(s *semanticSession, ref string, opts map[string]string) error {
	state, e := s.transitionState(ref, opts["task-ref"])
	if e != nil {
		return e
	}
	request := semMap(state["service_project_initialization"])
	if e = contractRequired(s, request, "project_id", "contract_ref", "contract_digest", "project_root"); e != nil {
		return e
	}
	contractRef, e := contractVerificationRef(s, text(request["contract_ref"]))
	if e != nil {
		return e
	}
	contract, e := contractBoundDoc(s, map[string]any{"ref": contractRef, "digest": request["contract_digest"]})
	if e != nil {
		return e
	}
	if e = s.verify("scaffold", contractRef, opts); e != nil {
		return e
	}
	projectRoot := text(request["project_root"])
	if !filepath.IsAbs(projectRoot) || filepath.Clean(projectRoot) != projectRoot || filepath.Dir(projectRoot) != text(contract["target_output_dir"]) {
		return s.reject("SERVICE_INITIALIZATION", "服务目标必须是批准输出目录内的规范绝对工程根")
	}
	if text(contract["architecture_profile"]) != "mvc-data-analysis-v1" || text(contract["scaffold_kind"]) != "backend-mvc-data-analysis" || text(contract["generator_skill"]) != "yss-layered-mvc-scaffold-generator" || contract["init_git"] != true || contract["project_name"] != request["project_id"] || filepath.Join(text(contract["target_output_dir"]), text(contract["project_name"])) != projectRoot {
		return s.reject("SERVICE_INITIALIZATION", "服务初始化合同身份或目标不一致")
	}
	handoffRef := path.Join(path.Dir(contractRef), text(contract["context_handoff_ref"]))
	if !contractWithin(handoffRef, path.Dir(contractRef)) || handoffRef == path.Dir(contractRef) || filepath.IsAbs(text(contract["context_handoff_ref"])) {
		return s.reject("SERVICE_INITIALIZATION", "CONTEXT交接必须位于合同目录内部")
	}
	handoff, e := s.bytes(handoffRef)
	if e != nil {
		return e
	}
	digest := "sha256:" + safefs.Digest(handoff)
	if text(contract["context_handoff_digest"]) != digest || text(request["context_handoff_digest"]) != digest {
		return s.reject("SERVICE_INITIALIZATION", "CONTEXT交接摘要变化")
	}
	parent := filepath.Dir(projectRoot)
	if opts["completed"] != "true" {
		return s.observedExternalAbsence(projectRoot, contractRef)
	}
	if e = s.registerExternalRoot(parent, contractRef); e != nil {
		return e
	}
	child := newSemanticSession(s.ctx, parent, s.args)
	child.v = s.externalViews[parent]
	s.children = append(s.children, child)
	name := filepath.Base(projectRoot)
	// Read the contract-owned files below individually. A recursive inventory
	// of the project would wrongly treat its independently required .git tree
	// as a governance asset and violate the safe reference policy.
	manifestRef, e := contractVerificationRef(s, text(request["manifest_ref"]))
	if e != nil {
		return e
	}
	verificationRef, e := contractVerificationRef(s, text(request["verification_ref"]))
	if e != nil {
		return e
	}
	resultRef, e := contractVerificationRef(s, text(request["result_ref"]))
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
	if text(result["result_schema"]) != "workflow-execution-result-v1" || text(result["work_unit"]) != "work-unit.service-project-initialization" || text(result["result"]) != "completed" || !semHas(result["evidence_refs"], text(request["manifest_ref"])) || !semHas(result["evidence_refs"], text(request["verification_ref"])) {
		return s.reject("SERVICE_INITIALIZATION", "缺少持久化已完成初始化执行结果")
	}
	if contractN(manifest["schema_version"]) != 4 || text(manifest["kind"]) != "service-project-initialization" || text(manifest["architecture_profile"]) != "mvc-data-analysis-v1" || manifest["project_name"] != request["project_id"] || manifest["contract_digest"] != request["contract_digest"] || text(manifest["completion_level"]) != "empty-scaffold-verified" {
		return s.reject("SERVICE_INITIALIZATION", "服务Manifest不是当前已验证空骨架")
	}
	if text(verification["status"]) != "passed" || text(verification["completion_level"]) != "empty-scaffold-verified" || text(verification["project_root"]) != projectRoot {
		return s.reject("SERVICE_INITIALIZATION", "服务验证身份或完成度错误")
	}
	for _, phase := range []string{"validate", "test", "package"} {
		found := false
		for _, v := range semList(verification["commands"]) {
			row := semMap(v)
			if text(row["phase"]) != phase {
				continue
			}
			exit, ok := integer(row["exit_code"])
			if !ok || exit != 0 || !strings.HasPrefix(text(row["command"]), "./mvnw "+phase) {
				return s.reject("SERVICE_INITIALIZATION", "Maven实际执行结果未通过")
			}
			for _, key := range []string{"stdout_ref", "stderr_ref"} {
				ref, e := contractVerificationRef(s, text(row[key]))
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
			return s.reject("SERVICE_INITIALIZATION", "缺少Maven阶段: "+phase)
		}
	}
	context, e := child.bytes(path.Join(name, "CONTEXT.md"))
	if e != nil {
		return e
	}
	if string(context) != string(handoff) {
		return s.reject("SERVICE_INITIALIZATION", "子项目CONTEXT与批准交接不一致")
	}
	if e = s.registerExternalRoot(projectRoot, contractRef); e != nil {
		return e
	}
	top, e := s.git(projectRoot, "rev-parse", "--show-toplevel")
	if e != nil || filepath.Clean(strings.TrimSpace(string(top))) != projectRoot {
		return s.reject("SERVICE_INITIALIZATION", "子工程Git身份缺失")
	}
	if _, e = child.bytes("skillUtils/skills-lock.json"); e != nil {
		return e
	}
	initialization, e := child.doc(path.Join(name, ".template-spec/process/service-initialization.json"))
	if e != nil {
		return e
	}
	if initialization["contract_id"] != contract["contract_id"] || text(initialization["context_handoff_digest"]) != digest {
		return s.reject("SERVICE_INITIALIZATION", "子工程初始化身份变化")
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
			writeContextJSONString(&b, x)
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
				return utf16Less(keys[i], keys[j])
			})
			b.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					b.WriteByte(',')
				}
				writeContextJSONString(&b, k)
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
func contractN(v any) int { n, _ := integer(v); return int(n) }
func contractVersion(v any) string {
	if x, ok := v.(string); ok {
		return x
	}
	return fmt.Sprint(v)
}
func contractRequired(s *semanticSession, m map[string]any, keys ...string) error {
	for _, k := range keys {
		if v, ok := m[k]; !ok || v == nil || v == "" {
			return s.reject("CONTRACT_INVALID", "缺少 "+k)
		}
	}
	return nil
}
func contractUnique(v any, nonempty bool) bool {
	a := semStrings(v)
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
func contractPath(v string) bool {
	if v == "" || filepath.IsAbs(v) || strings.ContainsAny(v, "\\\x00\n\r\t*?{}[]") || strings.Contains(v, ":") {
		return false
	}
	for _, p := range strings.Split(v, "/") {
		if p == ".." || p == "" || strings.TrimRight(p, " .") != p {
			return false
		}
	}
	return true
}
func contractDate(v any) bool { _, e := time.Parse(time.RFC3339Nano, text(v)); return e == nil }
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
func contractBinding(s *semanticSession, v any) (map[string]any, error) {
	m, ok := object(v)
	if !ok {
		return nil, s.reject("BINDING_INVALID", "依据须为 ref/digest 对象")
	}
	ref, d := text(m["ref"]), text(m["digest"])
	if ref == "" || !strings.HasPrefix(d, "sha256:") || !isSHA256(strings.TrimPrefix(d, "sha256:")) {
		return nil, s.reject("BINDING_INVALID", "依据引用或摘要非法")
	}
	observedRef := ref
	if filepath.IsAbs(ref) {
		var e error
		observedRef, e = contractVerificationRef(s, ref)
		if e != nil {
			return nil, e
		}
	}
	b, e := s.bytes(observedRef)
	if e != nil {
		return nil, e
	}
	if "sha256:"+safefs.Digest(b) != d {
		return nil, s.reject("CONTRACT_STALE", "依据原始字节变化: "+ref)
	}
	return m, nil
}
func contractBoundDoc(s *semanticSession, v any) (map[string]any, error) {
	m, e := contractBinding(s, v)
	if e != nil {
		return nil, e
	}
	ref := text(m["ref"])
	if filepath.IsAbs(ref) {
		ref, e = contractVerificationRef(s, ref)
		if e != nil {
			return nil, e
		}
	}
	return s.doc(ref)
}
func contractEvidence(s *semanticSession, refs any) error {
	if !contractUnique(refs, true) {
		return s.reject("EVIDENCE_REQUIRED", "证据引用须非空、唯一")
	}
	for _, ref := range semStrings(refs) {
		if filepath.IsAbs(ref) {
			var e error
			ref, e = contractVerificationRef(s, ref)
			if e != nil {
				return e
			}
		}
		if _, e := s.bytes(ref); e != nil {
			return fmt.Errorf("evidence %s: %w", ref, e)
		}
	}
	return nil
}
func contractBasis(s *semanticSession, raw map[string]any) (map[string]map[string]any, error) {
	basis := semMap(raw["basis"])
	result := map[string]map[string]any{}
	var resolve func(string, map[string]bool) (map[string]any, error)
	resolve = func(key string, stack map[string]bool) (map[string]any, error) {
		if stack[key] {
			return nil, s.reject("REFERENCE_CYCLE", "依据别名循环: "+key)
		}
		if m := result[key]; m != nil {
			return m, nil
		}
		v, ok := basis[key]
		if !ok {
			return nil, s.reject("CONTRACT_INVALID", "缺少依据: "+key)
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
func contractNeedBasis(s *semanticSession, b map[string]map[string]any, keys ...string) error {
	for _, k := range keys {
		if b[k] == nil {
			return s.reject("CONTRACT_INVALID", "缺少依据: "+k)
		}
	}
	return nil
}

type contractPlanEntry struct {
	ID, Kind string
	Fields   map[string]string
}

func contractPlan(s *semanticSession, ref string) ([]contractPlanEntry, bool, error) {
	raw, e := s.bytes(ref)
	if e != nil {
		return nil, false, e
	}
	fm, body, e := frontmatter(raw)
	if e != nil {
		return nil, false, e
	}
	v, e := schema.Parse(fm)
	if e != nil {
		return nil, false, e
	}
	supported := text(semMap(v)["content_profile"]) == "plan-spec-v1"
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
func contractLocate(s *semanticSession, ref, locator string) error {
	b, e := s.bytes(ref)
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
			return s.reject("LOCATOR_INVALID", locator)
		}
		for _, k := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			k = strings.ReplaceAll(strings.ReplaceAll(k, "~1", "/"), "~0", "~")
			switch x := v.(type) {
			case map[string]any:
				var ok bool
				v, ok = x[k]
				if !ok {
					return s.reject("LOCATOR_INVALID", locator)
				}
			case []any:
				i, err := strconv.Atoi(k)
				if err != nil || i < 0 || i >= len(x) {
					return s.reject("LOCATOR_INVALID", locator)
				}
				v = x[i]
			default:
				return s.reject("LOCATOR_INVALID", locator)
			}
		}
		return nil
	}
	if m := regexp.MustCompile(`^lines:(\d+)-(\d+)$`).FindStringSubmatch(locator); m != nil {
		a, _ := strconv.Atoi(m[1])
		z, _ := strconv.Atoi(m[2])
		if a < 1 || z < a || z > len(strings.Split(string(b), "\n")) {
			return s.reject("LOCATOR_INVALID", locator)
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
				return s.reject("LOCATOR_INVALID", "验收 ID 缺失或不唯一: "+locator)
			}
			return nil
		}
	}
	body := string(b)
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		var rawBody []byte
		_, rawBody, e = frontmatter(b)
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
		return s.reject("LOCATOR_INVALID", "定位缺失或不唯一: "+locator)
	}
	return nil
}

type nativeSlice struct {
	Raw, Normalized map[string]any
	Basis           map[string]map[string]any
	Ref             string
	Repositories    map[string]map[string]any
}

func loadNativeSlice(s *semanticSession, ref string) (*nativeSlice, error) {
	doc, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	raw := doc
	if m, ok := object(doc["slice_contract"]); ok {
		raw = m
	}
	n := contractN(raw["schema_version"])
	if n != 2 && n != 3 {
		return nil, s.reject("CONTRACT_SCHEMA", "Slice schema 仅支持 v2/v3")
	}
	out := &nativeSlice{Raw: raw, Normalized: contractCopy(raw), Ref: ref, Basis: map[string]map[string]any{}, Repositories: map[string]map[string]any{}}
	if n == 2 {
		version, e := contractBusinessVersion(s)
		if e != nil {
			return nil, e
		}
		if version == 1 {
			return nil, s.reject("BUSINESS_SLICE_V3_REQUIRED", "当前业务协议要求 v3")
		}
		ticket := text(semMap(raw["lifecycle_refs"])["ticket"])
		if ticket != "" {
			if e = contractImplementationTicket(s, ticket); e != nil {
				return nil, e
			}
		}
		compiler, e := s.doc(".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml")
		if e != nil {
			return nil, e
		}
		for section, fields := range semMap(compiler["slice_contract_required"]) {
			target := raw
			if section != "root" {
				target = semMap(raw[section])
			}
			for _, field := range semStrings(fields) {
				if _, ok := target[field]; !ok {
					return nil, s.reject("CONTRACT_INVALID", "缺少 "+section+"."+field)
				}
			}
		}
		for _, section := range []string{"common", "resolution", "backend"} {
			if _, ok := semMap(raw[section])["required_skills"].([]any); !ok {
				return nil, s.reject("CONTRACT_INVALID", section+".required_skills 须为数组")
			}
		}
		units := semList(raw["work_units"])
		if len(units) == 0 {
			return nil, s.reject("CONTRACT_INVALID", "缺少工作单元")
		}
		for _, v := range units {
			u := semMap(v)
			if !contractSame(u["contract_id"], raw["contract_id"]) || !contractSame(u["contract_version"], raw["contract_version"]) || text(semMap(u["work_unit"])["primary_skill"]) == "" {
				return nil, s.reject("CONTRACT_INVALID", "工作单元合同身份不一致")
			}
		}
		return out, nil
	}
	if e = s.validateSchema(".template-spec/process/schemas/slice-implementation-contract-v3.schema.json", raw); e != nil {
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
	if e = contractImplementationTicket(s, text(basis["ticket"]["ref"])); e != nil {
		return nil, e
	}
	if e = contractSliceBusiness(s, raw, basis); e != nil {
		return nil, e
	}
	scope, resolution, extension, app := semMap(raw["scope"]), contractCopy(semMap(raw["resolution"])), semMap(raw["extensions"]), semMap(raw["applicability"])
	if app["checks"] == nil {
		if e = contractNeedBasis(s, basis, "architecture_review"); e != nil {
			return nil, e
		}
	} else {
		if e = contractNeedBasis(s, basis, "lifecycle_registry", "process_tailoring"); e != nil {
			return nil, e
		}
		reg, e := s.doc(text(basis["lifecycle_registry"]["ref"]))
		if e != nil {
			return nil, e
		}
		check := apFind(reg["checks"], "id", "check.architecture-reviewed")
		if text(check["trigger"]) != "高风险或跨边界变化。" {
			return nil, s.unavailable("CAPABILITY", "未知架构审查触发规则")
		}
		required := text(scope["risk_level"]) == "high" || len(semStrings(scope["project_roots"])) > 1
		for _, impact := range []string{"high-risk", "architecture", "cross-repo", "cross-boundary"} {
			required = required || semHas(scope["impacted_areas"], impact)
		}
		if !semHas([]string{"low", "medium", "high"}, text(scope["risk_level"])) {
			return nil, s.reject("APPLICABILITY", "未知风险等级")
		}
		for _, impact := range semStrings(scope["impacted_areas"]) {
			if !semHas([]string{"ui", "frontend", "backend", "api", "data", "persistence", "domain", "architecture", "cross-repo", "cross-boundary", "high-risk"}, impact) {
				return nil, s.reject("APPLICABILITY", "未知影响")
			}
		}
		reason := fmt.Sprintf("%s 本切片风险 %s，影响 %s，工程数 %d。", text(check["trigger"]), text(scope["risk_level"]), first(strings.Join(semStrings(scope["impacted_areas"]), ", "), "无"), len(semStrings(scope["project_roots"])))
		expected := map[string]any{"check.architecture-reviewed": map[string]any{"status": "not-applicable", "reason": reason}}
		if required {
			expected = map[string]any{"check.architecture-reviewed": map[string]any{"status": "required", "reason": reason}}
			if e = contractNeedBasis(s, basis, "architecture_review"); e != nil {
				return nil, e
			}
		}
		if !contractSame(app["checks"], expected) {
			return nil, s.reject("APPLICABILITY", "材料适用性与生命周期规则冲突")
		}
	}
	for _, key := range []string{"frontend", "backend", "api", "cross_repo"} {
		a := semMap(app[key])
		if text(a["status"]) == "required" {
			if extension[key] == nil {
				return nil, s.reject("APPLICABILITY", key+" 缺少子合同")
			}
		} else if text(a["reason"]) == "" || extension[key] != nil {
			return nil, s.reject("APPLICABILITY", key+" 不适用理由或子合同冲突")
		}
	}
	for impact, key := range map[string]string{"frontend": "frontend", "ui": "frontend", "backend": "backend", "api": "api", "cross-repo": "cross_repo"} {
		if semHas(scope["impacted_areas"], impact) && text(semMap(app[key])["status"]) != "required" {
			return nil, s.reject("APPLICABILITY", impact+" 影响与适用性冲突")
		}
	}
	if semHas(scope["impacted_areas"], "data") || semHas(scope["impacted_areas"], "persistence") {
		if e = contractNeedBasis(s, basis, "data_architecture"); e != nil {
			return nil, e
		}
	}
	if raw["ticket_policy"] != nil && text(basis["ticket"]["version"]) == "" {
		return nil, s.reject("CONTRACT_INVALID", "冻结 Ticket 缺少版本")
	}
	if raw["ticket_policy"] != nil && len(semStrings(scope["project_roots"])) > 1 && semMap(extension["cross_repo"])["repository_bindings"] == nil {
		return nil, s.reject("REPOSITORY", "跨仓合同缺少逐仓登记")
	}
	if e = contractSliceRepositories(s, out); e != nil {
		return nil, e
	}
	if extension["frontend"] != nil {
		if text(semMap(app["frontend"])["baseline_kind"]) == "existing-ui-baseline" {
			if text(semMap(app["frontend"])["ui_change"]) != "none" || semHas(scope["impacted_areas"], "ui") {
				return nil, s.reject("APPLICABILITY", "既有 UI 不能承接 UI 变化")
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
			baseline, e := contractExistingUI(s, text(basis["existing_ui_baseline"]["ref"]))
			if e != nil {
				return nil, e
			}
			ids := []string{}
			for _, v := range semList(baseline["cases"]) {
				ids = append(ids, text(semMap(v)["case_id"]))
			}
			for _, id := range semStrings(semMap(extension["frontend"])["visual_baseline_case_ids"]) {
				if !semHas(ids, id) {
					return nil, s.reject("FRONTEND_BASELINE", "未登记既有 UI 用例")
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
			return nil, s.reject("ARCHITECTURE", "后端缺少架构身份")
		}
		if text(basis["technical_design"]["version"]) == "" {
			return nil, s.reject("TECHNICAL_DESIGN", "技术设计版本缺失")
		}
		for _, locator := range semStrings(semMap(extension["backend"])["design_refs"]) {
			if e = contractLocate(s, text(basis["technical_design"]["ref"]), locator); e != nil {
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
		return nil, s.reject("APPLICABILITY", "API 影响结论冲突")
	}
	for _, p := range semStrings(scope["allowed_write_paths"]) {
		if !contractPath(p) {
			return nil, s.reject("PATH", "写范围须为明确相对路径: "+p)
		}
	}
	for _, p := range semStrings(scope["project_roots"]) {
		if regexp.MustCompile(`^app/(backend|frontend)(/|$)`).MatchString(p) {
			return nil, s.reject("PATH", "禁止 app/backend 或 app/frontend")
		}
		if text(scope["implementation_path_policy"]) != "external-repository-native" && !regexp.MustCompile(`^apps/(backend|frontend)/[^/]+`).MatchString(p) {
			return nil, s.reject("PATH", "Harness 工程需具体 project 根")
		}
	}
	acceptance, verification := semMap(raw["acceptance"]), semMap(raw["verification"])
	for id, v := range acceptance {
		a := semMap(v)
		src := basis[text(a["source"])]
		if src == nil {
			return nil, s.reject("ACCEPTANCE", "验收来源缺失: "+id)
		}
		if e = contractLocate(s, text(src["ref"]), text(a["locator"])); e != nil {
			return nil, e
		}
	}
	for id, v := range verification {
		check := semMap(v)
		registered := false
		for _, root := range semStrings(scope["project_roots"]) {
			registered = registered || contractWithin(text(check["cwd"]), root)
		}
		if !registered {
			return nil, s.reject("VERIFICATION", "cwd 未登记: "+id)
		}
		for _, root := range semStrings(check["dependency_roots"]) {
			if out.Repositories[root] == nil {
				return nil, s.reject("REPOSITORY", "验证依赖根未登记")
			}
		}
		if !contractUnique(check["acceptance_refs"], true) {
			return nil, s.reject("ACCEPTANCE", "验证验收引用非法")
		}
		for _, a := range semStrings(check["acceptance_refs"]) {
			if acceptance[a] == nil {
				return nil, s.reject("ACCEPTANCE", "未知验收引用: "+a)
			}
		}
	}
	units := []any{}
	seen, used, covered := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, v := range semList(raw["work_units"]) {
		u := contractCopy(semMap(v))
		id := text(u["id"])
		if seen[id] {
			return nil, s.reject("WORK_UNIT", "工作单元重复")
		}
		seen[id] = true
		allowed := u["allowed_write_paths"]
		if allowed == nil {
			allowed = scope["allowed_write_paths"]
		}
		for _, p := range semStrings(allowed) {
			within := false
			for _, parent := range semStrings(scope["allowed_write_paths"]) {
				within = within || contractWithin(p, parent)
			}
			if !contractPath(p) || !within {
				return nil, s.reject("PATH", "工作单元写范围越界")
			}
		}
		root := text(u["project_root"])
		if root == "" && len(semStrings(scope["project_roots"])) == 1 {
			root = semStrings(scope["project_roots"])[0]
		}
		if !semHas(scope["project_roots"], root) {
			return nil, s.reject("REPOSITORY", "工作单元须选择工程根")
		}
		ids := []string{}
		for id, v := range verification {
			if semMap(v)["required_for_all"] == true {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		ids = contractUnion(ids, semStrings(u["verification_refs"]))
		checks := []any{}
		commands, evidence := []string{}, []string{}
		for _, id := range ids {
			v := verification[id]
			if v == nil || !contractWithin(text(semMap(v)["cwd"]), root) {
				return nil, s.reject("VERIFICATION", "未知验证或执行工程冲突")
			}
			c := contractCopy(semMap(v))
			c["id"] = id
			checks = append(checks, c)
			commands = append(commands, text(c["command"]))
			evidence = contractUnion(evidence, semStrings(c["expected_evidence"]))
			used[id] = true
		}
		for _, skill := range append([]string{text(u["primary_skill"])}, semStrings(u["supporting_skills"])...) {
			if !semHas(resolution["required_skills"], skill) {
				return nil, s.reject("SKILL_CLOSURE", "工作单元 Skill 未冻结")
			}
		}
		for _, id := range semStrings(u["acceptance_refs"]) {
			valid := false
			for _, v := range checks {
				valid = valid || semHas(semMap(v)["acceptance_refs"], id)
			}
			if acceptance[id] == nil || !valid {
				return nil, s.reject("ACCEPTANCE", "工作单元验收缺少验证覆盖")
			}
			covered[id] = true
		}
		if text(u["tdd_mode"]) == "controlled-generation" {
			if e = contractRequired(s, semMap(u["controlled_generation"]), "exception_reason", "generator", "generator_inputs", "expected_files", "verification_commands", "behavior_tests_after_generation"); e != nil {
				return nil, e
			}
		} else if u["controlled_generation"] != nil {
			return nil, s.reject("TDD", "行为测试不得携带受控生成例外")
		}
		u["allowed_write_paths"], u["project_root"], u["verification"], u["verification_commands"], u["expected_evidence"] = allowed, root, checks, commands, evidence
		u["supporting_skills"] = semStrings(u["supporting_skills"])
		u["forbidden_patterns"] = contractUnion(semStrings(scope["forbidden_patterns"]), semStrings(u["forbidden_patterns"]))
		row := contractCopy(u)
		row["contract_id"], row["contract_version"], row["work_unit"] = raw["contract_id"], raw["contract_version"], u
		units = append(units, row)
	}
	for id := range acceptance {
		if !covered[id] {
			return nil, s.reject("ACCEPTANCE", "未分配验收: "+id)
		}
	}
	for id := range verification {
		if !used[id] {
			return nil, s.reject("VERIFICATION", "未分配验证: "+id)
		}
	}
	for _, id := range semStrings(semMap(extension["cross_repo"])["integration_verification"]) {
		if len(semStrings(semMap(verification[id])["dependency_roots"])) == 0 {
			return nil, s.reject("VERIFICATION", "联合验证须显式依赖根")
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
		c := semMap(v)
		allCommands = append(allCommands, text(c["command"]))
		allEvidence = contractUnion(allEvidence, semStrings(c["expected_evidence"]))
	}
	common["verification_commands"], common["expected_evidence_files"] = allCommands, allEvidence
	out.Normalized["common"], out.Normalized["resolution"], out.Normalized["lifecycle_refs"], out.Normalized["work_units"] = common, resolution, refs, units
	out.Normalized["readiness"] = map[string]any{"blockers": []any{}, "stale_inputs": []any{}}
	for _, key := range []string{"backend", "frontend"} {
		m := contractCopy(semMap(extension[key]))
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

func contractTicketMetadata(s *semanticSession, ref string) (map[string]any, error) {
	raw, e := s.bytes(ref)
	if e != nil {
		return nil, e
	}
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
	hasHeader := strings.HasPrefix(string(raw), "---\n") || strings.HasPrefix(string(raw), "---\r\n")
	if hasHeader {
		raw, _, e = frontmatter(raw)
		if e != nil {
			return nil, s.unavailable("INPUT", ref+": "+e.Error())
		}
	}
	v, e := schema.Parse(raw)
	if e != nil && hasHeader {
		return nil, s.unavailable("INPUT", ref+": "+e.Error())
	}
	m, _ := object(v)
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}
func contractBusinessVersion(s *semanticSession) (int, error) {
	identity, e := s.doc("yss-project.yaml")
	if e != nil {
		return 0, e
	}
	if text(identity["repository_mode"]) == "template-source" {
		return 0, nil
	}
	exists, e := s.exists(".template-spec/agents/issue-tracker.md")
	if e != nil || !exists {
		return 0, e
	}
	tracker, e := contractTicketMetadata(s, ".template-spec/agents/issue-tracker.md")
	if e != nil {
		return 0, e
	}
	value, present := semMap(tracker["tracker"])["business_ticket_version"]
	if present && contractN(value) != 1 {
		return 0, s.reject("BUSINESS_TICKET_VERSION_UNSUPPORTED", "未知业务 Ticket 协议版本")
	}
	return contractN(value), nil
}
func contractImplementationTicket(s *semanticSession, ref string) error {
	if regexp.MustCompile(`(^|/)(work-items|business-tickets)/`).MatchString(ref) {
		return s.reject("IMPLEMENTATION_TICKET", "阶段或业务 Ticket 不能授予实现资格")
	}
	doc, e := contractTicketMetadata(s, ref)
	if e != nil {
		return e
	}
	if semHas([]string{"stage-work-item", "business-ticket", "business-ticket-set"}, text(doc["kind"])) {
		return s.reject("IMPLEMENTATION_TICKET", "非实现 Ticket")
	}
	return nil
}
func verifySliceSemantic(s *semanticSession, ref string, opts map[string]string) error {
	c, e := loadNativeSlice(s, ref)
	if e != nil {
		return e
	}
	bound := map[string]string{}
	for key, value := range opts {
		bound[key] = value
	}
	approval := first(opts["approval-ref"], opts["approval_ref"])
	if approval == "" && opts["checkpoint"] != "" {
		cp, err := s.doc(opts["checkpoint"])
		if err != nil {
			return err
		}
		gate := semMap(semMap(cp["gates"])["gate.slice-contract-approved"])
		if gate["status"] != "approved" || gate["subject_ref"] != c.Ref {
			return s.reject("SLICE_APPROVAL_REQUIRED", "当前 checkpoint 未绑定已批准的 Slice")
		}
		approval = first(text(gate["approval_ref"]), opts["checkpoint"])
	}
	if approval == "" {
		return s.reject("SLICE_APPROVAL_REQUIRED", "Slice verify 需要独立当前 checkpoint 或批准引用")
	}
	bound["approval-ref"] = approval
	if len(c.Repositories) > 0 && bound["unit"] == "" {
		units := semList(c.Normalized["work_units"])
		if len(units) == 0 {
			return s.reject("WORK_UNIT", "跨仓合同未登记工作单元")
		}
		for _, value := range units {
			unit := text(semMap(value)["id"])
			if unit == "" {
				return s.reject("WORK_UNIT", "跨仓合同工作单元缺少身份")
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
func contractApproval(s *semanticSession, gate string, binding map[string]any, approvalRef string, opts map[string]string) error {
	assetRef := text(binding["ref"])
	raw, e := s.bytes(assetRef)
	if e != nil {
		return e
	}
	asset := map[string]any{}
	state := semMap(binding["approval_context"])
	if len(state) == 0 {
		asset, e = s.doc(assetRef)
		if e != nil {
			return e
		}
	}
	if digest := text(binding["digest"]); digest != "" {
		if strings.TrimPrefix(digest, "sha256:") != safefs.Digest(raw) {
			return s.reject("APPROVAL_CURRENT_INVALID", "当前资产批准摘要过期")
		}
	}
	if len(state) == 0 {
		state = semMap(asset["approval_context"])
	}
	if len(state) == 0 && opts["checkpoint"] != "" {
		cp, e := s.doc(opts["checkpoint"])
		if e != nil {
			return e
		}
		for _, bucket := range []string{"gates", "checks"} {
			g := semMap(semMap(cp[bucket])[gate])
			state = semMap(g["approval_context"])
			if len(state) == 0 && text(g["subject_ref"]) != "" {
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
	expected, e := approvalExpectationFromState(s, gate, state)
	if e != nil {
		return e
	}
	if text(expected["subject_ref"]) != assetRef {
		basis := apArray(expected["basis"])
		found := false
		for _, v := range basis {
			m := semMap(v)
			if text(m["ref"]) == assetRef {
				found = true
			}
		}
		if !found {
			b, e := s.bind(assetRef)
			if e != nil {
				return e
			}
			basis = append(basis, map[string]any{"ref": b.Ref, "digest": b.Digest})
		}
		expected["basis"] = basis
	}
	record, e := s.doc(approvalRef)
	if e != nil {
		return e
	}
	record, e = selectApprovalRecordSemantic(s, record, gate)
	if e != nil {
		return e
	}
	if e = assertCurrentApprovalSemantic(s, record, expected); e != nil {
		return e
	}
	if binding["id"] != nil || binding["version"] != nil {
		matched := false
		for _, v := range semList(record["artifact_bindings"]) {
			m := semMap(v)
			idMatches := binding["id"] == nil || contractSame(m["id"], binding["id"])
			versionMatches := binding["version"] == nil || contractSame(m["version"], binding["version"])
			matched = matched || idMatches && versionMatches && text(m["digest"]) == text(binding["digest"])
		}
		if !matched {
			return s.reject("APPROVAL_CURRENT_INVALID", "批准未绑定当前资产身份/版本/摘要")
		}
	}
	return nil
}
func contractSliceApproval(s *semanticSession, c *nativeSlice, opts map[string]string) error {
	return contractSliceApprovalBinding(s, c, opts, nil)
}
func contractSliceApprovalBinding(s *semanticSession, c *nativeSlice, opts map[string]string, consumerBinding map[string]any) error {
	raw := c.Raw
	approval := first(opts["approval-ref"], opts["approval_ref"])
	if approval == "" {
		return s.reject("SLICE_APPROVAL_REQUIRED", "缺少 Slice 批准引用")
	}
	if text(raw["status"]) != "approved" {
		return s.reject("SLICE_APPROVAL_REQUIRED", "Slice 尚未 approved")
	}
	binding, e := s.bind(c.Ref)
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
	policy := semMap(s.roles["gate_policy"])
	orchestrator := semHas(policy["orchestrator"], "gate.slice-contract-approved")
	for _, bucket := range []string{"dual_digital_human", "digital_human_review", "check_reviews"} {
		if apFind(policy[bucket], "gate", "gate.slice-contract-approved") != nil {
			orchestrator = false
		}
	}
	if orchestrator {
		cp, e := s.doc(approval)
		if e != nil {
			return e
		}
		if cp["gate_id"] != nil || text(cp["repository_mode"]) != "project-instance" || text(cp["status"]) == "blocked" || len(semList(cp["blockers"])) > 0 {
			return s.reject("SLICE_APPROVAL_REQUIRED", "Slice 编排批准不是当前 checkpoint")
		}
		if e = s.validateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); e != nil {
			return e
		}
		gate := semMap(semMap(cp["gates"])["gate.slice-contract-approved"])
		implementation := semMap(semMap(cp["human_review"])["implementation"])
		if text(gate["status"]) != "approved" || text(gate["subject_ref"]) != c.Ref || text(implementation["slice_contract_ref"]) != c.Ref {
			return s.reject("SLICE_APPROVAL_REQUIRED", "checkpoint 未批准当前 Slice")
		}
		implementation = contractCopy(implementation)
		implementation["user_decisions"] = semMap(cp["human_review"])["user_decisions"]
		if e = assertImplementationDecisionSemantic(s, implementation); e != nil {
			return e
		}
	} else if e = contractApproval(s, "gate.slice-contract-approved", b, approval, opts); e != nil {
		return e
	}
	if contractN(raw["schema_version"]) == 3 {
		container, e := s.doc(approval)
		if e != nil {
			return e
		}
		refs := semStrings(container["evidence_refs"])
		if gates := semMap(container["gates"]); gates != nil {
			refs = semStrings(semMap(gates["gate.slice-contract-approved"])["evidence_refs"])
		}
		found := false
		for _, reviewRef := range refs {
			if !regexp.MustCompile(`[.](yaml|yml|json)$`).MatchString(reviewRef) {
				continue
			}
			review, e := s.doc(reviewRef)
			if e != nil {
				return e
			}
			rows := []map[string]any{review}
			if text(review["kind"]) == "review-bundle" {
				rows, e = reviewBundleRowsSemantic(s, review)
				if e != nil {
					return e
				}
			}
			var r map[string]any
			for _, row := range rows {
				if text(row["gate_id"]) == "check.design-reviewed" && text(row["subject_ref"]) == c.Ref {
					r = row
				}
			}
			if r == nil {
				continue
			}
			if text(r["subject_ref"]) != c.Ref || strings.TrimPrefix(text(r["subject_digest"]), "sha256:") != strings.TrimPrefix(binding.Digest, "sha256:") || text(r["principal_ref"]) == text(r["drafter_principal_ref"]) {
				return s.reject("DESIGN_REVIEW_REQUIRED", "设计审查未独立绑定当前 Slice")
			}
			reviewOpts := map[string]string{"checkpoint": approval}
			if e = contractApproval(s, "check.design-reviewed", b, reviewRef, reviewOpts); e != nil {
				return e
			}
			for _, v := range semList(r["findings"]) {
				f := semMap(v)
				if text(f["kind"]) == "suggestion" && text(f["follow_up"]) == "" {
					return s.reject("DESIGN_REVIEW_REQUIRED", "建议缺少 follow_up")
				}
				if text(f["kind"]) != "suggestion" && (!semHas([]string{"requirement-violation", "missing-evidence", "important-risk"}, text(f["kind"])) || text(f["status"]) != "resolved") {
					return s.reject("DESIGN_REVIEW_REQUIRED", "设计审查阻断项未解决")
				}
			}
			found = true
		}
		if !found {
			return s.reject("DESIGN_REVIEW_REQUIRED", "未登记 check.design-reviewed")
		}
	}
	return nil
}
func contractSliceFresh(s *semanticSession, c *nativeSlice, opts map[string]string) error {
	if e := contractSliceApproval(s, c, opts); e != nil {
		return e
	}
	n := c.Normalized
	registry, e := s.doc(".template-spec/agents/yss-skill-registry.yaml")
	if e != nil {
		return e
	}
	compiler, e := s.doc(".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml")
	if e != nil {
		return e
	}
	if contractN(registry["schema_version"]) != 3 || contractN(compiler["schema_version"]) != 2 {
		return s.unavailable("CAPABILITY", "未知 Skill/Compiler 协议")
	}
	r := semMap(n["resolution"])
	if text(r["freshness"]) != "current" || len(semList(semMap(n["readiness"])["blockers"])) > 0 || len(semList(semMap(n["readiness"])["stale_inputs"])) > 0 {
		return s.reject("CONTRACT_STALE", "合同 readiness/freshness 不允许执行")
	}
	if text(r["registry_digest"]) != strings.TrimPrefix(contractDigest(registry), "sha256:") || text(r["compiler_contract_digest"]) != strings.TrimPrefix(contractDigest(compiler), "sha256:") {
		return s.reject("CONTRACT_STALE", "Skill/Compiler 冻结摘要变化")
	}
	if !contractUnique(semMap(n["common"])["allowed_write_paths"], true) {
		return s.reject("PATH", "合同缺少写范围")
	}
	unit := opts["unit"]
	selectedContext := c
	if len(c.Repositories) == 0 {
		selectedContext, e = contractSelectLocalUnit(s, c, unit)
		if e != nil {
			return e
		}
		n = selectedContext.Normalized
		r = semMap(n["resolution"])
	}
	if len(c.Repositories) > 0 {
		if unit == "" && len(semList(n["work_units"])) == 1 {
			unit = text(semMap(semList(n["work_units"])[0])["id"])
		}
		if unit == "" {
			return s.reject("WORK_UNIT", "跨仓校验须显式工作单元")
		}
		found := false
		for _, v := range semList(n["work_units"]) {
			u := semMap(v)
			if text(u["id"]) == unit {
				repo := c.Repositories[text(u["project_root"])]
				if repo == nil {
					return s.reject("REPOSITORY", "工作单元未登记")
				}
				r = semMap(repo["resolution"])
				copyContext := *c
				copyContext.Basis = map[string]map[string]any{}
				for key, value := range c.Basis {
					copyContext.Basis[key] = value
				}
				for key, value := range semMap(repo["basis"]) {
					copyContext.Basis[key] = semMap(value)
				}
				copyContext.Normalized = contractCopy(n)
				common := contractCopy(semMap(n["common"]))
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
			return s.reject("WORK_UNIT", "工作单元未唯一选择工程")
		}
	}
	identity := semMap(r["architecture_identity"])
	if len(identity) > 0 {
		if text(r["architecture_identity_digest"]) != strings.TrimPrefix(contractDigest(identity), "sha256:") {
			return s.reject("ARCHITECTURE_STALE", "架构身份摘要变化")
		}
		if e = contractArchitecture(s, identity, selectedContext, registry, opts); e != nil {
			return e
		}
	}
	if e = contractComponents(s, r, registry); e != nil {
		return e
	}
	if design := semMap(r["technical_design"]); len(design) > 0 {
		doc, e := contractBoundDoc(s, design)
		if e != nil {
			return e
		}
		if text(design["version"]) == "" || text(doc["version"]) != text(design["version"]) {
			return s.reject("TECHNICAL_DESIGN_STALE", "技术设计版本变化")
		}
		if e = contractTechnicalDesign(s, text(design["ref"]), doc, selectedContext, opts); e != nil {
			return e
		}
	} else if text(semMap(n["backend"])["status"]) == "required" {
		return s.reject("TECHNICAL_DESIGN_REQUIRED", "后端 Slice 缺少当前技术设计")
	}
	if text(semMap(n["frontend"])["status"]) == "required" {
		delivery := semMap(r["frontend_delivery"])
		if len(delivery) == 0 {
			delivery = semMap(semMap(n["frontend"])["delivery"])
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
	s.report.ExecutionAuthorization = "not-evaluated"
	return nil
}

// A responsibility view is derived only after the entire source Slice has
// been normalized and approved. It never changes that source or its basis.
func contractSelectLocalUnit(s *semanticSession, c *nativeSlice, unit string) (*nativeSlice, error) {
	if contractN(c.Raw["schema_version"]) != 3 || unit == "" || len(c.Repositories) != 0 {
		return c, nil
	}
	u := apFind(c.Normalized["work_units"], "id", unit)
	if u == nil {
		return nil, s.reject("WORK_UNIT", "未知当前批准工作单元")
	}
	role := text(u["role_id"])
	if !semHas([]string{"role.backend-engineer", "role.frontend-engineer", "role.backend-agent", "role.frontend-agent"}, role) {
		return nil, s.reject("WORK_UNIT", "单仓职责选择需要明确后端或前端角色")
	}
	selected := *c
	selected.Normalized = contractCopy(c.Normalized)
	common := contractCopy(semMap(c.Normalized["common"]))
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

func contractSliceRepositories(s *semanticSession, c *nativeSlice) error {
	x := semMap(semMap(c.Raw["extensions"])["cross_repo"])
	bindings := semMap(x["repository_bindings"])
	if len(bindings) == 0 {
		return nil
	}
	if e := contractNeedBasis(s, c.Basis, "repository_preparation"); e != nil {
		return e
	}
	prep, e := s.doc(text(c.Basis["repository_preparation"]["ref"]))
	if e != nil {
		return e
	}
	if e = s.validateSchema(".template-spec/process/schemas/implementation-repository-preparation-result.schema.json", prep); e != nil {
		return e
	}
	identities, roots := map[string]bool{}, map[string]bool{}
	for root, v := range bindings {
		canonical, e := filepath.Abs(root)
		if e != nil || canonical != root || roots[canonical] {
			return s.reject("REPOSITORY", "工程根有路径别名")
		}
		roots[canonical] = true
		keys := semMap(v)
		selected := map[string]any{}
		for name, key := range keys {
			if name == "recipe_refs" || name == "capability_refs" {
				continue
			}
			b := c.Basis[text(key)]
			if b == nil {
				return s.reject("REPOSITORY", "缺少逐仓 basis."+text(key))
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
		actual := filepath.Join(text(reg["local_worktree"]), first(text(reg["project_root"]), "."))
		if actual != root || text(reg["status"]) != "current" || text(base["status"]) != "current" || reg["repository_id"] != base["repository_id"] || reg["project_id"] != base["project_id"] || !contractUnique(reg["allowed_write_paths"], true) {
			return s.reject("REPOSITORY", "逐仓身份、状态或写范围冲突")
		}
		key := text(reg["repository_id"]) + "\x00" + text(reg["project_id"])
		if identities[key] {
			return s.reject("REPOSITORY", "同一仓库工程重复绑定")
		}
		identities[key] = true
		matches := []map[string]any{}
		for _, v := range semList(prep["projects"]) {
			p := semMap(v)
			if p["project_id"] == reg["project_id"] && text(p["repository_ref"]) == text(semMap(selected["implementation_repository"])["ref"]) {
				matches = append(matches, p)
			}
		}
		if len(matches) != 1 || text(matches[0]["status"]) == "not-applicable" || text(matches[0]["project_root"]) != root {
			return s.reject("REPOSITORY", "准备结果没有唯一当前工程")
		}
		project := matches[0]
		if !semHas([]string{"existing-and-onboarded", "initialized-and-verified"}, text(project["status"])) {
			return s.reject("REPOSITORY_NOT_READY", "工程准备未就绪")
		}
		for _, k := range []string{"repository_ref", "baseline_ref"} {
			if text(project[k]) != "" {
				if _, e = s.bytes(text(project[k])); e != nil {
					return e
				}
			}
		}
		if text(project["delivery_role"]) == "backend" {
			p := semMap(semMap(project["design_prerequisites"])["technical_design"])
			if !contractSame(p["ref"], semMap(selected["technical_design"])["ref"]) || !contractSame(p["digest"], semMap(selected["technical_design"])["digest"]) {
				return s.reject("REPOSITORY", "准备结果技术设计绑定冲突")
			}
		}
		identity := base["architecture_identity"]
		if identity == nil {
			identity = reg["architecture_identity"]
		}
		if base["architecture_identity"] != nil && reg["architecture_identity"] != nil && !contractSame(base["architecture_identity"], reg["architecture_identity"]) {
			return s.reject("ARCHITECTURE", "逐仓架构身份冲突")
		}
		resolution := contractWithout(semMap(c.Raw["resolution"]), "architecture_identity", "architecture_identity_digest")
		if identity != nil {
			resolution["architecture_identity"], resolution["architecture_identity_digest"] = identity, strings.TrimPrefix(contractDigest(identity), "sha256:")
		}
		if selected["technical_design"] != nil {
			resolution["technical_design"] = selected["technical_design"]
		}
		if selected["frontend_delivery"] != nil {
			b := semMap(selected["frontend_delivery"])
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
		if e = s.registerExternalRoot(root, text(semMap(selected["implementation_repository"])["ref"])); e != nil {
			return e
		}
		c.Repositories[root] = map[string]any{"registration": reg, "project": project, "basis": selected, "resolution": resolution}
	}
	if !semSameSet(semMap(c.Raw["scope"])["project_roots"], mapKeysAsAny(c.Repositories)) {
		return s.reject("REPOSITORY", "逐仓绑定未覆盖全部工程根")
	}
	for _, key := range []string{"delivery_order", "rollback_order"} {
		if !semSameSet(x[key], mapKeysAsAny(c.Repositories)) {
			return s.reject("REPOSITORY", key+" 必须完整且唯一")
		}
	}
	for _, v := range semList(c.Raw["work_units"]) {
		u := semMap(v)
		repo := c.Repositories[text(u["project_root"])]
		if repo == nil {
			return s.reject("REPOSITORY", "工作单元未选择唯一工程")
		}
		allowed := u["allowed_write_paths"]
		if allowed == nil {
			allowed = semMap(c.Raw["scope"])["allowed_write_paths"]
		}
		for _, p := range semStrings(allowed) {
			within := false
			for _, parent := range semStrings(semMap(repo["registration"])["allowed_write_paths"]) {
				within = within || contractWithin(p, parent)
			}
			if !within {
				return s.reject("PATH", "超出逐仓写范围")
			}
		}
	}
	return nil
}

// The derived evidence always uses the selected repository's independently
// bound registration and assets, never another repository's global basis.
func contractSliceArchitectureEvidence(s *semanticSession, identity any, basis map[string]any) (map[string]any, error) {
	registration, e := contractBoundDoc(s, basis["repository_registration"])
	if e != nil {
		return nil, e
	}
	keys := []string{"engineering_baseline", "repository_registration", "manifest"}
	if contractN(semMap(identity)["schema_version"]) != 2 {
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
		original, bound := semMap(semMap(registration["architecture_evidence"])[key]), semMap(basis[key])
		if original["ref"] != bound["ref"] || original["digest"] != bound["digest"] || text(original["ref"]) == "" {
			return nil, s.reject("ARCHITECTURE", "逐仓登记未绑定当前 "+key)
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

func contractSliceBusiness(s *semanticSession, raw map[string]any, basis map[string]map[string]any) error {
	ticket, e := contractTicketMetadata(s, text(basis["ticket"]["ref"]))
	if e != nil {
		return e
	}
	wanted := semStrings(ticket["business_ticket_refs"])
	set := basis["business_ticket_set"]
	version, e := contractBusinessVersion(s)
	if e != nil {
		return e
	}
	if version != 1 && ticket["business_ticket_refs"] == nil && set == nil {
		return nil
	}
	if text(ticket["kind"]) != "vertical-slice-ticket" || set == nil {
		return s.reject("BUSINESS_SLICE_INVALID", "业务票来源要求 vertical-slice-ticket 和冻结集合")
	}
	business, e := contractBusinessTickets(s, text(set["ref"]))
	if e != nil {
		return e
	}
	if text(ticket["business_ticket_set_ref"]) != "" && text(ticket["business_ticket_set_ref"]) != text(set["ref"]) {
		return s.reject("BUSINESS_SLICE_INVALID", "Slice/业务票集合绑定冲突")
	}
	spec := semMap(business["spec"])
	if spec["ref"] != basis["spec"]["ref"] || spec["digest"] != basis["spec"]["digest"] {
		return s.reject("BUSINESS_SLICE_INVALID", "业务票与当前 Spec 冲突")
	}
	if len(wanted) == 0 {
		return s.reject("BUSINESS_SLICE_INVALID", "未选择业务票")
	}
	known := semMap(business["ticket_documents"])
	acs := []string{}
	for _, id := range wanted {
		b := semMap(known[id])
		if len(b) == 0 {
			return s.reject("BUSINESS_SLICE_INVALID", "未知业务票: "+id)
		}
		acs = contractUnion(acs, semStrings(b["acceptance_refs"]))
	}
	ticketAC := semStrings(ticket["acceptance_refs"])
	for _, id := range ticketAC {
		if !semHas(acs, id) {
			return s.reject("BUSINESS_SLICE_INVALID", "实现票验收超出业务票范围")
		}
	}
	covered := []string{}
	for _, v := range semMap(raw["acceptance"]) {
		a := semMap(v)
		if text(a["source"]) != "spec" || !semHas(ticketAC, text(a["locator"])) {
			return s.reject("BUSINESS_SLICE_INVALID", "Slice 验收未引用当前业务验收")
		}
		covered = contractUnion(covered, []string{text(a["locator"])})
	}
	if !semSameSet(ticketAC, covered) {
		return s.reject("BUSINESS_SLICE_INVALID", "Slice 未完整覆盖实现票验收")
	}
	return nil
}
func contractBusinessTickets(s *semanticSession, ref string) (map[string]any, error) {
	return contractBusinessTicketsMode(s, ref, "formal")
}
func contractBusinessLocator(s *semanticSession, binding map[string]any) error {
	ref, locator, kind := text(binding["ref"]), text(binding["locator"]), text(binding["locator_kind"])
	b, e := s.bytes(ref)
	if e != nil {
		return e
	}
	count := 0
	if regexp.MustCompile(`[.](yaml|yml|json)$`).MatchString(ref) {
		if kind == "heading" {
			return s.reject("BUSINESS_TICKETS", "结构化来源不能使用 heading 定位")
		}
		value, e := schema.Parse(b)
		if e != nil {
			return s.unavailable("INPUT", e.Error())
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
					if (key == "id" || strings.HasSuffix(key, "_id")) && text(item) == locator {
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
			_, body, err := frontmatter(b)
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
		return s.reject("BUSINESS_TICKETS", "来源稳定定位缺失或不唯一")
	}
	return nil
}
func contractBusinessTicketsMode(s *semanticSession, ref, mode string) (map[string]any, error) {
	if mode != "draft" && mode != "formal" {
		return nil, s.reject("BUSINESS_MODE_INVALID", "业务票模式必须为 draft 或 formal")
	}
	set, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	if e = s.validateSchema(".template-spec/process/schemas/business-tickets-v1.schema.json", set); e != nil {
		return nil, e
	}
	if text(set["kind"]) != "business-ticket-set" || !semHas([]string{"draft", "ready-for-human"}, text(set["status"])) || mode == "formal" && text(set["status"]) != "ready-for-human" {
		return nil, s.reject("BUSINESS_TICKETS", "业务票集合不是正式待审资产")
	}
	spec, e := contractBoundDoc(s, set["spec"])
	if e != nil {
		return nil, e
	}
	_ = spec
	entries, supported, e := contractPlan(s, text(semMap(set["spec"])["ref"]))
	if e != nil {
		return nil, e
	}
	if !supported {
		return nil, s.reject("BUSINESS_TICKETS", "Spec 未声明 plan-spec-v1")
	}
	ids := map[string]contractPlanEntry{}
	hasFR, hasAC := false, false
	for _, entry := range entries {
		if entry.Kind != "FR" && entry.Kind != "NFR" && entry.Kind != "AC" {
			continue
		}
		if _, ok := ids[entry.ID]; ok {
			return nil, s.reject("BUSINESS_TICKETS", "Spec ID 不唯一")
		}
		ids[entry.ID] = entry
		hasFR = hasFR || entry.Kind == "FR"
		hasAC = hasAC || entry.Kind == "AC"
	}
	if !hasFR || !hasAC {
		return nil, s.reject("BUSINESS_TICKETS", "Spec 缺少可机械读取的 FR/AC")
	}
	docs, covered := map[string]any{}, map[string]bool{}
	reportTickets := []any{}
	seenPaths := map[string]bool{}
	dependencies := map[string][]string{}
	for _, v := range semList(set["tickets"]) {
		binding := semMap(v)
		ticket, e := contractBoundDoc(s, binding)
		if e != nil {
			return nil, e
		}
		if e = s.validateSchema(".template-spec/process/schemas/business-tickets-v1.schema.json", ticket); e != nil {
			return nil, e
		}
		id := text(ticket["id"])
		if id == "" {
			id = text(ticket["ticket_id"])
		}
		if docs[id] != nil || seenPaths[text(binding["ref"])] || id != text(binding["id"]) || text(ticket["kind"]) != "business-ticket" || !semHas([]string{"draft", "ready-for-human"}, text(ticket["status"])) || mode == "formal" && text(ticket["status"]) != "ready-for-human" || ticket["version"] != binding["version"] {
			return nil, s.reject("BUSINESS_TICKETS", "业务票身份、状态或版本非法")
		}
		if !contractSame(ticket["spec"], set["spec"]) {
			return nil, s.reject("BUSINESS_TICKETS", "业务票 Spec 绑定冲突")
		}
		ticketRef := text(binding["ref"])
		seenPaths[ticketRef] = true
		if !strings.HasPrefix(ticketRef, path.Join(path.Dir(ref), "business-tickets")+"/") || !strings.HasSuffix(ticketRef, ".md") {
			return nil, s.reject("BUSINESS_TICKETS", "业务票不在集合相邻目录")
		}
		raw, e := s.bytes(ticketRef)
		if e != nil {
			return nil, e
		}
		body := string(raw)
		for _, heading := range []string{"业务结果", "范围", "非目标", "验收", "风险"} {
			if !regexp.MustCompile(`(?m)^##\s+` + heading + `\s*$`).MatchString(body) {
				return nil, s.reject("BUSINESS_TICKETS", "业务票缺少正文: "+heading)
			}
		}
		if !contractUnique(ticket["requirement_refs"], true) || !contractUnique(ticket["acceptance_refs"], true) {
			return nil, s.reject("BUSINESS_TICKETS", "业务票需求/验收引用无效")
		}
		for _, id := range semStrings(ticket["requirement_refs"]) {
			if _, ok := ids[id]; !ok {
				return nil, s.reject("BUSINESS_TICKETS", "未知 Spec ID: "+id)
			}
			if !semHas([]string{"FR", "NFR"}, ids[id].Kind) {
				return nil, s.reject("BUSINESS_TICKETS", "需求引用必须为 FR/NFR")
			}
			covered[id] = true
		}
		for _, id := range semStrings(ticket["acceptance_refs"]) {
			entry := ids[id]
			if entry.Kind != "AC" {
				return nil, s.reject("BUSINESS_TICKETS", "验收引用必须为 AC")
			}
			covered[id] = true
			requirements := regexp.MustCompile(`\b(?:FR|NFR)-[A-Za-z0-9][A-Za-z0-9._-]*\b`).FindAllString(entry.Fields["需求引用"], -1)
			if len(requirements) == 0 {
				return nil, s.reject("BUSINESS_TICKETS", "验收缺少原始需求引用")
			}
			for _, requirement := range requirements {
				if !semHas(ticket["requirement_refs"], requirement) {
					return nil, s.reject("BUSINESS_TICKETS", "业务验收缺少需求覆盖")
				}
			}
		}
		for _, v := range semList(ticket["open_questions"]) {
			q := semMap(v)
			if e = contractRequired(s, q, "id", "question", "owner", "resolve_by"); e != nil {
				return nil, e
			}
			if _, ok := q["blocking"].(bool); !ok {
				return nil, s.reject("BUSINESS_TICKETS", "未决项缺少布尔 blocking")
			}
			if mode == "formal" && q["blocking"] == true {
				return nil, s.reject("BUSINESS_TICKETS", "正式业务票仍有阻断问题")
			}
		}
		for _, v := range semList(ticket["source_refs"]) {
			b, e := contractBinding(s, v)
			if e != nil {
				return nil, e
			}
			if text(b["locator"]) != "" {
				if e = contractBusinessLocator(s, b); e != nil {
					return nil, e
				}
			}
		}
		dependencies[id] = semStrings(ticket["dependencies"])
		docs[id] = ticket
		reportTickets = append(reportTickets, map[string]any{"id": id, "ref": ticketRef, "version": ticket["version"], "source_digest": binding["digest"], "source_refs": ticket["source_refs"], "requirement_refs": ticket["requirement_refs"], "acceptance_refs": ticket["acceptance_refs"], "dependencies": ticket["dependencies"]})
	}
	active, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if active[id] {
			return s.reject("BUSINESS_TICKETS", "业务票依赖循环")
		}
		if done[id] {
			return nil
		}
		active[id] = true
		for _, dep := range dependencies[id] {
			if docs[dep] == nil {
				return s.reject("BUSINESS_TICKETS", "未知业务票依赖")
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
	for _, v := range semList(set["coverage_deferred"]) {
		d := semMap(v)
		id := first(text(d["source_id"]), text(d["id"]))
		if _, ok := ids[id]; !ok || covered[id] || deferred[id] {
			return nil, s.reject("BUSINESS_TICKETS", "延期覆盖非法")
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
			return nil, s.reject("BUSINESS_TICKETS", "Spec 未被覆盖: "+id)
		}
	}
	if mode == "formal" {
		reviewRef := first(text(semMap(set["review"])["ref"]), text(set["review_ref"]))
		review, e := s.doc(reviewRef)
		if e != nil {
			return nil, e
		}
		if contractN(review["schema_version"]) != 1 || text(review["kind"]) != "business-ticket-review" || text(review["result"]) != "passed" || text(review["reviewer"]) == "" || text(review["drafter"]) == "" || text(review["reviewer"]) == text(review["drafter"]) {
			return nil, s.reject("BUSINESS_TICKETS", "业务票独立审查未通过")
		}
		bound, e := s.bind(ref)
		if e != nil {
			return nil, e
		}
		if text(review["subject_ref"]) != ref || text(review["subject_digest"]) != bound.Digest {
			return nil, s.reject("BUSINESS_TICKETS", "业务票独立审查未绑定当前集合")
		}
		if e = s.basis(review["evidence"]); e != nil {
			return nil, e
		}
		if e = s.validateSchema(".template-spec/process/schemas/business-tickets-v1.schema.json", review); e != nil {
			return nil, e
		}
	}
	out := contractCopy(set)
	out["ticket_documents"] = docs
	out["tickets"] = reportTickets
	return out, nil
}

func contractBusinessCheckpoint(s *semanticSession, cp map[string]any, required bool) error {
	identity, e := s.doc("yss-project.yaml")
	if e != nil {
		return e
	}
	if text(identity["repository_mode"]) == "template-source" {
		return nil
	}
	version, e := contractBusinessVersion(s)
	if e != nil {
		return e
	}
	if version != 1 {
		return nil
	}
	profile, e := s.doc(".template-spec/process/harness-profile.yaml")
	if e != nil {
		return e
	}
	if semHas([]string{"harness.backend-delivery", "harness.frontend-delivery"}, text(profile["profile_id"])) {
		return nil
	}
	stage, next := text(cp["stage"]), text(cp["next_work_unit"])
	artifact := semMap(semMap(cp["artifacts"])["artifact.business-ticket-set"])
	formal := semHas([]string{"work-unit.technical-analysis", "work-unit.strategic-design-handoff", "work-unit.implementation-repository-preparation", "work-unit.ticket-decomposition", "work-unit.slice-implementation"}, next) || semHas([]string{"stage.system-data-engineering", "stage.vertical-slice-implementation", "stage.verification-release-retrospective"}, stage) || stage == "stage.ticket-formalization" && next != "work-unit.business-ticket-formalization"
	past := semHas([]string{"stage.product-design", "stage.system-data-engineering", "stage.ticket-formalization", "stage.vertical-slice-implementation", "stage.verification-release-retrospective"}, stage)
	completed := semHas([]string{"work-unit.prototype-design", "work-unit.prototype-design-v2", "work-unit.business-ticket-formalization"}, next) || text(semMap(cp["stage_trace"])["completed_work_unit"]) == "work-unit.spec-synthesis" || stage == "stage.spec-architecture" && semHas([]string{"completed", "paused-human-gate"}, text(cp["status"]))
	ref := first(text(artifact["ref"]), text(cp["business_ticket_set_ref"]))
	if !required && !formal && !past && !completed && ref == "" {
		return nil
	}
	if ref == "" {
		return s.reject("BUSINESS_CHECKPOINT_BLOCKED", "缺少业务票集合")
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
	b, e := s.bind(ref)
	if e != nil {
		return e
	}
	if text(artifact["digest"]) != "" && text(artifact["digest"]) != b.Digest {
		return s.reject("BUSINESS_SYNC_STALE", "checkpoint 业务票摘要过期")
	}
	for _, name := range []string{"map.md", "parent-ticket.md"} {
		target := path.Join(path.Dir(ref), name)
		exists, e := s.exists(target)
		if e != nil {
			return e
		}
		if !exists {
			continue
		}
		doc, e := s.doc(target)
		if e != nil {
			return e
		}
		if text(doc["business_ticket_set_ref"]) != "" && (text(doc["business_ticket_set_ref"]) != ref || text(doc["business_ticket_set_digest"]) != b.Digest) {
			return s.reject("BUSINESS_SYNC_STALE", "业务票阅读视图同步过期")
		}
	}
	return nil
}
func verifyRepositoryReadySemantic(s *semanticSession, ref string, opts map[string]string) error {
	doc, e := s.doc(ref)
	if e != nil {
		return e
	}
	prep := semMap(doc["implementation_repository_preparation"])
	if len(prep) == 0 {
		prep = doc
	}
	if e = s.validateSchema(".template-spec/process/schemas/implementation-repository-preparation-result.schema.json", prep); e != nil {
		return e
	}
	if contractN(prep["schema_version"]) != 2 || text(prep["result"]) != "completed" || prep["current_version"] != true || doc["implementation_repositories_stale"] == true {
		return s.reject("REPOSITORY_NOT_READY", "多项目准备不是当前 completed")
	}
	if e = contractEvidence(s, prep["evidence_refs"]); e != nil {
		return e
	}
	impacts := semMap(doc["delivery_impacts"])
	for _, role := range []string{"backend", "frontend"} {
		found := false
		for _, v := range semList(prep["projects"]) {
			p := semMap(v)
			if text(p["delivery_role"]) != role {
				continue
			}
			found = true
			if text(p["status"]) == "not-applicable" {
				if impacts[role] == true || text(p["reason"]) == "" {
					return s.reject("REPOSITORY_NOT_READY", "不适用与影响冲突")
				}
				continue
			}
			if e = contractRequired(s, p, "project_id", "repository_ref", "project_root", "repository_scope"); e != nil {
				return e
			}
			if role == "backend" {
				if e = contractBackendPrerequisites(s, semMap(p["design_prerequisites"]), nil, opts); e != nil {
					return e
				}
			}
			switch text(p["status"]) {
			case "existing-and-onboarded":
				r := semMap(p["onboarding_result"])
				if text(r["status"]) != "completed" {
					return s.reject("REPOSITORY_NOT_READY", "接管未完成")
				}
				if _, e = s.bytes(text(r["ref"])); e != nil {
					return e
				}
			case "initialized-and-verified":
				c := semMap(p["scaffold_contract"])
				if text(c["status"]) != "approved" || c["persisted"] != true || c["current_version"] != true || contractN(c["schema_version"]) != 4 {
					return s.reject("SCAFFOLD_CONTRACT", "非当前批准的 v4 脚手架合同")
				}
				manifest, e := s.doc(text(p["scaffold_manifest_ref"]))
				if e != nil {
					return e
				}
				if role == "backend" && (contractN(manifest["schema_version"]) != 4 || text(manifest["completion_level"]) != "empty-scaffold-verified" || !contractSame(manifest["design_prerequisites"], p["design_prerequisites"])) {
					return s.reject("SCAFFOLD_MANIFEST", "生成资产与准备依据冲突")
				}
				verification := semMap(p["scaffold_verification"])
				if text(verification["status"]) != "passed" {
					return s.reject("SCAFFOLD_VERIFICATION", "脚手架验证未通过")
				}
				if e = s.verify("verification", text(verification["ref"]), opts); e != nil {
					return e
				}
				if role == "frontend" && p["template_available"] != true {
					return s.reject("SCAFFOLD_TEMPLATE", "前端模板未验证")
				}
			default:
				return s.reject("REPOSITORY_NOT_READY", "不支持的准备状态")
			}
		}
		if !found {
			return s.reject("REPOSITORY_NOT_READY", role+" 缺少工程或不适用决定")
		}
	}
	return nil
}

func contractArchitecture(s *semanticSession, identity map[string]any, c *nativeSlice, registry map[string]any, opts map[string]string) error {
	if contractN(identity["schema_version"]) == 2 {
		return contractExistingArchitecture(s, identity, c, registry, opts)
	}
	if identity["schema_version"] != nil || identity["source_kind"] != nil {
		return s.reject("ARCH_SOURCE_UNSUPPORTED", "未知架构身份来源")
	}
	profile := semMap(semMap(registry["architecture_profiles"])[text(identity["architecture_profile"])])
	if len(profile) == 0 || profile["architecture_family"] != identity["architecture_family"] || profile["generator_skill"] != identity["generator_skill"] || text(identity["verification_database"]) != "h2" || text(identity["production_database"]) != "not-bound" || !contractUnique(identity["requested_capabilities"], false) {
		return s.reject("ARCH_IDENTITY_SHAPE", "脚手架架构/Profile/验证数据库不一致")
	}
	modules := semStrings(profile["core_modules"])
	for _, capability := range semStrings(identity["requested_capabilities"]) {
		add := semMap(profile["capability_modules"])[capability]
		if add == nil {
			return s.reject("ARCH_PROFILE_UNSUPPORTED", "未知架构能力")
		}
		modules = contractUnion(modules, semStrings(add))
	}
	if !semSameSet(modules, identity["resolved_modules"]) || !isSHA256(strings.TrimPrefix(text(identity["contract_digest"]), "sha256:")) {
		return s.reject("ARCH_IDENTITY_SHAPE", "模块闭包或合同摘要非法")
	}
	if c != nil {
		for _, key := range []string{"engineering_baseline", "repository_registration", "manifest"} {
			basis := c.Basis[key]
			if basis == nil {
				return s.reject("ARCH_EVIDENCE_REF", "缺少架构证据: "+key)
			}
			doc, e := contractBoundDoc(s, basis)
			if e != nil {
				return e
			}
			if !contractSame(identity, doc["architecture_identity"]) {
				return s.reject("ARCH_IDENTITY_CONFLICT", key+" 架构身份变化")
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
func contractExistingArchitecture(s *semanticSession, identity map[string]any, c *nativeSlice, registry map[string]any, opts map[string]string) error {
	if c == nil {
		return s.reject("ARCH_EVIDENCE_REF", "既有架构须由冻结依据消费")
	}
	if text(identity["source_kind"]) != "existing-registration" {
		return s.reject("ARCH_SOURCE_UNSUPPORTED", "未知既有架构来源")
	}
	fields := []string{"schema_version", "source_kind", "architecture_family", "architecture_profile", "repository_id", "project_id", "source_digest", "build_units_digest"}
	if e := contractRequired(s, identity, fields...); e != nil {
		return e
	}
	for key := range identity {
		if !semHas(append(fields, "platform_configuration"), key) {
			return s.reject("ARCH_IDENTITY_SHAPE", "既有身份混入生成事实")
		}
	}
	profile := semMap(semMap(registry["existing_project_profiles"])[text(identity["architecture_profile"])])
	if text(profile["source_kind"]) != "existing-registration" || text(profile["build_system"]) != "maven" || profile["architecture_family"] != identity["architecture_family"] {
		return s.reject("ARCH_PROFILE_UNSUPPORTED", "既有工程 Profile 不支持")
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
			return s.reject("ARCH_IDENTITY_CONFLICT", key+" 三方身份冲突")
		}
	}
	if text(base["kind"]) != "existing-engineering-baseline" || contractN(base["schema_version"]) != 1 || text(manifest["kind"]) != "existing-project-observation" || contractN(manifest["schema_version"]) != 1 || text(base["status"]) != "current" || text(reg["status"]) != "current" {
		return s.reject("ARCH_EVIDENCE_STALE", "工程基线/观测来源或状态非法")
	}
	if e = contractRequired(s, reg, "local_worktree", "project_root", "owner", "repository_url"); e != nil {
		return e
	}
	for _, key := range []string{"engineering_baseline", "manifest"} {
		registered := semMap(semMap(reg["architecture_evidence"])[key])
		if registered["ref"] != c.Basis[key]["ref"] || registered["digest"] != c.Basis[key]["digest"] {
			return s.reject("ARCH_EVIDENCE_CONFLICT", "登记未绑定当前 "+key)
		}
	}
	repoRoot, root := text(reg["local_worktree"]), filepath.Join(text(reg["local_worktree"]), text(reg["project_root"]))
	if e = s.registerExternalRoot(repoRoot, text(c.Basis["repository_registration"]["ref"])); e != nil {
		return e
	}
	if root != repoRoot {
		if e = s.registerExternalRoot(root, text(c.Basis["repository_registration"]["ref"])); e != nil {
			return e
		}
	}
	source := semMap(manifest["source"])
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(text(source["base_commit"])) || !contractUnique(source["roots"], true) || len(semList(source["files"])) == 0 || !contractSame(source, base["source"]) || contractDigest(source) != text(identity["source_digest"]) {
		return s.reject("ARCH_SOURCE_MISSING", "固定源码范围或摘要不一致")
	}
	queries := [][]string{{"rev-parse", "--show-toplevel"}, {"rev-parse", text(source["base_commit"]) + "^{commit}"}, {"config", "--local", "--get", "remote.origin.url"}, {"rev-parse", "HEAD"}}
	expect := []string{repoRoot, text(source["base_commit"]), text(reg["repository_url"]), text(source["base_commit"])}
	for i, q := range queries {
		out, e := s.git(repoRoot, q...)
		if e != nil {
			return e
		}
		if strings.TrimSpace(string(out)) != expect[i] {
			if i != 3 {
				return s.reject("ARCH_SOURCE_STALE", "Git 根/origin/提交与登记冲突")
			}
			allowed, err := candidateIntentAllowlist(s, repoRoot, c.Ref)
			if err != nil {
				return err
			}
			differences, err := s.git(repoRoot, "diff", "--no-renames", "--name-only", "-z", text(source["base_commit"]), "HEAD")
			if err != nil {
				return err
			}
			if err = candidateOnlyIntentPaths(s, differences, allowed, "ARCH_SOURCE_STALE"); err != nil {
				return err
			}
		}
	}
	treeBytes, e := s.git(repoRoot, "ls-tree", "-r", "-z", text(source["base_commit"]))
	if e != nil {
		return e
	}
	tree := map[string]string{}
	prefix := text(reg["project_root"])
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
			skip = skip || semHas([]string{".git", "target", "node_modules"}, part)
		}
		if skip {
			continue
		}
		beneath := false
		for _, scope := range semStrings(source["roots"]) {
			beneath = beneath || contractWithin(ref, scope)
		}
		if !beneath {
			continue
		}
		meta := strings.Fields(parts[0])
		if len(meta) != 3 || !semHas([]string{"100644", "100755"}, meta[0]) {
			return s.reject("ARCH_SOURCE_INVALID", "源码范围有 gitlink/symlink")
		}
		tree[ref] = meta[2]
	}
	intentAllowed, err := candidateIntentAllowlist(s, repoRoot, c.Ref)
	if err != nil {
		return err
	}
	seen, changes := map[string]bool{}, []string{}
	for _, v := range semList(source["files"]) {
		f := semMap(v)
		ref := text(f["path"])
		if !contractPath(ref) || seen[ref] || text(f["base_blob"]) != tree[ref] || !strings.HasPrefix(text(f["sha256"]), "sha256:") {
			return s.reject("ARCH_SOURCE_INVALID", "固定源码路径/blob 非法")
		}
		seen[ref] = true
		if intentAllowed[prefix+ref] {
			continue
		} // Original frozen blob/identity stays intact; intent current bytes are independently verified.
		b, e := s.externalBytes(root, ref)
		if e != nil {
			view := s.externalViews[root]
			d, x := view.watch(ref)
			if x != nil || d.Type != "missing" {
				return e
			}
			changes = append(changes, ref)
			continue
		}
		digest := "sha256:" + safefs.Digest(b)
		if digest != text(f["sha256"]) {
			changes = append(changes, ref)
			continue
		}
		h := sha1.New()
		fmt.Fprintf(h, "blob %d\x00", len(b))
		_, _ = h.Write(b)
		blob := hex.EncodeToString(h.Sum(nil))
		if blob != tree[ref] {
			found := false
			for _, p := range semList(source["patches"]) {
				patch := semMap(p)
				found = found || text(patch["path"]) == ref && text(patch["base_blob"]) == tree[ref] && text(patch["sha256"]) == digest
			}
			if !found {
				return s.reject("ARCH_SOURCE_UNDECLARED", "未登记基线补丁")
			}
		}
	}
	if len(seen) != len(tree) {
		return s.reject("ARCH_SOURCE_INCOMPLETE", "冻结源码清单不完整")
	}
	external := newSemanticSession(s.ctx, root, s.args)
	external.v = s.externalViews[root]
	s.children = append(s.children, external)
	for _, scope := range semStrings(source["roots"]) {
		files, e := external.scan(scope)
		if e != nil {
			return e
		}
		for _, ref := range files {
			skip := false
			for _, part := range strings.Split(ref, "/") {
				skip = skip || semHas([]string{".git", "target", "node_modules"}, part)
			}
			if !skip && !seen[ref] && !intentAllowed[prefix+ref] {
				changes = append(changes, ref)
			}
		}
	}
	units := semList(manifest["build_units"])
	if len(units) == 0 || !contractSame(units, base["build_units"]) || contractDigest(units) != text(identity["build_units_digest"]) {
		return s.reject("ARCH_BUILD_STALE", "构建单元摘要变化")
	}
	unitByPom, artifactMap, poms := map[string]map[string]any{}, map[string]string{}, map[string]contractPOM{}
	for _, v := range units {
		u := semMap(v)
		id, pom := text(u["id"]), text(u["pom_ref"])
		if id == "" || unitByPom[pom] != nil || !seen[pom] {
			return s.reject("ARCH_BUILD_CONFLICT", "构建单元重复或 POM 未冻结")
		}
		b, e := s.externalBytes(root, pom)
		if e != nil {
			return e
		}
		p, e := contractParsePOM(b)
		if e != nil {
			return s.reject("ARCH_BUILD_INVALID", e.Error())
		}
		if text(u["artifact_id"]) != p.Artifact || artifactMap[p.Artifact] != "" {
			return s.reject("ARCH_BUILD_CONFLICT", "artifactId 冲突")
		}
		artifactMap[p.Artifact] = id
		unitByPom[pom], poms[id] = u, p
	}
	for _, v := range units {
		u := semMap(v)
		p := poms[text(u["id"])]
		for _, module := range p.Modules {
			child := path.Join(path.Dir(text(u["pom_ref"])), module, "pom.xml")
			if strings.Contains(module, "${") || !contractPath(child) || unitByPom[child] == nil {
				return s.reject("ARCH_BUILD_INCOMPLETE", "实际 reactor 未登记")
			}
		}
		deps := []string{}
		for _, artifact := range p.Dependencies {
			if artifactMap[artifact] != "" {
				deps = contractUnion(deps, []string{artifactMap[artifact]})
			}
		}
		if !contractSame(contractSorted(deps), contractSorted(semStrings(u["depends_on"]))) {
			return s.reject("ARCH_BUILD_CONFLICT", "实际构建依赖变化")
		}
		for _, role := range semStrings(u["roles"]) {
			refs := semStrings(semMap(u["role_paths"])[role])
			if len(refs) == 0 {
				return s.reject("ARCH_BOUNDARY_MISSING", "责任源码映射缺失")
			}
			for _, ref := range refs {
				frozen, boundary := false, false
				for p := range seen {
					frozen = frozen || contractWithin(p, ref)
				}
				for _, scope := range semStrings(base["boundary_scope"]) {
					boundary = boundary || contractWithin(ref, scope)
				}
				if !contractPath(ref) || !frozen || !boundary {
					return s.reject("ARCH_BOUNDARY_CONFLICT", "责任源码越界")
				}
			}
		}
	}
	for _, role := range semStrings(profile["required_roles"]) {
		found := false
		for _, v := range units {
			found = found || semHas(semMap(v)["roles"], role)
		}
		if !found {
			return s.reject("ARCH_BOUNDARY_MISSING", "架构责任未覆盖")
		}
	}
	if !contractUnique(reg["allowed_write_paths"], true) || !contractUnique(base["boundary_scope"], true) || !contractUnique(base["verification_commands"], true) || !contractSame(base["verification_commands"], reg["verification_commands"]) {
		return s.reject("ARCH_REGISTRATION_MISSING", "写范围/业务边界/验证入口无效")
	}
	review, e := contractBoundDoc(s, base["boundary_review"])
	if e != nil {
		return e
	}
	gate := text(review["gate_id"])
	if !semHas([]string{"check.architecture-reviewed", "gate.technical-design-approved"}, gate) {
		return s.reject("ARCH_REVIEW_MISSING", "边界审查门禁无效")
	}
	if e = contractApproval(s, gate, c.Basis["manifest"], text(semMap(base["boundary_review"])["ref"]), opts); e != nil {
		return e
	}
	found := false
	for _, v := range semList(review["artifact_bindings"]) {
		b := semMap(v)
		found = found || b["id"] == base["id"] && b["version"] == base["version"] && text(b["digest"]) == contractDigest(contractWithout(base, "boundary_review"))
	}
	if !found || text(base["author"]) == "" || text(review["principal_ref"]) == text(base["author"]) {
		return s.reject("ARCH_REVIEW_STALE", "边界审查未独立绑定基线")
	}
	if e = contractEvidence(s, review["evidence_refs"]); e != nil {
		return e
	}
	databases := semMap(base["databases"])
	if databases["verification"] == nil || databases["production"] == nil {
		return s.reject("ARCH_DATABASE_MISSING", "验证/生产数据库须分别声明")
	}
	for _, v := range databases {
		db := semMap(v)
		if !semHas([]string{"declared", "verified", "unknown", "not-applicable"}, text(db["status"])) {
			return s.reject("ARCH_DATABASE_INVALID", "数据库状态非法")
		}
		if text(db["status"]) == "verified" {
			report, e := contractBoundDoc(s, db["verification"])
			if e != nil {
				return e
			}
			for _, k := range []string{"engine", "version", "environment_id"} {
				if text(db[k]) == "" || !contractSame(db[k], report[k]) {
					return s.reject("ARCH_DATABASE_STALE", "数据库环境/版本不一致")
				}
			}
			if report["source_digest"] != identity["source_digest"] {
				return s.reject("ARCH_DATABASE_STALE", "数据库验证源码变化")
			}
			if e = contractRecordedResults(s, report["results"], true); e != nil {
				return e
			}
		}
	}
	for _, ref := range changes {
		allowed, registered := false, false
		for _, p := range semStrings(semMap(c.Normalized["common"])["allowed_write_paths"]) {
			if filepath.IsAbs(p) {
				p, _ = filepath.Rel(root, p)
			}
			allowed = allowed || contractWithin(ref, p)
		}
		for _, p := range semStrings(reg["allowed_write_paths"]) {
			registered = registered || contractWithin(ref, p)
		}
		if !allowed || !registered {
			return s.reject("ARCH_SOURCE_OUT_OF_SCOPE", "当前源码增量超出已批准范围")
		}
	}
	return nil
}
func contractSorted(a []string) []string { x := append([]string(nil), a...); sort.Strings(x); return x }
func contractRecordedResults(s *semanticSession, v any, bound bool) error {
	rows := semList(v)
	if len(rows) == 0 {
		return s.reject("VERIFICATION_REQUIRED", "缺少真实验证记录")
	}
	for _, v := range rows {
		r := semMap(v)
		exit, ok := integer(r["exit_code"])
		if text(r["command"]) == "" || !ok || exit != 0 || !contractDate(r["executed_at"]) {
			return s.reject("VERIFICATION_FAILED", "实际命令、退出码或时间非法")
		}
		if bound {
			if e := s.basis(r["evidence"]); e != nil {
				return e
			}
		}
	}
	return nil
}

func contractComponents(s *semanticSession, resolution, registry map[string]any) error {
	frozen := semList(resolution["component_bindings"])
	required := []string{}
	skills := semStrings(resolution["required_skills"])
	for _, value := range semList(resolution["non_expanding_dependencies"]) {
		dependency := semMap(value)
		if text(dependency["type"]) == "component-dependency" {
			skills = append(skills, text(dependency["skill"]))
		}
	}
	for _, v := range semList(registry["capabilities"]) {
		c := semMap(v)
		provider := semMap(c["provider"])
		id := text(c["id"])
		if text(provider["kind"]) == "yss-component" && (semHas(resolution["required_capabilities"], id) || semHas(skills, text(c["primary_skill"]))) {
			bindingID := text(provider["binding_id"])
			if bindingID == "" {
				return s.reject("COMPONENT_CAPABILITY_INVALID", "组件 provider 缺少 binding_id")
			}
			if !semHas(required, bindingID) {
				required = append(required, bindingID)
			}
		}
	}
	if len(required) == 0 && len(frozen) == 0 {
		return nil
	}
	if len(frozen) == 0 {
		return s.reject("COMPONENT_CAPABILITY_MISSING", "冻结能力缺少平台证据")
	}
	catalog, e := contractPlatformCatalog(s)
	if e != nil {
		return e
	}
	if contractN(catalog["schema_version"]) != 2 {
		return s.unavailable("CAPABILITY", "未知后端平台目录协议")
	}
	seen := map[string]bool{}
	contexts := map[string]bool{}
	coordinates := map[string]map[string]string{}
	currentBindings := []any{}
	for _, v := range frozen {
		binding := semMap(v)
		id := text(binding["capability_id"])
		contextKey := contractDigest(map[string]any{"platform_configuration": binding["platform_configuration"], "architecture_family": binding["architecture_family"]})
		if contexts[contextKey+id] || id == "" || !semHas([]string{"domain-driven", "layered-mvc"}, text(binding["architecture_family"])) {
			return s.reject("COMPONENT_CAPABILITY_INVALID", "组件绑定缺失或重复")
		}
		seen[id] = true
		contexts[contextKey+id] = true
		platform := semMap(binding["platform_configuration"])
		profile, compat, _, e := contractPlatform(s, platform, true)
		if e != nil {
			return e
		}
		_ = catalog
		item := semMap(semMap(compat["component_capabilities"])[id])
		if len(item) == 0 {
			return s.reject("COMPONENT_CAPABILITY_MISSING", "组件不在平台闭包")
		}
		if text(item["status"]) != "verified" || text(compat["status"]) != "verified" || !semHas(item["verified_architectures"], text(binding["architecture_family"])) || len(semList(item["blockers"])) > 0 {
			return s.reject("COMPONENT_CAPABILITY_UNVERIFIED", "组件/平台/架构组合未验证")
		}
		if text(item["adoption_policy"]) == "forbidden" {
			return s.reject("COMPONENT_NEW_ADOPTION_FORBIDDEN", "组件已禁止新采纳")
		}
		if coordinates[contextKey] == nil {
			coordinates[contextKey] = map[string]string{}
		}
		for _, value := range semList(item["artifacts"]) {
			artifact := semMap(value)
			key := text(artifact["group_id"]) + ":" + text(artifact["artifact_id"])
			version := text(artifact["declared_version"]) + "->" + text(artifact["resolved_version"])
			if previous := coordinates[contextKey][key]; previous != "" && previous != version {
				return s.reject("COMPONENT_ARTIFACT_COORDINATE_CONFLICT", "同平台组件坐标解析版本冲突: "+key)
			}
			coordinates[contextKey][key] = version
		}
		currentEvidence := []any{}
		for _, v := range semList(item["evidence"]) {
			ev := semMap(v)
			if ev["architecture_family"] == binding["architecture_family"] {
				currentEvidence = append(currentEvidence, ev)
			}
		}
		if len(currentEvidence) == 0 || text(binding["component_digest"]) != text(item["component_digest"]) || !contractSame(binding["artifacts"], item["artifacts"]) || !contractSame(binding["evidence"], currentEvidence) || !contractSame(binding["verified_architectures"], item["verified_architectures"]) || text(binding["status"]) != text(item["status"]) {
			return s.reject("COMPONENT_CAPABILITY_STALE", "组件冻结绑定变化")
		}
		currentBindings = append(currentBindings, map[string]any{"capability_id": id, "status": item["status"], "verified_architectures": item["verified_architectures"], "artifacts": item["artifacts"], "evidence": currentEvidence, "component_digest": item["component_digest"], "architecture_family": binding["architecture_family"], "platform_configuration": binding["platform_configuration"]})
		for _, v := range semList(binding["evidence"]) {
			ev := semMap(v)
			doc, e := contractBoundDoc(s, ev)
			if e != nil {
				return e
			}
			if contractN(doc["schema_version"]) != 1 || text(doc["kind"]) != "backend-component-capability-evidence" || text(doc["status"]) != "passed" || doc["capability_id"] != binding["capability_id"] || doc["component_digest"] != binding["component_digest"] || doc["architecture_family"] != binding["architecture_family"] || doc["compatibility_id"] != compat["id"] || doc["profile_id"] != profile["id"] || doc["spring_boot_version"] != profile["spring_boot_version"] || !contractSame(doc["artifacts"], binding["artifacts"]) {
				return s.reject("COMPONENT_CAPABILITY_STALE", "组件真实证据来源不一致")
			}
		}
	}
	for _, id := range required {
		if !seen[id] {
			return s.reject("COMPONENT_CAPABILITY_MISSING", "所需组件未冻结")
		}
	}
	if len(frozen) > 0 && (!contractSame(frozen, currentBindings) || text(resolution["component_bindings_digest"]) != strings.TrimPrefix(contractDigest(currentBindings), "sha256:")) {
		return s.reject("COMPONENT_CAPABILITY_STALE", "冻结组件闭包摘要变化")
	}
	return nil
}
func contractDesignTargets(s *semanticSession, data map[string]any) (map[string]map[string]any, map[string]map[string]any, error) {
	design := data
	if contractN(data["schema_version"]) == 2 {
		design = semMap(data["design"])
	}
	ids, seams := map[string]map[string]any{}, map[string]map[string]any{}
	keys := map[string]string{"aggregate_catalog": "aggregate_id", "entity_catalog": "entity_id", "value_object_catalog": "value_object_id", "behavior_catalog": "behavior_id", "invariant_catalog": "invariant_id", "state_transition_catalog": "transition_id", "domain_event_catalog": "event_id", "gateway_catalog": "gateway_id", "module_catalog": "module_id", "use_case_catalog": "use_case_id", "rule_catalog": "rule_id", "integration_catalog": "integration_id", "component_catalog": "component_id"}
	for catalog, key := range keys {
		for _, v := range semList(design[catalog]) {
			item := semMap(v)
			id := text(item[key])
			if id == "" {
				id = text(item["id"])
			}
			if id == "" || ids[id] != nil {
				return nil, nil, s.reject("TECHNICAL_DESIGN_INVALID", "设计 ID 缺失或重复")
			}
			ids[id] = item
		}
	}
	for _, v := range semList(design["persistence_mapping"]) {
		item := semMap(v)
		id := text(item["mapping_id"])
		if id == "" {
			id = text(item["id"])
		}
		if id != "" {
			if ids[id] != nil {
				return nil, nil, s.reject("TECHNICAL_DESIGN_INVALID", "持久化设计 ID 重复")
			}
			ids[id] = item
		}
	}
	for _, v := range semList(design["test_seams"]) {
		item := semMap(v)
		id := first(text(item["seam_id"]), text(item["id"]))
		if id == "" || seams[id] != nil {
			return nil, nil, s.reject("TECHNICAL_DESIGN_INVALID", "seam ID 缺失或重复")
		}
		seams[id] = item
	}
	return ids, seams, nil
}
func contractTraceRow(s *semanticSession, row, source map[string]any, ids, seams map[string]map[string]any, slice string) error {
	switch text(row["disposition"]) {
	case "implemented":
		if !contractUnique(row["tactical_refs"], true) || !contractUnique(row["test_seam_refs"], true) {
			return s.reject("TECHNICAL_TRACEABILITY", "设计落点或 seam 缺失")
		}
		for _, ref := range semStrings(row["tactical_refs"]) {
			if ids[ref] == nil {
				return s.reject("TECHNICAL_TRACEABILITY", "设计落点悬空")
			}
		}
		for _, ref := range semStrings(row["test_seam_refs"]) {
			if seams[ref] == nil || !semHas(row["tactical_refs"], text(seams[ref]["subject_ref"])) {
				return s.reject("TECHNICAL_TRACEABILITY", "seam 未关联设计落点")
			}
		}
		if (text(source["kind"]) == "scenario" || strings.HasPrefix(text(row["source_id"]), "scenario.")) && source["critical"] == true {
			for _, outcome := range []string{"success", "failure"} {
				found := false
				for _, v := range semList(row["scenario_tests"]) {
					test := semMap(v)
					found = found || text(test["outcome"]) == outcome && semHas(row["test_seam_refs"], text(test["seam_ref"]))
				}
				if !found {
					return s.reject("TECHNICAL_TRACEABILITY", "关键场景缺少成功/失败测试")
				}
			}
		}
		return contractEvidence(s, row["evidence_refs"])
	case "not-applicable":
		if text(row["reason"]) == "" {
			return s.reject("TECHNICAL_TRACEABILITY", "不适用缺少理由")
		}
		return contractEvidence(s, row["evidence_refs"])
	case "deferred":
		if e := contractRequired(s, row, "reason", "risk", "owner", "followup_ticket_ref", "verification_plan", "target_version"); e != nil {
			return e
		}
		fallthrough
	case "pending", "conflict":
		if slice == "" || text(row["dependency_status"]) != "known" || !contractUnique(row["dependent_slice_refs"], true) || semHas(row["dependent_slice_refs"], slice) {
			return s.reject("TECHNICAL_TRACEABILITY", "未闭合承接阻断当前切片")
		}
		return nil
	default:
		return s.reject("TECHNICAL_TRACEABILITY", "未知承接状态")
	}
}
func contractTechnicalDesign(s *semanticSession, ref string, doc map[string]any, c *nativeSlice, opts map[string]string) error {
	if contractN(doc["schema_version"]) != 2 {
		if contractN(doc["schema_version"]) != 1 || opts["legacy-ddd"] != "true" {
			return s.reject("TECHNICAL_LEGACY_REQUIRED", "历史Tactical v1须显式legacy-ddd只读适配")
		}
		if e := contractTactical(s, doc); e != nil {
			return e
		}
		if (c != nil || opts["slice"] != "") && text(doc["status"]) != "approved" {
			return s.reject("TECHNICAL_DESIGN_STALE", "Slice仅消费approved历史DDD")
		}
		if doc["strategic_handoff"] != nil || doc["strategic_context_import_ref"] != nil || semMap(doc["upstream_impact"])["source_kind"] == "strategic-handoff" {
			return contractHandoffConsumption(s, doc, map[string]string{"consumer": "tactical", "slice": opts["slice"]})
		}
		s.report.Checks = append(s.report.Checks, SemanticCheck{ID: "legacy-ddd-read-only", SourceRef: ref, Status: "passed"})
		return nil
	}
	if e := s.validateSchema(".agents/skills/yss-technical-design/references/technical-design.schema.json", doc); e != nil {
		return e
	}
	if text(doc["digest"]) != contractDigest(contractWithout(doc, "digest")) {
		return s.reject("TECHNICAL_DESIGN_STALE", "技术设计摘要不一致")
	}
	if semHas([]string{"stale", "blocked", "drift", "new_impacts"}, text(doc["status"])) || c != nil && text(doc["status"]) != "approved" {
		return s.reject("TECHNICAL_DESIGN_STALE", "技术设计状态不能消费")
	}
	architecture := semMap(doc["architecture"])
	binding := map[string]any{"ref": architecture["decision_ref"], "digest": architecture["decision_digest"]}
	record, e := contractBoundDoc(s, binding)
	if e != nil {
		return e
	}
	if text(architecture["source_kind"]) == "scaffold-decision" {
		decision := apFind(record["decisions"], "project_id", text(architecture["project_id"]))
		if record["template"] != false || text(record["status"]) != "current" || !semHas([]string{"user-confirmed", "lifecycle-approved", "consumed"}, text(decision["status"])) || decision["confirmed_architecture"] != architecture["family"] {
			return s.reject("TECHNICAL_ARCHITECTURE", "项目架构未正式确认")
		}
		if e = contractScaffoldUserDecision(s, decision); e != nil {
			return e
		}
	} else {
		entry := apFind(record["repositories"], "project_id", text(architecture["project_id"]))
		if len(entry) == 0 {
			entry = record
		}
		if entry["project_id"] != architecture["project_id"] || semMap(entry["architecture_identity"])["architecture_family"] != architecture["family"] || semHas([]string{"stale", "blocked"}, text(entry["status"])) {
			return s.reject("TECHNICAL_ARCHITECTURE", "既有登记身份变化")
		}
		if contractN(semMap(entry["architecture_identity"])["schema_version"]) == 2 {
			registry, e := s.doc(".template-spec/agents/yss-skill-registry.yaml")
			if e != nil {
				return e
			}
			architectureContext := c
			if architectureContext == nil {
				evidence := semMap(entry["architecture_evidence"])
				basis := map[string]map[string]any{
					"repository_registration": {"ref": architecture["decision_ref"], "digest": architecture["decision_digest"]},
					"engineering_baseline":    semMap(evidence["engineering_baseline"]),
					"manifest":                semMap(evidence["manifest"]),
				}
				architectureContext = &nativeSlice{Raw: map[string]any{}, Normalized: map[string]any{"common": map[string]any{"allowed_write_paths": []any{}}}, Basis: basis}
			}
			if e = contractExistingArchitecture(s, semMap(entry["architecture_identity"]), architectureContext, registry, opts); e != nil {
				return e
			}
		}
	}
	context, spec, strategic := false, false, false
	for _, v := range semList(doc["inputs"]) {
		b := semMap(v)
		if _, e = contractBinding(s, b); e != nil {
			return e
		}
		context = context || text(b["kind"]) == "context" && text(b["ref"]) == "CONTEXT.md"
		spec = spec || text(b["kind"]) == "spec"
		strategic = strategic || text(b["kind"]) == "strategic"
	}
	if !context || !spec {
		return s.reject("TECHNICAL_DESIGN_INPUT", "缺少当前 Context/Spec")
	}
	if text(doc["status"]) == "approved" {
		if e = contractEvidence(s, doc["evidence_refs"]); e != nil {
			return e
		}
	}
	design := semMap(doc["design"])
	if text(doc["design_scope"]) == "engineering-only" {
		if e = contractEngineeringDesign(s, doc, record); e != nil {
			return e
		}
	} else if text(architecture["family"]) == "layered-mvc" {
		if e = contractMVC(s, design); e != nil {
			return e
		}
	} else {
		if !strategic {
			return s.reject("TECHNICAL_DESIGN_INPUT", "DDD 缺少战略输入")
		}
		if design["strategic_handoff"] != nil {
			return s.reject("TECHNICAL_DESIGN_INVALID", "DDD 不得另存战略绑定")
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
		if ids[text(seam["subject_ref"])] == nil {
			return s.reject("TECHNICAL_DESIGN_INVALID", "seam 主体悬空")
		}
	}
	sources, rows := map[string]map[string]any{}, map[string]map[string]any{}
	for _, v := range semList(doc["source_items"]) {
		source := semMap(v)
		id := text(source["source_id"])
		if sources[id] != nil {
			return s.reject("TECHNICAL_TRACEABILITY", "来源重复")
		}
		found := false
		for _, v := range semList(doc["inputs"]) {
			found = found || semMap(v)["ref"] == source["source_ref"]
		}
		if !found {
			return s.reject("TECHNICAL_TRACEABILITY", "来源不在冻结输入")
		}
		sources[id] = source
	}
	if len(sources) == 0 && doc["strategic_handoff"] == nil {
		return s.reject("TECHNICAL_TRACEABILITY", "无战略包须列本地规则/场景")
	}
	for _, v := range semList(doc["traceability"]) {
		row := semMap(v)
		id := text(row["source_id"])
		if rows[id] != nil || sources[id] == nil {
			return s.reject("TECHNICAL_TRACEABILITY", "承接重复或来源未知")
		}
		rows[id] = row
	}
	slice := opts["slice"]
	if c != nil {
		slice = text(c.Raw["slice_id"])
	}
	for id, source := range sources {
		if rows[id] == nil {
			return s.reject("TECHNICAL_TRACEABILITY", "缺少逐条承接")
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
func contractEngineeringDesign(s *semanticSession, data, registration map[string]any) error {
	identity := semMap(registration["architecture_identity"])
	if contractN(identity["schema_version"]) != 2 || text(identity["source_kind"]) != "existing-registration" {
		return s.reject("ENGINEERING_DESIGN_INVALID", "工程分支必须消费既有工程身份 v2")
	}
	binding := semMap(semMap(registration["architecture_evidence"])["engineering_baseline"])
	baseline, e := contractBoundDoc(s, binding)
	if e != nil {
		return e
	}
	boundEngineering, boundAPI := false, false
	for _, v := range semList(data["inputs"]) {
		b := semMap(v)
		boundEngineering = boundEngineering || text(b["kind"]) == "engineering" && b["ref"] == binding["ref"] && b["digest"] == binding["digest"]
		boundAPI = boundAPI || text(b["kind"]) == "api"
	}
	if !boundEngineering || !boundAPI {
		return s.reject("ENGINEERING_DESIGN_INVALID", "工程分支缺少固定基线/API输入")
	}
	d := semMap(data["design"])
	components := map[string]map[string]any{}
	for _, v := range semList(d["component_catalog"]) {
		c := semMap(v)
		id := text(c["component_id"])
		if components[id] != nil || id == "" {
			return s.reject("ENGINEERING_DESIGN_INVALID", "组件ID缺失或重复")
		}
		components[id] = c
	}
	paths := []string{}
	for _, c := range components {
		unit := apFind(baseline["build_units"], "id", text(c["build_unit_ref"]))
		if unit == nil {
			return s.reject("ENGINEERING_DESIGN_INVALID", "实际构建单元悬空")
		}
		for _, id := range semStrings(c["depends_on"]) {
			if components[id] == nil {
				return s.reject("ENGINEERING_DESIGN_INVALID", "工程组件依赖悬空")
			}
		}
		for _, ref := range semStrings(c["write_paths"]) {
			if !contractPath(ref) {
				return s.reject("PATH", "工程组件路径非法")
			}
			allowed := false
			for _, scope := range semStrings(registration["allowed_write_paths"]) {
				allowed = allowed || contractWithin(ref, scope)
			}
			if !allowed {
				return s.reject("ENGINEERING_DESIGN_INVALID", "工程写范围超出登记")
			}
			for _, scope := range semStrings(baseline["boundary_scope"]) {
				if contractWithin(ref, scope) || contractWithin(scope, ref) {
					return s.reject("ENGINEERING_DESIGN_INVALID", "工程分支触碰已登记业务边界")
				}
			}
			unitRoot := path.Dir(text(unit["pom_ref"]))
			if unitRoot != "." && !contractWithin(ref, unitRoot) {
				return s.reject("ENGINEERING_DESIGN_INVALID", "工程写范围与构建单元冲突")
			}
			paths = contractUnion(paths, []string{ref})
		}
	}
	if !semSameSet(d["allowed_write_paths"], paths) || len(semList(data["source_items"])) == 0 {
		return s.reject("ENGINEERING_DESIGN_INVALID", "工程写范围闭包或来源缺失")
	}
	for _, v := range semList(data["traceability"]) {
		if text(semMap(v)["disposition"]) != "implemented" {
			return s.reject("ENGINEERING_DESIGN_INVALID", "工程设计来源必须逐条承接")
		}
	}
	for _, v := range semList(d["read_dependencies"]) {
		input := semMap(v)
		found := false
		for _, v := range semList(data["inputs"]) {
			b := semMap(v)
			found = found || b["ref"] == input["ref"] && b["digest"] == input["digest"]
		}
		if !found {
			return s.reject("ENGINEERING_DESIGN_INVALID", "工程只读依赖未冻结")
		}
	}
	return nil
}
func contractMVC(s *semanticSession, design map[string]any) error {
	modules, cases, rules := map[string]map[string]any{}, map[string]map[string]any{}, map[string]map[string]any{}
	for _, v := range semList(design["module_catalog"]) {
		m := semMap(v)
		modules[text(m["module_id"])] = m
	}
	for _, v := range semList(design["use_case_catalog"]) {
		m := semMap(v)
		cases[text(m["use_case_id"])] = m
	}
	for _, v := range semList(design["rule_catalog"]) {
		m := semMap(v)
		rules[text(m["rule_id"])] = m
	}
	permitted := map[string][]string{"server": {"service", "core", "client"}, "service": {"repository", "adapter"}, "core": {"repository", "adapter"}, "repository": {}, "adapter": {"client", "feign-client"}, "client": {}, "feign-client": {"client"}}
	for _, m := range modules {
		for _, ref := range semStrings(m["depends_on"]) {
			if modules[ref] == nil || !semHas(permitted[text(m["layer"])], text(modules[ref]["layer"])) {
				return s.reject("TECHNICAL_DESIGN_INVALID", "MVC 模块依赖越界")
			}
		}
	}
	for _, item := range cases {
		if !semHas([]string{"service", "core"}, text(modules[text(item["module_ref"])]["layer"])) {
			return s.reject("TECHNICAL_DESIGN_INVALID", "MVC 用例须属于 service/core")
		}
		for _, ref := range semStrings(item["rule_refs"]) {
			if rules[ref] == nil {
				return s.reject("TECHNICAL_DESIGN_INVALID", "MVC 未声明规则")
			}
		}
	}
	for id, rule := range rules {
		for _, ref := range semStrings(rule["use_case_refs"]) {
			if cases[ref] == nil || !semHas(cases[ref]["rule_refs"], id) {
				return s.reject("TECHNICAL_DESIGN_INVALID", "规则/用例未双向关联")
			}
		}
	}
	for _, v := range semList(design["state_transition_catalog"]) {
		if cases[text(semMap(v)["use_case_ref"])] == nil {
			return s.reject("TECHNICAL_DESIGN_INVALID", "状态转换用例未知")
		}
	}
	if len(semList(design["state_transition_catalog"])) == 0 && text(design["state_not_applicable_reason"]) == "" {
		return s.reject("TECHNICAL_DESIGN_INVALID", "无状态变化须说明")
	}
	for _, v := range semList(design["persistence_mapping"]) {
		m := semMap(v)
		if cases[text(m["use_case_ref"])] == nil || text(modules[text(m["repository_ref"])]["layer"]) != "repository" {
			return s.reject("TECHNICAL_DESIGN_INVALID", "持久化用例/Repository 无效")
		}
	}
	for _, v := range semList(design["integration_catalog"]) {
		if text(modules[text(semMap(v)["adapter_ref"])]["layer"]) != "adapter" {
			return s.reject("TECHNICAL_DESIGN_INVALID", "集成未关联 Adapter")
		}
	}
	if len(semList(design["integration_catalog"])) == 0 && text(design["integration_not_applicable_reason"]) == "" {
		return s.reject("TECHNICAL_DESIGN_INVALID", "无集成须说明")
	}
	return nil
}
func contractTactical(s *semanticSession, d map[string]any) error {
	if e := contractRequired(s, d, "schema_version", "tactical_design_id", "tactical_version", "status", "context_ref", "aggregate_catalog", "entity_catalog", "value_object_catalog", "behavior_catalog", "invariant_catalog", "state_transition_catalog", "consistency_policy", "domain_event_catalog", "gateway_catalog", "persistence_mapping", "test_seams", "adr_candidates", "upstream_impact", "version", "digest", "evidence_refs"); e != nil {
		return e
	}
	if contractN(d["schema_version"]) != 1 || !regexp.MustCompile("^tactical-design[.][a-z0-9][a-z0-9-]*$").MatchString(text(d["tactical_design_id"])) || !regexp.MustCompile("^v[0-9]+$").MatchString(text(d["version"])) || !regexp.MustCompile("^v[0-9]+$").MatchString(text(d["tactical_version"])) || !regexp.MustCompile("^sha256:[A-Fa-f0-9]{64}$").MatchString(text(d["digest"])) || !semHas([]string{"draft", "ready-for-human", "approved", "blocked", "stale", "drift", "new_impacts", "not-applicable"}, text(d["status"])) {
		return s.reject("TACTICAL_DESIGN_INVALID", "战术身份、版本、状态或摘要格式非法")
	}
	requireStrings := func(m map[string]any, keys ...string) error {
		for _, key := range keys {
			if strings.TrimSpace(text(m[key])) == "" {
				return s.reject("TACTICAL_DESIGN_INVALID", "战术字段须为非空字符串: "+key)
			}
		}
		return nil
	}
	requireArray := func(m map[string]any, key string, minimum int) error {
		a, ok := m[key].([]any)
		if !ok || len(a) < minimum {
			return s.reject("TACTICAL_DESIGN_INVALID", "战术字段缺少数组或项: "+key)
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
	policy, ok := object(d["consistency_policy"])
	if !ok {
		return s.reject("TACTICAL_DESIGN_INVALID", "一致性政策须为对象")
	}
	if e := requireStrings(policy, "transaction_boundary", "concurrency", "idempotency", "cross_aggregate_strategy"); e != nil {
		return e
	}
	upstream, ok := object(d["upstream_impact"])
	if !ok {
		return s.reject("TACTICAL_DESIGN_INVALID", "上游影响须为对象")
	}
	if e := requireStrings(upstream, "spec_ref", "strategic_ref", "api_ref", "data_ref"); e != nil {
		return e
	}
	if upstream["upstream_current"] == false && text(d["status"]) != "stale" {
		return s.reject("TACTICAL_DESIGN_INVALID", "上游已过期须声明stale")
	}
	keys := map[string]string{"aggregate_catalog": "aggregate_id", "entity_catalog": "entity_id", "value_object_catalog": "value_object_id", "behavior_catalog": "behavior_id", "invariant_catalog": "invariant_id", "state_transition_catalog": "transition_id", "domain_event_catalog": "event_id", "gateway_catalog": "gateway_id", "test_seams": "seam_id"}
	prefixes := map[string]string{"aggregate_catalog": "aggregate", "entity_catalog": "entity", "value_object_catalog": "value-object", "behavior_catalog": "behavior", "invariant_catalog": "invariant", "state_transition_catalog": "transition", "domain_event_catalog": "domain-event", "gateway_catalog": "gateway", "test_seams": "test-seam"}
	catalogs := map[string]map[string]map[string]any{}
	for catalog, key := range keys {
		catalogs[catalog] = map[string]map[string]any{}
		for _, v := range semList(d[catalog]) {
			m, ok := object(v)
			id := text(m[key])
			if !ok || !regexp.MustCompile("^"+prefixes[catalog]+"[.][a-z0-9][a-z0-9-]*$").MatchString(id) || catalogs[catalog][id] != nil {
				return s.reject("TACTICAL_DESIGN_INVALID", "目录ID缺失、非法或重复")
			}
			catalogs[catalog][id] = m
		}
	}
	refsExist := func(values any, catalog string) error {
		for _, v := range semList(values) {
			ref, ok := v.(string)
			if !ok || catalogs[catalog][ref] == nil {
				return s.reject("TACTICAL_DESIGN_INVALID", "领域引用悬空")
			}
		}
		return nil
	}
	for id, a := range catalogs["aggregate_catalog"] {
		if e := requireStrings(a, "aggregate_id", "name", "context_ref", "root_entity", "consistency_boundary"); e != nil {
			return e
		}
		if a["model_origin"] == "strategic-concept" || a["api_exposure"] != "internal-only" || catalogs["entity_catalog"][text(a["root_entity"])] == nil || catalogs["entity_catalog"][text(a["root_entity"])]["aggregate_id"] != id {
			return s.reject("TACTICAL_DESIGN_INVALID", "聚合根、来源或暴露冲突")
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
			if catalogs["aggregate_catalog"][text(m["aggregate_id"])] == nil {
				return s.reject("TACTICAL_DESIGN_INVALID", "聚合归属悬空")
			}
			if catalog == "state_transition_catalog" && catalogs["behavior_catalog"][text(m["behavior_id"])] == nil {
				return s.reject("TACTICAL_DESIGN_INVALID", "状态转换行为悬空")
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
		if m["immutable"] != true || catalogs["aggregate_catalog"][text(m["aggregate_id"])] == nil {
			return s.reject("TACTICAL_DESIGN_INVALID", "值对象归属或immutable无效")
		}
	}
	for _, m := range catalogs["gateway_catalog"] {
		if e := requireStrings(m, "gateway_id", "name", "context_ref", "layer"); e != nil {
			return e
		}
		if m["layer"] != "Domain" {
			return s.reject("TACTICAL_DESIGN_INVALID", "Gateway必须属于Domain")
		}
		if e := requireArray(m, "capabilities", 1); e != nil {
			return e
		}
	}
	for _, v := range semList(d["persistence_mapping"]) {
		m, ok := object(v)
		if !ok {
			return s.reject("TACTICAL_DESIGN_INVALID", "存储映射须为对象")
		}
		if e := requireStrings(m, "aggregate_id", "storage_model", "notes"); e != nil {
			return e
		}
		if catalogs["aggregate_catalog"][text(m["aggregate_id"])] == nil {
			return s.reject("TACTICAL_DESIGN_INVALID", "存储聚合悬空")
		}
	}
	for _, m := range catalogs["test_seams"] {
		if e := requireStrings(m, "seam_id", "kind", "subject_ref", "assertion"); e != nil {
			return e
		}
		found := false
		for _, catalog := range []string{"aggregate_catalog", "entity_catalog", "value_object_catalog", "behavior_catalog", "invariant_catalog", "state_transition_catalog", "gateway_catalog"} {
			found = found || catalogs[catalog][text(m["subject_ref"])] != nil
		}
		if !found {
			return s.reject("TACTICAL_DESIGN_INVALID", "seam主体悬空")
		}
	}
	if semMap(d["complexity"])["escalate_to_standalone"] == true && strings.TrimSpace(text(semMap(d["complexity"])["standalone_ref"])) == "" {
		return s.reject("TACTICAL_DESIGN_INVALID", "复杂度升级缺少独立设计")
	}
	return nil
}

func verifyScaffoldSemantic(s *semanticSession, ref string, opts map[string]string) error {
	bytes, e := s.bytes(ref)
	if e != nil {
		return e
	}
	if !json.Valid(bytes) {
		return s.reject("SCAFFOLD_FORMAT", "脚手架合同必须为 JSON 对象")
	}
	doc, e := s.doc(ref)
	if e != nil {
		return e
	}
	if e = s.validateSchema(".template-spec/process/schemas/project-scaffold-contract.schema.json", doc); e != nil {
		return e
	}
	if contractN(doc["schema_version"]) != 4 || text(doc["status"]) != "approved" || doc["current_version"] != true || text(doc["persisted_ref"]) == "" {
		return s.reject("SCAFFOLD_CONTRACT", "脚手架合同必须已批准、持久化且当前")
	}
	policy := semMap(doc["generation_policy"])
	for key, value := range map[string]string{"mode": "initialize-only", "existing_target": "unsupported", "old_project_migration": "unsupported", "template_upgrade": "unsupported"} {
		if text(policy[key]) != value {
			return s.reject("SCAFFOLD_POLICY", "不支持的生成政策")
		}
	}
	if !contractUnique(doc["allowed_write_paths"], true) || !contractUnique(doc["verification_commands"], true) || !semHas(doc["expected_evidence_files"], ".yss/scaffold-generation.json") {
		return s.reject("SCAFFOLD_CONTRACT", "写范围/固定验证/Manifest 证据缺失")
	}
	if text(doc["delivery_role"]) == "frontend" {
		boundOpts := semanticOptions(opts)
		boundOpts["contract-ref"] = ref
		return contractFrontendScaffold(s, doc, boundOpts)
	}
	work := semMap(doc["work_unit"])
	if work["primary_skill"] != doc["generator_skill"] || text(work["tdd_mode"]) != "controlled-generation" || work["controlled_generation"] != true || !contractSame(work["allowed_write_paths"], doc["allowed_write_paths"]) || !contractSame(work["verification_commands"], doc["verification_commands"]) {
		return s.reject("SCAFFOLD_WORK_UNIT", "受控生成工作单元与合同冲突")
	}
	if text(doc["delivery_role"]) != "backend" || text(doc["scaffold_status"]) != "required" {
		return s.reject("SCAFFOLD_CONTRACT", "后端脚手架身份无效")
	}
	family, skill := text(doc["architecture_family"]), text(doc["generator_skill"])
	if family == "domain-driven" && skill != "yss-ddd-scaffold-generator" || family == "layered-mvc" && skill != "yss-layered-mvc-scaffold-generator" || !semHas([]string{"domain-driven", "layered-mvc"}, family) {
		return s.reject("SCAFFOLD_ARCHITECTURE", "生成器和架构族冲突")
	}
	if !contractSame(doc["verification_commands"], []string{"./mvnw validate", "./mvnw test", "./mvnw package"}) {
		return s.reject("SCAFFOLD_VERIFICATION", "Maven 验证入口须为固定三条命令")
	}
	approval := semMap(doc["approval"])
	if approval["approval_ref"] != doc["lifecycle_approval_ref"] || approval["persisted_ref"] != doc["persisted_ref"] || approval["current_version"] != doc["contract_version"] {
		return s.reject("SCAFFOLD_APPROVAL", "批准指针或版本冲突")
	}
	coordinates := semMap(doc["maven_coordinates"])
	values := []any{coordinates["group_id"], coordinates["project_version"], semMap(coordinates["parent"])["group_id"], semMap(coordinates["parent"])["artifact_id"], semMap(coordinates["parent"])["version"], coordinates["yss_components_version"]}
	for _, v := range values {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(text(v)) {
			return s.reject("SCAFFOLD_COORDINATES", "Maven 坐标非法")
		}
	}
	decisionRef := text(doc["decision_ref"])
	if !filepath.IsAbs(decisionRef) {
		decisionRef = path.Join(path.Dir(ref), decisionRef)
	}
	if filepath.IsAbs(decisionRef) {
		r, e := filepath.Rel(s.root, decisionRef)
		if e != nil || strings.HasPrefix(r, "..") {
			return s.reject("PATH", "架构决定越出治理 root")
		}
		decisionRef = filepath.ToSlash(r)
	}
	record, e := contractBoundDoc(s, map[string]any{"ref": decisionRef, "digest": doc["decision_digest"]})
	if e != nil {
		return e
	}
	decision := apFind(record["decisions"], "decision_id", text(doc["decision_id"]))
	if text(record["kind"]) != "scaffold-architecture-decisions" || record["template"] != false || text(record["status"]) != "current" || text(decision["status"]) != "lifecycle-approved" || decision["confirmed_architecture"] != doc["architecture_family"] || decision["project_id"] != doc["project_name"] {
		return s.reject("SCAFFOLD_DECISION", "架构决定不是当前项目正式批准")
	}
	profiles, modules := semMap(doc["profiles"]), semMap(doc["module_profile"])
	if decision["platform_profile"] != profiles["platform"] || decision["architecture_profile"] != doc["architecture_profile"] || text(decision["verification_database"]) != "h2" || text(decision["production_database"]) != "not-bound" || decision["database_profile"] != nil || !contractSame(decision["requested_capabilities"], modules["requested_capabilities"]) || !contractSame(decision["resolved_modules"], modules["resolved_modules"]) {
		return s.reject("SCAFFOLD_DECISION", "架构 Profile/模块闭包冲突")
	}
	if e = contractScaffoldUserDecision(s, decision); e != nil {
		return e
	}
	if text(doc["architecture_profile"]) == "mvc-data-analysis-v1" {
		if doc["init_git"] != true {
			return s.reject("SCAFFOLD_POLICY", "数据分析脚手架须显式要求独立 Git 初始化")
		}
		handoffRef := path.Join(path.Dir(ref), text(doc["context_handoff_ref"]))
		if !contractWithin(handoffRef, path.Dir(ref)) || handoffRef == path.Dir(ref) || filepath.IsAbs(text(doc["context_handoff_ref"])) {
			return s.reject("SCAFFOLD_CONTEXT", "Context handoff必须在批准合同目录内部")
		}
		context, e := s.bytes(handoffRef)
		if e != nil {
			return e
		}
		if "sha256:"+safefs.Digest(context) != text(doc["context_handoff_digest"]) {
			return s.reject("SCAFFOLD_CONTEXT", "Context handoff 摘要冲突")
		}
		if _, e = contextContractBytes(context); e != nil {
			return s.reject("SCAFFOLD_CONTEXT", e.Error())
		}
	} else if family == "layered-mvc" && doc["init_git"] != false {
		return s.reject("SCAFFOLD_POLICY", "通用 MVC 不允许初始化 Git")
	}
	if e = contractBackendPrerequisites(s, semMap(doc["design_prerequisites"]), doc, opts); e != nil {
		return e
	}
	profile, entry, reports, e := contractPlatform(s, semMap(doc["platform_configuration"]), true)
	if e != nil {
		return e
	}
	bom := semMap(semMap(doc["platform_configuration"])["bom"])
	if profiles["platform"] != profile["id"] || profiles["validation_namespace"] != profile["validation_namespace"] || !contractSame(coordinates["parent"], semMap(doc["platform_configuration"])["parent"]) || coordinates["yss_components_version"] != bom["version"] || bom["group_id"] != "com.yss.cloud" || bom["artifact_id"] != "yss-components-bom" || !contractSame(decision["platform_configuration"], doc["platform_configuration"]) {
		return s.reject("SCAFFOLD_PLATFORM", "架构决定/合同平台或Maven坐标变化")
	}
	for _, capability := range semStrings(modules["requested_capabilities"]) {
		if !semHas(entry["capabilities"], capability) {
			return s.reject("SCAFFOLD_PLATFORM", "平台未验证架构能力")
		}
	}
	found := false
	for _, report := range reports {
		if report["architecture_family"] != doc["architecture_family"] {
			continue
		}
		all := true
		for _, capability := range semStrings(modules["requested_capabilities"]) {
			all = all && semHas(report["verified_capabilities"], capability)
		}
		found = found || all
	}
	if !found {
		return s.reject("SCAFFOLD_PLATFORM", "架构/能力组合缺少qualification证据")
	}
	return nil
}
func contractFrontendScaffold(s *semanticSession, doc map[string]any, opts map[string]string) error {
	if text(doc["scaffold_kind"]) != "frontend-yss-vue3" || text(doc["generator_skill"]) != "yss-frontend-scaffold-generator" {
		return s.reject("SCAFFOLD_CONTRACT", "前端生成器身份错误")
	}
	front := semMap(doc["frontend"])
	template := semMap(front["template"])
	if text(template["kind"]) == "bundled" {
		for _, key := range []string{"app_name", "microapp_name"} {
			if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`).MatchString(text(front[key])) {
				return s.reject("SCAFFOLD_NAME", "应用名称非法")
			}
		}
		if !regexp.MustCompile(`^/(?:[a-zA-Z0-9_-]+/?)*$`).MatchString(text(front["base_route"])) || len(semMap(front["replacements"])) > 0 {
			return s.reject("SCAFFOLD_TEMPLATE", "内置基线不接受任意替换或非法 route")
		}
		manifestRef := ".agents/skills/yss-frontend-scaffold-generator/references/data-quality-v1.manifest.json"
		b, e := s.bind(manifestRef)
		if e != nil {
			return e
		}
		if b.Digest != text(template["manifest_digest"]) {
			return s.reject("SCAFFOLD_TEMPLATE", "内置 manifest 摘要冲突")
		}
		manifest, e := s.doc(manifestRef)
		if e != nil {
			return e
		}
		if manifest["baseline_id"] != template["baseline_id"] {
			return s.reject("SCAFFOLD_TEMPLATE", "基线身份冲突")
		}
		prefix := ".agents/skills/yss-frontend-scaffold-generator/assets/data-quality-v1"
		files, e := s.scan(prefix)
		if e != nil {
			return e
		}
		expected := semMap(manifest["files"])
		if len(files) != len(expected) {
			return s.reject("SCAFFOLD_TEMPLATE", "基线文件清单变化")
		}
		for _, file := range files {
			raw, e := s.bytes(file)
			if e != nil {
				return e
			}
			ref := strings.TrimPrefix(file, prefix+"/")
			if strings.TrimPrefix(text(expected[ref]), "sha256:") != safefs.Digest(raw) {
				return s.reject("SCAFFOLD_TEMPLATE", "基线源字节变化")
			}
		}
	} else {
		if e := contractFrontendGitTemplate(s, doc, template, opts); e != nil {
			return e
		}
	}
	if text(front["openapi_impact"]) == "frozen" {
		if _, e := contractBinding(s, map[string]any{"ref": front["openapi_json_ref"], "digest": front["openapi_json_digest"]}); e != nil {
			return e
		}
	} else if text(front["openapi_impact"]) != "not-applicable" || text(front["openapi_not_applicable_reason"]) == "" {
		return s.reject("SCAFFOLD_API", "API 不适用须说明理由")
	}
	return nil
}
func contractFrontendGitTemplate(s *semanticSession, doc, template map[string]any, opts map[string]string) error {
	checkout := first(opts["template-checkout"], s.args["template-checkout"])
	if checkout == "" {
		return s.unavailable("CAPABILITY", "外部Git模板需要显式template-checkout")
	}
	if !filepath.IsAbs(checkout) || filepath.Clean(checkout) != checkout {
		return s.reject("SCAFFOLD_TEMPLATE", "模板checkout必须为规范绝对目录")
	}
	if text(template["repository"]) == "" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(text(template["commit"])) {
		return s.reject("SCAFFOLD_TEMPLATE", "模板须绑定仓库及完整固定commit")
	}
	if e := s.registerExternalRoot(checkout, opts["contract-ref"]); e != nil {
		return e
	}
	top, e := s.git(checkout, "rev-parse", "--show-toplevel")
	if e != nil {
		return e
	}
	if filepath.Clean(strings.TrimSpace(string(top))) != checkout {
		return s.reject("SCAFFOLD_TEMPLATE", "模板checkout不是Git工作树")
	}
	head, e := s.git(checkout, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(head)) != text(template["commit"]) {
		return s.reject("SCAFFOLD_TEMPLATE", "模板HEAD与批准commit不一致")
	}
	origin, e := s.git(checkout, "config", "--local", "--get", "remote.origin.url")
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(origin)) != text(template["repository"]) {
		return s.reject("SCAFFOLD_TEMPLATE", "模板origin与批准仓库不一致")
	}
	// Read the approved immutable tree. Uncommitted checkout edits do not become
	// template authority, matching the old producer's fixed-commit archive.
	tree, e := s.git(checkout, "ls-tree", "-rz", "--full-tree", text(template["commit"]))
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
		if !ok || len(parts) != 3 || !contractPath(ref) || seen[ref] || parts[1] != "blob" || !semHas([]string{"100644", "100755", "120000"}, parts[0]) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(parts[2]) {
			return s.reject("SCAFFOLD_TEMPLATE", "模板Git树路径、类型或对象非法")
		}
		seen[ref] = true
		bytes, e := s.git(checkout, "cat-file", "blob", parts[2])
		if e != nil {
			return e
		}
		total += len(bytes)
		if total > 256<<20 {
			return s.unavailable("CAPABILITY", "模板固定树超过256MiB观测上限")
		}
	}
	return nil
}
func contractBackendPrerequisites(s *semanticSession, p, scaffold map[string]any, opts map[string]string) error {
	technical, e := contractBoundDoc(s, p["technical_design"])
	if e != nil {
		return e
	}
	if contractN(technical["schema_version"]) != 2 || text(technical["status"]) != "approved" || technical["version"] != semMap(p["technical_design"])["version"] {
		return s.reject("SCAFFOLD_TECHNICAL", "技术设计不是当前 approved v2")
	}
	if scaffold != nil {
		a := semMap(technical["architecture"])
		if a["family"] != scaffold["architecture_family"] || a["project_id"] != scaffold["project_name"] || a["decision_digest"] != scaffold["decision_digest"] {
			return s.reject("SCAFFOLD_TECHNICAL", "技术设计与脚手架架构决定冲突")
		}
	}
	if e = contractTechnicalDesign(s, text(semMap(p["technical_design"])["ref"]), technical, nil, opts); e != nil {
		return e
	}
	data, e := contractBoundDoc(s, p["data_architecture_decision"])
	if e != nil {
		return e
	}
	if e = s.validateSchema(".template-spec/process/schemas/data-architecture-decision.schema.json", data); e != nil {
		return e
	}
	if data["decision_version"] != semMap(p["data_architecture_decision"])["version"] {
		return s.reject("DATA_ARCHITECTURE_STALE", "数据架构决定版本不匹配")
	}
	if text(data["status"]) != "approved" || data["current_version"] != true || data["impact"] != semMap(p["data_architecture_decision"])["impact"] {
		return s.reject("DATA_ARCHITECTURE_STALE", "数据架构决定不是当前批准影响结论")
	}
	if _, e = contractBinding(s, map[string]any{"ref": data["assessment_ref"], "digest": data["assessment_digest"]}); e != nil {
		return e
	}
	if e = contractEvidence(s, data["evidence_refs"]); e != nil {
		return e
	}
	if text(data["impact"]) == "required" {
		if _, e = contractBinding(s, map[string]any{"ref": data["data_architecture_ref"], "digest": data["data_architecture_digest"]}); e != nil {
			return e
		}
		if e = contractEvidence(s, data["review_evidence_refs"]); e != nil {
			return e
		}
	}
	api, e := contractAPIDecision(s, semMap(p["api_contract_decision"]))
	if e != nil {
		return e
	}
	approvalRef := text(p["engineering_contract_approval_ref"])
	if scaffold != nil && (approvalRef != text(scaffold["lifecycle_approval_ref"]) || approvalRef != text(semMap(scaffold["approval"])["approval_ref"])) {
		return s.reject("SCAFFOLD_APPROVAL", "工程批准指针冲突")
	}
	if e = contractApproval(s, "gate.engineering-contract-approved", semMap(p["technical_design"]), approvalRef, opts); e != nil {
		return e
	}
	approval, e := s.doc(approvalRef)
	if e != nil {
		return e
	}
	approval, e = selectApprovalRecordSemantic(s, approval, "gate.engineering-contract-approved")
	if e != nil {
		return e
	}
	packageDoc, e := s.doc(text(approval["subject_ref"]))
	if e != nil {
		return e
	}
	if contractN(packageDoc["schema_version"]) != 1 || text(packageDoc["kind"]) != "engineering-contract-package" {
		return s.reject("ENGINEERING_PACKAGE", "缺少工程合同原子批准包")
	}
	if scaffold != nil && (packageDoc["project_id"] != scaffold["project_name"] || !semSameSet(approval["approval_scope"], []any{scaffold["contract_id"], scaffold["project_name"]})) {
		return s.reject("ENGINEERING_PACKAGE", "工程合同包项目或范围冲突")
	}
	if project := opts["project-id"]; project != "" && (text(packageDoc["project_id"]) != project || !semHas(approval["approval_scope"], project)) {
		return s.reject("ENGINEERING_PACKAGE", "工程包项目或批准范围与当前消费者不一致")
	}
	for key, doc := range map[string]map[string]any{"technical_design": technical, "data_architecture_decision": data, "api_contract_decision": api} {
		expected := contractCopy(semMap(p[key]))
		version := doc["decision_version"]
		if key == "technical_design" {
			version = doc["version"]
		}
		expected["version"] = version
		if key != "technical_design" {
			expected["impact"] = doc["impact"]
		}
		actual := semMap(packageDoc[key])
		for _, field := range []string{"ref", "version", "digest"} {
			if !contractSame(actual[field], expected[field]) {
				return s.reject("ENGINEERING_PACKAGE", "工程包未原子绑定当前资产")
			}
		}
		if key != "technical_design" && actual["impact"] != doc["impact"] {
			return s.reject("ENGINEERING_PACKAGE", "工程包影响结论冲突")
		}
		id := doc["decision_id"]
		if key == "technical_design" {
			id = doc["technical_design_id"]
		}
		match := false
		for _, v := range semList(approval["artifact_bindings"]) {
			b := semMap(v)
			match = match || b["id"] == id && b["version"] == version && b["digest"] == expected["digest"]
		}
		if !match {
			return s.reject("ENGINEERING_PACKAGE", "批准记录未绑定当前资产版本和字节")
		}
	}
	if text(api["impact"]) == "required" {
		if !contractSame(packageDoc["frozen_openapi"], api["openapi"]) {
			return s.reject("ENGINEERING_PACKAGE", "工程包未原子冻结 OpenAPI")
		}
	} else if packageDoc["frozen_openapi"] != nil {
		return s.reject("ENGINEERING_PACKAGE", "无 API 影响不得携带 frozen_openapi")
	}
	return nil
}
func contractAPIDecision(s *semanticSession, binding map[string]any) (map[string]any, error) {
	doc, e := contractBoundDoc(s, binding)
	if e != nil {
		return nil, e
	}
	suffix := ""
	if contractN(doc["schema_version"]) == 2 {
		suffix = "-v2"
	}
	if e = s.validateSchema(".template-spec/process/schemas/api-contract-decision"+suffix+".schema.json", doc); e != nil {
		return nil, e
	}
	if text(doc["status"]) != "approved" || doc["decision_version"] != binding["version"] || doc["impact"] != binding["impact"] {
		return nil, s.reject("API_DECISION_STALE", "API 决定版本/影响/批准状态冲突")
	}
	if _, e = contractBinding(s, map[string]any{"ref": doc["assessment_ref"], "digest": doc["assessment_digest"]}); e != nil {
		return nil, e
	}
	if e = contractEvidence(s, doc["evidence_refs"]); e != nil {
		return nil, e
	}
	if text(doc["impact"]) == "not-applicable" {
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
	bindingAPI := semMap(doc["openapi"])
	if text(review["result"]) != "approved" || review["blocking_findings"] == nil || len(semList(review["blocking_findings"])) > 0 || review["draft_ref"] != bindingAPI["ref"] || review["draft_digest"] != bindingAPI["digest"] {
		return nil, s.reject("OPENAPI_REVIEW", "OpenAPI 实际审查没有批准当前 Draft")
	}
	if e = contractEvidence(s, review["evidence_refs"]); e != nil {
		return nil, e
	}
	if e = contractOpenAPIValidation(s, validation); e != nil {
		return nil, e
	}
	draft := semMap(validation["draft"])
	if draft["ref"] != bindingAPI["ref"] || draft["sha256"] != bindingAPI["digest"] || semMap(doc["freeze"])["version"] != bindingAPI["version"] {
		return nil, s.reject("OPENAPI_FREEZE", "冻结没有绑定当前接口版本")
	}
	if contractN(doc["schema_version"]) == 1 {
		if semMap(doc["freeze"])["draft_ref"] != bindingAPI["ref"] || semMap(doc["freeze"])["draft_digest"] != bindingAPI["digest"] || semMap(doc["draft_review"])["draft_ref"] != bindingAPI["ref"] || semMap(doc["draft_review"])["draft_digest"] != bindingAPI["digest"] {
			return nil, s.reject("OPENAPI_FREEZE", "冻结 Draft 引用冲突")
		}
	}
	return doc, nil
}
func contractOpenAPIValidation(s *semanticSession, record map[string]any) error {
	if e := s.validateSchema(".template-spec/process/schemas/openapi-draft-validation-record.schema.json", record); e != nil {
		return e
	}
	draft, tool := semMap(record["draft"]), semMap(record["toolchain"])
	layout, err := viewWorkLayout(s.v)
	if err != nil {
		return err
	}
	feature, err := layout.FeatureOf(text(draft["ref"]))
	if err != nil {
		return err
	}
	base, _ := layout.FeatureRoot(feature)
	if !strings.HasPrefix(text(draft["ref"]), base+"/api/") {
		return s.reject("OPENAPI_VALIDATION", "Draft 必须位于配置的功能包根的 api 目录")
	}
	exit, ok := integer(tool["exit_code"])
	if record["template"] != false || text(record["status"]) != "passed" || !ok || exit != 0 || text(tool["command"]) != "pnpm exec redocly lint "+text(draft["ref"]) {
		return s.reject("OPENAPI_VALIDATION", "实际 lint 未通过或未指向当前 Draft")
	}
	for _, v := range semMap(record["checks"]) {
		if text(v) != "passed" {
			return s.reject("OPENAPI_VALIDATION", "结构/lint 检查未通过")
		}
	}
	doc, e := contractBoundDoc(s, map[string]any{"ref": draft["ref"], "digest": draft["sha256"]})
	if e != nil {
		return e
	}
	if text(doc["openapi"]) != "3.1.0" || draft["oas_version"] != doc["openapi"] {
		return s.reject("OPENAPI_VALIDATION", "必须 OAS 3.1.0")
	}
	lock, e := s.bytes(text(tool["lockfile_ref"]))
	if e != nil {
		return e
	}
	if _, e = s.bytes(text(tool["evidence_ref"])); e != nil {
		return e
	}
	version := regexp.QuoteMeta(text(tool["version"]))
	if !regexp.MustCompile(`['"]?@redocly/cli@`+version+`(?:['":\s(]|$)`).Match(lock) && !regexp.MustCompile(`['"]?@redocly/cli['"]?:[\s\S]{0,240}?\n\s+version:\s*['"]?`+version+`(?:\(|['"\s]|$)`).Match(lock) {
		return s.reject("OPENAPI_VALIDATION", "锁文件未绑定当前 Redocly 版本")
	}
	return contractOpenAPIReferences(s, text(draft["ref"]), doc)
}
func contractOpenAPIReferences(s *semanticSession, draftRef string, doc map[string]any) error {
	apiRoot := path.Dir(draftRef)
	cache := map[string]map[string]any{draftRef: doc}
	active := map[string]bool{}
	var load func(string, string) (any, string, error)
	load = func(ref, owner string) (any, string, error) {
		if regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`).MatchString(ref) || strings.HasPrefix(ref, "/") || strings.Contains(ref, "\\") {
			return nil, "", s.reject("OPENAPI_REF", "禁止远程或绝对引用")
		}
		parts := strings.SplitN(ref, "#", 2)
		file := owner
		if parts[0] != "" {
			file = path.Join(path.Dir(owner), parts[0])
		}
		if !contractPath(file) || !contractWithin(file, apiRoot) {
			return nil, "", s.reject("OPENAPI_REF", "引用越出 feature API 目录")
		}
		value := any(cache[file])
		if value == nil {
			parsed, e := s.doc(file)
			if e != nil {
				return nil, "", e
			}
			cache[file] = parsed
			value = parsed
		}
		if len(parts) > 1 && parts[1] != "" {
			if !strings.HasPrefix(parts[1], "/") {
				return nil, "", s.reject("OPENAPI_REF", "仅支持 JSON Pointer")
			}
			for _, key := range strings.Split(parts[1][1:], "/") {
				decoded, e := url.PathUnescape(key)
				if e != nil {
					return nil, "", s.reject("OPENAPI_REF", "非法 fragment")
				}
				key = strings.ReplaceAll(strings.ReplaceAll(decoded, "~1", "/"), "~0", "~")
				m, ok := object(value)
				if ok {
					var exists bool
					value, exists = m[key]
					if !exists {
						return nil, "", s.reject("OPENAPI_REF", "引用不可解析")
					}
				} else if a, ok := value.([]any); ok {
					i, e := strconv.Atoi(key)
					if e != nil || i < 0 || i >= len(a) {
						return nil, "", s.reject("OPENAPI_REF", "引用数组越界")
					}
					value = a[i]
				} else {
					return nil, "", s.reject("OPENAPI_REF", "引用不可解析")
				}
			}
		}
		return value, file, nil
	}
	var walk func(any, string) error
	walk = func(value any, owner string) error {
		switch v := value.(type) {
		case map[string]any:
			if ref := text(v["$ref"]); ref != "" {
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
	for route, v := range semMap(doc["paths"]) {
		item := semMap(v)
		for method, v := range item {
			if !semHas([]string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}, method) {
				continue
			}
			op := semMap(v)
			id := text(op["operationId"])
			if id == "" || ids[id] {
				return s.reject("OPENAPI_OPERATION", "operationId 缺失或重复")
			}
			ids[id] = true
			parameters := append(append([]any{}, semList(item["parameters"])...), semList(op["parameters"])...)
			for _, placeholder := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(route, -1) {
				found := false
				for _, v := range parameters {
					parameter := semMap(v)
					if text(parameter["$ref"]) != "" {
						resolved, _, e := load(text(parameter["$ref"]), draftRef)
						if e != nil {
							return e
						}
						parameter = semMap(resolved)
					}
					found = found || text(parameter["in"]) == "path" && text(parameter["name"]) == placeholder[1] && parameter["required"] == true
				}
				if !found {
					return s.reject("OPENAPI_OPERATION", "required path parameter 缺失")
				}
			}
		}
	}
	return nil
}
func contractTree(s *semanticSession, prefix string) (string, []string, error) {
	files, e := s.scan(prefix)
	if e != nil {
		return "", nil, e
	}
	rows := []any{}
	for _, ref := range files {
		b, e := s.bytes(ref)
		if e != nil {
			return "", nil, e
		}
		rows = append(rows, map[string]any{"path": strings.TrimPrefix(ref, prefix+"/"), "sha256": "sha256:" + safefs.Digest(b)})
	}
	return contractDigest(rows), files, nil
}
func contractPNG(s *semanticSession, b []byte, viewport map[string]any) error {
	if len(b) < 24 || len(b) > 5<<20 || string(b[:8]) != "\x89PNG\r\n\x1a\n" || string(b[12:16]) != "IHDR" || int(binary.BigEndian.Uint32(b[16:20])) != contractN(viewport["width"]) || int(binary.BigEndian.Uint32(b[20:24])) != contractN(viewport["height"]) {
		return s.reject("UI_BASELINE_IMAGE", "PNG 尺寸/格式/大小与 viewport 不一致")
	}
	return nil
}
func contractExistingUI(s *semanticSession, ref string) (map[string]any, error) {
	data, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	if e = s.validateSchema(".template-spec/process/schemas/existing-ui-baseline.schema.json", data); e != nil {
		return nil, e
	}
	prefix := path.Dir(ref)
	files, e := s.scan(prefix)
	if e != nil {
		return nil, e
	}
	if len(files) > 20000 {
		return nil, s.reject("UI_BASELINE_LIMIT", "文件数量超限")
	}
	consumed := map[string]bool{path.Base(ref): true}
	total := 0
	for _, file := range files {
		b, e := s.bytes(file)
		if e != nil {
			return nil, e
		}
		total += len(b)
	}
	if total > 100<<20 {
		return nil, s.reject("UI_BASELINE_LIMIT", "包超过 100MiB")
	}
	bound := func(v any) ([]byte, error) {
		b := semMap(v)
		rel := text(b["ref"])
		if !contractPath(rel) {
			return nil, s.reject("UI_BASELINE_PATH", "基线证据须为安全相对路径")
		}
		bytes, e := s.bytes(path.Join(prefix, rel))
		if e != nil {
			return nil, e
		}
		consumed[rel] = true
		if len(bytes) == 0 || "sha256:"+safefs.Digest(bytes) != text(b["digest"]) {
			return nil, s.reject("UI_BASELINE_STALE", "原始证据摘要不一致")
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
		m, ok := object(value)
		if !ok {
			return nil, s.reject("UI_BASELINE_FORMAT", "证据须为对象")
		}
		return m, nil
	}
	source := semMap(data["source"])
	manifest, e := document(source["manifest"])
	if e != nil {
		return nil, e
	}
	digest, sourceFiles, e := contractTree(s, path.Join(prefix, text(source["root_ref"])))
	if e != nil {
		return nil, e
	}
	if digest != text(source["digest"]) {
		return nil, s.reject("UI_BASELINE_STALE", "固定源码摘要不一致")
	}
	if _, e = bound(source["lock"]); e != nil {
		return nil, e
	}
	if !strings.HasPrefix(text(semMap(source["lock"])["ref"]), text(source["root_ref"])+"/") {
		return nil, s.reject("UI_BASELINE_LOCK", "锁文件不属于固定源码")
	}
	if contractN(manifest["schema_version"]) != 1 || text(manifest["kind"]) != "existing-ui-source-observation" {
		return nil, s.reject("UI_BASELINE_SOURCE", "源码观测 manifest 类型无效")
	}
	for _, key := range []string{"repository_id", "project_id", "source_commit"} {
		if manifest[key] != source[key] {
			return nil, s.reject("UI_BASELINE_SOURCE", "源码身份冲突")
		}
	}
	rows := []any{}
	sourcePaths := map[string]bool{}
	for _, file := range sourceFiles {
		rel := strings.TrimPrefix(file, prefix+"/")
		consumed[rel] = true
		sourcePaths[rel] = true
		b, e := s.bytes(file)
		if e != nil {
			return nil, e
		}
		rows = append(rows, map[string]any{"path": strings.TrimPrefix(file, path.Join(prefix, text(source["root_ref"]))+"/"), "digest": "sha256:" + safefs.Digest(b)})
	}
	if manifest["source_digest"] != source["digest"] || !contractSame(contractSortRows(semList(manifest["files"]), "path"), contractSortRows(rows, "path")) {
		return nil, s.reject("UI_BASELINE_SOURCE", "观测文件集与源码不一致")
	}
	routePrefix := ""
	if data["api_route_mapping"] != nil {
		mapping := semMap(data["api_route_mapping"])
		for _, key := range []string{"config", "environment"} {
			if !sourcePaths[text(semMap(mapping[key])["ref"])] {
				return nil, s.reject("UI_BASELINE_PROXY", "代理映射未绑定固定源码")
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
			return nil, s.reject("UI_BASELINE_PROXY", "未支持的固定 Vite prefix rewrite")
		}
		values := []string{}
		for _, line := range strings.Split(string(environment), "\n") {
			if regexp.MustCompile(`^\s*VITE_API_BASE_URL\s*=`).MatchString(line) {
				values = append(values, strings.TrimSpace(strings.SplitN(line, "=", 2)[1]))
			}
		}
		if len(values) != 1 || values[0] != text(mapping["prefix"]) {
			return nil, s.reject("UI_BASELINE_PROXY", "环境 prefix 不一致")
		}
		routePrefix = text(mapping["prefix"])
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
	if !regexp.MustCompile(`^3[.]1[.][0-9]+$`).MatchString(text(openapi["openapi"])) || openapi["paths"] == nil {
		return nil, s.reject("UI_BASELINE_API", "需 OpenAPI 3.1 原始接口")
	}
	for _, record := range []map[string]any{build, capture} {
		if contractN(record["schema_version"]) != 1 || record["source_commit"] != source["source_commit"] || record["source_digest"] != source["digest"] || text(record["command"]) == "" || !contractDate(record["executed_at"]) {
			return nil, s.reject("UI_BASELINE_EXECUTION", "构建/截图未绑定当前源码和实际执行")
		}
		exit, ok := integer(record["exit_code"])
		if !ok || exit != 0 || len(semList(record["evidence"])) == 0 {
			return nil, s.reject("UI_BASELINE_EXECUTION", "构建/截图退出码或原始日志缺失")
		}
		for _, evidence := range semList(record["evidence"]) {
			if _, e = bound(evidence); e != nil {
				return nil, e
			}
		}
	}
	output := semMap(build["output"])
	outputDigest, outputs, e := contractTree(s, path.Join(prefix, text(output["ref"])))
	if e != nil {
		return nil, e
	}
	if outputDigest != text(output["digest"]) || build["lock_digest"] != semMap(source["lock"])["digest"] {
		return nil, s.reject("UI_BASELINE_BUILD", "构建输出或锁文件变化")
	}
	for _, ref := range outputs {
		consumed[strings.TrimPrefix(ref, prefix+"/")] = true
	}
	if capture["build_digest"] != semMap(data["build"])["digest"] || capture["openapi_digest"] != semMap(data["openapi"])["digest"] || text(capture["ui_change"]) != "none" {
		return nil, s.reject("UI_BASELINE_CAPTURE", "截图来源或 UI 改动冲突")
	}
	ids := map[string]bool{}
	for _, v := range semList(data["cases"]) {
		item := semMap(v)
		id := text(item["case_id"])
		if ids[id] {
			return nil, s.reject("UI_BASELINE_CASE", "用例重复")
		}
		ids[id] = true
		for _, ref := range semStrings(item["source_refs"]) {
			if !sourcePaths[ref] {
				return nil, s.reject("UI_BASELINE_CASE", "用例源码悬空")
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
		if e = contractPNG(s, image, semMap(item["viewport"])); e != nil {
			return nil, e
		}
		if contractN(api["schema_version"]) != 1 || api["case_id"] != item["case_id"] || api["openapi_digest"] != semMap(data["openapi"])["digest"] || api["source_digest"] != source["digest"] || api["build_digest"] != semMap(data["build"])["digest"] || len(semList(api["exchanges"])) == 0 {
			return nil, s.reject("UI_BASELINE_API", "真实 API 证据来源冲突")
		}
		for _, v := range semList(api["exchanges"]) {
			exchange := semMap(v)
			status, ok := integer(exchange["status"])
			observed, e := url.Parse(text(exchange["url"]))
			if e != nil || !semHas([]string{"http", "https"}, observed.Scheme) || text(exchange["operation_id"]) == "" || text(exchange["method"]) == "" || !contractDate(exchange["executed_at"]) || !ok || status < 100 || status > 599 {
				return nil, s.reject("UI_BASELINE_API", "API 交换记录非法")
			}
			routes := []string{}
			for route, v := range semMap(openapi["paths"]) {
				for method, v := range semMap(v) {
					if strings.EqualFold(method, text(exchange["method"])) && semMap(v)["operationId"] == exchange["operation_id"] {
						routes = append(routes, route)
					}
				}
			}
			if len(routes) != 1 {
				return nil, s.reject("UI_BASELINE_API", "API operation 与冻结接口不一致")
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
					return nil, s.reject("UI_BASELINE_PROXY", "API URL prefix 不一致")
				}
				observedPath = strings.TrimPrefix(observedPath, routePrefix)
			}
			if !regexp.MustCompile("^" + strings.Join(parts, "/") + "$").MatchString(observedPath) {
				return nil, s.reject("UI_BASELINE_API", "API URL 路径不一致")
			}
			for _, key := range []string{"request", "response"} {
				if _, e = bound(exchange[key]); e != nil {
					return nil, e
				}
			}
		}
		matches := []map[string]any{}
		for _, v := range semList(capture["cases"]) {
			c := semMap(v)
			if c["case_id"] == item["case_id"] {
				matches = append(matches, c)
			}
		}
		if len(matches) != 1 {
			return nil, s.reject("UI_BASELINE_CAPTURE", "capture 用例缺失或重复")
		}
		for _, key := range []string{"route", "state", "viewport", "source_refs", "actions", "image", "api"} {
			if !contractSame(matches[0][key], item[key]) {
				return nil, s.reject("UI_BASELINE_CAPTURE", "截图动作绑定冲突")
			}
		}
	}
	if len(semList(capture["cases"])) != len(ids) {
		return nil, s.reject("UI_BASELINE_CAPTURE", "capture 有未登记用例")
	}
	for _, file := range files {
		if !consumed[strings.TrimPrefix(file, prefix+"/")] {
			return nil, s.reject("UI_BASELINE_PAYLOAD", "包含未绑定原始文件")
		}
	}
	return data, nil
}
func contractSortRows(rows []any, key string) []any {
	out := append([]any(nil), rows...)
	sort.Slice(out, func(i, j int) bool { return text(semMap(out[i])[key]) < text(semMap(out[j])[key]) })
	return out
}
func contractVisualBaseline(s *semanticSession, ref string) (map[string]any, error) {
	data, e := s.doc(ref)
	if e != nil {
		return nil, e
	}
	if e = contractRequired(s, data, "schema_version", "baseline_id", "feature", "version", "status", "bundle", "source", "capture_environment", "cases"); e != nil {
		return nil, e
	}
	bundle, source, env := semMap(data["bundle"]), semMap(data["source"]), semMap(data["capture_environment"])
	if contractN(data["schema_version"]) != 1 || text(data["status"]) != "approved" || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(text(data["feature"])) || text(data["baseline_id"]) != "visual-baseline."+text(data["feature"]) || !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(text(data["version"])) || text(bundle["format"]) != "portable-directory" || contractN(bundle["max_image_bytes"]) != 5<<20 || contractN(bundle["max_bundle_bytes"]) != 100<<20 {
		return nil, s.reject("VISUAL_BASELINE", "基线身份、状态或包限制错误")
	}
	if e = contractRequired(s, env, "browser_version", "operating_system", "fonts_digest", "locale", "timezone", "capture_script_ref", "capture_script_digest", "capture_result_ref", "capture_result_digest"); e != nil {
		return nil, e
	}
	if text(env["browser"]) != "chromium" || contractN(env["device_scale_factor"]) != 1 || text(env["color_space"]) != "srgb" || env["animations_disabled"] != true || env["cursor_hidden"] != true {
		return nil, s.reject("VISUAL_BASELINE_CAPTURE", "截图环境未满足固定条件")
	}
	prefix := path.Dir(ref)
	consumed := map[string]bool{}
	total := 0
	bound := func(ref, digest string, expectSize int) ([]byte, error) {
		if !contractPath(ref) || !isSHA256(strings.TrimPrefix(digest, "sha256:")) {
			return nil, s.reject("VISUAL_BASELINE_PATH", "基线文件或摘要非法")
		}
		bytes, e := s.bytes(path.Join(prefix, ref))
		if e != nil {
			return nil, e
		}
		if "sha256:"+safefs.Digest(bytes) != digest || expectSize >= 0 && len(bytes) != expectSize {
			return nil, s.reject("VISUAL_BASELINE_STALE", "基线原始字节变化")
		}
		if !consumed[ref] {
			total += len(bytes)
			consumed[ref] = true
		}
		return bytes, nil
	}
	for _, key := range []string{"prototype", "interaction_spec", "state_matrix"} {
		ref := text(source[key+"_ref"])
		if !strings.HasPrefix(ref, "sources/") {
			return nil, s.reject("VISUAL_BASELINE_PATH", "业务源快照须在 sources/")
		}
		if _, e = bound(ref, text(source[key+"_digest"]), -1); e != nil {
			return nil, e
		}
	}
	for _, key := range []string{"capture_script", "capture_result"} {
		ref := text(env[key+"_ref"])
		if !strings.HasPrefix(ref, "capture/") {
			return nil, s.reject("VISUAL_BASELINE_PATH", "截图证据须在 capture/")
		}
		if _, e = bound(ref, text(env[key+"_digest"]), -1); e != nil {
			return nil, e
		}
	}
	ids, viewports := map[string]bool{}, map[string]bool{}
	for _, v := range semList(data["cases"]) {
		item := semMap(v)
		if e = contractRequired(s, item, "case_id", "route", "page", "state", "viewport", "theme", "locale", "data_scenario", "image_ref", "image_digest", "image_size_bytes", "mask_ref", "mask_digest", "mask_size_bytes", "semantic_refs", "allowed_differences", "result"); e != nil {
			return nil, e
		}
		id := text(item["case_id"])
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(id) || ids[id] || text(item["result"]) != "passed" {
			return nil, s.reject("VISUAL_BASELINE_CASE", "用例 ID/状态错误")
		}
		ids[id] = true
		viewport := semMap(item["viewport"])
		if e = contractRequired(s, viewport, "name", "width", "height", "scroll_mode", "scroll_position"); e != nil {
			return nil, e
		}
		if contractN(viewport["width"]) <= 0 || contractN(viewport["height"]) <= 0 || !semHas([]string{"viewport", "segment"}, text(viewport["scroll_mode"])) || text(viewport["scroll_mode"]) == "viewport" && contractN(viewport["scroll_position"]) != 0 {
			return nil, s.reject("VISUAL_BASELINE_VIEWPORT", "viewport 无效")
		}
		viewports[fmt.Sprintf("%dx%d", contractN(viewport["width"]), contractN(viewport["height"]))] = true
		for _, ref := range semStrings(item["semantic_refs"]) {
			if !semHas([]any{source["prototype_ref"], source["interaction_spec_ref"], source["state_matrix_ref"]}, ref) {
				return nil, s.reject("VISUAL_BASELINE_SEMANTIC", "语义引用未绑定业务快照")
			}
		}
		if !contractUnique(item["semantic_refs"], true) {
			return nil, s.reject("VISUAL_BASELINE_SEMANTIC", "缺少语义引用")
		}
		imageRef := text(item["image_ref"])
		if !strings.HasPrefix(imageRef, "images/") || !strings.HasSuffix(imageRef, ".png") {
			return nil, s.reject("VISUAL_BASELINE_IMAGE", "图片路径非法")
		}
		image, e := bound(imageRef, text(item["image_digest"]), contractN(item["image_size_bytes"]))
		if e != nil {
			return nil, e
		}
		if e = contractPNG(s, image, viewport); e != nil {
			return nil, e
		}
		if text(item["mask_ref"]) == "not-applicable" {
			if text(item["mask_digest"]) != "not-applicable" || contractN(item["mask_size_bytes"]) != 0 {
				return nil, s.reject("VISUAL_BASELINE_MASK", "无 mask 占位合同错误")
			}
		} else {
			maskRef := text(item["mask_ref"])
			if !strings.HasPrefix(maskRef, "masks/") || !strings.HasSuffix(maskRef, ".png") {
				return nil, s.reject("VISUAL_BASELINE_MASK", "mask 路径非法")
			}
			mask, e := bound(maskRef, text(item["mask_digest"]), contractN(item["mask_size_bytes"]))
			if e != nil {
				return nil, e
			}
			if e = contractPNG(s, mask, viewport); e != nil {
				return nil, e
			}
		}
	}
	if len(ids) == 0 || !viewports["1440x900"] || !viewports["390x844"] || total != contractN(bundle["size_bytes"]) || total > 100<<20 {
		return nil, s.reject("VISUAL_BASELINE_LIMIT", "缺少规定 viewport 或实际包大小不一致")
	}
	files, e := s.scan(prefix)
	if e != nil {
		return nil, e
	}
	for _, file := range files {
		rel := strings.TrimPrefix(file, prefix+"/")
		if rel != "visual-baseline.yaml" && !consumed[rel] {
			return nil, s.reject("VISUAL_BASELINE_PAYLOAD", "存在未绑定 payload")
		}
	}
	raw, e := s.bytes(ref)
	if e != nil {
		return nil, e
	}
	digest, e := contractVisualDigest(raw)
	if e != nil {
		return nil, s.unavailable("INPUT", e.Error())
	}
	if digest != text(bundle["digest"]) {
		return nil, s.reject("VISUAL_BASELINE_STALE", "manifest 插入序摘要不一致")
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
func verifyFrontendDeliverySemantic(s *semanticSession, ref string, opts map[string]string) error {
	if opts["phase"] != "" && !semHas([]string{"preflight", "design", "contract", "inputs", "implementation", "verification"}, opts["phase"]) {
		return s.reject("FRONTEND_PHASE", "未知前端交付阶段")
	}
	if opts["unit"] != "" && ref != opts["checkpoint"] {
		return s.reject("FRONTEND_BINDING", "外部接收记录不得伪装本地工作单元输入")
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
		if text(c.Raw["status"]) != "approved" && opts["phase"] != "inputs" {
			return s.reject("SLICE_APPROVAL_REQUIRED", "前端实现须当前批准 Slice")
		}
	}
	return contractFrontendDelivery(s, binding, c, opts)
}
func contractFrontendDelivery(s *semanticSession, binding map[string]any, c *nativeSlice, opts map[string]string) error {
	ref := first(text(binding["acceptance_ref"]), text(binding["ref"]))
	if ref == "" {
		return s.reject("FRONTEND_DELIVERY_REQUIRED", "前端合同缺少接收记录")
	}
	doc, e := s.doc(ref)
	if e != nil {
		return e
	}
	n := contractN(doc["schema_version"])
	schemaRef := map[int]string{1: ".template-spec/process/schemas/frontend-delivery-acceptance-v1.schema.json", 2: ".template-spec/process/schemas/frontend-delivery-acceptance.schema.json", 3: ".template-spec/process/schemas/frontend-delivery-acceptance-v3.schema.json"}[n]
	if schemaRef == "" {
		return s.unavailable("CAPABILITY", "未知前端接收协议")
	}
	if e = s.validateSchema(schemaRef, doc); e != nil {
		return e
	}
	b, e := s.bind(ref)
	if e != nil {
		return e
	}
	if text(binding["digest"]) != "" && text(binding["digest"]) != b.Digest {
		return s.reject("FRONTEND_DELIVERY_STALE", "接收原字节变化")
	}
	slice := opts["slice"]
	if c != nil {
		slice = text(c.Raw["slice_id"])
	}
	if slice == "" || text(doc["slice_id"]) != slice {
		return s.reject("FRONTEND_DELIVERY_SLICE", "前端接收与执行切片冲突")
	}
	if n >= 2 {
		preflightBinding := semMap(doc["strategic_preflight"])
		preflight, e := contractBoundDoc(s, preflightBinding)
		if e != nil {
			return e
		}
		pn := contractN(preflight["schema_version"])
		preflightSchema := ".template-spec/process/schemas/frontend-strategic-preflight.schema.json"
		if pn == 2 {
			preflightSchema = ".template-spec/process/schemas/frontend-strategic-preflight-v2.schema.json"
		}
		if pn != 1 && pn != 2 {
			return s.unavailable("CAPABILITY", "未知前端战略预检协议")
		}
		if e = s.validateSchema(preflightSchema, preflight); e != nil {
			return e
		}
		if text(preflight["status"]) != "verified" || (n == 3) != (pn == 2) || pn == 2 && preflight["ui_baseline_kind"] != doc["ui_baseline_kind"] {
			return s.reject("FRONTEND_PREFLIGHT", "预检状态或协议版本冲突")
		}
		receipt, e := s.doc(text(preflight["import_receipt_ref"]))
		if e != nil {
			return e
		}
		base := path.Join("docs/handoffs", text(receipt["bundle_id"]), text(receipt["version"]))
		if !semHas([]string{"2", "3"}, fmt.Sprint(contractN(receipt["schema_version"]))) || text(preflight["import_receipt_ref"]) != base+"/import-receipt.json" || text(receipt["package_ref"]) != base+"/package" || preflight["bundle_digest"] != receipt["bundle_digest"] || preflight["bundle_digest"] != semMap(doc["strategic_handoff"])["bundle_digest"] {
			return s.reject("FRONTEND_PREFLIGHT", "预检收据身份或战略包版本冲突")
		}
		if e = contractReceipt(s, receipt); e != nil {
			return e
		}
		if e = contractReceiptConsumer(s, receipt, "frontend-engineering-design"); e != nil {
			return e
		}
		if e = s.verify("context-reconciliation", text(preflight["context_reconciliation_ref"]), map[string]string{"import-receipt": text(preflight["import_receipt_ref"])}); e != nil {
			return e
		}
		bundle, e := contractOpenHandoff(s, text(receipt["package_ref"]))
		if e != nil {
			return e
		}
		route := apFind(bundle.Handoff["consumer_routes"], "capability", "frontend-engineering-design")
		backend := apFind(bundle.Handoff["consumer_routes"], "capability", "backend-technical-design")
		routeReceipt := apFind(receipt["routes"], "capability", "frontend-engineering-design")
		if route == nil || routeReceipt == nil || text(route["activation"]) == "not-applicable" || route["route_id"] != preflight["route_id"] || routeReceipt["route_id"] != preflight["route_id"] || (pn == 2) != (contractN(bundle.Handoff["schema_version"]) == 5) {
			return s.reject("FRONTEND_PREFLIGHT", "当前交接路线或协议降级冲突")
		}
		sourceIDs := []string{}
		for _, v := range bundle.Rules {
			sourceIDs = append(sourceIDs, text(semMap(v)["rule_id"]))
		}
		for _, v := range bundle.Scenarios {
			m := semMap(v)
			if m["critical"] == true {
				sourceIDs = append(sourceIDs, text(m["scenario_id"]))
			}
		}
		if !semSameSet(sourceIDs, preflight["source_rule_refs"]) {
			return s.reject("FRONTEND_PREFLIGHT", "未完整绑定来源规则/关键场景")
		}
		visual := contractHandoffUIRef(bundle.Handoff)
		expectedVisual := path.Join(base, "package/payload/files", text(visual["persisted_ref"]), text(visual["manifest_ref"]))
		key := "visual_baseline_ref"
		if pn == 2 {
			key = "ui_baseline_ref"
		}
		if text(preflight[key]) != expectedVisual {
			return s.reject("FRONTEND_PREFLIGHT", "预检 UI baseline 引用冲突")
		}
		expectedMode := "required"
		if text(backend["activation"]) == "not-applicable" {
			expectedMode = "not-applicable"
		}
		dep := semMap(preflight["backend_dependency"])
		if text(dep["mode"]) != expectedMode || dep["route_id"] != backend["route_id"] || text(semMap(doc["backend_dependency"])["mode"]) != expectedMode {
			return s.reject("FRONTEND_PREFLIGHT", "后端依赖模式与当前路线冲突")
		}
		if expectedMode == "not-applicable" {
			for _, key := range []string{"reason", "impact_refs", "evidence_refs"} {
				if !contractSame(dep[key], backend[key]) {
					return s.reject("FRONTEND_PREFLIGHT", "后端不适用依据冲突")
				}
			}
			if doc["backend_delivery"] != nil {
				return s.reject("FRONTEND_DELIVERY", "后端不适用不得绑定交付收据")
			}
			if e = contractHandoffConsumption(s, doc, map[string]string{"consumer": "frontend", "slice": slice}); e != nil {
				return e
			}
			ids := map[string]bool{}
			for _, v := range semList(doc["frontend_cases"]) {
				item := semMap(v)
				id := text(item["case_id"])
				if id == "" || ids[id] || len(semStrings(item["operation_ids"])) > 0 {
					return s.reject("FRONTEND_CASE", "用例重复或无 API 分支声明接口")
				}
				ids[id] = true
				for _, source := range semStrings(item["source_ids"]) {
					if !semHas(sourceIDs, source) {
						return s.reject("FRONTEND_CASE", "未知来源规则/场景")
					}
				}
				caseKey := "visual_case_ids"
				if n == 3 {
					caseKey = "baseline_case_ids"
				}
				for _, id := range semStrings(item[caseKey]) {
					if !semHas(visual["case_ids"], id) {
						return s.reject("FRONTEND_CASE", "未知基线用例")
					}
				}
				evidence, e := s.bytes(text(item["evidence_ref"]))
				if e != nil {
					return e
				}
				if len(evidence) == 0 || "sha256:"+safefs.Digest(evidence) != text(item["evidence_digest"]) {
					return s.reject("FRONTEND_CASE", "前端用例说明为空或摘要变化")
				}
			}
			return nil
		}
	}
	return contractFrontendBackendDelivery(s, doc, slice, opts)
}

func contractFrontendBackendDelivery(s *semanticSession, acceptance map[string]any, slice string, opts map[string]string) error {
	binding := semMap(acceptance["backend_delivery"])
	receipt, e := s.doc(text(binding["import_receipt_ref"]))
	if e != nil {
		return e
	}
	base := path.Join("docs/backend-deliveries", text(receipt["delivery_id"]), text(receipt["version"]))
	if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(text(receipt["version"])) || text(binding["import_receipt_ref"]) != base+"/import-receipt.json" || text(receipt["package_ref"]) != base+"/package" || binding["bundle_digest"] != receipt["bundle_digest"] {
		return s.reject("FRONTEND_DELIVERY", "后端收据路径、身份或摘要冲突")
	}
	names, e := s.list(path.Dir(base))
	if e != nil {
		return e
	}
	latest, greatest := "", 0
	for _, name := range names {
		if !regexp.MustCompile(`^v[1-9][0-9]*$`).MatchString(name) {
			continue
		}
		exists, e := s.exists(path.Join(path.Dir(base), name, "import-receipt.json"))
		if e != nil {
			return e
		}
		if !exists {
			continue
		}
		version, e := strconv.Atoi(name[1:])
		if e != nil {
			return s.reject("FRONTEND_DELIVERY", "后端收据版本超限")
		}
		if version > greatest {
			greatest, latest = version, name
		}
	}
	if latest != text(receipt["version"]) {
		return s.reject("FRONTEND_DELIVERY_STALE", "已有更新后端收据，当前前端接收已过期")
	}
	manifest, source, e := backendOpenDelivery(s, text(receipt["package_ref"]), opts)
	if e != nil {
		return e
	}
	if manifest["bundle_digest"] != receipt["bundle_digest"] || manifest["delivery_id"] != receipt["delivery_id"] || manifest["version"] != receipt["version"] {
		return s.reject("FRONTEND_DELIVERY", "后端包与接收收据冲突")
	}
	delivery, e := source.doc(text(manifest["delivery_ref"]))
	if e != nil {
		return e
	}
	if semMap(delivery["scope"])["slice_id"] != slice || delivery["strategic_bundle_digest"] != semMap(acceptance["strategic_handoff"])["bundle_digest"] {
		return s.reject("FRONTEND_DELIVERY", "后端、战略和前端切片版本冲突")
	}
	if e = contractHandoffConsumption(s, acceptance, map[string]string{"consumer": "frontend", "slice": slice}); e != nil {
		return e
	}
	bundle, e := backendOpenStrategicInput(source, text(delivery["strategic_bundle_ref"]))
	if e != nil {
		return e
	}
	n := contractN(acceptance["schema_version"])
	if n != 3 && contractN(bundle.Handoff["schema_version"]) == 5 {
		return s.reject("FRONTEND_DELIVERY", "Handoff v5 必须使用前端接收 v3")
	}
	visual := contractHandoffUIRef(bundle.Handoff)
	seen := map[string]bool{}
	for _, v := range semList(acceptance["frontend_cases"]) {
		item := semMap(v)
		id := text(item["case_id"])
		if id == "" || seen[id] || n >= 2 && len(semStrings(item["operation_ids"])) == 0 {
			return s.reject("FRONTEND_CASE", "有后端依赖的用例须唯一并绑定已交付接口")
		}
		seen[id] = true
		for _, id := range semStrings(item["operation_ids"]) {
			if !semHas(semMap(delivery["scope"])["operation_ids"], id) {
				return s.reject("FRONTEND_CASE", "前端用例依赖未交付接口")
			}
		}
		for _, id := range semStrings(item["source_ids"]) {
			covered := semHas(semMap(delivery["scope"])["source_ids"], id)
			if !covered && n == 3 && bundle.Business != nil {
				row := apFind(bundle.Business["tickets"], "id", id)
				if row != nil {
					ticket, e := bundle.Source.doc(text(row["ref"]))
					if e != nil {
						return e
					}
					related := 0
					covered = true
					for _, v := range semList(ticket["source_refs"]) {
						binding := semMap(v)
						locator := text(binding["locator"])
						if binding["locator_kind"] == "id" && (apFind(bundle.Rules, "rule_id", locator) != nil || apFind(bundle.Scenarios, "scenario_id", locator) != nil) {
							related++
							if !semHas(semMap(delivery["scope"])["source_ids"], locator) {
								covered = false
							}
						}
					}
					covered = covered && related > 0
				}
			}
			if !covered {
				return s.reject("FRONTEND_CASE", "前端用例依赖未交付规则/场景")
			}
		}
		caseKey := "visual_case_ids"
		if n == 3 {
			caseKey = "baseline_case_ids"
		}
		for _, id := range semStrings(item[caseKey]) {
			if !semHas(visual["case_ids"], id) {
				return s.reject("FRONTEND_CASE", "前端用例视觉基线悬空")
			}
		}
		b, e := s.bytes(text(item["evidence_ref"]))
		if e != nil {
			return e
		}
		if len(b) == 0 || "sha256:"+safefs.Digest(b) != text(item["evidence_digest"]) {
			return s.reject("FRONTEND_CASE", "前端用例说明为空或摘要漂移")
		}
	}
	for _, id := range semStrings(semMap(delivery["scope"])["source_ids"]) {
		row := apFind(semMap(acceptance["strategic_handoff"])["rows"], "source_id", id)
		if text(row["disposition"]) != "mapped" || !semHas(row["dependent_slice_refs"], slice) {
			return s.reject("FRONTEND_CASE", "交付规则/场景未映射当前前端切片")
		}
	}
	return contractProbeBackend(s, delivery)
}

func contractProbeBackend(s *semanticSession, delivery map[string]any) error {
	environment := semMap(delivery["environment"])
	base, e := url.Parse(text(environment["base_url"]))
	validURL := func(u *url.URL) bool {
		return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.ContainsAny(u.Host, "\\\r\n")
	}
	if e != nil || !validURL(base) {
		return s.reject("FRONTEND_PROBE_ENVIRONMENT", "服务环境 URL 无效或包含凭据、查询、片段")
	}
	revision, e := url.Parse(text(environment["revision_path"]))
	if e != nil {
		return s.reject("FRONTEND_PROBE_ENVIRONMENT", "版本探测路径非法")
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
		return s.reject("FRONTEND_PROBE_ENVIRONMENT", "版本探测必须在登记环境同源内")
	}
	expected := map[string]any{"deployment_id": environment["deployment_id"], "source_commit": semMap(delivery["build"])["source_commit"], "openapi_digest": semMap(delivery["openapi"])["digest"], "artifact_digest": semMap(delivery["build"])["artifact_digest"], "test_data_digest": semMap(environment["test_data"])["digest"]}
	pointers := semMap(environment["revision_pointers"])
	for key, value := range expected {
		pointer := text(pointers[key])
		if text(value) == "" || !strings.HasPrefix(pointer, "/") || strings.Contains(pointer, "//") {
			return s.reject("FRONTEND_PROBE_ENVIRONMENT", "版本身份或 JSON Pointer 缺失: "+key)
		}
	}
	name := text(environment["authorization_env"])
	if name != "" && !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
		return s.reject("FRONTEND_PROBE_ENVIRONMENT", "授权环境变量名称非法")
	}
	authorization := ""
	if name != "" {
		authorization = os.Getenv(name)
		if authorization == "" {
			return s.unavailable("FRONTEND_PROBE_AUTHORIZATION", "缺少已登记授权环境变量")
		}
	}
	authDigest := safefs.Digest([]byte(authorization))
	publicTarget := (&url.URL{Scheme: target.Scheme, Host: target.Host, Path: target.Path}).String()
	key := publicTarget + ":" + contractDigest(map[string]any{"url": target.String(), "expected": expected, "pointers": pointers, "authorization_env": name, "authorization_digest": authDigest})
	return s.observeOnline(key, func() error {
		if e := s.guard(); e != nil {
			return e
		}
		if name != "" && safefs.Digest([]byte(os.Getenv(name))) != authDigest {
			return s.unavailable("INPUT_DRIFT", "版本探测授权环境在核验期间变化")
		}
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if e != nil {
			return s.reject("FRONTEND_PROBE_ENVIRONMENT", "版本探测请求无效")
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
			if cancelErr := s.guard(); cancelErr != nil {
				return cancelErr
			}
			return s.unavailable("FRONTEND_PROBE_UNAVAILABLE", "当前登记服务版本不可读")
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return s.reject("FRONTEND_PROBE_HTTP", fmt.Sprintf("当前服务版本 HTTP %d", response.StatusCode))
		}
		body, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if e != nil {
			if cancelErr := s.guard(); cancelErr != nil {
				return cancelErr
			}
			return s.unavailable("FRONTEND_PROBE_UNAVAILABLE", "服务版本响应读取失败")
		}
		if len(body) > 1<<20 || !json.Valid(body) {
			return s.reject("FRONTEND_PROBE_RESPONSE", "服务版本响应超限或不是 JSON")
		}
		value, e := schema.Parse(body)
		if e != nil {
			return s.reject("FRONTEND_PROBE_RESPONSE", "服务版本 JSON 不明确")
		}
		for key, expectedValue := range expected {
			current := value
			for _, segment := range strings.Split(text(pointers[key])[1:], "/") {
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
				return s.reject("FRONTEND_PROBE_REVISION", "真实服务版本不匹配: "+key)
			}
		}
		return nil
	})
}
