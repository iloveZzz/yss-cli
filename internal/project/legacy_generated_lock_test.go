package project

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// Portable bytes from fixed backend-delivery 0.3 project-overlay archive
// sha256 5d2f10f80501ea9e022fbfc83718cf321e9642ad0f834186763e0900ff6e91cd.
// This is source-derived test data, never a project-provided policy input.
const legacyGeneratedOverlayAssets = "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"
const legacyGeneratedPrevious = "623656c6acca0bc2584edbc201519ed24d99bc20b71cbc47d379c87e25023f55"
const legacyGeneratedOverlay = "2da827b0608825a0e48df764e8279ff020d505518039cfe00b6e13e6f10e173b"

var legacyGeneratedNames = []string{"alibaba-java-code-style", "code-review", "codebase-design", "competitive-intelligence", "domain-modeling", "grilling", "i-have-adhd", "implementation-repo-onboarding", "lombok", "mapstruct", "prototype-review", "tdd", "yss-application", "yss-audit-log", "yss-backend-spec-review", "yss-cache", "yss-ddd-scaffold-generator", "yss-design-system", "yss-distributed-id", "yss-domain", "yss-dto", "yss-excel-mvc", "yss-exception", "yss-implementation-contract-compiler", "yss-layered-mvc-scaffold-generator", "yss-mybatis", "yss-openapi-draft-review", "yss-openapi-governance", "yss-product-lifecycle", "yss-prototype-stage", "yss-repository", "yss-research", "yss-resilience4j", "yss-security-algorithm", "yss-skill-source-index-refresh", "yss-stage-decision", "yss-tactical-design", "yss-technical-design", "yss-up-springboot3", "yss-userinfo", "yss-validation", "yss-web-controller"}

func legacyGeneratedWrite(t *testing.T, root, ref string, raw []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, ref)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, mode); err != nil {
		t.Fatal(err)
	}
}
func legacyGeneratedJSON(t *testing.T, root, ref string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	legacyGeneratedWrite(t, root, ref, raw, 0644)
}
func legacyGeneratedFixture(t *testing.T) (string, *Binding) {
	t.Helper()
	root := freshRoot(t)
	compressed, err := base64.StdEncoding.DecodeString(legacyGeneratedOverlayAssets)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Ref, Content, SHA256 string
		Mode                 uint32
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	previous := map[string]string{".agents/skills/yss-design-system/SKILL.md": "daafb4075da9fd6487dc8e55d18f062279166099ba5b9ec25b0ae8b46d1d840a", ".agents/skills/yss-design-system/references/data-quality-theme.md": "485fc0bb3debc18f8accf7355da07ec6161ddc6b784116e7052142adb4cbc576", ".codex/skills/yss-design-system/SKILL.md": "daafb4075da9fd6487dc8e55d18f062279166099ba5b9ec25b0ae8b46d1d840a", ".codex/skills/yss-design-system/references/data-quality-theme.md": "485fc0bb3debc18f8accf7355da07ec6161ddc6b784116e7052142adb4cbc576", "scripts/lib/skill-supply-chain.mjs": "6f94418c3c399cb94e82a4d65ad65f78f8604d2a2e4b2ecf979ba4b346c62912"}
	managed := map[string]any{}
	for _, row := range rows {
		data, err := base64.StdEncoding.DecodeString(row.Content)
		if err != nil || safefs.Digest(data) != row.SHA256 {
			t.Fatal("corrupt fixed overlay fixture", err)
		}
		legacyGeneratedWrite(t, root, row.Ref, data, os.FileMode(row.Mode))
		managed[row.Ref] = map[string]any{"type": "copy", "ownership": "managed", "contentHash": previous[row.Ref]}
	}
	lock, err := os.ReadFile("testdata/legacy-backend-overlay-skills-lock.json")
	if err != nil || safefs.Digest(lock) != legacyGeneratedOverlay {
		t.Fatal("corrupt fixed regenerated lock", err)
	}
	previousLock, err := os.ReadFile("testdata/legacy-backend-selected-skills-lock.json")
	if err != nil || safefs.Digest(previousLock) != legacyGeneratedPrevious {
		t.Fatal("corrupt fixed selected lock", err)
	}
	legacyGeneratedWrite(t, root, "skills-lock.json", lock, 0644)
	managed["skills-lock.json"] = map[string]any{"type": "render", "ownership": "generated", "generatorId": "skills-lock", "generatorVersion": 1, "contentHash": legacyGeneratedPrevious}
	cli := map[string]any{"name": "create-yss-spec", "version": "3.4.12", "cli_commit": "6b1c388932615ece9fdb496ddf776ed904767061", "template_commit": "327072236d45a7705f9388e977427aac46f128fb", "snapshot_hash": "57f3b76de6f64eac30d1d7b24a62a33cae81641e0ce2d33c31813e3d95b99382", "manifest_hash": "2780e1855c9b0cb46fe056e226744817510ed50f86e9a71f8a269307789d5885"}
	legacyGeneratedJSON(t, root, ".yss-template.json", map[string]any{"metadataSchemaVersion": 3, "templateSource": domain.Profiles["spec"].TemplateSource, "cliVersion": cli["version"], "templateCommit": cli["template_commit"], "snapshotHash": cli["snapshot_hash"], "managedFilesManifestVersion": cli["manifest_hash"], "managedFiles": managed, "distribution": map[string]any{"mode": "selected", "runtimes": []string{"codex"}, "installedSkills": legacyGeneratedNames}})
	legacyGeneratedWrite(t, root, "yss-project.yaml", []byte("schema_version: 1\nrepository_mode: project-instance\n"), 0644)
	legacyGeneratedJSON(t, root, ".yss-plugin.json", map[string]any{"schema_version": 1, "plugin": "yss-backend-delivery", "plugin_bundle_sha256": "c48d67ec2908cfb91ca6d0f195d77bb8a67fadac4e738c3c1c5831732fe49d4e", "execution_owner": "project-local-yss-product-lifecycle", "business_execution_ready": false, "execution_scope": "plan-to-backend", "cli": cli, "core_digest": "0f62c8ef949213b4da6c89b9a23f1960532772201c7bd7d3053f8d5bf245c99d"})
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	contextBytes, err := b.Initial["CONTEXT.md"].Render(map[string]string{"projectName": "历史锁保护项目", "businessDomain": "待补充", "teamSize": "待补充"})
	if err != nil {
		t.Fatal(err)
	}
	legacyGeneratedWrite(t, root, "CONTEXT.md", contextBytes, 0644)
	binary, err := domain.ExecutableDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := safefs.Describe(root, ".yss-plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"schema_version": 2, "plugin": "yss-backend-delivery", "profile": "spec", "execution_scope": "plan-to-backend", "template_commit": b.TemplateCommit, "bundle_hash": b.BundleHash, "binary_sha256": binary, "legacy_binding": map[string]any{"path": ".yss-plugin.json", "sha256": receipt.Digest}}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	binding := &Binding{SchemaVersion: 1, Path: ".yss-backend-plugin.json", Data: base64.StdEncoding.EncodeToString(data), Guards: map[string]string{".yss-plugin.json": receipt.Digest}, LegacyBaselinePolicy: "backend-delivery-0.3-c48d67ec"}
	lockProtected(t, root)
	return root, binding
}
func TestLegacyGeneratedLockFixedMigrationAndWholeRollback(t *testing.T) {
	root, binding := legacyGeneratedFixture(t)
	before := lockTree(t, root, false)
	p, err := BuildWithBinding(root, "spec", "migrate", nil, nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Conflicts) != 0 || !lockChanged(p) {
		t.Fatalf("fixed source-derived generated lock must migrate: conflicts=%v preserved=%v", p.Conflicts, p.Preserved)
	}
	if _, err := Apply(p); err != nil {
		t.Fatal(err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	d, err := safefs.Describe(root, "skills-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stringsOf(id.Native.Distribution["runtimes"]), []string{"codex"}) {
		t.Fatal("historical projectionRoots broadened physical runtime")
	}
	for _, ref := range []string{".cursor", ".pi"} {
		if _, err := os.Lstat(filepath.Join(root, ref)); !os.IsNotExist(err) {
			t.Fatal("unselected runtime was installed", ref, err)
		}
	}
	if id.Native.Managed["skills-lock.json"].Applied != d || d.Digest == legacyGeneratedOverlay {
		t.Fatal("lock and metadata did not advance together")
	}
	after := lockTree(t, root, false)
	for _, ref := range []string{"src/business.sh", ".git/index", ".github/workflows/user.yml", "CONTEXT.md"} {
		if after[ref] != before[ref] {
			t.Fatal("business bytes/mode changed", ref)
		}
	}
	if _, err := transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, lockTree(t, root, false)) {
		t.Fatal("rollback did not restore every old byte/mode, metadata and binding together")
	}
}

func legacyGeneratedEdit(t *testing.T, root, ref string, change func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ref))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	change(v)
	legacyGeneratedJSON(t, root, ref, v)
}
func legacyGeneratedRebind(t *testing.T, root string, b *Binding) {
	t.Helper()
	receipt, err := safefs.Describe(root, ".yss-plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(b.Data)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	payload["legacy_binding"] = map[string]any{"path": ".yss-plugin.json", "sha256": receipt.Digest}
	data, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	b.Data = base64.StdEncoding.EncodeToString(data)
	b.Guards[".yss-plugin.json"] = receipt.Digest
}
func TestLegacyGeneratedLockRejectsUntrustedRegistrationHeaderAndSelection(t *testing.T) {
	cases := []string{"missing-registration", "ownership", "type", "generator-id", "generator-version", "unrelated-baseline", "missing-ownership", "receipt-cli-name", "receipt-cli-version", "receipt-cli-commit", "receipt-cli-template", "receipt-cli-snapshot", "receipt-cli-manifest", "receipt-core", "metadata-manifest", "metadata-version", "metadata-template", "metadata-snapshot", "mode-legacy-all", "runtime-extra", "runtime-hidden-type", "skill-missing", "skill-extra", "skill-duplicate", "skill-hidden-type", "lineage", "bridge-origin"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			root, b := legacyGeneratedFixture(t)
			if strings.HasPrefix(name, "receipt-cli-") {
				fields := map[string]string{"receipt-cli-name": "name", "receipt-cli-version": "version", "receipt-cli-commit": "cli_commit", "receipt-cli-template": "template_commit", "receipt-cli-snapshot": "snapshot_hash", "receipt-cli-manifest": "manifest_hash"}
				legacyGeneratedEdit(t, root, ".yss-plugin.json", func(v map[string]any) { v["cli"].(map[string]any)[fields[name]] = "untrusted" })
				legacyGeneratedRebind(t, root, b)
			} else if name == "receipt-core" || name == "bridge-origin" {
				legacyGeneratedEdit(t, root, ".yss-plugin.json", func(v map[string]any) {
					if name == "receipt-core" {
						v["core_digest"] = strings.Repeat("a", 64)
					} else {
						v["migration"] = map[string]any{"from_plugin": "untrusted", "from_bundle_sha256": strings.Repeat("a", 64), "previous_binding_sha256": strings.Repeat("b", 64), "requires_current_contract_validation": true}
					}
				})
				legacyGeneratedRebind(t, root, b)
			} else if name == "lineage" {
				data, _ := base64.StdEncoding.DecodeString(b.Data)
				var v map[string]any
				_ = json.Unmarshal(data, &v)
				v["legacy_binding"].(map[string]any)["sha256"] = strings.Repeat("a", 64)
				data, _ = json.Marshal(v)
				b.Data = base64.StdEncoding.EncodeToString(data)
			} else {
				legacyGeneratedEdit(t, root, ".yss-template.json", func(v map[string]any) {
					files := v["managedFiles"].(map[string]any)
					registration := files["skills-lock.json"].(map[string]any)
					d := v["distribution"].(map[string]any)
					switch name {
					case "missing-registration":
						delete(files, "skills-lock.json")
					case "ownership":
						registration["ownership"] = "user-owned"
					case "missing-ownership":
						delete(registration, "ownership")
					case "type":
						registration["type"] = "copy"
					case "generator-id":
						registration["generatorId"] = "other"
					case "generator-version":
						registration["generatorVersion"] = 2
					case "unrelated-baseline":
						registration["contentHash"] = strings.Repeat("a", 64)
					case "metadata-manifest":
						v["managedFilesManifestVersion"] = strings.Repeat("a", 64)
					case "metadata-version":
						v["cliVersion"] = "3.4.13"
					case "metadata-template":
						v["templateCommit"] = strings.Repeat("a", 40)
					case "metadata-snapshot":
						v["snapshotHash"] = strings.Repeat("a", 64)
					case "mode-legacy-all":
						d["mode"] = "legacy-all"
					case "runtime-extra":
						d["runtimes"] = []string{"codex", "cursor"}
					case "runtime-hidden-type":
						d["runtimes"] = []any{"codex", 1}
					case "skill-missing":
						d["installedSkills"] = legacyGeneratedNames[1:]
					case "skill-extra":
						d["installedSkills"] = append(append([]string{}, legacyGeneratedNames...), "archify")
					case "skill-duplicate":
						names := append([]string{}, legacyGeneratedNames...)
						names[0] = names[1]
						d["installedSkills"] = names
					case "skill-hidden-type":
						names := []any{}
						for _, s := range legacyGeneratedNames {
							names = append(names, s)
						}
						names[0] = 1
						d["installedSkills"] = names
					}
				})
			}
			before := lockTree(t, root, true)
			_, err := BuildWithBinding(root, "spec", "migrate", nil, nil, b)
			if lockCode(err) != "LEGACY_POLICY" {
				t.Fatalf("untrusted historical claim %s: got %v want LEGACY_POLICY", name, err)
			}
			if !reflect.DeepEqual(before, lockTree(t, root, true)) {
				t.Fatal("rejection wrote project bytes/mode")
			}
		})
	}
}
func TestLegacyGeneratedLockRejectsModifiedBytesModeAndFutureLock(t *testing.T) {
	for _, name := range []string{"bytes", "mode", "future-lock"} {
		t.Run(name, func(t *testing.T) {
			root, b := legacyGeneratedFixture(t)
			file := filepath.Join(root, "skills-lock.json")
			switch name {
			case "bytes":
				raw, _ := os.ReadFile(file)
				legacyGeneratedWrite(t, root, "skills-lock.json", append(raw, []byte("\n ")...), 0644)
			case "mode":
				if runtime.GOOS == "windows" {
					t.Skip("Windows does not retain Unix file permissions")
				}
				if err := os.Chmod(file, 0600); err != nil {
					t.Fatal(err)
				}
			case "future-lock":
				p, err := BuildWithBinding(root, "spec", "migrate", nil, nil, b)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, c := range p.Changes {
					if c.Path == "skills-lock.json" {
						raw, err := base64.StdEncoding.DecodeString(c.Data)
						if err != nil {
							t.Fatal(err)
						}
						legacyGeneratedWrite(t, root, c.Path, raw, 0644)
						found = true
					}
				}
				if !found {
					t.Fatal("missing future lock fixture")
				}
			}
			before := lockTree(t, root, true)
			_, err := BuildWithBinding(root, "spec", "migrate", nil, nil, b)
			if lockCode(err) != "CONFLICT" {
				t.Fatalf("modified historical lock accepted: %v", err)
			}
			if !reflect.DeepEqual(before, lockTree(t, root, true)) {
				t.Fatal("rejection wrote files")
			}
		})
	}
}
func TestLegacyGeneratedLockOverlayRegistrationStillRequiresFixedShape(t *testing.T) {
	root, b := legacyGeneratedFixture(t)
	legacyGeneratedEdit(t, root, ".yss-template.json", func(v map[string]any) {
		v["managedFiles"].(map[string]any)["skills-lock.json"].(map[string]any)["contentHash"] = legacyGeneratedOverlay
	})
	before := lockTree(t, root, true)
	p, err := BuildWithBinding(root, "spec", "migrate", nil, nil, b)
	if err != nil || len(p.Conflicts) != 0 || !lockChanged(p) {
		t.Fatalf("exact registered source overlay refused: %+v %v", p, err)
	}
	if !reflect.DeepEqual(before, lockTree(t, root, true)) {
		t.Fatal("planning wrote files")
	}
}
func TestLegacyGeneratedLockSavedPlanDriftAndRollbackPreserveUserChanges(t *testing.T) {
	for _, ref := range []string{"skills-lock.json", ".yss-template.json", ".yss-plugin.json"} {
		t.Run("saved-plan/"+ref, func(t *testing.T) {
			root, b := legacyGeneratedFixture(t)
			p, err := BuildWithBinding(root, "spec", "migrate", nil, nil, b)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "plan.json")
			if err := os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			p, err = ReadPlan(file)
			if err != nil {
				t.Fatal(err)
			}
			raw, err = os.ReadFile(filepath.Join(root, ref))
			if err != nil {
				t.Fatal(err)
			}
			legacyGeneratedWrite(t, root, ref, append(raw, []byte("\n ")...), 0644)
			before := lockTree(t, root, true)
			if _, err := Apply(p); lockCode(err) != "INPUT_DRIFT" {
				t.Fatalf("wrong saved plan refusal: %v", err)
			}
			if !reflect.DeepEqual(before, lockTree(t, root, true)) {
				t.Fatal("drift refusal partially migrated identity/binding/targets")
			}
		})
	}
	t.Run("rollback-user-lock", func(t *testing.T) {
		root, b := legacyGeneratedFixture(t)
		p, err := BuildWithBinding(root, "spec", "migrate", nil, nil, b)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(p); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(root, "skills-lock.json"))
		if err != nil {
			t.Fatal(err)
		}
		legacyGeneratedWrite(t, root, "skills-lock.json", append(raw, []byte("\n ")...), 0644)
		before := lockTree(t, root, true)
		if _, err := transaction.Rollback(root); lockCode(err) != "CONCURRENT" {
			t.Fatalf("wrong rollback refusal: %v", err)
		}
		if !reflect.DeepEqual(before, lockTree(t, root, true)) {
			t.Fatal("rollback overwrote later bytes or partially reverted identity/binding")
		}
	})
}
