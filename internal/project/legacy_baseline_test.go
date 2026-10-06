package project

import (
	"encoding/json"
	"errors"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"testing"
)

func TestLegacyBaselinePoliciesNeverAuthorizeUnknownOrNativeInputs(t *testing.T) {
	var policy legacyBaselinePolicy
	if err := json.Unmarshal(legacyPolicyBytes, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.SchemaVersion != 1 || len(policy.Files) != 5 || policy.GeneratedLock == nil || policy.GeneratedLock.Ref != "skills-lock.json" || len(policy.GeneratedLock.InstalledSkills) != 42 {
		t.Fatal("invalid fixed policy")
	}
	for _, name := range []string{"unknown", "native", "wrong-family", "wrong-source", "not-migration"} {
		t.Run(name, func(t *testing.T) {
			id := &Identity{Root: freshRoot(t), Profile: domain.Profiles["spec"], Legacy: map[string]any{"cliVersion": policy.CLIVersion, "templateCommit": policy.TemplateCommit, "snapshotHash": policy.SnapshotHash}}
			b := &Binding{LegacyBaselinePolicy: policy.ID}
			command := "migrate"
			switch name {
			case "unknown":
				b.LegacyBaselinePolicy = "project-supplied-policy"
			case "native":
				id.Native = &Metadata{}
			case "wrong-family":
				id.Profile = domain.Profiles["design"]
			case "wrong-source":
				id.Legacy["templateCommit"] = "different"
			case "not-migration":
				command = "sync"
			}
			err := applyLegacyBaseline(id, command, b, map[string]Managed{}, map[string]domain.Descriptor{}, map[string]bool{})
			if err == nil {
				t.Fatal("untrusted baseline accepted")
			}
			var failure *domain.Error
			if !errors.As(err, &failure) || failure.Code != "LEGACY_POLICY" {
				t.Fatalf("wrong refusal: %v", err)
			}
		})
	}
}
