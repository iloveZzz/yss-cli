package governance

import (
	"context"
	"path/filepath"
	"testing"
)

// Exercise the manifest-bound source view directly, with unmodified schemas
// and authorities from the actual embedded Profiles. The package adapters
// remain responsible for validating the enclosing manifest and its hashes.
func sourceSchemaTestView(t *testing.T, receiver string, captured map[string]any) (*semanticSession, *semanticSession) {
	t.Helper()
	root := apTestProfileRoot(t, receiver)
	source := specBaselineTestNativeSeed(t, "spec")
	bindings := map[string]string{}
	for _, ref := range []string{".yss.json", "yss-project.yaml", "CONTEXT.md", ".template-spec/process/harness-profile.yaml", approvalRegistryRef, approvalRolesRef} {
		apTestPut(t, root, "sealed/"+ref, mustReadSpecBaselineTestFile(t, filepath.Join(source, ref)))
		bindings[ref] = ref
	}
	for ref, value := range captured {
		apTestPut(t, root, "sealed/"+ref, value)
		bindings[ref] = ref
	}
	parent := newSemanticSession(context.Background(), root, nil)
	child, err := parent.sourceSnapshotSession("sealed", bindings)
	if err != nil {
		t.Fatal(err)
	}
	return parent, child
}

func TestImmutableSourceSchemasUseSourceProfile(t *testing.T) {
	for _, receiver := range []string{"design", "backend", "frontend"} {
		t.Run(receiver, func(t *testing.T) {
			parent, source := sourceSchemaTestView(t, receiver, nil)
			before := progressionInventory(t, parent.root)
			if err := source.authorities(); err != nil {
				t.Fatalf("valid immutable Spec authorities used %s schema: %v", receiver, err)
			}
			if source.report.Profile != "spec" || !contractSame(before, progressionInventory(t, parent.root)) {
				t.Fatal("source Profile changed or readonly validation wrote assets")
			}
			if err := parent.finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImmutableSourceSchemasNeverTrustPermissiveCapturedSchema(t *testing.T) {
	const ref = ".template-spec/process/schemas/lifecycle-registry.schema.json"
	for _, captured := range []string{`{}`, `{"type":"object","additionalProperties":true}`} {
		t.Run(captured, func(t *testing.T) {
			parent, source := sourceSchemaTestView(t, "backend", map[string]any{ref: []byte(captured)})
			registry := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(source.root, approvalRegistryRef))))
			semMap(semList(registry["gates"])[0])["unsupported_source_rule"] = true
			apTestPut(t, source.root, approvalRegistryRef, registry)
			before := progressionInventory(t, parent.root)
			apTestCode(t, source.authorities(), "SCHEMA")
			if !contractSame(before, progressionInventory(t, parent.root)) {
				t.Fatal("rejected captured authority mutated the package")
			}
		})
	}
	t.Run("source-checkpoint", func(t *testing.T) {
		const checkpointSchema = ".template-spec/process/schemas/lifecycle-checkpoint.schema.json"
		parent, source := sourceSchemaTestView(t, "backend", map[string]any{ref: []byte(`{}`), checkpointSchema: []byte(`{}`)})
		if err := source.authorities(); err != nil {
			t.Fatal(err)
		}
		before := progressionInventory(t, parent.root)
		apTestCode(t, source.validateSchema(checkpointSchema, map[string]any{"schema_version": 1, "unsupported_checkpoint_field": true}), "SCHEMA")
		if !contractSame(before, progressionInventory(t, parent.root)) {
			t.Fatal("rejected captured checkpoint schema mutated the package")
		}
	})
}

func TestImmutableSourceSchemaClosureIdentityAndDrift(t *testing.T) {
	const ref = ".template-spec/process/schemas/lifecycle-registry.schema.json"
	for _, variant := range []string{"missing-ref", "escape-ref", "online-ref", "unknown-profile", "metadata-profile-conflict", "unknown-registry-version", "captured-schema-drift", "captured-dependency-drift"} {
		t.Run(variant, func(t *testing.T) {
			captured := map[string]any{ref: []byte(`{}`)}
			switch variant {
			case "missing-ref":
				captured[ref] = []byte(`{"$ref":"missing.json"}`)
			case "escape-ref":
				captured[ref] = []byte(`{"$ref":"../../../../outside.json"}`)
			case "online-ref":
				captured[ref] = []byte(`{"$ref":"https://never-contact.invalid/schema.json"}`)
			case "captured-dependency-drift":
				captured[ref] = []byte(`{"$ref":"observed-source-dependency.json"}`)
				captured[".template-spec/process/schemas/observed-source-dependency.json"] = []byte(`{}`)
			}
			parent, source := sourceSchemaTestView(t, "backend", captured)
			if variant == "unknown-profile" || variant == "metadata-profile-conflict" {
				profileRef := ".template-spec/process/harness-profile.yaml"
				profile := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(source.root, profileRef))))
				profile["profile_id"] = "harness.unknown"
				if variant == "metadata-profile-conflict" {
					profile["profile_id"] = "harness.backend-delivery"
				}
				apTestPut(t, source.root, profileRef, profile)
			} else if variant == "unknown-registry-version" {
				registry := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(source.root, approvalRegistryRef))))
				registry["schema_version"] = 99
				apTestPut(t, source.root, approvalRegistryRef, registry)
			}
			before := progressionInventory(t, parent.root)
			err := source.authorities()
			if variant == "captured-schema-drift" || variant == "captured-dependency-drift" {
				if err != nil {
					t.Fatal(err)
				}
				changedRef := ref
				if variant == "captured-dependency-drift" {
					changedRef = ".template-spec/process/schemas/observed-source-dependency.json"
				}
				apTestPut(t, source.root, changedRef, []byte("{}\n"))
				apTestCode(t, parent.finish(), "INPUT_DRIFT")
				return
			}
			if err == nil {
				t.Fatalf("invalid source schema/identity accepted: %s", variant)
			}
			if !contractSame(before, progressionInventory(t, parent.root)) {
				t.Fatal("invalid source schema/identity validation wrote assets")
			}
		})
	}
}

func TestImmutableNestedSourceWrapperSchemaRemainsReceiverOwned(t *testing.T) {
	const ref = ".template-spec/process/schemas/strategic-handoff-delivery.schema.json"
	seed := specBaselineTestNativeSeed(t, "spec")
	captured := map[string]any{ref: []byte(`{}`), "nested/" + ref: []byte(`{}`)}
	bindings := map[string]string{}
	for _, asset := range []string{".yss.json", "yss-project.yaml", "CONTEXT.md", ".template-spec/process/harness-profile.yaml", approvalRegistryRef, approvalRolesRef, ref} {
		if asset != ref {
			captured["nested/"+asset] = mustReadSpecBaselineTestFile(t, filepath.Join(seed, asset))
		}
		bindings[asset] = asset
	}
	parent, source := sourceSchemaTestView(t, "backend", captured)
	if err := source.authorities(); err != nil {
		t.Fatal(err)
	}
	nested, err := source.sourceSnapshotSession("nested", bindings)
	if err != nil {
		t.Fatal(err)
	}
	before := progressionInventory(t, parent.root)
	if err = nested.authorities(); err != nil {
		t.Fatal(err)
	}
	if nested.report.Profile != "spec" {
		t.Fatal("nested source lost its own Profile")
	}
	// A permissive captured wrapper Schema cannot replace the receiver's
	// wrapper rules, even when nested source Registry/CP use Spec schemas.
	apTestCode(t, nested.validateSchema(ref, map[string]any{}), "SCHEMA")
	if parent.v.observed[ref].Type != "file" {
		t.Fatal("nested wrapper was not validated with the receiver's observed Schema")
	}
	if err = parent.finish(); err != nil {
		t.Fatal(err)
	}
	if !contractSame(before, progressionInventory(t, parent.root)) {
		t.Fatal("nested source/wrapper validation changed sealed bytes or modes")
	}
}
