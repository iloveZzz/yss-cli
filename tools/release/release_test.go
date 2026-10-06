package release_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/iloveZzz/yss-cli/tools/release"
)

func TestStableAssemblyRejectsIncompleteNativeSet(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "input.json")
	raw, _ := json.Marshal(release.Input{SchemaVersion: 1})
	if err := os.WriteFile(p, raw, 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	err := release.Assemble(p, out, release.Expected{})
	if release.Code(err) != "PLATFORMS" {
		t.Fatalf("want PLATFORMS, got %v", err)
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatalf("rejected input created output: %v", err)
	}
}

var platforms = []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"}
var documents = []string{"docs/source-lock.json", "docs/compatibility.md", "docs/porting-status.md", "docs/native-governance.md", "docs/cli-retirement.md", "compat/README.md", "README.md"}
var fixtureOnce sync.Once
var fixtureBinaries = map[string][]byte{}
var fixtureError error

const commit = "1234567890123456789012345678901234567890"

func hash(b []byte) string  { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func pointer[T any](v T) *T { return &v }

// These cross-built tiny binaries are parser fixtures, never native-run evidence.
func binaries(t *testing.T) map[string][]byte {
	t.Helper()
	fixtureOnce.Do(func() {
		d, err := os.MkdirTemp("", "release-parser-fixture-")
		if err != nil {
			fixtureError = err
			return
		}
		defer os.RemoveAll(d)
		for ref, body := range map[string]string{"go.mod": "module github.com/iloveZzz/yss-cli\n\ngo 1.27.1\n", "internal/domain/source.go": "package domain\nvar BuildCommit string\nvar BuildSourceState string\n", "cmd/yss/main.go": "package main\nimport \"github.com/iloveZzz/yss-cli/internal/domain\"\nfunc main(){println(domain.BuildCommit,domain.BuildSourceState)}\n"} {
			p := filepath.Join(d, ref)
			_ = os.MkdirAll(filepath.Dir(p), 0755)
			if err = os.WriteFile(p, []byte(body), 0644); err != nil {
				fixtureError = err
				return
			}
		}
		for _, p := range platforms {
			parts := strings.Split(p, "/")
			bin := filepath.Join(d, "fixture-bin")
			c := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w -X github.com/iloveZzz/yss-cli/internal/domain.BuildCommit="+commit+" -X github.com/iloveZzz/yss-cli/internal/domain.BuildSourceState=committed", "-o", bin, "./cmd/yss")
			c.Dir = d
			c.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+parts[0], "GOARCH="+parts[1], "GOPROXY=off", "GOSUMDB=off")
			if raw, err := c.CombinedOutput(); err != nil {
				fixtureError = fmt.Errorf("parser fixture %s: %v %s", p, err, raw)
				return
			}
			fixtureBinaries[p], fixtureError = os.ReadFile(bin)
			if fixtureError != nil {
				return
			}
		}
	})
	if fixtureError != nil {
		t.Fatal(fixtureError)
	}
	return fixtureBinaries
}

type fixture struct {
	dir      string
	input    release.Input
	expected release.Expected
}

func (f *fixture) file(t *testing.T, ref string, raw []byte) release.FileRef {
	t.Helper()
	p := filepath.Join(f.dir, ref)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0644); err != nil {
		t.Fatal(err)
	}
	return release.FileRef{Path: ref, SHA256: hash(raw)}
}
func (f *fixture) json(t *testing.T, ref string, value any) release.FileRef {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return f.file(t, ref, append(raw, '\n'))
}
func (f *fixture) manifest(t *testing.T) string {
	t.Helper()
	return filepath.Join(f.dir, f.json(t, "input.json", f.input).Path)
}
func (f *fixture) checks(t *testing.T, prefix string, ids []string) []release.Check {
	t.Helper()
	rows := []release.Check{}
	for _, id := range ids {
		report := map[string]any{"syntheticParserFixture": true, "status": "passed"}
		if id == "native-smoke" {
			parts := strings.Split(prefix, "-")
			platform := parts[len(parts)-2] + "/" + parts[len(parts)-1]
			results := []map[string]any{}
			for _, cmd := range []string{"version", "capabilities"} {
				env := f.json(t, prefix+"/smoke-global.json", map[string]any{"outputVersion": 1, "protocolVersion": 1, "status": "ok", "code": "OK"})
				results = append(results, map[string]any{"profile": "", "command": []string{cmd}, "status": "ok", "code": "OK", "exitCode": 0, "envelopeRef": path.Base(env.Path), "sha256": env.SHA256})
			}
			for _, p := range []string{"spec", "design", "backend", "frontend"} {
				env := f.json(t, prefix+"/smoke-"+p+".json", map[string]any{"outputVersion": 1, "protocolVersion": 1, "status": "ok", "code": "OK"})
				for _, cmd := range [][]string{{"init"}, {"context", "verify"}, {"diff"}, {"doctor"}, {"lifecycle", "query"}, {"sync", "--plan"}, {"sync", "--apply"}, {"skills", "list"}, {"assets", "list"}, {"migrate", "status"}, {"runtime", "begin"}, {"runtime", "event"}, {"runtime", "complete"}, {"runtime", "run"}, {"runtime", "events"}, {"runtime", "commands"}, {"runtime", "pin"}, {"runtime", "pins"}, {"runtime", "unpin"}, {"runtime", "inspect"}} {
					results = append(results, map[string]any{"profile": p, "command": cmd, "status": "ok", "code": "OK", "exitCode": 0, "envelopeRef": path.Base(env.Path), "sha256": env.SHA256})
				}
			}
			report = map[string]any{"schemaVersion": 1, "platform": platform, "status": "passed", "binary_sha256": hash(binaries(t)[platform]), "input_drift": false, "unexecuted": []string{}, "results": results}
		}
		if id == "native-recovery" {
			parts := strings.Split(prefix, "-")
			platform := parts[len(parts)-2] + "/" + parts[len(parts)-1]
			kinds := []string{"cancel", "terminate", "kill"}
			if strings.HasPrefix(platform, "windows/") {
				kinds = []string{"cancel", "kill"}
			}
			cases := []map[string]any{}
			for _, p := range []string{"spec", "design", "backend", "frontend"} {
				for _, kind := range kinds {
					cases = append(cases, map[string]any{"profile": p, "case": kind, "status": "passed", "observed_after_target_mutation": true, "binding_and_identity_restored_together": true, "repeated_recovery": true, "child_exit": -1, "output_sha256": f.file(t, prefix+"/output-"+p+"-"+kind+"-apply.json", nil).SHA256})
				}
			}
			report = map[string]any{"schema_version": 1, "kind": "native-process-recovery", "platform": platform, "status": "passed", "binary_sha256": hash(binaries(t)[platform]), "input_drift": false, "unexecuted": []string{}, "cases": cases}
		}
		if id == "plugin-native-smoke" {
			parts := strings.Split(prefix, "-")
			platform := parts[len(parts)-2] + "/" + parts[len(parts)-1]
			osname := parts[len(parts)-2]
			if osname == "windows" {
				osname = "win32"
			}
			arch := parts[len(parts)-1]
			if arch == "amd64" {
				arch = "x64"
			}
			report = f.pluginEvidence(t, prefix, platform)
		}
		if prefix == "gates" {
			report = f.gateEvidence(t, id)
		}
		rows = append(rows, release.Check{ID: id, Status: "passed", ExitCode: pointer(0), InputDrift: pointer(false), Unexecuted: []string{}, Report: f.json(t, prefix+"/"+id+".json", report), Stdout: f.file(t, prefix+"/"+id+".stdout", []byte("synthetic parser fixture output\n")), Stderr: f.file(t, prefix+"/"+id+".stderr", nil)})
	}
	return rows
}

func TestStableAssemblyRejectsIncompleteSmokeSurface(t *testing.T) {
	f := newFixture(t)
	editReceipt(t, f, 0, func(r *release.Receipt) {
		editRaw(t, f, &r.Checks[0].Report, func(raw map[string]any) { raw["results"] = raw["results"].([]any)[1:] })
	})
	err := release.Assemble(f.manifest(t), filepath.Join(f.dir, "out"), f.expected)
	if release.Code(err) != "EVIDENCE" {
		t.Fatalf("missing actual version smoke command accepted: %v", err)
	}
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{dir: t.TempDir()}
	identity := release.Identity{CLIVersion: "1.0.0", ProtocolVersion: 1, CLICommit: commit, SourceState: "committed", Bundles: map[string]release.BundleIdentity{}}
	profiles := map[string]any{}
	for _, p := range []string{"spec", "design", "backend", "frontend"} {
		sha := hash([]byte(p))
		identity.Bundles[p] = release.BundleIdentity{TemplateVersion: "git:" + commit, TemplateCommit: commit, SourceState: "committed", SourceSnapshotHash: sha, ManifestHash: sha, BundleHash: sha}
		profiles[p] = map[string]string{"templateCommit": commit}
	}
	f.expected = release.Expected{Identity: identity, Documents: map[string][]byte{}, RequiredPlatforms: append([]string(nil), platforms...), QualificationScope: release.FullQualificationScope, TemplateRoot: "/fixed/template", VerificationInputSHA256: hash([]byte("fixed-template-input")), VerificationPlan: asMap(map[string]any{"effective_profile": "release", "source_requirement": "committed", "strategy": "legacy-full", "policy_digest": hash([]byte("policy")), "commands": []any{map[string]any{"id": "check.fixture", "task_id": "task.fixture", "command": "fixture check", "gate_ids": []string{"check.fixture"}}}, "gates": []any{map[string]any{"id": "check.fixture", "selected": true}}}), VerificationInvocation: asMap(map[string]any{"command": "/fixed/template/scripts/run-template-verification", "args": []string{"--profile", "release"}})}
	f.input = release.Input{SchemaVersion: 1, Identity: identity, Documents: map[string]release.FileRef{}, RequiredPlatforms: append([]string(nil), platforms...), QualificationScope: release.FullQualificationScope}
	for _, ref := range documents {
		raw := []byte("frozen test document " + ref + "\n")
		if ref == "docs/source-lock.json" {
			raw, _ = json.Marshal(map[string]any{"schemaVersion": 2, "profiles": profiles})
		}
		f.expected.Documents[ref] = raw
		f.input.Documents[ref] = f.file(t, "documents/"+ref, raw)
	}
	f.input.SourceLockSHA256 = hash(f.expected.Documents["docs/source-lock.json"])
	f.expected.SourceLockSHA256 = f.input.SourceLockSHA256
	for _, p := range platforms {
		prefix := "native-" + strings.ReplaceAll(p, "/", "-")
		bin := f.file(t, prefix+"/binary", binaries(t)[p])
		receipt := release.Receipt{SchemaVersion: 1, Identity: f.input.Identity, Platform: p, RuntimePlatform: p, RuntimeVerification: "native-runner", BinarySHA256: bin.SHA256, BinaryBeforeSHA256: bin.SHA256, BinaryAfterSHA256: bin.SHA256, Status: "passed", ExitCode: pointer(0), InputDrift: pointer(false), Unexecuted: []string{}, Checks: f.checks(t, prefix, []string{"native-smoke", "native-recovery", "plugin-native-smoke"})}
		receipt.Version = f.json(t, prefix+"/version.json", map[string]any{"outputVersion": 1, "protocolVersion": 1, "status": "ok", "code": "OK", "result": map[string]any{"version": "1.0.0", "protocolVersion": 1, "cliCommit": commit, "sourceState": "committed"}})
		receipt.GitHubRunID = "synthetic-parser-fixture"
		receipt.GitHubRunAttempt = "1"
		receipt.VersionCommand = &release.Check{ID: "native-version", Command: []string{"parser-fixture", "version", "--json"}, Status: "passed", ExitCode: pointer(0), InputDrift: pointer(false), Unexecuted: []string{}, Stdout: receipt.Version, Stderr: f.file(t, prefix+"/version.stderr.log", nil)}
		for _, profile := range []string{"spec", "design", "backend", "frontend"} {
			ref := f.json(t, prefix+"/inspect-"+profile+".json", map[string]any{"outputVersion": 1, "protocolVersion": 1, "status": "ok", "code": "OK", "result": completeFixtureInspection(f, profile)})
			receipt.BundleCommands = append(receipt.BundleCommands, release.Check{ID: "native-inspect-" + profile, Command: []string{"parser-fixture", "bundle", "inspect", "--profile", profile, "--json"}, Status: "passed", ExitCode: pointer(0), InputDrift: pointer(false), Unexecuted: []string{}, Report: ref, Stdout: ref, Stderr: f.file(t, prefix+"/inspect-"+profile+".stderr.log", nil)})
		}
		f.input.Artifacts = append(f.input.Artifacts, release.Artifact{Platform: p, Binary: bin, Receipt: f.json(t, prefix+"/receipt.json", receipt)})
	}
	gate := release.Receipt{SchemaVersion: 1, QualificationScope: release.FullQualificationScope, Identity: f.input.Identity, Status: "passed", ExitCode: pointer(0), InputDrift: pointer(false), Unexecuted: []string{}, Checks: f.checks(t, "gates", []string{"full-template-integration", "cli-integration", "legacy-recovery", "real-project-isolation"})}
	f.input.ReleaseGate = f.json(t, "gate.json", gate)
	return f
}
func TestStableAssemblyReusesAllSixBinaryBytesAndProducesDeterministicArchives(t *testing.T) {
	f := newFixture(t)
	p := f.manifest(t)
	out := filepath.Join(f.dir, "out")
	if err := release.Assemble(p, out, f.expected); err != nil {
		t.Fatal(err)
	}
	for _, platform := range platforms {
		parts := strings.Split(platform, "/")
		ext := ".tar.gz"
		bin := "yss"
		if parts[0] == "windows" {
			ext = ".zip"
			bin = "yss.exe"
		}
		path := filepath.Join(out, "yss_1.0.0_"+parts[0]+"_"+parts[1]+ext)
		files := readArchive(t, path)
		if !bytes.Equal(files[bin], binaries(t)[platform]) {
			t.Fatalf("%s binary bytes changed", platform)
		}
		for ref, raw := range f.expected.Documents {
			if !bytes.Equal(files[ref], raw) {
				t.Fatalf("%s document %s changed", platform, ref)
			}
		}
		var m map[string]any
		if err := json.Unmarshal(files["release-manifest.json"], &m); err != nil {
			t.Fatal(err)
		}
		if m["stableReady"] != true || m["runtimeVerification"] != "passed" || m["binarySha256"] != hash(binaries(t)[platform]) {
			t.Fatalf("unqualified release manifest %v", m)
		}
	}
	other := filepath.Join(f.dir, "repeat")
	if err := release.Assemble(p, other, f.expected); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(out)
	for _, entry := range entries {
		a, _ := os.ReadFile(filepath.Join(out, entry.Name()))
		b, _ := os.ReadFile(filepath.Join(other, entry.Name()))
		if !bytes.Equal(a, b) {
			t.Fatalf("nondeterministic %s", entry.Name())
		}
	}
}
func readArchive(t *testing.T, p string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if strings.HasSuffix(p, ".zip") {
		r, err := zip.OpenReader(p)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for _, f := range r.File {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			files[f.Name] = b
			if (f.Name == "yss.exe" && f.Mode().Perm() != 0755) || (f.Name != "yss.exe" && f.Mode().Perm() != 0644) {
				t.Fatalf("wrong zip mode %s: %o", f.Name, f.Mode())
			}
		}
		return files
	}
	r, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	g, err := gzip.NewReader(r)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	tr := tar.NewReader(g)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[h.Name] = b
		if (h.Name == "yss" && h.Mode != 0755) || (h.Name != "yss" && h.Mode != 0644) {
			t.Fatalf("wrong tar mode %s: %o", h.Name, h.Mode)
		}
	}
	return files
}

func TestStableAssemblyRejectsUntrustedInputsWithoutWriting(t *testing.T) {
	tests := []struct {
		name, code string
		change     func(*testing.T, *fixture)
	}{
		{"duplicate-platform", "PLATFORMS", func(t *testing.T, f *fixture) { f.input.Artifacts[1].Platform = f.input.Artifacts[0].Platform }},
		{"unknown-platform", "PLATFORMS", func(t *testing.T, f *fixture) { f.input.Artifacts[1].Platform = "linux/386" }},
		{"prerelease-version", "PROVENANCE", func(t *testing.T, f *fixture) { f.input.CLIVersion = "1.0.0-alpha.3" }},
		{"wrong-commit", "PROVENANCE", func(t *testing.T, f *fixture) { f.input.CLICommit = strings.Repeat("b", 40) }},
		{"working-tree", "PROVENANCE", func(t *testing.T, f *fixture) { f.input.SourceState = "working-tree" }},
		{"binary-hash", "HASH", func(t *testing.T, f *fixture) {
			a := f.input.Artifacts[0]
			_ = os.WriteFile(filepath.Join(f.dir, a.Binary.Path), []byte("replacement"), 0644)
		}},
		{"binary-wrong-platform", "BINARY", func(t *testing.T, f *fixture) {
			f.input.Artifacts[0].Binary = f.file(t, f.input.Artifacts[0].Binary.Path, binaries(t)["linux/amd64"])
		}},
		{"not-go-binary", "BINARY", func(t *testing.T, f *fixture) {
			f.input.Artifacts[0].Binary = f.file(t, f.input.Artifacts[0].Binary.Path, []byte("claimed executable"))
		}},
		{"missing-check", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.Checks = r.Checks[:2] })
		}},
		{"duplicate-check", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.Checks[1].ID = r.Checks[0].ID })
		}},
		{"missing-exit", "EVIDENCE", func(t *testing.T, f *fixture) { editReceipt(t, f, 0, func(r *release.Receipt) { r.ExitCode = nil }) }},
		{"missing-drift", "EVIDENCE", func(t *testing.T, f *fixture) { editReceipt(t, f, 0, func(r *release.Receipt) { r.InputDrift = nil }) }},
		{"drift", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.InputDrift = pointer(true) })
		}},
		{"failed-check", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.Checks[0].ExitCode = pointer(1) })
		}},
		{"unexecuted", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.Checks[0].Unexecuted = []string{"required-case"} })
		}},
		{"null-unexecuted", "EVIDENCE", func(t *testing.T, f *fixture) { editReceipt(t, f, 0, func(r *release.Receipt) { r.Unexecuted = nil }) }},
		{"crossbuild-receipt", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.RuntimeVerification = "crosscompile" })
		}},
		{"wrong-runtime-platform", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.RuntimePlatform = "linux/amd64" })
		}},
		{"binary-after-drift", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.BinaryAfterSHA256 = strings.Repeat("f", 64) })
		}},
		{"wrong-version-envelope", "PROVENANCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				r.Version = f.json(t, r.Version.Path, map[string]any{"outputVersion": 1, "protocolVersion": 1, "status": "ok", "code": "OK", "result": map[string]any{"version": "1.0.1", "protocolVersion": 1, "cliCommit": commit, "sourceState": "committed"}})
			})
		}},
		{"wrong-receipt-commit", "PROVENANCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.CLICommit = strings.Repeat("c", 40) })
		}},
		{"interrupted-version-command", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.VersionCommand.Signal = "SIGKILL" })
		}},
		{"missing-inspect-command", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.BundleCommands = r.BundleCommands[:3] })
		}},
		{"wrong-inspect-source", "PROVENANCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.BundleCommands[0].Report, func(raw map[string]any) { raw["result"].(map[string]any)["bundleHash"] = strings.Repeat("e", 64) })
				r.BundleCommands[0].Stdout = r.BundleCommands[0].Report
			})
		}},
		{"raw-recovery-drift", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[1].Report, func(raw map[string]any) { raw["input_drift"] = true })
			})
		}},
		{"raw-recovery-incomplete", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[1].Report, func(raw map[string]any) { raw["cases"] = raw["cases"].([]any)[:11] })
			})
		}},
		{"no-observed-target-mutation", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[1].Report, func(raw map[string]any) {
					raw["cases"].([]any)[0].(map[string]any)["observed_after_target_mutation"] = false
				})
			})
		}},
		{"raw-smoke-failure", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[0].Report, func(raw map[string]any) { raw["results"].([]any)[0].(map[string]any)["exitCode"] = 1 })
			})
		}},
		{"raw-plugin-drift", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[2].Report, func(raw map[string]any) { raw["binary_drift"] = true })
			})
		}},
		{"raw-plugin-interrupted", "EVIDENCE", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[2].Report, func(raw map[string]any) { raw["commands"].([]any)[0].(map[string]any)["signal"] = "SIGTERM" })
			})
		}},
		{"missing-release-gate", "PATH", func(t *testing.T, f *fixture) { f.input.ReleaseGate = release.FileRef{} }},
		{"incomplete-release-gate", "EVIDENCE", func(t *testing.T, f *fixture) { editGate(t, f, func(r *release.Receipt) { r.Checks = r.Checks[:3] }) }},
		{"failed-raw-release-gate", "EVIDENCE", func(t *testing.T, f *fixture) {
			editGate(t, f, func(r *release.Receipt) {
				editRaw(t, f, &r.Checks[0].Report, func(raw map[string]any) { raw["status"] = "failed" })
			})
		}},
		{"raw-release-not-execution", "SCHEMA", func(t *testing.T, f *fixture) {
			editGate(t, f, func(r *release.Receipt) {
				r.Checks[0].Report = f.json(t, r.Checks[0].Report.Path, map[string]any{"claim": "ready"})
			})
		}},
		{"wrong-report-hash", "HASH", func(t *testing.T, f *fixture) {
			editReceipt(t, f, 0, func(r *release.Receipt) { r.Checks[0].Report.SHA256 = strings.Repeat("d", 64) })
		}},
		{"document-source-mismatch", "PROVENANCE", func(t *testing.T, f *fixture) {
			ref := "README.md"
			f.input.Documents[ref] = f.file(t, f.input.Documents[ref].Path, []byte("different source"))
		}},
		{"path-traversal", "PATH", func(t *testing.T, f *fixture) { f.input.Artifacts[0].Binary.Path = "../outside" }},
		{"absolute-path", "PATH", func(t *testing.T, f *fixture) {
			f.input.Artifacts[0].Binary.Path = filepath.Join(f.dir, f.input.Artifacts[0].Binary.Path)
		}},
		{"symlink-file", "PATH", func(t *testing.T, f *fixture) {
			a := f.input.Artifacts[0].Binary
			source := filepath.Join(f.dir, a.Path)
			other := source + "-real"
			if err := os.Rename(source, other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, source); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink-directory", "PATH", func(t *testing.T, f *fixture) {
			a := f.input.Artifacts[0].Binary
			p := filepath.Join(f.dir, path.Dir(a.Path))
			other := p + "-real"
			if err := os.Rename(p, other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, p); err != nil {
				t.Fatal(err)
			}
		}},
		{"repository-output", "PATH", func(t *testing.T, f *fixture) { f.expected.RepositoryRoot = f.dir }},
		{"existing-output", "EXISTS", func(t *testing.T, f *fixture) {
			p := filepath.Join(f.dir, "out")
			if err := os.Mkdir(p, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(p, "user.txt"), []byte("must survive"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.change(t, f)
			p := f.manifest(t)
			before := inventory(t, f.dir)
			err := release.Assemble(p, filepath.Join(f.dir, "out"), f.expected)
			if release.Code(err) != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
			after := inventory(t, f.dir)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejection modified input/output inventory")
			}
		})
	}
}
func editReceipt(t *testing.T, f *fixture, index int, edit func(*release.Receipt)) {
	t.Helper()
	ref := f.input.Artifacts[index].Receipt
	raw, err := os.ReadFile(filepath.Join(f.dir, ref.Path))
	if err != nil {
		t.Fatal(err)
	}
	var receipt release.Receipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	edit(&receipt)
	f.input.Artifacts[index].Receipt = f.json(t, ref.Path, receipt)
}
func editGate(t *testing.T, f *fixture, edit func(*release.Receipt)) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, f.input.ReleaseGate.Path))
	if err != nil {
		t.Fatal(err)
	}
	var receipt release.Receipt
	if err = json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	edit(&receipt)
	f.input.ReleaseGate = f.json(t, f.input.ReleaseGate.Path, receipt)
}
func editRaw(t *testing.T, f *fixture, ref *release.FileRef, edit func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, ref.Path))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err = json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	edit(obj)
	*ref = f.json(t, ref.Path, obj)
}
func inventory(t *testing.T, root string) map[string]string {
	t.Helper()
	rows := map[string]string{}
	err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		ref, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		desc := fmt.Sprintf("%v:%o", info.Mode().Type(), info.Mode().Perm())
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			desc += ":" + hash(b)
		} else if info.Mode()&os.ModeSymlink != 0 {
			s, err := os.Readlink(p)
			if err != nil {
				return err
			}
			desc += ":" + s
		}
		rows[ref] = desc
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
