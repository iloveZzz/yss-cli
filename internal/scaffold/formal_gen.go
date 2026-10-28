package scaffold

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

var downstreamSkills = []string{
	"yss-domain", "yss-application", "yss-repository", "yss-mybatis", "yss-web-controller", "yss-dto",
	"yss-exception", "yss-validation", "mapstruct", "lombok", "alibaba-java-code-style",
}

const compilerContractRef = ".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml"

func rawSHA256(src Source, ref string) (string, error) {
	data, err := src.Read(ref)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func referenceFields(manifest, contract *OMap) {
	reference, ok := contract.Get("reference_example")
	if !ok || !truthyValue(reference) {
		return
	}
	manifest.Set("reference_example", reference)
	status := "not-applicable"
	if mustGet(asMap(reference), "enabled") == true {
		status = "not-executed"
	}
	manifest.Set("reference_verification", newMap("status", status, "commands", []any{}))
}

func (g *dddGen) validateFormalContract(ctx context.Context) error {
	flow := &formalFlow{src: g.src, o: g.o, family: "domain-driven", skill: dddSkill, kind: "backend-ddd", profile: "target-domain-model"}
	state, platform, err := flow.validate(ctx)
	if err != nil {
		return err
	}
	g.formal, g.contract, g.platform = state, state.contract, platform
	g.coordinates = asMap(cloneValue(mustGet(state.contract, "maven_coordinates")))
	g.coordinateSrc = "approved-contract"
	g.profiles = asMap(cloneValue(mustGet(state.contract, "profiles")))
	return nil
}

func (g *dddGen) formalManifest(t *Tree, fingerprint, generator, ownership *OMap) (*OMap, error) {
	state, contract, o := g.formal, g.contract, g.o
	downstream := NewOMap()
	for _, skill := range downstreamSkills {
		root := ".agents/skills/" + skill
		if !g.src.Has(root + "/SKILL.md") {
			downstream.Set(skill, "unavailable")
			continue
		}
		digest, err := sourceTreeDigest(g.src, root)
		if err != nil {
			return nil, err
		}
		downstream.Set(skill, digest)
	}
	baseline, err := rawSHA256(g.src, dddSkillRoot+"/references/engineering-baseline.md")
	if err != nil {
		return nil, err
	}
	compiler, err := rawSHA256(g.src, compilerContractRef)
	if err != nil {
		return nil, err
	}
	ruleset, err := rawSHA256(g.src, dddAssets+"/templates/java/architecture-rules-test.java.template")
	if err != nil {
		return nil, err
	}
	number, _ := o.contractVersionNumber()
	manifest := newMap(
		"source_fingerprint", fingerprint,
		"generated_tree_digest", generatedTreeDigest(t),
		"platform_verification", state.platformStatus,
		"platform_configuration", mustGet(contract, "platform_configuration"),
		"schema_version", mustGet(contract, "schema_version"),
	)
	referenceFields(manifest, contract)
	manifest.Set("kind", "backend-scaffold")
	manifest.Set("architecture_profile", mustGet(contract, "architecture_profile"))
	manifest.Set("architecture_identity", scaffoldArchitectureIdentity(contract, state.digestHex))
	manifest.Set("contract_id", o.ContractID)
	manifest.Set("contract_version", number)
	manifest.Set("scaffold_request_id", mustGet(contract, "scaffold_request_id"))
	manifest.Set("architecture_family", mustGet(contract, "architecture_family"))
	manifest.Set("generator_skill", mustGet(contract, "generator_skill"))
	manifest.Set("decision_id", mustGet(contract, "decision_id"))
	manifest.Set("decision_digest", mustGet(contract, "decision_digest"))
	manifest.Set("module_profile", mustGet(contract, "module_profile"))
	manifest.Set("design_prerequisites", state.prerequisites)
	manifest.Set("approval_ref", o.ApprovalRef)
	manifest.Set("compiler_draft_ref", o.CompilerDraftRef)
	manifest.Set("persisted_ref", o.PersistedRef)
	manifest.Set("contract_file_ref", state.contractFile)
	manifest.Set("contract_digest", state.digestHex)
	manifest.Set("lifecycle_approval_ref", mustGet(contract, "lifecycle_approval_ref"))
	manifest.Set("current_version", mustGet(contract, "current_version"))
	manifest.Set("approver", mustGet(asMap(mustGet(contract, "approval")), "approver"))
	manifest.Set("allowed_write_paths", mustGet(contract, "allowed_write_paths"))
	manifest.Set("expected_evidence_files", mustGet(contract, "expected_evidence_files"))
	manifest.Set("project_name", g.name)
	manifest.Set("base_package", g.basePackage)
	manifest.Set("bootstrap_main_class", g.basePackage+"."+g.appClass)
	manifest.Set("bootstrap_main_source", g.mainSource)
	manifest.Set("maven_coordinates", g.coordinates)
	manifest.Set("maven_coordinates_source", g.coordinateSrc)
	manifest.Set("profiles", g.profiles)
	manifest.Set("generation_mode", "controlled-generation")
	manifest.Set("completion_level", "generated")
	manifest.Set("generator", generator)
	manifest.Set("ownership", ownership)
	manifest.Set("readiness", newMap(
		"downstream_skills", downstream,
		"contracts", newMap("engineering_baseline", baseline, "compiler_contract", compiler),
		"architecture_ruleset", ruleset,
	))
	manifest.Set("generation_policy", newMap("mode", "initialize-only", "existing_target", "unsupported", "old_project_migration", "unsupported", "template_upgrade", "unsupported"))
	manifest.Set("verification_commands", commandList())
	manifest.Set("generated_at", o.renderContext.GeneratedAt)
	return manifest, nil
}

// preflight mirrors the first steps of the MVC generate(): the contract's
// architecture profile selects the generator before anything is validated.
func (g *mvcGen) preflight() error {
	if !g.o.hasContract() {
		return nil
	}
	data, err := os.ReadFile(g.o.ContractFile)
	if err != nil {
		return failf("脚手架合同不可读取: %s", g.o.ContractFile)
	}
	parsed, err := ParseJSON(data)
	contract := asMap(parsed)
	if err != nil || contract == nil {
		return fail("脚手架合同必须是 JSON 对象")
	}
	profile := mustGet(contract, "architecture_profile")
	if profile == "mvc-data-analysis-v1" {
		return fail("retired: mvc-data-analysis-v1 仅允许历史只读审计；新建请使用 layered-mvc-service，再按需使用 setup-yss-harness")
	}
	if profile != "layered-mvc-service" {
		return fail("unsupported MVC generator/Profile pair")
	}
	return nil
}

func (g *mvcGen) validateFormalContract(ctx context.Context) error {
	flow := &formalFlow{src: g.src, o: g.o, family: "layered-mvc", skill: mvcSkill, kind: "backend-layered-mvc", profile: "layered-mvc-service"}
	state, platform, err := flow.validate(ctx)
	if err != nil {
		return err
	}
	g.formal, g.contract, g.platform = state, state.contract, platform
	g.modules = stringList(mustGet(asMap(mustGet(state.contract, "module_profile")), "resolved_modules"))
	return nil
}

func (g *mvcGen) formalManifest(t *Tree, fingerprint, generator, ownership *OMap, mainSource string) (*OMap, error) {
	state, contract, o := g.formal, g.contract, g.o
	name := fieldString(contract, "project_name")
	base := fieldString(contract, "base_package")
	application := upperCamel(name) + "Application"
	downstream := NewOMap()
	for _, skill := range []string{"yss-application", "yss-repository", "yss-mybatis", "yss-web-controller", "yss-dto", "yss-exception", "yss-validation", "mapstruct", "lombok", "alibaba-java-code-style"} {
		digest, err := sourceTreeDigest(g.src, ".agents/skills/"+skill)
		if err != nil {
			return nil, err
		}
		downstream.Set(skill, digest)
	}
	profiles, err := rawSHA256(g.src, ".template-spec/agents/backend-architecture-profiles.md")
	if err != nil {
		return nil, err
	}
	compiler, err := rawSHA256(g.src, compilerContractRef)
	if err != nil {
		return nil, err
	}
	ruleset := rawDigest(t.Data(name + "-server/src/test/java/" + filepath.ToSlash(filepath.Join(splitPackage(base)...)) + "/architecture/LayeredMvcArchitectureTest.java"))
	manifest := NewOMap()
	if binding, ok := contract.Get("platform_configuration"); ok && binding != nil {
		manifest.Set("source_fingerprint", fingerprint)
		manifest.Set("generated_tree_digest", generatedTreeDigest(t))
		manifest.Set("platform_verification", state.platformStatus)
		manifest.Set("platform_configuration", binding)
	}
	manifest.Set("schema_version", mustGet(contract, "schema_version"))
	referenceFields(manifest, contract)
	manifest.Set("kind", "backend-scaffold")
	manifest.Set("architecture_profile", mustGet(contract, "architecture_profile"))
	manifest.Set("architecture_identity", scaffoldArchitectureIdentity(contract, "sha256:"+state.digestHex))
	manifest.Set("contract_id", mustGet(contract, "contract_id"))
	manifest.Set("contract_version", mustGet(contract, "contract_version"))
	manifest.Set("scaffold_request_id", mustGet(contract, "scaffold_request_id"))
	manifest.Set("architecture_family", "layered-mvc")
	manifest.Set("generator_skill", mvcSkill)
	manifest.Set("decision_id", mustGet(contract, "decision_id"))
	manifest.Set("decision_digest", mustGet(contract, "decision_digest"))
	manifest.Set("decision_ref", mustGet(contract, "decision_ref"))
	manifest.Set("contract_digest", "sha256:"+state.digestHex)
	manifest.Set("contract_file_ref", state.contractFile)
	manifest.Set("approval_ref", o.ApprovalRef)
	manifest.Set("approver", mustGet(asMap(mustGet(contract, "approval")), "approver"))
	manifest.Set("lifecycle_approval_ref", mustGet(contract, "lifecycle_approval_ref"))
	manifest.Set("compiler_draft_ref", mustGet(contract, "compiler_draft_ref"))
	manifest.Set("persisted_ref", mustGet(contract, "persisted_ref"))
	manifest.Set("current_version", mustGet(contract, "current_version"))
	manifest.Set("design_prerequisites", state.prerequisites)
	manifest.Set("allowed_write_paths", mustGet(contract, "allowed_write_paths"))
	manifest.Set("expected_evidence_files", mustGet(contract, "expected_evidence_files"))
	manifest.Set("profiles", mustGet(contract, "profiles"))
	manifest.Set("module_profile", mustGet(contract, "module_profile"))
	manifest.Set("maven_coordinates", mustGet(contract, "maven_coordinates"))
	manifest.Set("maven_coordinates_source", "approved-contract")
	manifest.Set("project_name", name)
	manifest.Set("base_package", base)
	manifest.Set("bootstrap_main_class", o.BasePackage+"."+application)
	manifest.Set("bootstrap_main_source", mainSource)
	manifest.Set("generation_mode", "controlled-generation")
	manifest.Set("completion_level", "generated")
	manifest.Set("generation_policy", mustGet(contract, "generation_policy"))
	manifest.Set("verification_commands", commandList())
	manifest.Set("generator", generator)
	manifest.Set("ownership", ownership)
	manifest.Set("readiness", newMap(
		"downstream_skills", downstream,
		"contracts", newMap("architecture_profiles", profiles, "compiler_contract", compiler),
		"architecture_ruleset", ruleset,
	))
	manifest.Set("generated_at", o.renderContext.GeneratedAt)
	return manifest, nil
}

func rawDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func splitPackage(base string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(base); i++ {
		if i == len(base) || base[i] == '.' {
			parts = append(parts, base[start:i])
			start = i + 1
		}
	}
	return parts
}
