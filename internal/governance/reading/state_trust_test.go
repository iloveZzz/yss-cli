//go:build !yss_nobundle

package reading_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/governance/reading"
)

func TestEnabledReadingStateCanBeReadFromTrustedInstallation(t *testing.T) {
	root := installQueryProject(t)
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	if err := s.Authorities(); err != nil {
		t.Fatal(err)
	}
	const cp = ".work/demo/checkpoint.yaml"
	after, _, _, _, err := reading.EnablePolicy(s, cp)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, reading.PolicyRef)
	if err = os.WriteFile(path, after, 0644); err != nil {
		t.Fatal(err)
	}
	s = kernel.NewSemanticSession(context.Background(), root, nil)
	if err = s.Authorities(); err != nil {
		t.Fatal(err)
	}
	policy, present, err := reading.LoadPolicy(s)
	if err != nil || !present || policy["mode"] != "managed" || !kernel.SemHas(policy["checkpoints"], cp) {
		t.Fatalf("enabled state not readable: %v, %v", policy, err)
	}
}

func TestReadingStateUsesTrustedSchema(t *testing.T) {
	root := installQueryProject(t)
	writeQueryFile(t, root, reading.PolicyRef, `{"schema_version":1,"mode":"unrecognized","checkpoints":[]}`)
	newSession := func() *kernel.SemanticSession {
		s := kernel.NewSemanticSession(context.Background(), root, nil)
		if err := s.Authorities(); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if _, _, err := reading.LoadPolicy(newSession()); err == nil {
		t.Fatal("invalid reading state accepted")
	}
	writeQueryFile(t, root, ".template-spec/process/schemas/reading-policy.schema.json", `{"type":"object"}`)
	if _, _, err := reading.LoadPolicy(newSession()); err == nil {
		t.Fatal("tampered reading schema accepted")
	}
}

func TestRequiredReadingCannotBeDisabledByManualMode(t *testing.T) {
	root := installQueryProject(t)
	writeQueryFile(t, root, reading.PolicyRef, `{"schema_version":1,"mode":"manual","checkpoints":[]}`)
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	if err := s.Authorities(); err != nil {
		t.Fatal(err)
	}
	report, err := reading.CheckViews(s, ".work/demo/checkpoint.yaml", true)
	if err == nil || report["status"] != "stale" {
		t.Fatalf("required view skipped actual check: %v, %v", report, err)
	}
}

func TestReadingStateDetectsConcurrentChanges(t *testing.T) {
	root := installQueryProject(t)
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	if err := s.Authorities(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reading.LoadPolicy(s); err != nil {
		t.Fatal(err)
	}
	changed := `{"schema_version":1,"mode":"manual","checkpoints":[".work/demo/checkpoint.yaml"]}`
	writeQueryFile(t, root, reading.PolicyRef, changed)
	if err := s.FinishInputs(); err == nil {
		t.Fatal("concurrent state change was not detected")
	}
	raw, err := os.ReadFile(filepath.Join(root, reading.PolicyRef))
	if err != nil || string(raw) != changed {
		t.Fatalf("user change was not preserved: %v", err)
	}
}

func TestRenderingRejectsInvalidReadingState(t *testing.T) {
	root := installQueryProject(t)
	writeQueryFile(t, root, reading.PolicyRef, `{"schema_version":1,"mode":"invalid","checkpoints":[]}`)
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	if err := s.Authorities(); err != nil {
		t.Fatal(err)
	}
	_, err := reading.BuildReadingMode(s, ".work/demo/checkpoint.yaml", true)
	if err == nil || !strings.Contains(err.Error(), "reading-policy.schema.json") {
		t.Fatalf("invalid state not checked before render: %v", err)
	}
}

func TestReadingEnableAndRenderOnRealInstallation(t *testing.T) {
	root := installQueryProject(t)
	const cp = ".work/demo/checkpoint.json"
	writeQueryFile(t, root, cp, `{"feature_id":"demo","schema_version":1,"repository_mode":"project-instance","mode":"route","status":"routing","stage":"stage.entry-triage","artifacts":{},"gates":{},"context_reconciliation":{"status":"pending","ref":null,"reason":"pending","evidence_refs":[]},"next_work_unit":"work-unit.plan-opportunity","phase_boundary":{"decision":"continue","reason":"initial routing"},"stage_trace":{"stage":"stage.entry-triage","upstream_refs":[],"artifact_refs":[],"gate_decisions":[],"downstream_impacts":[]},"ticket_sync":{"status":"pending","refs":[]},"verification":{"commands":[],"evidence_refs":[]},"human_review":{"user_decisions":[],"required_decisions":[],"not_applicable":[],"required":false,"status":"not-applicable"},"git_checkpoint":{"required":false,"status":"not-applicable"},"blockers":[],"rollback":[]}`)
	if _, err := governance.Run("reading", "plan-enable", root, map[string]string{"checkpoint": cp, "output": "reading-plan.json"}); err != nil {
		t.Fatalf("plan-enable: %v", err)
	}
	policyPath := filepath.Join(root, reading.PolicyRef)
	original, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"schema_version":1,"mode":"manual","checkpoints":[".work/other/checkpoint.yaml"]}`)
	if err = os.WriteFile(policyPath, changed, 0644); err != nil {
		t.Fatal(err)
	}
	writeQueryFile(t, root, ".work/demo/user-note.txt", "user data\n")
	if _, err = governance.Run("reading", "apply-enable", root, map[string]string{"arg0": "reading-plan.json"}); err == nil {
		t.Fatal("stale enable plan accepted")
	}
	current, err := os.ReadFile(policyPath)
	if err != nil || !bytes.Equal(current, changed) {
		t.Fatalf("concurrent policy change overwritten: %v", err)
	}
	note, err := os.ReadFile(filepath.Join(root, ".work/demo/user-note.txt"))
	if err != nil || string(note) != "user data\n" {
		t.Fatalf("user file changed: %v", err)
	}
	if err = os.WriteFile(policyPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := governance.Run("reading", "apply-enable", root, map[string]string{"arg0": "reading-plan.json"}); err != nil {
		t.Fatalf("apply-enable: %v", err)
	}
	if _, err := governance.Run("reading", "render", root, map[string]string{"checkpoint": cp}); err != nil {
		t.Fatalf("render: %v", err)
	}
}
