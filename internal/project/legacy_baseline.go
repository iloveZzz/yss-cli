package project

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// The migration allowlist comes from the archived installed plugin lock and
// its exact pre-overlay baseline. Project-provided hashes cannot extend it.
//
//go:embed legacy-baseline-policy.json
var legacyPolicyBytes []byte

//go:embed legacy-design-baseline-policy.json
var legacyDesignPolicyBytes []byte

// Exact public project-binding asset from the archived design plugin bundle.
//
//go:embed legacy-design-core.json
var legacyDesignCoreBytes []byte

type legacyPolicyFile struct {
	Previous domain.Descriptor `json:"previous"`
	Overlay  domain.Descriptor `json:"overlay"`
}

// This is a single source-proven generated asset, not a general generated-file
// exception. Legacy bridge projects keep their original legacy-all lock path.
type legacyGeneratedLockPolicy struct {
	Ref              string           `json:"ref"`
	CLIName          string           `json:"cliName"`
	Type             string           `json:"type"`
	Ownership        string           `json:"ownership"`
	GeneratorID      string           `json:"generatorId"`
	GeneratorVersion int              `json:"generatorVersion"`
	Runtimes         []string         `json:"runtimes"`
	InstalledSkills  []string         `json:"installedSkills"`
	File             legacyPolicyFile `json:"file"`
}
type legacyBaselinePolicy struct {
	SchemaVersion     int                         `json:"schemaVersion"`
	ID                string                      `json:"id"`
	Plugin            string                      `json:"plugin"`
	Profile           string                      `json:"profile"`
	CLICommit         string                      `json:"cliCommit"`
	ManifestHash      string                      `json:"manifestHash"`
	ReceiptCoreDigest string                      `json:"receiptCoreDigest"`
	BundleDigest      string                      `json:"bundleDigest"`
	CLIVersion        string                      `json:"cliVersion"`
	TemplateCommit    string                      `json:"templateCommit"`
	SnapshotHash      string                      `json:"snapshotHash"`
	Files             map[string]legacyPolicyFile `json:"files"`
	BridgeFiles       map[string]legacyPolicyFile `json:"bridgeFiles"`
	BridgeOrigins     map[string]string           `json:"bridgeOrigins"`
	GeneratedLock     *legacyGeneratedLockPolicy  `json:"generatedLock,omitempty"`
}

func applyLegacyBaseline(id *Identity, command string, binding *Binding, managed map[string]Managed, inputs map[string]domain.Descriptor, trustedModes map[string]bool) error {
	if binding == nil || binding.LegacyBaselinePolicy == "" {
		return nil
	}
	var policy legacyBaselinePolicy
	policyBytes := legacyPolicyBytes
	if id.Profile.Name == "design" {
		policyBytes = legacyDesignPolicyBytes
	}
	if err := json.Unmarshal(policyBytes, &policy); err != nil {
		return domain.Wrap("LEGACY_POLICY", err)
	}
	if policy.Profile == "" {
		policy.Profile = "spec"
	}
	reject := func() error {
		return domain.Fail("LEGACY_POLICY", "historical overlay does not match the fixed migration policy")
	}
	if command != "migrate" || id.Native != nil || id.Profile.Name != policy.Profile || id.Legacy == nil || policy.SchemaVersion != 1 || binding.LegacyBaselinePolicy != policy.ID || id.Legacy["cliVersion"] != policy.CLIVersion || id.Legacy["templateCommit"] != policy.TemplateCommit || id.Legacy["snapshotHash"] != policy.SnapshotHash {
		return reject()
	}
	if _, err := binding.validate(id.Profile.Name, command); err != nil {
		return err
	}
	const receipt = ".yss-plugin.json"
	before, err := safefs.Describe(id.Root, receipt)
	if err != nil {
		return err
	}
	if before.Type != "file" || before.Digest != binding.Guards[receipt] {
		return reject()
	}
	raw, err := load(id.Root, receipt)
	if err != nil {
		return err
	}
	record, ok := object(raw)
	if !ok || number(record["schema_version"]) != 1 || record["plugin"] != policy.Plugin || record["plugin_bundle_sha256"] != policy.BundleDigest || record["execution_owner"] != "project-local-yss-product-lifecycle" || record["business_execution_ready"] != false {
		return reject()
	}
	if policy.Profile == "spec" && record["execution_scope"] != "plan-to-backend" {
		return reject()
	}
	bridged := false
	if migrationValue, hasMigration := record["migration"]; policy.Profile == "spec" && hasMigration {
		migration, valid := object(migrationValue)
		origin, known := policy.BridgeOrigins[text(migration["from_plugin"])]
		if !valid || !known || origin != migration["from_bundle_sha256"] || !digestPattern.MatchString(text(migration["previous_binding_sha256"])) || migration["requires_current_contract_validation"] != true {
			return reject()
		}
		bridged = true
		for ref, file := range policy.BridgeFiles {
			policy.Files[ref] = file
		}
	}
	if policy.Profile == "spec" && policy.GeneratedLock != nil {
		distribution, _ := object(id.Legacy["distribution"])
		// Fixed M4/0.2 public bridge sync preserves legacy-all. It receives no new
		// generated-lock allowance; its existing overlay/origin checks still apply.
		if !bridged || text(distribution["mode"]) != "legacy-all" {
			if !validLegacyGeneratedLock(id, record, policy) {
				return reject()
			}
			policy.Files[policy.GeneratedLock.Ref] = policy.GeneratedLock.File
		}
	}
	if policy.Profile == "design" {
		cli, valid := object(record["cli"])
		_, hasScope := record["execution_scope"]
		if !valid || hasScope || record["profile_id"] != id.Profile.ID || cli["cli_commit"] != policy.CLICommit || cli["version"] != policy.CLIVersion || cli["template_commit"] != policy.TemplateCommit || cli["snapshot_hash"] != policy.SnapshotHash || cli["manifest_hash"] != policy.ManifestHash || record["core_digest"] != policy.ReceiptCoreDigest || id.Legacy["manifestHash"] != policy.ManifestHash {
			return reject()
		}
		var core []struct {
			Ref    string `json:"ref"`
			Digest string `json:"sha256"`
			Mode   uint32 `json:"mode"`
		}
		if err := json.Unmarshal(legacyDesignCoreBytes, &core); err != nil {
			return domain.Wrap("LEGACY_POLICY", err)
		}
		for _, file := range core {
			observed, err := safefs.Describe(id.Root, file.Ref)
			if err != nil {
				return err
			}
			if observed != (domain.Descriptor{Type: "file", Digest: file.Digest, Mode: domain.FileMode(file.Mode)}) {
				return domain.Fail("CONFLICT", "historical plugin core bytes or permissions changed: "+file.Ref)
			}
			inputs[file.Ref] = observed
		}
	}
	data, err := base64.StdEncoding.DecodeString(binding.Data)
	if err != nil {
		return err
	}
	value, err := schema.Parse(data)
	if err != nil {
		return err
	}
	payload, ok := object(value)
	if !ok {
		return reject()
	}
	lineage, ok := object(payload["legacy_binding"])
	if !ok || lineage["path"] != receipt || lineage["sha256"] != before.Digest {
		return reject()
	}
	for ref, expected := range policy.Files {
		if expected.Previous.Type == "file" {
			expected.Previous.Mode = domain.FileMode(expected.Previous.Mode)
		}
		expected.Overlay.Mode = domain.FileMode(expected.Overlay.Mode)
		previous, ok := managed[ref]
		matches := previous.Applied == expected.Previous || previous.Applied == expected.Overlay
		// Old Spec contentHash metadata has no file mode. Its adapter's 0644
		// default is not an archived permission claim; current overlay mode is
		// still compared exactly below against the fixed trusted asset.
		if policy.Profile == "spec" && previous.Applied.Type == "file" && previous.Applied.Mode == 0644 && (previous.Applied.Digest == expected.Previous.Digest || previous.Applied.Digest == expected.Overlay.Digest) {
			matches = true
		}
		if (!ok && expected.Previous.Type != "missing") || (ok && !matches) {
			return reject()
		}
		observed, err := safefs.Describe(id.Root, ref)
		if err != nil {
			return err
		}
		if observed != expected.Overlay {
			return domain.Fail("CONFLICT", "historical overlay bytes or permissions changed: "+ref)
		}
		previous.Applied = expected.Overlay
		if !ok {
			previous.Baseline = expected.Overlay
			previous.Ownership = "managed"
		}
		managed[ref] = previous
		inputs[ref] = observed
		if trustedModes != nil {
			trustedModes[ref] = true
		}
	}
	inputs[receipt] = before
	return nil
}

func validLegacyGeneratedLock(id *Identity, record map[string]any, policy legacyBaselinePolicy) bool {
	lock := policy.GeneratedLock
	if lock == nil || lock.Ref != "skills-lock.json" {
		return false
	}
	files, _ := object(id.Legacy["managedFiles"])
	registration, ok := object(files[lock.Ref])
	if !ok || registration["type"] != lock.Type || registration["ownership"] != lock.Ownership || registration["generatorId"] != lock.GeneratorID || number(registration["generatorVersion"]) != lock.GeneratorVersion {
		return false
	}
	hash := text(registration["contentHash"])
	if hash != lock.File.Previous.Digest && hash != lock.File.Overlay.Digest {
		return false
	}
	distribution, ok := object(id.Legacy["distribution"])
	if !ok || distribution["mode"] != "selected" || !legacyExactStrings(distribution["runtimes"], lock.Runtimes) || !legacyExactStrings(distribution["installedSkills"], lock.InstalledSkills) {
		return false
	}
	cli, ok := object(record["cli"])
	return ok && cli["name"] == lock.CLIName && cli["version"] == policy.CLIVersion && cli["cli_commit"] == policy.CLICommit && cli["template_commit"] == policy.TemplateCommit && cli["snapshot_hash"] == policy.SnapshotHash && cli["manifest_hash"] == policy.ManifestHash && record["core_digest"] == policy.ReceiptCoreDigest && id.Legacy["managedFilesManifestVersion"] == policy.ManifestHash
}

// Exact set equality rejects non-string values, duplicates and extra names;
// historical ordering is not a source identity or additional runtime grant.
func legacyExactStrings(value any, expected []string) bool {
	var actual []string
	switch values := value.(type) {
	case []string:
		actual = values
	case []any:
		for _, v := range values {
			s, ok := v.(string)
			if !ok {
				return false
			}
			actual = append(actual, s)
		}
	default:
		return false
	}
	if len(actual) != len(expected) {
		return false
	}
	wanted := map[string]bool{}
	for _, s := range expected {
		wanted[s] = true
	}
	for _, s := range actual {
		if !wanted[s] {
			return false
		}
		delete(wanted, s)
	}
	return len(wanted) == 0
}
