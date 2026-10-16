package scaffold

import (
	"path"
	"regexp"
	"strings"
)

var (
	sha256Pattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	driftPattern  = regexp.MustCompile(`(?i)drift|stale`)
)

// platformDigestBytes is platformDigest for raw bytes (Buffer input in Node).
func platformDigestBytes(data []byte) string { return sha256Ref(data) }

func hasField(m *OMap, key string) bool { return m.Has(key) }

func portableRef(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "/") || strings.Contains(ref, `:\`) {
		return false
	}
	for _, part := range strings.FieldsFunc(ref, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return false
		}
	}
	return true
}

// platformRecipeDigest mirrors platformRecipeDigest(profile, entry).
func platformRecipeDigest(profile, entry *OMap) string {
	recipe := profile.Clone()
	recipe.Delete("candidate_blockers")
	recipe.Delete("recommended")
	orDefault := func(key string, fallback any) any {
		if value, ok := entry.Get(key); ok && value != nil {
			return value
		}
		return fallback
	}
	return platformDigest(newMap(
		"profile", recipe,
		"entry", newMap(
			"id", mustGet(entry, "id"),
			"profile_id", mustGet(entry, "profile_id"),
			"spring_boot_version", mustGet(entry, "spring_boot_version"),
			"parent", mustGet(entry, "parent"),
			"bom", mustGet(entry, "bom"),
			"components", orDefault("components", NewOMap()),
			"platform_artifact_bindings", orDefault("platform_artifact_bindings", NewOMap()),
			"external_snapshot_bindings", orDefault("external_snapshot_bindings", []any{}),
			"external_artifact_bindings", orDefault("external_artifact_bindings", []any{}),
		),
	))
}

func componentPlatformLine(profile *OMap) (string, bool) {
	line := asString(mustGet(profile, "component_platform_line"))
	if line == "boot2-java8" || line == "boot3-java17" {
		return line, true
	}
	return "", false
}

// platformBinding mirrors platformBinding(profile, entry).
func platformBinding(profile, entry *OMap) *OMap {
	binding := newMap(
		"schema_version", 2,
		"profile_id", mustGet(profile, "id"),
		"spring_boot_version", mustGet(profile, "spring_boot_version"),
		"java_version", mustGet(profile, "java_version"),
	)
	if line, ok := componentPlatformLine(profile); ok {
		binding.Set("component_platform_line", line)
	}
	binding.Set("parent", mustGet(entry, "parent"))
	binding.Set("bom", mustGet(entry, "bom"))
	binding.Set("compatibility_id", mustGet(entry, "id"))
	binding.Set("compatibility_digest", platformRecipeDigest(profile, entry))
	return binding
}

type resolvedPlatform struct {
	profile *OMap
	entry   *OMap
	reports []*OMap
}

// resolveBackendPlatform mirrors resolveBackendPlatform(binding, {catalog, requireVerified}).
func resolveBackendPlatform(src Source, catalog *Catalog, binding *OMap, requireVerified bool) (*resolvedPlatform, error) {
	if binding == nil || fieldString(binding, "schema_version") != "2" {
		return nil, fail("backend-platform: missing platform_configuration v2 (v1 is historical read-only); 生命周期须展示 Boot/Java/YSS 组合并取得用户确认")
	}
	profile, err := catalog.profile(fieldString(binding, "profile_id"), asString(mustGet(binding, "spring_boot_version")))
	if err != nil {
		return nil, err
	}
	if JSON(mustGet(binding, "spring_boot_version"), "") != JSON(mustGet(profile, "spring_boot_version"), "") || JSON(mustGet(binding, "java_version"), "") != JSON(mustGet(profile, "java_version"), "") {
		return nil, fail("backend-platform: unsupported exact Boot/Java combination (x/latest and Java 8/11 for Boot 3.5/4.1 are rejected)")
	}
	expectedLine, hasLine := componentPlatformLine(profile)
	actualLine, actualHas := mustGet(binding, "component_platform_line"), binding.Has("component_platform_line")
	if hasLine != actualHas || hasLine && actualLine != expectedLine {
		return nil, fail("backend-platform: component_platform_line does not match the selected platform profile")
	}
	var entry *OMap
	for _, item := range catalog.Compatibility {
		if mustGet(item, "id") == mustGet(binding, "compatibility_id") {
			entry = item
			break
		}
	}
	if entry == nil || fieldString(entry, "profile_id") != fieldString(profile, "id") || fieldString(entry, "spring_boot_version") != fieldString(profile, "spring_boot_version") {
		return nil, fail("backend-platform: YSS compatibility entry missing; parent/BOM/starter verification required")
	}
	if platformDigest(binding) != platformDigest(platformBinding(profile, entry)) {
		return nil, fail("backend-platform: stale compatibility digest or YSS coordinates")
	}
	resolved := &resolvedPlatform{profile: profile, entry: entry}
	if !requireVerified {
		return resolved, nil
	}
	if asString(mustGet(entry, "status")) != "verified" {
		return nil, failf("backend-platform: YSS combination %s is not verified", fieldString(entry, "id"))
	}
	evidence := asList(mustGet(entry, "evidence"))
	if len(evidence) == 0 {
		return nil, fail("backend-platform: verified combination lacks evidence")
	}
	recipe := platformRecipeDigest(profile, entry)
	for _, raw := range evidence {
		item := asMap(raw)
		ref, digest := asString(mustGet(item, "ref")), asString(mustGet(item, "digest"))
		if item == nil || ref == "" || digest == "" || !portableRef(ref) {
			return nil, fail("backend-platform: invalid portable evidence reference")
		}
		data, err := src.Read(ref)
		if err != nil {
			return nil, err
		}
		if platformDigestBytes(data) != digest {
			return nil, fail("backend-platform: compatibility evidence drift")
		}
		parsed, err := ParseJSON(data)
		report := asMap(parsed)
		if err != nil || report == nil {
			return nil, fail("backend-platform: compatibility evidence is not valid JSON")
		}
		if mustGet(report, "verification_scope") != "empty-scaffold" {
			return nil, fail("backend-platform: qualification requires empty-scaffold evidence")
		}
		if mustGet(report, "recipe_digest") != recipe {
			return nil, fail("backend-platform: evidence recipe drift")
		}
		family := asString(mustGet(report, "architecture_family"))
		fingerprint, err := platformSourceFingerprint(src, family)
		if err != nil {
			return nil, err
		}
		if !report.Has("source_fingerprint") || JSON(mustGet(report, "source_fingerprint"), "") != JSON(fingerprint, "") {
			return nil, fail("platform-provenance: generator/verifier source drift; requalify candidate")
		}
		if !sha256Pattern.MatchString(asString(mustGet(report, "generated_tree_digest"))) {
			return nil, fail("backend-platform: missing generated tree digest")
		}
		resolved.reports = append(resolved.reports, report)
		if mustGet(report, "status") != "passed" || JSON(mustGet(report, "spring_boot_version"), "") != JSON(mustGet(profile, "spring_boot_version"), "") ||
			JSON(mustGet(report, "java_version"), "") != JSON(mustGet(profile, "java_version"), "") ||
			family != asString(mustGet(item, "architecture_family")) ||
			platformDigest(mustGet(report, "parent")) != platformDigest(mustGet(entry, "parent")) ||
			platformDigest(mustGet(report, "bom")) != platformDigest(mustGet(entry, "bom")) {
			return nil, fail("backend-platform: compatibility evidence identity mismatch")
		}
		for _, phase := range []string{"validate", "test", "package"} {
			if !hasMavenEvidence(report, phase) {
				return nil, fail("backend-platform: missing real Maven evidence")
			}
		}
		if mustGet(report, "dependency_check") != "passed" || mustGet(report, "startup_check") != "passed" || mustGet(asMap(mustGet(report, "integration_tests")), "status") != "passed" {
			return nil, fail("backend-platform: missing dependency/startup/integration-test evidence")
		}
		required := []string{"effective-pom.xml", "dependency-trees.json", "platform-tests.xml", "boot-bom-effective.xml", "runtime-jar-entries.log", "startup.stdout.log", "startup.stderr.log"}
		for _, phase := range []string{"validate", "test", "package"} {
			required = append(required, "mvnw-"+phase+".stdout.log", "mvnw-"+phase+".stderr.log")
		}
		artifacts := asList(mustGet(report, "evidence_artifacts"))
		if artifacts == nil {
			return nil, fail("backend-platform: missing portable evidence artifacts")
		}
		for _, name := range required {
			found := false
			for _, artifact := range artifacts {
				found = found || mustGet(asMap(artifact), "ref") == name
			}
			if !found {
				return nil, fail("backend-platform: missing portable evidence artifacts")
			}
		}
		for _, raw := range artifacts {
			artifact := asMap(raw)
			artifactRef := asString(mustGet(artifact, "ref"))
			if artifactRef == "" || !portableRef(artifactRef) {
				return nil, fail("backend-platform: invalid evidence artifact path")
			}
			content, err := src.Read(path.Join(path.Dir(ref), artifactRef))
			if err != nil {
				return nil, err
			}
			if platformDigestBytes(content) != mustGet(artifact, "digest") {
				return nil, fail("backend-platform: evidence artifact drift")
			}
		}
	}
	return resolved, nil
}

func hasMavenEvidence(report *OMap, phase string) bool {
	for _, raw := range asList(mustGet(report, "commands")) {
		command := asMap(raw)
		text := asString(mustGet(command, "command"))
		match := text == "./mvnw "+phase || mustGet(command, "phase") == phase && strings.HasPrefix(text, "./mvnw "+phase+" ") && !strings.Contains(text, "skipTests") && !strings.Contains(text, "maven.test.skip")
		if match && fieldString(command, "exit_code") == "0" && truthyValue(mustGet(command, "stdout_ref")) && truthyValue(mustGet(command, "stderr_ref")) && truthyValue(mustGet(command, "executed_at")) {
			return true
		}
	}
	return false
}

func truthyValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case bool:
		return v
	case Num:
		return jsString(v) != "0"
	}
	return true
}

// assertContractPlatform mirrors assertContractPlatform(contract, decision, options).
func assertContractPlatform(src Source, catalog *Catalog, contract, decision *OMap, requireVerified bool) (*OMap, error) {
	binding := asMap(mustGet(contract, "platform_configuration"))
	resolved, err := resolveBackendPlatform(src, catalog, binding, requireVerified)
	if err != nil {
		return nil, err
	}
	profiles := asMap(mustGet(contract, "profiles"))
	if mustGet(profiles, "platform") != mustGet(resolved.profile, "id") || mustGet(profiles, "validation_namespace") != mustGet(resolved.profile, "validation_namespace") {
		return nil, fail("backend-platform: platform and validation_namespace are inconsistent")
	}
	coordinates := asMap(mustGet(contract, "maven_coordinates"))
	bom := asMap(mustGet(binding, "bom"))
	if platformDigest(mustGet(coordinates, "parent")) != platformDigest(mustGet(binding, "parent")) || mustGet(coordinates, "yss_components_version") != mustGet(bom, "version") || mustGet(bom, "group_id") != "com.yss.cloud" || mustGet(bom, "artifact_id") != "yss-components-bom" {
		return nil, fail("backend-platform: YSS Maven coordinates mismatch")
	}
	if decision != nil && platformDigest(mustGet(decision, "platform_configuration")) != platformDigest(binding) {
		return nil, fail("backend-platform: decision/contract platform_configuration mismatch")
	}
	family := mustGet(contract, "architecture_family")
	if requireVerified {
		found := false
		for _, item := range asList(mustGet(resolved.entry, "evidence")) {
			found = found || mustGet(asMap(item), "architecture_family") == family
		}
		if !found {
			return nil, fail("backend-platform: architecture has no compatibility evidence")
		}
	}
	requested := stringList(mustGet(asMap(mustGet(contract, "module_profile")), "requested_capabilities"))
	known := stringList(mustGet(resolved.entry, "capabilities"))
	for _, capability := range requested {
		if !containsString(known, capability) {
			return nil, failf("backend-platform: unverified capability: %s", capability)
		}
	}
	if requireVerified {
		ok := false
		for _, report := range resolved.reports {
			if mustGet(report, "architecture_family") != family {
				continue
			}
			verified := stringList(mustGet(report, "verified_capabilities"))
			all := true
			for _, capability := range requested {
				all = all && containsString(verified, capability)
			}
			ok = ok || all
		}
		if !ok {
			return nil, fail("backend-platform: architecture/capability combination lacks evidence")
		}
	}
	return resolved.profile, nil
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
