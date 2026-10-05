package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func legacySource(t *testing.T) string {
	t.Helper()
	source := os.Getenv("YSS_LEGACY_SOURCE")
	if source == "" {
		t.Skip("set YSS_LEGACY_SOURCE for fixed-source legacy oracle comparisons")
	}
	return source
}
func legacyInvocation(t *testing.T, source, alias string, args ...string) (int, []byte, string) {
	t.Helper()
	directory := map[string]string{"create-yss-spec": "create-yss-spec", "create-yss-harness-design": "create-yss-strategic-design", "create-yss-harness-backend": "create-yss-harness-backend", "create-yss-harness-frontend": "create-yss-harness-frontend"}[alias]
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	bin := filepath.Join(source, "submodules", directory, "bin", alias+".js")
	command := exec.CommandContext(ctx, "node", append([]string{bin}, args...)...)
	command.Dir = source
	var out, stderr bytes.Buffer
	command.Stdout = &out
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("legacy command timeout: %s %v", alias, args)
	}
	return code, out.Bytes(), stderr.String()
}
func jsonOrBytes(b []byte) any {
	var value any
	if json.Unmarshal(b, &value) == nil {
		return value
	}
	return string(b)
}

func TestFourLegacyEntryDifferentialContracts(t *testing.T) {
	source := legacySource(t)
	cases := [][]string{{"--help"}, {"--help", "--json"}, {"--version"}, {"--version", "--json"}, {"sync", "--json", "--unknown-option"}, {"sync", "--json", "--target-dir"}, {"migrate", "wat", "--json"}, {"migrate", "status", "--json", "--target-dir", ".", "--target-dir", "."}}
	for _, alias := range []string{"create-yss-spec", "create-yss-harness-design", "create-yss-harness-backend", "create-yss-harness-frontend"} {
		for index, args := range cases {
			t.Run(alias+"/"+string(rune('a'+index)), func(t *testing.T) {
				wantCode, wantOut, wantErr := legacyInvocation(t, source, alias, args...)
				gotCode, gotOut, gotErr := invoke(t, alias, args...)
				if gotCode != wantCode || !reflect.DeepEqual(jsonOrBytes(gotOut), jsonOrBytes(wantOut)) || gotErr != wantErr {
					t.Fatalf("parity mismatch args=%v\nold exit=%d stdout=%s stderr=%q\nGo exit=%d stdout=%s stderr=%q", args, wantCode, wantOut, wantErr, gotCode, gotOut, gotErr)
				}
				t.Logf("observed exit=%d JSON-or-byte parity", gotCode)
			})
		}
	}
}

func TestActualLegacySpecInitAndOptInNativeMigration(t *testing.T) {
	source := legacySource(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "legacy-spec")
	externalPlan := filepath.Join(base, "native-migrate-plan.json")
	started := time.Now()
	code, output, stderr := legacyInvocation(t, source, "create-yss-spec", "--target-dir", root, "--project-name", "Compat actual sample", "--business-domain", "Data", "--agent-runtime", "codex")
	if code != 0 {
		t.Fatalf("actual legacy init failed: %s %s", output, stderr)
	}
	t.Logf("actual legacy spec init exit 0: %s", time.Since(started))
	metadata := filepath.Join(root, ".yss-template.json")
	before, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatal(err)
	}
	code, output, stderr = legacyInvocation(t, source, "create-yss-spec", "sync", "--target-dir", root, "--json")
	if code != 0 {
		t.Fatalf("actual legacy sync preview failed: %s %s", output, stderr)
	}
	var legacyPlan map[string]any
	if json.Unmarshal(output, &legacyPlan) != nil || legacyPlan["operation"] != "sync" || legacyPlan["schemaVersion"] != float64(1) {
		t.Fatalf("legacy Plan v1: %s", output)
	}
	code, output, _ = invoke(t, "create-yss-spec", "sync", "--target-dir", root, "--json")
	if code != 1 || !bytes.Contains(output, []byte("YSS_UNPORTED")) {
		t.Fatalf("incompatible success schema must not be asserted: %s", output)
	}
	if after, _ := os.ReadFile(metadata); !bytes.Equal(before, after) {
		t.Fatal("unported call altered legacy metadata")
	}
	code, output, _ = invoke(t, "create-yss-spec", "--native", "diff", "--target-dir", root, "--json")
	if code != 0 {
		t.Fatalf("native read-only diff of actual old instance: %s", output)
	}
	code, output, _ = invoke(t, "create-yss-spec", "--native", "sync", "--target-dir", root, "--json")
	if code != 1 || !bytes.Contains(output, []byte("MIGRATION_REQUIRED")) {
		t.Fatalf("old instance sync must require migration: %s", output)
	}
	code, output, _ = invoke(t, "create-yss-spec", "--native", "migrate", "plan", "--target-dir", root, "--output", externalPlan, "--json")
	if code != 0 {
		t.Fatalf("actual native migrate plan: %s", output)
	}
	if after, _ := os.ReadFile(metadata); !bytes.Equal(before, after) {
		t.Fatal("migration preview altered old metadata")
	}
	code, output, _ = invoke(t, "create-yss-spec", "--native", "migrate", "apply", "--target-dir", root, "--plan", externalPlan, "--json")
	if code != 0 {
		t.Fatalf("actual native migrate apply: %s", output)
	}
	if after, _ := os.ReadFile(metadata); !bytes.Equal(before, after) {
		t.Fatal("migration failed to retain original legacy metadata bytes")
	}
	if _, err := os.Stat(filepath.Join(root, ".yss.json")); err != nil {
		t.Fatal(err)
	}
	code, output, _ = invoke(t, "create-yss-spec", "--native", "sync", "--target-dir", root, "--json")
	if code != 0 {
		t.Fatalf("native sync preview after actual migration: %s", output)
	}
	code, output, _ = invoke(t, "create-yss-spec", "--native", "migrate", "rollback", "--apply", "--target-dir", root, "--json")
	if code != 0 {
		t.Fatalf("native whole rollback: %s", output)
	}
	if _, err := os.Stat(filepath.Join(root, ".yss.json")); !os.IsNotExist(err) {
		t.Fatal("rollback retained native metadata")
	}
	if after, _ := os.ReadFile(metadata); !bytes.Equal(before, after) {
		t.Fatal("rollback altered original legacy metadata")
	}
	t.Logf("actual legacy init/sync and explicit Go diff/migrate/apply/sync/rollback sample completed: %s", time.Since(started))
}

func TestActualLegacySpecialistMigrationAndWholeRollback(t *testing.T) {
	source := legacySource(t)
	for _, side := range []string{"design", "backend", "frontend"} {
		t.Run(side, func(t *testing.T) {
			base, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			root := filepath.Join(base, "legacy-"+side)
			alias := "create-yss-harness-" + side
			code, out, stderr := legacyInvocation(t, source, alias, "init", "--target-dir", root, "--project-name", "Actual migration", "--business-domain", "Data", "--team-size", "1", "--json")
			if code != 0 {
				t.Fatalf("legacy init: %s %s", out, stderr)
			}
			metadata := filepath.Join(root, ".yss-harness-"+side+".json")
			before, e := os.ReadFile(metadata)
			if e != nil {
				t.Fatal(e)
			}
			contextBefore, e := os.ReadFile(filepath.Join(root, "CONTEXT.md"))
			if e != nil {
				t.Fatal(e)
			}
			if e = os.MkdirAll(filepath.Join(root, "src"), 0755); e != nil {
				t.Fatal(e)
			}
			business := []byte("business asset remains user-owned")
			if e = os.WriteFile(filepath.Join(root, "src", "asset.txt"), business, 0644); e != nil {
				t.Fatal(e)
			}
			plan := filepath.Join(base, "migrate.json")
			for _, args := range [][]string{{"--native", "doctor", "--target-dir", root, "--json"}, {"--native", "migrate", "plan", "--target-dir", root, "--output", plan, "--json"}, {"--native", "migrate", "apply", "--target-dir", root, "--plan", plan, "--json"}, {"--native", "sync", "--target-dir", root, "--json"}, {"--native", "migrate", "rollback", "--apply", "--target-dir", root, "--json"}} {
				code, out, stderr = invoke(t, alias, args...)
				if code != 0 {
					t.Fatalf("native %v: %s %s", args, out, stderr)
				}
			}
			for file, want := range map[string][]byte{metadata: before, filepath.Join(root, "CONTEXT.md"): contextBefore, filepath.Join(root, "src", "asset.txt"): business} {
				got, e := os.ReadFile(file)
				if e != nil || !bytes.Equal(got, want) {
					t.Fatalf("protected migration bytes changed: %s", file)
				}
			}
			if _, e = os.Stat(filepath.Join(root, ".yss.json")); !os.IsNotExist(e) {
				t.Fatal("whole migration rollback kept native metadata")
			}
		})
	}
}
