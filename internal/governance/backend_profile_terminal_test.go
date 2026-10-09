package governance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func backendProfileTestNativeSeed(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("YSS_NATIVE_BINARY")
	if binary == "" {
		t.Skip("actual fixed native CLI binary not configured")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "backend")
	cmd := exec.Command(binary, "init", "--profile", "backend", "--root", root, "--full", "--project-name", "synthetic-native-backend", "--business-domain", "test-only", "--team-size", "2", "--json")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual native Backend init: %v %s", err, raw)
	}
	for _, ref := range []string{".yss.json", "scripts/lib/implementation-contract-compiler.mjs", "scripts/lib/slice-contract-preparation.mjs", "scripts/lib/slice-contract.mjs"} {
		if _, err := os.Stat(filepath.Join(root, ref)); err != nil {
			t.Fatalf("actual native Backend Bundle missing %s: %v", ref, err)
		}
	}
	return root
}

func backendProfileTestDirectoryModes(t *testing.T, root string) map[string]string {
	t.Helper()
	modes := map[string]string{}
	if err := filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err == nil {
			modes[file] = info.Mode().String()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return modes
}

// The complete native Backend seed is produced from the real embedded Bundle.
// The fixture signs against its existing current policy, without injecting it.
func TestBackendProfileTerminalNativeBundleFixedSource(t *testing.T) {
	oracle := governanceOracleRoot(t)
	seed := backendProfileTestNativeSeed(t)
	seedBefore := progressionInventory(t, seed)
	seedDirs := backendProfileTestDirectoryModes(t, seed)
	program := `import path from 'node:path';import {pathToFileURL} from 'node:url';
const source=process.argv[1],seed=process.argv[2];
const {backendProfileTerminalFixture}=await import(pathToFileURL(path.join(source,'scripts/fixtures/backend-delivery/backend-profile-terminal-fixture.mjs')).href);
const f=await backendProfileTerminalFixture({nativeSeed:seed});
const repeated=await f.terminalModule.verifyBackendDeliveryTerminal(f.root,{checkpointRef:f.checkpointRef});
console.log(JSON.stringify({root:f.root,checkpoint:f.checkpointRef,terminal:f.terminalRef,result:f.result,repeated}));`
	cmd := exec.Command("node", "--input-type=module", "-e", program, oracle, seed)
	cmd.Dir = oracle
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native Backend profile producer: %v %s", err, raw)
	}
	var f struct {
		Root       string         `json:"root"`
		Checkpoint string         `json:"checkpoint"`
		Terminal   string         `json:"terminal"`
		Result     map[string]any `json:"result"`
		Repeated   map[string]any `json:"repeated"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(f.Root) })
	if !contractSame(seedBefore, progressionInventory(t, seed)) || !contractSame(seedDirs, backendProfileTestDirectoryModes(t, seed)) {
		t.Fatal("synthetic fixture changed the original native seed or transaction journal")
	}
	if _, err := os.Lstat(filepath.Join(f.Root, ".yss/transactions")); !os.IsNotExist(err) {
		t.Fatalf("synthetic fixture transplanted absolute-root native transaction state: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.Root, "strategy-source")); !os.IsNotExist(err) {
		t.Fatalf("synthetic fixture embedded an independent source Context in its receiver: %v", err)
	}
	for _, ref := range []string{".yss.json", ".template-spec/process/harness-profile.yaml", ".template-spec/process/lifecycle-registry.yaml", ".template-spec/agents/digital-human-roles.yaml", guidanceContractRef("backend"), "skills-lock.json"} {
		seedInfo, err := os.Lstat(filepath.Join(seed, ref))
		if err != nil {
			t.Fatal(err)
		}
		copiedInfo, err := os.Lstat(filepath.Join(f.Root, ref))
		if err != nil || copiedInfo.Mode() != seedInfo.Mode() {
			t.Fatalf("synthetic fixture changed native identity or policy type/mode: %s %v", ref, err)
		}
		if string(mustReadSpecBaselineTestFile(t, filepath.Join(seed, ref))) != string(mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, ref))) {
			t.Fatalf("synthetic fixture changed native identity or policy bytes: %s", ref)
		}
	}
	check := func() (*semanticSession, error) {
		s := newSemanticSession(context.Background(), f.Root, map[string]string{"checkpoint": f.Checkpoint})
		if err := s.authorities(); err != nil {
			return s, err
		}
		if err := s.verify("backend-terminal", f.Terminal, map[string]string{"checkpoint": f.Checkpoint}); err != nil {
			return s, err
		}
		return s, s.finish()
	}
	s, err := check()
	if err != nil {
		t.Fatalf("native Backend v3 professional/fresh terminal: %v", err)
	}
	if !contractSame(f.Result, f.Repeated) || !contractSame(s.report.Coverage, f.Result) {
		t.Fatalf("fixed terminal result differs: native=%#v producer=%#v repeated=%#v", s.report.Coverage, f.Result, f.Repeated)
	}
	for _, variant := range []string{"missing-professional-review", "missing-fresh-record", "wrong-current-slice", "wrong-current-profile"} {
		t.Run(variant, func(t *testing.T) {
			before := mustReadSpecBaselineTestFile(t, filepath.Join(f.Root, f.Checkpoint))
			defer apTestPut(t, f.Root, f.Checkpoint, before)
			cp := semMap(mustParseContract(before))
			switch variant {
			case "missing-professional-review":
				delete(semMap(cp["checks"]), "check.design-reviewed")
			case "missing-fresh-record":
				semMap(semMap(semMap(cp["gates"])["gate.fresh-verification-passed"])["evidence"])["evidence.fresh-verification"] = []any{}
			case "wrong-current-slice":
				semMap(semMap(cp["human_review"])["implementation"])["slice_contract_ref"] = "other-slice.yaml"
			case "wrong-current-profile":
				cp["profile_id"] = "harness.frontend-delivery"
			}
			apTestPut(t, f.Root, f.Checkpoint, cp)
			if _, err := check(); err == nil {
				t.Fatal("wrong or incomplete native Backend responsibilities accepted")
			}
		})
	}
}
