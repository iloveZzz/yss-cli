package bundle

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestEmbeddedLifecycleProfilesKeepTheirContracts(t *testing.T) {
	contracts := map[string][]string{
		"design":   {"STRATEGIC_PROFILE_ID", "PROFILE_NEXT_ROUTES", "assertStrategicWorkUnitDecision", "export function validateTicketFormalization"},
		"backend":  {"export function validateSliceContractReadiness", "export function validateTechnicalDesignCompletion", `"work-unit.harness-entry": ["work-unit.technical-design"`},
		"frontend": {"export function validateSliceContractReadiness", `"work-unit.frontend-engineering-design"`},
	}
	for profile, required := range contracts {
		t.Run(profile, func(t *testing.T) {
			b, err := Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			for variant, files := range map[string]map[string]File{"full": b.Files, "initial": b.Initial} {
				data, err := base64.StdEncoding.DecodeString(files["scripts/lib/lifecycle-transition.mjs"].Data)
				if err != nil {
					t.Fatal(err)
				}
				for _, contract := range required {
					if !strings.Contains(string(data), contract) {
						t.Errorf("%s lost Profile contract: %s", variant, contract)
					}
				}
				if profile == "design" && strings.Contains(string(data), `from "./backend-review.mjs"`) {
					t.Errorf("%s expanded strategic lifecycle into backend review", variant)
				}
			}
		})
	}
}

func TestEmbeddedWorkLayoutConsumers(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			b, e := Load(profile)
			if e != nil {
				t.Fatal(e)
			}
			central, ok := b.Initial["scripts/lib/work-layout.mjs"]
			if !ok {
				t.Fatal("initial project lacks configured layout resolver")
			}
			raw, _ := base64.StdEncoding.DecodeString(central.Data)
			if !strings.Contains(string(raw), "tracker.root") {
				t.Fatal("missing tracker authority")
			}
			ignore, e := b.Initial[".gitignore"].Render(nil)
			if e != nil || strings.Contains(string(ignore), "# Template-source only") || strings.Contains("\n"+string(ignore), "\n.work/\n") {
				t.Fatalf("source-only work ignore distributed: %s %v", ignore, e)
			}
			for ref, f := range b.Files {
				if strings.HasPrefix(ref, ".work/") {
					t.Fatal("instance assets distributed: " + ref)
				}
				if strings.HasSuffix(ref, "/stage-tracking.mjs") || strings.HasSuffix(ref, "/reading-view-policy.mjs") || strings.HasSuffix(ref, "/offline-html.mjs") {
					raw, _ := base64.StdEncoding.DecodeString(f.Data)
					if !strings.Contains(string(raw), "work-layout.mjs") {
						t.Fatal("unadapted production consumer: " + ref)
					}
				}
			}
			found := false
			for _, tr := range b.NativeTransforms {
				if tr.Path == "scripts/lib/work-layout.mjs" {
					found = true
					if !tr.SourceAbsent {
						t.Fatal("new compatibility module lost source absence provenance")
					}
				}
			}
			if !found {
				t.Fatal("layout module generator provenance absent")
			}
		})
	}
}
