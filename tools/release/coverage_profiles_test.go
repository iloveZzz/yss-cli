package release

import (
	"reflect"
	"testing"
)

func TestRequiredCoverageOnlySupportedProfiles(t *testing.T) {
	if got := RequiredGateCoverage("cli-integration"); !reflect.DeepEqual(got, []string{"cli.integration.spec", "cli.integration.backend"}) {
		t.Fatal(got)
	}
}

func TestProgramCoverageRequiresReinstallRatherThanLegacyRecovery(t *testing.T) {
	want := []string{"install-plan-read-only", "install-apply", "reinstall", "user-file-preservation"}
	if got := RequiredGateCoverage("program-installation"); !reflect.DeepEqual(got, want) {
		t.Fatalf("program coverage = %v, want %v", got, want)
	}
}

func TestReleaseScopesDoNotRequireRetiredMigrationEvidence(t *testing.T) {
	for scope, want := range map[string][]string{
		LocalQualificationScope: {"program-installation", "scoped-template-consumers"},
		FullQualificationScope:  {"full-template-integration", "cli-integration"},
	} {
		got, err := scopedChecks(scope)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s = %v, %v", scope, got, err)
		}
	}
	if got := RequiredGateCoverage("legacy-recovery"); got != nil {
		t.Fatalf("retired evidence still required: %v", got)
	}
}

func TestSyncEvidenceRequiresCommittedGitBaseline(t *testing.T) {
	row := func(command string, args ...string) any {
		values := make([]any, len(args))
		for i, arg := range args {
			values[i] = arg
		}
		return map[string]any{"command": command, "cwd": "/fixture", "args": values}
	}
	commands := []any{
		row("git", "init"), row("git", "-c", "user.name=Test", "commit"),
		row("yss", "sync", "--root", "/fixture", "--plan"), row("yss", "sync", "--root", "/fixture", "--apply"),
		row("yss", "sync", "--root", "/fixture", "--plan"), row("yss", "sync", "--root", "/fixture", "--apply"),
		row("yss", "sync", "--root", "/fixture", "--plan"),
	}
	if err := nativeSyncCommands(commands, "yss"); err != nil {
		t.Fatal(err)
	}
	if err := nativeSyncCommands(commands[1:], "yss"); err == nil {
		t.Fatal("missing git init accepted")
	}
	if err := nativeSyncCommands(append(commands, row("yss", "rollback", "--apply")), "yss"); err == nil {
		t.Fatal("sync rollback accepted")
	}
	bad := append([]any(nil), commands...)
	bad[2] = row("yss", "sync", "--root", "/different", "--plan")
	if err := nativeSyncCommands(bad, "yss"); err == nil {
		t.Fatal("mismatched root accepted")
	}
}

func TestSourceManifestAcceptsExactlySupportedProfiles(t *testing.T) {
	bundle := BundleIdentity{TemplateVersion: "5.0.0", TemplateCommit: "template", SourceSnapshotHash: "snapshot", ManifestHash: "manifest", BundleHash: "bundle"}
	r := &reader{identity: Identity{CLIVersion: "3.0.0", CLICommit: "cli", Bundles: map[string]BundleIdentity{"spec": bundle, "backend": bundle}}}
	row := func(profile string) any {
		return map[string]any{"family": profile, "namespace": "candidate-release", "package_name": "yss", "cli_commit": "cli", "core_commit": "template", "template_commit": "template", "version": "3.0.0", "template_version": "5.0.0", "source_contract_version": float64(2), "protocol_version": float64(1), "snapshot_hash": "snapshot", "manifest_hash": "manifest", "bundle_hash": "bundle", "binary_sha256": "binary"}
	}
	manifest := map[string]any{"schema_version": float64(2), "kind": "template-release-sources", "root_commit": "template", "families": []any{"spec", "backend"}, "entries": []any{row("spec"), row("backend")}}
	if err := r.sourceManifest(manifest, "binary"); err != nil {
		t.Fatal(err)
	}
	manifest["entries"] = append(manifest["entries"].([]any), row("spec"))
	if err := r.sourceManifest(manifest, "binary"); err == nil {
		t.Fatal("duplicate source accepted")
	}
}
